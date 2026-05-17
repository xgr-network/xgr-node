package e2e

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/xgr-network/xgr-node/contracts/abis"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/e2e/framework"
	"github.com/xgr-network/xgr-node/helper/common"
	"github.com/xgr-network/xgr-node/helper/hex"
	"github.com/xgr-network/xgr-node/types"
)

type stakingStakerInfo struct {
	Exists             bool
	Active             bool
	Amount             *big.Int
	JoinedAtBlock      *big.Int
	DeactivatedAtBlock *big.Int
	Validator          types.Address
}

func TestPoS_StakingV2_ValidatorLifecycle_MinStakeAndWithdrawBoundaries(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2Cluster(t, 3, 5, 2, 4)

	minSelfStake, err := queryValidatorMinSelfStake(servers[0], addrs[0])
	require.NoError(t, err)
	validatorThreshold, err := queryValidatorThreshold(servers[0], addrs[0])
	require.NoError(t, err)

	exactMinKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	exactMinAddr := crypto.PubKeyToAddress(&exactMinKey.PublicKey)
	fundAccount(t, servers[0], addrs[0], keys[0], exactMinAddr, new(big.Int).Mul(minSelfStake, big.NewInt(2)))
	receipt, err := sendStakeTx(servers[0], exactMinAddr, exactMinKey, minSelfStake)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status, "stake exactly min should succeed")

	belowMinKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	belowMinAddr := crypto.PubKeyToAddress(&belowMinKey.PublicKey)
	fundAccount(t, servers[0], addrs[0], keys[0], belowMinAddr, new(big.Int).Mul(minSelfStake, big.NewInt(2)))
	receipt, err = sendStakeTx(servers[0], belowMinAddr, belowMinKey, new(big.Int).Sub(minSelfStake, big.NewInt(1)))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status, "stake below min should fail")

	joinKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	joinAddr := crypto.PubKeyToAddress(&joinKey.PublicKey)
	fundAccount(t, servers[0], addrs[0], keys[0], joinAddr, new(big.Int).Mul(validatorThreshold, big.NewInt(2)))
	receipt, err = sendStakeTx(servers[0], joinAddr, joinKey, validatorThreshold)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	snapshotBeforeJoin := mustGetLatestSnapshotValidators(t, servers[0])
	require.NotContains(t, snapshotBeforeJoin, joinAddr, "join should not be consensus-effective in same epoch")
	waitForNextEpochTransition(t, servers, 5)
	snapshotAfterJoin := mustGetLatestSnapshotValidators(t, servers[0])
	require.Contains(t, snapshotAfterJoin, joinAddr, "join should be consensus-effective in next epoch")

	underMinKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	underMinAddr := crypto.PubKeyToAddress(&underMinKey.PublicKey)
	fundAccount(t, servers[0], addrs[0], keys[0], underMinAddr, new(big.Int).Mul(minSelfStake, big.NewInt(2)))
	receipt, err = sendStakeTx(servers[0], underMinAddr, underMinKey, new(big.Int).Sub(minSelfStake, big.NewInt(1)))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status)
	underMinInfo, err := queryStakerInfo(servers[0], addrs[0], underMinAddr)
	require.NoError(t, err)
	require.False(t, underMinInfo.Exists)
	receipt, err = sendSetActiveReceiptTx(servers[0], underMinAddr, underMinKey, true)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status)

	validatorAddr := addrs[0]
	validatorKey := keys[0]
	receipt, err = sendWithdrawTx(servers[0], validatorAddr, validatorKey, big.NewInt(1))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status, "withdraw while active must fail")

	waitUntilMidEpoch(t, servers[0], 5)
	require.NoError(t, sendSetActiveTx(servers[0], validatorAddr, validatorKey, false))
	snapshotBeforeDeactivateBoundary := mustGetLatestSnapshotValidators(t, servers[0])
	require.Contains(t, snapshotBeforeDeactivateBoundary, validatorAddr, "deactivate should not remove validator in same epoch")
	receipt, err = sendWithdrawTx(servers[0], validatorAddr, validatorKey, big.NewInt(1))
	require.NoError(t, err)

	waitForNextEpochTransition(t, servers, 5)
	snapshotAfterDeactivateBoundary := mustGetLatestSnapshotValidators(t, servers[0])
	require.NotContains(t, snapshotAfterDeactivateBoundary, validatorAddr, "deactivate should be effective after epoch boundary")
	receipt, err = sendWithdrawTx(servers[0], validatorAddr, validatorKey, big.NewInt(1))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	accountStake, err := framework.GetAccountStake(validatorAddr, servers[0].JSONRPC())
	require.NoError(t, err)
	withdrawTooMuch := new(big.Int).Sub(accountStake, new(big.Int).Sub(minSelfStake, big.NewInt(1)))
	receipt, err = sendWithdrawTx(servers[0], validatorAddr, validatorKey, withdrawTooMuch)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status)

	receipt, err = framework.UnstakeAmount(validatorAddr, validatorKey, servers[0])
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)
	waitForNextEpochTransition(t, servers, 5)

	info, err := queryValidatorInfo(servers[0], addrs[0], validatorAddr)
	require.NoError(t, err)
	require.False(t, info.Exists)
	require.Zero(t, info.StakedAmount.Sign())
}

func TestPoS_StakingV2_DelegatorLifecycle_ActivationWithdrawAndExit(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2Cluster(t, 3, 5, 1, 3)

	minSelfStake, err := queryValidatorMinSelfStake(servers[0], addrs[0])
	require.NoError(t, err)
	delegatorMinStake, err := queryDelegatorMinStake(servers[0], addrs[0])
	require.NoError(t, err)

	receipt, err := sendStakeTx(servers[0], addrs[0], keys[0], minSelfStake)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	delegatorKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegatorAddr := crypto.PubKeyToAddress(&delegatorKey.PublicKey)
	fundAccount(t, servers[0], addrs[0], keys[0], delegatorAddr, new(big.Int).Mul(delegatorMinStake, big.NewInt(8)))
	waitForNextEpochTransition(t, servers, 5)

	receipt, err = sendDelegateTx(servers[0], delegatorAddr, delegatorKey, addrs[0], delegatorMinStake)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status, "delegate on closed pool must fail")

	require.NoError(t, sendSetValidatorPoolConfigTx(servers[0], addrs[0], keys[0], true, new(big.Int).Mul(delegatorMinStake, big.NewInt(100)), 0))
	receipt, err = sendDelegateTx(servers[0], delegatorAddr, delegatorKey, addrs[0], new(big.Int).Sub(delegatorMinStake, big.NewInt(1)))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status, "new delegation below min must fail")

	receipt, err = sendDelegateTx(servers[0], delegatorAddr, delegatorKey, addrs[0], delegatorMinStake)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	waitForNextEpochTransition(t, servers, 5)
	activeDelegated, err := queryValidatorDelegatedStakeActive(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.Equal(t, 0, activeDelegated.Cmp(delegatorMinStake))

	waitUntilMidEpoch(t, servers[0], 5)
	require.NoError(t, sendSetDelegationActiveTx(servers[0], delegatorAddr, delegatorKey, addrs[0], false))
	waitForNextEpochTransition(t, servers, 5)

	beforeRaw, err := queryValidatorDelegatedStakeRaw(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	beforeActive, err := queryValidatorDelegatedStakeActive(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)

	addAmount := new(big.Int).Mul(delegatorMinStake, big.NewInt(2))
	receipt, err = sendDelegateTx(servers[0], delegatorAddr, delegatorKey, addrs[0], addAmount)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	afterRaw, err := queryValidatorDelegatedStakeRaw(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	afterActive, err := queryValidatorDelegatedStakeActive(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)

	require.Equal(t, 0, afterRaw.Cmp(new(big.Int).Add(beforeRaw, addAmount)))
	require.Equal(t, 0, afterActive.Cmp(beforeActive), "inactive top-up must not increase active delegated")

	belowMinDelegatorKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	belowMinDelegatorAddr := crypto.PubKeyToAddress(&belowMinDelegatorKey.PublicKey)
	fundAccount(t, servers[0], addrs[0], keys[0], belowMinDelegatorAddr, new(big.Int).Mul(delegatorMinStake, big.NewInt(2)))

	receipt, err = sendDelegateTx(servers[0], belowMinDelegatorAddr, belowMinDelegatorKey, addrs[0], new(big.Int).Sub(delegatorMinStake, big.NewInt(1)))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status)

	receipt, err = sendSetDelegationActiveReceiptTx(servers[0], belowMinDelegatorAddr, belowMinDelegatorKey, addrs[0], true)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status, "setDelegationActive(true) after below-min failed join must fail")

	receipt, err = sendDelegateTx(servers[0], delegatorAddr, delegatorKey, addrs[0], new(big.Int).Add(delegatorMinStake, big.NewInt(1)))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	receipt, err = sendSetDelegationActiveReceiptTx(servers[0], delegatorAddr, delegatorKey, addrs[0], true)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status, "setDelegationActive(true) in reactivation path must succeed")

	reactivatedInfo, err := queryStakerInfo(servers[0], addrs[0], delegatorAddr)
	require.NoError(t, err)
	require.True(t, reactivatedInfo.Active)
	require.Zero(t, reactivatedInfo.DeactivatedAtBlock.Sign())

	reactivatedDelegatedActive, err := queryValidatorDelegatedStakeActive(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.Equal(t, 0, reactivatedDelegatedActive.Cmp(reactivatedInfo.Amount))

	receipt, err = sendWithdrawDelegationReceiptTx(servers[0], delegatorAddr, delegatorKey, addrs[0], big.NewInt(1))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status, "withdraw while active must fail")

	require.NoError(t, sendSetDelegationActiveTx(servers[0], delegatorAddr, delegatorKey, addrs[0], false))

	deactivatedInfo, err := queryStakerInfo(servers[0], addrs[0], delegatorAddr)
	require.NoError(t, err)
	require.False(t, deactivatedInfo.Active)
	require.True(t, deactivatedInfo.DeactivatedAtBlock.Sign() > 0)

	receipt, err = sendWithdrawDelegationReceiptTx(servers[0], delegatorAddr, delegatorKey, addrs[0], big.NewInt(1))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status, "same-epoch withdraw after deactivate must fail")

	waitForNextEpochTransition(t, servers, 5)

	receipt, err = sendWithdrawDelegationReceiptTx(servers[0], delegatorAddr, delegatorKey, addrs[0], big.NewInt(1))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	info := &stakingStakerInfo{}
	info, err = queryStakerInfo(servers[0], addrs[0], delegatorAddr)
	require.NoError(t, err)

	withdrawBelowMin := new(big.Int).Sub(info.Amount, new(big.Int).Sub(delegatorMinStake, big.NewInt(1)))
	receipt, err = sendWithdrawDelegationReceiptTx(servers[0], delegatorAddr, delegatorKey, addrs[0], withdrawBelowMin)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status)

	info, err = queryStakerInfo(servers[0], addrs[0], delegatorAddr)
	require.NoError(t, err)

	receipt, err = sendWithdrawDelegationReceiptTx(servers[0], delegatorAddr, delegatorKey, addrs[0], new(big.Int).Set(info.Amount))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status, "full exit must use unstakeDelegation")

	countBefore, err := queryValidatorStakerCount(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)

	receipt, err = sendUnstakeDelegationTx(servers[0], delegatorAddr, delegatorKey, addrs[0])
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	countAfter, err := queryValidatorStakerCount(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.Equal(t, countBefore-1, countAfter)

	info, err = queryStakerInfo(servers[0], addrs[0], delegatorAddr)
	require.NoError(t, err)
	assert.False(t, info.Exists)
}

