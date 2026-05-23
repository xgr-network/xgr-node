package ibft

import (
	"errors"
	"math/big"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/consensus/ibft/fork"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/state"
	itrie "github.com/xgr-network/xgr-node/state/immutable-trie"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

type votingPowerForkManager struct {
	active             func(uint64) bool
	getStakeSnapshotFn func(uint64, validators.Validators) (map[types.Address]*big.Int, error)
	getStakeCalls      int
}

func (m *votingPowerForkManager) Initialize() error                       { return nil }
func (m *votingPowerForkManager) Close() error                            { return nil }
func (m *votingPowerForkManager) GetSigner(uint64) (signer.Signer, error) { return nil, nil }
func (m *votingPowerForkManager) GetValidatorStore(uint64) (fork.ValidatorStore, error) { return nil, nil }
func (m *votingPowerForkManager) GetValidators(uint64) (validators.Validators, error) { return nil, nil }
func (m *votingPowerForkManager) GetValidatorStakeSnapshot(height uint64, set validators.Validators) (map[types.Address]*big.Int, error) {
	m.getStakeCalls++
	if m.getStakeSnapshotFn != nil { return m.getStakeSnapshotFn(height, set) }
	return nil, nil
}
func (m *votingPowerForkManager) GetHooks(uint64) fork.HooksInterface { return nil }
func (m *votingPowerForkManager) IsPosActive(h uint64) bool { return m.active != nil && m.active(h) }

func newEffectivePowerExecutor(t *testing.T) (*state.Executor, types.Hash) {
	t.Helper()
	st := itrie.NewState(itrie.NewMemoryStorage())
	ex := state.NewExecutor(&chain.Params{Forks: chain.AllForksEnabled, BurnContract: map[uint64]types.Address{0: types.ZeroAddress}}, st, hclog.NewNullLogger())
	root, err := ex.WriteGenesis(nil, types.Hash{})
	require.NoError(t, err)
	ex.GetHash = func(*types.Header) state.GetHashByNumber { return func(uint64) types.Hash { return root } }
	return ex, root
}

func newBareVotingPowerTestTransition(t *testing.T, block uint64) *state.Transition {
	t.Helper()
	ex, root := newEffectivePowerExecutor(t)
	tx, err := ex.BeginTxn(root, &types.Header{Number: block, StateRoot: root}, types.ZeroAddress)
	require.NoError(t, err)
	return tx
}

func setNominalAndEffectiveWeight(tx *state.Transition, addr types.Address, nominal, effective uint64) {
	nomKey := types.BytesToHash(crypto.Keccak256([]byte("xgr.uptime.nominal.weight"), addr.Bytes()))
	effKey := types.BytesToHash(crypto.Keccak256([]byte("xgr.uptime.effective.weight"), addr.Bytes()))
	tx.Txn().SetState(pos.PosSysAddr, nomKey, types.BytesToHash(new(big.Int).SetUint64(nominal).Bytes()))
	tx.Txn().SetState(pos.PosSysAddr, effKey, types.BytesToHash(new(big.Int).SetUint64(effective).Bytes()))
}

func TestEffectiveVotingPowerSnapshot_UnitModeDoesNotReadStakeSnapshot(t *testing.T) {
	ex, root := newEffectivePowerExecutor(t)
	valSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000002")),
	)
	fm := &votingPowerForkManager{active: func(uint64) bool { return false }, getStakeSnapshotFn: func(uint64, validators.Validators) (map[types.Address]*big.Int, error) {
		return nil, errors.New("must not be called")
	}}
	backend := &backendIBFT{executor: ex, forkManager: fm, uptimeCfg: pos.UptimeConfig{MicroEpochNominalWeight: 10_000}}
	tx, err := ex.BeginTxn(root, &types.Header{Number: 1, StateRoot: root}, types.ZeroAddress)
	require.NoError(t, err)
	setNominalAndEffectiveWeight(tx, valSet.At(0).Addr(), 10_000, 10_000)
	setNominalAndEffectiveWeight(tx, valSet.At(1).Addr(), 10_000, 10_000)
	_, weightedRoot, err := tx.Commit()
	require.NoError(t, err)
	powers, _, err := backend.effectiveVotingPowerSnapshot(2, valSet, &types.Header{Number: 1, StateRoot: weightedRoot})
	require.NoError(t, err)
	require.Equal(t, 0, fm.getStakeCalls)
	for i := 0; i < valSet.Len(); i++ { require.Equal(t, uint64(1), powers[types.AddressToString(valSet.At(uint64(i)).Addr())].Uint64()) }
}

