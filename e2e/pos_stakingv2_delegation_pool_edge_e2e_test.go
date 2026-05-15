package e2e

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/xgr-network/xgr-node/contracts/abis"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/e2e/framework"
	"github.com/xgr-network/xgr-node/helper/common"
	"github.com/xgr-network/xgr-node/helper/keccak"
	"github.com/xgr-network/xgr-node/types"
)

func TestPoS_StakingV2_MinDelegatorStake_UpdateExistingDelegators(t *testing.T) {
	const epochSize = uint64(5)

	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, epochSize, 1, 4)

	validator := addrs[0]
	validatorKey := keys[0]
	knownStakers := append([]types.Address(nil), addrs...)

	minSelfStake, err := queryValidatorMinSelfStake(servers[0], validator)
	require.NoError(t, err)
	validatorInfo, err := queryStakerInfo(servers[0], validator, validator)
	require.NoError(t, err)
	require.True(t, validatorInfo.Exists)
	if validatorInfo.Amount.Cmp(minSelfStake) < 0 {
		receipt, stakeErr := sendStakeTx(servers[0], validator, validatorKey, new(big.Int).Sub(minSelfStake, validatorInfo.Amount))
		require.NoError(t, stakeErr)
		require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)
	}

	delegatorMinStake, err := queryDelegatorMinStake(servers[0], validator)
	require.NoError(t, err)

	maxDelegatedStake := new(big.Int).Mul(delegatorMinStake, big.NewInt(100))
	require.NoError(t, sendSetValidatorPoolConfigTxWithMinDelegatorStake(servers[0], validator, validatorKey, true, maxDelegatedStake, big.NewInt(0), 0))
	assertStakingConservation(t, servers[0], validator, knownStakers)

	delegatorAKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegatorA := crypto.PubKeyToAddress(&delegatorAKey.PublicKey)
	knownStakers = append(knownStakers, delegatorA)
	delegationA := new(big.Int).Mul(delegatorMinStake, big.NewInt(2))
	fundAccount(t, servers[0], validator, validatorKey, delegatorA, new(big.Int).Mul(delegatorMinStake, big.NewInt(12)))

	waitUntilMidEpoch(t, servers[0], epochSize)
	receiptA, err := sendDelegateTx(servers[0], delegatorA, delegatorAKey, validator, delegationA)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptA.Status)
	waitForNextEpochTransition(t, servers, epochSize)

	delegatorAInfo, err := queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	require.True(t, delegatorAInfo.Exists)
	require.True(t, delegatorAInfo.Active)
	require.Equal(t, 0, delegatorAInfo.Amount.Cmp(delegationA))
	activeAOnly, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, activeAOnly.Cmp(delegatorAInfo.Amount))
	rawAOnly, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, rawAOnly.Cmp(delegatorAInfo.Amount))
	assertStakingConservation(t, servers[0], validator, knownStakers)

	customMin := new(big.Int).Mul(delegatorMinStake, big.NewInt(3))
	require.NoError(t, sendSetValidatorPoolConfigTxWithMinDelegatorStake(servers[0], validator, validatorKey, true, maxDelegatedStake, customMin, 0))

	delegatorAInfoAfterMinRaise, err := queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	require.True(t, delegatorAInfoAfterMinRaise.Exists)
	require.True(t, delegatorAInfoAfterMinRaise.Active)
	require.Equal(t, 0, delegatorAInfoAfterMinRaise.Amount.Cmp(delegationA))
	activeAfterMinRaise, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, activeAfterMinRaise.Cmp(delegationA), "raising minDelegatorStake must not forcibly remove an already-active delegator")
	rawAfterMinRaise, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, rawAfterMinRaise.Cmp(delegationA))
	assertStakingConservation(t, servers[0], validator, knownStakers)

	delegatorBKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegatorB := crypto.PubKeyToAddress(&delegatorBKey.PublicKey)
	knownStakers = append(knownStakers, delegatorB)
	fundAccount(t, servers[0], validator, validatorKey, delegatorB, new(big.Int).Mul(delegatorMinStake, big.NewInt(12)))
	belowCustomMin := new(big.Int).Set(delegationA)

	activeBeforeRejectedB, rawBeforeRejectedB, amountABeforeRejectedB := minDelegatorStakeSnapshot(t, servers[0], validator, delegatorA)
	receiptB, err := sendDelegateTx(servers[0], delegatorB, delegatorBKey, validator, belowCustomMin)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receiptB.Status)
	delegatorBInfo, err := queryStakerInfo(servers[0], validator, delegatorB)
	require.NoError(t, err)
	require.False(t, delegatorBInfo.Exists)
	minDelegatorStakeRequireSnapshotUnchanged(t, servers[0], validator, delegatorA, activeBeforeRejectedB, rawBeforeRejectedB, amountABeforeRejectedB, "below-custom-min new delegation must not mutate staking state")
	assertStakingConservation(t, servers[0], validator, knownStakers)

	receiptB, err = sendDelegateTx(servers[0], delegatorB, delegatorBKey, validator, customMin)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptB.Status)
	delegatorBInfo, err = queryStakerInfo(servers[0], validator, delegatorB)
	require.NoError(t, err)
	require.True(t, delegatorBInfo.Exists)
	require.True(t, delegatorBInfo.Active)
	require.Equal(t, 0, delegatorBInfo.Amount.Cmp(customMin))
	delegatorAInfo, err = queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	rawAfterB, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, rawAfterB.Cmp(new(big.Int).Add(delegatorAInfo.Amount, delegatorBInfo.Amount)))
	assertStakingConservation(t, servers[0], validator, knownStakers)

	receiptDeactivateA, err := sendSetDelegationActiveReceiptTx(servers[0], delegatorA, delegatorAKey, validator, false)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptDeactivateA.Status)
	waitForNextEpochTransition(t, servers, epochSize)

	delegatorAInfoInactive, err := queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	require.True(t, delegatorAInfoInactive.Exists)
	require.False(t, delegatorAInfoInactive.Active)
	require.GreaterOrEqual(t, delegatorAInfoInactive.Amount.Cmp(delegationA), 0)
	require.Less(t, delegatorAInfoInactive.Amount.Cmp(customMin), 0)
	delegatorBInfo, err = queryStakerInfo(servers[0], validator, delegatorB)
	require.NoError(t, err)
	require.True(t, delegatorBInfo.Active)
	activeAfterAInactive, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, activeAfterAInactive.Cmp(delegatorBInfo.Amount), "inactive delegatorA must no longer contribute active delegated stake")
	rawAfterAInactive, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, rawAfterAInactive.Cmp(new(big.Int).Add(delegatorAInfoInactive.Amount, delegatorBInfo.Amount)), "raw delegated stake must still include inactive delegatorA")
	assertStakingConservation(t, servers[0], validator, knownStakers)

	activeBeforeRejectedReactivation, rawBeforeRejectedReactivation, amountABeforeRejectedReactivation := minDelegatorStakeSnapshot(t, servers[0], validator, delegatorA)
	receiptReactivateA, err := sendSetDelegationActiveReceiptTx(servers[0], delegatorA, delegatorAKey, validator, true)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receiptReactivateA.Status)
	delegatorAInfoAfterRejectedReactivation, err := queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	require.True(t, delegatorAInfoAfterRejectedReactivation.Exists)
	require.False(t, delegatorAInfoAfterRejectedReactivation.Active)
	minDelegatorStakeRequireSnapshotUnchanged(t, servers[0], validator, delegatorA, activeBeforeRejectedReactivation, rawBeforeRejectedReactivation, amountABeforeRejectedReactivation, "below-current-min reactivation must not mutate staking state")
	assertStakingConservation(t, servers[0], validator, knownStakers)

	beforeTopUpRaw, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	beforeTopUpActive, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	beforeTopUpInfo, err := queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	topUp := new(big.Int).Set(delegatorMinStake)
	receiptTopUpA, err := sendDelegateTx(servers[0], delegatorA, delegatorAKey, validator, topUp)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptTopUpA.Status)
	afterTopUpInfo, err := queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	require.True(t, afterTopUpInfo.Exists)
	require.False(t, afterTopUpInfo.Active)
	require.GreaterOrEqual(t, afterTopUpInfo.Amount.Cmp(customMin), 0)
	afterTopUpRaw, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	afterTopUpActive, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	delegatorBInfo, err = queryStakerInfo(servers[0], validator, delegatorB)
	require.NoError(t, err)
	require.GreaterOrEqual(t, afterTopUpRaw.Cmp(new(big.Int).Add(beforeTopUpRaw, topUp)), 0)
	require.Equal(t, 0, afterTopUpRaw.Cmp(new(big.Int).Add(afterTopUpInfo.Amount, delegatorBInfo.Amount)))
	require.GreaterOrEqual(t, afterTopUpActive.Cmp(beforeTopUpActive), 0)
	require.Equal(t, 0, afterTopUpActive.Cmp(delegatorBInfo.Amount), "inactive top-up must not add delegatorA to active delegated stake")
	require.Equal(t, 0, afterTopUpInfo.Amount.Cmp(new(big.Int).Add(beforeTopUpInfo.Amount, topUp)))
	assertStakingConservation(t, servers[0], validator, knownStakers)

	receiptReactivateA, err = sendSetDelegationActiveReceiptTx(servers[0], delegatorA, delegatorAKey, validator, true)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptReactivateA.Status)
	delegatorAInfoReactivated, err := queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	require.True(t, delegatorAInfoReactivated.Active)
	delegatorBInfo, err = queryStakerInfo(servers[0], validator, delegatorB)
	require.NoError(t, err)
	activeAfterAReactivated, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, activeAfterAReactivated.Cmp(new(big.Int).Add(delegatorAInfoReactivated.Amount, delegatorBInfo.Amount)), "reactivation at current min must include full delegatorA amount")
	assertStakingConservation(t, servers[0], validator, knownStakers)

	require.NoError(t, sendSetValidatorPoolConfigTxWithMinDelegatorStake(servers[0], validator, validatorKey, true, maxDelegatedStake, big.NewInt(0), 0))
	receiptDeactivateA, err = sendSetDelegationActiveReceiptTx(servers[0], delegatorA, delegatorAKey, validator, false)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptDeactivateA.Status)
	waitForNextEpochTransition(t, servers, epochSize)

	beforeWithdrawInfo, err := queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	require.True(t, beforeWithdrawInfo.Exists)
	require.False(t, beforeWithdrawInfo.Active)
	require.GreaterOrEqual(t, new(big.Int).Sub(beforeWithdrawInfo.Amount, big.NewInt(1)).Cmp(delegatorMinStake), 0)
	beforeWithdrawRaw, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	beforeWithdrawActive, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	receiptWithdrawA, err := sendWithdrawDelegationReceiptTx(servers[0], delegatorA, delegatorAKey, validator, big.NewInt(1))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptWithdrawA.Status)
	afterWithdrawInfo, err := queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	require.True(t, afterWithdrawInfo.Exists)
	require.False(t, afterWithdrawInfo.Active)
	require.Equal(t, 0, afterWithdrawInfo.Amount.Cmp(new(big.Int).Sub(beforeWithdrawInfo.Amount, big.NewInt(1))))
	afterWithdrawRaw, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	afterWithdrawActive, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, afterWithdrawRaw.Cmp(new(big.Int).Sub(beforeWithdrawRaw, big.NewInt(1))))
	require.Equal(t, 0, afterWithdrawActive.Cmp(beforeWithdrawActive), "inactive withdraw must not change active delegated stake")

	receiptReactivateAfterLower, err := sendSetDelegationActiveReceiptTx(servers[0], delegatorA, delegatorAKey, validator, true)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptReactivateAfterLower.Status)
	delegatorAInfoAfterLowerReactivation, err := queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	require.True(t, delegatorAInfoAfterLowerReactivation.Active)
	delegatorBInfo, err = queryStakerInfo(servers[0], validator, delegatorB)
	require.NoError(t, err)
	activeAfterLowerReactivation, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, activeAfterLowerReactivation.Cmp(new(big.Int).Add(delegatorAInfoAfterLowerReactivation.Amount, delegatorBInfo.Amount)))
	assertStakingConservation(t, servers[0], validator, knownStakers)
}

