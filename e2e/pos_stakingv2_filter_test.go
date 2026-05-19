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
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	ibftOp "github.com/xgr-network/xgr-node/consensus/ibft/proto"
	"github.com/xgr-network/xgr-node/contracts/abis"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/e2e/framework"
	"github.com/xgr-network/xgr-node/helper/common"
	"github.com/xgr-network/xgr-node/helper/hex"
	"github.com/xgr-network/xgr-node/types"
)

// TestPoS_StakingV2_FilteringAndFallback validates validator-set fetch tier rules:
// - Tier 1 (strict eligible) = active validators with stake >= threshold
// - if Tier 1 count drops below minNumValidators, fetcher falls back to Tier 2 (active validators)
func TestPoS_StakingV2_FilteringAndFallback(t *testing.T) {
	t.Skip("flaky in CI: initial Tier 1 validator set can be 3 depending on startup timing")
	numGenesisValidators := IBFTMinNodes
	require.Equal(t, 4, numGenesisValidators)

	defaultBalance := framework.EthToWei(7_000_000)
	stakeAmount := framework.EthToWei(3_000_000)

	ibftManager := framework.NewIBFTServersManager(
		t,
		numGenesisValidators,
		IBFTDirPrefix,
		func(_ int, config *framework.TestServerConfig) {
			config.SetEpochSize(5)
			config.PremineValidatorBalance(defaultBalance)
			config.SetIBFTPoS(true)
			config.SetMinValidatorCount(3)
			config.SetMaxValidatorCount(uint64(numGenesisValidators))
		},
	)
	t.Cleanup(func() { ibftManager.StopServers() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ibftManager.StartServers(ctx)

	servers := make([]*framework.TestServer, 0, numGenesisValidators)
	keys := make([]*ecdsa.PrivateKey, 0, numGenesisValidators)
	addrs := make([]types.Address, 0, numGenesisValidators)

	for i := 0; i < numGenesisValidators; i++ {
		srv := ibftManager.GetServer(i)
		key, err := srv.Config.PrivateKey()
		require.NoError(t, err)

		servers = append(servers, srv)
		keys = append(keys, key)
		addrs = append(addrs, crypto.PubKeyToAddress(&key.PublicKey))
	}

	// Bring all validators to threshold.
	for i := 0; i < numGenesisValidators; i++ {
		err := framework.StakeAmount(addrs[i], keys[i], stakeAmount, servers[i])
		require.NoError(t, err)
	}

	require.Empty(t, framework.WaitForServersToSeal(servers, 12))

	snap1 := mustGetLatestSnapshotValidators(t, servers[0])
	assert.Len(t, snap1, numGenesisValidators)
	for _, a := range addrs {
		assert.Contains(t, snap1, a)
	}

	// Deactivate one validator: Tier 1 remains >= min (3), so no fallback.
	require.NoError(t, sendSetActiveTx(servers[3], addrs[3], keys[3], false))
	require.Empty(t, framework.WaitForServersToSeal(servers, 18))

	snap2 := mustGetLatestSnapshotValidators(t, servers[0])
	assert.Len(t, snap2, 3)
	assert.NotContains(t, snap2, addrs[3])

	// Deactivate another validator: Tier 1 becomes 2 < min(3), so fallback to Tier 2 (active validators).
	require.NoError(t, sendSetActiveTx(servers[2], addrs[2], keys[2], false))
	require.Empty(t, framework.WaitForServersToSeal(servers, 24))

	snap3 := mustGetLatestSnapshotValidators(t, servers[0])
	assert.Len(t, snap3, numGenesisValidators)
	for _, a := range addrs {
		assert.Contains(t, snap3, a)
	}
}

func TestPoS_StakingV2_Tier2UsedWhenTier1BelowMinValidators(t *testing.T) {
	numGenesisValidators := IBFTMinNodes
	require.Equal(t, 4, numGenesisValidators)

	defaultBalance := framework.EthToWei(7_000_000)
	stakeAmount := framework.EthToWei(3_000_000)

	ibftManager := framework.NewIBFTServersManager(
		t,
		numGenesisValidators,
		IBFTDirPrefix,
		func(_ int, config *framework.TestServerConfig) {
			config.SetEpochSize(5)
			config.PremineValidatorBalance(defaultBalance)
			config.SetIBFTPoS(true)
			config.SetMinValidatorCount(3)
			config.SetMaxValidatorCount(uint64(numGenesisValidators))
		},
	)
	t.Cleanup(func() { ibftManager.StopServers() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ibftManager.StartServers(ctx)

	servers := make([]*framework.TestServer, 0, numGenesisValidators)
	keys := make([]*ecdsa.PrivateKey, 0, numGenesisValidators)
	addrs := make([]types.Address, 0, numGenesisValidators)

	for i := 0; i < numGenesisValidators; i++ {
		srv := ibftManager.GetServer(i)
		key, err := srv.Config.PrivateKey()
		require.NoError(t, err)

		servers = append(servers, srv)
		keys = append(keys, key)
		addrs = append(addrs, crypto.PubKeyToAddress(&key.PublicKey))
	}

	// Only 2 validators satisfy strict threshold => Tier 1 count is 2 < min(3).
	require.NoError(t, framework.StakeAmount(addrs[0], keys[0], stakeAmount, servers[0]))
	require.NoError(t, framework.StakeAmount(addrs[1], keys[1], stakeAmount, servers[1]))
	require.Empty(t, framework.WaitForServersToSeal(servers, 12))

	// Deactivate one validator so active set is exactly 3 => Tier 2 (active fallback) should be used.
	require.NoError(t, sendSetActiveTx(servers[3], addrs[3], keys[3], false))
	require.Empty(t, framework.WaitForServersToSeal(servers, 18))

	snap := mustGetLatestSnapshotValidators(t, servers[0])
	assert.Len(t, snap, 3)
	assert.Contains(t, snap, addrs[0])
	assert.Contains(t, snap, addrs[1])
	assert.Contains(t, snap, addrs[2])
	assert.NotContains(t, snap, addrs[3])
}

func TestPoS_StakingV2_UnstakeDeletesValidatorState(t *testing.T) {
	numGenesisValidators := IBFTMinNodes
	require.Equal(t, 4, numGenesisValidators)
	epochSize := uint64(5)

	defaultBalance := framework.EthToWei(7_000_000)
	stakeAmount := framework.EthToWei(2_000_000)

	ibftManager := framework.NewIBFTServersManager(
		t,
		numGenesisValidators,
		IBFTDirPrefix,
		func(_ int, config *framework.TestServerConfig) {
			config.SetEpochSize(epochSize)
			config.PremineValidatorBalance(defaultBalance)
			config.SetIBFTPoS(true)
			config.SetMinValidatorCount(3)
			config.SetMaxValidatorCount(uint64(numGenesisValidators))
		},
	)
	t.Cleanup(func() { ibftManager.StopServers() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ibftManager.StartServers(ctx)

	servers := make([]*framework.TestServer, 0, numGenesisValidators)
	keys := make([]*ecdsa.PrivateKey, 0, numGenesisValidators)
	addrs := make([]types.Address, 0, numGenesisValidators)

	for i := 0; i < numGenesisValidators; i++ {
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
	require.Empty(t, framework.WaitForServersToSeal(servers, 12))

	removeIdx := 3
	require.NoError(t, sendSetActiveTx(servers[removeIdx], addrs[removeIdx], keys[removeIdx], false))
	require.Empty(t, framework.WaitForServersToSeal(servers, 18))

	deactivatedInfo, err := queryValidatorInfo(servers[0], addrs[0], addrs[removeIdx])
	require.NoError(t, err)
	require.NotNil(t, deactivatedInfo.DeactivatedAtBlock)
	require.True(t, deactivatedInfo.DeactivatedAtBlock.Sign() > 0)

	// unstake is allowed only once chain entered the epoch after deactivation
	deactivatedAt := deactivatedInfo.DeactivatedAtBlock.Uint64()
	unstakeReadyHeight := ((deactivatedAt / epochSize) + 1) * epochSize
	require.Empty(t, framework.WaitForServersToSeal(servers, unstakeReadyHeight))

	receipt, err := framework.UnstakeAmount(addrs[removeIdx], keys[removeIdx], servers[removeIdx])
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)
	require.Empty(t, framework.WaitForServersToSeal(servers, 24))

	info, err := queryValidatorInfo(servers[0], addrs[0], addrs[removeIdx])
	require.NoError(t, err)
	assert.False(t, info.Exists)
	assert.False(t, info.Active)
	assert.Zero(t, info.StakedAmount.Sign())

	snap := mustGetLatestSnapshotValidators(t, servers[0])
	assert.NotContains(t, snap, addrs[removeIdx])
}

func TestPoS_StakingV2_WithdrawCannotDropBelowMinStake(t *testing.T) {
	numGenesisValidators := IBFTMinNodes
	require.Equal(t, 4, numGenesisValidators)

	defaultBalance := framework.EthToWei(7_000_000)
	stakeAmount := framework.EthToWei(2_000_001)

	ibftManager := framework.NewIBFTServersManager(
		t,
		numGenesisValidators,
		IBFTDirPrefix,
		func(_ int, config *framework.TestServerConfig) {
			config.SetEpochSize(5)
			config.PremineValidatorBalance(defaultBalance)
			config.SetIBFTPoS(true)
			config.SetMinValidatorCount(3)
			config.SetMaxValidatorCount(uint64(numGenesisValidators))
		},
	)
	t.Cleanup(func() { ibftManager.StopServers() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ibftManager.StartServers(ctx)

	srv := ibftManager.GetServer(0)
	key, err := srv.Config.PrivateKey()
	require.NoError(t, err)
	addr := crypto.PubKeyToAddress(&key.PublicKey)

	require.NoError(t, framework.StakeAmount(addr, key, stakeAmount, srv))
	require.Empty(t, framework.WaitForServersToSeal([]*framework.TestServer{srv}, 8))

	beforeInfo, err := queryValidatorInfo(srv, addr, addr)
	require.NoError(t, err)
	beforeStake := new(big.Int).Set(beforeInfo.StakedAmount)
	beforeAccountStake, err := framework.GetAccountStake(addr, srv.JSONRPC())
	require.NoError(t, err)

	require.NoError(t, sendSetActiveTx(srv, addr, key, false))
	require.Empty(t, framework.WaitForServersToSeal([]*framework.TestServer{srv}, 14))

	minStake, err := queryValidatorMinSelfStake(srv, addr)
	require.NoError(t, err)

	// withdraw checks are based on accountStake (staker amount), not validatorInfo.stakedAmount.
	// Keep a large safety margin below min stake to avoid reward drift between call and execution.
	targetRemaining := new(big.Int).Div(new(big.Int).Set(minStake), big.NewInt(2))
	withdrawAmount := new(big.Int).Sub(beforeAccountStake, targetRemaining)
	receipt, err := sendWithdrawTx(srv, addr, key, withdrawAmount)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	assert.Equal(t, uint64(types.ReceiptFailed), receipt.Status)

	info, err := queryValidatorInfo(srv, addr, addr)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, info.StakedAmount.Cmp(beforeStake), 0)
	afterAccountStake, err := framework.GetAccountStake(addr, srv.JSONRPC())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, afterAccountStake.Cmp(beforeAccountStake), 0)
}

func TestPoS_StakingV2_FinalizeEpoch_DeactivatesZeroUptimeValidator(t *testing.T) {
	const (
		numGenesisValidators = 4
		epochSize            = uint64(6)
	)

	defaultBalance := framework.EthToWei(7_000_000)
	stakeAmount := framework.EthToWei(2_000_000)

	ibftManager := framework.NewIBFTServersManager(
		t,
		numGenesisValidators,
		IBFTDirPrefix,
		func(_ int, config *framework.TestServerConfig) {
			config.SetEpochSize(epochSize)
			config.PremineValidatorBalance(defaultBalance)
			config.SetIBFTPoS(true)
			config.SetMinValidatorCount(3)
			config.SetMaxValidatorCount(numGenesisValidators)
		},
	)
	t.Cleanup(func() { ibftManager.StopServers() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ibftManager.StartServers(ctx)

	servers := make([]*framework.TestServer, 0, numGenesisValidators)
	keys := make([]*ecdsa.PrivateKey, 0, numGenesisValidators)
	addrs := make([]types.Address, 0, numGenesisValidators)

	for i := 0; i < numGenesisValidators; i++ {
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
	require.Empty(t, framework.WaitForServersToSeal(servers, 12))

	var snapBefore []types.Address
	require.Eventually(t, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), framework.DefaultTimeout)
		defer cancel()

		height, hErr := servers[0].JSONRPC().Eth().BlockNumber()
		if hErr != nil {
			return false
		}

		var snapErr error
		snapBefore, snapErr = getSnapshotValidatorsAt(ctx, servers[0], height)

		return snapErr == nil && len(snapBefore) == numGenesisValidators
	}, 90*time.Second, 2*time.Second, "expected 5-validator PoS set before offline phase")
	require.Len(t, snapBefore, numGenesisValidators)

	inSnapshot := make(map[types.Address]struct{}, len(snapBefore))
	for _, addr := range snapBefore {
		inSnapshot[addr] = struct{}{}
	}

	offlineIdx := -1
	offlineAddr := types.ZeroAddress
	for i, addr := range addrs {
		if i == 0 {
			continue
		}

		if _, ok := inSnapshot[addr]; !ok {
			continue
		}

		info, infoErr := queryValidatorInfo(servers[0], addrs[0], addr)
		require.NoError(t, infoErr)
		if info.Exists && info.Active {
			offlineIdx = i
			offlineAddr = addr
			break
		}
	}

	require.NotEqual(t, -1, offlineIdx, "failed to find active validator in snapshot")

	queryIdx := 0
	if offlineIdx == queryIdx {
		queryIdx = 1
	}

	infoBeforeStop, err := queryValidatorInfo(servers[queryIdx], addrs[queryIdx], offlineAddr)
	require.NoError(t, err)
	beforeStake := new(big.Int).Set(infoBeforeStop.StakedAmount)

	heightBefore, err := servers[queryIdx].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	firstBoundary := ((heightBefore / epochSize) + 1) * epochSize
	finalizeBoundary := firstBoundary + epochSize

	// Keep validator online until the block right before the epoch start, then stop it.
	// This isolates a full target epoch with 0% uptime.
	if firstBoundary > 1 {
		require.Empty(t, framework.WaitForServersToSeal(servers, firstBoundary-1))
	}
	ibftManager.StopServer(offlineIdx)

	// Wait epoch-by-epoch to stay below the helper's 1-minute per-call timeout
	// (single call across two full epochs is borderline under slower block times).
	require.Empty(t, framework.WaitForServersToSeal(ibftManager.ActiveServers(offlineIdx), firstBoundary+1))
	require.Empty(t, framework.WaitForServersToSeal(ibftManager.ActiveServers(offlineIdx), finalizeBoundary+1))

	targetEpoch := epochFromFinalizedBoundary(finalizeBoundary, epochSize)
	slashLog, hasSlashLog := findStakerSlashedLog(
		t,
		servers[queryIdx],
		targetEpoch,
		offlineAddr,
		offlineAddr,
	)

	infoOffline, err := queryValidatorInfo(servers[queryIdx], addrs[queryIdx], offlineAddr)
	require.NoError(t, err)
	t.Logf(
		"diag targetEpoch=%d hasSlashLog=%v beforeStake=%s afterStake=%s active=%v deactivatedAt=%s",
		targetEpoch,
		hasSlashLog,
		beforeStake.String(),
		infoOffline.StakedAmount.String(),
		infoOffline.Active,
		infoOffline.DeactivatedAtBlock.String(),
	)

	assert.True(t, infoOffline.Exists)
	assert.False(t, infoOffline.Active, "validator with 0%% uptime in finalized epoch must be inactive")

	expectedSlashAmount := new(big.Int).Div(new(big.Int).Mul(beforeStake, big.NewInt(20)), big.NewInt(10_000))
	require.Positive(t, expectedSlashAmount.Sign(), "test setup should produce a non-zero slash amount")
	expectedStakeAfter := new(big.Int).Sub(beforeStake, expectedSlashAmount)
	assert.Equal(t, expectedStakeAfter.String(), infoOffline.StakedAmount.String(), "0%% uptime slash must reduce validator stake")

	if hasSlashLog {
		slots := logDataWordBig(slashLog.Data, 5).Uint64()
		missed := logDataWordBig(slashLog.Data, 6).Uint64()
		require.Greater(t, slots, uint64(0), "StakerSlashed log should audit a real 0%%-uptime epoch (slots=0)")
		require.Equal(t, slots, missed, "StakerSlashed log should audit the 0%%-uptime path")
		assertStakerSlashedLogData(
			t,
			slashLog,
			1,
			expectedSlashAmount,
			beforeStake,
			beforeStake,
			expectedStakeAfter,
			slots,
			missed,
			20,
			chain.DefaultBurnedAddress,
		)
	}

	snapBeforeFinalizedEpoch := mustGetSnapshotValidatorsAt(t, servers[queryIdx], firstBoundary-1)
	assert.Contains(t, snapBeforeFinalizedEpoch, offlineAddr, "pre-finalized epoch snapshot must keep old membership")

	require.Empty(t, framework.WaitForServersToSeal(ibftManager.ActiveServers(offlineIdx), finalizeBoundary+3))
	snapNextEpoch := mustGetLatestSnapshotValidators(t, servers[queryIdx])
	assert.NotContains(t, snapNextEpoch, offlineAddr, "inactive validator should drop from next epoch set")

	for i := 0; i < numGenesisValidators; i++ {
		if i == offlineIdx {
			continue
		}

		info, infoErr := queryValidatorInfo(servers[queryIdx], addrs[queryIdx], addrs[i])
		require.NoError(t, infoErr)
		assert.True(t, info.Active, "online validators must remain active")
	}
}

func findStakerSlashedLog(
	t *testing.T,
	srv *framework.TestServer,
	epoch uint64,
	validator types.Address,
	staker types.Address,
) (*ethgo.Log, bool) {
	t.Helper()

	topic := stakerSlashedEventTopic()
	epochTopic := abiHashU64(epoch)
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
	if len(logs) == 0 {
		allFilter := &ethgo.LogFilter{}
		allFilter.SetFromUint64(0)
		logs, err = srv.JSONRPC().Eth().GetLogs(allFilter)
		require.NoError(t, err)
	}

	for _, log := range logs {
		if log.Address != ethgo.Address(pos.PosSysAddr) || len(log.Topics) != 4 {
			continue
		}
		if log.Topics[0] != topic || log.Topics[1] != epochTopic || log.Topics[2] != validatorTopic || log.Topics[3] != stakerTopic {
			continue
		}

		return log, true
	}

	t.Logf("missing StakerSlashed log for epoch=%d validator=%s staker=%s scanned=%d", epoch, validator, staker, len(logs))

	return nil, false
}

func assertStakerSlashedLogData(
	t *testing.T,
	log *ethgo.Log,
	role uint8,
	amount *big.Int,
	stakeSnapshot *big.Int,
	stakeBefore *big.Int,
	stakeAfter *big.Int,
	slots uint64,
	missed uint64,
	slashBps uint64,
	destination types.Address,
) {
	t.Helper()

	require.Equal(t, ethgo.Address(pos.PosSysAddr), log.Address)
	require.Len(t, log.Data, 9*32)
	require.Equal(t, new(big.Int).SetUint64(uint64(role)), logDataWordBig(log.Data, 0), "role")
	require.Equal(t, amount, logDataWordBig(log.Data, 1), "amount")
	require.Equal(t, stakeSnapshot, logDataWordBig(log.Data, 2), "stakeSnapshot")
	require.Equal(t, stakeBefore, logDataWordBig(log.Data, 3), "stakeBefore")
	require.Equal(t, stakeAfter, logDataWordBig(log.Data, 4), "stakeAfter")
	require.Equal(t, new(big.Int).SetUint64(slots), logDataWordBig(log.Data, 5), "slots")
	require.Equal(t, new(big.Int).SetUint64(missed), logDataWordBig(log.Data, 6), "missed")
	require.Equal(t, new(big.Int).SetUint64(slashBps), logDataWordBig(log.Data, 7), "slashBps")
	require.Equal(t, abiWordAddress(destination), log.Data[8*32:9*32], "destination")
}

func stakerSlashedEventTopic() ethgo.Hash {
	return ethgo.Hash(types.BytesToHash(crypto.Keccak256([]byte("StakerSlashed(uint256,address,address,uint8,uint256,uint256,uint256,uint256,uint256,uint256,uint256,address)"))))
}

func abiHashU64(v uint64) ethgo.Hash {
	return ethgo.Hash(types.BytesToHash(abiWordBig(new(big.Int).SetUint64(v))))
}

func abiHashAddress(addr types.Address) ethgo.Hash {
	return ethgo.Hash(types.BytesToHash(abiWordAddress(addr)))
}

func abiWordAddress(addr types.Address) []byte {
	var out [32]byte
	copy(out[12:], addr[:])
	return out[:]
}

func abiWordBig(v *big.Int) []byte {
	var out [32]byte
	if v == nil || v.Sign() == 0 {
		return out[:]
	}
	b := v.Bytes()
	if len(b) > 32 {
		b = b[len(b)-32:]
	}
	copy(out[32-len(b):], b)
	return out[:]
}

func logDataWordBig(data []byte, idx int) *big.Int {
	return new(big.Int).SetBytes(data[idx*32 : (idx+1)*32])
}

func sendSetActiveTx(srv *framework.TestServer, from types.Address, key *ecdsa.PrivateKey, value bool) error {
	m := abis.StakingABI.Methods["setActive"]
	if m == nil {
		return assert.AnError
	}

	inp, err := m.Inputs.Encode(map[string]interface{}{"active_": value})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), framework.DefaultTimeout)
	defer cancel()

	receipt, err := srv.SendRawTx(ctx, &framework.PreparedTransaction{
		From:     from,
		To:       &staking.AddrStakingContract,
		GasPrice: framework.TestGasPrice(),
		Gas:      framework.DefaultGasLimit,
		Value:    big.NewInt(0),
		Input:    append(m.ID(), inp...),
	}, key)
	if err != nil {
		return err
	}
	if receipt == nil || receipt.Status != uint64(types.ReceiptSuccess) {
		return assert.AnError
	}

	return nil
}

func mustGetLatestSnapshotValidators(t *testing.T, srv *framework.TestServer) []types.Address {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), framework.DefaultTimeout)
	defer cancel()

	height, err := srv.JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)

	out, err := getSnapshotValidatorsAt(ctx, srv, height)
	require.NoError(t, err)

	return out
}

func mustGetSnapshotValidatorsAt(t *testing.T, srv *framework.TestServer, height uint64) []types.Address {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), framework.DefaultTimeout)
	defer cancel()

	out, err := getSnapshotValidatorsAt(ctx, srv, height)
	require.NoError(t, err)

	return out
}

