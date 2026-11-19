package authorization

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	secp "github.com/xgr-network/xgr-node/interchain/ecdsa"
	"github.com/xgr-network/xgr-node/types"
)

var testNow = time.Unix(1_790_000_000, 0)

func fixture(t *testing.T, scheme string, n int) (*Adapter, Set, []Keys) {
	t.Helper()
	adapter, err := ForScheme(scheme)
	require.NoError(t, err)
	set := Set{SetID: 7, Validators: make([]types.Address, n), PublicKeys: make([][]byte, n)}
	keys := make([]Keys, n)
	for i := range keys {
		keys[i].ECDSA, err = crypto.GenerateECDSAKey()
		require.NoError(t, err)
		set.Validators[i] = crypto.PubKeyToAddress(&keys[i].ECDSA.PublicKey)
		if scheme == SchemeBLS {
			secret, err := crypto.GenerateBLSKey()
			require.NoError(t, err)
			raw, err := secret.MarshalBinary()
			require.NoError(t, err)
			keys[i].BLS = []byte(hex.EncodeToString(raw))
			set.PublicKeys[i], err = crypto.BLSSecretKeyToPubkeyBytes(secret)
			require.NoError(t, err)
		} else {
			set.PublicKeys[i] = append([]byte(nil), set.Validators[i].Bytes()...)
		}
	}
	require.NoError(t, adapter.ValidateSet(set))
	return adapter, set, keys
}
func membership(t *testing.T, adapter *Adapter, set Set) Membership {
	t.Helper()
	m := Membership{OriginChainID: 1643, DestinationDomain: 50, Registry: types.StringToAddress("0xabc"),
		ExpectedSetID: set.SetID, ValidUntil: uint64(testNow.Unix() + 300), Action: protocol.ActionAddValidator, Validator: set.Validators[0]}
	if adapter.Scheme() == SchemeBLS {
		m.BLSPublicKey = append([]byte(nil), set.PublicKeys[0]...)
		var err error
		m.BLSPublicKeyEIP2537, err = crypto.BLSPublicKeyToEIP2537(m.BLSPublicKey)
		require.NoError(t, err)
	}
	return m
}
func direct(set Set) protocol.XITADirectMessage {
	return protocol.XITADirectMessage{SourceChainID: 1643, SourceDomain: 1643, DestinationDomain: 50,
		RouteID: types.StringToHash("0x1234"), SetID: set.SetID, SourceBlockNumber: 550,
		SourceRegistry: types.StringToAddress("0x1111"), Gateway: types.StringToAddress("0x2222"),
		SourceRouter: types.StringToAddress("0x3333"), SourceMailbox: types.StringToAddress("0x4444"), DestinationRouter: types.StringToAddress("0x5555"),
		ValidatorFeeWei: big.NewInt(8000), MessageID: types.StringToHash("0x6789"), DestinationISM: types.StringToAddress("0x6666"), DestinationRegistry: types.StringToAddress("0x7777")}
}
func fee(set Set) protocol.ILNSourceFeeProposal {
	return protocol.ILNSourceFeeProposal{SourceChainID: 50, SourceDomain: 50, Registry: types.StringToAddress("0xabcd"),
		SetID: set.SetID, Nonce: 3, ValidUntil: uint64(testNow.Unix() + 300), ValidatorFeeWei: big.NewInt(8000)}
}