func minDelegatorStakeSnapshot(t *testing.T, srv *framework.TestServer, validator, delegator types.Address) (*big.Int, *big.Int, *big.Int) {
	t.Helper()

	active, err := queryValidatorDelegatedStakeActive(srv, validator, validator)
	require.NoError(t, err)
	raw, err := queryValidatorDelegatedStakeRaw(srv, validator, validator)
	require.NoError(t, err)
	info, err := queryStakerInfo(srv, validator, delegator)
	require.NoError(t, err)

	return active, raw, new(big.Int).Set(info.Amount)
}

func minDelegatorStakeRequireSnapshotUnchanged(
	t *testing.T,
	srv *framework.TestServer,
	validator types.Address,
	delegator types.Address,
	activeBefore *big.Int,
	rawBefore *big.Int,
	amountBefore *big.Int,
	msg string,
) {
	t.Helper()

	activeAfter, rawAfter, amountAfter := minDelegatorStakeSnapshot(t, srv, validator, delegator)
	require.Equal(t, 0, activeAfter.Cmp(activeBefore), msg)
	require.Equal(t, 0, rawAfter.Cmp(rawBefore), msg)
	require.Equal(t, 0, amountAfter.Cmp(amountBefore), msg)
}

func TestPoS_StakingV2_DelegationPoolCap_UsesRewardGrownRawStake(t *testing.T) {
	const epochSize = uint64(5)

	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, epochSize, 1, 4)

	validator := addrs[0]
	validatorKey := keys[0]
	knownStakers := append([]types.Address(nil), addrs...)

	delegatorMinStake, err := queryDelegatorMinStake(servers[0], validator)
	require.NoError(t, err)
	effectiveMinDelegatorStake := new(big.Int).Set(delegatorMinStake)

	maxTotalDelegatedStake := new(big.Int).Mul(delegatorMinStake, big.NewInt(3))
	require.NoError(t, sendSetValidatorPoolConfigTx(servers[0], validator, validatorKey, true, maxTotalDelegatedStake, 0))

	delegatorAKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegatorA := crypto.PubKeyToAddress(&delegatorAKey.PublicKey)
	knownStakers = append(knownStakers, delegatorA)
	delegatorAPrincipal := new(big.Int).Mul(delegatorMinStake, big.NewInt(2))
	fundAccount(t, servers[0], validator, validatorKey, delegatorA, new(big.Int).Mul(delegatorMinStake, big.NewInt(10)))

	waitUntilMidEpoch(t, servers[0], epochSize)
	receiptA, err := sendDelegateTx(servers[0], delegatorA, delegatorAKey, validator, delegatorAPrincipal)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptA.Status)
	waitForNextEpochTransition(t, servers, epochSize)

	delegatorAInfoBeforeReward, err := queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	require.True(t, delegatorAInfoBeforeReward.Exists)
	require.True(t, delegatorAInfoBeforeReward.Active)
	require.Equal(t, 0, delegatorAInfoBeforeReward.Amount.Cmp(delegatorAPrincipal))
	rawBeforeReward, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, rawBeforeReward.Cmp(delegatorAPrincipal))
	activeBeforeReward, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, activeBeforeReward.Cmp(delegatorAPrincipal))
	assertStakingConservation(t, servers[0], validator, knownStakers)

	delegatorAInfoAfterReward, rawAfterReward := waitForDelegationRewardGrowth(t, servers, validator, validatorKey, delegatorA, delegatorAPrincipal, epochSize)
	require.GreaterOrEqual(t, rawAfterReward.Cmp(delegatorAInfoAfterReward.Amount), 0)
	require.Greater(t, rawAfterReward.Cmp(delegatorAPrincipal), 0)
	activeAfterReward, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	require.GreaterOrEqual(t, activeAfterReward.Cmp(rawAfterReward), 0)
	assertStakingConservation(t, servers[0], validator, knownStakers)

	delegatorBKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegatorB := crypto.PubKeyToAddress(&delegatorBKey.PublicKey)
	knownStakers = append(knownStakers, delegatorB)
	fundAccount(t, servers[0], validator, validatorKey, delegatorB, new(big.Int).Mul(delegatorMinStake, big.NewInt(10)))

	remainingByPrincipal := new(big.Int).Sub(maxTotalDelegatedStake, delegatorAPrincipal)
	attemptAmount := new(big.Int).Set(remainingByPrincipal)
	require.GreaterOrEqual(t, attemptAmount.Cmp(effectiveMinDelegatorStake), 0)
	require.Greater(t, new(big.Int).Add(new(big.Int).Set(rawAfterReward), attemptAmount).Cmp(maxTotalDelegatedStake), 0)

	waitUntilMidEpoch(t, servers[0], epochSize)
	rawBeforeRejectedB, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	activeBeforeRejectedB, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	delegatorAInfoBeforeRejectedB, err := queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)

	receiptB, err := sendDelegateTx(servers[0], delegatorB, delegatorBKey, validator, attemptAmount)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receiptB.Status)

	delegatorBInfo, err := queryStakerInfo(servers[0], validator, delegatorB)
	require.NoError(t, err)
	require.False(t, delegatorBInfo.Exists)
	rawAfterRejectedB, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, rawAfterRejectedB.Cmp(rawBeforeRejectedB), "failed cap tx must not mutate raw delegated stake")
	activeAfterRejectedB, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, activeAfterRejectedB.Cmp(activeBeforeRejectedB), "failed cap tx must not mutate active delegated stake")
	delegatorAInfoAfterRejectedB, err := queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	require.Equal(t, 0, delegatorAInfoAfterRejectedB.Amount.Cmp(delegatorAInfoBeforeRejectedB.Amount), "failed cap tx must not mutate existing delegator amount")
	assertStakingConservation(t, servers[0], validator, knownStakers)

	remainingByRaw := new(big.Int).Sub(maxTotalDelegatedStake, rawAfterRejectedB)
	if remainingByRaw.Cmp(effectiveMinDelegatorStake) >= 0 {
		receiptB, err = sendDelegateTx(servers[0], delegatorB, delegatorBKey, validator, remainingByRaw)
		require.NoError(t, err)
		require.Equal(t, uint64(types.ReceiptSuccess), receiptB.Status)
		rawAtCap, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
		require.NoError(t, err)
		require.Equal(t, 0, rawAtCap.Cmp(maxTotalDelegatedStake))
	} else {
		require.GreaterOrEqual(t, remainingByRaw.Sign(), 0)
		require.Less(t, remainingByRaw.Cmp(effectiveMinDelegatorStake), 0, "reward-grown raw stake left less than one minimum delegation of pool capacity")
		tooSmallToBeValid := new(big.Int).Set(remainingByRaw)
		if tooSmallToBeValid.Sign() == 0 {
			tooSmallToBeValid = big.NewInt(1)
		}
		receiptB, err = sendDelegateTx(servers[0], delegatorB, delegatorBKey, validator, tooSmallToBeValid)
		require.NoError(t, err)
		require.Equal(t, uint64(types.ReceiptFailed), receiptB.Status)
		delegatorBInfo, err = queryStakerInfo(servers[0], validator, delegatorB)
		require.NoError(t, err)
		require.False(t, delegatorBInfo.Exists)
	}

	assertStakingConservation(t, servers[0], validator, knownStakers)
}

