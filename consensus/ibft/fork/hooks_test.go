package fork

import (
	"errors"
	"math/big"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/assert"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/consensus/ibft/hook"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/helper/common"
	stakingHelper "github.com/xgr-network/xgr-node/helper/staking"
	"github.com/xgr-network/xgr-node/state"
	itrie "github.com/xgr-network/xgr-node/state/immutable-trie"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
	"github.com/xgr-network/xgr-node/validators/store"
)

type mockHeaderModifierStore struct {
	store.ValidatorStore

	ModifyHeaderFunc  func(*types.Header, types.Address) error
	VerifyHeaderFunc  func(*types.Header) error
	ProcessHeaderFunc func(*types.Header) error
}

func Test_registerStakingContractDeploymentHooks_overwritesStaleBootstrapSlotsWhenUninitialized(t *testing.T) {
	t.Parallel()

	oldVals := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1111")),
	)
	newVals := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x2222")),
	)

	params := stakingHelper.PredeployParams{
		MinValidatorCount: 1,
		MaxValidatorCount: 10,
		EpochSize:         10,
	}

	oldState, err := stakingHelper.PredeployStakingSCBootstrap(oldVals, params)
	assert.NoError(t, err)

	newState, err := stakingHelper.PredeployStakingSCBootstrap(newVals, params)
	assert.NoError(t, err)

	// Find the validators[0] storage slot (same key, different value between old/new bootstrap sets).
	var validators0Key types.Hash
	for k, oldV := range oldState.Storage {
		newV, ok := newState.Storage[k]
		if !ok || oldV == newV || oldV == (types.Hash{}) || newV == (types.Hash{}) {
			continue
		}

		validators0Key = k

		break
	}
	assert.NotEqual(t, types.Hash{}, validators0Key)

	maxValidatorsKey := types.BytesToHash(big.NewInt(6).Bytes())
	validatorsLengthKey := types.BytesToHash(big.NewInt(0).Bytes())

	txn := newTestTransition(t)
	// Existing contract account with partial recovery state:
	// - capacity is set (slot 6 non-zero)
	// - validator length is zero (slot 0 => uninitialized)
	// - stale validators[0] slot is still populated from old state
	err = txn.SetAccountDirectly(staking.AddrStakingContract, &chain.GenesisAccount{
		Code:    oldState.Code,
		Balance: big.NewInt(0),
		Storage: map[types.Hash]types.Hash{
			maxValidatorsKey:    oldState.Storage[maxValidatorsKey],
			validatorsLengthKey: {},
			validators0Key:      oldState.Storage[validators0Key],
		},
	})
	assert.NoError(t, err)

	hooks := &hook.Hooks{}
	fork := &IBFTFork{
		Deployment:        &common.JSONNumber{Value: 10},
		MinValidatorCount: &common.JSONNumber{Value: params.MinValidatorCount},
		MaxValidatorCount: &common.JSONNumber{Value: params.MaxValidatorCount},
		ValidatorType:     validators.ECDSAValidatorType,
		Validators:        newVals,
	}
	registerStakingContractDeploymentHooks(hooks, fork, 10)

	assert.NoError(t, hooks.PreCommitState(&types.Header{Number: 10}, txn))

	assert.Equal(
		t,
		newState.Storage[validators0Key],
		txn.Txn().GetState(staking.AddrStakingContract, validators0Key),
	)
}

