package e2e

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/e2e/framework"
	"github.com/xgr-network/xgr-node/types"
)

func TestPoS_AllValidatorsStoppedAndRestarted_Tier1_ResumesDeterministically(t *testing.T) {
	servers, _, addrs, manager := setupStakingV2Cluster(t, 4, 6, 3, 5)

	require.Empty(t, framework.WaitForServersToSeal(servers, 20))
	heightBeforeStop, err := servers[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)

	manager.StopServers()

	restartCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	manager.StartServers(restartCtx)

	require.Empty(t, framework.WaitForServersToSeal(servers, heightBeforeStop+5))
	assertNoPermanentHeightDivergence(t, servers)

	snap := mustGetLatestSnapshotValidators(t, servers[0])
	require.GreaterOrEqual(t, len(snap), 3)
	for _, snapAddr := range snap {
		assert.Contains(t, addrs, snapAddr)
	}
}

func TestPoS_AllValidatorsStoppedAndRestarted_Tier2_ResumesDeterministically(t *testing.T) {
	servers, keys, addrs, manager := setupStakingV2ClusterNoAutoStake(t, 4, 6, 3, 5)

	minStake, err := queryMinStake(servers[0], addrs[0])
	require.NoError(t, err)
	assertStakePreconditionsNoAutoStake(t, "tier2", servers[0], addrs[0], addrs[0], minStake)
	assertStakePreconditionsNoAutoStake(t, "tier2", servers[1], addrs[1], addrs[1], minStake)
	stakeAmountWithContext(t, "tier2", servers[0], addrs[0], keys[0], minStake)
	stakeAmountWithContext(t, "tier2", servers[1], addrs[1], keys[1], minStake)
	waitForNextEpochTransition(t, servers, 6)

	countTier1, countTier2, _ := countSelectionTiersFromValidatorList(t, servers[0], addrs[0], minStake)
	require.Less(t, countTier1, uint64(3))
	require.GreaterOrEqual(t, countTier2, uint64(3))
	require.Contains(t, mustGetLatestSnapshotValidators(t, servers[0]), addrs[3])

	heightBeforeStop, err := servers[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	manager.StopServers()

	restartCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	manager.StartServers(restartCtx)

	require.Empty(t, framework.WaitForServersToSeal(servers, heightBeforeStop+5))
	assertNoPermanentHeightDivergence(t, servers)

	countTier1After, countTier2After, _ := countSelectionTiersFromValidatorList(t, servers[0], addrs[0], minStake)
	require.Less(t, countTier1After, uint64(3))
	require.GreaterOrEqual(t, countTier2After, uint64(3))
	assert.Contains(t, mustGetLatestSnapshotValidators(t, servers[0]), addrs[3])
}

func TestPoS_AllValidatorsStoppedAndRestarted_Tier3_ResumesDeterministically(t *testing.T) {
	servers, keys, addrs, manager := setupStakingV2ClusterNoAutoStake(t, 4, 6, 3, 5)

	minStake, err := queryMinStake(servers[0], addrs[0])
	require.NoError(t, err)
	assertStakePreconditionsNoAutoStake(t, "tier3", servers[0], addrs[0], addrs[0], minStake)
	assertStakePreconditionsNoAutoStake(t, "tier3", servers[1], addrs[1], addrs[1], minStake)
	assertStakePreconditionsNoAutoStake(t, "tier3", servers[2], addrs[2], addrs[2], minStake)
	stakeAmountWithContext(t, "tier3", servers[0], addrs[0], keys[0], minStake)
	stakeAmountWithContext(t, "tier3", servers[1], addrs[1], keys[1], minStake)
	stakeAmountWithContext(t, "tier3", servers[2], addrs[2], keys[2], minStake)
	waitForNextEpochTransition(t, servers, 6)

	require.NoError(t, sendSetActiveTx(servers[1], addrs[1], keys[1], false))
	require.NoError(t, sendSetActiveTx(servers[2], addrs[2], keys[2], false))
	waitForNextEpochTransition(t, servers, 6)

	countTier1, countTier2, countTier3 := countSelectionTiersFromValidatorList(t, servers[0], addrs[0], minStake)
	require.Less(t, countTier1, uint64(3))
	require.Less(t, countTier2, uint64(3))
	require.GreaterOrEqual(t, countTier3, uint64(3))
	require.Contains(t, mustGetLatestSnapshotValidators(t, servers[0]), addrs[1])

	heightBeforeStop, err := servers[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	manager.StopServers()

	restartCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	manager.StartServers(restartCtx)

	require.Empty(t, framework.WaitForServersToSeal(servers, heightBeforeStop+5))
	assertNoPermanentHeightDivergence(t, servers)

	countTier1After, countTier2After, countTier3After := countSelectionTiersFromValidatorList(t, servers[0], addrs[0], minStake)
	require.Less(t, countTier1After, uint64(3))
	require.Less(t, countTier2After, uint64(3))
	require.GreaterOrEqual(t, countTier3After, uint64(3))
	assert.Contains(t, mustGetLatestSnapshotValidators(t, servers[0]), addrs[1])
}

func TestPoS_StoppedValidatorCatchesUpToRunningChain_DeterministicSnapshotAndTierCounts(t *testing.T) {
	servers, keys, addrs, manager := setupStakingV2ClusterNoAutoStake(t, 4, 6, 3, 5)

	minStake, err := queryMinStake(servers[0], addrs[0])
	require.NoError(t, err)
	assertStakePreconditionsNoAutoStake(t, "catchup", servers[0], addrs[0], addrs[0], minStake)
	assertStakePreconditionsNoAutoStake(t, "catchup", servers[1], addrs[1], addrs[1], minStake)
	assertStakePreconditionsNoAutoStake(t, "catchup", servers[2], addrs[2], addrs[2], minStake)
	stakeAmountWithContext(t, "catchup", servers[0], addrs[0], keys[0], minStake)
	stakeAmountWithContext(t, "catchup", servers[1], addrs[1], keys[1], minStake)
	stakeAmountWithContext(t, "catchup", servers[2], addrs[2], keys[2], minStake)
	waitForNextEpochTransition(t, servers, 6)

	offlineIdx := 3
	manager.StopServer(offlineIdx)

	activeServers := manager.ActiveServers(offlineIdx)
	heightBeforeCatchup, err := activeServers[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	require.Empty(t, framework.WaitForServersToSeal(activeServers, heightBeforeCatchup+8))

	restartCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, manager.GetServer(offlineIdx).Start(restartCtx))
	require.NoError(t, manager.GetServer(offlineIdx).WaitForReady(restartCtx))

	assert.Eventually(t, func() bool {
		offlineHeight, offlineErr := servers[offlineIdx].JSONRPC().Eth().BlockNumber()
		if offlineErr != nil {
			return false
		}

		for i := range servers {
			if i == offlineIdx {
				continue
			}
			h, err := servers[i].JSONRPC().Eth().BlockNumber()
			if err != nil {
				return false
			}
			if h > offlineHeight+1 {
				return false
			}
		}

		return true
	}, 2*time.Minute, time.Second)

	baselineSnap := mustGetLatestSnapshotValidators(t, servers[0])
	for i := 1; i < len(servers); i++ {
		assert.ElementsMatch(t, baselineSnap, mustGetLatestSnapshotValidators(t, servers[i]))
	}

	t1Baseline, t2Baseline, t3Baseline := countSelectionTiersFromValidatorList(t, servers[0], addrs[0], minStake)
	t1Restarted, t2Restarted, t3Restarted := countSelectionTiersFromValidatorList(t, servers[offlineIdx], addrs[0], minStake)
	assert.Equal(t, t1Baseline, t1Restarted)
	assert.Equal(t, t2Baseline, t2Restarted)
	assert.Equal(t, t3Baseline, t3Restarted)
}

func assertNoPermanentHeightDivergence(t *testing.T, servers []*framework.TestServer) {
	t.Helper()

	assert.Eventually(t, func() bool {
		minHeight := uint64(^uint64(0))
		maxHeight := uint64(0)

		for _, srv := range servers {
			h, err := srv.JSONRPC().Eth().BlockNumber()
			if err != nil {
				return false
			}
			if h < minHeight {
				minHeight = h
			}
			if h > maxHeight {
				maxHeight = h
			}
		}

		return maxHeight-minHeight <= 1
	}, 2*time.Minute, time.Second)
}

type stakePreconditions struct {
	blockNumber       uint64
	balance           *big.Int
	accountStake      *big.Int
	validatorInfo     *stakingValidatorInfo
	thresholdMet      bool
	joinEffectiveAt   *big.Int
	diagnosticContext string
}

func readStakePreconditions(
	t *testing.T,
	srv *framework.TestServer,
	queryFrom types.Address,
	validator types.Address,
	contextLabel string,
) *stakePreconditions {
	t.Helper()

	blockNumber, err := srv.JSONRPC().Eth().BlockNumber()
	require.NoError(t, err, "failed to read block number (%s)", contextLabel)

	balance := framework.GetAccountBalance(t, validator, srv.JSONRPC())

	accountStake, err := framework.GetAccountStake(validator, srv.JSONRPC())
	require.NoError(t, err, "failed to read accountStake (%s)", contextLabel)

	info, err := queryValidatorInfo(srv, queryFrom, validator)
	require.NoError(t, err, "failed to read validatorInfo (%s)", contextLabel)

	thresholdMet, err := queryIsValidatorThresholdMet(srv, queryFrom, validator)
	require.NoError(t, err, "failed to read isValidatorThresholdMet (%s)", contextLabel)

	joinEffectiveAt, err := queryJoinEffectiveAtBlock(srv, queryFrom, validator)
	require.NoError(t, err, "failed to read joinEffectiveAtBlock (%s)", contextLabel)

	return &stakePreconditions{
		blockNumber:       blockNumber,
		balance:           balance,
		accountStake:      accountStake,
		validatorInfo:     info,
		thresholdMet:      thresholdMet,
		joinEffectiveAt:   joinEffectiveAt,
		diagnosticContext: contextLabel,
	}
}

func assertStakePreconditionsNoAutoStake(
	t *testing.T,
	testLabel string,
	srv *framework.TestServer,
	queryFrom types.Address,
	validator types.Address,
	minStake *big.Int,
) {
	t.Helper()

	ctx := fmt.Sprintf("%s pre-stake validator=%s", testLabel, validator.String())
	pre := readStakePreconditions(t, srv, queryFrom, validator, ctx)
	t.Logf(
		"[%s] block=%d balance=%s accountStake=%s exists=%v active=%v thresholdMet=%v joinEffectiveAt=%s stakedAmount=%s deactivatedAt=%s minStake=%s",
		ctx,
		pre.blockNumber,
		pre.balance.String(),
		pre.accountStake.String(),
		pre.validatorInfo.Exists,
		pre.validatorInfo.Active,
		pre.thresholdMet,
		pre.joinEffectiveAt.String(),
		pre.validatorInfo.StakedAmount.String(),
		pre.validatorInfo.DeactivatedAtBlock.String(),
		minStake.String(),
	)

	require.GreaterOrEqualf(t, pre.balance.Cmp(minStake), 0, "[%s] insufficient balance for stake", ctx)
	require.Lessf(t, pre.accountStake.Cmp(minStake), 0, "[%s] account already has accountStake >= minStake before first stake", ctx)
	require.Truef(t, pre.validatorInfo.Exists, "[%s] validator entry must exist before staking top-up", ctx)
	require.Falsef(t, pre.thresholdMet, "[%s] threshold already met before first stake", ctx)
}

func stakeAmountWithContext(
	t *testing.T,
	testLabel string,
	srv *framework.TestServer,
	validator types.Address,
	key *ecdsa.PrivateKey,
	amount *big.Int,
) {
	t.Helper()

	ctx := fmt.Sprintf("%s stake validator=%s", testLabel, validator.String())
	pre := readStakePreconditions(t, srv, validator, validator, ctx+" pre")
	err := framework.StakeAmount(validator, key, amount, srv)
	if err == nil {
		return
	}

	post := readStakePreconditions(t, srv, validator, validator, ctx+" post")
	require.FailNowf(
		t,
		"StakeAmount reverted",
		"[%s] err=%v amount=%s pre{block=%d balance=%s accountStake=%s exists=%v active=%v thresholdMet=%v joinEffectiveAt=%s stakedAmount=%s deactivatedAt=%s} post{block=%d balance=%s accountStake=%s exists=%v active=%v thresholdMet=%v joinEffectiveAt=%s stakedAmount=%s deactivatedAt=%s}",
		ctx,
		err,
		amount.String(),
		pre.blockNumber,
		pre.balance.String(),
		pre.accountStake.String(),
		pre.validatorInfo.Exists,
		pre.validatorInfo.Active,
		pre.thresholdMet,
		pre.joinEffectiveAt.String(),
		pre.validatorInfo.StakedAmount.String(),
		pre.validatorInfo.DeactivatedAtBlock.String(),
		post.blockNumber,
		post.balance.String(),
		post.accountStake.String(),
		post.validatorInfo.Exists,
		post.validatorInfo.Active,
		post.thresholdMet,
		post.joinEffectiveAt.String(),
		post.validatorInfo.StakedAmount.String(),
		post.validatorInfo.DeactivatedAtBlock.String(),
	)
}
