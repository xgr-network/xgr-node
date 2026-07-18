package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/xgr-network/xgr-node/e2e/framework"
	"github.com/xgr-network/xgr-node/types"
)

func TestPoS_StakingV2_ValidatorFullUnstakeAfterOneEpoch_DoesNotBreakConsensus(t *testing.T) {
	const epochSize = uint64(8)

	servers, keys, addrs, manager := setupStakingV2Cluster(t, 4, epochSize, 1, 4)
	waitForNextEpochTransition(t, servers, epochSize)

	removedIdx := 3
	removed := addrs[removedIdx]
	removedKey := keys[removedIdx]

	waitUntilEpochOffsetAtMost(t, servers[0], epochSize, 1)
	receipt, err := sendSetActiveReceiptTx(servers[removedIdx], removed, removedKey, false)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)
	deactivatedInfo, err := queryStakerInfo(servers[0], addrs[0], removed)
	require.NoError(t, err)
	require.False(t, deactivatedInfo.Active)
	require.True(t, deactivatedInfo.DeactivatedAtBlock.Sign() > 0)

	receipt, err = framework.UnstakeAmount(removed, removedKey, servers[removedIdx])
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status, "same-epoch validator full unstake must be rejected")

	firstBoundary := ((deactivatedInfo.DeactivatedAtBlock.Uint64() / epochSize) + 1) * epochSize
	require.Empty(t, framework.WaitForServersToSeal(servers, firstBoundary))
	receipt, err = framework.UnstakeAmount(removed, removedKey, servers[removedIdx])
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status, "validator full unstake must succeed at the first next epoch boundary")

	heightAfterUnstake, err := servers[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	require.Empty(t, framework.WaitForServersToSealWithTimeout(
		servers,
		heightAfterUnstake+(epochSize*3)+2,
		2*time.Minute,
	))
	assertNoPermanentHeightDivergence(t, servers)
	assertCommonHeadHashAtLowestHeight(t, servers)

	snapshot := mustGetLatestSnapshotValidators(t, servers[0])
	require.Len(t, snapshot, 3)
	require.NotContains(t, snapshot, removed)

	validatorSet, err := framework.GetValidatorSet(addrs[0], servers[0].JSONRPC())
	require.NoError(t, err)
	require.Len(t, validatorSet, 3)
	require.NotContains(t, validatorSet, removed)

	// Restart one remaining validator after the full exit to cover SnapshotValidatorStore recovery.
	restartIdx := 0
	heightBeforeRestart, err := servers[1].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	manager.StopServer(restartIdx)
	restartCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, servers[restartIdx].Start(restartCtx))
	require.Empty(t, framework.WaitForServersToSeal(servers, heightBeforeRestart+epochSize+2))
	assertNoPermanentHeightDivergence(t, servers)
	assertCommonHeadHashAtLowestHeight(t, servers)

	assertServerLogsDoNotContain(t, servers,
		"missing effective stake",
		"unauthorized proposer",
		"failed to run sequence",
		"unable to build proposal",
	)
}

func assertCommonHeadHashAtLowestHeight(t *testing.T, servers []*framework.TestServer) {
	t.Helper()

	minHeight := uint64(^uint64(0))
	for _, srv := range servers {
		height, err := srv.JSONRPC().Eth().BlockNumber()
		require.NoError(t, err)
		if height < minHeight {
			minHeight = height
		}
	}

	var expected ethgo.Hash
	for idx, srv := range servers {
		block, err := srv.JSONRPC().Eth().GetBlockByNumber(ethgo.BlockNumber(minHeight), false)
		require.NoError(t, err)
		require.NotNil(t, block)
		if idx == 0 {
			expected = block.Hash
			continue
		}
		assert.Equal(t, expected, block.Hash, "server %d must agree on block hash at height %d", idx, minHeight)
	}
}

func assertServerLogsDoNotContain(t *testing.T, servers []*framework.TestServer, needles ...string) {
	t.Helper()

	for _, srv := range servers {
		logPath := filepath.Join(srv.Config.LogsDir, srv.Config.Name+".log")
		raw, err := os.ReadFile(logPath)
		require.NoError(t, err)
		lower := strings.ToLower(string(raw))
		for _, needle := range needles {
			assert.NotContains(t, lower, strings.ToLower(needle), "%s must not contain consensus safety error %q", logPath, needle)
		}
	}
}
