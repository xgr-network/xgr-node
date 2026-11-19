// Package authorization is the cryptographic boundary of the shared
// interchain protocol. The runtime owns evidence, validator eligibility,
// approval, deadlines and recovery; an explicitly selected adapter owns keys,
// canonical operation codecs, individual signatures and quorum proofs.
//
// No exported method signs arbitrary bytes. Each signing entry point accepts
// a typed protocol operation. ECDSA uses the existing consensus validator key;
// BLS uses the existing hex-encoded validator-bls key.
package authorization

import (
	"bytes"
	stdECDSA "crypto/ecdsa"
	"fmt"
	"math/big"
	"math/bits"
	"time"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	secp "github.com/xgr-network/xgr-node/interchain/ecdsa"
	"github.com/xgr-network/xgr-node/interchain/verification"
	"github.com/xgr-network/xgr-node/types"
)

const (
	SchemeBLS   = "bls"
	SchemeECDSA = "ecdsa"
)

// Adapter is selected from trusted chain configuration, never from a peer
// message, signature length, registry response or an unavailable precompile.
type Adapter struct{ scheme string }

func ForScheme(scheme string) (*Adapter, error) {
	if scheme != SchemeBLS && scheme != SchemeECDSA {
		return nil, fmt.Errorf("unsupported interchain signature scheme %q", scheme)
	}
	return &Adapter{scheme: scheme}, nil
}
func (a *Adapter) Scheme() string {
	if a == nil {
		return ""
	}
	return a.scheme
}
func (a *Adapter) DirectDomain() string {
	if a.Scheme() == SchemeBLS {
		return protocol.XITADirectMessageDomain
	}
	if a.Scheme() == SchemeECDSA {
		return secp.Domain
	}
	return ""
}
func (a *Adapter) valid() error {
	if a.Scheme() != SchemeBLS && a.Scheme() != SchemeECDSA {
		return fmt.Errorf("interchain signature adapter is not configured")
	}
	return nil
}

// Set is an ordered registry snapshot. PublicKeys are compressed BLS keys or
// exactly the corresponding 20-byte ECDSA addresses. Set order is canonical.
type Set struct {
	Validators []types.Address
	PublicKeys [][]byte
	SetID      uint64
}

type Keys struct {
	ECDSA *stdECDSA.PrivateKey
	BLS   []byte
}

type Vote = protocol.Vote

// Proof preserves each on-chain codec: BLS uses a minimal big-endian bitmap
// and compressed aggregate; ECDSA uses a fixed-size little-endian bitmap and
// ascending-index r||s||v signatures. The selected adapter is always required.
type Proof struct {
	Bitmap    []byte
	Signature []byte
}

// ExpandedSignature preserves the released BLS archive representation while
// keeping the runtime independent of BLS point encoding. ECDSA has one
// canonical packed representation and returns a copy of it unchanged.
func (a *Adapter) ExpandedSignature(proof Proof) ([]byte, error) {
	if err := a.valid(); err != nil {
		return nil, err
	}
	if a.scheme == SchemeBLS {
		return crypto.BLSSignatureToEIP2537(proof.Signature)
	}
	if len(proof.Signature) == 0 || len(proof.Signature)%secp.SignatureSize != 0 || len(proof.Signature) > secp.MaxValidators*secp.SignatureSize {
		return nil, fmt.Errorf("invalid packed ECDSA signature length")
	}
	return append([]byte(nil), proof.Signature...), nil
}

// Membership represents the same single-validator ADD/REMOVE lifecycle for
// both schemes. Registry is signed by ECDSA. The released V3 BLS wire has no
// registry field; its configured single-registry binding remains a runtime
// requirement and is not silently added to existing signatures.
type Membership struct {
	OriginChainID       uint64
	DestinationDomain   uint32
	Registry            types.Address
	ExpectedSetID       uint64
	ValidUntil          uint64
	Action              protocol.Action
	Validator           types.Address
	BLSPublicKey        []byte
	BLSPublicKeyEIP2537 []byte
}

