package pos

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/helper/common"
	stakingHelper "github.com/xgr-network/xgr-node/helper/staking"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func setStakerForEpoch(t *testing.T, txn *state.Transition, epoch uint64, addr types.Address, joinedAt uint64, amount int64) {
	t.Helper()
	writeStakerInfo(txn, addr, true, true, joinedAt, 0, types.ZeroAddress)
	txn.Txn().SetState(PosSysAddr, keyStakerStakeSnapshot(epoch, addr), bigToHash(big.NewInt(amount)))
	writeStakedAmount(txn, addr, big.NewInt(amount))
}

func writeStakerInfo(txn *state.Transition, addr types.Address, exists bool, active bool, joinedAt uint64, deactivatedAt uint64, validator types.Address) {
	flags := uint64(0)
	if exists {
		flags |= 1
	}
	if active {
		flags |= 1 << 8
	}
	txn.Txn().SetState(staking.AddrStakingContract, stakerSlotWithOffset(addr, 0), bigToHash(new(big.Int).SetUint64(flags)))
	txn.Txn().SetState(staking.AddrStakingContract, stakerSlotWithOffset(addr, 2), bigToHash(new(big.Int).SetUint64(joinedAt)))
	txn.Txn().SetState(staking.AddrStakingContract, stakerSlotWithOffset(addr, 3), bigToHash(new(big.Int).SetUint64(deactivatedAt)))
	txn.Txn().SetState(staking.AddrStakingContract, stakerSlotWithOffset(addr, 4), types.BytesToHash(common.PadLeftOrTrim(validator.Bytes(), 32)))
}

func appendValidatorStaker(txn *state.Transition, validator types.Address, staker types.Address) {
	base := crypto.Keccak256(common.PadLeftOrTrim(validator.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(6).Bytes(), 32))
	baseHash := types.BytesToHash(base)
	curState := txn.Txn().GetState(staking.AddrStakingContract, baseHash)
	cur := new(big.Int).SetBytes(curState[:]).Uint64()
	txn.Txn().SetState(staking.AddrStakingContract, baseHash, bigToHash(new(big.Int).SetUint64(cur+1)))
	dataBase := crypto.Keccak256(common.PadLeftOrTrim(base, 32))
	slot := new(big.Int).Add(new(big.Int).SetBytes(dataBase), new(big.Int).SetUint64(cur))
	txn.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(slot.Bytes()), types.BytesToHash(common.PadLeftOrTrim(staker.Bytes(), 32)))
}

func writeValidatorPoolConfig(txn *state.Transition, validator types.Address, enabled bool, maxDelegated *big.Int, minDelegatorStake *big.Int, commission uint16) {
	base := crypto.Keccak256(common.PadLeftOrTrim(validator.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(5).Bytes(), 32))
	var packed uint64
	packed |= 1
	packed |= 1 << 8
	if enabled {
		packed |= 1 << 16
	}
	txn.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(base), bigToHash(new(big.Int).SetUint64(packed)))
	txn.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(new(big.Int).Add(new(big.Int).SetBytes(base), big.NewInt(1)).Bytes()), bigToHash(maxDelegated))
	txn.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(new(big.Int).Add(new(big.Int).SetBytes(base), big.NewInt(2)).Bytes()), bigToHash(minDelegatorStake))
	txn.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(new(big.Int).Add(new(big.Int).SetBytes(base), big.NewInt(3)).Bytes()), bigToHash(new(big.Int).SetUint64(uint64(commission))))
}

func newPosTestTransitionWithStakingConfig(t *testing.T, vals validators.Validators, params stakingHelper.PredeployParams) *state.Transition {
	t.Helper()

	txn := newPosTestTransition(t)
	contractState, err := stakingHelper.PredeployStakingSC(vals, params)
	require.NoError(t, err)
	require.NoError(t, txn.SetAccountDirectly(staking.AddrStakingContract, contractState))

	return txn
}

func setUptimeCounters(txn *state.Transition, epoch uint64, addr types.Address, slots, missed uint64) {
	txn.Txn().SetState(PosSysAddr, keyProposerSlots(epoch, addr), u64ToHash(slots))
	txn.Txn().SetState(PosSysAddr, keyProposerMissed(epoch, addr), u64ToHash(missed))
}

func TestFinalizeEpoch_RewardSplitError_DoesNotTransferFromFeePool(t *testing.T) {
	validator := types.StringToAddress("0x1636")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validator))
	txn := newPosTestTransitionWithStakingConfig(t, set, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})

	header := makeFinalizeHeader(20, 10, set)
	epoch := uint64(2)
	requireSnapshotCreated(t, txn, epoch, set)
	setUptimeCounters(txn, epoch, validator, 10, 0)
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, validator), bigToHash(big.NewInt(100)))
	writeStakedAmount(txn, validator, big.NewInt(100))
	txn.Txn().SetBalance(state.FeePoolAddress, big.NewInt(200))

	origSplit := finalizeEpochRewardSplitFn
	finalizeEpochRewardSplitFn = func(_ *state.Transition, _ uint64, _ uint64, _ types.Address, _ *big.Int) (validatorRewardSplit, error) {
		return validatorRewardSplit{}, fmt.Errorf("injected split failure")
	}
	t.Cleanup(func() { finalizeEpochRewardSplitFn = origSplit })

	preFeePool := new(big.Int).Set(txn.Txn().GetBalance(state.FeePoolAddress))
	preStakingBal := new(big.Int).Set(txn.Txn().GetBalance(staking.AddrStakingContract))

	err := FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn)
	require.Error(t, err)
	require.Contains(t, err.Error(), "injected split failure")

	require.Equal(t, preFeePool.String(), txn.Txn().GetBalance(state.FeePoolAddress).String())
	require.Equal(t, preStakingBal.String(), txn.Txn().GetBalance(staking.AddrStakingContract).String())
	require.Equal(t, int64(100), readStakedAmount(txn, validator).Int64())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyRewardTotal(epoch))).Sign())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyValidatorReward(epoch, validator))).Sign())
}

