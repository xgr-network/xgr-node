package interchain

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

func TestQuorumThreshold(t *testing.T) {
	cases := map[int]int{1: 1, 2: 2, 3: 2, 4: 3, 5: 4, 6: 4}
	for n, want := range cases {
		got, err := QuorumThreshold(n)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

func TestMembershipPayloadRoundTrip(t *testing.T) {
	compressed, eip := testMembershipKeys(t)
	p := MembershipPayload{
		OriginChainID:     1643,
		DestinationDomain: 8453,
		SetID:             17,
		ValidUntil:        1234567890,
		Action:            ActionAddValidator,
		Validator:         types.StringToAddress("0x1000000000000000000000000000000000000001"),
		BLSPublicKey:        compressed,
		BLSPublicKeyEIP2537: eip,
	}
	raw, err := p.MarshalBinary()
	require.NoError(t, err)

	var decoded MembershipPayload
	require.NoError(t, decoded.UnmarshalBinary(raw))
	require.Equal(t, p.OriginChainID, decoded.OriginChainID)
	require.Equal(t, p.DestinationDomain, decoded.DestinationDomain)
	require.Equal(t, p.SetID, decoded.SetID)
	require.Equal(t, p.ValidUntil, decoded.ValidUntil)
	require.Equal(t, p.Action, decoded.Action)
	require.Equal(t, p.Validator, decoded.Validator)
	require.Equal(t, p.BLSPublicKey, decoded.BLSPublicKey)
	require.Equal(t, p.BLSPublicKeyEIP2537, decoded.BLSPublicKeyEIP2537)
}

func TestMembershipPayloadRejectsWrongDomain(t *testing.T) {
	compressed, eip := testMembershipKeys(t)
	p := MembershipPayload{
		OriginChainID:     1643,
		DestinationDomain: 8453,
		SetID:             1,
		ValidUntil:        1234567890,
		Action:            ActionRemoveValidator,
		Validator:         types.StringToAddress("0x1000000000000000000000000000000000000001"),
		BLSPublicKey:        compressed,
		BLSPublicKeyEIP2537: eip,
	}
	raw, err := p.MarshalBinary()
	require.NoError(t, err)
	raw[0] ^= 0xff

	var decoded MembershipPayload
	require.Error(t, decoded.UnmarshalBinary(raw))
}

func TestVerifyAndAggregateTwoOfThree(t *testing.T) {
	message := []byte("xgr interchain aggregate test")
	publicKeys := make([][]byte, 3)
	votes := make([]Vote, 0, 2)

	for i := 0; i < 3; i++ {
		key, err := crypto.GenerateBLSKey()
		require.NoError(t, err)
		pub, err := crypto.BLSSecretKeyToPubkeyBytes(key)
		require.NoError(t, err)
		publicKeys[i] = pub

		if i < 2 {
			sig, err := crypto.SignByBLS(key, message)
			require.NoError(t, err)
			votes = append(votes, Vote{ValidatorIndex: i, Signature: sig})
		}
	}

	require.NoError(t, VerifyQuorum(publicKeys, votes, message))
	bitmap, aggregate, err := AggregateVotes(publicKeys, votes, message)
	require.NoError(t, err)
	require.Equal(t, 0, bitmap.Cmp(big.NewInt(3)))
	require.NotEmpty(t, aggregate)
	require.NoError(t, VerifyAggregatedQuorum(publicKeys, bitmap, aggregate, message))
}

func TestAggregateRejectsOneOfThree(t *testing.T) {
	message := []byte("xgr interchain no quorum")
	publicKeys := make([][]byte, 3)
	var firstKey interface{ }

	keys := make([]interface{}, 0)
	_ = firstKey
	_ = keys

	blsKeys := make([][]byte, 3)
	_ = blsKeys

	key0, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		key, err := crypto.GenerateBLSKey()
		require.NoError(t, err)
		if i == 0 {
			key = key0
		}
		pub, err := crypto.BLSSecretKeyToPubkeyBytes(key)
		require.NoError(t, err)
		publicKeys[i] = pub
	}

	sig, err := crypto.SignByBLS(key0, message)
	require.NoError(t, err)
	_, _, err = AggregateVotes(publicKeys, []Vote{{ValidatorIndex: 0, Signature: sig}}, message)
	require.ErrorIs(t, err, ErrQuorumNotReached)
}

func TestVerifyQuorumRejectsDuplicateSigner(t *testing.T) {
	message := []byte("duplicate")
	keys := make([][]byte, 2)
	private := make([][]byte, 0)
	_ = private

	key0, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	key1, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	keys[0], err = crypto.BLSSecretKeyToPubkeyBytes(key0)
	require.NoError(t, err)
	keys[1], err = crypto.BLSSecretKeyToPubkeyBytes(key1)
	require.NoError(t, err)
	sig, err := crypto.SignByBLS(key0, message)
	require.NoError(t, err)

	err = VerifyQuorum(keys, []Vote{
		{ValidatorIndex: 0, Signature: sig},
		{ValidatorIndex: 0, Signature: sig},
	}, message)
	require.ErrorIs(t, err, ErrDuplicateSigner)
}


func TestMembershipPayloadRejectsWrongBLSKeyLength(t *testing.T) {
	p := MembershipPayload{
		OriginChainID:     1643,
		DestinationDomain: 8453,
		SetID:             1,
		ValidUntil:        1234567890,
		Action:            ActionAddValidator,
		Validator:         types.StringToAddress("0x1000000000000000000000000000000000000001"),
		BLSPublicKey:      make([]byte, BLSPublicKeyCompressedLength-1),
	}
	_, err := p.MarshalBinary()
	require.Error(t, err)
	require.Contains(t, err.Error(), "48 bytes")
}

func TestMembershipPayloadRejectsMissingExpiry(t *testing.T) {
	p := MembershipPayload{
		OriginChainID:     1643,
		DestinationDomain: 8453,
		SetID:             1,
		Action:            ActionAddValidator,
		Validator:         types.StringToAddress("0x1000000000000000000000000000000000000001"),
		BLSPublicKey:      make([]byte, BLSPublicKeyCompressedLength),
	}
	_, err := p.MarshalBinary()
	require.Error(t, err)
	require.Contains(t, err.Error(), "valid-until")
}


func TestMembershipPayloadRejectsMismatchedEIP2537Key(t *testing.T) {
	compressed, eip := testMembershipKeys(t)
	eip[127] ^= 0x01
	p := MembershipPayload{
		OriginChainID:       1643,
		DestinationDomain:   8453,
		SetID:               1,
		ValidUntil:          1234567890,
		Action:              ActionAddValidator,
		Validator:           types.StringToAddress("0x1000000000000000000000000000000000000001"),
		BLSPublicKey:        compressed,
		BLSPublicKeyEIP2537: eip,
	}
	_, err := p.MarshalBinary()
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not match canonical")
}

func testMembershipKeys(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	compressed, err := crypto.BLSSecretKeyToPubkeyBytes(key)
	require.NoError(t, err)
	eip, err := crypto.BLSPublicKeyToEIP2537(compressed)
	require.NoError(t, err)
	return compressed, eip
}


func TestMembershipPayloadCanonicalVector(t *testing.T) {
	compressed, err := hex.DecodeString("a695ad325dfc7e1191fbc9f186f58eff42a634029731b18380ff89bf42c464a42cb8ca55b200f051f57f1e1893c68759")
	require.NoError(t, err)
	eip, err := hex.DecodeString("000000000000000000000000000000000695ad325dfc7e1191fbc9f186f58eff42a634029731b18380ff89bf42c464a42cb8ca55b200f051f57f1e1893c687590000000000000000000000000000000010ea7912ef7a227c01298a7c7a96b1851b23021741c71938f39638b1d368aaa621452426b5d8199773a2cb5b2743a5da")
	require.NoError(t, err)

	payload := MembershipPayload{
		OriginChainID: 1643, DestinationDomain: 8453, SetID: 7, ValidUntil: 1700000000,
		Action: ActionAddValidator,
		Validator: types.StringToAddress("0x1111111111111111111111111111111111111111"),
		BLSPublicKey: compressed, BLSPublicKeyEIP2537: eip,
	}
	raw, err := payload.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t,
		"5847525f494e544552434841494e5f5632000000000000066b000021050000000000000007000000006553f1000111111111111111111111111111111111111111110030a695ad325dfc7e1191fbc9f186f58eff42a634029731b18380ff89bf42c464a42cb8ca55b200f051f57f1e1893c687590080000000000000000000000000000000000695ad325dfc7e1191fbc9f186f58eff42a634029731b18380ff89bf42c464a42cb8ca55b200f051f57f1e1893c687590000000000000000000000000000000010ea7912ef7a227c01298a7c7a96b1851b23021741c71938f39638b1d368aaa621452426b5d8199773a2cb5b2743a5da",
		hex.EncodeToString(raw),
	)
}

