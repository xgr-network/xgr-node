package e2e

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/e2e/framework"
	"github.com/xgr-network/xgr-node/types"
)

func TestPoS_StakingV2_CrossLayerLifecycle_ConsensusSurvives(t *testing.T) {
	const epochSize = uint64(10)

	servers, keys, addrs, manager := setupStakingV2Cluster(t, 5, epochSize, 1, 5)
	knownStakers := append([]types.Address(nil), addrs...)

	waitForNextEpochTransition(t, servers, epochSize)
	require.Empty(t, framework.WaitForServersToSeal(servers, epochSize*2+2))
	waitForAllServersSameHead(t, servers)

	initialSnapshot := mustGetLatestSnapshotValidators(t, servers[0])
	require.Len(t, initialSnapshot, 5)
	for _, addr := range addrs {
		assert.Contains(t, initialSnapshot, addr)
	}
	initialValidatorSet, err := framework.GetValidatorSet(addrs[0], servers[0].JSONRPC())
	require.NoError(t, err)
	require.Len(t, initialValidatorSet, 5)

	// Stop one validator immediately after an epoch transition. The remaining four
	// validators must keep sealing while microepoch liveness/weight decay observes
	// the missing validator.
	waitForNextEpochTransition(t, servers, epochSize)
	offlineIdx := 4
	manager.StopServer(offlineIdx)
	activeWhileOffline := manager.ActiveServers(offlineIdx)
	heightAfterStop, err := activeWhileOffline[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	require.Empty(t, framework.WaitForServersToSeal(activeWhileOffline, heightAfterStop+6))
	waitForAllServersSameHead(t, activeWhileOffline)
	assertServerLogsDoNotContain(t, servers, criticalConsensusLogPatterns()...)

	// Restart the offline validator and ensure the post-PoS snapshot store is used
	// consistently by the restarted node.
	restartCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	require.NoError(t, manager.GetServer(offlineIdx).Start(restartCtx))
	require.NoError(t, manager.GetServer(offlineIdx).WaitForReady(restartCtx))
	cancel()
	require.Empty(t, framework.WaitForServersToSeal(servers, heightAfterStop+epochSize+8))
	waitForAllServersSameHead(t, servers)
	assertServerLogsDoNotContain(t, servers, criticalConsensusLogPatterns()...)

	// Exercise the one-epoch validator exit path while all validators are online.
	removedIdx := 3
	removed := addrs[removedIdx]
	removedKey := keys[removedIdx]
	waitUntilEpochOffsetAtMost(t, servers[0], epochSize, 1)
	setInactiveReceipt, err := sendSetActiveReceiptTx(servers[removedIdx], removed, removedKey, false)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), setInactiveReceipt.Status)
	deactivatedInfo, err := queryStakerInfo(servers[0], addrs[0], removed)
	require.NoError(t, err)
	require.True(t, deactivatedInfo.Exists)
	require.False(t, deactivatedInfo.Active)
	require.True(t, deactivatedInfo.DeactivatedAtBlock.Sign() > 0)

	heightAfterDeactivate, err := servers[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	require.Equal(t, deactivatedInfo.DeactivatedAtBlock.Uint64()/epochSize, heightAfterDeactivate/epochSize, "same-epoch rejection check must not accidentally cross an epoch boundary")

	sameEpochUnstake, err := framework.UnstakeAmount(removed, removedKey, servers[removedIdx])
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), sameEpochUnstake.Status, "same-epoch validator full unstake must be rejected")

	firstBoundary := ((deactivatedInfo.DeactivatedAtBlock.Uint64() / epochSize) + 1) * epochSize
	require.Empty(t, framework.WaitForServersToSeal(servers, firstBoundary+1))
	fullUnstake, err := framework.UnstakeAmount(removed, removedKey, servers[removedIdx])
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), fullUnstake.Status, "validator full unstake must succeed in E+1")

	removedValidatorInfo, err := queryValidatorInfo(servers[0], addrs[0], removed)
	require.NoError(t, err)
	require.False(t, removedValidatorInfo.Exists)

	require.Eventually(t, func() bool {
		validatorSet, setErr := framework.GetValidatorSet(addrs[0], servers[0].JSONRPC())
		if setErr != nil || len(validatorSet) != 4 {
			return false
		}
		for _, addr := range validatorSet {
			if addr == removed {
				return false
			}
		}

		return true
	}, 2*time.Minute, time.Second)

	waitForServersToAdvanceEpochs(t, servers, epochSize, 3)
	snapshotAfterExit := mustGetLatestSnapshotValidators(t, servers[0])
	require.NotEmpty(t, snapshotAfterExit)
	require.LessOrEqual(t, len(snapshotAfterExit), 4)
	require.NotContains(t, snapshotAfterExit, removed)
	waitForAllServersSameHead(t, servers)
	assertServerLogsDoNotContain(t, servers, criticalConsensusLogPatterns()...)

	// Restart one still-active validator after the full exit to cover consensus
	// recovery against the shrunken validator set.
	restartedActiveIdx := 0
	heightBeforeActiveRestart, err := servers[1].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	manager.StopServer(restartedActiveIdx)
	activeDuringRestart := manager.ActiveServers(restartedActiveIdx)
	require.Empty(t, framework.WaitForServersToSeal(activeDuringRestart, heightBeforeActiveRestart+6))
	restartActiveCtx, cancelActive := context.WithTimeout(context.Background(), 3*time.Minute)
	require.NoError(t, manager.GetServer(restartedActiveIdx).Start(restartActiveCtx))
	require.NoError(t, manager.GetServer(restartedActiveIdx).WaitForReady(restartActiveCtx))
	cancelActive()
	require.Empty(t, framework.WaitForServersToSeal(servers, heightBeforeActiveRestart+10))
	waitForAllServersSameHead(t, servers)
	assertServerLogsDoNotContain(t, servers, criticalConsensusLogPatterns()...)

	// Delegation lifecycle/accounting on a validator that remains active after the
	// validator full exit.
	delegationValidatorIdx := 1
	delegationValidator := addrs[delegationValidatorIdx]
	delegationValidatorKey := keys[delegationValidatorIdx]
	delegatorKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegatorAddr := crypto.PubKeyToAddress(&delegatorKey.PublicKey)
	knownStakers = append(knownStakers, delegatorAddr)

	delegatorMinStake, err := queryDelegatorMinStake(servers[0], delegationValidator)
	require.NoError(t, err)
	delegationAmount := new(big.Int).Mul(delegatorMinStake, big.NewInt(2))
	fundAccount(t, servers[0], delegationValidator, delegationValidatorKey, delegatorAddr, new(big.Int).Mul(delegatorMinStake, big.NewInt(10)))
	require.NoError(t, sendSetValidatorPoolConfigTx(servers[0], delegationValidator, delegationValidatorKey, true, new(big.Int).Mul(delegatorMinStake, big.NewInt(100)), 0))

	delegateReceipt, err := sendDelegateTx(servers[0], delegatorAddr, delegatorKey, delegationValidator, delegationAmount)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), delegateReceipt.Status)
	assertStakingConservation(t, servers[0], delegationValidator, knownStakers)

	waitForNextEpochTransition(t, servers, epochSize)
	delegatorInfo, err := queryStakerInfo(servers[0], delegationValidator, delegatorAddr)
	require.NoError(t, err)
	require.True(t, delegatorInfo.Exists)
	require.True(t, delegatorInfo.Active)
	require.Equal(t, 0, delegatorInfo.Amount.Cmp(delegationAmount))
	delegatedRaw, err := queryValidatorDelegatedStakeRaw(servers[0], delegationValidator, delegationValidator)
	require.NoError(t, err)
	delegatedActive, err := queryValidatorDelegatedStakeActive(servers[0], delegationValidator, delegationValidator)
	require.NoError(t, err)
	require.Equal(t, 0, delegatedRaw.Cmp(delegationAmount))
	require.Equal(t, 0, delegatedActive.Cmp(delegationAmount))

	waitUntilMidEpoch(t, servers[0], epochSize)
	require.NoError(t, sendSetDelegationActiveTx(servers[0], delegatorAddr, delegatorKey, delegationValidator, false))
	waitForNextEpochTransition(t, servers, epochSize)
	inactiveDelegatorInfo, err := queryStakerInfo(servers[0], delegationValidator, delegatorAddr)
	require.NoError(t, err)
	require.True(t, inactiveDelegatorInfo.Exists)
	require.False(t, inactiveDelegatorInfo.Active)
	rawBeforeDelegationUnstake, err := queryValidatorDelegatedStakeRaw(servers[0], delegationValidator, delegationValidator)
	require.NoError(t, err)
	activeBeforeDelegationUnstake, err := queryValidatorDelegatedStakeActive(servers[0], delegationValidator, delegationValidator)
	require.NoError(t, err)

	unstakeDelegationReceipt, err := sendUnstakeDelegationTx(servers[0], delegatorAddr, delegatorKey, delegationValidator)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), unstakeDelegationReceipt.Status)
	delegatorInfoAfterUnstake, err := queryStakerInfo(servers[0], delegationValidator, delegatorAddr)
	require.NoError(t, err)
	require.False(t, delegatorInfoAfterUnstake.Exists)
	require.False(t, validatorHasStaker(t, servers[0], delegationValidator, delegationValidator, delegatorAddr))
	rawAfterDelegationUnstake, err := queryValidatorDelegatedStakeRaw(servers[0], delegationValidator, delegationValidator)
	require.NoError(t, err)
	activeAfterDelegationUnstake, err := queryValidatorDelegatedStakeActive(servers[0], delegationValidator, delegationValidator)
	require.NoError(t, err)
	require.Equal(t, 0, rawAfterDelegationUnstake.Cmp(new(big.Int).Sub(rawBeforeDelegationUnstake, inactiveDelegatorInfo.Amount)))
	require.Equal(t, 0, activeAfterDelegationUnstake.Cmp(activeBeforeDelegationUnstake), "inactive full delegation unstake must not change active delegated stake")
	assertStakingConservation(t, servers[0], delegationValidator, knownStakers)

	waitForNextEpochTransition(t, servers, epochSize)
	waitForAllServersSameHead(t, servers)
	finalValidatorSet, err := framework.GetValidatorSet(addrs[0], servers[0].JSONRPC())
	require.NoError(t, err)
	require.Len(t, finalValidatorSet, 4)
	require.NotContains(t, finalValidatorSet, removed)
	finalSnapshot := mustGetLatestSnapshotValidators(t, servers[0])
	require.NotEmpty(t, finalSnapshot)
	require.LessOrEqual(t, len(finalSnapshot), 4)
	require.NotContains(t, finalSnapshot, removed)
	assertServerLogsDoNotContain(t, servers, criticalConsensusLogPatterns()...)
}

