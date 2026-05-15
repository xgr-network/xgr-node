package contract

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/helper/common"
	"github.com/xgr-network/xgr-node/helper/keccak"
	stakingHelper "github.com/xgr-network/xgr-node/helper/staking"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func TestFetchValidators(t *testing.T) {
	t.Parallel()

	res, err := FetchValidators(validators.ValidatorType("fake"), nil, types.ZeroAddress)
	assert.Nil(t, res)
	assert.ErrorContains(t, err, "unsupported validator type")
}

func TestFetchValidators_UsesTier1WhenTier1MeetsMinValidators(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(addr1),
		validators.NewECDSAValidator(addr2),
	)
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	setMetaStake(t, transition, addr1, mustBig("2000000000000000000000000"))
	setMetaStake(t, transition, addr2, mustBig("2000000000000000000000000"))
	setMetaActive(t, transition, addr2, false)

	res, err := FetchECDSAValidators(transition, types.ZeroAddress)
	assert.NoError(t, err)
	assert.Equal(t, validators.NewECDSAValidatorSet(validators.NewECDSAValidator(addr1)), res)
}

func TestFetchValidators_UsesTier2WhenTier1BelowMinValidators(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(addr1),
		validators.NewECDSAValidator(addr2),
	)
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 2, MaxValidatorCount: 10, EpochSize: 10})
	setMetaStake(t, transition, addr1, mustBig("2000000000000000000000000"))
	setMetaStake(t, transition, addr2, mustBig("2000000000000000000000000"))
	setMetaActive(t, transition, addr2, false)

	res, err := FetchECDSAValidators(transition, types.ZeroAddress)
	assert.NoError(t, err)
	assert.Equal(t, vals, res)
}

func TestFetchValidators_Tier1MaxValidatorsUsesStakeThenAddressOrdering(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(addr1),
		validators.NewECDSAValidator(addr2),
		validators.NewECDSAValidator(types.StringToAddress("3")),
	)
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	setMaxNumValidators(t, transition, 2)
	setMetaStake(t, transition, addr1, mustBig("2000000000000000000000000"))
	setMetaStake(t, transition, addr2, mustBig("3000000000000000000000000"))
	setMetaStake(t, transition, types.StringToAddress("3"), mustBig("3000000000000000000000000"))

	res, err := FetchECDSAValidators(transition, types.ZeroAddress)
	assert.NoError(t, err)
	assert.Equal(t, validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(addr2),
		validators.NewECDSAValidator(types.StringToAddress("3")),
	), res)
}

func TestFetchValidators_Tier1TieBreakDeterministicByAddressWhenStakeEqual(t *testing.T) {
	t.Parallel()

	lowAddr := addr1
	highAddr := addr2
	other := types.StringToAddress("3")

	vals := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(lowAddr),
		validators.NewECDSAValidator(highAddr),
		validators.NewECDSAValidator(other),
	)
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	setMaxNumValidators(t, transition, 1)

	// strict Tier1 preconditions: active + self >= 200k + effective total >= 2M
	selfStake := mustBig("3000000000000000000000000")
	for _, addr := range []types.Address{lowAddr, highAddr, other} {
		setMetaStake(t, transition, addr, selfStake)
	}
	setMetaStake(t, transition, other, mustBig("1000000000000000000000000"))

	expectedWinner := lowAddr
	for i := 0; i < 5; i++ {
		res, err := FetchECDSAValidators(transition, types.ZeroAddress)
		require.NoError(t, err)
		require.Equal(t, 1, res.Len())
		assert.Equal(t, expectedWinner, res.At(0).Addr(), "run=%d", i)
		assert.NotEqual(t, highAddr, res.At(0).Addr(), "run=%d", i)
		assert.NotEqual(t, other, res.At(0).Addr(), "run=%d", i)
	}
}