func TestExplicitSchemeAndSnapshotValidation(t *testing.T) {
	for _, unknown := range []string{"", "auto", "BLS", "ed25519"} {
		_, err := ForScheme(unknown)
		require.Error(t, err)
	}
	var absent *Adapter
	_, err := absent.EncodeDirect(protocol.XITADirectMessage{})
	require.Error(t, err)
	for _, scheme := range []string{SchemeBLS, SchemeECDSA} {
		t.Run(scheme, func(t *testing.T) {
			a, set, _ := fixture(t, scheme, 3)
			bad := set
			bad.SetID = 0
			require.Error(t, a.ValidateSet(bad))
			bad = set
			bad.PublicKeys = bad.PublicKeys[:2]
			require.Error(t, a.ValidateSet(bad))
			bad = set
			bad.Validators = append([]types.Address(nil), set.Validators...)
			bad.Validators[1] = bad.Validators[0]
			require.Error(t, a.ValidateSet(bad))
			bad.Validators[1] = types.ZeroAddress
			require.Error(t, a.ValidateSet(bad))
			bad = set
			bad.PublicKeys = append([][]byte(nil), set.PublicKeys...)
			bad.PublicKeys[1] = bad.PublicKeys[0]
			require.Error(t, a.ValidateSet(bad))
			bad.PublicKeys[1] = []byte{1}
			require.Error(t, a.ValidateSet(bad))
		})
	}
	a, set, _ := fixture(t, SchemeECDSA, 64)
	require.NoError(t, a.ValidateSet(set))
	set.Validators = append(set.Validators, types.StringToAddress("0xffff"))
	set.PublicKeys = append(set.PublicKeys, set.Validators[64].Bytes())
	require.Error(t, a.ValidateSet(set))
}