func TestFinalizeEpoch_RewardSplitError_SecondValidator_DoesNotMutateFirstValidator(t *testing.T) {
	v1 := types.StringToAddress("0x1638")
	v2 := types.StringToAddress("0x1639")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v1), validators.NewECDSAValidator(v2))
	txn := newPosTestTransitionWithStakingConfig(t, set, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})

	header := makeFinalizeHeader(20, 10, set)
	epoch := uint64(2)
	requireSnapshotCreated(t, txn, epoch, set)
	setUptimeCounters(txn, epoch, v1, 10, 0)
	setUptimeCounters(txn, epoch, v2, 10, 0)
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, v1), bigToHash(big.NewInt(100)))
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, v2), bigToHash(big.NewInt(100)))
	writeStakedAmount(txn, v1, big.NewInt(100))
	writeStakedAmount(txn, v2, big.NewInt(100))
	txn.Txn().SetBalance(state.FeePoolAddress, big.NewInt(200))

	origSplit := finalizeEpochRewardSplitFn
	finalizeEpochRewardSplitFn = func(_ *state.Transition, _ uint64, _ uint64, validator types.Address, reward *big.Int) (validatorRewardSplit, error) {
		if validator == v2 {
			return validatorRewardSplit{}, fmt.Errorf("injected split failure on second validator")
		}
		return validatorRewardSplit{ValidatorNet: new(big.Int).Set(reward), Commission: new(big.Int), DelegatorsNet: new(big.Int), DelegatorsNetAfterCommission: new(big.Int), SelfStakeReward: new(big.Int).Set(reward), DelegatorRemainder: new(big.Int)}, nil
	}
	t.Cleanup(func() { finalizeEpochRewardSplitFn = origSplit })

	preFeePool := new(big.Int).Set(txn.Txn().GetBalance(state.FeePoolAddress))
	preStakingBal := new(big.Int).Set(txn.Txn().GetBalance(staking.AddrStakingContract))

	err := FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn)
	require.Error(t, err)
	require.Contains(t, err.Error(), "injected split failure on second validator")

	require.Equal(t, preFeePool.String(), txn.Txn().GetBalance(state.FeePoolAddress).String())
	require.Equal(t, preStakingBal.String(), txn.Txn().GetBalance(staking.AddrStakingContract).String())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyRewardTotal(epoch))).Sign())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyValidatorReward(epoch, v1))).Sign())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyValidatorReward(epoch, v2))).Sign())
	require.Equal(t, int64(100), readStakedAmount(txn, v1).Int64())
	require.Equal(t, int64(100), readStakedAmount(txn, v2).Int64())
}

func TestFinalizeEpoch_DelegatorWithoutSnapshotDoesNotGetRewardAndDoesNotCrash(t *testing.T) {
	v := types.StringToAddress("0x1701")
	d := types.StringToAddress("0x1702")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	txn := newPosTestTransitionWithStakingConfig(t, set, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	epoch := uint64(2)
	reward := big.NewInt(100)

	writeStakerInfo(txn, v, true, true, 1, 0, types.ZeroAddress)
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, v), bigToHash(big.NewInt(100)))
	setStakerForEpoch(t, txn, epoch, v, 1, 100)
	writeStakerInfo(txn, d, true, true, 5, 0, v) // live active, but no epoch snapshot on purpose
	writeStakedAmount(txn, d, big.NewInt(50))
	appendValidatorStaker(txn, v, d)

	split, err := splitValidatorRewardByDelegation(txn, 19, 10, v, reward)
	require.NoError(t, err)
	require.Equal(t, "100", split.ValidatorNet.String())
	require.Zero(t, split.Commission.Sign())
	require.Zero(t, split.DelegatorsNet.Sign())
	require.Empty(t, split.DelegatorParts)
}