func TestFetchValidators_Tier1TieBreakIgnoresBelowThresholdCandidate(t *testing.T) {
	t.Parallel()

	lowAddr := addr1
	highAddr := addr2
	belowThreshold := types.StringToAddress("3")

	vals := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(lowAddr),
		validators.NewECDSAValidator(highAddr),
		validators.NewECDSAValidator(belowThreshold),
	)
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	setMaxNumValidators(t, transition, 2)

	selfStake := mustBig("200000000000000000000000")
	for _, addr := range []types.Address{lowAddr, highAddr, belowThreshold} {
		setMetaStake(t, transition, addr, selfStake)
	}
	lowDelegator := types.StringToAddress("301")
	highDelegator := types.StringToAddress("302")
	setStakerAsDelegator(t, transition, lowDelegator, lowAddr, mustBig("1800000000000000000000000"), 1, true)
	setStakerAsDelegator(t, transition, highDelegator, highAddr, mustBig("1800000000000000000000000"), 1, true)
	setValidatorStakers(t, transition, lowAddr, []types.Address{lowDelegator})
	setValidatorStakers(t, transition, highAddr, []types.Address{highDelegator})
	setValidatorStakers(t, transition, belowThreshold, nil)

	res, err := FetchECDSAValidators(transition, types.ZeroAddress)
	require.NoError(t, err)
	require.Equal(t, 2, res.Len())
	assert.Equal(t, lowAddr, res.At(0).Addr())
	assert.Equal(t, highAddr, res.At(1).Addr())
}

func TestFetchValidators_Tier2MaxValidatorsUsesCurrentDocumentedOrdering(t *testing.T) {
	t.Parallel()

	orderedFirst := addr2
	orderedSecond := addr1
	vals := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(orderedFirst),
		validators.NewECDSAValidator(orderedSecond),
	)
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 2, MaxValidatorCount: 10, EpochSize: 10})
	setMaxNumValidators(t, transition, 1)
	setMetaStake(t, transition, orderedFirst, mustBig("1000000000000000000"))
	setMetaStake(t, transition, orderedSecond, mustBig("9000000000000000000000000"))

	res, err := FetchECDSAValidators(transition, types.ZeroAddress)
	assert.NoError(t, err)
	assert.Equal(t, validators.NewECDSAValidatorSet(validators.NewECDSAValidator(orderedFirst)), res, "Tier 2 trimming keeps validator-array order")
}

func TestFetchValidators_UsesTier3WhenTier2BelowMinValidators(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(addr1),
		validators.NewECDSAValidator(addr2),
	)
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 2, MaxValidatorCount: 10, EpochSize: 10})
	setMetaStake(t, transition, addr1, mustBig("2000000000000000000000000"))
	setMetaStake(t, transition, addr2, mustBig("2000000000000000000000000"))
	setMetaActive(t, transition, addr1, false)
	setMetaActive(t, transition, addr2, false)

	res, err := FetchECDSAValidators(transition, types.ZeroAddress)
	assert.NoError(t, err)
	assert.Equal(t, vals, res)
}

func TestFetchValidators_Tier3MaxValidatorsUsesStakeThenAddressOrdering(t *testing.T) {
	t.Parallel()

	addr3 := types.StringToAddress("3")
	vals := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(addr1),
		validators.NewECDSAValidator(addr2),
		validators.NewECDSAValidator(addr3),
	)
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 3, MaxValidatorCount: 10, EpochSize: 10})
	setMaxNumValidators(t, transition, 2)
	setMetaStake(t, transition, addr1, mustBig("300"))
	setMetaStake(t, transition, addr2, mustBig("300"))
	setMetaStake(t, transition, addr3, mustBig("100"))
	setMetaActive(t, transition, addr1, false)
	setMetaActive(t, transition, addr2, false)
	setMetaActive(t, transition, addr3, false)

	res, err := FetchECDSAValidators(transition, types.ZeroAddress)
	assert.NoError(t, err)
	assert.Equal(t, validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(addr1),
		validators.NewECDSAValidator(addr2),
	), res)
}

func TestFetchBLSValidators_FilteredByActiveStakeAndBLS(t *testing.T) {
	t.Parallel()

	vals := validators.NewBLSValidatorSet(
		validators.NewBLSValidator(addr1, testBLSPubKey1),
		validators.NewBLSValidator(addr2, testBLSPubKey2),
	)
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	setMetaStake(t, transition, addr1, mustBig("2000000000000000000000000"))
	setMetaStake(t, transition, addr2, mustBig("2000000000000000000000000"))
	setMetaActive(t, transition, addr2, false)

	res, err := FetchBLSValidators(transition, types.ZeroAddress)
	assert.NoError(t, err)
	assert.Equal(t, validators.NewBLSValidatorSet(validators.NewBLSValidator(addr1, testBLSPubKey1)), res)
}