func TestPoS_StakingV2_EconomicConservation_FullLifecycle(t *testing.T) {
	epochSize := uint64(5)
	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, epochSize, 1, 4)

	validator := addrs[0]
	validatorKey := keys[0]
	knownStakers := append([]types.Address(nil), addrs...)

	minSelfStake, err := queryValidatorMinSelfStake(servers[0], validator)
	require.NoError(t, err)
	delegatorMinStake, err := queryDelegatorMinStake(servers[0], validator)
	require.NoError(t, err)

	validatorInfo, err := queryStakerInfo(servers[0], validator, validator)
	require.NoError(t, err)
	require.True(t, validatorInfo.Exists)

	if validatorInfo.Amount.Cmp(minSelfStake) < 0 {
		topUp := new(big.Int).Sub(minSelfStake, validatorInfo.Amount)
		receipt, stakeErr := sendStakeTx(servers[0], validator, validatorKey, topUp)
		require.NoError(t, stakeErr)
		require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)
	}

	require.NoError(t, sendSetValidatorPoolConfigTx(servers[0], validator, validatorKey, true, new(big.Int).Mul(delegatorMinStake, big.NewInt(100)), 0))

	delegatorAKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegatorA := crypto.PubKeyToAddress(&delegatorAKey.PublicKey)
	knownStakers = append(knownStakers, delegatorA)
	delegationA := new(big.Int).Mul(delegatorMinStake, big.NewInt(3))
	fundAccount(t, servers[0], validator, validatorKey, delegatorA, new(big.Int).Mul(delegatorMinStake, big.NewInt(10)))

	delegatorBKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegatorB := crypto.PubKeyToAddress(&delegatorBKey.PublicKey)
	knownStakers = append(knownStakers, delegatorB)
	delegationB := new(big.Int).Mul(delegatorMinStake, big.NewInt(2))
	fundAccount(t, servers[0], validator, validatorKey, delegatorB, new(big.Int).Mul(delegatorMinStake, big.NewInt(10)))

	assertStakingConservation(t, servers[0], validator, knownStakers)

	rejectedDelegatorKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	rejectedDelegator := crypto.PubKeyToAddress(&rejectedDelegatorKey.PublicKey)
	fundAccount(t, servers[0], validator, validatorKey, rejectedDelegator, new(big.Int).Mul(delegatorMinStake, big.NewInt(2)))

	assertRejectedStakingTxDoesNotChangeKnownStakes(t, servers[0], validator, knownStakers, epochSize, func() (*ethgo.Receipt, error) {
		return sendDelegateTx(servers[0], rejectedDelegator, rejectedDelegatorKey, validator, new(big.Int).Sub(delegatorMinStake, big.NewInt(1)))
	}, "below-min delegation must be rejected without changing the staking economy")

	rejectedInfo, err := queryStakerInfo(servers[0], validator, rejectedDelegator)
	require.NoError(t, err)
	require.False(t, rejectedInfo.Exists, "below-min rejected delegator must not be created")

	receipt, err := sendDelegateTx(servers[0], delegatorA, delegatorAKey, validator, delegationA)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	delegatorAInfo, err := queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	require.True(t, delegatorAInfo.Exists)
	require.Equal(t, 0, delegatorAInfo.Amount.Cmp(delegationA))

	assertStakingConservation(t, servers[0], validator, knownStakers)

	assertRejectedStakingTxDoesNotChangeKnownStakes(t, servers[0], validator, knownStakers, epochSize, func() (*ethgo.Receipt, error) {
		return sendWithdrawDelegationReceiptTx(servers[0], delegatorA, delegatorAKey, validator, big.NewInt(1))
	}, "active delegator withdraw must be rejected without changing the staking economy")

	receipt, err = sendDelegateTx(servers[0], delegatorB, delegatorBKey, validator, delegationB)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	delegatorBInfo, err := queryStakerInfo(servers[0], validator, delegatorB)
	require.NoError(t, err)
	require.True(t, delegatorBInfo.Exists)
	require.Equal(t, 0, delegatorBInfo.Amount.Cmp(delegationB))

	rawAfterDelegations, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, rawAfterDelegations.Cmp(new(big.Int).Add(delegationA, delegationB)))

	assertStakingConservation(t, servers[0], validator, knownStakers)

	waitForNextEpochTransition(t, servers, epochSize)

	activeAfterDelegations, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)

	delegatorAInfo, err = queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	delegatorBInfo, err = queryStakerInfo(servers[0], validator, delegatorB)
	require.NoError(t, err)

	require.Equal(t, 0, activeAfterDelegations.Cmp(new(big.Int).Add(delegatorAInfo.Amount, delegatorBInfo.Amount)))
	assertStakingConservation(t, servers[0], validator, knownStakers)

	receipt, err = sendSetDelegationActiveReceiptTx(servers[0], delegatorA, delegatorAKey, validator, false)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	delegatorAInfo, err = queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	require.False(t, delegatorAInfo.Active)

	assertStakingConservation(t, servers[0], validator, knownStakers)

	waitForNextEpochTransition(t, servers, epochSize)

	activeAfterAInactive, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)

	delegatorBInfo, err = queryStakerInfo(servers[0], validator, delegatorB)
	require.NoError(t, err)

	require.Equal(t, 0, activeAfterAInactive.Cmp(delegatorBInfo.Amount))
	assertStakingConservation(t, servers[0], validator, knownStakers)

	beforeRaw, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	beforeActive, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	beforeDelegatorAInfo, err := queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)

	inactiveTopUp := new(big.Int).Set(delegatorMinStake)
	receipt, err = sendDelegateTx(servers[0], delegatorA, delegatorAKey, validator, inactiveTopUp)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	afterRaw, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	afterActive, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	afterDelegatorAInfo, err := queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)

	require.Equal(t, 0, afterRaw.Cmp(new(big.Int).Add(beforeRaw, inactiveTopUp)))
	require.Equal(t, 0, afterActive.Cmp(beforeActive), "inactive top-up must not increase active delegated stake")
	require.Equal(t, 0, afterDelegatorAInfo.Amount.Cmp(new(big.Int).Add(beforeDelegatorAInfo.Amount, inactiveTopUp)))

	assertStakingConservation(t, servers[0], validator, knownStakers)

	withdrawDelegatorA := big.NewInt(1)
	beforeDelegatorAInfo = afterDelegatorAInfo

	receipt, err = sendWithdrawDelegationReceiptTx(servers[0], delegatorA, delegatorAKey, validator, withdrawDelegatorA)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	afterDelegatorAInfo, err = queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	require.Equal(t, 0, afterDelegatorAInfo.Amount.Cmp(new(big.Int).Sub(beforeDelegatorAInfo.Amount, withdrawDelegatorA)))

	assertStakingConservation(t, servers[0], validator, knownStakers)

	receipt, err = sendSetDelegationActiveReceiptTx(servers[0], delegatorA, delegatorAKey, validator, true)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	delegatorAInfo, err = queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	delegatorBInfo, err = queryStakerInfo(servers[0], validator, delegatorB)
	require.NoError(t, err)

	activeAfterAReactivation, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, activeAfterAReactivation.Cmp(new(big.Int).Add(delegatorAInfo.Amount, delegatorBInfo.Amount)))

	assertStakingConservation(t, servers[0], validator, knownStakers)

	receipt, err = sendSetDelegationActiveReceiptTx(servers[0], delegatorB, delegatorBKey, validator, false)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	delegatorBInfo, err = queryStakerInfo(servers[0], validator, delegatorB)
	require.NoError(t, err)
	require.False(t, delegatorBInfo.Active)

	assertStakingConservation(t, servers[0], validator, knownStakers)

	waitForNextEpochTransition(t, servers, epochSize)

	activeAfterBInactive, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)

	delegatorAInfo, err = queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)

	require.Equal(t, 0, activeAfterBInactive.Cmp(delegatorAInfo.Amount))
	assertStakingConservation(t, servers[0], validator, knownStakers)

	beforeRaw, err = queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	beforeActive, err = queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)

	beforeDelegatorBInfo, err := queryStakerInfo(servers[0], validator, delegatorB)
	require.NoError(t, err)

	receipt, err = sendUnstakeDelegationTx(servers[0], delegatorB, delegatorBKey, validator)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	delegatorBInfo, err = queryStakerInfo(servers[0], validator, delegatorB)
	require.NoError(t, err)
	require.False(t, delegatorBInfo.Exists)
	require.False(t, validatorHasStaker(t, servers[0], validator, validator, delegatorB))

	afterRaw, err = queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	afterActive, err = queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)

	require.Equal(t, 0, afterRaw.Cmp(new(big.Int).Sub(beforeRaw, beforeDelegatorBInfo.Amount)))
	require.Equal(t, 0, afterActive.Cmp(beforeActive), "inactive full unstake must not change active delegated stake")

	assertStakingConservation(t, servers[0], validator, knownStakers)

	receipt, err = sendSetActiveReceiptTx(servers[0], validator, validatorKey, false)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	validatorInfo, err = queryStakerInfo(servers[0], validator, validator)
	require.NoError(t, err)
	require.False(t, validatorInfo.Active)

	assertStakingConservation(t, servers[0], validator, knownStakers)

	waitForNextEpochTransition(t, servers, epochSize)

	assertStakingConservation(t, servers[0], validator, knownStakers)

	withdrawValidator := big.NewInt(1)
	beforeValidatorInfo, err := queryStakerInfo(servers[0], validator, validator)
	require.NoError(t, err)

	receipt, err = sendWithdrawTx(servers[0], validator, validatorKey, withdrawValidator)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	afterValidatorInfo, err := queryStakerInfo(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, afterValidatorInfo.Amount.Cmp(new(big.Int).Sub(beforeValidatorInfo.Amount, withdrawValidator)))

	assertStakingConservation(t, servers[0], validator, knownStakers)

	waitForNextEpochTransition(t, servers, epochSize)

	receipt, err = framework.UnstakeAmount(validator, validatorKey, servers[0])
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	validatorInfo, err = queryStakerInfo(servers[0], validator, validator)
	require.NoError(t, err)
	require.False(t, validatorInfo.Exists)

	delegatorAInfo, err = queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	require.True(t, delegatorAInfo.Exists)
	require.Equal(t, validator, delegatorAInfo.Validator)

	assertStakingConservation(t, servers[0], validator, knownStakers)

	receipt, err = sendSetDelegationActiveReceiptTx(servers[0], delegatorA, delegatorAKey, validator, false)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	assertStakingConservation(t, servers[0], validator, knownStakers)

	waitForNextEpochTransition(t, servers, epochSize)

	assertStakingConservation(t, servers[0], validator, knownStakers)

	receipt, err = sendUnstakeDelegationTx(servers[0], delegatorA, delegatorAKey, validator)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	delegatorAInfo, err = queryStakerInfo(servers[0], validator, delegatorA)
	require.NoError(t, err)
	require.False(t, delegatorAInfo.Exists)

	assertStakingConservation(t, servers[0], validator, knownStakers)
}

