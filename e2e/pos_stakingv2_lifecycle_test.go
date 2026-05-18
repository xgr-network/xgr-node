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
	"github.com/umbracle/ethgo"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/e2e/framework"
	"github.com/xgr-network/xgr-node/types"
)

func setupStakingV2Cluster(t *testing.T, numValidators int, epochSize, minValidators, maxValidators uint64) ([]*framework.TestServer, []*ecdsa.PrivateKey, []types.Address, *framework.IBFTServersManager) {
	t.Helper()

	defaultBalance := framework.EthToWei(100_000_000)
	stakeAmount := framework.EthToWei(2_000_000)

	ibftManager := framework.NewIBFTServersManager(
		t,
		numValidators,
		IBFTDirPrefix,
		func(_ int, config *framework.TestServerConfig) {
			config.SetEpochSize(epochSize)
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

	for i := 0; i < numValidators; i++ {
		require.NoError(t, framework.StakeAmount(addrs[i], keys[i], stakeAmount, servers[i]))
	}
	require.Empty(t, framework.WaitForServersToSeal(servers, epochSize+6))

	return servers, keys, addrs, ibftManager
}

func setupStakingV2ClusterWithStandbyNode(t *testing.T, numNodes, numGenesisValidators int, epochSize, minValidators, maxValidators uint64) ([]*framework.TestServer, []*ecdsa.PrivateKey, []types.Address, *framework.IBFTServersManager) {
	t.Helper()

	defaultBalance := framework.EthToWei(100_000_000)
	stakeAmount := framework.EthToWei(2_000_000)

	ibftManager := framework.NewIBFTServersManagerWithGenesisValidators(
		t,
		numNodes,
		numGenesisValidators,
		IBFTDirPrefix,
		func(index int, config *framework.TestServerConfig) {
			if index >= numGenesisValidators {
				config.SetIBFTDirPrefix("polygon-edge-non-validator-")
				config.SetIBFTDir("polygon-edge-non-validator-" + fmt.Sprint(index))
			}
			config.SetEpochSize(epochSize)
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

	servers := make([]*framework.TestServer, 0, numNodes)
	keys := make([]*ecdsa.PrivateKey, 0, numNodes)
	addrs := make([]types.Address, 0, numNodes)

	for i := 0; i < numNodes; i++ {
		srv := ibftManager.GetServer(i)
		key, err := srv.Config.PrivateKey()
		require.NoError(t, err)

		servers = append(servers, srv)
		keys = append(keys, key)
		addrs = append(addrs, crypto.PubKeyToAddress(&key.PublicKey))
	}

	for i := 0; i < numGenesisValidators; i++ {
		require.NoError(t, framework.StakeAmount(addrs[i], keys[i], stakeAmount, servers[i]))
	}
	require.Empty(t, framework.WaitForServersToSeal(servers, epochSize+6))

	return servers, keys, addrs, ibftManager
}

func setupStakingV2ClusterNoAutoStake(t *testing.T, numValidators int, epochSize, minValidators, maxValidators uint64) ([]*framework.TestServer, []*ecdsa.PrivateKey, []types.Address, *framework.IBFTServersManager) {
	t.Helper()

	defaultBalance := framework.EthToWei(100_000_000)

	ibftManager := framework.NewIBFTServersManager(
		t,
		numValidators,
		IBFTDirPrefix,
		func(_ int, config *framework.TestServerConfig) {
			config.SetEpochSize(epochSize)
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

	require.Empty(t, framework.WaitForServersToSeal(servers, epochSize+3))

	return servers, keys, addrs, ibftManager
}

func fundAccount(t *testing.T, srv *framework.TestServer, from types.Address, key *ecdsa.PrivateKey, to types.Address, amount *big.Int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), framework.DefaultTimeout)
	defer cancel()

	receipt, err := srv.SendRawTx(ctx, &framework.PreparedTransaction{
		From:     from,
		To:       &to,
		GasPrice: framework.TestGasPrice(),
		Gas:      framework.DefaultGasLimit,
		Value:    amount,
	}, key)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)
}

func ensureStakeFunding(
	t *testing.T,
	payerSrv *framework.TestServer,
	payerAddr types.Address,
	payerKey *ecdsa.PrivateKey,
	stakerAddr types.Address,
	required *big.Int,
) {
	t.Helper()

	balance := framework.GetAccountBalance(t, stakerAddr, payerSrv.JSONRPC())
	if balance.Cmp(required) >= 0 {
		return
	}

	topUp := new(big.Int).Sub(required, balance)
	topUp.Add(topUp, framework.EthToWei(1)) // extra headroom for pending tx / gas
	fundAccount(t, payerSrv, payerAddr, payerKey, stakerAddr, topUp)
}

func TestPoS_StakingV2_StakeBelowMinDoesNotJoinSet(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, 5, 3, 5)

	minStake, err := queryMinStake(servers[0], addrs[0])
	require.NoError(t, err)

	candidateKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	candidateAddr := crypto.PubKeyToAddress(&candidateKey.PublicKey)

	fundAccount(t, servers[0], addrs[0], keys[0], candidateAddr, new(big.Int).Mul(minStake, big.NewInt(2)))
	require.NoError(t, framework.StakeAmount(candidateAddr, candidateKey, new(big.Int).Sub(minStake, big.NewInt(1)), servers[0]))
	require.Empty(t, framework.WaitForServersToSeal(servers, 18))

	snap := mustGetLatestSnapshotValidators(t, servers[0])
	assert.NotContains(t, snap, candidateAddr)
}

func TestPoS_StakingV2_StakeAtMinJoinsSet(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, 5, 3, 5)

	minStake, err := queryMinStake(servers[0], addrs[0])
	require.NoError(t, err)

	candidateKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	candidateAddr := crypto.PubKeyToAddress(&candidateKey.PublicKey)

	fundAccount(t, servers[0], addrs[0], keys[0], candidateAddr, new(big.Int).Mul(minStake, big.NewInt(3)))
	require.NoError(t, framework.StakeAmount(candidateAddr, candidateKey, minStake, servers[0]))
	require.Empty(t, framework.WaitForServersToSeal(servers, 22))

	listed := mustGetStakingValidatorList(t, servers[0], addrs[0])
	assert.Contains(t, listed, candidateAddr, "candidate with exactly min self stake should join validators()")

	snap := mustGetLatestSnapshotValidators(t, servers[0])
	assert.NotContains(t, snap, candidateAddr, "snapshot Tier 1 still requires validator threshold support")
}

func TestPoS_StakingV2_BelowMinStakeExcludedWhenTier1StillSatisfiesMinValidators(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2ClusterNoAutoStake(t, 4, 6, 3, 5)

	threshold, err := queryValidatorThreshold(servers[0], addrs[0])
	require.NoError(t, err)
	require.NoError(t, framework.StakeAmount(addrs[0], keys[0], threshold, servers[0]))
	require.NoError(t, framework.StakeAmount(addrs[1], keys[1], threshold, servers[1]))
	require.NoError(t, framework.StakeAmount(addrs[2], keys[2], threshold, servers[2]))
	waitForNextEpochTransition(t, servers, 6)

	listed := mustGetStakingValidatorList(t, servers[0], addrs[0])
	require.Contains(t, listed, addrs[3], "target validator must be in validators() universe")
	countTier1, countTier2, countTier3 := countSelectionTiersFromValidatorList(t, servers[0], addrs[0], threshold)
	require.GreaterOrEqual(t, countTier1, uint64(3), "Tier 1 should remain active because strict-eligible count meets minValidators")
	assert.GreaterOrEqual(t, countTier2, countTier1)
	assert.GreaterOrEqual(t, countTier3, countTier2)

	snap := mustGetLatestSnapshotValidators(t, servers[0])
	assert.NotContains(t, snap, addrs[3], "below-min listed validator must be excluded while Tier 1 is selected")
}

func TestPoS_StakingV2_BelowMinStakeRetainedViaTier2WhenTier1FallsBelowMinValidators(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2ClusterNoAutoStake(t, 4, 6, 3, 5)

	minStake, err := queryMinStake(servers[0], addrs[0])
	require.NoError(t, err)
	require.NoError(t, framework.StakeAmount(addrs[0], keys[0], minStake, servers[0]))
	require.NoError(t, framework.StakeAmount(addrs[1], keys[1], minStake, servers[1]))
	waitForNextEpochTransition(t, servers, 6)

	listed := mustGetStakingValidatorList(t, servers[0], addrs[0])
	require.Contains(t, listed, addrs[3], "target validator must be in validators() universe")
	countTier1, countTier2, _ := countSelectionTiersFromValidatorList(t, servers[0], addrs[0], minStake)
	require.Less(t, countTier1, uint64(3), "Tier 1 should be insufficient with only two strict validators")
	require.GreaterOrEqual(t, countTier2, uint64(3), "Tier 2 should satisfy minValidators via active fallback")

	snap := mustGetLatestSnapshotValidators(t, servers[0])
	assert.Contains(t, snap, addrs[3], "listed below-min validator should be retained because Tier 2 is selected")
}

func TestPoS_StakingV2_InactiveButStakedRetainedViaTier3WhenTier2FallsBelowMinValidators(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2ClusterNoAutoStake(t, 4, 6, 3, 5)

	minStake, err := queryMinStake(servers[0], addrs[0])
	require.NoError(t, err)
	require.NoError(t, framework.StakeAmount(addrs[0], keys[0], minStake, servers[0]))
	require.NoError(t, framework.StakeAmount(addrs[1], keys[1], minStake, servers[1]))
	require.NoError(t, framework.StakeAmount(addrs[2], keys[2], minStake, servers[2]))
	waitForNextEpochTransition(t, servers, 6)

	require.NoError(t, sendSetActiveTx(servers[1], addrs[1], keys[1], false))
	require.NoError(t, sendSetActiveTx(servers[2], addrs[2], keys[2], false))
	waitForNextEpochTransition(t, servers, 6)

	listed := mustGetStakingValidatorList(t, servers[0], addrs[0])
	require.Contains(t, listed, addrs[2], "target validator must be in validators() universe")
	countTier1, countTier2, countTier3 := countSelectionTiersFromValidatorList(t, servers[0], addrs[0], minStake)
	require.Less(t, countTier1, uint64(3), "Tier 1 should be insufficient with inactive validators")
	require.Less(t, countTier2, uint64(3), "Tier 2 should be insufficient when active validator count falls below minValidators")
	require.GreaterOrEqual(t, countTier3, uint64(3), "Tier 3 should satisfy minValidators with stake-only fallback")

	snap := mustGetLatestSnapshotValidators(t, servers[0])
	assert.Contains(t, snap, addrs[2], "inactive listed validator with stake>0 should be retained when Tier 3 is selected")
}

func TestPoS_StakingV2_Tier1ToTier2Transition(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2ClusterNoAutoStake(t, 4, 6, 3, 5)
	threshold, err := queryValidatorThreshold(servers[0], addrs[0])
	require.NoError(t, err)

	require.NoError(t, framework.StakeAmount(addrs[0], keys[0], threshold, servers[0]))
	require.NoError(t, framework.StakeAmount(addrs[1], keys[1], threshold, servers[1]))
	require.NoError(t, framework.StakeAmount(addrs[2], keys[2], threshold, servers[2]))
	waitForNextEpochTransition(t, servers, 6)
	snapTier1 := mustGetLatestSnapshotValidators(t, servers[0])
	assert.NotContains(t, snapTier1, addrs[3], "below-min listed validator should be excluded while Tier 1 remains active")

	require.NoError(t, sendSetActiveTx(servers[2], addrs[2], keys[2], false))
	waitForNextEpochTransition(t, servers, 6)

	countTier1, countTier2, _ := countSelectionTiersFromValidatorList(t, servers[0], addrs[0], threshold)
	require.Less(t, countTier1, uint64(3))
	require.GreaterOrEqual(t, countTier2, uint64(3))
	snapTier2 := mustGetLatestSnapshotValidators(t, servers[0])
	assert.Contains(t, snapTier2, addrs[3], "below-min listed validator should appear once selection transitions from Tier 1 to Tier 2")
}

func TestPoS_StakingV2_Tier2ToTier1Recovery(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2ClusterNoAutoStake(t, 4, 6, 3, 5)
	threshold, err := queryValidatorThreshold(servers[0], addrs[0])
	require.NoError(t, err)

	require.NoError(t, framework.StakeAmount(addrs[0], keys[0], threshold, servers[0]))
	require.NoError(t, framework.StakeAmount(addrs[1], keys[1], threshold, servers[1]))
	require.NoError(t, framework.StakeAmount(addrs[2], keys[2], threshold, servers[2]))
	waitForNextEpochTransition(t, servers, 6)

	require.NoError(t, sendSetActiveTx(servers[2], addrs[2], keys[2], false))
	waitForNextEpochTransition(t, servers, 6)
	require.Contains(t, mustGetLatestSnapshotValidators(t, servers[0]), addrs[3], "below-min listed validator should be included while Tier 2 is active")

	require.NoError(t, sendSetActiveTx(servers[2], addrs[2], keys[2], true))
	waitForNextEpochTransition(t, servers, 6)
	countTier1, _, _ := countSelectionTiersFromValidatorList(t, servers[0], addrs[0], threshold)
	require.GreaterOrEqual(t, countTier1, uint64(3))
	snap := mustGetLatestSnapshotValidators(t, servers[0])
	assert.NotContains(t, snap, addrs[3], "below-min listed validator should drop once Tier 1 recovers")
}

func TestPoS_StakingV2_Tier2ToTier3Transition(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2ClusterNoAutoStake(t, 4, 6, 3, 5)
	minStake, err := queryMinStake(servers[0], addrs[0])
	require.NoError(t, err)

	require.NoError(t, framework.StakeAmount(addrs[0], keys[0], minStake, servers[0]))
	require.NoError(t, framework.StakeAmount(addrs[1], keys[1], minStake, servers[1]))
	require.NoError(t, framework.StakeAmount(addrs[2], keys[2], minStake, servers[2]))
	waitForNextEpochTransition(t, servers, 6)

	require.NoError(t, sendSetActiveTx(servers[2], addrs[2], keys[2], false))
	waitForNextEpochTransition(t, servers, 6)
	countTier1Before, countTier2Before, _ := countSelectionTiersFromValidatorList(t, servers[0], addrs[0], minStake)
	require.Less(t, countTier1Before, uint64(3))
	require.GreaterOrEqual(t, countTier2Before, uint64(3), "first phase should remain in Tier 2")

	require.NoError(t, sendSetActiveTx(servers[1], addrs[1], keys[1], false))
	waitForNextEpochTransition(t, servers, 6)

	countTier1, countTier2, countTier3 := countSelectionTiersFromValidatorList(t, servers[0], addrs[0], minStake)
	require.Less(t, countTier1, uint64(3))
	require.Less(t, countTier2, uint64(3))
	require.GreaterOrEqual(t, countTier3, uint64(3))
	snap := mustGetLatestSnapshotValidators(t, servers[0])
	assert.Contains(t, snap, addrs[2], "inactive staked validator should be present once selection transitions to Tier 3")
}

func TestPoS_StakingV2_Tier3ToTier1Recovery(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2ClusterNoAutoStake(t, 4, 6, 3, 5)
	minStake, err := queryMinStake(servers[0], addrs[0])
	require.NoError(t, err)

	require.NoError(t, framework.StakeAmount(addrs[0], keys[0], minStake, servers[0]))
	require.NoError(t, framework.StakeAmount(addrs[1], keys[1], minStake, servers[1]))
	require.NoError(t, framework.StakeAmount(addrs[2], keys[2], minStake, servers[2]))
	require.NoError(t, framework.StakeAmount(addrs[3], keys[3], minStake, servers[3]))
	waitForNextEpochTransition(t, servers, 6)

	require.NoError(t, sendSetActiveTx(servers[3], addrs[3], keys[3], false))
	require.NoError(t, sendSetActiveTx(servers[2], addrs[2], keys[2], false))
	require.NoError(t, sendSetActiveTx(servers[1], addrs[1], keys[1], false))
	waitForNextEpochTransition(t, servers, 6)
	require.Contains(t, mustGetLatestSnapshotValidators(t, servers[0]), addrs[3], "validator should be included while Tier 3 is active")

	require.NoError(t, sendSetActiveTx(servers[3], addrs[3], keys[3], true))
	require.NoError(t, sendSetActiveTx(servers[2], addrs[2], keys[2], true))
	require.NoError(t, sendSetActiveTx(servers[1], addrs[1], keys[1], true))
	waitForNextEpochTransition(t, servers, 6)

	countTier1, _, _ := countSelectionTiersFromValidatorList(t, servers[0], addrs[0], minStake)
	require.GreaterOrEqual(t, countTier1, uint64(3))
	snap := mustGetLatestSnapshotValidators(t, servers[0])
	assert.Contains(t, snap, addrs[3], "validator should remain selected after Tier 1 recovery because it is strict-eligible again")
}

func TestPoS_StakingV2_DeactivateKeepsMembershipUntilEpochBoundary(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, 6, 3, 4)

	require.NoError(t, sendSetActiveTx(servers[3], addrs[3], keys[3], false))

	ctx, cancel := context.WithTimeout(context.Background(), framework.DefaultTimeout)
	defer cancel()
	heightNow, err := servers[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	nextBoundary := ((heightNow / 6) + 1) * 6

	require.Empty(t, framework.WaitForServersToSeal(servers, nextBoundary-1))
	snapBeforeBoundary, err := getSnapshotValidatorsAt(ctx, servers[0], nextBoundary-1)
	require.NoError(t, err)
	assert.Contains(t, snapBeforeBoundary, addrs[3])

	require.Empty(t, framework.WaitForServersToSeal(servers, nextBoundary+3))
	snapAfterBoundary := mustGetLatestSnapshotValidators(t, servers[0])
	assert.NotContains(t, snapAfterBoundary, addrs[3])
}

func TestPoS_StakingV2_RejoinAfterDeactivate(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, 6, 3, 4)

	require.NoError(t, sendSetActiveTx(servers[3], addrs[3], keys[3], false))
	require.Empty(t, framework.WaitForServersToSeal(servers, 20))
	snapAfterDeactivate := mustGetLatestSnapshotValidators(t, servers[0])
	assert.NotContains(t, snapAfterDeactivate, addrs[3])

	require.NoError(t, sendSetActiveTx(servers[3], addrs[3], keys[3], true))
	require.Empty(t, framework.WaitForServersToSeal(servers, 30))
	snapAfterRejoin := mustGetLatestSnapshotValidators(t, servers[0])
	assert.Contains(t, snapAfterRejoin, addrs[3])
}