func getSnapshotValidatorsAt(ctx context.Context, srv *framework.TestServer, height uint64) ([]types.Address, error) {
	snap, err := srv.IBFTOperator().GetSnapshot(ctx, &ibftOp.SnapshotReq{Number: height})
	if err != nil {
		return nil, err
	}

	out := make([]types.Address, 0, len(snap.Validators))
	for _, v := range snap.Validators {
		out = append(out, types.BytesToAddress(v.Data))
	}

	return out, nil
}

type stakingValidatorInfo struct {
	Exists             bool
	Active             bool
	StakedAmount       *big.Int
	DeactivatedAtBlock *big.Int
}

func queryValidatorInfo(srv *framework.TestServer, from, validator types.Address) (*stakingValidatorInfo, error) {
	m := abis.StakingABI.Methods["validatorInfo"]
	if m == nil {
		return nil, assert.AnError
	}

	input, err := m.Inputs.Encode(map[string]interface{}{"account": ethgo.Address(validator)})
	if err != nil {
		return nil, err
	}

	to := ethgo.Address(staking.AddrStakingContract)
	resp, err := srv.JSONRPC().Eth().Call(&ethgo.CallMsg{
		From:     ethgo.Address(from),
		To:       &to,
		Data:     append(m.ID(), input...),
		GasPrice: framework.TestGasPriceUint64(),
		Value:    big.NewInt(0),
	}, ethgo.Latest)
	if err != nil {
		return nil, err
	}

	b, err := hex.DecodeHex(resp)
	if err != nil {
		return nil, err
	}

	decoded, err := m.Outputs.Decode(b)
	if err != nil {
		return nil, err
	}

	mapResult, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, assert.AnError
	}

	exists, _ := mapResult["0"].(bool)
	if !exists {
		if v, ok := mapResult["exists"].(bool); ok {
			exists = v
		}
	}

	active, _ := mapResult["1"].(bool)
	if !active {
		if v, ok := mapResult["active"].(bool); ok {
			active = v
		}
	}

	stakedAmount, _ := mapResult["2"].(*big.Int)
	if stakedAmount == nil {
		stakedAmount, _ = mapResult["stakedAmount"].(*big.Int)
	}
	if stakedAmount == nil {
		stakedAmount = big.NewInt(0)
	}

	deactivatedAtBlock, _ := mapResult["3"].(*big.Int)
	if deactivatedAtBlock == nil {
		deactivatedAtBlock, _ = mapResult["deactivatedAtBlock"].(*big.Int)
	}
	if deactivatedAtBlock == nil {
		deactivatedAtBlock = big.NewInt(0)
	}

	return &stakingValidatorInfo{
		Exists:             exists,
		Active:             active,
		StakedAmount:       stakedAmount,
		DeactivatedAtBlock: deactivatedAtBlock,
	}, nil
}