func TestBothAdaptersPreserveTypedOperationsAndQuorumRules(t *testing.T) {
	for _, scheme := range []string{SchemeBLS, SchemeECDSA} {
		t.Run(scheme, func(t *testing.T) {
			a, set, keys := fixture(t, scheme, 9)
			m := membership(t, a, set)
			d := direct(set)
			p := fee(set)
			// This deliberately crosses the byte boundary and leaves holes in both
			// sets. A 3-validator fixture cannot detect bitmap byte-order mistakes.
			indices := []int{8, 7, 5, 3, 1, 0}
			memberVotes := make([]Vote, 0, len(indices))
			directVotes := make([]Vote, 0, len(indices))
			feeVotes := make([]Vote, 0, len(indices))
			for _, index := range indices {
				signature, err := a.SignMembership(keys[index], m, testNow)
				require.NoError(t, err)
				require.NoError(t, a.VerifyMembershipVote(set, index, m, signature))
				memberVotes = append(memberVotes, Vote{ValidatorIndex: index, Signature: signature})
				signature, err = a.SignDirect(keys[index], d)
				require.NoError(t, err)
				require.NoError(t, a.VerifyDirectVote(set, index, d, signature))
				directVotes = append(directVotes, Vote{ValidatorIndex: index, Signature: signature})
				signature, err = a.SignSourceFee(keys[index], p)
				require.NoError(t, err)
				require.NoError(t, a.VerifySourceFeeVote(set, index, p, signature))
				feeVotes = append(feeVotes, Vote{ValidatorIndex: index, Signature: signature})
			}
			mp, err := a.AssembleMembershipQuorum(set, memberVotes, m)
			require.NoError(t, err)
			dp, err := a.AssembleDirectQuorum(set, directVotes, d)
			require.NoError(t, err)
			fp, err := a.AssembleSourceFeeQuorum(set, feeVotes, p)
			require.NoError(t, err)
			require.NoError(t, a.VerifyMembershipQuorum(set, m, mp))
			require.NoError(t, a.VerifyDirectQuorum(set, d, dp))
			require.NoError(t, a.VerifySourceFeeQuorum(set, p, fp))
			expected := []byte{0x01, 0xab}
			if scheme == SchemeECDSA {
				expected = []byte{0xab, 0x01}
			}
			for _, proof := range []Proof{mp, dp, fp} {
				require.Equal(t, expected, proof.Bitmap)
				count, err := a.SignerCount(set, proof)
				require.NoError(t, err)
				require.Equal(t, 6, count)
			}
			require.Equal(t, indices[0], memberVotes[0].ValidatorIndex, "assembling must not reorder caller-owned votes")
			_, err = a.AssembleDirectQuorum(set, directVotes[:5], d)
			require.Error(t, err, "ceil(2*9/3) requires six identities")
			duplicates := append([]Vote(nil), directVotes...)
			duplicates[1] = duplicates[0]
			_, err = a.AssembleDirectQuorum(set, duplicates, d)
			require.Error(t, err)
			require.Error(t, a.VerifyDirectVote(set, 2, d, directVotes[0].Signature), "an index is not an identity proof")
			require.Error(t, a.VerifyDirectVote(set, -1, d, directVotes[0].Signature))
			require.Error(t, a.VerifyDirectVote(set, 9, d, directVotes[0].Signature))
			wrongEpoch := set
			wrongEpoch.SetID++
			require.Error(t, a.VerifyMembershipQuorum(wrongEpoch, m, mp))
			require.Error(t, a.VerifyDirectQuorum(wrongEpoch, d, dp))
			require.Error(t, a.VerifySourceFeeQuorum(wrongEpoch, p, fp))
			malformed := dp
			malformed.Bitmap = []byte{expected[1], expected[0]}
			require.Error(t, a.VerifyDirectQuorum(set, d, malformed), "foreign bitmap endianness must fail")
			malformed = dp
			malformed.Bitmap = append([]byte{0}, expected...)
			require.Error(t, a.VerifyDirectQuorum(set, d, malformed), "noncanonical padding must fail")
			malformed = dp
			malformed.Signature = append(append([]byte(nil), dp.Signature...), 0)
			require.Error(t, a.VerifyDirectQuorum(set, d, malformed), "trailing signature bytes must fail")
			changed := m
			changed.Action = protocol.ActionRemoveValidator
			require.Error(t, a.VerifyMembershipQuorum(set, changed, mp))
			changed = m
			changed.Validator = set.Validators[2]
			require.Error(t, a.VerifyMembershipQuorum(set, changed, mp))
			changed = m
			changed.ValidUntil++
			require.Error(t, a.VerifyMembershipQuorum(set, changed, mp))
			changed = m
			changed.DestinationDomain++
			require.Error(t, a.VerifyMembershipQuorum(set, changed, mp))
			changedFee := p
			changedFee.Nonce++
			require.Error(t, a.VerifySourceFeeQuorum(set, changedFee, fp))
			changedFee = p
			changedFee.Registry = set.Validators[2]
			require.Error(t, a.VerifySourceFeeQuorum(set, changedFee, fp))
			changedFee = p
			changedFee.ValidatorFeeWei = big.NewInt(8001)
			require.Error(t, a.VerifySourceFeeQuorum(set, changedFee, fp))
			require.Error(t, a.VerifyDirectVote(set, indices[0], d, feeVotes[0].Signature), "typed operations cannot substitute signatures")
			expired := m
			expired.ValidUntil = uint64(testNow.Unix() - 1)
			_, err = a.SignMembership(keys[0], expired, testNow)
			require.Error(t, err)
			excessive := m
			excessive.ValidUntil = uint64(testNow.Unix() + 601)
			_, err = a.SignMembership(keys[0], excessive, testNow)
			require.Error(t, err)
			_, err = a.SignDirect(Keys{}, d)
			require.Error(t, err)
		})
	}
}