func waitForDelegationRewardGrowth(
	t *testing.T,
	servers []*framework.TestServer,
	validator types.Address,
	validatorKey *ecdsa.PrivateKey,
	delegator types.Address,
	principal *big.Int,
	epochSize uint64,
) (*stakingStakerInfo, *big.Int) {
	t.Helper()

	for epochAttempt := 0; epochAttempt < 6; epochAttempt++ {
		for txIndex := 0; txIndex < 4; txIndex++ {
			sendRewardFeeTransfer(t, servers[0], validator, validatorKey)
		}
		waitForNextEpochTransition(t, servers, epochSize)

		info, err := queryStakerInfo(servers[0], validator, delegator)
		require.NoError(t, err)
		require.True(t, info.Exists)
		require.True(t, info.Active)

		raw, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
		require.NoError(t, err)
		if raw.Cmp(principal) > 0 && info.Amount.Cmp(principal) > 0 {
			return info, raw
		}
	}

	info, err := queryStakerInfo(servers[0], validator, delegator)
	require.NoError(t, err)
	raw, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	require.Greater(t, info.Amount.Cmp(principal), 0, "delegator stake amount did not grow from fee rewards")
	require.Greater(t, raw.Cmp(principal), 0, "delegated raw stake did not grow from fee rewards")

	return info, raw
}