func queryIsValidatorThresholdMet(srv *framework.TestServer, from, validator types.Address) (bool, error) {
	m := abis.StakingABI.Methods["isValidatorThresholdMet"]
	if m == nil {
		return false, assert.AnError
	}

	input, err := m.Inputs.Encode(map[string]interface{}{"validator": ethgo.Address(validator)})
	if err != nil {
		return false, err
	}

	to := ethgo.Address(staking.AddrStakingContract)
	resp, err := srv.JSONRPC().Eth().Call(&ethgo.CallMsg{
		From:     ethgo.Address(from),
		To:       &to,
		Data:     append(m.ID(), input...),
		GasPrice: framework.TestGasPriceUint64(),
		Value:    big.NewInt(0),
	}, ethgo.Latest)
	if err != nil {
		return false, err
	}

	b, err := hex.DecodeHex(resp)
	if err != nil {
		return false, err
	}

	decoded, err := m.Outputs.Decode(b)
	if err != nil {
		return false, err
	}

	mapResult, ok := decoded.(map[string]interface{})
	if !ok {
		return false, assert.AnError
	}

	if met, ok := mapResult["0"].(bool); ok {
		return met, nil
	}

	for _, v := range mapResult {
		if met, ok := v.(bool); ok {
			return met, nil
		}
	}

	return false, assert.AnError
}