func TestFinalizeEpoch_DelegatorFromPreviousEpochGetsReward(t *testing.T) {
	v := types.StringToAddress("0x1703")
	d := types.StringToAddress("0x1704")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	txn := newPosTestTransitionWithStakingConfig(t, set, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	epoch := uint64(2)

	writeStakerInfo(txn, v, true, true, 1, 0, types.ZeroAddress)
	setStakerForEpoch(t, txn, epoch, v, 1, 100)
	setStakerForEpoch(t, txn, epoch, d, 8, 100)
	appendValidatorStaker(txn, v, d)

	split, err := splitValidatorRewardByDelegation(txn, 19, 10, v, big.NewInt(100))
	require.NoError(t, err)
	require.Equal(t, "50", split.ValidatorNet.String())
	require.Equal(t, "50", split.DelegatorsNet.String())
	require.Len(t, split.DelegatorParts, 1)
	require.Equal(t, d, split.DelegatorParts[0].Delegator)
	require.Equal(t, "50", split.DelegatorParts[0].Amount.String())
}

func TestFinalizeEpoch_ReadValidatorEffectiveDelegationsAt_UsesSnapshotOnly(t *testing.T) {
	v := types.StringToAddress("0x1710")
	withSnapshot := types.StringToAddress("0x1711")
	noSnapshot := types.StringToAddress("0x1712")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	txn := newPosTestTransitionWithStakingConfig(t, set, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	epoch := uint64(2)

	setStakerForEpoch(t, txn, epoch, v, 1, 100)
	setStakerForEpoch(t, txn, epoch, withSnapshot, 1, 40)
	writeStakerInfo(txn, noSnapshot, true, true, 1, 0, v)
	writeStakedAmount(txn, noSnapshot, big.NewInt(60))
	appendValidatorStaker(txn, v, withSnapshot)
	appendValidatorStaker(txn, v, noSnapshot)

	delegations, err := readValidatorEffectiveDelegationsAt(txn, epoch, v, 19, 10)
	require.NoError(t, err)
	require.Len(t, delegations, 1)
	require.Equal(t, withSnapshot, delegations[0].Delegator)
	require.Equal(t, "40", delegations[0].Amount.String())
}

func TestFinalizeEpoch_DelegatorRewardHonorsCommission(t *testing.T) {
	v := types.StringToAddress("0x1705")
	d := types.StringToAddress("0x1706")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	txn := newPosTestTransitionWithStakingConfig(t, set, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	epoch := uint64(2)

	minDelegatorStake := big.NewInt(7777)
	commissionBps := uint16(1000)
	require.NotZero(t, minDelegatorStake.Sign())
	require.NotEqual(t, minDelegatorStake.Uint64(), uint64(commissionBps))
	writeValidatorPoolConfig(txn, v, true, big.NewInt(1000), minDelegatorStake, commissionBps) // 10%
	setStakerForEpoch(t, txn, epoch, v, 1, 100)
	setStakerForEpoch(t, txn, epoch, d, 8, 100)
	appendValidatorStaker(txn, v, d)

	split, err := splitValidatorRewardByDelegation(txn, 19, 10, v, big.NewInt(100))
	require.NoError(t, err)
	require.Equal(t, "55", split.ValidatorNet.String()) // self 50 + commission 5
	require.Equal(t, "5", split.Commission.String())
	require.Equal(t, "45", split.DelegatorsNet.String())
	require.Len(t, split.DelegatorParts, 1)
	require.Equal(t, "45", split.DelegatorParts[0].Amount.String())
}

func TestFinalizeEpoch_CommissionUsesCurrentPoolConfigSlot(t *testing.T) {
	v := types.StringToAddress("0x1713")
	d := types.StringToAddress("0x1714")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	txn := newPosTestTransitionWithStakingConfig(t, set, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	header := makeFinalizeHeader(20, 10, set)
	epoch := uint64(2)
	requireSnapshotCreated(t, txn, epoch, set)

	minDelegatorStake := big.NewInt(7777)
	commissionBps := uint16(1000)
	require.NotZero(t, minDelegatorStake.Sign())
	require.NotEqual(t, minDelegatorStake.Uint64(), uint64(commissionBps))
	writeValidatorPoolConfig(txn, v, true, big.NewInt(1000), minDelegatorStake, commissionBps)
	setStakerForEpoch(t, txn, epoch, v, 1, 100)
	setStakerForEpoch(t, txn, epoch, d, 8, 100)
	appendValidatorStaker(txn, v, d)
	setUptimeCounters(txn, epoch, v, 10, 0)
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, v), bigToHash(big.NewInt(200)))
	txn.Txn().SetBalance(state.FeePoolAddress, big.NewInt(100))

	require.NoError(t, FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn))

	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyValidatorReward(epoch, v))).Sign())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyValidatorCommissionReward(epoch, v))).Sign())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyValidatorDelegatorsRewardTotal(epoch, v))).Sign())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyDelegatorReward(epoch, v, d))).Sign())
	require.Equal(t, "155", readStakedAmount(txn, v).String())
	require.Equal(t, "145", readStakedAmount(txn, d).String())
}

func writeValidatorDelegatedAggregateForRewardTest(txn *state.Transition, validator types.Address, raw, active *big.Int) {
	for slot, amount := range map[int64]*big.Int{8: raw, 9: active} {
		base := crypto.Keccak256(common.PadLeftOrTrim(validator.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(slot).Bytes(), 32))
		txn.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(base), bigToHash(amount))
	}
}

func readValidatorDelegatedAggregateForRewardTest(txn *state.Transition, validator types.Address, slot int64) *big.Int {
	base := crypto.Keccak256(common.PadLeftOrTrim(validator.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(slot).Bytes(), 32))
	h := txn.Txn().GetState(staking.AddrStakingContract, types.BytesToHash(base))
	return new(big.Int).SetBytes(h[:])
}