func sendRewardFeeTransfer(t *testing.T, srv *framework.TestServer, from types.Address, key *ecdsa.PrivateKey) {
	t.Helper()

	receiverKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	receiver := crypto.PubKeyToAddress(&receiverKey.PublicKey)
	rewardGasPrice := new(big.Int).Mul(framework.TestGasPrice(), big.NewInt(1000))

	ctx, cancel := context.WithTimeout(context.Background(), framework.DefaultTimeout)
	defer cancel()

	receipt, err := srv.SendRawTx(ctx, &framework.PreparedTransaction{
		From:     from,
		To:       &receiver,
		GasPrice: rewardGasPrice,
		Gas:      framework.DefaultGasLimit,
		Value:    big.NewInt(1),
	}, key)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)
}

func TestPoS_StakingV2_DelegationPool_MaxDelegatedStakeCap(t *testing.T) {
	const epochSize = uint64(5)

	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, epochSize, 1, 4)

	minSelfStake, err := queryValidatorMinSelfStake(servers[0], addrs[0])
	require.NoError(t, err)
	receiptStake, err := sendStakeTx(servers[0], addrs[0], keys[0], minSelfStake)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptStake.Status)

	delegatorMinStake, err := queryDelegatorMinStake(servers[0], addrs[0])
	require.NoError(t, err)

	maxDelegatedStake := new(big.Int).Mul(delegatorMinStake, big.NewInt(2))
	require.NoError(t, sendSetValidatorPoolConfigTx(servers[0], addrs[0], keys[0], true, maxDelegatedStake, 0))

	delegatorAKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegatorA := crypto.PubKeyToAddress(&delegatorAKey.PublicKey)

	delegatorBKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegatorB := crypto.PubKeyToAddress(&delegatorBKey.PublicKey)

	fundAccount(t, servers[0], addrs[0], keys[0], delegatorA, new(big.Int).Mul(delegatorMinStake, big.NewInt(10)))
	fundAccount(t, servers[0], addrs[0], keys[0], delegatorB, new(big.Int).Mul(delegatorMinStake, big.NewInt(10)))

	waitUntilMidEpoch(t, servers[0], epochSize)
	receiptA, err := sendDelegateTx(servers[0], delegatorA, delegatorAKey, addrs[0], new(big.Int).Set(maxDelegatedStake))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptA.Status)

	waitForNextEpochTransition(t, servers, epochSize)

	delegatorAInfoBefore, err := queryStakerInfo(servers[0], addrs[0], delegatorA)
	require.NoError(t, err)
	require.True(t, delegatorAInfoBefore.Exists)
	require.True(t, delegatorAInfoBefore.Active)
	// StakerInfo.Amount can include rewards, so only lower-bound principal here.
	require.GreaterOrEqual(t, delegatorAInfoBefore.Amount.Cmp(maxDelegatedStake), 0)

	delegatedRawBefore, err := queryValidatorDelegatedStakeRaw(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.Equal(t, 0, delegatedRawBefore.Cmp(maxDelegatedStake))
	delegatedRawSlot8Before, err := queryStakingMappingUintStorage(servers[0], addrs[0], 8)
	require.NoError(t, err)
	require.Equal(t, 0, delegatedRawSlot8Before.Cmp(maxDelegatedStake))

	delegatedActiveBefore, err := queryValidatorDelegatedStakeActive(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.GreaterOrEqual(t, delegatedActiveBefore.Cmp(maxDelegatedStake), 0)
	stakerCountBeforeRejectedTxs, err := queryValidatorStakerCount(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.False(t, validatorHasStaker(t, servers[0], addrs[0], addrs[0], delegatorB))

	receiptB, err := sendDelegateTx(servers[0], delegatorB, delegatorBKey, addrs[0], big.NewInt(1))
	assertTxRejected(t, receiptB, err)

	delegatorBInfo, err := queryStakerInfo(servers[0], addrs[0], delegatorB)
	require.NoError(t, err)
	require.False(t, delegatorBInfo.Exists)

	delegatorAInfoAfterB, err := queryStakerInfo(servers[0], addrs[0], delegatorA)
	require.NoError(t, err)
	require.True(t, delegatorAInfoAfterB.Exists)
	require.True(t, delegatorAInfoAfterB.Active)
	require.GreaterOrEqual(t, delegatorAInfoAfterB.Amount.Cmp(maxDelegatedStake), 0)

	delegatedRawAfterB, err := queryValidatorDelegatedStakeRaw(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.Equal(t, 0, delegatedRawAfterB.Cmp(maxDelegatedStake))
	delegatedRawSlot8AfterB, err := queryStakingMappingUintStorage(servers[0], addrs[0], 8)
	require.NoError(t, err)
	require.Equal(t, 0, delegatedRawSlot8AfterB.Cmp(maxDelegatedStake))

	delegatedActiveAfterB, err := queryValidatorDelegatedStakeActive(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.GreaterOrEqual(t, delegatedActiveAfterB.Cmp(maxDelegatedStake), 0)
	stakerCountAfterBRejected, err := queryValidatorStakerCount(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.Equal(t, stakerCountBeforeRejectedTxs, stakerCountAfterBRejected)
	require.False(t, validatorHasStaker(t, servers[0], addrs[0], addrs[0], delegatorB))

	stakerCountBeforeTopUpRejected, err := queryValidatorStakerCount(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	delegatedRawBeforeTopUp, err := queryValidatorDelegatedStakeRaw(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	delegatedRawSlot8BeforeTopUp, err := queryStakingMappingUintStorage(servers[0], addrs[0], 8)
	require.NoError(t, err)

	receiptATopUp, err := sendDelegateTx(servers[0], delegatorA, delegatorAKey, addrs[0], big.NewInt(1))
	assertTxRejected(t, receiptATopUp, err)

	delegatorAInfoAfterTopUp, err := queryStakerInfo(servers[0], addrs[0], delegatorA)
	require.NoError(t, err)
	require.True(t, delegatorAInfoAfterTopUp.Exists)
	require.True(t, delegatorAInfoAfterTopUp.Active)
	// StakerInfo.Amount can increase from rewards between snapshots; cap is enforced by delegated raw.
	require.GreaterOrEqual(t, delegatorAInfoAfterTopUp.Amount.Cmp(maxDelegatedStake), 0)

	delegatorBInfoAfterTopUp, err := queryStakerInfo(servers[0], addrs[0], delegatorB)
	require.NoError(t, err)
	require.False(t, delegatorBInfoAfterTopUp.Exists)

	delegatedRawAfterTopUp, err := queryValidatorDelegatedStakeRaw(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	// In current PoS accounting, delegated raw can grow between reads due reward accrual.
	require.GreaterOrEqual(t, delegatedRawAfterTopUp.Cmp(maxDelegatedStake), 0)
	require.GreaterOrEqual(t, delegatedRawAfterTopUp.Cmp(delegatedRawBeforeTopUp), 0)
	delegatedRawSlot8AfterTopUp, err := queryStakingMappingUintStorage(servers[0], addrs[0], 8)
	require.NoError(t, err)
	require.Equal(t, 0, delegatedRawSlot8AfterTopUp.Cmp(delegatedRawAfterTopUp))
	require.GreaterOrEqual(t, delegatedRawSlot8AfterTopUp.Cmp(delegatedRawSlot8BeforeTopUp), 0)

	delegatedActiveAfterTopUp, err := queryValidatorDelegatedStakeActive(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.GreaterOrEqual(t, delegatedActiveAfterTopUp.Cmp(maxDelegatedStake), 0)
	stakerCountAfterTopUpRejected, err := queryValidatorStakerCount(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.Equal(t, stakerCountBeforeTopUpRejected, stakerCountAfterTopUpRejected)
	require.Equal(t, stakerCountBeforeRejectedTxs, stakerCountAfterTopUpRejected)
	require.False(t, validatorHasStaker(t, servers[0], addrs[0], addrs[0], delegatorB))
}

func TestPoS_StakingV2_MultipleDelegators_IndependentLifecycle(t *testing.T) {
	const epochSize = uint64(5)

	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, epochSize, 1, 4)

	minSelfStake, err := queryValidatorMinSelfStake(servers[0], addrs[0])
	require.NoError(t, err)
	receiptStake, err := sendStakeTx(servers[0], addrs[0], keys[0], minSelfStake)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptStake.Status)

	delegatorMinStake, err := queryDelegatorMinStake(servers[0], addrs[0])
	require.NoError(t, err)

	require.NoError(t, sendSetValidatorPoolConfigTx(servers[0], addrs[0], keys[0], true, new(big.Int).Mul(delegatorMinStake, big.NewInt(1000)), 0))

	countBefore, err := queryValidatorStakerCount(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)

	delegatorAKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegatorA := crypto.PubKeyToAddress(&delegatorAKey.PublicKey)
	delegatorBKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegatorB := crypto.PubKeyToAddress(&delegatorBKey.PublicKey)

	delegationA := new(big.Int).Set(delegatorMinStake)
	delegationB := new(big.Int).Set(delegatorMinStake)

	fundAccount(t, servers[0], addrs[0], keys[0], delegatorA, new(big.Int).Mul(delegatorMinStake, big.NewInt(12)))
	fundAccount(t, servers[0], addrs[0], keys[0], delegatorB, new(big.Int).Mul(delegatorMinStake, big.NewInt(12)))

	waitUntilMidEpoch(t, servers[0], epochSize)
	receiptA, err := sendDelegateTx(servers[0], delegatorA, delegatorAKey, addrs[0], delegationA)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptA.Status)
	waitForNextEpochTransition(t, servers, epochSize)

	waitUntilMidEpoch(t, servers[0], epochSize)
	receiptB, err := sendDelegateTx(servers[0], delegatorB, delegatorBKey, addrs[0], delegationB)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptB.Status)

	delegatorAInfo, err := queryStakerInfo(servers[0], addrs[0], delegatorA)
	require.NoError(t, err)
	require.True(t, delegatorAInfo.Exists)
	require.True(t, delegatorAInfo.Active)

	delegatorBInfo, err := queryStakerInfo(servers[0], addrs[0], delegatorB)
	require.NoError(t, err)
	require.True(t, delegatorBInfo.Exists)
	require.True(t, delegatorBInfo.Active)

	waitForDelegatedActiveStake(t, servers, addrs[0], new(big.Int).Add(delegationA, delegationB), epochSize)

	countAfterBothJoined, err := queryValidatorStakerCount(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.Equal(t, countBefore+2, countAfterBothJoined)
	require.True(t, validatorHasStaker(t, servers[0], addrs[0], addrs[0], delegatorA))
	require.True(t, validatorHasStaker(t, servers[0], addrs[0], addrs[0], delegatorB))

	require.NoError(t, sendSetDelegationActiveTx(servers[0], delegatorA, delegatorAKey, addrs[0], false))

	delegatorAInfoAfterDeactivate, err := queryStakerInfo(servers[0], addrs[0], delegatorA)
	require.NoError(t, err)
	require.True(t, delegatorAInfoAfterDeactivate.Exists)
	require.False(t, delegatorAInfoAfterDeactivate.Active)

	delegatorBInfoBeforeBoundary, err := queryStakerInfo(servers[0], addrs[0], delegatorB)
	require.NoError(t, err)
	require.True(t, delegatorBInfoBeforeBoundary.Exists)
	require.True(t, delegatorBInfoBeforeBoundary.Active)

	activeBeforeBoundary, err := queryValidatorDelegatedStakeActive(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.GreaterOrEqual(t, activeBeforeBoundary.Cmp(delegationB), 0)

	delegatorAInfoAfterBoundary, err := queryStakerInfo(servers[0], addrs[0], delegatorA)
	require.NoError(t, err)
	require.True(t, delegatorAInfoAfterBoundary.Exists)
	require.False(t, delegatorAInfoAfterBoundary.Active)

	delegatorBInfoAfterBoundary, err := queryStakerInfo(servers[0], addrs[0], delegatorB)
	require.NoError(t, err)
	require.True(t, delegatorBInfoAfterBoundary.Exists)
	require.True(t, delegatorBInfoAfterBoundary.Active)

	waitForDelegatedActiveStake(t, servers, addrs[0], delegationB, epochSize)

	receiptUnstakeA := waitForUnstakeDelegationSuccess(t, servers, delegatorA, delegatorAKey, addrs[0], epochSize)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptUnstakeA.Status)

	delegatorAInfoAfterUnstake, err := queryStakerInfo(servers[0], addrs[0], delegatorA)
	require.NoError(t, err)
	require.False(t, delegatorAInfoAfterUnstake.Exists)

	delegatorBInfoAfterAUnstake, err := queryStakerInfo(servers[0], addrs[0], delegatorB)
	require.NoError(t, err)
	require.True(t, delegatorBInfoAfterAUnstake.Exists)
	require.True(t, delegatorBInfoAfterAUnstake.Active)
	// B keeps its delegation principal, but amount can increase due to rewards between txs.
	require.GreaterOrEqual(t, delegatorBInfoAfterAUnstake.Amount.Cmp(delegationB), 0)

	countAfterAUnstake, err := queryValidatorStakerCount(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.Equal(t, countAfterBothJoined-1, countAfterAUnstake)
	require.False(t, validatorHasStaker(t, servers[0], addrs[0], addrs[0], delegatorA))
	require.True(t, validatorHasStaker(t, servers[0], addrs[0], addrs[0], delegatorB))

	activeAfterAUnstake, err := queryValidatorDelegatedStakeActive(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	// Active delegated stake can be higher than principal because rewards accrue on active stake.
	require.GreaterOrEqual(t, activeAfterAUnstake.Cmp(delegationB), 0)

	require.NoError(t, sendSetDelegationActiveTx(servers[0], delegatorB, delegatorBKey, addrs[0], false))
	waitForNextEpochTransition(t, servers, epochSize)

	receiptUnstakeB := waitForUnstakeDelegationSuccess(t, servers, delegatorB, delegatorBKey, addrs[0], epochSize)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptUnstakeB.Status)

	delegatorBInfoAfterUnstake, err := queryStakerInfo(servers[0], addrs[0], delegatorB)
	require.NoError(t, err)
	require.False(t, delegatorBInfoAfterUnstake.Exists)

	countAfterBUnstake, err := queryValidatorStakerCount(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.Equal(t, countAfterAUnstake-1, countAfterBUnstake)
	require.False(t, validatorHasStaker(t, servers[0], addrs[0], addrs[0], delegatorB))

	activeAfterBUnstake, err := queryValidatorDelegatedStakeActive(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.Equal(t, 0, activeAfterBUnstake.Cmp(big.NewInt(0)))
}

func TestPoS_StakingV2_DelegationPool_MinDelegatorStake_DefaultAndCustom(t *testing.T) {
	const epochSize = uint64(5)

	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, epochSize, 1, 4)

	minSelfStake, err := queryValidatorMinSelfStake(servers[0], addrs[0])
	require.NoError(t, err)
	receiptStake, err := sendStakeTx(servers[0], addrs[0], keys[0], minSelfStake)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptStake.Status)

	delegatorMinStake, err := queryDelegatorMinStake(servers[0], addrs[0])
	require.NoError(t, err)
	maxDelegatedStake := new(big.Int).Mul(delegatorMinStake, big.NewInt(1000))

	// minDelegatorStake=0 -> DELEGATOR_MIN_STAKE fallback
	require.NoError(t, sendSetValidatorPoolConfigTx(servers[0], addrs[0], keys[0], true, maxDelegatedStake, 0))

	k1, _ := crypto.GenerateECDSAKey()
	d1 := crypto.PubKeyToAddress(&k1.PublicKey)
	fundAccount(t, servers[0], addrs[0], keys[0], d1, new(big.Int).Mul(delegatorMinStake, big.NewInt(3)))
	r, err := sendDelegateTx(servers[0], d1, k1, addrs[0], new(big.Int).Sub(delegatorMinStake, big.NewInt(1)))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), r.Status)
	r, err = sendDelegateTx(servers[0], d1, k1, addrs[0], new(big.Int).Set(delegatorMinStake))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), r.Status)

	// custom minDelegatorStake > default: below fails, equal succeeds
	customMin := new(big.Int).Mul(delegatorMinStake, big.NewInt(3))
	require.NoError(t, sendSetValidatorPoolConfigTxWithMinDelegatorStake(servers[0], addrs[0], keys[0], true, maxDelegatedStake, customMin, 0))

	k2, _ := crypto.GenerateECDSAKey()
	d2 := crypto.PubKeyToAddress(&k2.PublicKey)
	fundAccount(t, servers[0], addrs[0], keys[0], d2, new(big.Int).Mul(customMin, big.NewInt(2)))
	r, err = sendDelegateTx(servers[0], d2, k2, addrs[0], new(big.Int).Sub(customMin, big.NewInt(1)))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), r.Status)
	r, err = sendDelegateTx(servers[0], d2, k2, addrs[0], new(big.Int).Set(customMin))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), r.Status)

	// invalid custom min below default must revert/fail
	tooLow := new(big.Int).Sub(delegatorMinStake, big.NewInt(1))
	err = sendSetValidatorPoolConfigTxWithMinDelegatorStake(servers[0], addrs[0], keys[0], true, maxDelegatedStake, tooLow, 0)
	require.Error(t, err)
}