func TestPoS_StakingV2_Delegator_DoubleWithdrawAndDoubleUnstakeRejected(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, 5, 1, 4)

	validator := addrs[0]
	validatorKey := keys[0]
	knownStakers := append([]types.Address(nil), addrs...)

	delegatorMinStake, err := queryDelegatorMinStake(servers[0], validator)
	require.NoError(t, err)

	require.NoError(t, sendSetValidatorPoolConfigTx(servers[0], validator, validatorKey, true, new(big.Int).Mul(delegatorMinStake, big.NewInt(100)), 0))

	delegatorKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegator := crypto.PubKeyToAddress(&delegatorKey.PublicKey)
	knownStakers = append(knownStakers, delegator)

	delegationAmount := new(big.Int).Mul(delegatorMinStake, big.NewInt(3))
	fundAccount(t, servers[0], validator, validatorKey, delegator, new(big.Int).Mul(delegatorMinStake, big.NewInt(6)))

	receipt, err := sendDelegateTx(servers[0], delegator, delegatorKey, validator, delegationAmount)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	waitForNextEpochTransition(t, servers, 5)

	receipt, err = sendSetDelegationActiveReceiptTx(servers[0], delegator, delegatorKey, validator, false)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	waitForNextEpochTransition(t, servers, 5)

	_, _ = stakingEconomySnapshot(t, servers[0], validator, knownStakers)

	delegatorInfoBeforeWithdraw, err := queryStakerInfo(servers[0], validator, delegator)
	require.NoError(t, err)
	require.True(t, delegatorInfoBeforeWithdraw.Exists)
	require.False(t, delegatorInfoBeforeWithdraw.Active)
	require.Greater(t, delegatorInfoBeforeWithdraw.Amount.Cmp(delegatorMinStake), 0, "delegator amount must allow a partial withdraw and invalid below-minimum withdraw checks")

	rawBeforeWithdraw, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	activeBeforeWithdraw, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)

	assertStakingConservation(t, servers[0], validator, knownStakers)

	withdrawAmount := big.NewInt(1)
	receipt, err = sendWithdrawDelegationReceiptTx(servers[0], delegator, delegatorKey, validator, withdrawAmount)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	delegatorInfoAfterWithdraw, err := queryStakerInfo(servers[0], validator, delegator)
	require.NoError(t, err)
	require.True(t, delegatorInfoAfterWithdraw.Exists)
	require.Equal(t, 0, delegatorInfoAfterWithdraw.Amount.Cmp(new(big.Int).Sub(delegatorInfoBeforeWithdraw.Amount, withdrawAmount)))

	rawAfterWithdraw, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	activeAfterWithdraw, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)

	require.Equal(t, 0, rawAfterWithdraw.Cmp(new(big.Int).Sub(rawBeforeWithdraw, withdrawAmount)))
	require.Equal(t, 0, activeAfterWithdraw.Cmp(activeBeforeWithdraw), "inactive delegator withdraw must not change active delegated stake")

	assertStakingConservation(t, servers[0], validator, knownStakers)

	assertRejectedDelegationMutation := func(amount *big.Int, msg string) {
		t.Helper()

		delegatorInfoBefore, infoErr := queryStakerInfo(servers[0], validator, delegator)
		require.NoError(t, infoErr)

		beforeRaw, rawErr := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
		require.NoError(t, rawErr)

		beforeActive, activeErr := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
		require.NoError(t, activeErr)

		receipt, txErr := sendWithdrawDelegationReceiptTx(servers[0], delegator, delegatorKey, validator, amount)
		require.NoError(t, txErr)
		require.Equal(t, uint64(types.ReceiptFailed), receipt.Status, msg)

		delegatorInfoAfter, infoErr := queryStakerInfo(servers[0], validator, delegator)
		require.NoError(t, infoErr)
		require.Equal(t, delegatorInfoBefore.Exists, delegatorInfoAfter.Exists, msg)
		require.Equal(t, 0, delegatorInfoAfter.Amount.Cmp(delegatorInfoBefore.Amount), msg)

		afterRaw, rawErr := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
		require.NoError(t, rawErr)

		afterActive, activeErr := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
		require.NoError(t, activeErr)

		require.Equal(t, 0, afterRaw.Cmp(beforeRaw), msg)
		require.Equal(t, 0, afterActive.Cmp(beforeActive), msg)
	}

	remainingAfterWithdraw := new(big.Int).Set(delegatorInfoAfterWithdraw.Amount)

	assertRejectedDelegationMutation(big.NewInt(0), "zero delegation withdraw must be rejected without mutating staking accounting")
	assertRejectedDelegationMutation(new(big.Int).Set(remainingAfterWithdraw), "full delegation withdraw must be rejected without mutating staking accounting")

	withdrawBelowMin := new(big.Int).Sub(remainingAfterWithdraw, new(big.Int).Sub(delegatorMinStake, big.NewInt(1)))
	assertRejectedDelegationMutation(withdrawBelowMin, "delegation withdraw leaving below the minimum must be rejected without mutating staking accounting")

	beforeUnstakeInfo, err := queryStakerInfo(servers[0], validator, delegator)
	require.NoError(t, err)
	require.True(t, beforeUnstakeInfo.Exists)

	beforeUnstakeRaw, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	beforeUnstakeActive, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)

	receipt, err = sendUnstakeDelegationTx(servers[0], delegator, delegatorKey, validator)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	delegatorInfoAfterUnstake, err := queryStakerInfo(servers[0], validator, delegator)
	require.NoError(t, err)
	require.False(t, delegatorInfoAfterUnstake.Exists)
	require.False(t, validatorHasStaker(t, servers[0], validator, validator, delegator))

	afterUnstakeRaw, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	afterUnstakeActive, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)

	require.Equal(t, 0, afterUnstakeRaw.Cmp(new(big.Int).Sub(beforeUnstakeRaw, beforeUnstakeInfo.Amount)))
	require.Equal(t, 0, afterUnstakeActive.Cmp(beforeUnstakeActive), "inactive full unstake must not change active delegated stake")

	assertStakingConservation(t, servers[0], validator, knownStakers)

	balanceBeforeDoubleUnstake, _ := stakingEconomySnapshot(t, servers[0], validator, knownStakers)

	rawBeforeDoubleUnstake, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	activeBeforeDoubleUnstake, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)

	receipt, err = sendUnstakeDelegationTx(servers[0], delegator, delegatorKey, validator)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status, "double unstakeDelegation must be rejected")

	balanceAfterDoubleUnstake, _ := stakingEconomySnapshot(t, servers[0], validator, knownStakers)
	require.GreaterOrEqual(t, balanceAfterDoubleUnstake.Cmp(balanceBeforeDoubleUnstake), 0, "double unstakeDelegation must not debit the staking contract balance")

	rawAfterDoubleUnstake, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	activeAfterDoubleUnstake, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)

	require.Equal(t, 0, rawAfterDoubleUnstake.Cmp(rawBeforeDoubleUnstake))
	require.Equal(t, 0, activeAfterDoubleUnstake.Cmp(activeBeforeDoubleUnstake))

	delegatorInfoAfterDoubleUnstake, err := queryStakerInfo(servers[0], validator, delegator)
	require.NoError(t, err)
	require.False(t, delegatorInfoAfterDoubleUnstake.Exists)
	require.False(t, validatorHasStaker(t, servers[0], validator, validator, delegator))

	assertStakingConservation(t, servers[0], validator, knownStakers)
}