func TestFetchBLSValidators_FallbackUsesStakePositiveAndValidBLSWhenActiveSetBelowMin(t *testing.T) {
	t.Parallel()

	vals := validators.NewBLSValidatorSet(
		validators.NewBLSValidator(addr1, testBLSPubKey1),
		validators.NewBLSValidator(addr2, testBLSPubKey2),
	)
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 2, MaxValidatorCount: 10, EpochSize: 10})
	setMetaStake(t, transition, addr1, mustBig("2000000000000000000000000"))
	setMetaStake(t, transition, addr2, mustBig("2000000000000000000000000"))
	setMetaActive(t, transition, addr2, false)

	res, err := FetchBLSValidators(transition, types.ZeroAddress)
	assert.NoError(t, err)
	assert.Equal(t, vals, res)
}

func TestFetchBLSValidators_FallbackUsesOnlyValidBLSWhenFilteredBelowMinAndInvalidBLSPresent(t *testing.T) {
	t.Parallel()

	vals := validators.NewBLSValidatorSet(
		validators.NewBLSValidator(addr1, testBLSPubKey1),
		validators.NewBLSValidator(addr2, []byte{0x01}),
	)
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 5, MaxValidatorCount: 10, EpochSize: 10})
	setMetaStake(t, transition, addr1, mustBig("200"))
	setMetaStake(t, transition, addr2, mustBig("300"))
	setMetaActive(t, transition, addr1, false)
	setMetaActive(t, transition, addr2, false)

	res, err := FetchBLSValidators(transition, types.ZeroAddress)
	assert.NoError(t, err)
	assert.Equal(t, validators.NewBLSValidatorSet(validators.NewBLSValidator(addr1, testBLSPubKey1)), res)
}

func TestFetchBLSValidators_TrimmedByMaxValidatorCount(t *testing.T) {
	t.Parallel()

	vals := validators.NewBLSValidatorSet(
		validators.NewBLSValidator(addr1, testBLSPubKey1),
		validators.NewBLSValidator(addr2, testBLSPubKey2),
	)
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	setMaxNumValidators(t, transition, 1)
	setMetaStake(t, transition, addr1, mustBig("2000000000000000000000000"))
	setMetaStake(t, transition, addr2, mustBig("3000000000000000000000000"))

	res, err := FetchBLSValidators(transition, types.ZeroAddress)
	assert.NoError(t, err)
	assert.Equal(t, validators.NewBLSValidatorSet(validators.NewBLSValidator(addr2, testBLSPubKey2)), res)
}

func TestIsEmergencyModeActive_TrueWhenTier1BelowMinimum(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(addr1),
		validators.NewECDSAValidator(addr2),
	)
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 2, MaxValidatorCount: 10, EpochSize: 10})
	setMetaStake(t, transition, addr1, mustBig("2000000000000000000000000"))
	setMetaStake(t, transition, addr2, mustBig("1000000000000000000000000"))

	assert.True(t, IsEmergencyModeActive(transition))
}

func TestIsEmergencyModeActive_FalseWhenTier1MeetsMinimum(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(addr1),
		validators.NewECDSAValidator(addr2),
	)
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 2, MaxValidatorCount: 10, EpochSize: 10})
	setMetaStake(t, transition, addr1, mustBig("2000000000000000000000000"))
	setMetaStake(t, transition, addr2, mustBig("2000000000000000000000000"))

	assert.False(t, IsEmergencyModeActive(transition))
}

func TestFetchValidators_UsesSelfPlusActiveDelegatedStakeForThreshold(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(addr1))
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 1, MaxValidatorCount: 10, EpochSize: 10})
	setMetaStake(t, transition, addr1, mustBig("200000000000000000000000")) // 200k self
	setDelegatedActiveStake(t, transition, addr1, mustBig("1800000000000000000000000"))

	res, err := FetchECDSAValidators(transition, types.ZeroAddress)
	assert.NoError(t, err)
	assert.Equal(t, vals, res)
}

func TestFetchValidators_RequiresEffectiveTotalSupportAtLeastThreshold(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(addr1))
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	setMetaStake(t, transition, addr1, mustBig("200000000000000000000000")) // 200k self
	setDelegatedActiveStake(t, transition, addr1, mustBig("1799999999999999999999999"))

	res, err := FetchECDSAValidators(transition, types.ZeroAddress)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Len())
}