func sendSetValidatorPoolConfigTxWithMinDelegatorStake(
	srv *framework.TestServer,
	from types.Address,
	key *ecdsa.PrivateKey,
	delegationEnabled bool,
	maxTotalDelegatedStake *big.Int,
	minDelegatorStake *big.Int,
	commissionBps uint16,
) error {
	m := abis.StakingABI.Methods["setValidatorPoolConfig"]
	if m == nil {
		return errors.New("staking ABI missing setValidatorPoolConfig")
	}

	inp, err := m.Inputs.Encode(map[string]interface{}{
		"delegationEnabled":      delegationEnabled,
		"maxTotalDelegatedStake": maxTotalDelegatedStake,
		"minDelegatorStake":      minDelegatorStake,
		"commissionBps":          commissionBps,
	})
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
	if receipt == nil {
		return errors.New("setValidatorPoolConfig transaction receipt is nil")
	}
	if receipt.Status != uint64(types.ReceiptSuccess) {
		return errors.New("setValidatorPoolConfig transaction failed")
	}

	return nil
}

func TestPoS_StakingV2_StorageLayout_DelegationOracle(t *testing.T) {
	const epochSize = uint64(5)

	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, epochSize, 1, 4)

	minSelfStake, err := queryValidatorMinSelfStake(servers[0], addrs[0])
	require.NoError(t, err)
	receiptStake, err := sendStakeTx(servers[0], addrs[0], keys[0], minSelfStake)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptStake.Status)

	delegatorMinStake, err := queryDelegatorMinStake(servers[0], addrs[0])
	require.NoError(t, err)
	require.NoError(t, sendSetValidatorPoolConfigTx(servers[0], addrs[0], keys[0], true, new(big.Int).Mul(delegatorMinStake, big.NewInt(1000)), 0))

	delegatorKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegator := crypto.PubKeyToAddress(&delegatorKey.PublicKey)
	delegation := new(big.Int).Set(delegatorMinStake)

	fundAccount(t, servers[0], addrs[0], keys[0], delegator, new(big.Int).Mul(delegatorMinStake, big.NewInt(10)))
	receiptDelegate, err := sendDelegateTx(servers[0], delegator, delegatorKey, addrs[0], delegation)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptDelegate.Status)

	waitForNextEpochTransition(t, servers, epochSize)

	abiActive, err := queryValidatorDelegatedStakeActive(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.GreaterOrEqual(t, abiActive.Cmp(delegation), 0)
	abiRaw, err := queryValidatorDelegatedStakeRaw(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.Equal(t, 0, abiRaw.Cmp(delegation))

	abiCount, err := queryValidatorStakerCount(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.GreaterOrEqual(t, abiCount, uint64(2))
	require.True(t, validatorHasStaker(t, servers[0], addrs[0], addrs[0], delegator))

	rawActiveSlot6, err := queryStakingMappingUintStorage(servers[0], addrs[0], 6)
	require.NoError(t, err)
	rawActiveSlot9, err := queryStakingMappingUintStorage(servers[0], addrs[0], 9)
	require.NoError(t, err)
	require.NotEqual(t, 0, rawActiveSlot6.Cmp(abiActive), "slot 6 must not match active delegated stake mapping")
	require.Equal(t, 0, rawActiveSlot9.Cmp(abiActive), "slot 9 must match active delegated stake mapping")
	rawDelegatedSlot8, err := queryStakingMappingUintStorage(servers[0], addrs[0], 8)
	require.NoError(t, err)
	require.Equal(t, 0, rawDelegatedSlot8.Cmp(abiRaw), "slot 8 must match raw delegated stake mapping")

	rawStakersByValidatorLenAt6, err := queryStakingMappingUintStorage(servers[0], addrs[0], 6)
	require.NoError(t, err)
	rawStakersByValidatorLenAt8, err := queryStakingMappingUintStorage(servers[0], addrs[0], 8)
	require.NoError(t, err)
	require.Equal(t, new(big.Int).SetUint64(abiCount).Uint64(), rawStakersByValidatorLenAt6.Uint64())
	require.NotEqual(t, 0, rawStakersByValidatorLenAt6.Cmp(rawStakersByValidatorLenAt8))

	for i := uint64(0); i < abiCount; i++ {
		abiStaker, abiErr := queryValidatorStakerAt(servers[0], addrs[0], addrs[0], i)
		require.NoError(t, abiErr)

		rawStakerAt6, rawErr := queryStakingValidatorStakerAtRawStorage(servers[0], addrs[0], 6, i)
		require.NoError(t, rawErr)
		rawStakerAt8, raw8Err := queryStakingValidatorStakerAtRawStorage(servers[0], addrs[0], 8, i)
		require.NoError(t, raw8Err)

		require.Equal(t, abiStaker, rawStakerAt6)
		require.NotEqual(t, abiStaker, rawStakerAt8)

		rawIndexPlusOneAt7, rawIndexErr := queryStakingNestedMappingUintStorage(servers[0], addrs[0], abiStaker, 7)
		require.NoError(t, rawIndexErr)
		require.Equal(t, 0, rawIndexPlusOneAt7.Cmp(new(big.Int).SetUint64(i+1)))

		rawIndexPlusOneAt8, rawIndexAt8Err := queryStakingNestedMappingUintStorage(servers[0], addrs[0], abiStaker, 8)
		require.NoError(t, rawIndexAt8Err)
		require.NotEqual(t, 0, rawIndexPlusOneAt8.Cmp(new(big.Int).SetUint64(i+1)))
	}
}

func TestPoS_StakingV2_StorageLayout_ValidatorPoolConfigOracle(t *testing.T) {
	const epochSize = uint64(5)

	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, epochSize, 1, 4)
	validator := addrs[0]

	minSelfStake, err := queryValidatorMinSelfStake(servers[0], validator)
	require.NoError(t, err)
	receiptStake, err := sendStakeTx(servers[0], validator, keys[0], minSelfStake)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receiptStake.Status)

	delegatorMinStake, err := queryDelegatorMinStake(servers[0], validator)
	require.NoError(t, err)

	maxTotalDelegatedStake := new(big.Int).Mul(delegatorMinStake, big.NewInt(123))
	minDelegatorStake := new(big.Int).Mul(delegatorMinStake, big.NewInt(7))
	commissionBps := uint16(1234)
	require.NotEqual(t, minDelegatorStake.Uint64(), uint64(commissionBps), "oracle values must distinguish minDelegatorStake from commissionBps")

	require.NoError(t, sendSetValidatorPoolConfigTxWithMinDelegatorStake(
		servers[0],
		validator,
		keys[0],
		true,
		maxTotalDelegatedStake,
		minDelegatorStake,
		commissionBps,
	))

	pool, err := queryValidatorPoolConfig(servers[0], validator, validator)
	require.NoError(t, err)
	require.True(t, pool.delegationEnabled)
	require.Equal(t, 0, pool.maxDelegatedStake.Cmp(maxTotalDelegatedStake))
	require.Equal(t, 0, pool.minDelegatorStake.Cmp(minDelegatorStake))
	require.Equal(t, commissionBps, pool.commissionBps)

	packedFlags, err := queryStakingMappingUintStorageWithOffset(servers[0], validator, 5, 0)
	require.NoError(t, err)
	require.Equal(t, uint64(1), new(big.Int).And(new(big.Int).Set(packedFlags), big.NewInt(0xff)).Uint64(), "pool exists flag must be packed at base+0")
	require.Equal(t, uint64(1), new(big.Int).And(new(big.Int).Rsh(new(big.Int).Set(packedFlags), 8), big.NewInt(0xff)).Uint64(), "pool active flag must be packed at base+0")
	require.Equal(t, uint64(1), new(big.Int).And(new(big.Int).Rsh(new(big.Int).Set(packedFlags), 16), big.NewInt(0xff)).Uint64(), "pool delegationEnabled flag must be packed at base+0")

	rawMaxTotalDelegatedStake, err := queryStakingMappingUintStorageWithOffset(servers[0], validator, 5, 1)
	require.NoError(t, err)
	require.Equal(t, 0, rawMaxTotalDelegatedStake.Cmp(maxTotalDelegatedStake), "_poolConfig base+1 must be maxTotalDelegatedStake")
	rawMinDelegatorStake, err := queryStakingMappingUintStorageWithOffset(servers[0], validator, 5, 2)
	require.NoError(t, err)
	require.Equal(t, 0, rawMinDelegatorStake.Cmp(minDelegatorStake), "_poolConfig base+2 must be minDelegatorStake")
	rawCommissionBps, err := queryStakingMappingUintStorageWithOffset(servers[0], validator, 5, 3)
	require.NoError(t, err)
	require.Equal(t, uint64(commissionBps), rawCommissionBps.Uint64(), "_poolConfig base+3 must be commissionBps")
	require.NotEqual(t, rawMinDelegatorStake.Uint64(), rawCommissionBps.Uint64(), "raw oracle must distinguish minDelegatorStake from commissionBps")
}