func Test_registerStakingContractDeploymentHooks_bootstrapsWhenEpochSizeSlotMissing(t *testing.T) {
	t.Parallel()

	oldVals := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1111")),
	)
	newVals := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x2222")),
	)

	params := stakingHelper.PredeployParams{
		MinValidatorCount: 1,
		MaxValidatorCount: 10,
		EpochSize:         10,
	}

	oldState, err := stakingHelper.PredeployStakingSCBootstrap(oldVals, params)
	assert.NoError(t, err)

	newState, err := stakingHelper.PredeployStakingSCBootstrap(newVals, params)
	assert.NoError(t, err)

	var validators0Key types.Hash
	for k, oldV := range oldState.Storage {
		newV, ok := newState.Storage[k]
		if !ok || oldV == newV || oldV == (types.Hash{}) || newV == (types.Hash{}) {
			continue
		}
		validators0Key = k
		break
	}
	assert.NotEqual(t, types.Hash{}, validators0Key)

	capKey := types.BytesToHash(big.NewInt(0).Bytes())
	lenKey := types.BytesToHash(big.NewInt(1).Bytes())
	epochKey := types.BytesToHash(big.NewInt(8).Bytes())

	txn := newTestTransition(t)
	err = txn.SetAccountDirectly(staking.AddrStakingContract, &chain.GenesisAccount{
		Code:    oldState.Code,
		Balance: big.NewInt(0),
		Storage: map[types.Hash]types.Hash{
			capKey:         oldState.Storage[capKey],
			lenKey:         oldState.Storage[lenKey],
			epochKey:       {}, // missing epoch size must trigger bootstrap rewrite
			validators0Key: oldState.Storage[validators0Key],
		},
	})
	assert.NoError(t, err)

	hooks := &hook.Hooks{}
	fork := &IBFTFork{
		Deployment:        &common.JSONNumber{Value: 10},
		MinValidatorCount: &common.JSONNumber{Value: params.MinValidatorCount},
		MaxValidatorCount: &common.JSONNumber{Value: params.MaxValidatorCount},
		ValidatorType:     validators.ECDSAValidatorType,
		Validators:        newVals,
	}
	registerStakingContractDeploymentHooks(hooks, fork, 10)

	assert.NoError(t, hooks.PreCommitState(&types.Header{Number: 10}, txn))
	assert.Equal(t, newState.Storage[epochKey], txn.Txn().GetState(staking.AddrStakingContract, epochKey))
	assert.Equal(t, newState.Storage[validators0Key], txn.Txn().GetState(staking.AddrStakingContract, validators0Key))
}

func (m *mockHeaderModifierStore) ModifyHeader(header *types.Header, addr types.Address) error {
	return m.ModifyHeaderFunc(header, addr)
}

func (m *mockHeaderModifierStore) VerifyHeader(header *types.Header) error {
	return m.VerifyHeaderFunc(header)
}

func (m *mockHeaderModifierStore) ProcessHeader(header *types.Header) error {
	return m.ProcessHeaderFunc(header)
}

type mockUpdatableStore struct {
	store.ValidatorStore

	UpdateValidatorStoreFunc func(validators.Validators, uint64) error
}

func (m *mockUpdatableStore) UpdateValidatorSet(validators validators.Validators, height uint64) error {
	return m.UpdateValidatorStoreFunc(validators, height)
}

func Test_registerHeaderModifierHooks(t *testing.T) {
	t.Parallel()

	t.Run("should do nothing if validator store doesn't implement HeaderModifier", func(t *testing.T) {
		t.Parallel()

		type invalidValidatorStoreMock struct {
			store.ValidatorStore
		}

		hooks := &hook.Hooks{}
		mockStore := &invalidValidatorStoreMock{}

		registerHeaderModifierHooks(hooks, mockStore)

		assert.Equal(
			t,
			&hook.Hooks{},
			hooks,
		)
	})

	t.Run("should register functions to the hooks", func(t *testing.T) {
		t.Parallel()

		var (
			header = &types.Header{
				Number: 100,
				Hash:   types.BytesToHash(crypto.Keccak256([]byte{0x10, 0x0})),
			}
			addr = types.StringToAddress("1")

			err1 = errors.New("error 1")
			err2 = errors.New("error 1")
			err3 = errors.New("error 1")
		)

		hooks := &hook.Hooks{}
		mockStore := &mockHeaderModifierStore{
			ModifyHeaderFunc: func(h *types.Header, a types.Address) error {
				assert.Equal(t, header, h)
				assert.Equal(t, addr, a)

				return err1
			},
			VerifyHeaderFunc: func(h *types.Header) error {
				assert.Equal(t, header, h)

				return err2
			},
			ProcessHeaderFunc: func(h *types.Header) error {
				assert.Equal(t, header, h)

				return err3
			},
		}

		registerHeaderModifierHooks(hooks, mockStore)

		assert.Nil(t, hooks.ShouldWriteTransactionFunc)
		assert.Nil(t, hooks.VerifyBlockFunc)
		assert.Nil(t, hooks.PreCommitStateFunc)
		assert.Nil(t, hooks.PostInsertBlockFunc)

		assert.Equal(
			t,
			hooks.ModifyHeader(header, addr),
			err1,
		)
		assert.Equal(
			t,
			hooks.VerifyHeader(header),
			err2,
		)
		assert.Equal(
			t,
			hooks.ProcessHeader(header),
			err3,
		)
	})
}