func TestFinalizeEpoch_SlashingAndRewards_AccountingOrderAndConservation(t *testing.T) {
	validatorA := types.StringToAddress("0x1721")
	validatorB := types.StringToAddress("0x1722")
	delegatorA := types.StringToAddress("0x1723")
	delegatorB := types.StringToAddress("0x1724")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validatorA), validators.NewECDSAValidator(validatorB))
	txn := newPosTestTransitionWithStakingConfig(t, set, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	header := makeFinalizeHeader(20, 10, set)
	epoch := uint64(2)
	requireSnapshotCreated(t, txn, epoch, set)

	writeValidatorPoolConfig(txn, validatorB, true, big.NewInt(10_000), big.NewInt(1), 1_000)
	writeStakerInfo(txn, validatorA, true, true, 1, 0, validatorA)
	writeStakerInfo(txn, validatorB, true, true, 1, 0, validatorB)
	setStakerForEpoch(t, txn, epoch, validatorA, 1, 1_000)
	setStakerForEpoch(t, txn, epoch, delegatorA, 1, 500)
	setStakerForEpoch(t, txn, epoch, validatorB, 1, 2_000)
	setStakerForEpoch(t, txn, epoch, delegatorB, 1, 1_000)
	appendValidatorStaker(txn, validatorA, validatorA)
	appendValidatorStaker(txn, validatorA, delegatorA)
	appendValidatorStaker(txn, validatorB, validatorB)
	appendValidatorStaker(txn, validatorB, delegatorB)
	writeValidatorDelegatedAggregateForRewardTest(txn, validatorA, big.NewInt(500), big.NewInt(500))
	writeValidatorDelegatedAggregateForRewardTest(txn, validatorB, big.NewInt(1_000), big.NewInt(1_000))
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, validatorA), bigToHash(big.NewInt(1_500)))
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, validatorB), bigToHash(big.NewInt(3_000)))
	setUptimeCounters(txn, epoch, validatorA, 1, 1)
	setUptimeCounters(txn, epoch, validatorB, 1, 0)
	txn.Txn().SetBalance(staking.AddrStakingContract, big.NewInt(4_500))
	txn.Txn().SetBalance(state.FeePoolAddress, big.NewInt(900))

	require.NoError(t, FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn))

	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keySlashed(epoch, validatorA))).Sign())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keySlashAmount(epoch, validatorA))).Sign())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyValidatorReward(epoch, validatorA))).Sign())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyValidatorReward(epoch, validatorB))).Sign())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyValidatorCommissionReward(epoch, validatorB))).Sign())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyValidatorDelegatorsRewardTotal(epoch, validatorB))).Sign())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyDelegatorReward(epoch, validatorB, delegatorB))).Sign())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyRewardTotal(epoch))).Sign())
	require.Equal(t, "0", txn.Txn().GetBalance(state.FeePoolAddress).String())
	require.Equal(t, "998", readStakedAmount(txn, validatorA).String())
	require.Equal(t, "499", readStakedAmount(txn, delegatorA).String())
	require.Equal(t, "2630", readStakedAmount(txn, validatorB).String())
	require.Equal(t, "1270", readStakedAmount(txn, delegatorB).String())
	require.Equal(t, "499", readValidatorDelegatedAggregateForRewardTest(txn, validatorA, 8).String())
	require.Equal(t, "499", readValidatorDelegatedAggregateForRewardTest(txn, validatorA, 9).String())
	require.Equal(t, "1270", readValidatorDelegatedAggregateForRewardTest(txn, validatorB, 8).String())
	require.Equal(t, "1270", readValidatorDelegatedAggregateForRewardTest(txn, validatorB, 9).String())
	require.Equal(t, "5397", txn.Txn().GetBalance(staking.AddrStakingContract).String())
	require.Equal(t, "0", txn.Txn().GetBalance(state.FeePoolAddress).String())
}

func TestFinalizeEpoch_FiveValidatorsEqualStakeEqualUptime_EqualRewardsAndFinalStake(t *testing.T) {
	v1 := types.StringToAddress("0x1801")
	v2 := types.StringToAddress("0x1802")
	v3 := types.StringToAddress("0x1803")
	v4 := types.StringToAddress("0x1804")
	v5 := types.StringToAddress("0x1805")
	vals := []types.Address{v1, v2, v3, v4, v5}
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v1), validators.NewECDSAValidator(v2), validators.NewECDSAValidator(v3), validators.NewECDSAValidator(v4), validators.NewECDSAValidator(v5))
	txn := newPosTestTransitionWithStakingConfig(t, set, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	header := makeFinalizeHeader(20, 10, set)
	epoch := uint64(2)
	requireSnapshotCreated(t, txn, epoch, set)

	stake := int64(2_000_000)
	for _, v := range vals {
		setUptimeCounters(txn, epoch, v, 10, 0)
		txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, v), bigToHash(big.NewInt(stake)))
		txn.Txn().SetState(PosSysAddr, keyStakerStakeSnapshot(epoch, v), bigToHash(big.NewInt(stake)))
		writeStakedAmount(txn, v, big.NewInt(stake))
	}
	txn.Txn().SetBalance(state.FeePoolAddress, big.NewInt(1_000_000))

	require.NoError(t, FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn))

	for _, v := range vals {
		require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyValidatorReward(epoch, v))).Sign())
		require.Equal(t, int64(2_200_000), readStakedAmount(txn, v).Int64())
	}
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyRewardTotal(epoch))).Sign())
	require.Equal(t, int64(0), txn.Txn().GetBalance(state.FeePoolAddress).Int64())
}