func TestPoS_StakingV2_Validator_DoubleWithdrawAndDoubleUnstakeRejected(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, 5, 1, 4)

	validator := addrs[0]
	validatorKey := keys[0]
	knownStakers := append([]types.Address(nil), addrs...)

	minSelfStake, err := queryValidatorMinSelfStake(servers[0], validator)
	require.NoError(t, err)

	validatorInfoBeforeDeactivate, err := queryStakerInfo(servers[0], validator, validator)
	require.NoError(t, err)
	require.True(t, validatorInfoBeforeDeactivate.Exists)
	require.Greater(t, validatorInfoBeforeDeactivate.Amount.Cmp(minSelfStake), 0, "setup validator stake must allow a partial withdraw while staying above the minimum")

	receipt, err := sendSetActiveReceiptTx(servers[0], validator, validatorKey, false)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	waitForNextEpochTransition(t, servers, 5)

	_, _ = stakingEconomySnapshot(t, servers[0], validator, knownStakers)

	validatorInfoBeforeWithdraw, err := queryStakerInfo(servers[0], validator, validator)
	require.NoError(t, err)
	require.True(t, validatorInfoBeforeWithdraw.Exists)
	require.False(t, validatorInfoBeforeWithdraw.Active)

	assertStakingConservation(t, servers[0], validator, knownStakers)

	withdrawAmount := big.NewInt(1)
	receipt, err = sendWithdrawTx(servers[0], validator, validatorKey, withdrawAmount)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	validatorInfoAfterWithdraw, err := queryStakerInfo(servers[0], validator, validator)
	require.NoError(t, err)
	require.True(t, validatorInfoAfterWithdraw.Exists)
	require.Equal(t, 0, validatorInfoAfterWithdraw.Amount.Cmp(new(big.Int).Sub(validatorInfoBeforeWithdraw.Amount, withdrawAmount)))

	assertStakingConservation(t, servers[0], validator, knownStakers)

	remainingAfterWithdraw := new(big.Int).Set(validatorInfoAfterWithdraw.Amount)

	assertRejectedValidatorWithdraw := func(amount *big.Int, msg string) {
		t.Helper()

		validatorInfoBefore, infoErr := queryStakerInfo(servers[0], validator, validator)
		require.NoError(t, infoErr)

		receipt, txErr := sendWithdrawTx(servers[0], validator, validatorKey, amount)
		require.NoError(t, txErr)
		require.Equal(t, uint64(types.ReceiptFailed), receipt.Status, msg)

		validatorInfoAfter, infoErr := queryStakerInfo(servers[0], validator, validator)
		require.NoError(t, infoErr)
		require.Equal(t, validatorInfoBefore.Exists, validatorInfoAfter.Exists, msg)
		require.Equal(t, 0, validatorInfoAfter.Amount.Cmp(validatorInfoBefore.Amount), msg)

		assertStakingConservation(t, servers[0], validator, knownStakers)
	}

	assertRejectedValidatorWithdraw(big.NewInt(0), "zero validator withdraw must be rejected without mutating validator accounting")
	assertRejectedValidatorWithdraw(new(big.Int).Set(remainingAfterWithdraw), "full validator withdraw must be rejected without mutating validator accounting")

	withdrawBelowMin := new(big.Int).Sub(remainingAfterWithdraw, new(big.Int).Sub(minSelfStake, big.NewInt(1)))
	assertRejectedValidatorWithdraw(withdrawBelowMin, "validator withdraw leaving below the minimum must be rejected without mutating validator accounting")

	receipt, err = framework.UnstakeAmount(validator, validatorKey, servers[0])
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	validatorInfoAfterUnstake, err := queryStakerInfo(servers[0], validator, validator)
	require.NoError(t, err)
	require.False(t, validatorInfoAfterUnstake.Exists)

	validatorSet, err := framework.GetValidatorSet(validator, servers[0].JSONRPC())
	require.NoError(t, err)
	require.NotContains(t, validatorSet, validator)

	assertStakingConservation(t, servers[0], validator, knownStakers)

	balanceBeforeDoubleUnstake, _ := stakingEconomySnapshot(t, servers[0], validator, knownStakers)

	receipt, err = framework.UnstakeAmount(validator, validatorKey, servers[0])
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status, "double validator unstake must be rejected")

	balanceAfterDoubleUnstake, _ := stakingEconomySnapshot(t, servers[0], validator, knownStakers)
	require.GreaterOrEqual(t, balanceAfterDoubleUnstake.Cmp(balanceBeforeDoubleUnstake), 0, "double validator unstake must not debit the staking contract balance")

	validatorInfoAfterDoubleUnstake, err := queryStakerInfo(servers[0], validator, validator)
	require.NoError(t, err)
	require.False(t, validatorInfoAfterDoubleUnstake.Exists)

	assertStakingConservation(t, servers[0], validator, knownStakers)
}