func Test_registerUpdateValidatorsHooks(t *testing.T) {
	t.Parallel()

	var (
		vals = validators.NewECDSAValidatorSet(
			validators.NewECDSAValidator(types.StringToAddress("1")),
			validators.NewECDSAValidator(types.StringToAddress("2")),
		)
	)

	t.Run("should do nothing if validator store doesn't implement Updatable", func(t *testing.T) {
		t.Parallel()

		type invalidValidatorStoreMock struct {
			store.ValidatorStore
		}

		hooks := &hook.Hooks{}
		mockStore := &invalidValidatorStoreMock{}

		registerUpdateValidatorsHooks(hooks, mockStore, vals, 0)

		assert.Equal(
			t,
			&hook.Hooks{},
			hooks,
		)
	})

	t.Run("should register UpdateValidatorSet to the hooks", func(t *testing.T) {
		t.Parallel()

		var (
			fromHeight uint64 = 10
			err               = errors.New("test")

			block = &types.Block{
				Header:       &types.Header{},
				Transactions: []*types.Transaction{},
				Uncles:       []*types.Header{},
			}
		)

		hooks := &hook.Hooks{}
		mockStore := &mockUpdatableStore{
			UpdateValidatorStoreFunc: func(v validators.Validators, h uint64) error {
				assert.Equal(t, vals, v)
				assert.Equal(t, fromHeight, h)

				return err
			},
		}

		registerUpdateValidatorsHooks(hooks, mockStore, vals, fromHeight)

		assert.Nil(t, hooks.ModifyHeaderFunc)
		assert.Nil(t, hooks.VerifyHeaderFunc)
		assert.Nil(t, hooks.ProcessHeaderFunc)
		assert.Nil(t, hooks.ShouldWriteTransactionFunc)
		assert.Nil(t, hooks.VerifyBlockFunc)
		assert.Nil(t, hooks.PreCommitStateFunc)

		// case 1: the block number is not the one before fromHeight
		assert.NoError(
			t,
			hooks.PostInsertBlockFunc(block),
		)

		// case 2: the block number is the one before fromHeight
		block.Header.Number = fromHeight - 1

		assert.Equal(
			t,
			hooks.PostInsertBlockFunc(block),
			err,
		)
	})
}

func Test_registerTxInclusionGuardHooks(t *testing.T) {
	t.Parallel()

	epochSize := uint64(10)
	hooks := &hook.Hooks{}

	key, err := crypto.GenerateECDSAKey()
	assert.NoError(t, err)
	keyManager := signer.NewECDSAKeyManagerFromKey(key)
	headerSigner := signer.NewSigner(keyManager, keyManager)

	registerTxInclusionGuardHooks(hooks, epochSize, pos.DefaultUptimeConfig(), func(uint64) (signer.Signer, error) {
		return headerSigner, nil
	}, 1)

	assert.Nil(t, hooks.ModifyHeaderFunc)
	assert.Nil(t, hooks.VerifyHeaderFunc)
	assert.Nil(t, hooks.ProcessHeaderFunc)
	assert.NotNil(t, hooks.PreCommitStateFunc)
	assert.Nil(t, hooks.PostInsertBlockFunc)

	assert.NoError(t, hooks.PreCommitStateFunc(&types.Header{Number: 1}, newTestTransition(t)))

	var (
		cases = map[uint64]bool{
			0:               true,
			epochSize - 1:   true,
			epochSize:       false,
			epochSize + 1:   true,
			epochSize*2 - 1: true,
			epochSize * 2:   false,
			epochSize*2 + 1: true,
		}

		blockWithoutTransactions = &types.Block{
			Header:       &types.Header{},
			Transactions: []*types.Transaction{},
		}

		blockWithTransactions = &types.Block{
			Header: &types.Header{},
			Transactions: []*types.Transaction{
				{
					Nonce: 0,
				},
			},
		}
	)

	for h, ok := range cases {
		assert.Equal(
			t,
			ok,
			hooks.ShouldWriteTransactions(h),
		)

		blockWithTransactions.Header.Number = h
		blockWithoutTransactions.Header.Number = h
		blockWithSystemTransaction := &types.Block{
			Header:       &types.Header{Number: h},
			Transactions: []*types.Transaction{pos.EpochFinalizationSystemTx(h)},
		}

		if ok {
			assert.NoError(t, hooks.VerifyBlock(blockWithoutTransactions))
			assert.NoError(t, hooks.VerifyBlock(blockWithTransactions))
		} else {
			assert.ErrorIs(t, ErrTxInLastEpochOfBlock, hooks.VerifyBlock(blockWithoutTransactions))
			assert.NoError(t, hooks.VerifyBlock(blockWithSystemTransaction))
			assert.ErrorIs(t, ErrTxInLastEpochOfBlock, hooks.VerifyBlock(blockWithTransactions))
		}
	}
}

