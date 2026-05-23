package pos

import (
	"math/big"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/state"
	itrie "github.com/xgr-network/xgr-node/state/immutable-trie"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func macroTestTransition(t *testing.T) *state.Transition {
	t.Helper()
	st := itrie.NewState(itrie.NewMemoryStorage())
	ex := state.NewExecutor(&chain.Params{Forks: chain.AllForksEnabled, BurnContract: map[uint64]types.Address{0: types.ZeroAddress}}, st, hclog.NewNullLogger())
	root, err := ex.WriteGenesis(nil, types.Hash{})
	require.NoError(t, err)
	ex.GetHash = func(*types.Header) state.GetHashByNumber { return func(uint64) types.Hash { return root } }
	tx, err := ex.BeginTxn(root, &types.Header{Number: 1, StateRoot: root}, types.ZeroAddress)
	require.NoError(t, err)
	return tx
}

func macroTestExecutorAndRoot(t *testing.T) (*state.Executor, types.Hash) {
	t.Helper()
	st := itrie.NewState(itrie.NewMemoryStorage())
	ex := state.NewExecutor(&chain.Params{Forks: chain.AllForksEnabled, BurnContract: map[uint64]types.Address{0: types.ZeroAddress}}, st, hclog.NewNullLogger())
	root, err := ex.WriteGenesis(nil, types.Hash{})
	require.NoError(t, err)
	ex.GetHash = func(*types.Header) state.GetHashByNumber { return func(uint64) types.Hash { return root } }
	return ex, root
}

func TestMacroEpochValidatorSet_Roundtrip(t *testing.T) {
	txn := macroTestTransition(t)
	epoch := uint64(3)
	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001")), validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000002")))
	require.NoError(t, StoreMacroEpochValidatorSet(txn, epoch, vals))
	require.True(t, HasMacroEpochValidatorSet(txn, epoch))
	loaded, ok, err := LoadMacroEpochValidatorSet(txn, epoch, validators.ECDSAValidatorType)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, loaded.Equal(vals))
}

func TestMacroEpochValidatorSet_StoreValidation(t *testing.T) {
	txn := macroTestTransition(t)
	require.Error(t, StoreMacroEpochValidatorSet(txn, 1, nil))
	require.Error(t, StoreMacroEpochValidatorSet(txn, 1, validators.NewECDSAValidatorSet()))
}

func TestMacroEpochValidatorSet_LoadMissing(t *testing.T) {
	txn := macroTestTransition(t)
	loaded, ok, err := LoadMacroEpochValidatorSet(txn, 9, validators.ECDSAValidatorType)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, loaded)
}

func TestMacroEpochStakeSnapshot_Roundtrip(t *testing.T) {
	txn := macroTestTransition(t)
	epoch := uint64(4)
	a := types.StringToAddress("0x1000000000000000000000000000000000000001")
	b := types.StringToAddress("0x1000000000000000000000000000000000000002")
	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(a), validators.NewECDSAValidator(b))
	require.NoError(t, StoreMacroEpochStakeSnapshot(txn, epoch, map[types.Address]*big.Int{a: big.NewInt(100), b: big.NewInt(200)}))
	loaded, ok, err := LoadMacroEpochStakeSnapshot(txn, epoch, vals)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "100", loaded[a].String())
	require.Equal(t, "200", loaded[b].String())
}