func (m Membership) BLS() protocol.MembershipPayload {
	return protocol.MembershipPayload{OriginChainID: m.OriginChainID, DestinationDomain: m.DestinationDomain,
		SetID: m.ExpectedSetID, ValidUntil: m.ValidUntil, Action: m.Action, Validator: m.Validator,
		BLSPublicKey: m.BLSPublicKey, BLSPublicKeyEIP2537: m.BLSPublicKeyEIP2537}
}
func FromBLS(m protocol.MembershipPayload) Membership {
	return Membership{OriginChainID: m.OriginChainID, DestinationDomain: m.DestinationDomain,
		ExpectedSetID: m.SetID, ValidUntil: m.ValidUntil, Action: m.Action, Validator: m.Validator,
		BLSPublicKey: m.BLSPublicKey, BLSPublicKeyEIP2537: m.BLSPublicKeyEIP2537}
}
func (m Membership) ecdsa() secp.MembershipV5 {
	return secp.MembershipV5{OriginChainID: m.OriginChainID, DestinationDomain: m.DestinationDomain,
		Registry: m.Registry, ExpectedSetID: m.ExpectedSetID, ValidUntil: m.ValidUntil, Action: uint8(m.Action), Validator: m.Validator}
}

func (a *Adapter) ValidateSet(set Set) error {
	if err := a.valid(); err != nil {
		return err
	}
	n := len(set.Validators)
	if set.SetID == 0 || n == 0 || n != len(set.PublicKeys) {
		return fmt.Errorf("invalid interchain validator snapshot")
	}
	if a.scheme == SchemeECDSA && n > secp.MaxValidators {
		return fmt.Errorf("ECDSA validator set exceeds %d", secp.MaxValidators)
	}
	identities := make(map[types.Address]struct{}, n)
	keys := make(map[string]struct{}, n)
	for i, validator := range set.Validators {
		if validator == types.ZeroAddress {
			return fmt.Errorf("zero validator identity at index %d", i)
		}
		if _, ok := identities[validator]; ok {
			return fmt.Errorf("duplicate validator identity at index %d", i)
		}
		identities[validator] = struct{}{}
		key := set.PublicKeys[i]
		if a.scheme == SchemeECDSA {
			if !bytes.Equal(key, validator.Bytes()) {
				return fmt.Errorf("ECDSA verification key does not match validator %d", i)
			}
		} else {
			if len(key) != protocol.BLSPublicKeyCompressedLength {
				return fmt.Errorf("invalid BLS public key length at index %d", i)
			}
			if _, err := crypto.UnmarshalBLSPublicKey(key); err != nil {
				return fmt.Errorf("invalid BLS public key at index %d: %w", i, err)
			}
			if _, ok := keys[string(key)]; ok {
				return fmt.Errorf("duplicate BLS verification key at index %d", i)
			}
			keys[string(key)] = struct{}{}
		}
	}
	return nil
}
func (a *Adapter) checkSet(set Set, epoch uint64) error {
	if err := a.ValidateSet(set); err != nil {
		return err
	}
	if epoch != set.SetID {
		return fmt.Errorf("authorization set %d differs from validator snapshot %d", epoch, set.SetID)
	}
	return nil
}

func (a *Adapter) EncodeMembership(m Membership) ([]byte, error) {
	if err := a.valid(); err != nil {
		return nil, err
	}
	if a.scheme == SchemeBLS {
		return m.BLS().MarshalBinary()
	}
	if len(m.BLSPublicKey) != 0 || len(m.BLSPublicKeyEIP2537) != 0 {
		return nil, fmt.Errorf("ECDSA membership cannot contain BLS keys")
	}
	return secp.MembershipPayloadV5(m.ecdsa())
}
func (a *Adapter) DecodeMembership(raw []byte) (Membership, error) {
	if err := a.valid(); err != nil {
		return Membership{}, err
	}
	if a.scheme == SchemeBLS {
		var m protocol.MembershipPayload
		if err := m.UnmarshalBinary(raw); err != nil {
			return Membership{}, err
		}
		return FromBLS(m), nil
	}
	m, err := secp.ParseMembershipPayloadV5(raw)
	if err != nil {
		return Membership{}, err
	}
	return Membership{OriginChainID: m.OriginChainID, DestinationDomain: m.DestinationDomain, Registry: m.Registry,
		ExpectedSetID: m.ExpectedSetID, ValidUntil: m.ValidUntil, Action: protocol.Action(m.Action), Validator: m.Validator}, nil
}
func (a *Adapter) EncodeDirect(m protocol.XITADirectMessage) ([]byte, error) {
	if err := a.valid(); err != nil {
		return nil, err
	}
	return m.MarshalBinaryWithDomain(a.DirectDomain())
}
func (a *Adapter) DecodeDirect(raw []byte) (protocol.XITADirectMessage, error) {
	var m protocol.XITADirectMessage
	if err := a.valid(); err != nil {
		return m, err
	}
	if err := m.UnmarshalBinaryWithDomain(raw, a.DirectDomain()); err != nil {
		return protocol.XITADirectMessage{}, err
	}
	return m, nil
}