func TestPoS_StakingV2_ValidatorExit_DoesNotRemoveDelegatorPosition(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, 5, 1, 4)

	minSelfStake, err := queryValidatorMinSelfStake(servers[0], addrs[0])
	require.NoError(t, err)
	delegatorMinStake, err := queryDelegatorMinStake(servers[0], addrs[0])
	require.NoError(t, err)

	receipt, err := sendStakeTx(servers[0], addrs[0], keys[0], minSelfStake)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	require.NoError(t, sendSetValidatorPoolConfigTx(servers[0], addrs[0], keys[0], true, new(big.Int).Mul(delegatorMinStake, big.NewInt(100)), 0))

	delegatorKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegatorAddr := crypto.PubKeyToAddress(&delegatorKey.PublicKey)
	fundAccount(t, servers[0], addrs[0], keys[0], delegatorAddr, new(big.Int).Mul(delegatorMinStake, big.NewInt(4)))

	receipt, err = sendDelegateTx(servers[0], delegatorAddr, delegatorKey, addrs[0], new(big.Int).Mul(delegatorMinStake, big.NewInt(2)))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	waitForNextEpochTransition(t, servers, 5)

	stakerCountBeforeExit, err := queryValidatorStakerCount(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.Greater(t, stakerCountBeforeExit, uint64(0))
	require.True(t, validatorHasStaker(t, servers[0], addrs[1], addrs[0], delegatorAddr))

	require.NoError(t, sendSetActiveTx(servers[0], addrs[0], keys[0], false))

	waitForNextEpochTransition(t, servers, 5)

	receipt, err = framework.UnstakeAmount(addrs[0], keys[0], servers[0])
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	waitForNextEpochTransition(t, servers, 5)

	validatorInfo, err := queryValidatorInfo(servers[0], addrs[1], addrs[0])
	require.NoError(t, err)
	require.False(t, validatorInfo.Exists)

	selfStakerInfo, err := queryStakerInfo(servers[0], addrs[1], addrs[0])
	require.NoError(t, err)
	require.False(t, selfStakerInfo.Exists)

	delegatorInfo, err := queryStakerInfo(servers[0], addrs[1], delegatorAddr)
	require.NoError(t, err)
	require.True(t, delegatorInfo.Exists)

	stakerCountAfterExit, err := queryValidatorStakerCount(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.Greater(t, stakerCountAfterExit, uint64(0))
	require.True(t, validatorHasStaker(t, servers[0], addrs[1], addrs[0], delegatorAddr))

	require.NoError(t, sendSetDelegationActiveTx(servers[0], delegatorAddr, delegatorKey, addrs[0], false))

	waitForNextEpochTransition(t, servers, 5)

	receipt, err = sendWithdrawDelegationReceiptTx(servers[0], delegatorAddr, delegatorKey, addrs[0], big.NewInt(1))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	receipt, err = sendUnstakeDelegationTx(servers[0], delegatorAddr, delegatorKey, addrs[0])
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)
}

func TestPoS_StakingV2_DelegationAfterValidatorRejoin_Invariants(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, 5, 1, 4)

	validatorAddr := addrs[0]
	validatorKey := keys[0]

	validatorThreshold, err := queryValidatorThreshold(servers[0], addrs[0])
	require.NoError(t, err)
	delegatorMinStake, err := queryDelegatorMinStake(servers[0], addrs[0])
	require.NoError(t, err)

	require.NoError(t, sendSetValidatorPoolConfigTx(servers[0], validatorAddr, validatorKey, true, new(big.Int).Mul(delegatorMinStake, big.NewInt(100)), 0))

	assertValidatorAbsent := func() {
		t.Helper()

		validatorInfo, infoErr := queryValidatorInfo(servers[0], addrs[0], validatorAddr)
		require.NoError(t, infoErr)
		require.False(t, validatorInfo.Exists)

		validatorSet, setErr := framework.GetValidatorSet(addrs[0], servers[0].JSONRPC())
		require.NoError(t, setErr)
		require.NotContains(t, validatorSet, validatorAddr)
		require.NotContains(t, mustGetLatestSnapshotValidators(t, servers[0]), validatorAddr)
	}

	delegatorKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegatorAddr := crypto.PubKeyToAddress(&delegatorKey.PublicKey)
	fundAccount(t, servers[0], addrs[0], keys[0], delegatorAddr, new(big.Int).Mul(delegatorMinStake, big.NewInt(20)))

	receipt, err := sendDelegateTx(servers[0], delegatorAddr, delegatorKey, validatorAddr, new(big.Int).Mul(delegatorMinStake, big.NewInt(3)))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	waitForNextEpochTransition(t, servers, 5)

	delegatorInfoBeforeExit, err := queryStakerInfo(servers[0], addrs[0], delegatorAddr)
	require.NoError(t, err)
	require.True(t, delegatorInfoBeforeExit.Exists)
	require.Equal(t, validatorAddr, delegatorInfoBeforeExit.Validator)

	require.NoError(t, sendSetActiveTx(servers[0], validatorAddr, validatorKey, false))

	waitForNextEpochTransition(t, servers, 5)

	receipt, err = framework.UnstakeAmount(validatorAddr, validatorKey, servers[0])
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	waitForNextEpochTransition(t, servers, 5)

	assertValidatorAbsent()

	receipt, err = sendSetDelegationActiveReceiptTx(servers[0], delegatorAddr, delegatorKey, validatorAddr, false)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)
	assertValidatorAbsent()

	receipt, err = sendSetDelegationActiveReceiptTx(servers[0], delegatorAddr, delegatorKey, validatorAddr, true)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)
	assertValidatorAbsent()

	receipt, err = sendSetDelegationActiveReceiptTx(servers[0], delegatorAddr, delegatorKey, validatorAddr, false)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)
	assertValidatorAbsent()

	waitForNextEpochTransition(t, servers, 5)

	receipt, err = sendWithdrawDelegationReceiptTx(servers[0], delegatorAddr, delegatorKey, validatorAddr, big.NewInt(1))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)
	assertValidatorAbsent()

	receipt, err = sendSetDelegationActiveReceiptTx(servers[0], delegatorAddr, delegatorKey, validatorAddr, true)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)
	assertValidatorAbsent()

	delegator2Key, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegator2Addr := crypto.PubKeyToAddress(&delegator2Key.PublicKey)
	fundAccount(t, servers[0], addrs[0], keys[0], delegator2Addr, new(big.Int).Mul(delegatorMinStake, big.NewInt(20)))

	receipt, err = sendDelegateTx(servers[0], delegator2Addr, delegator2Key, validatorAddr, new(big.Int).Mul(delegatorMinStake, big.NewInt(2)))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status)

	delegator2Info, err := queryStakerInfo(servers[0], addrs[0], delegator2Addr)
	require.NoError(t, err)
	require.False(t, delegator2Info.Exists)
	require.False(t, validatorHasStaker(t, servers[0], addrs[0], validatorAddr, delegator2Addr))

	assertValidatorAbsent()

	require.NotContains(t, mustGetLatestSnapshotValidators(t, servers[0]), validatorAddr)

	receipt, err = sendStakeTx(servers[0], validatorAddr, validatorKey, validatorThreshold)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	require.NotContains(t, mustGetLatestSnapshotValidators(t, servers[0]), validatorAddr)

	waitForNextEpochTransition(t, servers, 5)

	require.Contains(t, mustGetLatestSnapshotValidators(t, servers[0]), validatorAddr)

	delegatorInfoAfterRejoin, err := queryStakerInfo(servers[0], addrs[0], delegatorAddr)
	require.NoError(t, err)
	require.Equal(t, validatorAddr, delegatorInfoAfterRejoin.Validator)

	delegatedActiveAfterEpoch, err := queryValidatorDelegatedStakeActive(servers[0], addrs[0], validatorAddr)
	require.NoError(t, err)
	require.Equal(t, 0, delegatedActiveAfterEpoch.Cmp(delegatorInfoAfterRejoin.Amount), "active old delegation is only economically relevant again after valid rejoin + snapshot")

	receipt, err = sendSetDelegationActiveReceiptTx(servers[0], delegatorAddr, delegatorKey, validatorAddr, false)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	waitForNextEpochTransition(t, servers, 5)

	delegatedActiveAfterLegacyDeactivate, err := queryValidatorDelegatedStakeActive(servers[0], addrs[0], validatorAddr)
	require.NoError(t, err)
	require.Zero(t, delegatedActiveAfterLegacyDeactivate.Sign())

	poolAfterRejoin, err := queryValidatorPoolConfig(servers[0], addrs[0], validatorAddr)
	require.NoError(t, err)
	require.False(t, poolAfterRejoin.delegationEnabled)
	require.Zero(t, poolAfterRejoin.maxDelegatedStake.Sign())
	require.Zero(t, poolAfterRejoin.minDelegatorStake.Sign())
	require.Equal(t, uint16(0), poolAfterRejoin.commissionBps)

	receipt, err = sendDelegateTx(servers[0], delegator2Addr, delegator2Key, validatorAddr, new(big.Int).Mul(delegatorMinStake, big.NewInt(2)))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status)

	delegator2AfterClosedPool, err := queryStakerInfo(servers[0], addrs[0], delegator2Addr)
	require.NoError(t, err)
	require.False(t, delegator2AfterClosedPool.Exists)
	require.False(t, validatorHasStaker(t, servers[0], addrs[0], validatorAddr, delegator2Addr))

	require.NoError(t, sendSetValidatorPoolConfigTx(
		servers[0],
		validatorAddr,
		validatorKey,
		true,
		new(big.Int).Mul(delegatorMinStake, big.NewInt(100)),
		0,
	))

	receipt, err = sendDelegateTx(servers[0], delegator2Addr, delegator2Key, validatorAddr, new(big.Int).Mul(delegatorMinStake, big.NewInt(2)))
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	delegator2AfterReopen, err := queryStakerInfo(servers[0], addrs[0], delegator2Addr)
	require.NoError(t, err)
	require.True(t, delegator2AfterReopen.Exists)
	require.Equal(t, validatorAddr, delegator2AfterReopen.Validator)
	require.True(t, validatorHasStaker(t, servers[0], addrs[0], validatorAddr, delegator2Addr))

	waitForNextEpochTransition(t, servers, 5)

	receipt, err = sendUnstakeDelegationTx(servers[0], delegatorAddr, delegatorKey, validatorAddr)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	legacyAfterUnstake, err := queryStakerInfo(servers[0], addrs[0], delegatorAddr)
	require.NoError(t, err)
	require.False(t, legacyAfterUnstake.Exists)
	require.False(t, validatorHasStaker(t, servers[0], addrs[0], validatorAddr, delegatorAddr))

	delegatedActiveAfterLegacyUnstake, err := queryValidatorDelegatedStakeActive(servers[0], addrs[0], validatorAddr)
	require.NoError(t, err)
	require.Equal(t, 0, delegatedActiveAfterLegacyUnstake.Cmp(delegator2AfterReopen.Amount))
}