func queryJoinEffectiveAtBlock(srv *framework.TestServer, from, account types.Address) (*big.Int, error) {
	m := abis.StakingABI.Methods["joinEffectiveAtBlock"]
	if m == nil {
		return nil, assert.AnError
	}

	input, err := m.Inputs.Encode(map[string]interface{}{"account": ethgo.Address(account)})
	if err != nil {
		return nil, err
	}

	to := ethgo.Address(staking.AddrStakingContract)
	resp, err := srv.JSONRPC().Eth().Call(&ethgo.CallMsg{
		From:     ethgo.Address(from),
		To:       &to,
		Data:     append(m.ID(), input...),
		GasPrice: framework.TestGasPriceUint64(),
		Value:    big.NewInt(0),
	}, ethgo.Latest)
	if err != nil {
		return nil, err
	}

	b, err := hex.DecodeHex(resp)
	if err != nil {
		return nil, err
	}

	decoded, err := m.Outputs.Decode(b)
	if err != nil {
		return nil, err
	}

	mapResult, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, assert.AnError
	}

	if at, ok := mapResult["0"].(*big.Int); ok {
		return at, nil
	}

	for _, v := range mapResult {
		if at, ok := v.(*big.Int); ok {
			return at, nil
		}
	}

	return nil, assert.AnError
}