func signBLS(keys Keys, raw []byte) ([]byte, error) {
	key, err := crypto.BytesToBLSSecretKey(keys.BLS)
	if err != nil {
		return nil, fmt.Errorf("load local BLS signing key: %w", err)
	}
	return crypto.SignByBLS(key, raw)
}
func (a *Adapter) SignMembership(keys Keys, m Membership, now time.Time) ([]byte, error) {
	raw, err := a.EncodeMembership(m)
	if err != nil {
		return nil, err
	}
	unix := now.Unix()
	if unix < 0 || m.ValidUntil < uint64(unix) || m.ValidUntil-uint64(unix) > 600 {
		return nil, fmt.Errorf("stale or excessive membership deadline")
	}
	if a.scheme == SchemeBLS {
		return signBLS(keys, raw)
	}
	_, sig, err := secp.SignMembershipV5(keys.ECDSA, m.ecdsa(), now)
	return sig, err
}
func (a *Adapter) SignDirect(keys Keys, m protocol.XITADirectMessage) ([]byte, error) {
	raw, err := a.EncodeDirect(m)
	if err != nil {
		return nil, err
	}
	if a.scheme == SchemeBLS {
		return signBLS(keys, raw)
	}
	_, sig, err := secp.SignDirectV5(keys.ECDSA, m)
	return sig, err
}
func (a *Adapter) SignSourceFee(keys Keys, p protocol.ILNSourceFeeProposal) ([]byte, error) {
	if err := a.valid(); err != nil {
		return nil, err
	}
	raw, err := p.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if a.scheme == SchemeBLS {
		return signBLS(keys, raw)
	}
	_, sig, err := secp.SignSourceFeeV5(keys.ECDSA, p)
	return sig, err
}
func (a *Adapter) SignPossession(keys Keys, m Membership) ([]byte, error) {
	if err := a.valid(); err != nil {
		return nil, err
	}
	if m.Action != protocol.ActionAddValidator {
		return nil, fmt.Errorf("possession proof requires validator ADD")
	}
	if a.scheme == SchemeBLS {
		raw, err := protocol.MarshalBootstrapPayload(m.OriginChainID, m.DestinationDomain, m.Validator, m.BLSPublicKey, m.BLSPublicKeyEIP2537)
		if err != nil {
			return nil, err
		}
		sig, err := signBLS(keys, raw)
		if err != nil {
			return nil, err
		}
		if err = a.VerifyPossession(m, sig); err != nil {
			return nil, err
		}
		return sig, nil
	}
	if len(m.BLSPublicKey) != 0 || len(m.BLSPublicKeyEIP2537) != 0 {
		return nil, fmt.Errorf("ECDSA possession cannot contain BLS keys")
	}
	validator, _, sig, err := secp.SignPossessionV5(keys.ECDSA, m.OriginChainID, m.DestinationDomain, m.Registry)
	if err != nil {
		return nil, err
	}
	if validator != m.Validator {
		return nil, fmt.Errorf("possession signer is not the validator candidate")
	}
	return sig, nil
}
func (a *Adapter) VerifyPossession(m Membership, signature []byte) error {
	if err := a.valid(); err != nil {
		return err
	}
	if m.Action != protocol.ActionAddValidator {
		return fmt.Errorf("possession proof requires validator ADD")
	}
	if a.scheme == SchemeBLS {
		raw, err := protocol.MarshalBootstrapPayload(m.OriginChainID, m.DestinationDomain, m.Validator, m.BLSPublicKey, m.BLSPublicKeyEIP2537)
		if err != nil {
			return err
		}
		return crypto.VerifyBLSSignatureFromBytes(m.BLSPublicKey, signature, raw)
	}
	if len(m.BLSPublicKey) != 0 || len(m.BLSPublicKeyEIP2537) != 0 || !secp.VerifyPossessionV5(m.OriginChainID, m.DestinationDomain, m.Registry, m.Validator, signature) {
		return fmt.Errorf("invalid ECDSA possession proof")
	}
	return nil
}