func TestMembershipWireAndPossessionRetainSchemeSpecificBindings(t *testing.T) {
	for _, scheme := range []string{SchemeBLS, SchemeECDSA} {
		t.Run(scheme, func(t *testing.T) {
			a, set, keys := fixture(t, scheme, 3)
			m := membership(t, a, set)
			raw, err := a.EncodeMembership(m)
			require.NoError(t, err)
			decoded, err := a.DecodeMembership(raw)
			require.NoError(t, err)
			if scheme == SchemeBLS {
				oldRaw, err := m.BLS().MarshalBinary()
				require.NoError(t, err)
				require.Equal(t, oldRaw, raw)
				require.Equal(t, m.BLS(), decoded.BLS())
				require.Equal(t, types.ZeroAddress, decoded.Registry, "V3 wire must not silently gain registry binding")
			} else {
				require.Equal(t, m, decoded)
			}
			for _, invalid := range [][]byte{nil, raw[:len(raw)-1], append(append([]byte(nil), raw...), 0)} {
				_, err = a.DecodeMembership(invalid)
				require.Error(t, err)
			}
			otherScheme := SchemeBLS
			if scheme == SchemeBLS {
				otherScheme = SchemeECDSA
			}
			other, err := ForScheme(otherScheme)
			require.NoError(t, err)
			_, err = other.DecodeMembership(raw)
			require.Error(t, err)
			proof, err := a.SignPossession(keys[0], m)
			require.NoError(t, err)
			require.NoError(t, a.VerifyPossession(m, proof))
			changed := m
			changed.DestinationDomain++
			require.Error(t, a.VerifyPossession(changed, proof))
			changed = m
			changed.Validator = set.Validators[1]
			require.Error(t, a.VerifyPossession(changed, proof))
			_, err = a.SignPossession(keys[1], m)
			require.Error(t, err, "candidate must control the selected verification key")
			changed = m
			changed.Registry = set.Validators[2]
			if scheme == SchemeECDSA {
				require.Error(t, a.VerifyPossession(changed, proof))
			} else {
				require.NoError(t, a.VerifyPossession(changed, proof), "released BLS bootstrap payload has no registry address")
			}
			changed = m
			changed.Action = protocol.ActionRemoveValidator
			require.Error(t, a.VerifyPossession(changed, proof))
			_, err = a.SignPossession(keys[0], changed)
			require.Error(t, err)
		})
	}
}

func TestDirectSharedCodecRejectsForeignDomainsAndInvalidFields(t *testing.T) {
	for _, scheme := range []string{SchemeBLS, SchemeECDSA} {
		t.Run(scheme, func(t *testing.T) {
			a, set, keys := fixture(t, scheme, 3)
			message := direct(set)
			raw, err := a.EncodeDirect(message)
			require.NoError(t, err)
			decoded, err := a.DecodeDirect(raw)
			require.NoError(t, err)
			require.Equal(t, message, decoded)
			signature, err := a.SignDirect(keys[0], message)
			require.NoError(t, err)
			mutations := []func(*protocol.XITADirectMessage){
				func(m *protocol.XITADirectMessage) { m.SourceChainID++ }, func(m *protocol.XITADirectMessage) { m.SourceDomain++ },
				func(m *protocol.XITADirectMessage) { m.DestinationDomain++ }, func(m *protocol.XITADirectMessage) { m.RouteID[0] ^= 1 },
				func(m *protocol.XITADirectMessage) { m.SetID++ }, func(m *protocol.XITADirectMessage) { m.SourceBlockNumber++ },
				func(m *protocol.XITADirectMessage) { m.SourceRegistry[0] ^= 1 }, func(m *protocol.XITADirectMessage) { m.Gateway[0] ^= 1 },
				func(m *protocol.XITADirectMessage) { m.SourceRouter[0] ^= 1 }, func(m *protocol.XITADirectMessage) { m.SourceMailbox[0] ^= 1 },
				func(m *protocol.XITADirectMessage) { m.DestinationRouter[0] ^= 1 }, func(m *protocol.XITADirectMessage) { m.ValidatorFeeWei = big.NewInt(8001) },
				func(m *protocol.XITADirectMessage) { m.MessageID[0] ^= 1 }, func(m *protocol.XITADirectMessage) { m.DestinationISM[0] ^= 1 },
				func(m *protocol.XITADirectMessage) { m.DestinationRegistry[0] ^= 1 },
			}
			for _, mutate := range mutations {
				altered := message
				mutate(&altered)
				require.Error(t, a.VerifyDirectVote(set, 0, altered, signature))
			}
			otherScheme := SchemeBLS
			if scheme == SchemeBLS {
				otherScheme = SchemeECDSA
			}
			other, err := ForScheme(otherScheme)
			require.NoError(t, err)
			_, err = other.DecodeDirect(raw)
			require.Error(t, err)
			_, err = a.DecodeDirect(append(append([]byte(nil), raw...), 0))
			require.Error(t, err)
			_, err = a.DecodeDirect(raw[:len(raw)-1])
			require.Error(t, err)
			invalid := append([]byte(nil), raw...)
			copy(invalid[len(invalid)-20:], make([]byte, 20))
			_, err = a.DecodeDirect(invalid)
			require.Error(t, err)
			_, err = message.MarshalBinaryWithDomain("untrusted-domain")
			require.Error(t, err)
			if scheme == SchemeECDSA {
				_, err = secp.Digest(invalid)
				require.Error(t, err)
			}
		})
	}
}

