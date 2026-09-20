package interchain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	interchainRuntime "github.com/xgr-network/xgr-node/interchain/runtime"
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
