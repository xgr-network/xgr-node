package interchain

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/types"
)

func TestILNRouteKeyCanonicalVector(t *testing.T) {
	key := ILNRouteKey{
		SourceChainID:     8453,
		SourceDomain:      8453,
		DestinationDomain: 1643,
	}

	raw, err := key.MarshalBinary()
	require.NoError(t, err)
	require.Equal(
		t,
		"5847525f494c4e5f524f5554455f56310000000000002105000021050000066b",
		hex.EncodeToString(raw),
	)

	hashA, err := key.Hash()
	require.NoError(t, err)
	hashB, err := key.Hash()
	require.NoError(t, err)
	require.Equal(t, hashA, hashB)
}

func TestILNGovernanceFeeUpdateRoundTrip(t *testing.T) {
	p := ILNGovernanceProposal{
		Type:       ILNProposalFeeUpdate,
		Registry:   types.StringToAddress("0x5555555555555555555555555555555555555555"),
		SetID:      7,
		Nonce:      12,
		ValidUntil: 1900000000,
		Route: ILNRoute{
			Key: ILNRouteKey{
				SourceChainID:     8453,
				SourceDomain:      8453,
				DestinationDomain: 1643,
			},
			ValidatorFeeWei: big.NewInt(50_000_000_000_000),
		},
	}

	raw, err := p.MarshalBinary()
	require.NoError(t, err)

	var decoded ILNGovernanceProposal
	require.NoError(t, decoded.UnmarshalBinary(raw))
	require.Equal(t, p.Type, decoded.Type)
	require.Equal(t, p.SetID, decoded.SetID)
	require.Equal(t, p.Nonce, decoded.Nonce)
	require.Equal(t, p.ValidUntil, decoded.ValidUntil)
	require.Equal(t, p.Route.Key, decoded.Route.Key)
	require.Zero(t, p.Route.ValidatorFeeWei.Cmp(decoded.Route.ValidatorFeeWei))

	id, err := p.ProposalID()
	require.NoError(t, err)
	require.NotEqual(t, types.ZeroHash, id)
}

func TestILNGovernanceRouteAddRoundTrip(t *testing.T) {
	p := ILNGovernanceProposal{
		Type:       ILNProposalRouteAdd,
		Registry:   types.StringToAddress("0x5555555555555555555555555555555555555555"),
		SetID:      3,
		Nonce:      1,
		ValidUntil: 1900000000,
		Route: ILNRoute{
			Key: ILNRouteKey{
				SourceChainID:     8453,
				SourceDomain:      8453,
				DestinationDomain: 1643,
			},
			Gateway:           types.StringToAddress("0x1111111111111111111111111111111111111111"),
			SourceRouter:      types.StringToAddress("0x6666666666666666666666666666666666666666"),
			Mailbox:           types.StringToAddress("0x2222222222222222222222222222222222222222"),
			MerkleTreeHook:    types.StringToAddress("0x3333333333333333333333333333333333333333"),
			DestinationRouter: types.StringToAddress("0x4444444444444444444444444444444444444444"),
			ValidatorFeeWei:   big.NewInt(1),
			Enabled:           true,
		},
	}

	raw, err := p.MarshalBinary()
	require.NoError(t, err)

	var decoded ILNGovernanceProposal
	require.NoError(t, decoded.UnmarshalBinary(raw))
	require.Equal(t, p.Type, decoded.Type)
	require.Equal(t, p.Route.Key, decoded.Route.Key)
	require.Equal(t, p.Route.Gateway, decoded.Route.Gateway)
	require.Equal(t, p.Route.SourceRouter, decoded.Route.SourceRouter)
	require.Equal(t, p.Route.Mailbox, decoded.Route.Mailbox)
	require.Equal(t, p.Route.MerkleTreeHook, decoded.Route.MerkleTreeHook)
	require.Equal(t, p.Route.DestinationRouter, decoded.Route.DestinationRouter)
	require.True(t, decoded.Route.Enabled)
	require.Zero(t, p.Route.ValidatorFeeWei.Cmp(decoded.Route.ValidatorFeeWei))
}

func TestILNGovernanceRouteStateRejectsExtraFields(t *testing.T) {
	p := ILNGovernanceProposal{
		Type:       ILNProposalRouteDisable,
		Registry:   types.StringToAddress("0x5555555555555555555555555555555555555555"),
		SetID:      1,
		Nonce:      1,
		ValidUntil: 1900000000,
		Route: ILNRoute{
			Key: ILNRouteKey{
				SourceChainID:     8453,
				SourceDomain:      8453,
				DestinationDomain: 1643,
			},
			Gateway: types.StringToAddress("0x1111111111111111111111111111111111111111"),
		},
	}

	_, err := p.MarshalBinary()
	require.Error(t, err)
	require.Contains(t, err.Error(), "only route key")
}

func TestILNGovernanceRejectsMissingReplayContext(t *testing.T) {
	p := ILNGovernanceProposal{
		Type:       ILNProposalFeeUpdate,
		Registry:   types.StringToAddress("0x5555555555555555555555555555555555555555"),
		SetID:      1,
		ValidUntil: 1900000000,
		Route: ILNRoute{
			Key: ILNRouteKey{
				SourceChainID:     8453,
				SourceDomain:      8453,
				DestinationDomain: 1643,
			},
			ValidatorFeeWei: big.NewInt(1),
		},
	}

	_, err := p.MarshalBinary()
	require.Error(t, err)
	require.Contains(t, err.Error(), "nonce")
}

func TestILNGovernanceProposalIDDifferentiatesNonce(t *testing.T) {
	base := ILNGovernanceProposal{
		Type:       ILNProposalFeeUpdate,
		Registry:   types.StringToAddress("0x5555555555555555555555555555555555555555"),
		SetID:      1,
		Nonce:      1,
		ValidUntil: 1900000000,
		Route: ILNRoute{
			Key: ILNRouteKey{
				SourceChainID:     8453,
				SourceDomain:      8453,
				DestinationDomain: 1643,
			},
			ValidatorFeeWei: big.NewInt(1),
		},
	}
	other := base
	other.Nonce = 2

	id1, err := base.ProposalID()
	require.NoError(t, err)
	id2, err := other.ProposalID()
	require.NoError(t, err)
	require.NotEqual(t, id1, id2)
}


func TestILNCheckpointRoundTrip(t *testing.T) {
	p := ILNCheckpointPayload{
		SourceChainID:     8453,
		SourceDomain:      8453,
		DestinationDomain: 1643,
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
