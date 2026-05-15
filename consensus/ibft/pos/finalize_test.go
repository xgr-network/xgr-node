package pos

import (
	"math/big"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/contracts/staking"
	stakingHelper "github.com/xgr-network/xgr-node/helper/staking"
	"github.com/xgr-network/xgr-node/state"
	itrie "github.com/xgr-network/xgr-node/state/immutable-trie"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func TestSlashByEffectiveStake_DistributesRemainderBySortedAddress(t *testing.T) {
	t.Parallel()

	validator := types.StringToAddress("0x00000000000000000000000000000000000000f3")
	delegatorHigh := types.StringToAddress("0x00000000000000000000000000000000000000f2")
	delegatorLow := types.StringToAddress("0x00000000000000000000000000000000000000f1")
	donation := types.StringToAddress("0x2006")
	txn := newPosTestTransitionWithStaking(t, validator)
	epoch := uint64(2)

	setStakerForEpoch(t, txn, epoch, validator, 1, 101)
	setStakerForEpoch(t, txn, epoch, delegatorHigh, 1, 101)
	setStakerForEpoch(t, txn, epoch, delegatorLow, 1, 101)
	appendValidatorStaker(txn, validator, delegatorHigh)
	appendValidatorStaker(txn, validator, delegatorLow)
	addValidatorDelegatedAggregate(txn, validator, big.NewInt(101), true)
	addValidatorDelegatedAggregate(txn, validator, big.NewInt(101), true)
	txn.Txn().SetBalance(staking.AddrStakingContract, big.NewInt(303))

	before := map[types.Address]*big.Int{
		validator:     new(big.Int).Set(readStakedAmount(txn, validator)),
		delegatorHigh: new(big.Int).Set(readStakedAmount(txn, delegatorHigh)),
		delegatorLow:  new(big.Int).Set(readStakedAmount(txn, delegatorLow)),
	}

	slashEvent, err := slashByEffectiveStakeFromStakingContract(txn, epoch, validator, donation, big.NewInt(500), 10, 10, 100, 20, 10)
	require.NoError(t, err)
	require.NotNil(t, slashEvent)

	allocated := map[types.Address]*big.Int{}
	totalAllocated := big.NewInt(0)
	for addr, stakeBefore := range before {
		allocation := new(big.Int).Sub(stakeBefore, readStakedAmount(txn, addr))
		allocated[addr] = allocation
		totalAllocated.Add(totalAllocated, allocation)
		require.LessOrEqual(t, allocation.Cmp(stakeBefore), 0)
	}

	require.Equal(t, int64(5), totalAllocated.Int64())
	require.Equal(t, int64(5), txn.Txn().GetBalance(donation).Int64())
	require.Equal(t, int64(2), allocated[delegatorLow].Int64(), "lowest address receives first remainder wei")
	require.Equal(t, int64(2), allocated[delegatorHigh].Int64(), "second-lowest address receives second remainder wei")
	require.Equal(t, int64(1), allocated[validator].Int64(), "highest address does not receive remainder before lower addresses")
}

func TestSetValidatorInactiveInStaking_ClearsPoolConfigActiveAndIsIdempotent(t *testing.T) {
	t.Parallel()

	validator := types.StringToAddress("0x1006")
	txn := newPosTestTransitionWithStaking(t, validator)
	poolKey := stakingPoolConfigPackedKey(validator)
	poolPacked := new(big.Int).SetUint64(0x0101 | 1<<16)
	txn.Txn().SetState(staking.AddrStakingContract, poolKey, bigToHash(poolPacked))
	poolBase := new(big.Int).SetBytes(poolKey.Bytes())
	maxKey := types.BytesToHash(new(big.Int).Add(new(big.Int).Set(poolBase), big.NewInt(2)).Bytes())
	minKey := types.BytesToHash(new(big.Int).Add(new(big.Int).Set(poolBase), big.NewInt(3)).Bytes())
	commissionKey := types.BytesToHash(new(big.Int).Add(new(big.Int).Set(poolBase), big.NewInt(4)).Bytes())
	txn.Txn().SetState(staking.AddrStakingContract, maxKey, bigToHash(big.NewInt(123)))
	txn.Txn().SetState(staking.AddrStakingContract, minKey, bigToHash(big.NewInt(456)))
	txn.Txn().SetState(staking.AddrStakingContract, commissionKey, bigToHash(big.NewInt(789)))
	maxBefore := txn.Txn().GetState(staking.AddrStakingContract, maxKey)
	minBefore := txn.Txn().GetState(staking.AddrStakingContract, minKey)
	commissionBefore := txn.Txn().GetState(staking.AddrStakingContract, commissionKey)

	require.True(t, poolConfigActiveForTest(txn, validator))
	setValidatorInactiveInStaking(txn, validator, 100)

	packed := new(big.Int).SetBytes(txn.Txn().GetState(staking.AddrStakingContract, stakingMetaPackedKey(validator)).Bytes())
	require.False(t, isValidatorMetaActive(packed))
	require.False(t, poolConfigActiveForTest(txn, validator))
	require.True(t, poolConfigDelegationEnabledForTest(txn, validator))
	require.Equal(t, maxBefore, txn.Txn().GetState(staking.AddrStakingContract, maxKey))
	require.Equal(t, minBefore, txn.Txn().GetState(staking.AddrStakingContract, minKey))
	require.Equal(t, commissionBefore, txn.Txn().GetState(staking.AddrStakingContract, commissionKey))
	require.Equal(t, uint64(100), u64FromHash(txn.Txn().GetState(staking.AddrStakingContract, stakingDeactivatedAtBlockKey(validator))))

	setValidatorInactiveInStaking(txn, validator, 200)
	require.Equal(t, uint64(100), u64FromHash(txn.Txn().GetState(staking.AddrStakingContract, stakingDeactivatedAtBlockKey(validator))))
}

func TestPosSysAddrNormalUserCallCannotMutateInternalStorage(t *testing.T) {
	t.Parallel()

	user := types.StringToAddress("0x3001")
	st := itrie.NewState(itrie.NewMemoryStorage())
	ex := state.NewExecutor(&chain.Params{Forks: chain.AllForksEnabled, BurnContract: map[uint64]types.Address{0: types.ZeroAddress}}, st, hclog.NewNullLogger())
	root, err := ex.WriteGenesis(map[types.Address]*chain.GenesisAccount{user: {Balance: big.NewInt(1_000_000)}}, types.ZeroHash)
	require.NoError(t, err)
	ex.GetHash = func(h *types.Header) state.GetHashByNumber {
		return func(i uint64) types.Hash { return root }
	}
	txn, err := ex.BeginTxn(root, &types.Header{GasLimit: 100_000}, types.ZeroAddress)
	require.NoError(t, err)
	key := keyUptimeLastProcessedSlot()
	before := u64ToHash(777)
	txn.Txn().SetState(PosSysAddr, key, before)
	unknownKey := types.StringToHash("0x1234")

	tx := &types.Transaction{
		From:     user,
		To:       &PosSysAddr,
		Value:    big.NewInt(1),
		Gas:      50_000,
		GasPrice: big.NewInt(0),
		Input:    []byte{0x55, 0x00, 0x55},
	}
	_, err = txn.Apply(tx)
	require.NoError(t, err)

	require.Equal(t, before, txn.Txn().GetState(PosSysAddr, key))
	require.Equal(t, types.ZeroHash, txn.Txn().GetState(PosSysAddr, unknownKey))
	require.Equal(t, int64(1), txn.Txn().GetBalance(PosSysAddr).Int64())
}

func poolConfigActiveForTest(txn *state.Transition, validator types.Address) bool {
	packed := new(big.Int).SetBytes(txn.Txn().GetState(staking.AddrStakingContract, stakingPoolConfigPackedKey(validator)).Bytes())
	active := new(big.Int).Rsh(packed, 8)
	active.And(active, new(big.Int).SetUint64(0xff))
	return active.Sign() != 0
}

func poolConfigDelegationEnabledForTest(txn *state.Transition, validator types.Address) bool {
	packed := new(big.Int).SetBytes(txn.Txn().GetState(staking.AddrStakingContract, stakingPoolConfigPackedKey(validator)).Bytes())
	enabled := new(big.Int).Rsh(packed, 16)
	enabled.And(enabled, new(big.Int).SetUint64(0xff))
	return enabled.Sign() != 0
}

func TestSetValidatorInactiveInStaking_DoesNotRewriteDeactivatedAtBlockForAlreadyInactive(t *testing.T) {
	t.Parallel()

	validator := types.StringToAddress("0x1004")
	txn := newPosTestTransitionWithStaking(t, validator)

	setValidatorInactiveInStaking(txn, validator, 100)
	firstPackedState := txn.Txn().GetState(staking.AddrStakingContract, stakingMetaPackedKey(validator))
	firstPacked := new(big.Int).SetBytes(firstPackedState[:])
	require.False(t, isValidatorMetaActive(firstPacked))
	require.Equal(t, uint64(100), u64FromHash(txn.Txn().GetState(staking.AddrStakingContract, stakingDeactivatedAtBlockKey(validator))))

	setValidatorInactiveInStaking(txn, validator, 200)
	secondPackedState := txn.Txn().GetState(staking.AddrStakingContract, stakingMetaPackedKey(validator))
	secondPacked := new(big.Int).SetBytes(secondPackedState[:])
	require.False(t, isValidatorMetaActive(secondPacked))
	require.Equal(t, uint64(100), u64FromHash(txn.Txn().GetState(staking.AddrStakingContract, stakingDeactivatedAtBlockKey(validator))))
}

func newPosTestTransition(t *testing.T) *state.Transition {
	t.Helper()

	st := itrie.NewState(itrie.NewMemoryStorage())

	forks := chain.Forks{}
	for name, fork := range *chain.AllForksEnabled {
		forks[name] = fork
	}
	forks[chain.FeePoolSplit] = chain.NewFork(0)

	ex := state.NewExecutor(&chain.Params{
		Forks: &forks,
		BurnContract: map[uint64]types.Address{
			0: types.ZeroAddress,
		},
	}, st, hclog.NewNullLogger())

	rootHash, err := ex.WriteGenesis(nil, types.Hash{})
	require.NoError(t, err)

	ex.GetHash = func(h *types.Header) state.GetHashByNumber {
		return func(i uint64) types.Hash {
			return rootHash
		}
	}

	transition, err := ex.BeginTxn(rootHash, &types.Header{}, types.ZeroAddress)
	require.NoError(t, err)

	return transition
}

func newPosTestTransitionWithStaking(t *testing.T, validator types.Address) *state.Transition {
	t.Helper()

	txn := newPosTestTransition(t)
	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validator))

	contractState, err := stakingHelper.PredeployStakingSC(vals, stakingHelper.PredeployParams{
		MinValidatorCount: 1,
		MaxValidatorCount: 10,
		EpochSize:         10,
	})
	require.NoError(t, err)
	require.NoError(t, txn.SetAccountDirectly(staking.AddrStakingContract, contractState))

	return txn
}