func TestPoS_StakingV2_MaxValidatorsCutsLowestTier1Validator(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2ClusterWithStandbyNode(t, 5, 4, 5, 3, 4)

	minStake, err := queryMinStake(servers[0], addrs[0])
	require.NoError(t, err)
	threshold, err := queryValidatorThreshold(servers[0], addrs[0])
	require.NoError(t, err)
	incumbentDInfo, err := queryValidatorInfo(servers[0], addrs[0], addrs[3])
	require.NoError(t, err)

	// Enforce deterministic strict-tier ranking:
	// Incumbent A > Incumbent B > Incumbent C > Candidate > Incumbent D.
	require.NoError(t, framework.StakeAmount(addrs[0], keys[0], framework.EthToWei(40), servers[0]))
	require.NoError(t, framework.StakeAmount(addrs[1], keys[1], framework.EthToWei(30), servers[1]))
	require.NoError(t, framework.StakeAmount(addrs[2], keys[2], framework.EthToWei(20), servers[2]))

	candidateAddr := addrs[4]
	candidateKey := keys[4]

	candidateStake := new(big.Int).Add(incumbentDInfo.StakedAmount, framework.EthToWei(10))
	require.GreaterOrEqual(t, candidateStake.Cmp(minStake), 0, "candidate must satisfy min self stake")
	require.Greater(t, candidateStake.Cmp(threshold), 0, "candidate stake must strictly exceed threshold")
	gasPrice := framework.TestGasPriceUint64()
	gasReserve := new(big.Int).Mul(
		new(big.Int).SetUint64(framework.DefaultGasLimit),
		new(big.Int).SetUint64(gasPrice),
	)
	gasReserve.Mul(gasReserve, big.NewInt(5))
	requiredCandidate := new(big.Int).Add(candidateStake, gasReserve)
	ensureStakeFunding(t, servers[0], addrs[0], keys[0], candidateAddr, requiredCandidate)
	heightAfterFunding, err := servers[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	require.Empty(t, framework.WaitForServersToSeal(servers, heightAfterFunding))
	candidateBalance, err := servers[0].JSONRPC().Eth().GetBalance(ethgo.Address(candidateAddr), ethgo.Latest)
	require.NoError(t, err)
	minimumCandidateBalance := new(big.Int).Add(candidateStake, new(big.Int).Div(new(big.Int).Set(gasReserve), big.NewInt(2)))
	require.GreaterOrEqual(t, candidateBalance.Cmp(minimumCandidateBalance), 0, "candidate balance must cover candidate stake plus gas reserve")
	t.Logf("staking maxValidators candidate: candidateStake=%s candidateBalance=%s gasReserve=%s gasPrice=%d defaultGasLimit=%d", candidateStake.String(), candidateBalance.String(), gasReserve.String(), gasPrice, framework.DefaultGasLimit)
	require.NoError(t, framework.StakeAmount(candidateAddr, candidateKey, candidateStake, servers[4]))

	candidateInfo, err := queryValidatorInfo(servers[0], addrs[0], candidateAddr)
	require.NoError(t, err)
	assert.True(t, candidateInfo.Active, "candidate must be active")
	assert.GreaterOrEqual(t, candidateInfo.StakedAmount.Cmp(minStake), 0, "candidate must satisfy min self stake")

	metThreshold, err := queryIsValidatorThresholdMet(servers[0], addrs[0], candidateAddr)
	require.NoError(t, err)
	assert.True(t, metThreshold, "candidate must satisfy threshold")

	joinEffectiveAt, err := queryJoinEffectiveAtBlock(servers[0], addrs[0], candidateAddr)
	require.NoError(t, err)
	heightNow, err := servers[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)

	snapshotBlock := joinEffectiveAt.Uint64() + 1
	if heightNow < snapshotBlock {
		require.Empty(t, framework.WaitForServersToSeal(servers, snapshotBlock))
		heightNow, err = servers[0].JSONRPC().Eth().BlockNumber()
		require.NoError(t, err)
	}
	assert.GreaterOrEqual(t, new(big.Int).SetUint64(heightNow).Cmp(joinEffectiveAt), 0, "candidate join must be effective")

	ctx, cancel := context.WithTimeout(context.Background(), framework.DefaultTimeout)
	defer cancel()
	snap, err := getSnapshotValidatorsAt(ctx, servers[0], snapshotBlock)
	require.NoError(t, err)
	require.Len(t, snap, 4, "maxValidators cap must be applied")
	assert.Contains(t, snap, candidateAddr, "candidate should be included in capped top-4 snapshot")
	assert.NotContains(t, snap, addrs[3], "lowest incumbent must be displaced")
	assert.Contains(t, snap, addrs[0], "incumbent A should remain in snapshot")
	assert.Contains(t, snap, addrs[1], "incumbent B should remain in snapshot")
	assert.Contains(t, snap, addrs[2], "incumbent C should remain in snapshot")
}

func waitUntilEpochOffsetAtMost(t *testing.T, srv *framework.TestServer, epochSize uint64, maxOffset uint64) {
	t.Helper()

	for {
		height, err := srv.JSONRPC().Eth().BlockNumber()
		require.NoError(t, err)
		if height%epochSize <= maxOffset {
			return
		}

		require.Empty(t, framework.WaitForServersToSeal([]*framework.TestServer{srv}, height+1))
	}
}

func waitForNextEpochTransition(t *testing.T, servers []*framework.TestServer, epochSize uint64) {
	t.Helper()

	heightNow, err := servers[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	nextBoundary := ((heightNow / epochSize) + 1) * epochSize
	require.Empty(t, framework.WaitForServersToSeal(servers, nextBoundary+1))
}

func mustGetStakingValidatorList(t *testing.T, srv *framework.TestServer, from types.Address) []types.Address {
	t.Helper()

	res, err := framework.GetValidatorSet(from, srv.JSONRPC())
	require.NoError(t, err)

	return res
}

func countSelectionTiersFromValidatorList(
	t *testing.T,
	srv *framework.TestServer,
	queryFrom types.Address,
	minStake *big.Int,
) (tier1 uint64, tier2 uint64, tier3 uint64) {
	t.Helper()

	for _, addr := range mustGetStakingValidatorList(t, srv, queryFrom) {
		info, err := queryValidatorInfo(srv, queryFrom, addr)
		require.NoError(t, err)

		if info.StakedAmount.Sign() > 0 {
			tier3++
		}
		if info.Active {
			tier2++
		}
		if info.Active && info.StakedAmount.Cmp(minStake) >= 0 {
			tier1++
		}
	}

	return tier1, tier2, tier3
}

func TestPoS_StakingV2_MaxValidatorsTieBreakIsDeterministic(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2ClusterWithStandbyNode(t, 5, 4, 5, 3, 4)

	minStake, err := queryMinStake(servers[0], addrs[0])
	require.NoError(t, err)
	threshold, err := queryValidatorThreshold(servers[0], addrs[0])
	require.NoError(t, err)
	require.Greater(t, threshold.Cmp(minStake), 0, "threshold must be greater than min self stake")

	// Raise two incumbents above threshold so the capped last seat is contested only by A/B.
	require.NoError(t, framework.StakeAmount(addrs[0], keys[0], minStake, servers[0]))
	require.NoError(t, framework.StakeAmount(addrs[1], keys[1], minStake, servers[1]))

	candidateA := addrs[2]
	candidateB := addrs[3]

	// Use the 5th real running validator as strict-eligible extra candidate.
	// This creates 5 strict-eligible validators for maxValidators=4:
	// fixed winners = {topup0, topup1, extra}; tie-break = {A, B} for final seat.
	extraAddr := addrs[4]
	extraKey := keys[4]
	extraStake := new(big.Int).Add(threshold, new(big.Int).Mul(minStake, big.NewInt(50)))
	requiredExtra := new(big.Int).Add(extraStake, framework.EthToWei(2))
	ensureStakeFunding(t, servers[0], addrs[0], keys[0], extraAddr, requiredExtra)
	require.NoError(t, framework.StakeAmount(extraAddr, extraKey, extraStake, servers[4]))

	var metExtra bool
	for i := 0; i < 6; i++ {
		metExtra, err = queryIsValidatorThresholdMet(servers[0], addrs[0], extraAddr)
		require.NoError(t, err)
		joinExtra, err := queryJoinEffectiveAtBlock(servers[0], addrs[0], extraAddr)
		require.NoError(t, err)
		heightNow, err := servers[0].JSONRPC().Eth().BlockNumber()
		require.NoError(t, err)

		if metExtra && new(big.Int).SetUint64(heightNow).Cmp(joinExtra) >= 0 {
			break
		}

		waitForNextEpochTransition(t, servers, 5)
	}
	require.True(t, metExtra, "extra validator must be strict Tier1-eligible")
	metA, err := queryIsValidatorThresholdMet(servers[0], addrs[0], candidateA)
	require.NoError(t, err)
	metB, err := queryIsValidatorThresholdMet(servers[0], addrs[0], candidateB)
	require.NoError(t, err)
	require.True(t, metA && metB, "tie-break must be evaluated only after both candidates are strict Tier1-eligible")

	snap1 := mustGetLatestSnapshotValidators(t, servers[0])
	require.Len(t, snap1, 4)
	require.Contains(t, snap1, addrs[0])
	require.Contains(t, snap1, addrs[1])
	require.Contains(t, snap1, extraAddr)
	winnerA := containsAddress(snap1, candidateA)
	winnerB := containsAddress(snap1, candidateB)
	require.NotEqual(t, winnerA, winnerB, "exactly one candidate should win the tie for the capped seat")

	height, err := servers[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), framework.DefaultTimeout)
	defer cancel()
	snapAtHFromSrv0, err := getSnapshotValidatorsAt(ctx, servers[0], height)
	require.NoError(t, err)
	snapAtHFromSrv1, err := getSnapshotValidatorsAt(ctx, servers[1], height)
	require.NoError(t, err)

	assert.Equal(t, containsAddress(snapAtHFromSrv0, candidateA), containsAddress(snapAtHFromSrv1, candidateA))
	assert.Equal(t, containsAddress(snapAtHFromSrv0, candidateB), containsAddress(snapAtHFromSrv1, candidateB))
	assert.Equal(t, winnerA, containsAddress(snapAtHFromSrv0, candidateA))
	assert.Equal(t, winnerB, containsAddress(snapAtHFromSrv0, candidateB))
}

func containsAddress(set []types.Address, target types.Address) bool {
	for _, addr := range set {
		if addr == target {
			return true
		}
	}

	return false
}