func TestFetchValidators_RequiresSelfValidatorPosition(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(addr1))
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	setMetaStake(t, transition, addr1, mustBig("2000000000000000000000000"))

	// Simulate non-self staker by changing validator pointer at _staker[addr1].validator (base + 4).
	transition.Txn().SetState(staking.AddrStakingContract, stakerSlotWithOffsetTest(addr1, 4), types.BytesToHash(addr2.Bytes()))

	res, err := FetchECDSAValidators(transition, types.ZeroAddress)
	assert.NoError(t, err)
	assert.Equal(t, 0, res.Len())
}

func TestFetchValidators_DelegatorNeverBecomesValidatorCandidate(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(addr1))
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	setMetaStake(t, transition, addr1, mustBig("200000000000000000000000"))

	delegator := types.StringToAddress("0x335")
	setStakerAsDelegator(t, transition, delegator, addr1, mustBig("5000000000000000000000000"), 1, true)
	setValidatorStakers(t, transition, addr1, []types.Address{addr1, delegator})

	// Simulate accidental listing in validators() storage - fetcher must still filter by self-position.
	setValidatorUniverse(t, transition, []types.Address{addr1, delegator})

	res, err := FetchECDSAValidators(transition, types.ZeroAddress)
	require.NoError(t, err)
	require.Equal(t, 1, res.Len())
	assert.Equal(t, addr1, res.At(0).Addr())
}

func TestFetchValidators_RequiresValidatorMinSelfStakeEvenWithHighDelegation(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(addr1))
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})
	setMetaStake(t, transition, addr1, mustBig("199999999999999999999999")) // just below 200k

	delegator := types.StringToAddress("0x336")
	setStakerAsDelegator(t, transition, delegator, addr1, mustBig("5000000000000000000000000"), 1, true)
	setValidatorStakers(t, transition, addr1, []types.Address{delegator})

	res, err := FetchECDSAValidators(transition, types.ZeroAddress)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Len())
}

func TestReadValidatorEffectiveTotalStakeAt_NewValidatorCountsFromNextEpoch(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(addr1))
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})

	setMetaStake(t, transition, addr1, mustBig("2000000000000000000000000"))
	setStakerJoinedAt(t, transition, addr1, 19)
	setStakerValidator(t, transition, addr1, addr1)

	sameEpoch := ReadValidatorEffectiveTotalStakeAt(transition, addr1, 19)
	nextEpoch := ReadValidatorEffectiveTotalStakeAt(transition, addr1, 20)

	assert.Equal(t, 0, sameEpoch.Sign())
	assert.Equal(t, mustBig("2000000000000000000000000"), nextEpoch)
}

func TestReadValidatorEffectiveTotalStakeAt_NewDelegatorCountsFromNextEpoch(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(addr1))
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})

	delegator := types.StringToAddress("0x333")
	setStakerAsDelegator(t, transition, delegator, addr1, mustBig("10000000000000000000000"), 19, true) // 10k at block 19
	setValidatorStakers(t, transition, addr1, []types.Address{delegator})

	// same epoch as join (epoch 1) => not effective
	sameEpoch := ReadValidatorEffectiveTotalStakeAt(transition, addr1, 19)
	// next epoch => effective
	nextEpoch := ReadValidatorEffectiveTotalStakeAt(transition, addr1, 20)

	assert.Equal(t, mustBig("10"), sameEpoch) // bootstrap self stake
	assert.Equal(t, mustBig("10000000000000000000010"), nextEpoch)
}

func TestReadValidatorEffectiveTotalStakeAt_DeactivationEffectiveAtEpochBoundary(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(addr1))
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})

	delegator := types.StringToAddress("0x334")
	setStakerAsDelegator(t, transition, delegator, addr1, mustBig("10000000000000000000000"), 9, true)
	setValidatorStakers(t, transition, addr1, []types.Address{delegator})

	// deactivate in epoch 2 -> still counted in epoch 2, excluded from epoch 3
	setStakerDeactivatedAt(t, transition, delegator, 21)
	beforeBoundary := ReadValidatorEffectiveTotalStakeAt(transition, addr1, 29)
	afterBoundary := ReadValidatorEffectiveTotalStakeAt(transition, addr1, 30)

	assert.Equal(t, mustBig("10000000000000000000010"), beforeBoundary)
	assert.Equal(t, mustBig("10"), afterBoundary)
}

