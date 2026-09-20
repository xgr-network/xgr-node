package interchain

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	"github.com/coinbase/kryptology/pkg/signatures/bls/bls_sig"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

const (
	DomainV2                     = "XGR_INTERCHAIN_V2"
	BootstrapDomainV1            = "XGR_INTERCHAIN_BOOTSTRAP_V1"
	CheckpointDomainV1           = "XGR_INTERCHAIN_CHECKPOINT_V1"
	BLSPublicKeyCompressedLength = 48
	BLSPublicKeyEIP2537Length    = 128
)

type Action uint8

const (
	ActionAddValidator    Action = 1
	ActionRemoveValidator Action = 2
)

var (
	ErrEmptyValidatorSet = errors.New("interchain validator set is empty")
	ErrQuorumNotReached  = errors.New("interchain quorum not reached")
	ErrDuplicateSigner   = errors.New("duplicate interchain signer")
	ErrSignerOutOfRange  = errors.New("interchain signer index out of range")
)

type MembershipPayload struct {
	OriginChainID     uint64
	DestinationDomain uint32
	// SetID is the expected current destination validator-set version.
	// A destination MUST reject a transition whose SetID differs from its
	// current set ID and MUST increment the set ID after a successful transition.
	SetID        uint64
	ValidUntil   uint64
	Action       Action
	Validator           types.Address
	BLSPublicKey        []byte
	BLSPublicKeyEIP2537 []byte
}

func MarshalBootstrapPayload(
	originChainID uint64,
	destinationDomain uint32,
	validator types.Address,
	blsPublicKey []byte,
	blsPublicKeyEIP2537 []byte,
) ([]byte, error) {
	if originChainID == 0 {
		return nil, fmt.Errorf("origin chain id must be non-zero")
	}
	if destinationDomain == 0 {
		return nil, fmt.Errorf("destination domain must be non-zero")
	}
	if validator == types.ZeroAddress {
		return nil, fmt.Errorf("validator address must be non-zero")
	}
	if len(blsPublicKey) != BLSPublicKeyCompressedLength {
		return nil, fmt.Errorf("validator BLS public key must be %d bytes, got %d", BLSPublicKeyCompressedLength, len(blsPublicKey))
	}
	if len(blsPublicKeyEIP2537) != BLSPublicKeyEIP2537Length {
		return nil, fmt.Errorf("validator EIP-2537 BLS public key must be %d bytes, got %d", BLSPublicKeyEIP2537Length, len(blsPublicKeyEIP2537))
	}
	expectedEIP2537, err := crypto.BLSPublicKeyToEIP2537(blsPublicKey)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(expectedEIP2537, blsPublicKeyEIP2537) {
		return nil, fmt.Errorf("validator EIP-2537 BLS public key does not match canonical XGR key")
	}

	var buf bytes.Buffer
	buf.WriteString(BootstrapDomainV1)
	_ = binary.Write(&buf, binary.BigEndian, originChainID)
	_ = binary.Write(&buf, binary.BigEndian, destinationDomain)
	buf.Write(validator.Bytes())
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(blsPublicKey)))
	buf.Write(blsPublicKey)
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(blsPublicKeyEIP2537)))
	buf.Write(blsPublicKeyEIP2537)

	return buf.Bytes(), nil
}

type CheckpointPayload struct {
	OriginChainID     uint64
	DestinationDomain uint32
	SetID             uint64
	Mailbox           types.Address
	MerkleTreeHook    types.Address
	Root              types.Hash
	Index             uint32
}