func (a *Adapter) verifyVote(set Set, index int, epoch uint64, raw []byte, signature []byte, verifyECDSA func(types.Address) bool) error {
	if err := a.checkSet(set, epoch); err != nil {
		return err
	}
	if index < 0 || index >= len(set.Validators) {
		return protocol.ErrSignerOutOfRange
	}
	if a.scheme == SchemeBLS {
		return crypto.VerifyBLSSignatureFromBytes(set.PublicKeys[index], signature, raw)
	}
	if !verifyECDSA(set.Validators[index]) {
		return fmt.Errorf("invalid ECDSA vote at validator index %d", index)
	}
	return nil
}
func (a *Adapter) VerifyMembershipVote(set Set, index int, m Membership, signature []byte) error {
	raw, err := a.EncodeMembership(m)
	if err != nil {
		return err
	}
	return a.verifyVote(set, index, m.ExpectedSetID, raw, signature, func(signer types.Address) bool { return secp.VerifyMembershipVoteV5(m.ecdsa(), signer, signature) })
}
func (a *Adapter) VerifyDirectVote(set Set, index int, m protocol.XITADirectMessage, signature []byte) error {
	raw, err := a.EncodeDirect(m)
	if err != nil {
		return err
	}
	return a.verifyVote(set, index, m.SetID, raw, signature, func(signer types.Address) bool { return secp.VerifyDirectVoteV5(m, signer, signature) })
}
func (a *Adapter) VerifySourceFeeVote(set Set, index int, p protocol.ILNSourceFeeProposal, signature []byte) error {
	raw, err := p.MarshalBinary()
	if err != nil {
		return err
	}
	return a.verifyVote(set, index, p.SetID, raw, signature, func(signer types.Address) bool { return secp.VerifySourceFeeVoteV5(p, signer, signature) })
}

func ecdsaVotes(votes []Vote) []secp.VoteV5 {
	result := make([]secp.VoteV5, len(votes))
	for i, vote := range votes {
		result[i] = secp.VoteV5{ValidatorIndex: vote.ValidatorIndex, Signature: vote.Signature}
	}
	return result
}
func (a *Adapter) assemble(set Set, votes []Vote, epoch uint64, raw []byte, assembleECDSA func() ([]byte, []byte, error)) (Proof, error) {
	if err := a.checkSet(set, epoch); err != nil {
		return Proof{}, err
	}
	if a.scheme == SchemeBLS {
		bitmap, signature, err := protocol.AggregateVotes(set.PublicKeys, votes, raw)
		if err != nil {
			return Proof{}, err
		}
		return Proof{Bitmap: bitmap.Bytes(), Signature: signature}, nil
	}
	bitmap, signature, err := assembleECDSA()
	if err != nil {
		return Proof{}, err
	}
	return Proof{Bitmap: bitmap, Signature: signature}, nil
}
func (a *Adapter) AssembleMembershipQuorum(set Set, votes []Vote, m Membership) (Proof, error) {
	raw, err := a.EncodeMembership(m)
	if err != nil {
		return Proof{}, err
	}
	return a.assemble(set, votes, m.ExpectedSetID, raw, func() ([]byte, []byte, error) {
		return secp.AssembleMembershipQuorumV5(m.ecdsa(), set.Validators, ecdsaVotes(votes))
	})
}
func (a *Adapter) AssembleDirectQuorum(set Set, votes []Vote, m protocol.XITADirectMessage) (Proof, error) {
	raw, err := a.EncodeDirect(m)
	if err != nil {
		return Proof{}, err
	}
	return a.assemble(set, votes, m.SetID, raw, func() ([]byte, []byte, error) {
		return secp.AssembleDirectQuorumV5(m, set.Validators, ecdsaVotes(votes))
	})
}
func (a *Adapter) AssembleSourceFeeQuorum(set Set, votes []Vote, p protocol.ILNSourceFeeProposal) (Proof, error) {
	raw, err := p.MarshalBinary()
	if err != nil {
		return Proof{}, err
	}
	return a.assemble(set, votes, p.SetID, raw, func() ([]byte, []byte, error) {
		return secp.AssembleSourceFeeQuorumV5(p, set.Validators, ecdsaVotes(votes))
	})
}