func TestFinalizeEpoch_FiveValidators_MissedZeroRewardsIndependentOfSlots(t *testing.T) {
	v1 := types.StringToAddress("0x1901")
	v2 := types.StringToAddress("0x1902")
	v3 := types.StringToAddress("0x1903")
	v4 := types.StringToAddress("0x1904")
	v5 := types.StringToAddress("0x1905")
	vals := []types.Address{v1, v2, v3, v4, v5}
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v1), validators.NewECDSAValidator(v2), validators.NewECDSAValidator(v3), validators.NewECDSAValidator(v4), validators.NewECDSAValidator(v5))
	txn := newPosTestTransitionWithStakingConfig(t, set, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	epoch := uint64(2)
	requireSnapshotCreated(t, txn, epoch, set)

	stake := int64(2_000_000)
	slotsByVal := map[types.Address]uint64{v1: 1, v2: 2, v3: 5, v4: 10, v5: 20}
	for _, v := range vals {
		setUptimeCounters(txn, epoch, v, slotsByVal[v], 0)
		txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, v), bigToHash(big.NewInt(stake)))
		txn.Txn().SetState(PosSysAddr, keyStakerStakeSnapshot(epoch, v), bigToHash(big.NewInt(stake)))
		writeStakedAmount(txn, v, big.NewInt(stake))
	}
	header := makeFinalizeHeader(20, 10, set)
	txn.Txn().SetBalance(state.FeePoolAddress, big.NewInt(1_000_000))
	require.NoError(t, FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn))

	firstReward := bigFromHash(txn.Txn().GetState(PosSysAddr, keyValidatorReward(epoch, v1)))
	firstFinalStake := readStakedAmount(txn, v1)
	for _, v := range vals {
		require.Equal(t, uint64(0), u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerMissed(epoch, v))))
		require.Equal(t, firstReward.String(), bigFromHash(txn.Txn().GetState(PosSysAddr, keyValidatorReward(epoch, v))).String())
		require.Equal(t, firstFinalStake.String(), readStakedAmount(txn, v).String())
	}
}

func TestFinalizeEpoch_MissedSlotsCauseExplainedRewardDifference(t *testing.T) {
	v1 := types.StringToAddress("0x1a01")
	v2 := types.StringToAddress("0x1a02")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v1), validators.NewECDSAValidator(v2))
	txn := newPosTestTransitionWithStakingConfig(t, set, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	header := makeFinalizeHeader(20, 10, set)
	epoch := uint64(2)
	requireSnapshotCreated(t, txn, epoch, set)
	for _, v := range []types.Address{v1, v2} {
		txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, v), bigToHash(big.NewInt(2_000_000)))
		txn.Txn().SetState(PosSysAddr, keyStakerStakeSnapshot(epoch, v), bigToHash(big.NewInt(2_000_000)))
		writeStakedAmount(txn, v, big.NewInt(2_000_000))
	}
	setUptimeCounters(txn, epoch, v1, 10, 0)
	setUptimeCounters(txn, epoch, v2, 10, 2)
	txn.Txn().SetBalance(state.FeePoolAddress, big.NewInt(1_000_000))

	require.NoError(t, FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn))
	require.True(t, readStakedAmount(txn, v1).Cmp(readStakedAmount(txn, v2)) > 0)
	require.Equal(t, uint64(0), u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerSlots(epoch, v1))))
	require.Equal(t, uint64(0), u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerSlots(epoch, v2))))
	require.Equal(t, uint64(0), u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerMissed(epoch, v1))))
	require.Equal(t, uint64(0), u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerMissed(epoch, v2))))
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakeSnapshot(epoch, v1))).Sign())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakeSnapshot(epoch, v2))).Sign())
	require.Equal(t, int64(1), txn.Txn().GetBalance(state.FeePoolAddress).Int64())
}