func (p CheckpointPayload) MarshalBinary() ([]byte, error) {
	if p.OriginChainID == 0 || p.DestinationDomain == 0 || p.SetID == 0 {
		return nil, fmt.Errorf("checkpoint origin, destination, and set id must be non-zero")
	}
	if p.Mailbox == types.ZeroAddress || p.MerkleTreeHook == types.ZeroAddress {
		return nil, fmt.Errorf("checkpoint mailbox and merkle tree hook must be non-zero")
	}
	if p.Root == types.ZeroHash {
		return nil, fmt.Errorf("checkpoint root must be non-zero")
	}
	var buf bytes.Buffer
	buf.WriteString(CheckpointDomainV1)
	_ = binary.Write(&buf, binary.BigEndian, p.OriginChainID)
	_ = binary.Write(&buf, binary.BigEndian, p.DestinationDomain)
	_ = binary.Write(&buf, binary.BigEndian, p.SetID)
	buf.Write(p.Mailbox.Bytes())
	buf.Write(p.MerkleTreeHook.Bytes())
	buf.Write(p.Root.Bytes())
	_ = binary.Write(&buf, binary.BigEndian, p.Index)
	return buf.Bytes(), nil
}

func (p *CheckpointPayload) UnmarshalBinary(raw []byte) error {
	if p == nil {
		return fmt.Errorf("checkpoint payload is nil")
	}
	const tail = 8 + 4 + 8 + types.AddressLength + types.AddressLength + types.HashLength + 4
	if len(raw) != len(CheckpointDomainV1)+tail {
		return fmt.Errorf("invalid checkpoint payload length %d", len(raw))
	}
	if string(raw[:len(CheckpointDomainV1)]) != CheckpointDomainV1 {
		return fmt.Errorf("invalid checkpoint domain")
	}
	o := len(CheckpointDomainV1)
	p.OriginChainID = binary.BigEndian.Uint64(raw[o:o+8]); o += 8
	p.DestinationDomain = binary.BigEndian.Uint32(raw[o:o+4]); o += 4
	p.SetID = binary.BigEndian.Uint64(raw[o:o+8]); o += 8
	p.Mailbox = types.BytesToAddress(raw[o:o+types.AddressLength]); o += types.AddressLength
	p.MerkleTreeHook = types.BytesToAddress(raw[o:o+types.AddressLength]); o += types.AddressLength
	p.Root = types.BytesToHash(raw[o:o+types.HashLength]); o += types.HashLength
	p.Index = binary.BigEndian.Uint32(raw[o:o+4])
	_, err := p.MarshalBinary()
	return err
}

func (p CheckpointPayload) Hash() (types.Hash, error) {
	raw, err := p.MarshalBinary()
	if err != nil {
		return types.ZeroHash, err
	}
	return crypto.Keccak256Hash(raw), nil
}

type Vote struct {
	ValidatorIndex int
	Signature      []byte
}

func QuorumThreshold(validatorCount int) (int, error) {
	if validatorCount <= 0 {
		return 0, ErrEmptyValidatorSet
	}

	return (2*validatorCount + 2) / 3, nil
}

func (p MembershipPayload) MarshalBinary() ([]byte, error) {
	if p.OriginChainID == 0 {
		return nil, fmt.Errorf("origin chain id must be non-zero")
	}
	if p.DestinationDomain == 0 {
		return nil, fmt.Errorf("destination domain must be non-zero")
	}
	if p.ValidUntil == 0 {
		return nil, fmt.Errorf("membership valid-until must be non-zero")
	}
	if p.Action != ActionAddValidator && p.Action != ActionRemoveValidator {
		return nil, fmt.Errorf("unsupported membership action %d", p.Action)
	}
	if p.Validator == types.ZeroAddress {
		return nil, fmt.Errorf("validator address must be non-zero")
	}
	if len(p.BLSPublicKey) != BLSPublicKeyCompressedLength {
		return nil, fmt.Errorf("validator BLS public key must be %d bytes, got %d", BLSPublicKeyCompressedLength, len(p.BLSPublicKey))
	}
	if len(p.BLSPublicKeyEIP2537) != BLSPublicKeyEIP2537Length {
		return nil, fmt.Errorf("validator EIP-2537 BLS public key must be %d bytes, got %d", BLSPublicKeyEIP2537Length, len(p.BLSPublicKeyEIP2537))
	}
	expectedEIP2537, err := crypto.BLSPublicKeyToEIP2537(p.BLSPublicKey)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(expectedEIP2537, p.BLSPublicKeyEIP2537) {
		return nil, fmt.Errorf("validator EIP-2537 BLS public key does not match canonical XGR key")
	}

	var buf bytes.Buffer
	buf.WriteString(DomainV2)
	_ = binary.Write(&buf, binary.BigEndian, p.OriginChainID)
	_ = binary.Write(&buf, binary.BigEndian, p.DestinationDomain)
	_ = binary.Write(&buf, binary.BigEndian, p.SetID)
	_ = binary.Write(&buf, binary.BigEndian, p.ValidUntil)
	buf.WriteByte(byte(p.Action))
	buf.Write(p.Validator.Bytes())
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(p.BLSPublicKey)))
	buf.Write(p.BLSPublicKey)
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(p.BLSPublicKeyEIP2537)))
	buf.Write(p.BLSPublicKeyEIP2537)

	return buf.Bytes(), nil
}