func TestPoS_StakingV2_Reentrancy_DelegatorWithdrawCannotDoubleSpend(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, 5, 1, 4)

	validator := addrs[0]
	validatorKey := keys[0]
	caller := addrs[1]
	callerKey := keys[1]
	knownStakers := append([]types.Address(nil), addrs...)

	delegatorMinStake, err := queryDelegatorMinStake(servers[0], validator)
	require.NoError(t, err)

	fundAccount(t, servers[0], validator, validatorKey, caller, new(big.Int).Mul(delegatorMinStake, big.NewInt(10)))
	require.NoError(t, sendSetValidatorPoolConfigTx(servers[0], validator, validatorKey, true, new(big.Int).Mul(delegatorMinStake, big.NewInt(100)), 0))

	maliciousDelegator := deployStakingV2ReentrantDelegator(t, servers[0], caller, callerKey)
	knownStakers = append(knownStakers, maliciousDelegator)

	delegationAmount := new(big.Int).Mul(delegatorMinStake, big.NewInt(3))
	receipt, err := sendStakingV2ReentrantDelegatorTx(servers[0], caller, callerKey, maliciousDelegator, "delegate", delegationAmount, map[string]interface{}{
		"validator_": ethgo.Address(validator),
	})
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	assertStakingConservation(t, servers[0], validator, knownStakers)

	waitForNextEpochTransition(t, servers, 5)

	receipt, err = sendStakingV2ReentrantDelegatorTx(servers[0], caller, callerKey, maliciousDelegator, "setDelegationActive", big.NewInt(0), map[string]interface{}{
		"validator_": ethgo.Address(validator),
		"active":     false,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	waitForNextEpochTransition(t, servers, 5)

	infoBefore, err := queryStakerInfo(servers[0], validator, maliciousDelegator)
	require.NoError(t, err)
	require.True(t, infoBefore.Exists)
	require.False(t, infoBefore.Active)

	rawBefore, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	activeBefore, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	countBefore, err := queryValidatorStakerCount(servers[0], validator, validator)
	require.NoError(t, err)

	require.True(t, validatorHasStaker(t, servers[0], validator, validator, maliciousDelegator))

	receipt, err = sendStakingV2ReentrantDelegatorTx(servers[0], caller, callerKey, maliciousDelegator, "configure", big.NewInt(0), map[string]interface{}{
		"mode_":      stakingV2ReentryModeWithdraw,
		"validator_": ethgo.Address(validator),
		"amount_":    big.NewInt(1),
	})
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	balanceImmediatelyBeforePayout, err := servers[0].JSONRPC().Eth().GetBalance(ethgo.Address(staking.AddrStakingContract), ethgo.Latest)
	require.NoError(t, err)

	maliciousBalanceBeforePayout, err := servers[0].JSONRPC().Eth().GetBalance(ethgo.Address(maliciousDelegator), ethgo.Latest)
	require.NoError(t, err)

	receipt, err = sendStakingV2ReentrantDelegatorTx(servers[0], caller, callerKey, maliciousDelegator, "withdrawDelegation", big.NewInt(0), map[string]interface{}{
		"validator_": ethgo.Address(validator),
		"amount_":    big.NewInt(1),
	})
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	balanceAfter, err := servers[0].JSONRPC().Eth().GetBalance(ethgo.Address(staking.AddrStakingContract), ethgo.Latest)
	require.NoError(t, err)

	maliciousBalanceAfterPayout, err := servers[0].JSONRPC().Eth().GetBalance(ethgo.Address(maliciousDelegator), ethgo.Latest)
	require.NoError(t, err)

	attempted, err := queryStakingV2ReentrantDelegatorBool(servers[0], validator, maliciousDelegator, "attempted")
	require.NoError(t, err)
	require.True(t, attempted, "malicious fallback must attempt the configured reentrant withdraw")

	infoAfter, err := queryStakerInfo(servers[0], validator, maliciousDelegator)
	require.NoError(t, err)
	require.True(t, infoAfter.Exists)
	require.Equal(t, 0, infoAfter.Amount.Cmp(new(big.Int).Sub(infoBefore.Amount, big.NewInt(1))), "withdraw reentry must not withdraw a second unit")

	rawAfter, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, rawAfter.Cmp(new(big.Int).Sub(rawBefore, big.NewInt(1))))

	activeAfter, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, activeAfter.Cmp(activeBefore), "inactive delegator withdraw must not affect active delegated stake")

	countAfter, err := queryValidatorStakerCount(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, countBefore, countAfter, "partial withdraw must not create or remove staker-list entries")

	require.Equal(t, 0, new(big.Int).Sub(maliciousBalanceAfterPayout, maliciousBalanceBeforePayout).Cmp(big.NewInt(1)), "malicious receiver balance must increase only by the outer withdraw payout")
	require.Greater(t, balanceAfter.Cmp(new(big.Int).Sub(balanceImmediatelyBeforePayout, big.NewInt(2))), 0, "staking contract balance must not lose a second withdraw payout even if block rewards drift")
	require.True(t, validatorHasStaker(t, servers[0], validator, validator, maliciousDelegator))

	assertStakingConservation(t, servers[0], validator, knownStakers)
}