func TestReadValidatorEffectiveTotalStakeAt_DeactivationInsideEpochRemainsEffectiveUntilNextEpoch(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(addr1))
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})

	delegator := types.StringToAddress("0x335")
	setStakerAsDelegator(t, transition, delegator, addr1, mustBig("10000000000000000000000"), 8, true)
	setValidatorStakers(t, transition, addr1, []types.Address{delegator})
	setStakerDeactivatedAt(t, transition, delegator, 19)

	// Deactivation at block 19 (epoch 1) remains epoch-effective through block 19.
	assert.Equal(t, mustBig("10000000000000000000010"), ReadValidatorEffectiveTotalStakeAt(transition, addr1, 19))
	// From epoch 2 onward this delegator is no longer epoch-effective.
	assert.Equal(t, mustBig("10"), ReadValidatorEffectiveTotalStakeAt(transition, addr1, 20))
	assert.Equal(t, mustBig("10"), ReadValidatorEffectiveTotalStakeAt(transition, addr1, 29))
}

func TestReadValidatorVotingStakeAt_DeactivatedValidatorStillCountsStake(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(addr1))
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})

	setMetaStake(t, transition, addr1, mustBig("10"))
	setStakerValidator(t, transition, addr1, addr1)
	setStakerJoinedAt(t, transition, addr1, 1)
	setMetaActive(t, transition, addr1, false)
	setStakerDeactivatedAt(t, transition, addr1, 21)

	effective := ReadValidatorEffectiveTotalStakeAt(transition, addr1, 30)
	voting := ReadValidatorVotingStakeAt(transition, addr1, 30)

	assert.Equal(t, 0, effective.Sign())
	assert.Equal(t, mustBig("10"), voting)
}

func TestReadValidatorVotingStakeAt_DeactivatedValidatorWithActiveDelegator(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(addr1))
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})

	delegator := types.StringToAddress("0x334")
	setStakerAsDelegator(t, transition, delegator, addr1, mustBig("100"), 9, true)
	setValidatorStakers(t, transition, addr1, []types.Address{delegator})

	setMetaStake(t, transition, addr1, mustBig("10"))
	setStakerValidator(t, transition, addr1, addr1)
	setStakerJoinedAt(t, transition, addr1, 1)
	setMetaActive(t, transition, addr1, false)
	setStakerDeactivatedAt(t, transition, addr1, 21)

	assert.Equal(t, mustBig("110"), ReadValidatorVotingStakeAt(transition, addr1, 30))
}

func TestReadValidatorVotingStakeAt_DeactivatedValidatorWithDeactivatedDelegator(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(addr1))
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})

	delegator := types.StringToAddress("0x334")
	setStakerAsDelegator(t, transition, delegator, addr1, mustBig("100"), 9, true)
	setStakerDeactivatedAt(t, transition, delegator, 21)
	setValidatorStakers(t, transition, addr1, []types.Address{delegator})

	setMetaStake(t, transition, addr1, mustBig("10"))
	setStakerValidator(t, transition, addr1, addr1)
	setStakerJoinedAt(t, transition, addr1, 1)
	setMetaActive(t, transition, addr1, false)
	setStakerDeactivatedAt(t, transition, addr1, 21)

	assert.Equal(t, mustBig("10"), ReadValidatorVotingStakeAt(transition, addr1, 30))
}

func TestReadValidatorVotingStakeAt_RawSelfStakeCountsWithoutJoinMaturity(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(addr1))
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})

	setMetaStake(t, transition, addr1, mustBig("10"))
	setStakerValidator(t, transition, addr1, addr1)
	setStakerJoinedAt(t, transition, addr1, 25)
	setMetaActive(t, transition, addr1, true)

	assert.Equal(t, 0, ReadValidatorEffectiveTotalStakeAt(transition, addr1, 29).Sign())
	assert.Equal(t, mustBig("10"), ReadValidatorVotingStakeAt(transition, addr1, 29))
}