func TestMacroEpochStakeSnapshot_LoadValidation(t *testing.T) {
	txn := macroTestTransition(t)
	epoch := uint64(5)
	a := types.StringToAddress("0x1000000000000000000000000000000000000001")
	b := types.StringToAddress("0x1000000000000000000000000000000000000002")
	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(a), validators.NewECDSAValidator(b))

	require.Error(t, StoreMacroEpochStakeSnapshot(txn, epoch, nil))
	require.Error(t, StoreMacroEpochStakeSnapshot(txn, epoch, map[types.Address]*big.Int{a: big.NewInt(0)}))
	require.Error(t, StoreMacroEpochStakeSnapshot(txn, epoch, map[types.Address]*big.Int{a: big.NewInt(-1)}))
	require.NoError(t, StoreMacroEpochStakeSnapshot(txn, epoch, map[types.Address]*big.Int{a: big.NewInt(100), b: big.NewInt(200), types.StringToAddress("0x1000000000000000000000000000000000000003"): big.NewInt(300)}))
	loaded, ok, err := LoadMacroEpochStakeSnapshot(txn, epoch, vals)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, loaded, 2)

	_, _, err = LoadMacroEpochStakeSnapshot(txn, epoch, validators.NewECDSAValidatorSet(validators.NewECDSAValidator(a), validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000004"))))
	require.Error(t, err)
}

func TestMacroEpochValidatorSet_BLSRoundtrip(t *testing.T) {
	txn := macroTestTransition(t)
	epoch := uint64(6)
	a := types.StringToAddress("0x1000000000000000000000000000000000000001")
	b := types.StringToAddress("0x1000000000000000000000000000000000000002")
	sk1, _ := crypto.GenerateBLSKey()
	pk1, _ := crypto.BLSSecretKeyToPubkeyBytes(sk1)
	sk2, _ := crypto.GenerateBLSKey()
	pk2, _ := crypto.BLSSecretKeyToPubkeyBytes(sk2)
	vals := validators.NewBLSValidatorSet(validators.NewBLSValidator(a, pk1), validators.NewBLSValidator(b, pk2))
	require.NoError(t, StoreMacroEpochValidatorSet(txn, epoch, vals))
	loaded, ok, err := LoadMacroEpochValidatorSet(txn, epoch, validators.BLSValidatorType)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, vals.Type(), loaded.Type())
	require.Equal(t, vals.Len(), loaded.Len())
}

func TestMacroEpochValidatorSet_BLSMissingOrInvalidPubkeyFails(t *testing.T) {
	txn := macroTestTransition(t)
	epoch := uint64(7)
	a := types.StringToAddress("0x1000000000000000000000000000000000000001")
	sk, _ := crypto.GenerateBLSKey()
	pk, _ := crypto.BLSSecretKeyToPubkeyBytes(sk)
	vals := validators.NewBLSValidatorSet(validators.NewBLSValidator(a, pk))
	require.NoError(t, StoreMacroEpochValidatorSet(txn, epoch, vals))

	// missing
	txn.Txn().SetState(PosSysAddr, keyEpochValidatorBLSPub(epoch, 0, 0), types.Hash{})
	_, _, err := LoadMacroEpochValidatorSet(txn, epoch, validators.BLSValidatorType)
	require.Error(t, err)

	// invalid
	require.NoError(t, StoreMacroEpochValidatorSet(txn, epoch, validators.NewBLSValidatorSet(validators.NewBLSValidator(a, pk))))
	txn.Txn().SetState(PosSysAddr, keyEpochValidatorBLSPub(epoch, 0, 0), u64ToHash(48))
	txn.Txn().SetState(PosSysAddr, keyEpochValidatorBLSPub(epoch, 0, 1), types.BytesToHash([]byte{1, 2, 3}))
	_, _, err = LoadMacroEpochValidatorSet(txn, epoch, validators.BLSValidatorType)
	require.Error(t, err)
}

func TestMacroEpochValidatorSet_BLSInvalidPubkeyStoreDoesNotPartiallyWrite(t *testing.T) {
	txn := macroTestTransition(t)
	epoch := uint64(8)
	a := types.StringToAddress("0x1000000000000000000000000000000000000001")
	vals := validators.NewBLSValidatorSet(validators.NewBLSValidator(a, []byte{1, 2, 3}))

	err := StoreMacroEpochValidatorSet(txn, epoch, vals)
	require.Error(t, err)
	require.False(t, HasMacroEpochValidatorSet(txn, epoch))

	loaded, ok, loadErr := LoadMacroEpochValidatorSet(txn, epoch, validators.BLSValidatorType)
	require.NoError(t, loadErr)
	require.False(t, ok)
	require.Nil(t, loaded)
}