func TestFinalizeEpoch_EmitsDeterministicPosSystemLogsAndClearsHistoryState(t *testing.T) {
	validatorA := types.StringToAddress("0x2721")
	validatorB := types.StringToAddress("0x2722")
	delegatorA := types.StringToAddress("0x2723")
	delegatorB := types.StringToAddress("0x2724")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validatorA), validators.NewECDSAValidator(validatorB))
	txn := newPosTestTransitionWithStakingConfig(t, set, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	header := makeFinalizeHeader(20, 10, set)
	epoch := uint64(2)
	requireSnapshotCreated(t, txn, epoch, set)

	writeValidatorPoolConfig(txn, validatorB, true, big.NewInt(10_000), big.NewInt(1), 1_000)
	writeStakerInfo(txn, validatorA, true, true, 1, 0, validatorA)
	writeStakerInfo(txn, validatorB, true, true, 1, 0, validatorB)
	setStakerForEpoch(t, txn, epoch, validatorA, 1, 1_000)
	setStakerForEpoch(t, txn, epoch, delegatorA, 1, 500)
	setStakerForEpoch(t, txn, epoch, validatorB, 1, 2_000)
	setStakerForEpoch(t, txn, epoch, delegatorB, 1, 1_000)
	appendValidatorStaker(txn, validatorA, validatorA)
	appendValidatorStaker(txn, validatorA, delegatorA)
	appendValidatorStaker(txn, validatorB, validatorB)
	appendValidatorStaker(txn, validatorB, delegatorB)
	writeValidatorDelegatedAggregateForRewardTest(txn, validatorA, big.NewInt(500), big.NewInt(500))
	writeValidatorDelegatedAggregateForRewardTest(txn, validatorB, big.NewInt(1_000), big.NewInt(1_000))
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, validatorA), bigToHash(big.NewInt(1_500)))
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, validatorB), bigToHash(big.NewInt(3_000)))
	setUptimeCounters(txn, epoch, validatorA, 1, 1)
	setUptimeCounters(txn, epoch, validatorB, 1, 0)
	txn.Txn().SetBalance(staking.AddrStakingContract, big.NewInt(4_500))
	txn.Txn().SetBalance(state.FeePoolAddress, big.NewInt(900))

	require.NoError(t, FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn))

	logs := txn.Txn().Logs()
	require.Len(t, logs, 7)
	for _, log := range logs {
		require.Equal(t, PosSysAddr, log.Address)
	}
	require.Equal(t, posEventEpochFinalizedTopic, logs[0].Topics[0])
	require.Equal(t, posEventValidatorUptimeFinalizedTopic, logs[1].Topics[0])
	require.Equal(t, posEventValidatorUptimeFinalizedTopic, logs[2].Topics[0])
	require.Equal(t, posEventStakerRewardedTopic, logs[3].Topics[0])
	require.Equal(t, posEventStakerRewardedTopic, logs[4].Topics[0])
	require.Equal(t, posEventStakerSlashedTopic, logs[5].Topics[0])
	require.Equal(t, posEventStakerSlashedTopic, logs[6].Topics[0])

	word := func(data []byte, idx int) *big.Int { return new(big.Int).SetBytes(data[idx*32 : (idx+1)*32]) }
	require.Equal(t, "900", word(logs[0].Data, 0).String()) // poolBalanceBefore
	require.Equal(t, "900", word(logs[0].Data, 1).String()) // totalDistributed
	require.Equal(t, "0", word(logs[0].Data, 2).String())   // poolRemainder
	require.Equal(t, "3", word(logs[0].Data, 3).String())   // totalSlashed
	require.Equal(t, "1", word(logs[0].Data, 4).String())   // activeValidators

	require.Equal(t, abiHashAddress(validatorA), logs[1].Topics[2])
	require.Equal(t, "1", word(logs[1].Data, 0).String())    // slots
	require.Equal(t, "1", word(logs[1].Data, 1).String())    // missed
	require.Equal(t, "0", word(logs[1].Data, 2).String())    // uptimeBps
	require.Equal(t, "0", word(logs[1].Data, 3).String())    // effectiveWeight
	require.Equal(t, "1500", word(logs[1].Data, 4).String()) // stakeSnapshot
	require.Equal(t, "0", word(logs[1].Data, 5).String())    // rewardEligible
	require.Equal(t, "1", word(logs[1].Data, 6).String())    // slashed

	require.Equal(t, abiHashAddress(validatorB), logs[2].Topics[2])
	require.Equal(t, "1", word(logs[2].Data, 0).String())     // slots
	require.Equal(t, "0", word(logs[2].Data, 1).String())     // missed
	require.Equal(t, "10000", word(logs[2].Data, 2).String()) // uptimeBps
	require.Equal(t, "3000", word(logs[2].Data, 3).String())  // effectiveWeight
	require.Equal(t, "3000", word(logs[2].Data, 4).String())  // stakeSnapshot
	require.Equal(t, "1", word(logs[2].Data, 5).String())     // rewardEligible
	require.Equal(t, "0", word(logs[2].Data, 6).String())     // slashed

	require.Equal(t, abiHashAddress(validatorB), logs[3].Topics[2])
	require.Equal(t, abiHashAddress(validatorB), logs[3].Topics[3])
	require.Equal(t, "1", word(logs[3].Data, 0).String())    // validator role
	require.Equal(t, "630", word(logs[3].Data, 1).String())  // amount
	require.Equal(t, "2000", word(logs[3].Data, 2).String()) // stakeSnapshot
	require.Equal(t, "2000", word(logs[3].Data, 3).String()) // stakeBefore
	require.Equal(t, "2630", word(logs[3].Data, 4).String()) // stakeAfter

	require.Equal(t, abiHashAddress(validatorB), logs[4].Topics[2])
	require.Equal(t, abiHashAddress(delegatorB), logs[4].Topics[3])
	require.Equal(t, "2", word(logs[4].Data, 0).String())    // delegator role
	require.Equal(t, "270", word(logs[4].Data, 1).String())  // amount
	require.Equal(t, "1000", word(logs[4].Data, 2).String()) // stakeSnapshot
	require.Equal(t, "1000", word(logs[4].Data, 3).String()) // stakeBefore
	require.Equal(t, "1270", word(logs[4].Data, 4).String()) // stakeAfter

	require.Equal(t, abiHashAddress(validatorA), logs[5].Topics[2])
	require.Equal(t, abiHashAddress(validatorA), logs[5].Topics[3])
	require.Equal(t, "1", word(logs[5].Data, 0).String())    // validator role
	require.Equal(t, "2", word(logs[5].Data, 1).String())    // slash amount
	require.Equal(t, "1000", word(logs[5].Data, 2).String()) // stakeSnapshot
	require.Equal(t, "1000", word(logs[5].Data, 3).String()) // stakeBefore
	require.Equal(t, "998", word(logs[5].Data, 4).String())  // stakeAfter
	require.Equal(t, "1", word(logs[5].Data, 5).String())    // slots
	require.Equal(t, "1", word(logs[5].Data, 6).String())    // missed
	require.Equal(t, "20", word(logs[5].Data, 7).String())   // slashBps
	require.Equal(t, abiWordAddress(chain.DefaultBurnedAddress), logs[5].Data[8*32:9*32])

	require.Equal(t, abiHashAddress(validatorA), logs[6].Topics[2])
	require.Equal(t, abiHashAddress(delegatorA), logs[6].Topics[3])
	require.Equal(t, "2", word(logs[6].Data, 0).String())   // delegator role
	require.Equal(t, "1", word(logs[6].Data, 1).String())   // slash amount
	require.Equal(t, "500", word(logs[6].Data, 2).String()) // stakeSnapshot
	require.Equal(t, "500", word(logs[6].Data, 3).String()) // stakeBefore
	require.Equal(t, "499", word(logs[6].Data, 4).String()) // stakeAfter
	require.Equal(t, "1", word(logs[6].Data, 5).String())   // slots
	require.Equal(t, "1", word(logs[6].Data, 6).String())   // missed
	require.Equal(t, "20", word(logs[6].Data, 7).String())  // slashBps
	require.Equal(t, abiWordAddress(chain.DefaultBurnedAddress), logs[6].Data[8*32:9*32])

	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyRewardTotal(epoch))).Sign())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyValidatorReward(epoch, validatorB))).Sign())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyDelegatorReward(epoch, validatorB, delegatorB))).Sign())
	require.Zero(t, u64FromHash(txn.Txn().GetState(PosSysAddr, keyEpochValidatorsLen(epoch))))
	require.Zero(t, u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerSlots(epoch, validatorB))))
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakeSnapshot(epoch, validatorA))).Sign())
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakerStakeSnapshot(epoch, delegatorB))).Sign())
}

