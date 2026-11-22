// Package ecdsa implements the on-chain XGR Interchain ECDSA signature format.
// The lifecycle codec matches XGRInterchainECDSAValidatorRegistryV5. Membership
// policy and activation remain in the shared runtime. No private key leaves
// the node.
package ecdsa

import (
	"bytes"
	stdECDSA "crypto/ecdsa"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

const (
	BootstrapDomainV2  = "XITA_ECDSA_BOOTSTRAP_V2"
	PossessionDomainV2 = "XITA_ECDSA_POSSESSION_V2"
	MembershipDomainV2 = "XITA_ECDSA_MEMBERSHIP_V2"
	MemberAdd          = uint8(1)
	MemberRemove       = uint8(2)
	maxMembershipAge   = 10 * time.Minute
)

// MembershipV5 names ONE validator action on ONE destination Registry,
// not a replacement for the entire destination validator set.
type MembershipV5 struct {
	OriginChainID     uint64
	DestinationDomain uint32
	Registry          types.Address
	ExpectedSetID     uint64
	ValidUntil        uint64
	Action            uint8
	Validator         types.Address
}

// VoteV5 holds a validator-indexed secp256k1 proof in Solidity's 65-byte
// r||s||v (v=27/28) format. Signatures are never accepted from external
// network messages without recovering their signer against the current set.
type VoteV5 struct {
	ValidatorIndex int
	Signature      []byte
}

var ErrInvalidLifecycle = errors.New("invalid destination ECDSA validator lifecycle")

func appendU64(buf []byte, n uint64) []byte {
	var v [8]byte
	binary.BigEndian.PutUint64(v[:], n)
	return append(buf, v[:]...)
}
func appendU32(buf []byte, n uint32) []byte {
	var v [4]byte
	binary.BigEndian.PutUint32(v[:], n)
	return append(buf, v[:]...)
}

// BootstrapPayloadV5 EXACTLY matches encodeBootstrapPayload in the V5
// Solidity contract. No Registry exists yet; the proof is bound to XGR
// origin, destination domain and validator identity.
func BootstrapPayloadV5(origin uint64, domain uint32, validator types.Address) ([]byte, error) {
	if origin == 0 || domain == 0 || validator == types.ZeroAddress {
		return nil, ErrInvalidLifecycle
	}
	out := appendU64([]byte(BootstrapDomainV2), origin)
	out = appendU32(out, domain)
	out = append(out, validator.Bytes()...)
	return out, nil
}

// PossessionPayloadV5 EXACTLY matches encodePossessionPayload. A
// destination-specific, deployed Registry address prevents replay across
// replacement deployments. Only the validator's ECDSA key can prove this.
func PossessionPayloadV5(origin uint64, domain uint32, registry, validator types.Address) ([]byte, error) {
	if origin == 0 || domain == 0 || registry == types.ZeroAddress || validator == types.ZeroAddress {
		return nil, ErrInvalidLifecycle
	}
	out := appendU64([]byte(PossessionDomainV2), origin)
	out = appendU32(out, domain)
	out = append(out, registry.Bytes()...)
	out = append(out, validator.Bytes()...)
	return out, nil
}

// MembershipPayloadV5 EXACTLY matches encodeMembershipPayload in Solidity.
// The registry address, chain/domain, expected epoch, deadline and individual
// ADD/REMOVE action are all signed; there is no global XGR validator roster.
func MembershipPayloadV5(m MembershipV5) ([]byte, error) {
	if m.OriginChainID == 0 || m.DestinationDomain == 0 ||
		m.Registry == types.ZeroAddress || m.ExpectedSetID == 0 || m.ValidUntil == 0 ||
		m.Validator == types.ZeroAddress || (m.Action != MemberAdd && m.Action != MemberRemove) {
		return nil, ErrInvalidLifecycle
	}
	out := appendU64([]byte(MembershipDomainV2), m.OriginChainID)
	out = appendU32(out, m.DestinationDomain)
	out = append(out, m.Registry.Bytes()...)
	out = appendU64(out, m.ExpectedSetID)
	out = appendU64(out, m.ValidUntil)
	out = append(out, m.Action)
	out = append(out, m.Validator.Bytes()...)
	return out, nil
}

// ParseMembershipPayloadV5 accepts exactly one canonical registry-bound
// transition. The configured adapter, never a peer-supplied prefix, selects
// this decoder.
func ParseMembershipPayloadV5(raw []byte) (MembershipV5, error) {
	const tail = 8 + 4 + 20 + 8 + 8 + 1 + 20
	var m MembershipV5
	if len(raw) != len(MembershipDomainV2)+tail || !bytes.HasPrefix(raw, []byte(MembershipDomainV2)) {
		return m, ErrInvalidLifecycle
	}
	o := len(MembershipDomainV2)
	m.OriginChainID = binary.BigEndian.Uint64(raw[o : o+8])
	o += 8
	m.DestinationDomain = binary.BigEndian.Uint32(raw[o : o+4])
	o += 4
	m.Registry = types.BytesToAddress(raw[o : o+20])
	o += 20
	m.ExpectedSetID = binary.BigEndian.Uint64(raw[o : o+8])
	o += 8
	m.ValidUntil = binary.BigEndian.Uint64(raw[o : o+8])
	o += 8
	m.Action = raw[o]
	o++
	m.Validator = types.BytesToAddress(raw[o : o+20])
	if _, err := MembershipPayloadV5(m); err != nil {
		return MembershipV5{}, err
	}
	return m, nil
}

// VerifyMembershipVoteV5 authenticates one vote without claiming a quorum.
func VerifyMembershipVoteV5(m MembershipV5, signer types.Address, signature []byte) bool {
	raw, err := MembershipPayloadV5(m)
	return err == nil && verifyOne(raw, signer, signature)
}

func VerifyPossessionV5(origin uint64, domain uint32, registry, validator types.Address, signature []byte) bool {
	raw, err := PossessionPayloadV5(origin, domain, registry, validator)
	return err == nil && verifyOne(raw, validator, signature)
}

// SignBootstrapV5 reads only the caller-supplied in-memory local private key.
// The returned address is derived from the key, never chosen by an RPC caller.
// This helper does not submit a deployment or activate a validator.
func SignBootstrapV5(key *stdECDSA.PrivateKey, origin uint64, domain uint32) (types.Address, []byte, []byte, error) {
	if key == nil {
		return types.ZeroAddress, nil, nil, ErrInvalidLifecycle
	}
	validator := crypto.PubKeyToAddress(&key.PublicKey)
	raw, err := BootstrapPayloadV5(origin, domain, validator)
	if err != nil {
		return types.ZeroAddress, nil, nil, err
	}
	sig, err := signTyped(key, raw)
	return validator, raw, sig, err
}

func SignPossessionV5(key *stdECDSA.PrivateKey, origin uint64, domain uint32, registry types.Address) (types.Address, []byte, []byte, error) {
	if key == nil {
		return types.ZeroAddress, nil, nil, ErrInvalidLifecycle
	}
	validator := crypto.PubKeyToAddress(&key.PublicKey)
	raw, err := PossessionPayloadV5(origin, domain, registry, validator)
	if err != nil {
		return types.ZeroAddress, nil, nil, err
	}
	sig, err := signTyped(key, raw)
	return validator, raw, sig, err
}

// SignMembershipV5 only signs a typed, unexpired single-validator transition.
// Authentication of the local XGR PoS validator is a separate worker gate.
func SignMembershipV5(key *stdECDSA.PrivateKey, membership MembershipV5, now time.Time) ([]byte, []byte, error) {
	if key == nil {
		return nil, nil, ErrInvalidLifecycle
	}
	raw, err := MembershipPayloadV5(membership)
	if err != nil {
		return nil, nil, err
	}
	nowUnix := now.Unix()
	if nowUnix < 0 || membership.ValidUntil < uint64(nowUnix) ||
		membership.ValidUntil-uint64(nowUnix) > uint64(maxMembershipAge/time.Second) {
		return nil, nil, fmt.Errorf("%w: stale or excessive membership deadline", ErrInvalidLifecycle)
	}
	sig, err := signTyped(key, raw)
	return raw, sig, err
}

func signTyped(key *stdECDSA.PrivateKey, payload []byte) ([]byte, error) {
	digest := crypto.Keccak256Hash(payload)
	sig, err := crypto.Sign(key, digest.Bytes())
	if err != nil {
		return nil, err
	}
	if len(sig) != SignatureSize || sig[64] > 1 {
		return nil, ErrInvalidLifecycle
	}
	sig[64] += 27
	return sig, nil
}

func verifyOne(payload []byte, signer types.Address, sig []byte) bool {
	if signer == types.ZeroAddress || len(sig) != SignatureSize ||
		(sig[64] != 27 && sig[64] != 28) {
		return false
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:64])
	if !crypto.ValidateSignatureValues(big.NewInt(int64(sig[64]-27)), r, s, true) {
		return false
	}
	local := append([]byte(nil), sig...)
	local[64] -= 27 // Go recovery uses v=0/1; Solidity uses 27/28.
	digest := crypto.Keccak256Hash(payload)
	pub, err := crypto.RecoverPubkey(local, digest.Bytes())
	return err == nil && crypto.PubKeyToAddress(pub) == signer
}