func TestPoS_StakingV2_Reentrancy_DelegatorUnstakeCannotDoubleSpend(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2Cluster(t, 4, 5, 1, 4)

	validator := addrs[0]
	validatorKey := keys[0]
	caller := addrs[1]
	callerKey := keys[1]
	knownStakers := append([]types.Address(nil), addrs...)

	delegatorMinStake, err := queryDelegatorMinStake(servers[0], validator)
	require.NoError(t, err)

	fundAccount(t, servers[0], validator, validatorKey, caller, new(big.Int).Mul(delegatorMinStake, big.NewInt(10)))
	require.NoError(t, sendSetValidatorPoolConfigTx(servers[0], validator, validatorKey, true, new(big.Int).Mul(delegatorMinStake, big.NewInt(100)), 0))

	maliciousDelegator := deployStakingV2ReentrantDelegator(t, servers[0], caller, callerKey)
	knownStakers = append(knownStakers, maliciousDelegator)

	delegationAmount := new(big.Int).Mul(delegatorMinStake, big.NewInt(3))
	receipt, err := sendStakingV2ReentrantDelegatorTx(servers[0], caller, callerKey, maliciousDelegator, "delegate", delegationAmount, map[string]interface{}{
		"validator_": ethgo.Address(validator),
	})
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	assertStakingConservation(t, servers[0], validator, knownStakers)

	waitForNextEpochTransition(t, servers, 5)

	receipt, err = sendStakingV2ReentrantDelegatorTx(servers[0], caller, callerKey, maliciousDelegator, "setDelegationActive", big.NewInt(0), map[string]interface{}{
		"validator_": ethgo.Address(validator),
		"active":     false,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	waitForNextEpochTransition(t, servers, 5)

	infoBefore, err := queryStakerInfo(servers[0], validator, maliciousDelegator)
	require.NoError(t, err)
	require.True(t, infoBefore.Exists)
	require.False(t, infoBefore.Active)

	rawBefore, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	activeBefore, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)

	require.True(t, validatorHasStaker(t, servers[0], validator, validator, maliciousDelegator))

	receipt, err = sendStakingV2ReentrantDelegatorTx(servers[0], caller, callerKey, maliciousDelegator, "configure", big.NewInt(0), map[string]interface{}{
		"mode_":      stakingV2ReentryModeUnstake,
		"validator_": ethgo.Address(validator),
		"amount_":    big.NewInt(0),
	})
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	balanceImmediatelyBeforePayout, err := servers[0].JSONRPC().Eth().GetBalance(ethgo.Address(staking.AddrStakingContract), ethgo.Latest)
	require.NoError(t, err)

	maliciousBalanceBeforePayout, err := servers[0].JSONRPC().Eth().GetBalance(ethgo.Address(maliciousDelegator), ethgo.Latest)
	require.NoError(t, err)

	receipt, err = sendStakingV2ReentrantDelegatorTx(servers[0], caller, callerKey, maliciousDelegator, "unstakeDelegation", big.NewInt(0), map[string]interface{}{
		"validator_": ethgo.Address(validator),
	})
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)

	balanceAfter, err := servers[0].JSONRPC().Eth().GetBalance(ethgo.Address(staking.AddrStakingContract), ethgo.Latest)
	require.NoError(t, err)

	maliciousBalanceAfterPayout, err := servers[0].JSONRPC().Eth().GetBalance(ethgo.Address(maliciousDelegator), ethgo.Latest)
	require.NoError(t, err)

	attempted, err := queryStakingV2ReentrantDelegatorBool(servers[0], validator, maliciousDelegator, "attempted")
	require.NoError(t, err)
	require.True(t, attempted, "malicious fallback must attempt the configured reentrant unstake")

	infoAfter, err := queryStakerInfo(servers[0], validator, maliciousDelegator)
	require.NoError(t, err)
	require.False(t, infoAfter.Exists)
	require.False(t, validatorHasStaker(t, servers[0], validator, validator, maliciousDelegator))

	rawAfter, err := queryValidatorDelegatedStakeRaw(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, rawAfter.Cmp(new(big.Int).Sub(rawBefore, infoBefore.Amount)), "raw delegated stake must drop by exactly the exited delegation")

	activeAfter, err := queryValidatorDelegatedStakeActive(servers[0], validator, validator)
	require.NoError(t, err)
	require.Equal(t, 0, activeAfter.Cmp(activeBefore), "inactive delegator unstake must not affect active delegated stake")

	require.Equal(t, 0, new(big.Int).Sub(maliciousBalanceAfterPayout, maliciousBalanceBeforePayout).Cmp(infoBefore.Amount), "malicious receiver balance must increase only by the outer unstake payout")
	require.Greater(t, balanceAfter.Cmp(new(big.Int).Sub(balanceImmediatelyBeforePayout, new(big.Int).Mul(infoBefore.Amount, big.NewInt(2)))), 0, "staking contract balance must not lose a second unstake payout even if block rewards drift")

	assertStakingConservation(t, servers[0], validator, knownStakers)
}

func assertStakingConservation(t *testing.T, srv *framework.TestServer, queryFrom types.Address, knownStakers []types.Address) {
	t.Helper()

	contractBalance, amounts := stakingEconomySnapshot(t, srv, queryFrom, knownStakers)

	summedExistingStakerAmounts := big.NewInt(0)
	for _, amount := range amounts {
		summedExistingStakerAmounts.Add(summedExistingStakerAmounts, amount)
	}

	require.Equal(t, 0, contractBalance.Cmp(summedExistingStakerAmounts), "staking contract native balance must equal summed stakerInfo.amount for all existing known stakers")
}

func assertRejectedStakingTxDoesNotChangeKnownStakes(
	t *testing.T,
	srv *framework.TestServer,
	queryFrom types.Address,
	knownStakers []types.Address,
	epochSize uint64,
	sendTx func() (*ethgo.Receipt, error),
	msg string,
) {
	t.Helper()

	for attempt := 0; attempt < 2; attempt++ {
		waitUntilMidEpoch(t, srv, epochSize)

		beforeWindow := rejectedTxEpochWindowInfo(t, srv, epochSize)
		balanceBefore, amountsBefore := stakingEconomySnapshotAt(t, srv, queryFrom, knownStakers, beforeWindow.blockNumber)

		receipt, err := sendTx()
		require.NoError(t, err)
		require.Equal(t, uint64(types.ReceiptFailed), receipt.Status, msg)

		afterWindow := rejectedTxEpochWindowInfo(t, srv, epochSize)
		balanceAfter, amountsAfter := stakingEconomySnapshotAt(t, srv, queryFrom, knownStakers, afterWindow.blockNumber)

		if beforeWindow.epoch != afterWindow.epoch {
			if attempt == 0 {
				t.Logf(
					"retrying rejected-tx invariant check after epoch finalized during window: beforeBlock=%d beforeEpoch=%d afterBlock=%d afterEpoch=%d balanceBefore=%s balanceAfter=%s",
					beforeWindow.blockNumber,
					beforeWindow.epoch,
					afterWindow.blockNumber,
					afterWindow.epoch,
					balanceBefore.String(),
					balanceAfter.String(),
				)
				continue
			}

			require.Failf(
				t,
				"test invalid: epoch finalized during rejected-tx invariant window",
				"beforeBlock=%d beforeEpoch=%d afterBlock=%d afterEpoch=%d balanceBefore=%s balanceAfter=%s",
				beforeWindow.blockNumber,
				beforeWindow.epoch,
				afterWindow.blockNumber,
				afterWindow.epoch,
				balanceBefore.String(),
				balanceAfter.String(),
			)
		}

		require.GreaterOrEqual(t, balanceAfter.Cmp(balanceBefore), 0, msg)
		require.Len(t, amountsAfter, len(amountsBefore), msg)

		for staker, amountBefore := range amountsBefore {
			amountAfter, ok := amountsAfter[staker]
			require.True(t, ok, msg)
			require.Equal(t, 0, amountAfter.Cmp(amountBefore), msg)
		}

		assertStakingConservation(t, srv, queryFrom, knownStakers)

		return
	}
}

type rejectedTxEpochWindow struct {
	blockNumber uint64
	epoch       uint64
}

func rejectedTxEpochWindowInfo(t *testing.T, srv *framework.TestServer, epochSize uint64) rejectedTxEpochWindow {
	t.Helper()

	blockNumber, err := srv.JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)

	return rejectedTxEpochWindow{
		blockNumber: blockNumber,
		epoch:       blockNumber / epochSize,
	}
}

func stakingEconomySnapshot(t *testing.T, srv *framework.TestServer, queryFrom types.Address, knownStakers []types.Address) (*big.Int, map[types.Address]*big.Int) {
	t.Helper()

	blockNumber, err := srv.JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)

	return stakingEconomySnapshotAt(t, srv, queryFrom, knownStakers, blockNumber)
}

func stakingEconomySnapshotAt(t *testing.T, srv *framework.TestServer, queryFrom types.Address, knownStakers []types.Address, blockNumber uint64) (*big.Int, map[types.Address]*big.Int) {
	t.Helper()

	block := ethgo.BlockNumber(blockNumber)

	contractBalance, err := srv.JSONRPC().Eth().GetBalance(ethgo.Address(staking.AddrStakingContract), block)
	require.NoError(t, err)

	amounts := make(map[types.Address]*big.Int, len(knownStakers))
	for _, staker := range knownStakers {
		if _, ok := amounts[staker]; ok {
			continue
		}

		info, infoErr := queryStakerInfoAt(srv, queryFrom, staker, block)
		require.NoError(t, infoErr)

		if info.Exists {
			amounts[staker] = new(big.Int).Set(info.Amount)
		} else {
			amounts[staker] = big.NewInt(0)
		}
	}

	return new(big.Int).Set(contractBalance), amounts
}