// SignerCount validates bitmap and proof shape using the selected codec.
// It does not replace cryptographic Verify*Quorum or current-set checks.
func (a *Adapter) SignerCount(set Set, proof Proof) (int, error) {
	if err := a.ValidateSet(set); err != nil {
		return 0, err
	}
	n := len(set.Validators)
	bitmap := proof.Bitmap
	if a.scheme == SchemeBLS {
		if len(bitmap) == 0 || len(bitmap) > (n+7)/8 || bitmap[0] == 0 || new(big.Int).SetBytes(bitmap).BitLen() > n || len(proof.Signature) != verification.BLSSignatureCompressedLength {
			return 0, fmt.Errorf("invalid canonical BLS quorum proof")
		}
	} else {
		if len(bitmap) != (n+7)/8 || (n%8 != 0 && bitmap[len(bitmap)-1]>>uint(n%8) != 0) {
			return 0, fmt.Errorf("invalid canonical ECDSA bitmap")
		}
	}
	count := 0
	for _, b := range bitmap {
		count += bits.OnesCount8(b)
	}
	if a.scheme == SchemeECDSA && len(proof.Signature) != count*secp.SignatureSize {
		return 0, fmt.Errorf("ECDSA signature count does not match bitmap")
	}
	return count, nil
}
func (a *Adapter) verifyQuorum(set Set, epoch uint64, raw []byte, proof Proof, verifyECDSA func() bool) error {
	if err := a.checkSet(set, epoch); err != nil {
		return err
	}
	count, err := a.SignerCount(set, proof)
	if err != nil {
		return err
	}
	threshold, err := verification.QuorumThreshold(len(set.Validators))
	if err != nil {
		return err
	}
	if count < threshold {
		return protocol.ErrQuorumNotReached
	}
	if a.scheme == SchemeBLS {
		return verification.VerifyAggregatedQuorum(set.PublicKeys, new(big.Int).SetBytes(proof.Bitmap), proof.Signature, raw)
	}
	if !verifyECDSA() {
		return fmt.Errorf("invalid ECDSA quorum proof")
	}
	return nil
}
func (a *Adapter) VerifyMembershipQuorum(set Set, m Membership, proof Proof) error {
	raw, err := a.EncodeMembership(m)
	if err != nil {
		return err
	}
	return a.verifyQuorum(set, m.ExpectedSetID, raw, proof, func() bool {
		return secp.VerifyMembershipQuorumV5(m.ecdsa(), set.Validators, proof.Bitmap, proof.Signature)
	})
}
func (a *Adapter) VerifyDirectQuorum(set Set, m protocol.XITADirectMessage, proof Proof) error {
	raw, err := a.EncodeDirect(m)
	if err != nil {
		return err
	}
	return a.verifyQuorum(set, m.SetID, raw, proof, func() bool { return secp.VerifyDirectQuorumV5(m, set.Validators, proof.Bitmap, proof.Signature) })
}
func (a *Adapter) VerifySourceFeeQuorum(set Set, p protocol.ILNSourceFeeProposal, proof Proof) error {
	raw, err := p.MarshalBinary()
	if err != nil {
		return err
	}
	return a.verifyQuorum(set, p.SetID, raw, proof, func() bool { return secp.VerifySourceFeeQuorumV5(p, set.Validators, proof.Bitmap, proof.Signature) })
}