func Test_registerTxInclusionGuardHooks_PrePoAForkSafety(t *testing.T) {
	t.Parallel()

	epochSize := uint64(10)
	firstPoSFrom := uint64(100)
	hooks := &hook.Hooks{}

	registerTxInclusionGuardHooks(hooks, epochSize, pos.DefaultUptimeConfig(), func(uint64) (signer.Signer, error) {
		return nil, errors.New("signer must not be requested before PoS activation")
	}, firstPoSFrom)

	boundary := epochSize
	assert.True(t, hooks.ShouldWriteTransactions(boundary), "PoA boundary block production must still allow normal transactions")
	assert.NoError(t, hooks.VerifyBlock(&types.Block{Header: &types.Header{Number: boundary}}), "empty PoA boundary block remains valid")
	assert.NoError(t, hooks.VerifyBlock(&types.Block{
		Header:       &types.Header{Number: boundary},
		Transactions: []*types.Transaction{{Nonce: 1}},
	}), "user transactions remain valid before PoS activation")

	txn := newTestTransition(t)
	assert.NoError(t, hooks.PreCommitStateFunc(&types.Header{Number: boundary}, txn))
	assert.Empty(t, txn.Receipts(), "no epoch-finalization system receipt is produced before PoS activation")
}

func Test_registerTxInclusionGuardHooks_RejectsMalformedEpochFinalizationSystemTx(t *testing.T) {
	t.Parallel()

	epochSize := uint64(10)
	hooks := &hook.Hooks{}

	registerTxInclusionGuardHooks(hooks, epochSize, pos.DefaultUptimeConfig(), func(uint64) (signer.Signer, error) {
		return nil, nil
	}, 1)

	boundary := epochSize
	valid := pos.EpochFinalizationSystemTx(boundary)
	assert.NoError(t, hooks.VerifyBlock(&types.Block{
		Header:       &types.Header{Number: boundary},
		Transactions: []*types.Transaction{valid},
	}))

	cases := map[string]func(*types.Transaction){
		"wrong nonce": func(tx *types.Transaction) { tx.Nonce = boundary + 1 },
		"wrong hash":  func(tx *types.Transaction) { tx.Hash[0] ^= 0x01 },
		"wrong from":  func(tx *types.Transaction) { tx.From = types.ZeroAddress },
		"wrong input": func(tx *types.Transaction) { tx.Input = []byte("bad-input") },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			tx := valid.Copy()
			mutate(tx)

			assert.ErrorIs(t, ErrTxInLastEpochOfBlock, hooks.VerifyBlock(&types.Block{
				Header:       &types.Header{Number: boundary},
				Transactions: []*types.Transaction{tx},
			}))
		})
	}
}