// AssembleMembershipQuorumV5 sorts verified votes by *destination validator
// index* and returns Solidity's LITTLE-endian bitmap and concatenated 65-byte
// ECDSA signatures. BLS bitmap conventions must never be reused.
func AssembleMembershipQuorumV5(m MembershipV5, validators []types.Address, votes []VoteV5) ([]byte, []byte, error) {
	payload, err := MembershipPayloadV5(m)
	if err != nil {
		return nil, nil, err
	}
	return assembleTyped(payload, validators, votes)
}

// VerifyMembershipQuorumV5 verifies the same typed message as the on-chain
// Registry V5, including canonical bitmap size and high-bit rejection.
func VerifyMembershipQuorumV5(m MembershipV5, validators []types.Address, bitmap, signatures []byte) bool {
	payload, err := MembershipPayloadV5(m)
	return err == nil && verifyTypedQuorum(payload, validators, bitmap, signatures)
}

func assembleTyped(payload []byte, validators []types.Address, votes []VoteV5) ([]byte, []byte, error) {
	n := len(validators)
	if n == 0 || n > MaxValidators || len(votes) < (2*n+2)/3 || len(votes) > n {
		return nil, nil, fmt.Errorf("%w: no valid destination quorum", ErrInvalidLifecycle)
	}
	for i, validator := range validators {
		if validator == types.ZeroAddress {
			return nil, nil, ErrInvalidLifecycle
		}
		for j := 0; j < i; j++ {
			if validators[j] == validator {
				return nil, nil, fmt.Errorf("%w: repeated validator identity", ErrInvalidLifecycle)
			}
		}
	}
	ordered := append([]VoteV5(nil), votes...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ValidatorIndex < ordered[j].ValidatorIndex })
	bitmap := make([]byte, (n+7)/8)
	signatures := make([]byte, 0, len(ordered)*SignatureSize)
	for i, vote := range ordered {
		if vote.ValidatorIndex < 0 || vote.ValidatorIndex >= n ||
			(i > 0 && ordered[i-1].ValidatorIndex == vote.ValidatorIndex) ||
			!verifyOne(payload, validators[vote.ValidatorIndex], vote.Signature) {
			return nil, nil, fmt.Errorf("%w: invalid, duplicate or unauthorized vote", ErrInvalidLifecycle)
		}
		bitmap[vote.ValidatorIndex/8] |= 1 << uint(vote.ValidatorIndex%8)
		signatures = append(signatures, vote.Signature...)
	}
	if !verifyTypedQuorum(payload, validators, bitmap, signatures) {
		return nil, nil, ErrInvalidLifecycle
	}
	return bitmap, signatures, nil
}

