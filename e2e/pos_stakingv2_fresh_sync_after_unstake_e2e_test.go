package e2e

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/xgr-network/xgr-node/consensus/ibft/fork"
	"github.com/xgr-network/xgr-node/e2e/framework"
	"github.com/xgr-network/xgr-node/types"
)

func TestPoS_StakingV2_FreshNodeSyncAfterDelayedPoAToPoSAndValidatorFullUnstake(t *testing.T) {
	const (
		validatorCount    = 5
		epochSize         = uint64(20)
		microEpochSize    = uint64(5)
		poSDeployment     = uint64(40)
		poSActivation     = uint64(60)
		premineEth        = 100_000_000
		stakeEth          = 2_000_000
		removedValidator  = 4
		minValidatorCount = uint64(1)
		maxValidatorCount = uint64(validatorCount)
	)

	servers, keys, addrs, manager := setupDelayedPoAToPoSStakingV2Cluster(
		t,
		validatorCount,
		epochSize,
		microEpochSize,
		poSDeployment,
		poSActivation,
		premineEth,
		stakeEth,
		minValidatorCount,
		maxValidatorCount,
	)

	require.Greater(t, poSActivation, uint64(1), "PoA -> PoS activation must be delayed")
	require.Empty(t, framework.WaitForServersToSeal(servers, poSActivation+3), "cluster must continue sealing after delayed PoS activation")
	waitForAllServersSameHead(t, servers)

	validatorSet, err := framework.GetValidatorSet(addrs[0], servers[0].JSONRPC())
	require.NoError(t, err)
	require.Len(t, validatorSet, validatorCount)
	assertServerLogsDoNotContain(t, servers, criticalConsensusLogPatterns()...)

	// Run for at least one full macro epoch after the delayed PoS activation before
	// exercising validator lifecycle changes.
	require.Empty(t, framework.WaitForServersToSeal(servers, poSActivation+epochSize+2))

	removed := addrs[removedValidator]
	removedKey := keys[removedValidator]
	waitUntilEarlyOrMidEpoch(t, servers[0], epochSize)

	receipt, err := sendSetActiveReceiptTx(servers[removedValidator], removed, removedKey, false)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	stakerInfo, err := queryStakerInfo(servers[0], addrs[0], removed)
	require.NoError(t, err)
	require.True(t, stakerInfo.Exists)
	require.False(t, stakerInfo.Active)
	require.True(t, stakerInfo.DeactivatedAtBlock.Sign() > 0)

	receipt, err = framework.UnstakeAmount(removed, removedKey, servers[removedValidator])
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status, "same-epoch validator full unstake must fail")

	unstakeReadyHeight := ((stakerInfo.DeactivatedAtBlock.Uint64() / epochSize) + 1) * epochSize
	require.Empty(t, framework.WaitForServersToSeal(servers, unstakeReadyHeight))

	receipt, err = framework.UnstakeAmount(removed, removedKey, servers[removedValidator])
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status, "validator full unstake must succeed at E+1")

	stakerInfo, err = queryStakerInfo(servers[0], addrs[0], removed)
	require.NoError(t, err)
	require.False(t, stakerInfo.Exists)

	validatorInfo, err := queryValidatorInfo(servers[0], addrs[0], removed)
	require.NoError(t, err)
	require.False(t, validatorInfo.Exists)

	waitForValidatorSetSizeAndAbsence(t, servers[0], addrs[0], removed, validatorCount-1)

	heightAfterUnstake, err := servers[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	require.Empty(t, framework.WaitForServersToSeal(servers, heightAfterUnstake+(2*epochSize)+2), "remaining validators must keep sealing after removal")
	waitForAllServersSameHead(t, servers)
	assertServerLogsDoNotContain(t, servers, criticalConsensusLogPatterns()...)

	fresh := startFreshNonValidatorSyncNode(t, manager, "fresh-sync-after-unstake")
	clusterHead := waitForServerToCatchUpToHead(t, fresh, servers[0])

	freshHead, err := fresh.JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	require.GreaterOrEqual(t, freshHead, clusterHead, "fresh sync node must catch up to at least the selected cluster head")
	assertSameBlockHash(t, fresh, servers[0], clusterHead)

	freshValidatorSet, err := framework.GetValidatorSet(addrs[0], fresh.JSONRPC())
	require.NoError(t, err)
	require.Len(t, freshValidatorSet, validatorCount-1)
	require.NotContains(t, freshValidatorSet, removed)

	allServers := append(append([]*framework.TestServer{}, servers...), fresh)
	assertSameBlockHash(t, fresh, servers[0], clusterHead)
	assertServerLogsDoNotContain(t, allServers, criticalConsensusLogPatterns()...)

	finalValidatorSet, err := framework.GetValidatorSet(addrs[0], servers[0].JSONRPC())
	require.NoError(t, err)
	require.Len(t, finalValidatorSet, validatorCount-1)
	require.NotContains(t, finalValidatorSet, removed)
}