func TestReadValidatorVotingStakeAt_MissingSelfMappingOrRawStakeReturnsZero(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(addr1))
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: 10, EpochSize: 10})

	setMetaStake(t, transition, addr1, mustBig("0"))
	setStakerValidator(t, transition, addr1, types.ZeroAddress)

	assert.Equal(t, 0, ReadValidatorVotingStakeAt(transition, addr1, 30).Sign())
}

func newTransitionWithParams(t *testing.T, vals validators.Validators, params stakingHelper.PredeployParams) *state.Transition {
	t.Helper()
	transition := newTestTransition(t)

	contractState, err := stakingHelper.PredeployStakingSC(vals, params)
	assert.NoError(t, err)
	assert.NoError(t, transition.SetAccountDirectly(staking.AddrStakingContract, contractState))
	transition.ContextPtr().Number = int64(params.EpochSize * 2)

	return transition
}

func setMetaStake(t *testing.T, transition *state.Transition, addr types.Address, stake *big.Int) {
	t.Helper()
	key := stakerSlotWithOffsetTest(addr, 1)
	transition.Txn().SetState(staking.AddrStakingContract, key, types.BytesToHash(stake.Bytes()))
}

func setMetaActive(t *testing.T, transition *state.Transition, addr types.Address, active bool) {
	t.Helper()
	key := stakerSlotWithOffsetTest(addr, 0)
	cur := transition.Txn().GetState(staking.AddrStakingContract, key)
	v := new(big.Int).SetBytes(cur[:])
	if active {
		v.Or(v, new(big.Int).Lsh(big.NewInt(1), 8))
	} else {
		mask := new(big.Int).Lsh(big.NewInt(1), 8)
		mask.Not(mask)
		v.And(v, mask)
	}
	transition.Txn().SetState(staking.AddrStakingContract, key, types.BytesToHash(v.Bytes()))
}

func setMaxNumValidators(t *testing.T, transition *state.Transition, max uint64) {
	t.Helper()

	key := types.BytesToHash(big.NewInt(1).Bytes())
	raw := transition.Txn().GetState(staking.AddrStakingContract, key)
	v := new(big.Int).SetBytes(raw[:])

	// clear max (bits [128..191])
	mask64 := new(big.Int).SetUint64(^uint64(0))
	clearMask := new(big.Int).Lsh(mask64, 128)
	clearMask.Not(clearMask)
	v.And(v, clearMask)

	maxShifted := new(big.Int).Lsh(new(big.Int).SetUint64(max), 128)
	v.Or(v, maxShifted)

	transition.Txn().SetState(staking.AddrStakingContract, key, types.BytesToHash(v.Bytes()))
}

func stakerSlotWithOffsetTest(addr types.Address, offset int64) types.Hash {
	base := keccak.Keccak256(nil,
		append(common.PadLeftOrTrim(addr.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(3).Bytes(), 32)...),
	)
	idx := new(big.Int).SetBytes(base)
	idx.Add(idx, big.NewInt(offset))
	return types.BytesToHash(idx.Bytes())
}

func setDelegatedActiveStake(t *testing.T, transition *state.Transition, validator types.Address, amount *big.Int) {
	t.Helper()
	base := keccak.Keccak256(nil,
		append(common.PadLeftOrTrim(validator.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(9).Bytes(), 32)...),
	)
	transition.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(base), types.BytesToHash(amount.Bytes()))
}

func setStakerAsDelegator(
	t *testing.T,
	transition *state.Transition,
	delegator types.Address,
	validator types.Address,
	amount *big.Int,
	joinedAt uint64,
	active bool,
) {
	t.Helper()

	setMetaStake(t, transition, delegator, amount)
	setMetaActive(t, transition, delegator, active)
	setStakerValidator(t, transition, delegator, validator)
	setStakerJoinedAt(t, transition, delegator, joinedAt)
}

func setStakerValidator(t *testing.T, transition *state.Transition, addr, validator types.Address) {
	t.Helper()
	transition.Txn().SetState(staking.AddrStakingContract, stakerSlotWithOffsetTest(addr, 4), types.BytesToHash(validator.Bytes()))
}

func setStakerJoinedAt(t *testing.T, transition *state.Transition, addr types.Address, joinedAt uint64) {
	t.Helper()
	transition.Txn().SetState(staking.AddrStakingContract, stakerSlotWithOffsetTest(addr, 2), types.BytesToHash(new(big.Int).SetUint64(joinedAt).Bytes()))
}