func (p *MembershipPayload) UnmarshalBinary(raw []byte) error {
	if p == nil {
		return fmt.Errorf("membership payload is nil")
	}

	const fixedTail = 8 + 4 + 8 + 8 + 1 + types.AddressLength + 2 + BLSPublicKeyCompressedLength + 2 + BLSPublicKeyEIP2537Length
	if len(raw) < len(DomainV2)+fixedTail {
		return fmt.Errorf("membership payload too short: %d", len(raw))
	}
	if string(raw[:len(DomainV2)]) != DomainV2 {
		return fmt.Errorf("invalid interchain domain")
	}

	offset := len(DomainV2)
	p.OriginChainID = binary.BigEndian.Uint64(raw[offset : offset+8])
	offset += 8
	p.DestinationDomain = binary.BigEndian.Uint32(raw[offset : offset+4])
	offset += 4
	p.SetID = binary.BigEndian.Uint64(raw[offset : offset+8])
	offset += 8
	p.ValidUntil = binary.BigEndian.Uint64(raw[offset : offset+8])
	offset += 8
	p.Action = Action(raw[offset])
	offset++
	p.Validator = types.BytesToAddress(raw[offset : offset+types.AddressLength])
	offset += types.AddressLength

	keyLen := int(binary.BigEndian.Uint16(raw[offset : offset+2]))
	offset += 2
	if keyLen != BLSPublicKeyCompressedLength || offset+keyLen+2 > len(raw) {
		return fmt.Errorf("invalid validator BLS public key length %d", keyLen)
	}
	p.BLSPublicKey = append(p.BLSPublicKey[:0], raw[offset:offset+keyLen]...)
	offset += keyLen

	eipKeyLen := int(binary.BigEndian.Uint16(raw[offset : offset+2]))
	offset += 2
	if eipKeyLen != BLSPublicKeyEIP2537Length || offset+eipKeyLen != len(raw) {
		return fmt.Errorf("invalid validator EIP-2537 BLS public key length %d", eipKeyLen)
	}
	p.BLSPublicKeyEIP2537 = append(p.BLSPublicKeyEIP2537[:0], raw[offset:offset+eipKeyLen]...)

	_, err := p.MarshalBinary()
	return err
}

func (p MembershipPayload) Hash() (types.Hash, error) {
	raw, err := p.MarshalBinary()
	if err != nil {
		return types.ZeroHash, err
	}

	return crypto.Keccak256Hash(raw), nil
}