func TestFinalizeEpoch_ValidatorUptimeFinalized_EmitsAllValidatorsWithoutRewardsOrSlash(t *testing.T) {
	v1 := types.StringToAddress("0x2a01")
	v2 := types.StringToAddress("0x2a02")
	v3 := types.StringToAddress("0x2a03")
	v4 := types.StringToAddress("0x2a04")
	set := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(v1),
		validators.NewECDSAValidator(v2),
		validators.NewECDSAValidator(v3),
		validators.NewECDSAValidator(v4),
	)
	txn := newPosTestTransitionWithStakingConfig(t, set, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	header := makeFinalizeHeader(20, 10, set)
	epoch := uint64(2)
	requireSnapshotCreated(t, txn, epoch, set)

	for _, v := range []types.Address{v1, v2, v3, v4} {
		writeStakerInfo(txn, v, true, true, 1, 0, v)
		writeStakedAmount(txn, v, big.NewInt(900))
	}
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, v1), bigToHash(big.NewInt(900)))
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, v2), bigToHash(big.NewInt(900)))
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, v3), bigToHash(big.NewInt(900)))
	setUptimeCounters(txn, epoch, v1, 10, 0)
	setUptimeCounters(txn, epoch, v2, 10, 2)
	setUptimeCounters(txn, epoch, v3, 10, 3)
	setUptimeCounters(txn, epoch, v4, 0, 0)
	txn.Txn().SetBalance(state.FeePoolAddress, big.NewInt(0))

	require.NoError(t, FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn))

	logs := txn.Txn().Logs()
	require.Len(t, logs, 5)
	require.Equal(t, posEventEpochFinalizedTopic, logs[0].Topics[0])
	for i, v := range []types.Address{v1, v2, v3, v4} {
		log := logs[i+1]
		require.Equal(t, PosSysAddr, log.Address)
		require.Equal(t, posEventValidatorUptimeFinalizedTopic, log.Topics[0])
		require.Equal(t, abiHashU64(epoch), log.Topics[1])
		require.Equal(t, abiHashAddress(v), log.Topics[2])
	}

	word := func(data []byte, idx int) *big.Int { return new(big.Int).SetBytes(data[idx*32 : (idx+1)*32]) }
	assertUptime := func(logIdx int, slots, missed, uptimeBps, effectiveWeight, stakeSnapshot uint64, rewardEligible, slashed bool) {
		t.Helper()
		log := logs[logIdx]
		require.Equal(t, new(big.Int).SetUint64(slots).String(), word(log.Data, 0).String())
		require.Equal(t, new(big.Int).SetUint64(missed).String(), word(log.Data, 1).String())
		require.Equal(t, new(big.Int).SetUint64(uptimeBps).String(), word(log.Data, 2).String())
		require.Equal(t, new(big.Int).SetUint64(effectiveWeight).String(), word(log.Data, 3).String())
		require.Equal(t, new(big.Int).SetUint64(stakeSnapshot).String(), word(log.Data, 4).String())
		if rewardEligible {
			require.Equal(t, "1", word(log.Data, 5).String())
		} else {
			require.Equal(t, "0", word(log.Data, 5).String())
		}
		if slashed {
			require.Equal(t, "1", word(log.Data, 6).String())
		} else {
			require.Equal(t, "0", word(log.Data, 6).String())
		}
	}
	assertUptime(1, 10, 0, 10_000, 900, 900, true, false)
	assertUptime(2, 10, 2, 8_000, 800, 900, true, false)
	assertUptime(3, 10, 3, 7_000, 0, 900, false, false)
	assertUptime(4, 0, 0, 0, 0, 0, false, false)

	for _, v := range []types.Address{v1, v2, v3, v4} {
		require.Zero(t, u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerSlots(epoch, v))))
		require.Zero(t, u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerMissed(epoch, v))))
		require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakeSnapshot(epoch, v))).Sign())
		require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keySlashed(epoch, v))).Sign())
		require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keySlashAmount(epoch, v))).Sign())
	}
	require.Zero(t, u64FromHash(txn.Txn().GetState(PosSysAddr, keyEpochValidatorsLen(epoch))))
}