func queryStakingMappingUintStorage(srv *framework.TestServer, validator types.Address, slot int64) (*big.Int, error) {
	base := keccak.Keccak256(nil,
		append(
			common.PadLeftOrTrim(validator.Bytes(), 32),
			common.PadLeftOrTrim(big.NewInt(slot).Bytes(), 32)...,
		),
	)
	raw, err := srv.JSONRPC().Eth().GetStorageAt(ethgo.Address(staking.AddrStakingContract), ethgo.Hash(types.BytesToHash(base)), ethgo.Latest)
	if err != nil {
		return nil, err
	}

	return new(big.Int).SetBytes(raw[:]), nil
}

func queryStakingMappingUintStorageWithOffset(srv *framework.TestServer, validator types.Address, slot int64, offset int64) (*big.Int, error) {
	base := keccak.Keccak256(nil,
		append(
			common.PadLeftOrTrim(validator.Bytes(), 32),
			common.PadLeftOrTrim(big.NewInt(slot).Bytes(), 32)...,
		),
	)
	location := new(big.Int).Add(new(big.Int).SetBytes(base), big.NewInt(offset))
	raw, err := srv.JSONRPC().Eth().GetStorageAt(ethgo.Address(staking.AddrStakingContract), ethgo.Hash(types.BytesToHash(location.Bytes())), ethgo.Latest)
	if err != nil {
		return nil, err
	}

	return new(big.Int).SetBytes(raw[:]), nil
}

