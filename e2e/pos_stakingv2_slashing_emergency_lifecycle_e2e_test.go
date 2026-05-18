package e2e

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/e2e/framework"
	"github.com/xgr-network/xgr-node/types"
)

func TestPoS_StakingV2_SlashingEmergencyLifecycle_ConsensusSurvives(t *testing.T) {
	const (
		validatorCount    = 7
		macroEpochSize    = uint64(20)
		microEpochSize    = uint64(7)
		minValidatorCount = uint64(7)
		maxValidatorCount = uint64(7)
	)

	servers, keys, addrs, manager := setupStakingV2SlashingEmergencyCluster(
		t,
		validatorCount,
		macroEpochSize,
		microEpochSize,
		minValidatorCount,
		maxValidatorCount,
	)

	waitForNextEpochTransition(t, servers, macroEpochSize)
	require.Empty(t, framework.WaitForServersToSeal(servers, macroEpochSize*2+2))
	waitForAllServersSameHead(t, servers)

	initialSnapshot := mustGetLatestSnapshotValidators(t, servers[0])
	require.Len(t, initialSnapshot, validatorCount)
	for _, addr := range addrs {
		assert.Contains(t, initialSnapshot, addr)
	}
	assertEmergencyModeActive(t, servers[0], addrs[0], addrs, minValidatorCount, false)
	assertServerLogsDoNotContain(t, servers, criticalConsensusLogPatterns()...)

	// Validator A misses a full epoch in normal mode. With 6/7 online, IBFT can
	// still finalize the epoch and FinalizeEpoch must slash/deactivate A.
	validatorAIdx := 6
	validatorA := addrs[validatorAIdx]
	queryIdx := 0
	infoABefore, err := queryValidatorInfo(servers[queryIdx], addrs[queryIdx], validatorA)
	require.NoError(t, err)
	require.True(t, infoABefore.Exists)
	require.True(t, infoABefore.Active)
	stakeABefore := new(big.Int).Set(infoABefore.StakedAmount)

	firstBoundaryA, finalizeBoundaryA := stopValidatorForFullTargetEpoch(t, manager, servers, macroEpochSize, validatorAIdx)
	waitForServersToReachBlock(t, manager.ActiveServers(validatorAIdx), firstBoundaryA+1)
	waitForServersToReachBlock(t, manager.ActiveServers(validatorAIdx), finalizeBoundaryA+1)
	waitForAllServersSameHead(t, manager.ActiveServers(validatorAIdx))

	targetEpochA := epochFromFinalizedBoundary(finalizeBoundaryA, macroEpochSize)
	slashLogA, hasSlashLogA := findStakerSlashedLog(t, servers[queryIdx], targetEpochA, validatorA, validatorA)
	if !hasSlashLogA {
		slashLogA, hasSlashLogA = findStakerSlashedLogForValidator(t, servers[queryIdx], validatorA, validatorA)
	}
	require.True(t, hasSlashLogA, "validator A must emit StakerSlashed while emergency mode is inactive")
	require.NotNil(t, slashLogA)

	infoAAfterSlash := waitForValidatorInactive(t, servers[queryIdx], addrs[queryIdx], validatorA)
	require.Less(t, infoAAfterSlash.StakedAmount.Cmp(stakeABefore), 0, "validator A stake must be reduced by slashing")
	assert.Contains(t, mustGetSnapshotValidatorsAt(t, servers[queryIdx], firstBoundaryA-1), validatorA, "pre-finalized epoch snapshot keeps A")
	assertServerLogsDoNotContain(t, servers, criticalConsensusLogPatterns()...)

	assertEmergencyModeActive(t, servers[0], addrs[0], addrs, minValidatorCount, true)

	// Validator B misses one full epoch while emergency mode is active. The chain
	// keeps 5/7 validators online, which is enough for IBFT liveness, but B must
	// not be slashed while the emergency fallback is active.
	validatorBIdx := 5
	validatorB := addrs[validatorBIdx]
	infoBBefore, err := queryValidatorInfo(servers[queryIdx], addrs[queryIdx], validatorB)
	require.NoError(t, err)
	require.True(t, infoBBefore.Exists)
	require.True(t, infoBBefore.Active)
	stakeBBefore := new(big.Int).Set(infoBBefore.StakedAmount)

	firstBoundaryB, finalizeBoundaryB := stopValidatorForFullTargetEpoch(t, manager, manager.ActiveServers(validatorAIdx), macroEpochSize, validatorBIdx)
	onlineDuringEmergency := manager.ActiveServers(validatorAIdx, validatorBIdx)
	require.Len(t, onlineDuringEmergency, 5)
	waitForServersToReachBlock(t, onlineDuringEmergency, firstBoundaryB+1)
	waitForServersToReachBlock(t, onlineDuringEmergency, finalizeBoundaryB+1)
	waitForAllServersSameHead(t, onlineDuringEmergency)

	targetEpochB := epochFromFinalizedBoundary(finalizeBoundaryB, macroEpochSize)
	_, hasSlashLogB := findStakerSlashedLog(t, servers[queryIdx], targetEpochB, validatorB, validatorB)
	if !hasSlashLogB {
		_, hasSlashLogB = findStakerSlashedLogForValidator(t, servers[queryIdx], validatorB, validatorB)
	}
	require.False(t, hasSlashLogB, "validator B must not emit StakerSlashed while emergency mode is active")
	infoBAfterEmergencyEpoch, err := queryValidatorInfo(servers[queryIdx], addrs[queryIdx], validatorB)
	require.NoError(t, err)
	require.True(t, infoBAfterEmergencyEpoch.Exists)
	require.Equal(t, stakeBBefore.String(), infoBAfterEmergencyEpoch.StakedAmount.String(), "validator B stake must not be reduced while emergency mode is active")
	assertServerLogsDoNotContain(t, servers, criticalConsensusLogPatterns()...)

	// Recovery: bring both stopped validators back, repair A's below-threshold
	// self stake, reactivate any validators deactivated by 0% uptime, and wait for
	// the validator-set refresh to leave emergency mode.
	restartValidator(t, manager, validatorAIdx)
	restartValidator(t, manager, validatorBIdx)
	waitForAllServersSameHead(t, servers)

	topUpValidatorToThreshold(t, servers[0], addrs[0], validatorA, keys[validatorAIdx])
	require.NoError(t, sendSetActiveTx(servers[0], validatorA, keys[validatorAIdx], true))
	require.NoError(t, sendSetActiveTx(servers[0], validatorB, keys[validatorBIdx], true))
	assertEmergencyModeEventually(t, servers[0], addrs[0], addrs, minValidatorCount, false)

	waitForNextEpochTransition(t, servers, macroEpochSize)
	waitForServersToReachBlock(t, servers, finalizeBoundaryB+macroEpochSize+3)
	waitForAllServersSameHead(t, servers)

	assertEmergencyModeActive(t, servers[0], addrs[0], addrs, minValidatorCount, false)
	finalSnapshot := mustGetLatestSnapshotValidators(t, servers[0])
	require.Len(t, finalSnapshot, validatorCount)
	assert.Contains(t, finalSnapshot, validatorA)
	assert.Contains(t, finalSnapshot, validatorB)
	for _, addr := range addrs {
		assert.Contains(t, finalSnapshot, addr)
	}
	assertServerLogsDoNotContain(t, servers, criticalConsensusLogPatterns()...)
}

