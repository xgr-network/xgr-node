package interchain

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/types"
)

var testILNRouteID = types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

func TestILNRouteKeyCanonicalVector(t *testing.T) {
	key := ILNRouteKey{
		SourceChainID:     8453,
		SourceDomain:      8453,
		DestinationDomain: 1643,
		RouteID:           testILNRouteID,
	}

	raw, err := key.MarshalBinary()
	require.NoError(t, err)
	require.Equal(
		t,
		"5847525f494c4e5f524f5554455f56320000000000002105000021050000066baaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		hex.EncodeToString(raw),
	)

	hashA, err := key.Hash()
	require.NoError(t, err)
	hashB, err := key.Hash()
	require.NoError(t, err)
	require.Equal(t, hashA, hashB)
}

func TestILNSourceFeeV315CanonicalVector(t *testing.T) {
	p := ILNSourceFeeProposal{
		SourceChainID: 8453, SourceDomain: 8453,
		Registry: types.StringToAddress("0x5555555555555555555555555555555555555555"),
		SetID: 7, Nonce: 12, ValidUntil: 1900000000,
		ValidatorFeeWei: big.NewInt(50_000_000_000_000),
	}
	raw, err := p.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, "584954415f534f555243455f4645455f5633313500000000000021050000210555555555555555555555555555555555555555550000000000000007000000000000000c00000000713fb30000000000000000000000000000000000000000000000000000002d79883d2000", hex.EncodeToString(raw))
	var decoded ILNSourceFeeProposal
	require.NoError(t, decoded.UnmarshalBinary(raw))
	require.Equal(t, p.SourceChainID, decoded.SourceChainID)
	require.Equal(t, p.SourceDomain, decoded.SourceDomain)
	require.Equal(t, p.Registry, decoded.Registry)
	require.Equal(t, p.SetID, decoded.SetID)
	require.Equal(t, p.Nonce, decoded.Nonce)
	require.Zero(t, p.ValidatorFeeWei.Cmp(decoded.ValidatorFeeWei))
	require.Error(t, decoded.UnmarshalBinary(append(raw, 0)))
}

func TestILNSourceFeeProposalRejectsMissingNonceAndReplay(t *testing.T) {
	p := ILNSourceFeeProposal{
		SourceChainID: 8453, SourceDomain: 8453,
		Registry: types.StringToAddress("0x5555555555555555555555555555555555555555"),
		SetID: 7, ValidUntil: 1900000000, ValidatorFeeWei: big.NewInt(1),
	}
	_, err := p.MarshalBinary()
	require.ErrorContains(t, err, "nonce")
	p.Nonce = 1
	first, err := p.ProposalID()
	require.NoError(t, err)
	p.Nonce = 2
	second, err := p.ProposalID()
	require.NoError(t, err)
	require.NotEqual(t, first, second)
}

func TestXITADeterministicAssetAndRouteID(t *testing.T) {
	asset, err := XITACanonicalAssetID(1643, types.ZeroAddress, 0)
	require.NoError(t, err)
	require.NotEqual(t, types.ZeroHash, asset)
	_, err = XITACanonicalAssetID(1643, types.ZeroAddress, 1)
	require.Error(t, err)
	otherAsset, err := XITACanonicalAssetID(8453, types.StringToAddress("0x1111111111111111111111111111111111111111"), 1)
	require.NoError(t, err)
	require.NotEqual(t, asset, otherAsset)
	forward, err := XITADirectedRouteID(asset, 1643, 1643, 8453, 8453)
	require.NoError(t, err)
	again, err := XITADirectedRouteID(asset, 1643, 1643, 8453, 8453)
	require.NoError(t, err)
	require.Equal(t, forward, again)
	reverse, err := XITADirectedRouteID(asset, 8453, 8453, 1643, 1643)
	require.NoError(t, err)
	require.NotEqual(t, forward, reverse)
	another, err := XITADirectedRouteID(otherAsset, 1643, 1643, 8453, 8453)
	require.NoError(t, err)
	require.NotEqual(t, forward, another)
	_, err = XITADirectedRouteID(asset, 8453, 8453, 137, 137)
	require.Error(t, err)
}