func queryStakingValidatorStakerAtRawStorage(srv *framework.TestServer, validator types.Address, slot int64, idx uint64) (types.Address, error) {
	base := keccak.Keccak256(nil,
		append(
			common.PadLeftOrTrim(validator.Bytes(), 32),
			common.PadLeftOrTrim(big.NewInt(slot).Bytes(), 32)...,
		),
	)
	arrayBase := keccak.Keccak256(nil, common.PadLeftOrTrim(base, 32))
	location := new(big.Int).Add(new(big.Int).SetBytes(arrayBase), new(big.Int).SetUint64(idx))

	raw, err := srv.JSONRPC().Eth().GetStorageAt(
		ethgo.Address(staking.AddrStakingContract),
		ethgo.Hash(types.BytesToHash(location.Bytes())),
		ethgo.Latest,
	)
	if err != nil {
		return types.ZeroAddress, err
	}

	return types.BytesToAddress(raw[12:]), nil
}

func queryStakingNestedMappingUintStorage(
	srv *framework.TestServer,
	outerKey types.Address,
	innerKey types.Address,
	slot int64,
) (*big.Int, error) {
	outerLocation := keccak.Keccak256(nil,
		append(
			common.PadLeftOrTrim(outerKey.Bytes(), 32),
			common.PadLeftOrTrim(big.NewInt(slot).Bytes(), 32)...,
		),
	)
	valueLocation := keccak.Keccak256(nil,
		append(
			common.PadLeftOrTrim(innerKey.Bytes(), 32),
			common.PadLeftOrTrim(outerLocation, 32)...,
		),
	)
	raw, err := srv.JSONRPC().Eth().GetStorageAt(
		ethgo.Address(staking.AddrStakingContract),
		ethgo.Hash(types.BytesToHash(valueLocation)),
		ethgo.Latest,
	)
	if err != nil {
		return nil, err
	}

	return new(big.Int).SetBytes(raw[:]), nil
}