func Test_shouldSkipEpochFinalizationBeforePoS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		boundary     uint64
		firstPoSFrom uint64
		skip         bool
	}{
		{
			name:         "skip when finalized epoch is fully pre-PoS at cutover boundary",
			boundary:     50,
			firstPoSFrom: 50,
			skip:         true,
		},
		{
			name:         "do not skip direct PoS from genesis",
			boundary:     50,
			firstPoSFrom: 1,
			skip:         false,
		},
		{
			name:         "do not skip when PoS starts mid-previous epoch",
			boundary:     50,
			firstPoSFrom: 45,
			skip:         false,
		},
		{
			name:         "do not skip next boundary after cutover",
			boundary:     100,
			firstPoSFrom: 50,
			skip:         false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.skip, shouldSkipEpochFinalizationBeforePoS(tt.boundary, tt.firstPoSFrom))
		})
	}
}

func newTestTransition(
	t *testing.T,
) *state.Transition {
	t.Helper()

	st := itrie.NewState(itrie.NewMemoryStorage())

	ex := state.NewExecutor(&chain.Params{
		Forks: chain.AllForksEnabled,
		BurnContract: map[uint64]types.Address{
			0: types.ZeroAddress,
		},
	}, st, hclog.NewNullLogger())

	rootHash, err := ex.WriteGenesis(nil, types.Hash{})
	assert.NoError(t, err)

	ex.GetHash = func(h *types.Header) state.GetHashByNumber {
		return func(i uint64) types.Hash {
			return rootHash
		}
	}

	transition, err := ex.BeginTxn(
		rootHash,
		&types.Header{},
		types.ZeroAddress,
	)
	assert.NoError(t, err)

	return transition
}

func Test_registerStakingContractDeploymentHooks(t *testing.T) {
	t.Parallel()

	hooks := &hook.Hooks{}
	fork := &IBFTFork{
		Deployment: &common.JSONNumber{
			Value: 10,
		},
		MinValidatorCount: &common.JSONNumber{Value: 1},
		MaxValidatorCount: &common.JSONNumber{Value: 10},
		ValidatorType:     validators.ECDSAValidatorType,
		Validators: validators.NewECDSAValidatorSet(
			validators.NewECDSAValidator(types.StringToAddress("0x1234")),
		),
	}

	registerStakingContractDeploymentHooks(hooks, fork, 10)

	expectedState, err := stakingHelper.PredeployStakingSCBootstrap(
		fork.Validators,
		stakingHelper.PredeployParams{MinValidatorCount: 1, MaxValidatorCount: 10, EpochSize: 10},
	)
	assert.NoError(t, err)

	assert.Nil(t, hooks.ShouldWriteTransactionFunc)
	assert.Nil(t, hooks.ModifyHeaderFunc)
	assert.Nil(t, hooks.VerifyHeaderFunc)
	assert.Nil(t, hooks.ProcessHeaderFunc)
	assert.Nil(t, hooks.PostInsertBlockFunc)

	txn := newTestTransition(t)

	// deployment should not happen
	assert.NoError(
		t,
		hooks.PreCommitState(&types.Header{Number: 5}, txn),
	)

	assert.False(
		t,
		txn.AccountExists(staking.AddrStakingContract),
	)

	// should deploy contract
	assert.NoError(
		t,
		hooks.PreCommitState(&types.Header{Number: 10}, txn),
	)

	assert.True(
		t,
		txn.AccountExists(staking.AddrStakingContract),
	)
	assert.Equal(t, expectedState.Code, txn.GetCode(staking.AddrStakingContract))

	// should update only bytecode (if contract is deployed again, it returns error)
	assert.NoError(
		t,
		hooks.PreCommitState(&types.Header{Number: 10}, txn),
	)

	assert.True(
		t,
		txn.AccountExists(staking.AddrStakingContract),
	)
	assert.Equal(t, expectedState.Code, txn.GetCode(staking.AddrStakingContract))
}