func findStakerSlashedLogForValidator(
	t *testing.T,
	srv *framework.TestServer,
	validator types.Address,
	staker types.Address,
) (*ethgo.Log, bool) {
	t.Helper()

	topic := stakerSlashedEventTopic()
	validatorTopic := abiHashAddress(validator)
	stakerTopic := abiHashAddress(staker)
	filter := &ethgo.LogFilter{
		Address: []ethgo.Address{ethgo.Address(pos.PosSysAddr)},
		Topics: [][]*ethgo.Hash{
			{&topic},
		},
	}
	filter.SetFromUint64(0)

	logs, err := srv.JSONRPC().Eth().GetLogs(filter)
	require.NoError(t, err)
	for _, log := range logs {
		if log.Address != ethgo.Address(pos.PosSysAddr) || len(log.Topics) != 4 {
			continue
		}
		if log.Topics[0] == topic && log.Topics[2] == validatorTopic && log.Topics[3] == stakerTopic {
			return log, true
		}
	}

	t.Logf("missing StakerSlashed log for validator=%s staker=%s scanned=%d", validator, staker, len(logs))

	return nil, false
}

func setupStakingV2SlashingEmergencyCluster(
	t *testing.T,
	numValidators int,
	macroEpochSize uint64,
	microEpochSize uint64,
	minValidators uint64,
	maxValidators uint64,
) ([]*framework.TestServer, []*ecdsa.PrivateKey, []types.Address, *framework.IBFTServersManager) {
	t.Helper()

	defaultBalance := framework.EthToWei(100_000_000)
	ibftManager := framework.NewIBFTServersManager(
		t,
		numValidators,
		IBFTDirPrefix,
		func(_ int, config *framework.TestServerConfig) {
			config.SetEpochSize(macroEpochSize)
			config.SetBlockTime(1)
			config.SetMicroEpochConfig(microEpochSize, 10_000, 9_000)
			config.PremineValidatorBalance(defaultBalance)
			config.SetIBFTPoS(true)
			config.SetMinValidatorCount(minValidators)
			config.SetMaxValidatorCount(maxValidators)
		},
	)
	t.Cleanup(func() { ibftManager.StopServers() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ibftManager.StartServers(ctx)

	servers := make([]*framework.TestServer, 0, numValidators)
	keys := make([]*ecdsa.PrivateKey, 0, numValidators)
	addrs := make([]types.Address, 0, numValidators)

	for i := 0; i < numValidators; i++ {
		srv := ibftManager.GetServer(i)
		key, err := srv.Config.PrivateKey()
		require.NoError(t, err)

		servers = append(servers, srv)
		keys = append(keys, key)
		addrs = append(addrs, crypto.PubKeyToAddress(&key.PublicKey))
	}

	threshold, err := queryValidatorThreshold(servers[0], addrs[0])
	require.NoError(t, err)
	require.Positive(t, threshold.Sign(), "validator threshold must be non-zero")
	stakeAmount := framework.EthToWei(2_000_000)
	require.GreaterOrEqual(t, stakeAmount.Cmp(threshold), 0, "test stake must satisfy validator threshold")
	for i := 0; i < numValidators; i++ {
		receipt, stakeErr := sendStakeTx(servers[i], addrs[i], keys[i], stakeAmount)
		require.NoError(t, stakeErr)
		require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)
	}
	require.Empty(t, framework.WaitForServersToSeal(servers, macroEpochSize+6))

	return servers, keys, addrs, ibftManager
}

func stopValidatorForFullTargetEpoch(
	t *testing.T,
	manager *framework.IBFTServersManager,
	sealingServers []*framework.TestServer,
	epochSize uint64,
	validatorIdx int,
) (uint64, uint64) {
	t.Helper()

	heightBefore, err := sealingServers[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	firstBoundary := ((heightBefore / epochSize) + 1) * epochSize
	finalizeBoundary := firstBoundary + epochSize
	if firstBoundary > 1 {
		require.Empty(t, framework.WaitForServersToSeal(sealingServers, firstBoundary-1))
	}
	manager.StopServer(validatorIdx)

	return firstBoundary, finalizeBoundary
}

func waitForServersToReachBlock(t *testing.T, servers []*framework.TestServer, target uint64) {
	t.Helper()

	require.Eventually(t, func() bool {
		for _, srv := range servers {
			height, err := srv.JSONRPC().Eth().BlockNumber()
			if err != nil || height < target {
				return false
			}
		}

		return true
	}, 5*time.Minute, time.Second, "servers did not reach block %d", target)
}

func waitForValidatorInactive(t *testing.T, srv *framework.TestServer, from, validator types.Address) *stakingValidatorInfo {
	t.Helper()

	var info *stakingValidatorInfo
	require.Eventually(t, func() bool {
		var err error
		info, err = queryValidatorInfo(srv, from, validator)
		return err == nil && info.Exists && !info.Active
	}, 2*time.Minute, time.Second)

	return info
}

func queryEmergencyModeActive(t *testing.T, srv *framework.TestServer, from types.Address, validators []types.Address, minValidators uint64) bool {
	t.Helper()

	eligible := uint64(0)
	for _, validator := range validators {
		info, err := queryValidatorInfo(srv, from, validator)
		require.NoError(t, err)
		if !info.Exists || !info.Active {
			continue
		}

		thresholdMet, err := queryIsValidatorThresholdMet(srv, from, validator)
		require.NoError(t, err)
		if thresholdMet {
			eligible++
		}
	}

	return eligible < minValidators
}

func assertEmergencyModeActive(t *testing.T, srv *framework.TestServer, from types.Address, validators []types.Address, minValidators uint64, expected bool) {
	t.Helper()

	actual := queryEmergencyModeActive(t, srv, from, validators, minValidators)
	require.Equal(t, expected, actual, "derived emergency mode state mismatch")
}

func assertEmergencyModeEventually(t *testing.T, srv *framework.TestServer, from types.Address, validators []types.Address, minValidators uint64, expected bool) {
	t.Helper()

	require.Eventually(t, func() bool {
		return queryEmergencyModeActive(t, srv, from, validators, minValidators) == expected
	}, 2*time.Minute, time.Second)
}

func topUpValidatorToThreshold(t *testing.T, srv *framework.TestServer, from, validator types.Address, key *ecdsa.PrivateKey) {
	t.Helper()

	threshold, err := queryValidatorThreshold(srv, from)
	require.NoError(t, err)
	info, err := queryValidatorInfo(srv, from, validator)
	require.NoError(t, err)
	require.True(t, info.Exists)
	if info.StakedAmount.Cmp(threshold) >= 0 {
		return
	}

	topUp := new(big.Int).Sub(threshold, info.StakedAmount)
	receipt, err := sendStakeTx(srv, validator, key, topUp)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)
}

func restartValidator(t *testing.T, manager *framework.IBFTServersManager, validatorIdx int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, manager.GetServer(validatorIdx).Start(ctx))
	require.NoError(t, manager.GetServer(validatorIdx).WaitForReady(ctx))
}
