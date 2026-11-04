// Package blspop validates the public evidence for the staking PoP migration.
// It never loads private keys, calls RPCs, changes stake or mutates chain state.
package blspop

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"

	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

const (
	ManifestVersion        = uint32(1)
	MaxEntries             = 4096
	MaxManifestBytes       = 2 * 1024 * 1024
	StakingContractAddress = "0x0000000000000000000000000000000000001001"
)

var stakingContract = types.StringToAddress(StakingContractAddress)

// Entry contains public data only. Multiple historic keys for the same address
// are allowed because the macro snapshot may retain a key after rotation.
type Entry struct {
	Validator string `json:"validator"`
	PublicKey string `json:"publicKey"`
	Proof     string `json:"proof"`
}

type Manifest struct {
	Version          uint32  `json:"version"`
	ChainID          uint64  `json:"chainId"`
	Contract         string  `json:"stakingContract"`
	ActivationHeight uint64  `json:"activationHeight"`
	Entries          []Entry `json:"entries"`
}

type ValidatorKey struct {
	Validator types.Address
	PublicKey []byte
}

type pair struct {
	validator types.Address
	key       [48]byte
}

// VerifiedManifest cannot be constructed by trusting a JSON "verified" flag.
// Every entry is independently checked before this type is returned.
type VerifiedManifest struct {
	digest types.Hash
	keys   map[pair]struct{}
}

func (v *VerifiedManifest) Digest() types.Hash { return v.digest }

func (v *VerifiedManifest) Has(validator types.Address, key []byte) bool {
	if v == nil || len(key) != 48 {
		return false
	}
	p := pair{validator: validator}
	copy(p.key[:], key)
	_, ok := v.keys[p]
	return ok
}

// RequireExactCoverage compares with the union of registered parent-state keys
// and the keys in the canonical validator snapshot that will sign H. The caller
// must obtain those keys from locally authenticated state, never a remote flag.
// This function does not prove that a caller supplied the correct state root.
func (v *VerifiedManifest) RequireExactCoverage(required []ValidatorKey) error {
	if v == nil {
		return fmt.Errorf("missing verified migration manifest")
	}
	seen := make(map[pair]struct{}, len(required))
	for _, requiredKey := range required {
		if requiredKey.Validator == types.ZeroAddress || len(requiredKey.PublicKey) != 48 {
			return fmt.Errorf("invalid required migration key")
		}
		if !v.Has(requiredKey.Validator, requiredKey.PublicKey) {
			return fmt.Errorf("migration proof missing for validator %s", requiredKey.Validator)
		}
		p := pair{validator: requiredKey.Validator}
		copy(p.key[:], requiredKey.PublicKey)
		seen[p] = struct{}{}
	}
	if len(seen) == 0 || len(seen) != len(v.keys) {
		return fmt.Errorf("migration manifest coverage mismatch")
	}
	return nil
}

func addressString(addr types.Address) string { return "0x" + hex.EncodeToString(addr[:]) }

func fixedHex(value string, size int) ([]byte, error) {
	if len(value) != 2+size*2 || value[:2] != "0x" {
		return nil, fmt.Errorf("expected canonical %d-byte hex value", size)
	}
	raw, err := hex.DecodeString(value[2:])
	if err != nil || "0x"+hex.EncodeToString(raw) != value {
		return nil, fmt.Errorf("noncanonical hex value")
	}
	return raw, nil
}

func NewManifest(chainID, activationHeight uint64, entries []Entry) (*Manifest, error) {
	manifest := &Manifest{
		Version: ManifestVersion, ChainID: chainID, Contract: StakingContractAddress,
		ActivationHeight: activationHeight, Entries: append([]Entry(nil), entries...),
	}
	sort.Slice(manifest.Entries, func(i, j int) bool {
		a, b := manifest.Entries[i], manifest.Entries[j]
		if a.Validator != b.Validator {
			return a.Validator < b.Validator
		}
		return a.PublicKey < b.PublicKey
	})
	if _, err := manifest.Verify(chainID, activationHeight); err != nil {
		return nil, err
	}
	return manifest, nil
}