func setStakerDeactivatedAt(t *testing.T, transition *state.Transition, addr types.Address, deactivatedAt uint64) {
	t.Helper()
	transition.Txn().SetState(staking.AddrStakingContract, stakerSlotWithOffsetTest(addr, 3), types.BytesToHash(new(big.Int).SetUint64(deactivatedAt).Bytes()))
}

func setValidatorStakers(t *testing.T, transition *state.Transition, validator types.Address, stakers []types.Address) {
	t.Helper()

	base := keccak.Keccak256(nil,
		append(common.PadLeftOrTrim(validator.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(stakersByValidatorSlot).Bytes(), 32)...),
	)
	transition.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(base), types.BytesToHash(new(big.Int).SetUint64(uint64(len(stakers))).Bytes()))

	dataBase := keccak.Keccak256(nil, common.PadLeftOrTrim(base, 32))
	idx := new(big.Int).SetBytes(dataBase)
	for i, staker := range stakers {
		slot := new(big.Int).Add(idx, new(big.Int).SetUint64(uint64(i)))
		transition.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(slot.Bytes()), types.BytesToHash(staker.Bytes()))
	}
}

func setValidatorUniverse(t *testing.T, transition *state.Transition, vals []types.Address) {
	t.Helper()

	lenSlot := types.BytesToHash(big.NewInt(validatorsSlot).Bytes())
	transition.Txn().SetState(staking.AddrStakingContract, lenSlot, types.BytesToHash(new(big.Int).SetUint64(uint64(len(vals))).Bytes()))

	base := keccak.Keccak256(nil, common.PadLeftOrTrim(big.NewInt(validatorsSlot).Bytes(), 32))
	idx := new(big.Int).SetBytes(base)
	for i, v := range vals {
		slot := new(big.Int).Add(idx, new(big.Int).SetUint64(uint64(i)))
		transition.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(slot.Bytes()), types.BytesToHash(v.Bytes()))
	}
}

func mustBig(v string) *big.Int {
	b, ok := new(big.Int).SetString(v, 10)
	if !ok {
		panic("invalid big int")
	}
	return b
}

func TestFetchECDSAValidators_EmergencyFallbackDeterministicRegression(t *testing.T) {
	t.Parallel()

	addr3 := types.StringToAddress("3")
	vals := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(addr1),
		validators.NewECDSAValidator(addr2),
		validators.NewECDSAValidator(addr3),
	)

	t.Run("strict eligible below min falls back to active validators when active count meets min", func(t *testing.T) {
		transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 2, MaxValidatorCount: 10, EpochSize: 10})
		setMetaStake(t, transition, addr1, mustBig("2000000000000000000000000"))
		setMetaStake(t, transition, addr2, mustBig("1"))
		setMetaStake(t, transition, addr3, mustBig("1"))
		setMetaActive(t, transition, addr3, false)

		res, err := FetchECDSAValidators(transition, types.ZeroAddress)
		require.NoError(t, err)
		require.Equal(t, validators.NewECDSAValidatorSet(
			validators.NewECDSAValidator(addr1),
			validators.NewECDSAValidator(addr2),
		), res)
	})

	t.Run("active count below min falls back to weighted stake with address tie break and max trim", func(t *testing.T) {
		transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 3, MaxValidatorCount: 10, EpochSize: 10})
		setMaxNumValidators(t, transition, 2)
		setMetaStake(t, transition, addr1, mustBig("100"))
		setMetaStake(t, transition, addr2, mustBig("300"))
		setMetaStake(t, transition, addr3, mustBig("300"))
		setMetaActive(t, transition, addr1, false)
		setMetaActive(t, transition, addr2, false)
		setMetaActive(t, transition, addr3, false)

		for i := 0; i < 5; i++ {
			res, err := FetchECDSAValidators(transition, types.ZeroAddress)
			require.NoError(t, err)
			require.Equal(t, validators.NewECDSAValidatorSet(
				validators.NewECDSAValidator(addr2),
				validators.NewECDSAValidator(addr3),
			), res, "run=%d", i)
		}
	})
}