func verifyTypedQuorum(payload []byte, validators []types.Address, bitmap, signatures []byte) bool {
	n := len(validators)
	if n == 0 || n > MaxValidators || len(bitmap) != (n+7)/8 {
		return false
	}
	selected := 0
	for i := 0; i < len(bitmap)*8; i++ {
		if bitmap[i/8]&(1<<uint(i%8)) == 0 {
			continue
		}
		if i >= n {
			return false
		}
		selected++
	}
	if selected < (2*n+2)/3 || len(signatures) != selected*SignatureSize {
		return false
	}
	offset := 0
	for i, signer := range validators {
		if signer == types.ZeroAddress {
			return false
		}
		for j := 0; j < i; j++ {
			if validators[j] == signer {
				return false
			}
		}
		if bitmap[i/8]&(1<<uint(i%8)) == 0 {
			continue
		}
		if !verifyOne(payload, signer, signatures[offset:offset+SignatureSize]) {
			return false
		}
		offset += SignatureSize
	}
	return offset == len(signatures)
}

// AssertV5PayloadSeparation avoids accidental BLS-signature reuse.
func AssertV5PayloadSeparation(m MembershipV5) error {
	raw, err := MembershipPayloadV5(m)
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(raw, []byte(MembershipDomainV2)) ||
		bytes.HasPrefix(raw, []byte(Domain)) {
		return ErrInvalidLifecycle
	}
	return nil
}