func TestMacroEpochStakeSnapshot_InvalidInputDoesNotPartiallyWrite(t *testing.T) {
	txn := macroTestTransition(t)
	epoch := uint64(9)
	a := types.StringToAddress("0x1000000000000000000000000000000000000001")
	b := types.StringToAddress("0x1000000000000000000000000000000000000002")
	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(a), validators.NewECDSAValidator(b))

	err := StoreMacroEpochStakeSnapshot(txn, epoch, map[types.Address]*big.Int{
		a: big.NewInt(100),
		b: big.NewInt(0),
	})
	require.Error(t, err)

	_, _, loadErr := LoadMacroEpochStakeSnapshot(txn, epoch, vals)
	require.Error(t, loadErr)
	require.Contains(t, loadErr.Error(), "missing non-positive stake")

	err = StoreMacroEpochStakeSnapshot(txn, epoch, map[types.Address]*big.Int{
		a: big.NewInt(100),
		b: nil,
	})
	require.Error(t, err)

	_, _, loadErr = LoadMacroEpochStakeSnapshot(txn, epoch, vals)
	require.Error(t, loadErr)
	require.Contains(t, loadErr.Error(), "missing non-positive stake")
}

func TestHasMacroEpochStakeSnapshot_StrictPresence(t *testing.T) {
	txn := macroTestTransition(t)
	epoch := uint64(10)
	a := types.StringToAddress("0x1000000000000000000000000000000000000001")
	b := types.StringToAddress("0x1000000000000000000000000000000000000002")
	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(a), validators.NewECDSAValidator(b))

	require.False(t, HasMacroEpochStakeSnapshot(txn, epoch, vals))
	require.False(t, HasMacroEpochStakeSnapshot(txn, epoch, nil))
	require.False(t, HasMacroEpochStakeSnapshot(txn, epoch, validators.NewECDSAValidatorSet()))

	// validator set can exist while stakes are still missing
	require.NoError(t, StoreMacroEpochValidatorSet(txn, epoch, vals))
	require.False(t, HasMacroEpochStakeSnapshot(txn, epoch, vals))

	// zero stake should be treated as missing/invalid presence
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, a), bigToHash(big.NewInt(100)))
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, b), bigToHash(big.NewInt(0)))
	require.False(t, HasMacroEpochStakeSnapshot(txn, epoch, vals))

	// complete positive stakes => strict presence true
	require.NoError(t, StoreMacroEpochStakeSnapshot(txn, epoch, map[types.Address]*big.Int{
		a: big.NewInt(100),
		b: big.NewInt(200),
	}))
	require.True(t, HasMacroEpochStakeSnapshot(txn, epoch, vals))
}

func TestMacroEpochNoSlashMode_RoundtripAndMissing(t *testing.T) {
	txn := macroTestTransition(t)
	epochTrue := uint64(21)
	epochFalse := uint64(22)
	epochMissing := uint64(23)

	require.NoError(t, StoreMacroEpochNoSlashMode(txn, epochTrue, true))
	noSlash, ok := LoadMacroEpochNoSlashMode(txn, epochTrue)
	require.True(t, ok)
	require.True(t, noSlash)

	require.NoError(t, StoreMacroEpochNoSlashMode(txn, epochFalse, false))
	noSlash, ok = LoadMacroEpochNoSlashMode(txn, epochFalse)
	require.True(t, ok)
	require.False(t, noSlash)

	noSlash, ok = LoadMacroEpochNoSlashMode(txn, epochMissing)
	require.False(t, ok)
	require.False(t, noSlash)
}