// Verify does not sort or normalize input. An agreed manifest must already use
// canonical lowercase fixed-length hex and strict address/key ordering.
func (m *Manifest) Verify(expectedChainID, expectedHeight uint64) (*VerifiedManifest, error) {
	if m == nil || m.Version != ManifestVersion || m.ChainID == 0 || m.ChainID > math.MaxInt64 ||
		m.ChainID != expectedChainID || m.ActivationHeight == 0 || m.ActivationHeight != expectedHeight ||
		m.Contract != StakingContractAddress || len(m.Entries) == 0 || len(m.Entries) > MaxEntries {
		return nil, fmt.Errorf("invalid staking PoP manifest context")
	}
	// Binary commitment is independent of JSON parser and map ordering.
	encoded := append([]byte(nil), crypto.Keccak256([]byte("XGR_STAKING_BLS_POP_MANIFEST_V1"))...)
	encoded = binary.BigEndian.AppendUint32(encoded, m.Version)
	encoded = binary.BigEndian.AppendUint64(encoded, m.ChainID)
	encoded = append(encoded, stakingContract[:]...)
	encoded = binary.BigEndian.AppendUint64(encoded, m.ActivationHeight)
	encoded = binary.BigEndian.AppendUint32(encoded, uint32(len(m.Entries)))
	verified := &VerifiedManifest{keys: make(map[pair]struct{}, len(m.Entries))}
	var previous []byte
	for idx, entry := range m.Entries {
		addrRaw, err := fixedHex(entry.Validator, 20)
		if err != nil {
			return nil, fmt.Errorf("entry %d validator: %w", idx, err)
		}
		key, err := fixedHex(entry.PublicKey, 48)
		if err != nil {
			return nil, fmt.Errorf("entry %d public key: %w", idx, err)
		}
		proof, err := fixedHex(entry.Proof, 96)
		if err != nil {
			return nil, fmt.Errorf("entry %d proof: %w", idx, err)
		}
		orderingKey := append(append([]byte(nil), addrRaw...), key...)
		if previous != nil && bytes.Compare(previous, orderingKey) >= 0 {
			return nil, fmt.Errorf("duplicate or unsorted migration entry %d", idx)
		}
		previous = orderingKey
		addr := types.BytesToAddress(addrRaw)
		if err := crypto.VerifyStakingPoP(new(big.Int).SetUint64(m.ChainID), stakingContract, addr, key, proof, new(big.Int)); err != nil {
			return nil, fmt.Errorf("entry %d possession proof: %w", idx, err)
		}
		p := pair{validator: addr}
		copy(p.key[:], key)
		verified.keys[p] = struct{}{}
		encoded = append(encoded, orderingKey...)
		encoded = append(encoded, proof...)
	}
	verified.digest = types.BytesToHash(crypto.Keccak256(encoded))
	return verified, nil
}

// CanonicalJSON is the sole accepted manifest file representation. The byte
// comparison in DecodeManifest rejects duplicate and unknown JSON fields,
// alternative number encodings, trailing documents and ambiguous whitespace.
func (m *Manifest) CanonicalJSON() ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("missing manifest")
	}
	if _, err := m.Verify(m.ChainID, m.ActivationHeight); err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func DecodeManifest(raw []byte, expectedChainID, expectedHeight uint64) (*Manifest, *VerifiedManifest, error) {
	if len(raw) == 0 || len(raw) > MaxManifestBytes {
		return nil, nil, fmt.Errorf("invalid manifest file size")
	}
	var m Manifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return nil, nil, err
	}
	verified, err := m.Verify(expectedChainID, expectedHeight)
	if err != nil {
		return nil, nil, err
	}
	canonical, err := json.MarshalIndent(&m, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(raw, append(canonical, '\n')) {
		return nil, nil, fmt.Errorf("noncanonical migration manifest JSON")
	}
	return &m, verified, nil
}
