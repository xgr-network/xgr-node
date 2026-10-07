package interchain

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/interchain/evm"
	interchainRuntime "github.com/xgr-network/xgr-node/interchain/runtime"
	"github.com/xgr-network/xgr-node/types"
)

func TestSetActiveRequiresExplicitActiveFlag(t *testing.T) {
	cmd := getSetActiveCommand()
	require.NoError(t, cmd.Flags().Set("data-dir", t.TempDir()))
	require.NoError(t, cmd.Flags().Set("chain", "base"))

	err := cmd.PreRunE(cmd, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--active must be explicitly set")
}

func TestSetActiveRejectsMissingDataDir(t *testing.T) {
	cmd := getSetActiveCommand()
	require.NoError(t, cmd.Flags().Set("chain", "base"))
	require.NoError(t, cmd.Flags().Set("active", "true"))

	err := cmd.PreRunE(cmd, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--data-dir")
}

func TestSetActiveParsesFalse(t *testing.T) {
	cmd := getSetActiveCommand()
	require.NoError(t, cmd.Flags().Set("data-dir", t.TempDir()))
	require.NoError(t, cmd.Flags().Set("chain", "base"))
	require.NoError(t, cmd.Flags().Set("active", "false"))

	require.NoError(t, cmd.PreRunE(cmd, nil))
}

func TestExecuteSetActiveReturnsFinalDestinationState(t *testing.T) {
	old := enqueueAndWait
	defer func() { enqueueAndWait = old }()

	enqueueAndWait = func(dataDir, chain string, active bool, timeout time.Duration) (*interchainRuntime.RequestResult, error) {
		require.Equal(t, "/node", dataDir)
		require.Equal(t, "base", chain)
		require.True(t, active)
		require.Equal(t, time.Minute, timeout)
		return &interchainRuntime.RequestResult{
			Done: true, Validator: "0x1000000000000000000000000000000000000001",
			Active: true, Changed: true, TxHash: "0xabc", SetID: 8,
		}, nil
	}

	result, err := executeSetActive(&setActiveParams{
		dataDir: "/node", chain: "BASE", active: true, timeout: time.Minute,
	})
	require.NoError(t, err)
	require.True(t, result.DestinationCommit)
	require.True(t, result.FinalActive)
	require.True(t, result.Changed)
	require.Equal(t, uint64(8), result.SetID)
	require.Equal(t, "0xabc", result.TransactionHash)
}


func TestProposalCreateRequiresSourceAndDestination(t *testing.T) {
	cmd := getProposalCreateCommand()
	require.NoError(t, cmd.Flags().Set("data-dir", t.TempDir()))
	require.NoError(t, cmd.Flags().Set("type", "fee-update"))
	require.NoError(t, cmd.Flags().Set("nonce", "1"))
	require.NoError(t, cmd.Flags().Set("fee-wei", "10"))

	err := cmd.PreRunE(cmd, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--source and --destination")
}

func TestExecuteProposalCreateDoesNotApprove(t *testing.T) {
	oldSet := getProposalValidatorSet
	oldNonce := getProposalGovernanceNonce
	oldCreate := enqueueGovernanceCreate
	defer func() {
		getProposalValidatorSet = oldSet
		getProposalGovernanceNonce = oldNonce
		enqueueGovernanceCreate = oldCreate
	}()

	t.Setenv("XGR_INTERCHAIN_BASE_CHAIN_ID", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_DOMAIN", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_RPC", "https://base.example.invalid")
	t.Setenv("XGR_INTERCHAIN_BASE_ILN_REGISTRY_ADDR", "0x5555555555555555555555555555555555555555")
	t.Setenv("XGR_INTERCHAIN_BASE_REGISTRY_ADDR", "0x6666666666666666666666666666666666666666")
	t.Setenv("XGR_INTERCHAIN_XGR_CHAIN_ID", "1643")
	t.Setenv("XGR_INTERCHAIN_XGR_DOMAIN", "1643")

	getProposalValidatorSet = func(destination *evm.Destination) (*evm.ValidatorSet, error) {
		require.Equal(t, "base", destination.Name)
		return &evm.ValidatorSet{SetID: 9}, nil
	}
	getProposalGovernanceNonce = func(source *evm.Destination, destinationDomain uint32, routeID types.Hash) (uint64, error) {
		require.Equal(t, "base", source.Name)
		require.Equal(t, uint32(1643), destinationDomain)
		require.Equal(t, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", routeID.String())
		return 3, nil
	}
	enqueueGovernanceCreate = func(
		dataDir string,
		proposal protocol.ILNGovernanceProposal,
		timeout time.Duration,
	) (*interchainRuntime.GovernanceRequestResult, error) {
		require.Equal(t, "/node", dataDir)
		require.Equal(t, uint64(9), proposal.SetID)
		require.Equal(t, uint64(4), proposal.Nonce)
		require.Equal(t, uint64(8453), proposal.Route.Key.SourceChainID)
		require.Equal(t, uint32(1643), proposal.Route.Key.DestinationDomain)
		require.Equal(t, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", proposal.Route.Key.RouteID.String())
		require.Equal(t, "0x5555555555555555555555555555555555555555", proposal.Registry.String())
		require.Zero(t, big.NewInt(25).Cmp(proposal.Route.ValidatorFeeWei))
		return &interchainRuntime.GovernanceRequestResult{
			Done: true,
			ProposalID: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Approved: false,
			Quorum: false,
		}, nil
	}

	result, err := executeProposalCreate(&proposalCreateParams{
		dataDir: "/node",
		source: "base",
		destination: "xgr",
		proposalType: "fee-update",
		ttl: 5 * time.Minute,
		feeWei: "25",
		routeID: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		timeout: time.Minute,
	})
	require.NoError(t, err)
	require.False(t, result.Approved)
	require.False(t, result.Quorum)
}



func TestProposalCreateAllowsAutomaticNonce(t *testing.T) {
	cmd := getProposalCreateCommand()
	require.NoError(t, cmd.Flags().Set("data-dir", t.TempDir()))
	require.NoError(t, cmd.Flags().Set("source", "base"))
	require.NoError(t, cmd.Flags().Set("destination", "xgr"))
	require.NoError(t, cmd.Flags().Set("type", "fee-update"))
	require.NoError(t, cmd.Flags().Set("route-id", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	require.NoError(t, cmd.Flags().Set("fee-wei", "10"))
	require.NoError(t, cmd.PreRunE(cmd, nil))
}

func TestExecuteProposalCreateRejectsStaleNonceOverride(t *testing.T) {
	oldSet := getProposalValidatorSet
	oldNonce := getProposalGovernanceNonce
	defer func() {
		getProposalValidatorSet = oldSet
		getProposalGovernanceNonce = oldNonce
	}()

	t.Setenv("XGR_INTERCHAIN_BASE_CHAIN_ID", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_DOMAIN", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_RPC", "https://base.example.invalid")
	t.Setenv("XGR_INTERCHAIN_BASE_ILN_REGISTRY_ADDR", "0x5555555555555555555555555555555555555555")
	t.Setenv("XGR_INTERCHAIN_BASE_REGISTRY_ADDR", "0x6666666666666666666666666666666666666666")
	t.Setenv("XGR_INTERCHAIN_XGR_CHAIN_ID", "1643")
	t.Setenv("XGR_INTERCHAIN_XGR_DOMAIN", "1643")

	getProposalValidatorSet = func(*evm.Destination) (*evm.ValidatorSet, error) {
		return &evm.ValidatorSet{SetID: 9}, nil
	}
	getProposalGovernanceNonce = func(*evm.Destination, uint32, types.Hash) (uint64, error) {
		return 3, nil
	}

	_, err := executeProposalCreate(&proposalCreateParams{
		dataDir: "/node",
		source: "base",
		destination: "xgr",
		proposalType: "fee-update",
		nonce: 3,
		ttl: 5 * time.Minute,
		feeWei: "25",
		routeID: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		timeout: time.Minute,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "confirmed next route governance nonce 4")
}

func TestProposalApproveIsSeparateCommand(t *testing.T) {
	cmd := getProposalApproveCommand()
	require.NoError(t, cmd.Flags().Set("data-dir", t.TempDir()))
	require.NoError(t, cmd.Flags().Set(
		"proposal-id",
		"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	))
	require.NoError(t, cmd.PreRunE(cmd, nil))
}


func TestProposalRouteAddRequiresSourceRouter(t *testing.T) {
	cmd := getProposalCreateCommand()
	require.NoError(t, cmd.Flags().Set("data-dir", t.TempDir()))
	require.NoError(t, cmd.Flags().Set("source", "base"))
	require.NoError(t, cmd.Flags().Set("destination", "xgr"))
	require.NoError(t, cmd.Flags().Set("type", "route-add"))
	require.NoError(t, cmd.Flags().Set("nonce", "1"))
	require.NoError(t, cmd.Flags().Set("route-id", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	require.NoError(t, cmd.Flags().Set("gateway", "0x1111111111111111111111111111111111111111"))
	require.NoError(t, cmd.Flags().Set("mailbox", "0x2222222222222222222222222222222222222222"))
	require.NoError(t, cmd.Flags().Set("merkle-tree-hook", "0x3333333333333333333333333333333333333333"))
	require.NoError(t, cmd.Flags().Set("destination-router", "0x4444444444444444444444444444444444444444"))
	require.NoError(t, cmd.Flags().Set("fee-wei", "10"))

	err := cmd.PreRunE(cmd, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--source-router")
}