func sendSetActiveReceiptTx(srv *framework.TestServer, from types.Address, key *ecdsa.PrivateKey, value bool) (*ethgo.Receipt, error) {
	m := abis.StakingABI.Methods["setActive"]
	if m == nil {
		return nil, errors.New("staking ABI missing setActive")
	}

	inp, err := m.Inputs.Encode(map[string]interface{}{"active_": value})
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

func sendStakeTx(srv *framework.TestServer, from types.Address, key *ecdsa.PrivateKey, amount *big.Int) (*ethgo.Receipt, error) {
	m := abis.StakingABI.Methods["stake"]
	if m == nil {
		return nil, errors.New("staking ABI missing stake")
	}

	ctx, cancel := context.WithTimeout(context.Background(), framework.DefaultTimeout)
	defer cancel()

	return srv.SendRawTx(ctx, &framework.PreparedTransaction{
		From:     from,
		To:       &staking.AddrStakingContract,
		GasPrice: framework.TestGasPrice(),
		Gas:      framework.DefaultGasLimit,
		Value:    amount,
		Input:    m.ID(),
	}, key)
}

func sendSetDelegationActiveTx(srv *framework.TestServer, from types.Address, key *ecdsa.PrivateKey, validator types.Address, active bool) error {
	receipt, err := sendSetDelegationActiveReceiptTx(srv, from, key, validator, active)
	if err != nil {
		return err
	}
	if receipt.Status != uint64(types.ReceiptSuccess) {
		return errors.New("setDelegationActive transaction failed")
	}

	return nil
}

func sendSetDelegationActiveReceiptTx(srv *framework.TestServer, from types.Address, key *ecdsa.PrivateKey, validator types.Address, active bool) (*ethgo.Receipt, error) {
	m := abis.StakingABI.Methods["setDelegationActive"]
	if m == nil {
		return nil, errors.New("staking ABI missing setDelegationActive")
	}

	inp, err := m.Inputs.Encode(map[string]interface{}{
		"validator": ethgo.Address(validator),
		"active_":  active,
	})
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

func sendWithdrawDelegationTx(srv *framework.TestServer, from types.Address, key *ecdsa.PrivateKey, validator types.Address, amount *big.Int) error {
	receipt, err := sendWithdrawDelegationReceiptTx(srv, from, key, validator, amount)
	if err != nil {
		return err
	}
	if receipt.Status != uint64(types.ReceiptSuccess) {
		return errors.New("withdrawDelegation transaction failed")
	}

	return nil
}

func sendWithdrawDelegationReceiptTx(srv *framework.TestServer, from types.Address, key *ecdsa.PrivateKey, validator types.Address, amount *big.Int) (*ethgo.Receipt, error) {
	m := abis.StakingABI.Methods["withdrawDelegation"]
	if m == nil {
		return nil, errors.New("staking ABI missing withdrawDelegation")
	}

	inp, err := m.Inputs.Encode(map[string]interface{}{
		"validator": ethgo.Address(validator),
		"amount":    amount,
	})
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

func sendUnstakeDelegationTx(srv *framework.TestServer, from types.Address, key *ecdsa.PrivateKey, validator types.Address) (*ethgo.Receipt, error) {
	m := abis.StakingABI.Methods["unstakeDelegation"]
	if m == nil {
		return nil, errors.New("staking ABI missing unstakeDelegation")
	}

	inp, err := m.Inputs.Encode(map[string]interface{}{"validator": ethgo.Address(validator)})
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

func queryStakerInfo(srv *framework.TestServer, from, staker types.Address) (*stakingStakerInfo, error) {
	return queryStakerInfoAt(srv, from, staker, ethgo.Latest)
}

func queryStakerInfoAt(srv *framework.TestServer, from, staker types.Address, block ethgo.BlockNumber) (*stakingStakerInfo, error) {
	m := abis.StakingABI.Methods["stakerInfo"]
	if m == nil {
		return nil, errors.New("staking ABI missing stakerInfo")
	}

	input, err := m.Inputs.Encode(map[string]interface{}{"account": ethgo.Address(staker)})
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
	}, block)
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

	root, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, errors.New("stakerInfo decode root")
	}

	tuple, ok := root["0"].(map[string]interface{})
	if !ok {
		return nil, errors.New("stakerInfo decode tuple")
	}

	out := &stakingStakerInfo{}
	out.Exists, _ = tuple["exists"].(bool)
	out.Active, _ = tuple["active"].(bool)
	out.Amount, _ = tuple["amount"].(*big.Int)
	out.JoinedAtBlock, _ = tuple["joinedAtBlock"].(*big.Int)
	out.DeactivatedAtBlock, _ = tuple["deactivatedAtBlock"].(*big.Int)

	if v, ok := tuple["validator"].(ethgo.Address); ok {
		out.Validator = types.Address(v)
	}
	if out.Amount == nil {
		out.Amount = big.NewInt(0)
	}
	if out.JoinedAtBlock == nil {
		out.JoinedAtBlock = big.NewInt(0)
	}
	if out.DeactivatedAtBlock == nil {
		out.DeactivatedAtBlock = big.NewInt(0)
	}

	return out, nil
}

func queryValidatorDelegatedStakeRaw(srv *framework.TestServer, from, validator types.Address) (*big.Int, error) {
	return queryUintMethodByValidator(srv, from, validator, "validatorDelegatedStakeRaw")
}

func queryValidatorDelegatedStakeActive(srv *framework.TestServer, from, validator types.Address) (*big.Int, error) {
	return queryUintMethodByValidator(srv, from, validator, "validatorDelegatedStakeActive")
}

func queryValidatorStakerCount(srv *framework.TestServer, from, validator types.Address) (uint64, error) {
	m := abis.StakingABI.Methods["validatorStakerCount"]
	if m == nil {
		return 0, errors.New("staking ABI missing validatorStakerCount")
	}

	input, err := m.Inputs.Encode(map[string]interface{}{"validator": ethgo.Address(validator)})
	if err != nil {
		return 0, err
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
		return 0, err
	}

	return common.ParseUint64orHex(&resp)
}

func queryValidatorStakerAt(srv *framework.TestServer, from, validator types.Address, idx uint64) (types.Address, error) {
	m := abis.StakingABI.Methods["validatorStakerAt"]
	if m == nil {
		return types.ZeroAddress, errors.New("staking ABI missing validatorStakerAt")
	}

	input, err := m.Inputs.Encode(map[string]interface{}{
		"validator": ethgo.Address(validator),
		"idx":       new(big.Int).SetUint64(idx),
	})
	if err != nil {
		return types.ZeroAddress, err
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
		return types.ZeroAddress, err
	}

	b, err := hex.DecodeHex(resp)
	if err != nil {
		return types.ZeroAddress, err
	}

	decoded, err := m.Outputs.Decode(b)
	if err != nil {
		return types.ZeroAddress, err
	}

	root, ok := decoded.(map[string]interface{})
	if !ok {
		return types.ZeroAddress, errors.New("validatorStakerAt decode root")
	}

	if val, ok := root["0"].(ethgo.Address); ok {
		return types.Address(val), nil
	}

	return types.ZeroAddress, errors.New("validatorStakerAt decode value")
}

func validatorHasStaker(t *testing.T, srv *framework.TestServer, from, validator, expectedStaker types.Address) bool {
	t.Helper()

	count, err := queryValidatorStakerCount(srv, from, validator)
	require.NoError(t, err)

	for i := uint64(0); i < count; i++ {
		staker, sErr := queryValidatorStakerAt(srv, from, validator, i)
		require.NoError(t, sErr)

		if staker == expectedStaker {
			return true
		}
	}

	return false
}

func waitUntilMidEpoch(t *testing.T, srv *framework.TestServer, epochSize uint64) {
	t.Helper()

	for {
		height, err := srv.JSONRPC().Eth().BlockNumber()
		require.NoError(t, err)

		pos := height % epochSize
		if epochSize <= 3 {
			if pos != 0 {
				return
			}
		} else if pos >= 1 && pos <= epochSize-3 {
			return
		}

		require.Empty(t, framework.WaitForServersToSeal([]*framework.TestServer{srv}, height+1))
	}
}

func queryUintMethodByValidator(srv *framework.TestServer, from, validator types.Address, method string) (*big.Int, error) {
	m := abis.StakingABI.Methods[method]
	if m == nil {
		return nil, errors.New("staking ABI missing " + method)
	}

	input, err := m.Inputs.Encode(map[string]interface{}{"validator": ethgo.Address(validator)})
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

	return common.ParseUint256orHex(&resp)
}