func TestILNCheckpointCanonicalVector(t *testing.T) {
	p := ILNCheckpointPayload{
		SourceChainID:       8453,
		SourceDomain:        8453,
		DestinationDomain:   1643,
		RouteID:             testILNRouteID,
		SetID:               9,
		SourceBlockNumber:   123456,
		Registry:            types.StringToAddress("0x5555555555555555555555555555555555555555"),
		Gateway:             types.StringToAddress("0x1111111111111111111111111111111111111111"),
		SourceRouter:        types.StringToAddress("0x6666666666666666666666666666666666666666"),
		Mailbox:             types.StringToAddress("0x2222222222222222222222222222222222222222"),
		MerkleTreeHook:      types.StringToAddress("0x3333333333333333333333333333333333333333"),
		DestinationRouter:   types.StringToAddress("0x4444444444444444444444444444444444444444"),
		ValidatorFeeWei:     big.NewInt(12345),
		AuthorizedMessageID: types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		Root:                types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		Index:               17,
	}

	raw, err := p.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t,
		"5847525f494c4e5f434845434b504f494e545f56320000000000002105000021050000066baaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0000000000000009000000000001e2405555555555555555555555555555555555555555111111111111111111111111111111111111111166666666666666666666666666666666666666662222222222222222222222222222222222222222333333333333333333333333333333333333333344444444444444444444444444444444444444440000000000000000000000000000000000000000000000000000000000003039bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa00000011",
		hex.EncodeToString(raw),
	)
}

func TestILNCheckpointRoundTrip(t *testing.T) {
	p := ILNCheckpointPayload{
		SourceChainID:     8453,
		SourceDomain:      8453,
		DestinationDomain: 1643,
		RouteID:           testILNRouteID,
		SetID:             9,
		SourceBlockNumber: 123456,
		Registry:          types.StringToAddress("0x5555555555555555555555555555555555555555"),
		Gateway:           types.StringToAddress("0x1111111111111111111111111111111111111111"),
		SourceRouter:      types.StringToAddress("0x6666666666666666666666666666666666666666"),
		Mailbox:           types.StringToAddress("0x2222222222222222222222222222222222222222"),
		MerkleTreeHook:    types.StringToAddress("0x3333333333333333333333333333333333333333"),
		DestinationRouter: types.StringToAddress("0x4444444444444444444444444444444444444444"),
		ValidatorFeeWei:   big.NewInt(12345),
		AuthorizedMessageID: types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		Root:              types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		Index:             17,
	}
	raw, err := p.MarshalBinary()
	require.NoError(t, err)

	var decoded ILNCheckpointPayload
	require.NoError(t, decoded.UnmarshalBinary(raw))
	require.Equal(t, p.SourceChainID, decoded.SourceChainID)
	require.Equal(t, p.SourceDomain, decoded.SourceDomain)
	require.Equal(t, p.DestinationDomain, decoded.DestinationDomain)
	require.Equal(t, p.RouteID, decoded.RouteID)
	require.Equal(t, p.SetID, decoded.SetID)
	require.Equal(t, p.SourceBlockNumber, decoded.SourceBlockNumber)
	require.Equal(t, p.Registry, decoded.Registry)
	require.Equal(t, p.Gateway, decoded.Gateway)
	require.Equal(t, p.SourceRouter, decoded.SourceRouter)
	require.Equal(t, p.Mailbox, decoded.Mailbox)
	require.Equal(t, p.MerkleTreeHook, decoded.MerkleTreeHook)
	require.Equal(t, p.DestinationRouter, decoded.DestinationRouter)
	require.Zero(t, p.ValidatorFeeWei.Cmp(decoded.ValidatorFeeWei))
	require.Equal(t, p.AuthorizedMessageID, decoded.AuthorizedMessageID)
	require.Equal(t, p.Root, decoded.Root)
	require.Equal(t, p.Index, decoded.Index)
}


func TestILNRouteKeyDifferentiatesParallelRoutes(t *testing.T) {
	base := ILNRouteKey{
		SourceChainID: 8453,
		SourceDomain: 8453,
		DestinationDomain: 1643,
		RouteID: testILNRouteID,
	}
	other := base
	other.RouteID = types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	baseHash, err := base.Hash()
	require.NoError(t, err)
	otherHash, err := other.Hash()
	require.NoError(t, err)
	require.NotEqual(t, baseHash, otherHash)
}

func TestILNRouteKeyRejectsZeroRouteID(t *testing.T) {
	_, err := (ILNRouteKey{
		SourceChainID: 8453,
		SourceDomain: 8453,
		DestinationDomain: 1643,
	}).MarshalBinary()
	require.Error(t, err)
	require.Contains(t, err.Error(), "route id")
}
