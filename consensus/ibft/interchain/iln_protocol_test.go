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