func Test_getPreDeployParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                    string
		fork                    *IBFTFork
		bootstrapValidatorCount uint64
		params                  stakingHelper.PredeployParams
		wantErr                 bool
	}{
		{
			name: "uses the given values",
			fork: &IBFTFork{
				MinValidatorCount: &common.JSONNumber{Value: 10},
				MaxValidatorCount: &common.JSONNumber{Value: 20},
			},
			bootstrapValidatorCount: 10,
			params: stakingHelper.PredeployParams{
				MinValidatorCount: 10,
				MaxValidatorCount: 20,
				EpochSize:         10,
			},
		},
		{
			name:                    "missing values default to bootstrap validator count",
			fork:                    &IBFTFork{},
			bootstrapValidatorCount: 4,
			params: stakingHelper.PredeployParams{
				MinValidatorCount: 4,
				MaxValidatorCount: 4,
				EpochSize:         10,
			},
		},
		{
			name:                    "errors when bootstrap validator count is empty",
			fork:                    &IBFTFork{},
			bootstrapValidatorCount: 0,
			wantErr:                 true,
		},
		{
			name: "errors when min exceeds max",
			fork: &IBFTFork{
				MinValidatorCount: &common.JSONNumber{Value: 5},
				MaxValidatorCount: &common.JSONNumber{Value: 4},
			},
			wantErr: true,
		},
		{
			name: "errors when max exceeds epoch snapshot max",
			fork: &IBFTFork{
				MaxValidatorCount: &common.JSONNumber{Value: pos.MaxEpochValidatorsSnapshot + 1},
			},
			bootstrapValidatorCount: 4,
			wantErr:                 true,
		},
		{
			name: "errors when min exceeds bootstrap validator count",
			fork: &IBFTFork{
				MinValidatorCount: &common.JSONNumber{Value: 5},
				MaxValidatorCount: &common.JSONNumber{Value: 10},
			},
			bootstrapValidatorCount: 4,
			wantErr:                 true,
		},
	}

	for _, test := range tests {
		test := test

		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			params, err := getPreDeployParams(test.fork, 10, test.bootstrapValidatorCount)
			if test.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, test.params, params)
		})
	}
}

func Test_registerStakingContractDeploymentHooks_UsesHeaderValidatorsWhenForkValidatorsEmpty(t *testing.T) {
	t.Parallel()

	headerValidators := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1111")),
		validators.NewECDSAValidator(types.StringToAddress("0x2222")),
	)

	hooks := &hook.Hooks{}
	fork := &IBFTFork{
		Deployment:        &common.JSONNumber{Value: 10},
		MinValidatorCount: &common.JSONNumber{Value: 1},
		MaxValidatorCount: &common.JSONNumber{Value: 4},
		ValidatorType:     validators.ECDSAValidatorType,
		Validators:        validators.NewECDSAValidatorSet(),
	}

	registerStakingContractDeploymentHooks(hooks, fork, 10)

	txn := newTestTransition(t)
	err := hooks.PreCommitState(newHeaderWithECDSAValidators(10, headerValidators), txn)
	assert.NoError(t, err)
	assert.True(t, txn.AccountExists(staking.AddrStakingContract))
}

func Test_registerStakingContractDeploymentHooks_RejectsEmptyHeaderBootstrapValidators(t *testing.T) {
	t.Parallel()

	hooks := &hook.Hooks{}
	fork := &IBFTFork{
		Deployment:        &common.JSONNumber{Value: 10},
		MinValidatorCount: &common.JSONNumber{Value: 1},
		MaxValidatorCount: &common.JSONNumber{Value: 4},
		ValidatorType:     validators.ECDSAValidatorType,
		Validators:        validators.NewECDSAValidatorSet(),
	}

	registerStakingContractDeploymentHooks(hooks, fork, 10)

	err := hooks.PreCommitState(newHeaderWithECDSAValidators(10, validators.NewECDSAValidatorSet()), newTestTransition(t))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fallback failed")
	assert.Contains(t, err.Error(), "decoded validator set is empty")
}

func newHeaderWithECDSAValidators(number uint64, vals validators.Validators) *types.Header {
	extra := &signer.IstanbulExtra{
		Validators:           vals,
		CommittedSeals:       &signer.SerializedSeal{},
		ParentCommittedSeals: &signer.SerializedSeal{},
	}

	return &types.Header{
		Number:    number,
		ExtraData: extra.MarshalRLPTo(make([]byte, signer.IstanbulExtraVanity)),
	}
}