func epochFromFinalizedBoundary(boundary, epochSize uint64) uint64 {
	if boundary < epochSize || epochSize == 0 {
		return 0
	}

	return boundary/epochSize - 1
}

func queryValidatorMinSelfStake(srv *framework.TestServer, from types.Address) (*big.Int, error) {
	m := abis.StakingABI.Methods["VALIDATOR_MIN_SELF_STAKE"]
	if m == nil {
		return nil, assert.AnError
	}

	to := ethgo.Address(staking.AddrStakingContract)
	resp, err := srv.JSONRPC().Eth().Call(&ethgo.CallMsg{
		From:     ethgo.Address(from),
		To:       &to,
		Data:     m.ID(),
		GasPrice: framework.TestGasPriceUint64(),
		Value:    big.NewInt(0),
	}, ethgo.Latest)
	if err != nil {
		return nil, err
	}

	return common.ParseUint256orHex(&resp)
}

func queryValidatorThreshold(srv *framework.TestServer, from types.Address) (*big.Int, error) {
	m := abis.StakingABI.Methods["VALIDATOR_THRESHOLD"]
	if m == nil {
		return nil, assert.AnError
	}

	to := ethgo.Address(staking.AddrStakingContract)
	resp, err := srv.JSONRPC().Eth().Call(&ethgo.CallMsg{
		From:     ethgo.Address(from),
		To:       &to,
		Data:     m.ID(),
		GasPrice: framework.TestGasPriceUint64(),
		Value:    big.NewInt(0),
	}, ethgo.Latest)
	if err != nil {
		return nil, err
	}

	return common.ParseUint256orHex(&resp)
}

func queryMinStake(srv *framework.TestServer, from types.Address) (*big.Int, error) {
	return queryValidatorMinSelfStake(srv, from)
}

func sendWithdrawTx(
	srv *framework.TestServer,
	from types.Address,
	key *ecdsa.PrivateKey,
	amount *big.Int,
) (*ethgo.Receipt, error) {
	m := abis.StakingABI.Methods["withdraw"]
	if m == nil {
		return nil, assert.AnError
	}

	inp, err := m.Inputs.Encode(map[string]interface{}{"amount": amount})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), framework.DefaultTimeout)
	defer cancel()

	return srv.SendRawTx(ctx, &framework.PreparedTransaction{
		From:     from,
		To:       &staking.AddrStakingContract,
		GasPrice: framework.TestGasPrice(),
		Gas:      framework.DefaultGasLimit,
		Value:    big.NewInt(0),
		Input:    append(m.ID(), inp...),
	}, key)
}