func TestFetchBLSValidators_EmergencyFallbackDeterministicAndInvalidKeys(t *testing.T) {
	t.Parallel()

	addr3 := types.StringToAddress("3")
	invalidBLS := []byte{0x01, 0x02, 0x03}

	t.Run("invalid active staked BLS key is skipped without panic and active fallback stays deterministic", func(t *testing.T) {
		vals := validators.NewBLSValidatorSet(
			validators.NewBLSValidator(addr1, testBLSPubKey1),
			validators.NewBLSValidator(addr2, invalidBLS),
			validators.NewBLSValidator(addr3, testBLSPubKey2),
		)
		transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 2, MaxValidatorCount: 10, EpochSize: 10})
		setMetaStake(t, transition, addr1, mustBig("2000000000000000000000000"))
		setMetaStake(t, transition, addr2, mustBig("9000000000000000000000000"))
		setMetaStake(t, transition, addr3, mustBig("1"))

		require.NotPanics(t, func() {
			res, err := FetchBLSValidators(transition, types.ZeroAddress)
			require.NoError(t, err)
			require.Equal(t, validators.NewBLSValidatorSet(
				validators.NewBLSValidator(addr1, testBLSPubKey1),
				validators.NewBLSValidator(addr3, testBLSPubKey2),
			), res)
		})
	})

	t.Run("weighted emergency fallback ignores invalid key and trims equal stake by address", func(t *testing.T) {
		vals := validators.NewBLSValidatorSet(
			validators.NewBLSValidator(addr1, testBLSPubKey1),
			validators.NewBLSValidator(addr2, invalidBLS),
			validators.NewBLSValidator(addr3, testBLSPubKey2),
		)
		transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 3, MaxValidatorCount: 10, EpochSize: 10})
		setMaxNumValidators(t, transition, 1)
		setMetaStake(t, transition, addr1, mustBig("300"))
		setMetaStake(t, transition, addr2, mustBig("1000000"))
		setMetaStake(t, transition, addr3, mustBig("300"))
		setMetaActive(t, transition, addr1, false)
		setMetaActive(t, transition, addr2, false)
		setMetaActive(t, transition, addr3, false)

		for i := 0; i < 5; i++ {
			res, err := FetchBLSValidators(transition, types.ZeroAddress)
			require.NoError(t, err)
			require.Equal(t, validators.NewBLSValidatorSet(validators.NewBLSValidator(addr1, testBLSPubKey1)), res, "run=%d", i)
		}
	})
}

func TestFetchValidators_RemoveValidatorSwapPopOrderAndTrimRemainDeterministic(t *testing.T) {
	t.Parallel()

	addr3 := types.StringToAddress("3")
	addr4 := types.StringToAddress("4")
	vals := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(addr1),
		validators.NewECDSAValidator(addr2),
		validators.NewECDSAValidator(addr3),
		validators.NewECDSAValidator(addr4),
	)
	transition := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 4, MaxValidatorCount: 10, EpochSize: 10})
	setMetaStake(t, transition, addr1, mustBig("4000000000000000000000000"))
	setMetaStake(t, transition, addr2, mustBig("2000000000000000000000000"))
	setMetaStake(t, transition, addr3, mustBig("2000000000000000000000000"))
	setMetaStake(t, transition, addr4, mustBig("3000000000000000000000000"))

	removeValidatorSwapPopForTest(t, transition, addr2)
	setMaxNumValidators(t, transition, 2)

	res, err := FetchECDSAValidators(transition, types.ZeroAddress)
	require.NoError(t, err)
	require.Equal(t, validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(addr1),
		validators.NewECDSAValidator(addr4),
	), res)
	require.Equal(t, addr4, readValidators(transition)[1], "middle removal must swap-pop the previous tail into the removed slot")
	require.Equal(t, []types.Address{addr1, addr4}, []types.Address{res.At(0).Addr(), res.At(1).Addr()}, "proposer calculation inputs must remain stable after removal and max trim")
}

func removeValidatorSwapPopForTest(t *testing.T, transition *state.Transition, removed types.Address) {
	t.Helper()

	vals := readValidators(transition)
	idx := -1
	for i, addr := range vals {
		if addr == removed {
			idx = i
			break
		}
	}
	require.NotEqual(t, -1, idx)
	last := len(vals) - 1
	if idx != last {
		vals[idx] = vals[last]
	}
	vals = vals[:last]
	setValidatorUniverse(t, transition, vals)
}