func waitForAllServersSameHead(t *testing.T, servers []*framework.TestServer) {
	t.Helper()

	require.Eventually(t, func() bool {
		if len(servers) == 0 {
			return true
		}

		var expectedHeight uint64
		var expectedHash ethgo.Hash
		for idx, srv := range servers {
			height, err := srv.JSONRPC().Eth().BlockNumber()
			if err != nil {
				return false
			}
			block, err := srv.JSONRPC().Eth().GetBlockByNumber(ethgo.BlockNumber(height), false)
			if err != nil || block == nil {
				return false
			}
			if idx == 0 {
				expectedHeight = height
				expectedHash = block.Hash
				continue
			}
			if height != expectedHeight || block.Hash != expectedHash {
				return false
			}
		}

		return true
	}, 2*time.Minute, time.Second)
}

func waitForServersToAdvanceEpochs(t *testing.T, servers []*framework.TestServer, epochSize uint64, epochs uint64) {
	t.Helper()

	for i := uint64(0); i < epochs; i++ {
		waitForNextEpochTransition(t, servers, epochSize)
	}
}

func criticalConsensusLogPatterns() []string {
	return []string{
		"missing effective stake",
		"unauthorized proposer",
		"unable to build proposal",
		"failed to run sequence",
		"panic",
	}
}