func TestECDSARejectsMalleabilityAndRetainsOrderedSignatures(t *testing.T) {
	a, set, keys := fixture(t, SchemeECDSA, 3)
	message := direct(set)
	sig0, err := a.SignDirect(keys[0], message)
	require.NoError(t, err)
	sig1, err := a.SignDirect(keys[1], message)
	require.NoError(t, err)
	before := append([]byte(nil), sig0...)
	proof, err := a.AssembleDirectQuorum(set, []Vote{{ValidatorIndex: 1, Signature: sig1}, {ValidatorIndex: 0, Signature: sig0}}, message)
	require.NoError(t, err)
	require.Equal(t, append(append([]byte(nil), sig0...), sig1...), proof.Signature)
	require.Equal(t, before, sig0, "verification must not normalize caller-owned signature bytes")
	highS := append([]byte(nil), sig0...)
	s := new(big.Int).SetBytes(highS[32:64])
	s.Sub(crypto.S256.Params().N, s).FillBytes(highS[32:64])
	if highS[64] == 27 {
		highS[64] = 28
	} else {
		highS[64] = 27
	}
	require.Error(t, a.VerifyDirectVote(set, 0, message, highS))
	for _, v := range []byte{0, 1, 29, 255} {
		invalid := append([]byte(nil), sig0...)
		invalid[64] = v
		require.Error(t, a.VerifyDirectVote(set, 0, message, invalid))
	}
	malformed := proof
	malformed.Signature = append(append([]byte(nil), sig1...), sig0...)
	require.Error(t, a.VerifyDirectQuorum(set, message, malformed))
	duplicateSet := set
	duplicateSet.Validators = []types.Address{set.Validators[0], set.Validators[0], set.Validators[2]}
	raw, err := a.EncodeDirect(message)
	require.NoError(t, err)
	require.False(t, secp.VerifyQuorum(raw, duplicateSet.Validators, []byte{3}, append(append([]byte(nil), sig0...), sig0...)), "legacy raw entry point must not count one identity twice")
	archive, err := a.ExpandedSignature(proof)
	require.NoError(t, err)
	require.Equal(t, proof.Signature, archive)
	archive[0] ^= 1
	require.False(t, bytes.Equal(archive, proof.Signature), "expanded output must not alias proof")
}

func TestMembershipDeadlineMatchesV3ContractBoundaries(t *testing.T) {
	for _, scheme := range []string{SchemeBLS, SchemeECDSA} {
		t.Run(scheme, func(t *testing.T) {
			a, set, keys := fixture(t, scheme, 3)
			m := membership(t, a, set)
			for _, offset := range []int64{0, 1, 600} {
				m.ValidUntil = uint64(testNow.Unix() + offset)
				signature, err := a.SignMembership(keys[0], m, testNow)
				require.NoError(t, err, "V3 allows the current second and at most 600 future seconds")
				require.NoError(t, a.VerifyMembershipVote(set, 0, m, signature))
			}
			for _, offset := range []int64{-1, 601} {
				m.ValidUntil = uint64(testNow.Unix() + offset)
				_, err := a.SignMembership(keys[0], m, testNow)
				require.Error(t, err)
			}
			_, err := a.SignMembership(keys[0], m, time.Unix(-1, 0))
			require.Error(t, err)
		})
	}
}