func TestBootstrapPayloadCanonicalVector(t *testing.T) {
	compressed, err := hex.DecodeString("a695ad325dfc7e1191fbc9f186f58eff42a634029731b18380ff89bf42c464a42cb8ca55b200f051f57f1e1893c68759")
	require.NoError(t, err)
	eip, err := hex.DecodeString("000000000000000000000000000000000695ad325dfc7e1191fbc9f186f58eff42a634029731b18380ff89bf42c464a42cb8ca55b200f051f57f1e1893c687590000000000000000000000000000000010ea7912ef7a227c01298a7c7a96b1851b23021741c71938f39638b1d368aaa621452426b5d8199773a2cb5b2743a5da")
	require.NoError(t, err)

	raw, err := MarshalBootstrapPayload(
		1643, 8453,
		types.StringToAddress("0x1111111111111111111111111111111111111111"),
		compressed, eip,
	)
	require.NoError(t, err)
	require.Equal(t,
		"5847525f494e544552434841494e5f424f4f5453545241505f5631000000000000066b0000210511111111111111111111111111111111111111110030a695ad325dfc7e1191fbc9f186f58eff42a634029731b18380ff89bf42c464a42cb8ca55b200f051f57f1e1893c687590080000000000000000000000000000000000695ad325dfc7e1191fbc9f186f58eff42a634029731b18380ff89bf42c464a42cb8ca55b200f051f57f1e1893c687590000000000000000000000000000000010ea7912ef7a227c01298a7c7a96b1851b23021741c71938f39638b1d368aaa621452426b5d8199773a2cb5b2743a5da",
		hex.EncodeToString(raw),
	)
}