func TestEffectiveVotingPowerSnapshot_WeightedModeUsesStakeSnapshot(t *testing.T) {
	ex, root := newEffectivePowerExecutor(t)
	a := types.StringToAddress("0x1000000000000000000000000000000000000001")
	b := types.StringToAddress("0x1000000000000000000000000000000000000002")
	valSet := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(a), validators.NewECDSAValidator(b))
	fm := &votingPowerForkManager{active: func(uint64) bool { return true }, getStakeSnapshotFn: func(uint64, validators.Validators) (map[types.Address]*big.Int, error) {
		return map[types.Address]*big.Int{a: big.NewInt(100), b: big.NewInt(200)}, nil
	}}
	backend := &backendIBFT{executor: ex, forkManager: fm, uptimeCfg: pos.UptimeConfig{MicroEpochNominalWeight: 10_000}}
	tx, err := ex.BeginTxn(root, &types.Header{Number: 1, StateRoot: root}, types.ZeroAddress)
	require.NoError(t, err)
	setNominalAndEffectiveWeight(tx, a, 10_000, 5_000)
	setNominalAndEffectiveWeight(tx, b, 10_000, 10_000)
	_, weightedRoot, err := tx.Commit()
	require.NoError(t, err)
	powers, _, err := backend.effectiveVotingPowerSnapshot(2, valSet, &types.Header{Number: 1, StateRoot: weightedRoot})
	require.NoError(t, err)
	require.Equal(t, 1, fm.getStakeCalls)
	require.Equal(t, uint64(100), powers[types.AddressToString(a)].Uint64())
	require.Equal(t, uint64(200), powers[types.AddressToString(b)].Uint64())
}

func TestEffectiveVotingPowerSnapshot_StakeSnapshotErrorIsPropagated(t *testing.T) {
	ex, root := newEffectivePowerExecutor(t)
	valSet := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001")))
	fm := &votingPowerForkManager{active: func(uint64) bool { return true }, getStakeSnapshotFn: func(uint64, validators.Validators) (map[types.Address]*big.Int, error) {
		return nil, errors.New("stake snapshot failed")
	}}
	backend := &backendIBFT{executor: ex, forkManager: fm, uptimeCfg: pos.UptimeConfig{MicroEpochNominalWeight: 10_000}}
	tx, err := ex.BeginTxn(root, &types.Header{Number: 1, StateRoot: root}, types.ZeroAddress)
	require.NoError(t, err)
	_, weightedRoot, err := tx.Commit()
	require.NoError(t, err)
	_, _, err = backend.effectiveVotingPowerSnapshot(2, valSet, &types.Header{Number: 1, StateRoot: weightedRoot})
	require.Error(t, err)
	require.Contains(t, err.Error(), "stake snapshot failed")
}

func TestEffectiveVotingPowerSnapshot_CutoverParentPoAUsesUnitVoting(t *testing.T) {
	ex, root := newEffectivePowerExecutor(t)
	a := types.StringToAddress("0x1000000000000000000000000000000000000001")
	b := types.StringToAddress("0x1000000000000000000000000000000000000002")
	valSet := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(a), validators.NewECDSAValidator(b))
	fm := &votingPowerForkManager{
		active: func(h uint64) bool { return h >= 10 },
		getStakeSnapshotFn: func(uint64, validators.Validators) (map[types.Address]*big.Int, error) {
			return nil, errors.New("must not be called on cutover block with PoA parent")
		},
	}
	backend := &backendIBFT{executor: ex, forkManager: fm, uptimeCfg: pos.UptimeConfig{MicroEpochNominalWeight: 10_000}}
	tx, err := ex.BeginTxn(root, &types.Header{Number: 9, StateRoot: root}, types.ZeroAddress)
	require.NoError(t, err)
	setNominalAndEffectiveWeight(tx, a, 10_000, 10_000)
	setNominalAndEffectiveWeight(tx, b, 10_000, 10_000)
	_, weightedRoot, err := tx.Commit()
	require.NoError(t, err)

	// height=10 (cutover), parent=9 is PoA => unit voting and no stake snapshot call
	powers, _, err := backend.effectiveVotingPowerSnapshot(10, valSet, &types.Header{Number: 9, StateRoot: weightedRoot})
	require.NoError(t, err)
	require.Equal(t, 0, fm.getStakeCalls)
	require.Equal(t, uint64(1), powers[types.AddressToString(a)].Uint64())
	require.Equal(t, uint64(1), powers[types.AddressToString(b)].Uint64())
}