func newPosTestTransitionWithStakingValidators(t *testing.T, vals validators.Validators, minValidators, maxValidators uint64) *state.Transition {
	t.Helper()

	txn := newPosTestTransition(t)

	contractState, err := stakingHelper.PredeployStakingSC(vals, stakingHelper.PredeployParams{
		MinValidatorCount: minValidators,
		MaxValidatorCount: maxValidators,
		EpochSize:         10,
	})
	require.NoError(t, err)
	require.NoError(t, txn.SetAccountDirectly(staking.AddrStakingContract, contractState))

	return txn
}

func newPosTestTransitionWithStakingValidatorsAtBlock(
	t *testing.T,
	vals validators.Validators,
	minValidators, maxValidators, blockNumber uint64,
) *state.Transition {
	t.Helper()

	st := itrie.NewState(itrie.NewMemoryStorage())

	forks := chain.Forks{}
	for name, fork := range *chain.AllForksEnabled {
		forks[name] = fork
	}
	forks[chain.FeePoolSplit] = chain.NewFork(0)

	ex := state.NewExecutor(&chain.Params{
		Forks: &forks,
		BurnContract: map[uint64]types.Address{
			0: types.ZeroAddress,
		},
	}, st, hclog.NewNullLogger())

	rootHash, err := ex.WriteGenesis(nil, types.Hash{})
	require.NoError(t, err)

	ex.GetHash = func(h *types.Header) state.GetHashByNumber {
		return func(i uint64) types.Hash {
			return rootHash
		}
	}

	txn, err := ex.BeginTxn(rootHash, &types.Header{Number: blockNumber}, types.ZeroAddress)
	require.NoError(t, err)

	contractState, err := stakingHelper.PredeployStakingSC(vals, stakingHelper.PredeployParams{
		MinValidatorCount: minValidators,
		MaxValidatorCount: maxValidators,
		EpochSize:         10,
	})
	require.NoError(t, err)
	require.NoError(t, txn.SetAccountDirectly(staking.AddrStakingContract, contractState))

	return txn
}