func TestEnsureMacroEpochSnapshots(t *testing.T) {
	txn := macroTestTransition(t)
	epoch := uint64(11)
	a := types.StringToAddress("0x1000000000000000000000000000000000000001")
	b := types.StringToAddress("0x1000000000000000000000000000000000000002")
	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(a), validators.NewECDSAValidator(b))
	stakes := map[types.Address]*big.Int{a: big.NewInt(100), b: big.NewInt(200)}

	require.NoError(t, EnsureMacroEpochSnapshots(txn, epoch, vals, stakes))
	loadedVals, ok, err := LoadMacroEpochValidatorSet(txn, epoch, validators.ECDSAValidatorType)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, loadedVals.Equal(vals))
	loadedStakes, ok, err := LoadMacroEpochStakeSnapshot(txn, epoch, vals)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "100", loadedStakes[a].String())

	require.NoError(t, EnsureMacroEpochSnapshots(txn, epoch, vals, stakes))

	err = EnsureMacroEpochSnapshots(txn, epoch, validators.NewECDSAValidatorSet(validators.NewECDSAValidator(a)), stakes)
	require.Error(t, err)

	err = EnsureMacroEpochSnapshots(txn, epoch, vals, map[types.Address]*big.Int{a: big.NewInt(999), b: big.NewInt(200)})
	require.Error(t, err)

	err = EnsureMacroEpochSnapshots(txn, epoch+1, vals, map[types.Address]*big.Int{a: big.NewInt(100)})
	require.Error(t, err)
}

func TestEnsureMacroEpochSnapshots_PersistAfterCommitAndReload(t *testing.T) {
	ex, root := macroTestExecutorAndRoot(t)
	tx, err := ex.BeginTxn(root, &types.Header{Number: 1, StateRoot: root}, types.ZeroAddress)
	require.NoError(t, err)

	epoch := uint64(2)
	a := types.StringToAddress("0x1000000000000000000000000000000000000001")
	b := types.StringToAddress("0x1000000000000000000000000000000000000002")
	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(a), validators.NewECDSAValidator(b))
	stakes := map[types.Address]*big.Int{a: big.NewInt(100), b: big.NewInt(200)}
	require.NoError(t, EnsureMacroEpochSnapshots(tx, epoch, vals, stakes))

	_, newRoot, err := tx.Commit()
	require.NoError(t, err)
	tx2, err := ex.BeginTxn(newRoot, &types.Header{Number: 2, StateRoot: newRoot}, types.ZeroAddress)
	require.NoError(t, err)

	loadedVals, ok, err := LoadMacroEpochValidatorSet(tx2, epoch, validators.ECDSAValidatorType)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, loadedVals.Equal(vals))
	loadedStakes, ok, err := LoadMacroEpochStakeSnapshot(tx2, epoch, vals)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "100", loadedStakes[a].String())
	require.Equal(t, "200", loadedStakes[b].String())
}

func TestStoreMacroEpochSnapshots_PersistAfterCommitAndReload(t *testing.T) {
	ex, root := macroTestExecutorAndRoot(t)
	tx, err := ex.BeginTxn(root, &types.Header{Number: 1, StateRoot: root}, types.ZeroAddress)
	require.NoError(t, err)

	epoch := uint64(2)
	a := types.StringToAddress("0x1000000000000000000000000000000000000001")
	b := types.StringToAddress("0x1000000000000000000000000000000000000002")
	vals := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(a), validators.NewECDSAValidator(b))
	require.NoError(t, StoreMacroEpochValidatorSet(tx, epoch, vals))
	require.NoError(t, StoreMacroEpochStakeSnapshot(tx, epoch, map[types.Address]*big.Int{a: big.NewInt(111), b: big.NewInt(222)}))

	_, newRoot, err := tx.Commit()
	require.NoError(t, err)
	tx2, err := ex.BeginTxn(newRoot, &types.Header{Number: 2, StateRoot: newRoot}, types.ZeroAddress)
	require.NoError(t, err)

	loadedVals, ok, err := LoadMacroEpochValidatorSet(tx2, epoch, validators.ECDSAValidatorType)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, loadedVals.Equal(vals))
	loadedStakes, ok, err := LoadMacroEpochStakeSnapshot(tx2, epoch, vals)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "111", loadedStakes[a].String())
	require.Equal(t, "222", loadedStakes[b].String())
}
