package blspop

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

func testEntry(t *testing.T, validator types.Address) (Entry, ValidatorKey) {
	t.Helper()
	key, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	publicKey, err := crypto.BLSSecretKeyToPubkeyBytes(key)
	require.NoError(t, err)
	proof, err := crypto.CreateStakingPoP(key, big.NewInt(1643), stakingContract, validator, new(big.Int))
	require.NoError(t, err)
	return Entry{Validator: addressString(validator), PublicKey: "0x" + hex.EncodeToString(publicKey), Proof: "0x" + hex.EncodeToString(proof)}, ValidatorKey{Validator: validator, PublicKey: publicKey}
}

func TestManifestDeterminismAndExactCoverage(t *testing.T) {
	one, first := testEntry(t, types.Address{19: 1})
	two, second := testEntry(t, types.Address{19: 2})
	m, err := NewManifest(1643, 100, []Entry{two, one})
	require.NoError(t, err)
	n, err := NewManifest(1643, 100, []Entry{one, two})
	require.NoError(t, err)
	mRaw, err := m.CanonicalJSON()
	require.NoError(t, err)
	nRaw, err := n.CanonicalJSON()
	require.NoError(t, err)
	require.Equal(t, mRaw, nRaw)
	_, verified, err := DecodeManifest(mRaw, 1643, 100)
	require.NoError(t, err)
	require.NotEqual(t, types.ZeroHash, verified.Digest())
	require.NoError(t, verified.RequireExactCoverage([]ValidatorKey{first, second, first}))
	require.Error(t, verified.RequireExactCoverage([]ValidatorKey{first}))
	require.Error(t, verified.RequireExactCoverage(nil))
	third := ValidatorKey{Validator: types.Address{19: 3}, PublicKey: first.PublicKey}
	require.Error(t, verified.RequireExactCoverage([]ValidatorKey{first, third}))
	require.False(t, verified.Has(first.Validator, nil))
	require.False(t, (*VerifiedManifest)(nil).Has(first.Validator, first.PublicKey))
	_, _, err = DecodeManifest(mRaw, 1887, 100)
	require.Error(t, err)
	_, _, err = DecodeManifest(mRaw, 1643, 101)
	require.Error(t, err)
}

func TestManifestRotationRequiresBothKeys(t *testing.T) {
	oldEntry, oldKey := testEntry(t, types.Address{19: 1})
	newEntry, newKey := testEntry(t, oldKey.Validator)
	m, err := NewManifest(1643, 100, []Entry{oldEntry, newEntry})
	require.NoError(t, err)
	verified, err := m.Verify(1643, 100)
	require.NoError(t, err)
	// A current registered key and the older macro snapshot key are distinct
	// requirements even when they belong to the same validator address.
	require.NoError(t, verified.RequireExactCoverage([]ValidatorKey{newKey, oldKey}))
	require.Error(t, verified.RequireExactCoverage([]ValidatorKey{newKey}))
}

func TestManifestRejectsUnprovedAndAmbiguousInputs(t *testing.T) {
	one, _ := testEntry(t, types.Address{19: 1})
	two, _ := testEntry(t, types.Address{19: 2})
	valid, err := NewManifest(1643, 100, []Entry{one, two})
	require.NoError(t, err)
	mutations := map[string]func(*Manifest){
		"duplicate":        func(m *Manifest) { m.Entries = []Entry{one, one} },
		"unsorted":         func(m *Manifest) { m.Entries = []Entry{two, one} },
		"missingProof":     func(m *Manifest) { m.Entries[0].Proof = "" },
		"wrongProof":       func(m *Manifest) { m.Entries[0].Proof = two.Proof },
		"wrongAddress":     func(m *Manifest) { m.Entries[0].Validator = addressString(types.Address{19: 3}) },
		"wrongContract":    func(m *Manifest) { m.Contract = addressString(types.Address{19: 4}) },
		"wrongVersion":     func(m *Manifest) { m.Version++ },
		"empty":            func(m *Manifest) { m.Entries = nil },
		"noncanonicalHex":  func(m *Manifest) { m.Entries[0].PublicKey = "0X" + one.PublicKey[2:] },
		"truncatedAddress": func(m *Manifest) { m.Entries[0].Validator = "0x01" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			copy := *valid
			copy.Entries = append([]Entry(nil), valid.Entries...)
			mutate(&copy)
			_, err := copy.Verify(1643, 100)
			require.Error(t, err)
		})
	}
	raw, err := valid.CanonicalJSON()
	require.NoError(t, err)
	for _, invalid := range [][]byte{
		append(append([]byte(nil), raw...), raw...),
		bytes.Replace(raw, []byte("\"version\": 1,"), []byte("\"version\": 1,\n  \"version\": 1,"), 1),
		bytes.Replace(raw, []byte("\"version\": 1,"), []byte("\"verified\": true,\n  \"version\": 1,"), 1),
		[]byte(strings.TrimSpace(string(raw))),
	} {
		_, _, err := DecodeManifest(invalid, 1643, 100)
		require.Error(t, err)
	}
	compact, err := json.Marshal(valid)
	require.NoError(t, err)
	_, _, err = DecodeManifest(compact, 1643, 100)
	require.Error(t, err)
}