func assertTxRejected(t *testing.T, receipt *ethgo.Receipt, err error) {
	t.Helper()

	if err != nil {
		return
	}
	require.NotNil(t, receipt)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status)
}

func waitForDelegatedActiveStake(
	t *testing.T,
	servers []*framework.TestServer,
	validator types.Address,
	expected *big.Int,
	epochSize uint64,
) {
	t.Helper()

	maxBlocks := epochSize*3 + 2
	for i := uint64(0); i < maxBlocks; i++ {
		active, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
		require.NoError(t, err)
		if active.Cmp(expected) == 0 {
			return
		}

		height, hErr := servers[0].JSONRPC().Eth().BlockNumber()
		require.NoError(t, hErr)
		require.Empty(t, framework.WaitForServersToSeal(servers, height+1))
	}

	active, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, active.Cmp(expected))
}

func waitForUnstakeDelegationSuccess(
	t *testing.T,
	servers []*framework.TestServer,
	from types.Address,
	key *ecdsa.PrivateKey,
	validator types.Address,
	epochSize uint64,
) *ethgo.Receipt {
	t.Helper()

	maxBlocks := epochSize*3 + 2
	for i := uint64(0); i < maxBlocks; i++ {
		receipt, err := sendUnstakeDelegationTx(servers[0], from, key, validator)
		if err == nil && receipt != nil && receipt.Status == uint64(types.ReceiptSuccess) {
			return receipt
		}

		height, hErr := servers[0].JSONRPC().Eth().BlockNumber()
		require.NoError(t, hErr)
		require.Empty(t, framework.WaitForServersToSeal(servers, height+1))
	}

	receipt, err := sendUnstakeDelegationTx(servers[0], from, key, validator)
	require.NoError(t, err)
	require.NotNil(t, receipt)

	return receipt
}