func VerifyQuorum(publicKeys [][]byte, votes []Vote, message []byte) error {
	threshold, err := QuorumThreshold(len(publicKeys))
	if err != nil {
		return err
	}

	if len(votes) < threshold {
		return fmt.Errorf("%w: have %d need %d", ErrQuorumNotReached, len(votes), threshold)
	}

	seen := make(map[int]struct{}, len(votes))
	valid := 0

	for _, vote := range votes {
		if vote.ValidatorIndex < 0 || vote.ValidatorIndex >= len(publicKeys) {
			return fmt.Errorf("%w: %d", ErrSignerOutOfRange, vote.ValidatorIndex)
		}
		if _, ok := seen[vote.ValidatorIndex]; ok {
			return fmt.Errorf("%w: %d", ErrDuplicateSigner, vote.ValidatorIndex)
		}
		seen[vote.ValidatorIndex] = struct{}{}

		if err := crypto.VerifyBLSSignatureFromBytes(publicKeys[vote.ValidatorIndex], vote.Signature, message); err != nil {
			return fmt.Errorf("invalid signature from interchain validator %d: %w", vote.ValidatorIndex, err)
		}
		valid++
	}

	if valid < threshold {
		return fmt.Errorf("%w: have %d need %d", ErrQuorumNotReached, valid, threshold)
	}

	return nil
}

// AggregateVotes verifies an unweighted interchain quorum, aggregates the individual
// Kryptology BLS signatures, and returns the signer bitmap using the same validator-index
// convention as XGR's existing BLS committed seals.
func AggregateVotes(publicKeys [][]byte, votes []Vote, message []byte) (*big.Int, []byte, error) {
	if err := VerifyQuorum(publicKeys, votes, message); err != nil {
		return nil, nil, err
	}

	bitmap := new(big.Int)
	signatures := make([]*bls_sig.Signature, 0, len(votes))
	for _, vote := range votes {
		sig, err := crypto.UnmarshalBLSSignature(vote.Signature)
		if err != nil {
			return nil, nil, fmt.Errorf("unmarshal interchain signature %d: %w", vote.ValidatorIndex, err)
		}
		signatures = append(signatures, sig)
		bitmap.SetBit(bitmap, vote.ValidatorIndex, 1)
	}

	aggregate, err := bls_sig.NewSigPop().AggregateSignatures(signatures...)
	if err != nil {
		return nil, nil, fmt.Errorf("aggregate interchain signatures: %w", err)
	}
	raw, err := aggregate.MarshalBinary()
	if err != nil {
		return nil, nil, fmt.Errorf("marshal interchain aggregate signature: %w", err)
	}

	return bitmap, raw, nil
}

// VerifyAggregatedQuorum performs the local preflight used before a destination
// transaction is submitted. The destination contract remains authoritative.
func VerifyAggregatedQuorum(publicKeys [][]byte, bitmap *big.Int, aggregateSignature, message []byte) error {
	threshold, err := QuorumThreshold(len(publicKeys))
	if err != nil {
		return err
	}
	if bitmap == nil || bitmap.Sign() <= 0 {
		return fmt.Errorf("%w: empty signer bitmap", ErrQuorumNotReached)
	}
	if bitmap.BitLen() > len(publicKeys) {
		return fmt.Errorf("%w: bitmap bit %d exceeds validator set size %d", ErrSignerOutOfRange, bitmap.BitLen()-1, len(publicKeys))
	}

	selected := make([]*bls_sig.PublicKey, 0, len(publicKeys))
	for idx, raw := range publicKeys {
		if bitmap.Bit(idx) == 0 {
			continue
		}
		pk, err := crypto.UnmarshalBLSPublicKey(raw)
		if err != nil {
			return fmt.Errorf("unmarshal interchain public key %d: %w", idx, err)
		}
		selected = append(selected, pk)
	}
	if len(selected) < threshold {
		return fmt.Errorf("%w: have %d need %d", ErrQuorumNotReached, len(selected), threshold)
	}

	aggregateKey, err := bls_sig.NewSigPop().AggregatePublicKeys(selected...)
	if err != nil {
		return fmt.Errorf("aggregate interchain public keys: %w", err)
	}
	aggregate := &bls_sig.MultiSignature{}
	if err := aggregate.UnmarshalBinary(aggregateSignature); err != nil {
		return fmt.Errorf("unmarshal interchain aggregate signature: %w", err)
	}
	ok, err := bls_sig.NewSigPop().VerifyMultiSignature(aggregateKey, message, aggregate)
	if err != nil {
		return err
	}
	if !ok {
		return crypto.ErrInvalidBLSSignature
	}

	return nil
}