func setupDelayedPoAToPoSStakingV2Cluster(
	t *testing.T,
	validatorCount int,
	epochSize uint64,
	microEpochSize uint64,
	poSDeployment uint64,
	poSActivation uint64,
	premineEth int64,
	stakeEth int64,
	minValidators uint64,
	maxValidators uint64,
) ([]*framework.TestServer, []*ecdsa.PrivateKey, []types.Address, *framework.IBFTServersManager) {
	t.Helper()

	manager := framework.NewIBFTServersManager(
		t,
		validatorCount,
		"delayed-poa-pos-fresh-sync-",
		func(_ int, cfg *framework.TestServerConfig) {
			cfg.SetBlockTime(1)
			cfg.SetIBFTBaseTimeout(2)
			cfg.SetMicroEpochConfig(microEpochSize, epochSize/microEpochSize, 10_000, 9_000)
			cfg.PremineValidatorBalance(framework.EthToWei(premineEth))
		},
	)
	t.Cleanup(func() { manager.StopServers() })

	require.NoError(t, switchIBFTToDelayedPoS(t, manager.GetServer(0), poSActivation, poSDeployment, minValidators, maxValidators))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	manager.StartServers(ctx)

	servers := manager.ActiveServers()
	addrs, keys := collectValidatorCredentials(t, manager, validatorCount)

	require.Empty(t, framework.WaitForServersToSeal(servers, poSDeployment+1), "staking contract must be deployed before validators stake")
	for i := 0; i < validatorCount; i++ {
		require.NoError(t, framework.StakeAmount(addrs[i], keys[i], framework.EthToWei(stakeEth), manager.GetServer(0)))
	}

	require.Empty(t, framework.WaitForServersToSeal(servers, poSActivation-1), "cluster must seal PoA blocks before delayed PoS activation")
	require.Empty(t, framework.WaitForServersToSeal(servers, poSActivation), "cluster must seal the delayed PoS activation block")

	return servers, keys, addrs, manager
}

func switchIBFTToDelayedPoS(t *testing.T, srv *framework.TestServer, from, deployment, minValidators, maxValidators uint64) error {
	t.Helper()

	return srv.SwitchIBFTTypeWithPoSConfig(
		fork.PoS,
		from,
		nil,
		&deployment,
		&minValidators,
		&maxValidators,
	)
}

func startFreshNonValidatorSyncNode(t *testing.T, manager *framework.IBFTServersManager, name string) *framework.TestServer {
	t.Helper()

	seed := manager.GetServer(0)
	freshDir := fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
	fresh := framework.NewTestServer(t, seed.Config.RootDir, func(cfg *framework.TestServerConfig) {
		cfg.SetConsensus(framework.ConsensusIBFT)
		cfg.SetIBFTDirPrefix("fresh-non-validator-")
		cfg.SetIBFTDir(freshDir)
		cfg.SetBootnodes(seed.Config.Bootnodes)
		cfg.SetLogsDir(seed.Config.LogsDir)
		cfg.SetSaveLogs(true)
		cfg.SetName("node-fresh-sync")
		cfg.SetBlockTime(1)
		cfg.SetIBFTBaseTimeout(2)
	})
	t.Cleanup(func() { fresh.Stop() })

	_, err := fresh.SecretsInit()
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, fresh.Start(ctx))

	return fresh
}

func waitForServerToCatchUpToHead(t *testing.T, srv, headSource *framework.TestServer) uint64 {
	t.Helper()

	targetHeight, err := headSource.JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	targetBlock, err := headSource.JSONRPC().Eth().GetBlockByNumber(ethgo.BlockNumber(targetHeight), false)
	require.NoError(t, err)
	require.NotNil(t, targetBlock)

	require.Eventually(t, func() bool {
		height, hErr := srv.JSONRPC().Eth().BlockNumber()
		if hErr != nil || height < targetHeight {
			return false
		}

		block, bErr := srv.JSONRPC().Eth().GetBlockByNumber(ethgo.BlockNumber(targetHeight), false)
		return bErr == nil && block != nil && block.Hash == targetBlock.Hash
	}, 10*time.Minute, 2*time.Second, "fresh node must sync from genesis to cluster height %d", targetHeight)

	return targetHeight
}

func waitUntilEarlyOrMidEpoch(t *testing.T, srv *framework.TestServer, epochSize uint64) {
	t.Helper()

	require.Eventually(t, func() bool {
		height, err := srv.JSONRPC().Eth().BlockNumber()
		if err != nil {
			return false
		}
		offset := height % epochSize
		if offset >= 2 && offset <= epochSize/2 {
			return true
		}

		return len(framework.WaitForServersToSeal([]*framework.TestServer{srv}, height+1)) == 0
	}, 2*time.Minute, time.Second)
}

func waitForValidatorSetSizeAndAbsence(t *testing.T, srv *framework.TestServer, from, removed types.Address, expectedSize int) {
	t.Helper()

	require.Eventually(t, func() bool {
		validatorSet, err := framework.GetValidatorSet(from, srv.JSONRPC())
		return err == nil && len(validatorSet) == expectedSize && !containsAddress(validatorSet, removed)
	}, 2*time.Minute, time.Second)
}

func assertSameBlockHash(t *testing.T, a, b *framework.TestServer, height uint64) {
	t.Helper()

	blockA, err := a.JSONRPC().Eth().GetBlockByNumber(ethgo.BlockNumber(height), false)
	require.NoError(t, err)
	require.NotNil(t, blockA)
	blockB, err := b.JSONRPC().Eth().GetBlockByNumber(ethgo.BlockNumber(height), false)
	require.NoError(t, err)
	require.NotNil(t, blockB)
	require.Equal(t, blockB.Hash, blockA.Hash, "servers must agree on block hash at height %d", height)
}
