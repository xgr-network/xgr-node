package fork

import (
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/helper/common"
	"github.com/xgr-network/xgr-node/state"
	itrie "github.com/xgr-network/xgr-node/state/immutable-trie"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
	"github.com/xgr-network/xgr-node/validators/store"
)

func TestPoPGuardRejectsUnprovedCachedSetAndKeepsHistoricalHeight(t *testing.T) {
	key, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	pub, err := crypto.BLSSecretKeyToPubkeyBytes(key)
	require.NoError(t, err)
	addr := types.Address{19: 1}
	vals := validators.NewBLSValidatorSet(validators.NewBLSValidator(addr, pub))
	forks := chain.AllForksEnabled.Copy()
	forks.SetFork(chain.StakingBLSPoP, chain.NewFork(100))
	ex := state.NewExecutor(&chain.Params{Forks: forks, ChainID: 1643}, itrie.NewState(itrie.NewMemoryStorage()), hclog.NewNullLogger())
	ex.GetHash = func(*types.Header) state.GetHashByNumber { return func(uint64) types.Hash { return types.ZeroHash } }
	root, err := ex.WriteGenesis(map[types.Address]*chain.GenesisAccount{staking.AddrStakingContract: {Nonce: 1, Code: []byte{0x00}}}, types.ZeroHash)
	require.NoError(t, err)
	// The migration is tested separately with the actual IBFT hook. This fixture
	// isolates the independent cached-set guard after BeginTxn returns.
	ex.BeginTxnHook = func(*types.Header, *state.Transition) error { return nil }
	fm := &ForkManager{
		forks:     IBFTForks{&IBFTFork{Type: PoS, ValidatorType: validators.BLSValidatorType, From: common.JSONNumber{Value: 0}}},
		epochSize: 10, executor: ex,
		blockchain:      &store.MockBlockchain{GetHeaderByNumberFn: func(height uint64) (*types.Header, bool) { return &types.Header{Number: height, StateRoot: root}, true }},
		validatorStores: map[store.SourceType]ValidatorStore{store.Contract: &mockValidatorStore{GetValidatorsFunc: func(uint64, uint64, uint64) (validators.Validators, error) { return vals, nil }}},
	}
	_, err = fm.GetValidators(99)
	require.NoError(t, err, "historical/parent height before H must retain old verification")
	_, err = fm.GetValidators(100)
	require.ErrorContains(t, err, "possession not verified")
	proved, err := ex.BeginTxn(root, &types.Header{Number: 99}, types.ZeroAddress)
	require.NoError(t, err)
	proved.Txn().SetState(staking.AddrStakingContract, state.StakingPoPVerifiedSlot(addr, pub), types.Hash{31: 1})
	_, root, err = proved.Commit()
	require.NoError(t, err)
	_, err = fm.GetValidators(100)
	require.NoError(t, err, "verified cached key is admitted at H")
}