func TestFinalizeEpoch_NonPayableDoesNotEmitPosSystemLogs(t *testing.T) {
	validator := types.StringToAddress("0x2821")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validator))
	txn := newPosTestTransitionWithStakingConfig(t, set, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	header := makeFinalizeHeader(20, 10, set)
	epoch := uint64(2)
	requireSnapshotCreated(t, txn, epoch, set)
	setStakerForEpoch(t, txn, epoch, validator, 1, 1_000)
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, validator), bigToHash(big.NewInt(1_000)))
	setUptimeCounters(txn, epoch, validator, 1, 0)
	txn.SetNonPayable(true)

	require.NoError(t, FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn))
	require.Empty(t, txn.Txn().Logs())
}

func TestFinalizeEpoch_ClearsZeroRewardDelegatorSnapshot(t *testing.T) {
	validator := types.StringToAddress("0x2921")
	delegator := types.StringToAddress("0x2922")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validator))
	txn := newPosTestTransitionWithStakingConfig(t, set, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	header := makeFinalizeHeader(20, 10, set)
	epoch := uint64(2)
	requireSnapshotCreated(t, txn, epoch, set)

	setStakerForEpoch(t, txn, epoch, validator, 1, 100)
	setStakerForEpoch(t, txn, epoch, delegator, 1, 50)
	appendValidatorStaker(txn, validator, delegator)
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, validator), bigToHash(big.NewInt(150)))
	setUptimeCounters(txn, epoch, validator, 1, 0)
	txn.Txn().SetBalance(state.FeePoolAddress, big.NewInt(0))

	require.NotZero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakerStakeSnapshot(epoch, delegator))).Sign())
	require.NoError(t, FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn))
	require.Zero(t, bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakerStakeSnapshot(epoch, delegator))).Sign())
}

func TestFinalizeEpochLogsPersistInSystemReceipt(t *testing.T) {
	validator := types.StringToAddress("0x17f1")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validator))
	txn := newPosTestTransitionWithStakingConfig(t, set, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})

	header := makeFinalizeHeader(20, 10, set)
	epoch := uint64(2)
	receiptTx := EpochFinalizationSystemTx(header.Number)

	requireSnapshotCreated(t, txn, epoch, set)
	setUptimeCounters(txn, epoch, validator, 10, 0)
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, validator), bigToHash(big.NewInt(100)))
	txn.Txn().SetState(PosSysAddr, keyStakerStakeSnapshot(epoch, validator), bigToHash(big.NewInt(100)))
	txn.Txn().SetBalance(state.FeePoolAddress, big.NewInt(10))

	require.NoError(t, FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn))
	require.NoError(t, txn.WriteSystemReceipt(receiptTx))
	require.Empty(t, txn.Txn().Logs(), "system logs must be drained into the receipt")

	receipts := txn.Receipts()
	require.Len(t, receipts, 1)
	require.Equal(t, receiptTx.Hash, receipts[0].TxHash)
	require.Equal(t, types.StateTx, receipts[0].TransactionType)
	require.NotEmpty(t, receipts[0].Logs)
	require.Equal(t, posEventEpochFinalizedTopic, receipts[0].Logs[0].Topics[0])

	var foundUptime bool
	for _, log := range receipts[0].Logs {
		require.Equal(t, PosSysAddr, log.Address)
		require.Equal(t, receiptTx.Hash, log.TxHash)
		require.Equal(t, uint64(0), log.BlockNumber)
		if log.Topics[0] == posEventValidatorUptimeFinalizedTopic {
			foundUptime = true
		}
	}
	require.True(t, foundUptime, "ValidatorUptimeFinalized log must be persisted in receipt logs")
	require.NotEqual(t, types.Bloom{}, receipts[0].LogsBloom)
	for _, log := range receipts[0].Logs {
		require.True(t, receipts[0].LogsBloom.IsLogInBloom(log), "receipt bloom must include PosSysAddr finalization logs")
	}
}