func TestCheckpointPayloadRoundTripAndCanonicalVector(t *testing.T) {
	payload := CheckpointPayload{
		OriginChainID:     1643,
		DestinationDomain: 8453,
		SetID:             7,
		Mailbox:           types.StringToAddress("0x1111111111111111111111111111111111111111"),
		MerkleTreeHook:    types.StringToAddress("0x2222222222222222222222222222222222222222"),
		Root:              types.StringToHash("0x3333333333333333333333333333333333333333333333333333333333333333"),
		Index:             42,
	}
	raw, err := payload.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t,
		"5847525f494e544552434841494e5f434845434b504f494e545f5631000000000000066b0000210500000000000000071111111111111111111111111111111111111111222222222222222222222222222222222222222233333333333333333333333333333333333333333333333333333333333333330000002a",
		hex.EncodeToString(raw),
	)

	var decoded CheckpointPayload
	require.NoError(t, decoded.UnmarshalBinary(raw))
	require.Equal(t, payload, decoded)
}

func TestCheckpointPayloadRejectsIncompleteContext(t *testing.T) {
	_, err := (CheckpointPayload{
		OriginChainID:     1643,
		DestinationDomain: 8453,
		SetID:             1,
		Mailbox:           types.StringToAddress("0x1111111111111111111111111111111111111111"),
		MerkleTreeHook:    types.StringToAddress("0x2222222222222222222222222222222222222222"),
		Index:             1,
	}).MarshalBinary()
	require.Error(t, err)
	require.Contains(t, err.Error(), "root")
}
