package ibft

import (
	"encoding/binary"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/consensus/ibft/fork"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/helper/common"
	"github.com/xgr-network/xgr-node/helper/keccak"
	stakingHelper "github.com/xgr-network/xgr-node/helper/staking"
	"github.com/xgr-network/xgr-node/state"
	itrie "github.com/xgr-network/xgr-node/state/immutable-trie"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
	contractstore "github.com/xgr-network/xgr-node/validators/store/contract"
)

func posKeyLastProposer() types.Hash {
	return types.BytesToHash(crypto.Keccak256([]byte("xgr.pos.last.proposer")))
}
func addressToHash(addr types.Address) types.Hash {
	var out types.Hash
	copy(out[12:], addr[:])
	return out
}

type sequenceSigner struct {
	extra     *signer.IstanbulExtra
	proposers map[uint64]types.Address
}

func (m *sequenceSigner) Type() validators.ValidatorType                                   { return validators.ECDSAValidatorType }
func (m *sequenceSigner) Address() types.Address                                           { return types.ZeroAddress }
func (m *sequenceSigner) InitIBFTExtra(*types.Header, validators.Validators, signer.Seals) {}
func (m *sequenceSigner) GetIBFTExtra(*types.Header) (*signer.IstanbulExtra, error) {
	return m.extra, nil
}
func (m *sequenceSigner) GetValidators(*types.Header) (validators.Validators, error) {
	return m.extra.Validators, nil
}
func (m *sequenceSigner) WriteProposerSeal(h *types.Header) (*types.Header, error) { return h, nil }
func (m *sequenceSigner) EcrecoverFromHeader(h *types.Header) (types.Address, error) {
	return m.proposers[h.Number], nil
}
func (m *sequenceSigner) CreateCommittedSeal([]byte) ([]byte, error) { return nil, nil }
func (m *sequenceSigner) VerifyCommittedSeal(validators.Validators, types.Address, []byte, []byte) error {
	return nil
}
func (m *sequenceSigner) WriteCommittedSeals(*types.Header, uint64, map[types.Address][]byte) (*types.Header, error) {
	return nil, nil
}
func (m *sequenceSigner) VerifyCommittedSeals(types.Hash, signer.Seals, validators.Validators, int) error {
	return nil
}
func (m *sequenceSigner) VerifyParentCommittedSeals(types.Hash, *types.Header, validators.Validators, int, bool) error {
	return nil
}
func (m *sequenceSigner) SignIBFTMessage([]byte) ([]byte, error) { return nil, nil }
func (m *sequenceSigner) EcrecoverFromIBFTMessage([]byte, []byte) (types.Address, error) {
	return types.ZeroAddress, nil
}
func (m *sequenceSigner) CalculateHeaderHash(*types.Header) (types.Hash, error) {
	return types.ZeroHash, nil
}
func (m *sequenceSigner) FilterHeaderForHash(h *types.Header) (*types.Header, error) { return h, nil }

func TestSnapshotVotingPowers_PrePoS_UnitBasedWithoutStakeState(t *testing.T) {
	t.Parallel()
	pool := newTesterAccountPool(t, 3)
	backend := &backendIBFT{}

	powers, snap, err := backend.snapshotVotingPowers(11, 10, pool.ValidatorSet(), nil, false)
	require.NoError(t, err)
	require.Equal(t, "3", snap.totalVotingPower)
	for i := 0; i < pool.ValidatorSet().Len(); i++ {
		addr := pool.ValidatorSet().At(uint64(i)).Addr()
		require.Equal(t, uint64(1), powers[types.AddressToString(addr)].Uint64())
	}
}

func TestSnapshotVotingPowers_StakeWeightedInactive_DoesNotReadStakeState(t *testing.T) {
	t.Parallel()
	valSet := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001")))
	stateTx := newBareVotingPowerTestTransition(t, 10)
	backend := &backendIBFT{uptimeCfg: pos.UptimeConfig{MicroEpochNominalWeight: 10_000}}

	powers, snap, err := backend.snapshotVotingPowers(11, 0, valSet, stateTx, false)
	require.NoError(t, err)
	require.Equal(t, "1", snap.totalVotingPower)
	require.Equal(t, uint64(1), powers[types.AddressToString(valSet.At(0).Addr())].Uint64())
}

func TestSnapshotVotingPowers_BootstrapStakeDecayFloorsToOne(t *testing.T) {
	t.Parallel()
	val := types.StringToAddress("0x10000000000000000000000000000000000000f1")
	valSet := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(val))
	stateTx := newVotingPowerTestTransition(t, valSet, 10)
	require.False(t, contractstore.IsEmergencyModeActive(stateTx))
	setNominalAndEffectiveWeight(stateTx, val, 10_000, 1)

	backend := &backendIBFT{uptimeCfg: pos.UptimeConfig{MicroEpochSize: 1, MicroEpochNominalWeight: 10_000}}
	powers, snap, err := backend.snapshotVotingPowers(11, 10, valSet, stateTx, true)
	require.NoError(t, err)
	require.Equal(t, uint64(1), powers[types.AddressToString(val)].Uint64())
	require.Equal(t, "1", snap.totalVotingPower)
}

func TestSnapshotVotingPowers_BootstrapStakeDecayTotalVotingPowerNonZero(t *testing.T) {
	t.Parallel()
	valSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x10000000000000000000000000000000000000f2")),
		validators.NewECDSAValidator(types.StringToAddress("0x10000000000000000000000000000000000000f3")),
	)
	stateTx := newVotingPowerTestTransition(t, valSet, 10)
	setStakingConfigForVotingPowerTest(t, stateTx, 2, 1, uint64(valSet.Len()))
	for i := 0; i < valSet.Len(); i++ {
		setNominalAndEffectiveWeight(stateTx, valSet.At(uint64(i)).Addr(), 10_000, 1)
	}

	backend := &backendIBFT{uptimeCfg: pos.UptimeConfig{MicroEpochSize: 1, MicroEpochNominalWeight: 10_000}}
	powers, snap, err := backend.snapshotVotingPowers(11, 10, valSet, stateTx, true)
	require.NoError(t, err)
	require.Equal(t, "2", snap.totalVotingPower)
	for i := 0; i < valSet.Len(); i++ {
		require.Equal(t, uint64(1), powers[types.AddressToString(valSet.At(uint64(i)).Addr())].Uint64())
	}
}

func TestSnapshotVotingPowers_EmergencyModeUsesDeterministicUnitVotingPower(t *testing.T) {
	t.Parallel()
	valSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x10000000000000000000000000000000000000f4")),
		validators.NewECDSAValidator(types.StringToAddress("0x10000000000000000000000000000000000000f5")),
	)
	stateTx := newVotingPowerTestTransition(t, valSet, 10)
	setStakingConfigForVotingPowerTest(t, stateTx, 2, 1, uint64(valSet.Len()))
	setValidatorSelfStake(t, stateTx, valSet.At(0).Addr(), 10)
	setValidatorSelfStake(t, stateTx, valSet.At(1).Addr(), 1_000_000)
	for i := 0; i < valSet.Len(); i++ {
		setNominalAndEffectiveWeight(stateTx, valSet.At(uint64(i)).Addr(), 10_000, 5_000)
	}
	require.True(t, contractstore.IsEmergencyModeActive(stateTx))

	backend := &backendIBFT{uptimeCfg: pos.UptimeConfig{MicroEpochSize: 1, MicroEpochNominalWeight: 10_000}}
	powers, snap, err := backend.snapshotVotingPowers(11, 10, valSet, stateTx, true)
	require.NoError(t, err)
	require.Equal(t, "2", snap.totalVotingPower)
	for i := 0; i < valSet.Len(); i++ {
		require.Equal(t, uint64(1), powers[types.AddressToString(valSet.At(uint64(i)).Addr())].Uint64())
	}
}

func TestSnapshotVotingPowers_FirstPoSBlock_UsesParentStateStake(t *testing.T) {
	t.Parallel()
	valSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000002")),
	)
	stateTx := newVotingPowerTestTransition(t, valSet, 9)
	a := valSet.At(0).Addr()
	b := valSet.At(1).Addr()
	setValidatorSelfStake(t, stateTx, a, 10_000)
	setValidatorSelfStake(t, stateTx, b, 30_000)
	setNominalAndEffectiveWeight(stateTx, a, 10_000, 10_000)
	setNominalAndEffectiveWeight(stateTx, b, 10_000, 10_000)

	backend := &backendIBFT{uptimeCfg: pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000}, forkManager: &votingPowerForkManager{active: func(h uint64) bool { return h >= 10 }}}
	res, snap, err := backend.snapshotVotingPowers(10, 9, valSet, stateTx, true)
	require.NoError(t, err)
	require.Equal(t, uint64(10_000), res[types.AddressToString(a)].Uint64())
	require.Equal(t, uint64(30_000), res[types.AddressToString(b)].Uint64())
	require.Equal(t, "40000", snap.totalVotingPower)
	require.Equal(t, weightedQuorumThreshold(big.NewInt(40_000)).String(), snap.quorumThreshold)
}

func TestSnapshotVotingPowers_PoS_MissingStakeIsHardError(t *testing.T) {
	t.Parallel()
	val := types.StringToAddress("0x1000000000000000000000000000000000000001")
	valSet := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(val))
	stateTx := newBareVotingPowerTestTransition(t, 10)
	setValidatorSelfStakeRaw(t, stateTx, val, 0, 0, types.ZeroAddress)
	setNominalAndEffectiveWeight(stateTx, val, 10_000, 10_000)
	backend := &backendIBFT{uptimeCfg: pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000}, forkManager: &votingPowerForkManager{active: func(uint64) bool { return true }}}

	_, _, err := backend.snapshotVotingPowers(10, 9, valSet, stateTx, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing effective stake")
}

func TestSnapshotVotingPowers_UsesRawSelfStake_WithoutJoinMaturity(t *testing.T) {
	t.Parallel()
	val := types.StringToAddress("0x1000000000000000000000000000000000000001")
	valSet := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(val))
	stateTx := newBareVotingPowerTestTransition(t, 10)
	setValidatorSelfStakeRaw(t, stateTx, val, 10_000, 10, val)
	setNominalAndEffectiveWeight(stateTx, val, 10_000, 10_000)
	backend := &backendIBFT{uptimeCfg: pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000}, forkManager: &votingPowerForkManager{active: func(uint64) bool { return true }}}

	res, _, err := backend.snapshotVotingPowers(10, 9, valSet, stateTx, true)
	require.NoError(t, err)
	require.Equal(t, uint64(10_000), res[types.AddressToString(val)].Uint64())
}

func TestSnapshotVotingPowers_UsesRawSelfStake_WithoutJoinMetadata(t *testing.T) {
	t.Parallel()
	val := types.StringToAddress("0x1000000000000000000000000000000000000001")
	valSet := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(val))
	stateTx := newBareVotingPowerTestTransition(t, 10)
	setValidatorSelfStakeRaw(t, stateTx, val, 42, 0, val)
	setNominalAndEffectiveWeight(stateTx, val, 10_000, 10_000)
	backend := &backendIBFT{uptimeCfg: pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000}, forkManager: &votingPowerForkManager{active: func(uint64) bool { return true }}}

	res, _, err := backend.snapshotVotingPowers(10, 9, valSet, stateTx, true)
	require.NoError(t, err)
	require.Equal(t, uint64(42), res[types.AddressToString(val)].Uint64())
}

func TestSnapshotVotingPowers_ActiveDelegationIncludedInactiveExcluded(t *testing.T) {
	t.Parallel()
	valSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000002")),
	)
	stateTx := newVotingPowerTestTransition(t, valSet, 10)
	validatorA := valSet.At(0).Addr()
	validatorB := valSet.At(1).Addr()
	activeDelegator := types.StringToAddress("0x2000000000000000000000000000000000000001")
	inactiveDelegator := types.StringToAddress("0x2000000000000000000000000000000000000002")

	setValidatorSelfStake(t, stateTx, validatorA, 200)
	setValidatorSelfStake(t, stateTx, validatorB, 500)
	setValidatorDelegatedStakeTotals(t, stateTx, validatorA, 700, 300)
	setDelegatorStakeForValidator(t, stateTx, validatorA, activeDelegator, 300, 1, 0, true)
	// Raw-only delegated stake is present in the validator's delegation snapshot,
	// but it joined at the snapshot height and is therefore not epoch-effective yet.
	setDelegatorStakeForValidator(t, stateTx, validatorA, inactiveDelegator, 400, 9, 0, false)
	setNominalAndEffectiveWeight(stateTx, validatorA, 10_000, 10_000)
	setNominalAndEffectiveWeight(stateTx, validatorB, 10_000, 10_000)

	backend := &backendIBFT{
		uptimeCfg:   pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000},
		forkManager: &votingPowerForkManager{active: func(uint64) bool { return true }},
	}
	powers, snap, err := backend.snapshotVotingPowers(10, 9, valSet, stateTx, true)
	require.NoError(t, err)

	validatorAPower := powers[types.AddressToString(validatorA)]
	validatorBPower := powers[types.AddressToString(validatorB)]
	require.Equal(t, uint64(500), validatorAPower.Uint64(), "validatorA must include self stake plus active delegated stake")
	require.Equal(t, validatorBPower, validatorAPower, "validatorA self 200 + active delegation 300 must equal validatorB self 500")
	require.NotEqual(t, uint64(900), validatorAPower.Uint64(), "inactive/raw-only delegated stake must not contribute")
	require.Equal(t, uint64(500), validatorBPower.Uint64())
	require.Equal(t, "1000", snap.totalVotingPower)
	require.Equal(t, weightedQuorumThreshold(big.NewInt(1_000)).String(), snap.quorumThreshold)

	setValidatorDelegatedStakeTotals(t, stateTx, validatorA, 700, 0)
	setStakerStakeForValidator(t, stateTx, activeDelegator, validatorA, 300, 9, 0, false)
	powers, snap, err = backend.snapshotVotingPowers(10, 9, valSet, stateTx, true)
	require.NoError(t, err)

	validatorAPower = powers[types.AddressToString(validatorA)]
	validatorBPower = powers[types.AddressToString(validatorB)]
	require.Equal(t, uint64(200), validatorAPower.Uint64(), "validatorA must fall back to self stake when all raw delegation is inactive")
	require.Equal(t, uint64(500), validatorBPower.Uint64(), "validatorB must be unchanged")
	require.Equal(t, "700", snap.totalVotingPower)
	require.Equal(t, weightedQuorumThreshold(big.NewInt(700)).String(), snap.quorumThreshold)
}

func TestSnapshotVotingPowers_MidEpochStakeChangesDoNotAffectCurrentPower(t *testing.T) {
	t.Parallel()
	valSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000002")),
	)
	validatorA := valSet.At(0).Addr()
	validatorB := valSet.At(1).Addr()
	delegatorA := types.StringToAddress("0x2000000000000000000000000000000000000001")

	st := itrie.NewState(itrie.NewMemoryStorage())
	ex := state.NewExecutor(&chain.Params{Forks: chain.AllForksEnabled, BurnContract: map[uint64]types.Address{0: types.ZeroAddress}}, st, hclog.NewNullLogger())
	genesisRoot, err := ex.WriteGenesis(nil, types.Hash{})
	require.NoError(t, err)
	ex.GetHash = func(h *types.Header) state.GetHashByNumber {
		return func(i uint64) types.Hash { return genesisRoot }
	}
	beginTxn := func(root types.Hash, block uint64) *state.Transition {
		t.Helper()
		tx, err := ex.BeginTxn(root, &types.Header{Number: block}, types.ZeroAddress)
		require.NoError(t, err)
		return tx
	}
	commitTxn := func(tx *state.Transition) types.Hash {
		t.Helper()
		_, root, err := tx.Commit()
		require.NoError(t, err)
		return root
	}

	currentTx := beginTxn(genesisRoot, 15)
	contractState, err := stakingHelper.PredeployStakingSC(valSet, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: uint64(valSet.Len()), EpochSize: 20})
	require.NoError(t, err)
	require.NoError(t, currentTx.SetAccountDirectly(staking.AddrStakingContract, contractState))
	setValidatorSelfStake(t, currentTx, validatorA, 500)
	setValidatorSelfStake(t, currentTx, validatorB, 500)
	setNominalAndEffectiveWeight(currentTx, validatorA, 10_000, 10_000)
	setNominalAndEffectiveWeight(currentTx, validatorB, 10_000, 10_000)
	currentRoot := commitTxn(currentTx)
	currentParent := &types.Header{Number: 15, StateRoot: currentRoot}

	backend := &backendIBFT{
		executor:    ex,
		uptimeCfg:   pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000},
		forkManager: &votingPowerForkManager{active: func(uint64) bool { return true }},
	}
	assertVotingPower := func(parent *types.Header, height uint64, wantA uint64, wantB uint64, wantTotal uint64) {
		t.Helper()
		powers, snap, err := backend.effectiveVotingPowerSnapshot(height, valSet, parent)
		require.NoError(t, err)
		require.Equal(t, wantA, powers[types.AddressToString(validatorA)].Uint64())
		require.Equal(t, wantB, powers[types.AddressToString(validatorB)].Uint64())
		require.Equal(t, new(big.Int).SetUint64(wantTotal).String(), snap.totalVotingPower)
		require.Equal(t, weightedQuorumThreshold(new(big.Int).SetUint64(wantTotal)).String(), snap.quorumThreshold)
	}

	assertVotingPower(currentParent, 16, 500, 500, 1_000)

	// Commit live StakingV2 state changes on top of the current parent root. The
	// current voting-power path must keep using currentParent.StateRoot, while the
	// next parent root models the first snapshot after the epoch boundary.
	nextTx := beginTxn(currentRoot, 20)
	setValidatorSelfStakeRaw(t, nextTx, validatorA, 900, 1, validatorA)
	setValidatorDelegatedStakeTotals(t, nextTx, validatorA, 600, 600)
	setDelegatorStakeForValidator(t, nextTx, validatorA, delegatorA, 600, 15, 0, true)
	nextRoot := commitTxn(nextTx)
	nextParent := &types.Header{Number: 20, StateRoot: nextRoot}

	assertVotingPower(currentParent, 16, 500, 500, 1_000)
	assertVotingPower(nextParent, 21, 1_500, 500, 2_000)
}

func TestPoS_Slashing_ReducesValidatorSelfStakeAndVotingPowerWithoutDelegationDrift(t *testing.T) {
	validatorA := types.StringToAddress("0x1000000000000000000000000000000000000101")
	validatorB := types.StringToAddress("0x1000000000000000000000000000000000000102")
	delegatorA := types.StringToAddress("0x2000000000000000000000000000000000000101")
	valSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(validatorA),
		validators.NewECDSAValidator(validatorB),
	)
	const epochSize uint64 = 10
	const slashEpoch uint64 = 2

	tx := newVotingPowerTestTransitionWithFeePoolSplit(t, valSet, 20)
	setStakingConfigForVotingPowerTest(t, tx, epochSize, 0, uint64(valSet.Len()))
	setValidatorSelfStake(t, tx, validatorA, 1_000)
	setValidatorSelfStake(t, tx, validatorB, 1_000)
	setDelegatorStakeForValidator(t, tx, validatorA, delegatorA, 500, 1, 0, true)
	setValidatorDelegatedStakeTotals(t, tx, validatorA, 500, 500)
	setNominalAndEffectiveWeight(tx, validatorA, 10_000, 10_000)
	setNominalAndEffectiveWeight(tx, validatorB, 10_000, 10_000)
	tx.Txn().SetBalance(staking.AddrStakingContract, big.NewInt(2_500))

	backend := &backendIBFT{uptimeCfg: pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000}, forkManager: &votingPowerForkManager{active: func(uint64) bool { return true }}}
	assertSnapshot := func(height, parent uint64, wantA, wantB, wantTotal uint64) {
		t.Helper()
		powers, snap, err := backend.snapshotVotingPowers(height, parent, valSet, tx, true)
		require.NoError(t, err)
		require.Equal(t, wantA, powers[types.AddressToString(validatorA)].Uint64())
		require.Equal(t, wantB, powers[types.AddressToString(validatorB)].Uint64())
		require.Equal(t, new(big.Int).SetUint64(wantTotal).String(), snap.totalVotingPower)
		require.Equal(t, weightedQuorumThreshold(new(big.Int).SetUint64(wantTotal)).String(), snap.quorumThreshold)
	}

	require.Equal(t, uint64(1_000), readStakerAmountForVotingPowerTest(tx, validatorA))
	require.Equal(t, uint64(500), readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 8))
	require.Equal(t, uint64(500), readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 9))
	require.Equal(t, uint64(500), readStakerAmountForVotingPowerTest(tx, delegatorA))
	assertSnapshot(20, 19, 1_500, 1_000, 2_500)

	setPoSEpochValidatorsSnapshotForVotingPowerTest(t, tx, slashEpoch, valSet)
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.stake.snapshot", slashEpoch, validatorA, big.NewInt(1_500))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.stake.snapshot", slashEpoch, validatorB, big.NewInt(1_000))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.staker.stake.snapshot", slashEpoch, validatorA, big.NewInt(1_000))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.staker.stake.snapshot", slashEpoch, delegatorA, big.NewInt(500))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.staker.stake.snapshot", slashEpoch, validatorB, big.NewInt(1_000))
	setPoSU64StateForVotingPowerTest(tx, "xgr.pos.prop.slots", slashEpoch, validatorA, 1)
	setPoSU64StateForVotingPowerTest(tx, "xgr.pos.prop.miss", slashEpoch, validatorA, 1)
	setPoSU64StateForVotingPowerTest(tx, "xgr.pos.prop.slots", slashEpoch, validatorB, 1)
	setPoSU64StateForVotingPowerTest(tx, "xgr.pos.prop.miss", slashEpoch, validatorB, 0)

	header := &types.Header{Number: 20}
	headerSigner := &sequenceSigner{extra: &signer.IstanbulExtra{Validators: valSet}}
	require.NoError(t, pos.FinalizeEpoch(header, epochSize, pos.UptimeConfig{}, headerSigner, tx))

	logs := tx.Txn().Logs()
	assertStakerSlashedLogForVotingPowerTest(t, logs, slashEpoch, validatorA, validatorA, 1, 10, 1_000, 1_000, 990)
	assertStakerSlashedLogForVotingPowerTest(t, logs, slashEpoch, validatorA, delegatorA, 2, 5, 500, 500, 495)
	require.Zero(t, readPoSU64StateForVotingPowerTest(tx, "xgr.pos.slashed", slashEpoch, validatorA))
	require.Zero(t, readPoSBigStateForVotingPowerTest(tx, "xgr.pos.slash.amount", slashEpoch, validatorA).Sign())
	require.Equal(t, uint64(990), readStakerAmountForVotingPowerTest(tx, validatorA))
	require.Equal(t, uint64(1_000), readStakerAmountForVotingPowerTest(tx, validatorB))

	// Current production slashing intentionally allocates the epoch slash across
	// effective self and delegated positions; these assertions ensure the raw and
	// active delegation aggregates track the delegator staker amount exactly.
	require.Equal(t, uint64(495), readStakerAmountForVotingPowerTest(tx, delegatorA))
	require.Equal(t, uint64(495), readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 8))
	require.Equal(t, uint64(495), readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 9))
	require.Positive(t, readStakerAmountForVotingPowerTest(tx, validatorA))
	require.Positive(t, readStakerAmountForVotingPowerTest(tx, delegatorA))
	require.False(t, isStakerActiveForVotingPowerTest(tx, validatorA), "zero-uptime validator is deactivated by finalize")
	assertSnapshot(21, 20, 1_485, 1_000, 2_485)
}

func TestPoS_Slashing_InactiveDelegationAccounting(t *testing.T) {
	validatorA := types.StringToAddress("0x1000000000000000000000000000000000000121")
	validatorB := types.StringToAddress("0x1000000000000000000000000000000000000122")
	activeDelegator := types.StringToAddress("0x2000000000000000000000000000000000000121")
	inactiveDelegator := types.StringToAddress("0x2000000000000000000000000000000000000122")
	valSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(validatorA),
		validators.NewECDSAValidator(validatorB),
	)
	const epochSize uint64 = 10
	const slashEpoch uint64 = 2

	tx := newVotingPowerTestTransitionWithFeePoolSplit(t, valSet, 20)
	setStakingConfigForVotingPowerTest(t, tx, epochSize, 0, uint64(valSet.Len()))
	setValidatorSelfStake(t, tx, validatorA, 1_000)
	setValidatorSelfStake(t, tx, validatorB, 1_000)
	setDelegatorStakeForValidator(t, tx, validatorA, activeDelegator, 500, 1, 0, true)
	setDelegatorStakeForValidator(t, tx, validatorA, inactiveDelegator, 300, 1, 9, false)
	setValidatorDelegatedStakeTotals(t, tx, validatorA, 800, 500)
	setNominalAndEffectiveWeight(tx, validatorA, 10_000, 10_000)
	setNominalAndEffectiveWeight(tx, validatorB, 10_000, 10_000)
	tx.Txn().SetBalance(staking.AddrStakingContract, big.NewInt(2_800))

	backend := &backendIBFT{uptimeCfg: pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000}, forkManager: &votingPowerForkManager{active: func(uint64) bool { return true }}}
	assertSnapshot := func(height, parent uint64, wantA, wantB, wantTotal uint64) {
		t.Helper()
		powers, snap, err := backend.snapshotVotingPowers(height, parent, valSet, tx, true)
		require.NoError(t, err)
		require.Equal(t, wantA, powers[types.AddressToString(validatorA)].Uint64())
		require.Equal(t, wantB, powers[types.AddressToString(validatorB)].Uint64())
		require.Equal(t, new(big.Int).SetUint64(wantTotal).String(), snap.totalVotingPower)
		require.Equal(t, weightedQuorumThreshold(new(big.Int).SetUint64(wantTotal)).String(), snap.quorumThreshold)
	}

	require.Equal(t, uint64(800), readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 8))
	require.Equal(t, uint64(500), readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 9))
	assertSnapshot(20, 19, 1_500, 1_000, 2_500)

	setPoSEpochValidatorsSnapshotForVotingPowerTest(t, tx, slashEpoch, valSet)
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.stake.snapshot", slashEpoch, validatorA, big.NewInt(1_500))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.stake.snapshot", slashEpoch, validatorB, big.NewInt(1_000))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.staker.stake.snapshot", slashEpoch, validatorA, big.NewInt(1_000))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.staker.stake.snapshot", slashEpoch, activeDelegator, big.NewInt(500))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.staker.stake.snapshot", slashEpoch, inactiveDelegator, big.NewInt(300))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.staker.stake.snapshot", slashEpoch, validatorB, big.NewInt(1_000))
	setPoSU64StateForVotingPowerTest(tx, "xgr.pos.prop.slots", slashEpoch, validatorA, 1)
	setPoSU64StateForVotingPowerTest(tx, "xgr.pos.prop.miss", slashEpoch, validatorA, 1)
	setPoSU64StateForVotingPowerTest(tx, "xgr.pos.prop.slots", slashEpoch, validatorB, 1)
	setPoSU64StateForVotingPowerTest(tx, "xgr.pos.prop.miss", slashEpoch, validatorB, 0)

	header := &types.Header{Number: 20}
	headerSigner := &sequenceSigner{extra: &signer.IstanbulExtra{Validators: valSet}}
	var finalizeErr error
	require.NotPanics(t, func() { finalizeErr = pos.FinalizeEpoch(header, epochSize, pos.UptimeConfig{}, headerSigner, tx) })
	require.NoError(t, finalizeErr)

	logs := tx.Txn().Logs()
	assertStakerSlashedLogForVotingPowerTest(t, logs, slashEpoch, validatorA, validatorA, 1, 9, 1_000, 1_000, 991)
	assertStakerSlashedLogForVotingPowerTest(t, logs, slashEpoch, validatorA, activeDelegator, 2, 4, 500, 500, 496)
	assertStakerSlashedLogForVotingPowerTest(t, logs, slashEpoch, validatorA, inactiveDelegator, 2, 2, 300, 300, 298)
	require.Zero(t, readPoSU64StateForVotingPowerTest(tx, "xgr.pos.slashed", slashEpoch, validatorA))
	require.Zero(t, readPoSBigStateForVotingPowerTest(tx, "xgr.pos.slash.amount", slashEpoch, validatorA).Sign())
	require.Equal(t, uint64(991), readStakerAmountForVotingPowerTest(tx, validatorA))
	require.Equal(t, uint64(496), readStakerAmountForVotingPowerTest(tx, activeDelegator))
	require.Equal(t, uint64(298), readStakerAmountForVotingPowerTest(tx, inactiveDelegator), "inactive delegator with an epoch stake snapshot is slashed, but does not contribute to active delegated aggregate")
	require.Equal(t, uint64(794), readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 8))
	require.Equal(t, uint64(496), readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 9))
	require.Equal(t, readStakerAmountForVotingPowerTest(tx, activeDelegator)+readStakerAmountForVotingPowerTest(tx, inactiveDelegator), readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 8))
	require.Equal(t, readStakerAmountForVotingPowerTest(tx, activeDelegator), readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 9))
	require.Equal(t, uint64(1_000), readStakerAmountForVotingPowerTest(tx, validatorB))
	require.Equal(t, uint64(2_785), txnBalanceUint64ForVotingPowerTest(tx, staking.AddrStakingContract))
	require.False(t, isStakerActiveForVotingPowerTest(tx, validatorA))
	assertSnapshot(21, 20, 1_487, 1_000, 2_487)
}

func TestPoS_Slashing_MultipleDelegators_ProportionalAndDustAccounting(t *testing.T) {
	validatorA := types.StringToAddress("0x1000000000000000000000000000000000000131")
	validatorB := types.StringToAddress("0x1000000000000000000000000000000000000132")
	delegator1 := types.StringToAddress("0x2000000000000000000000000000000000000131")
	delegator2 := types.StringToAddress("0x2000000000000000000000000000000000000132")
	delegator3 := types.StringToAddress("0x2000000000000000000000000000000000000133")
	valSet := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validatorA), validators.NewECDSAValidator(validatorB))
	const epochSize uint64 = 10
	const slashEpoch uint64 = 2

	tx := newVotingPowerTestTransitionWithFeePoolSplit(t, valSet, 20)
	setStakingConfigForVotingPowerTest(t, tx, epochSize, 0, uint64(valSet.Len()))
	setValidatorSelfStake(t, tx, validatorA, 1_000)
	setValidatorSelfStake(t, tx, validatorB, 2_000)
	setDelegatorStakeForValidator(t, tx, validatorA, delegator1, 333, 1, 0, true)
	setDelegatorStakeForValidator(t, tx, validatorA, delegator2, 777, 1, 0, true)
	setDelegatorStakeForValidator(t, tx, validatorA, delegator3, 111, 1, 0, true)
	setValidatorDelegatedStakeTotals(t, tx, validatorA, 1_221, 1_221)
	setNominalAndEffectiveWeight(tx, validatorA, 10_000, 10_000)
	setNominalAndEffectiveWeight(tx, validatorB, 10_000, 10_000)
	tx.Txn().SetBalance(staking.AddrStakingContract, big.NewInt(4_221))

	setPoSEpochValidatorsSnapshotForVotingPowerTest(t, tx, slashEpoch, valSet)
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.stake.snapshot", slashEpoch, validatorA, big.NewInt(2_221))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.stake.snapshot", slashEpoch, validatorB, big.NewInt(2_000))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.staker.stake.snapshot", slashEpoch, validatorA, big.NewInt(1_000))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.staker.stake.snapshot", slashEpoch, delegator1, big.NewInt(333))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.staker.stake.snapshot", slashEpoch, delegator2, big.NewInt(777))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.staker.stake.snapshot", slashEpoch, delegator3, big.NewInt(111))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.staker.stake.snapshot", slashEpoch, validatorB, big.NewInt(2_000))
	setPoSU64StateForVotingPowerTest(tx, "xgr.pos.prop.slots", slashEpoch, validatorA, 1)
	setPoSU64StateForVotingPowerTest(tx, "xgr.pos.prop.miss", slashEpoch, validatorA, 1)
	setPoSU64StateForVotingPowerTest(tx, "xgr.pos.prop.slots", slashEpoch, validatorB, 1)
	setPoSU64StateForVotingPowerTest(tx, "xgr.pos.prop.miss", slashEpoch, validatorB, 0)

	require.NoError(t, pos.FinalizeEpoch(&types.Header{Number: 20}, epochSize, pos.UptimeConfig{}, &sequenceSigner{extra: &signer.IstanbulExtra{Validators: valSet}}, tx))

	const wantSlash uint64 = 22
	reductions := (uint64(1_000) - readStakerAmountForVotingPowerTest(tx, validatorA)) +
		(uint64(333) - readStakerAmountForVotingPowerTest(tx, delegator1)) +
		(uint64(777) - readStakerAmountForVotingPowerTest(tx, delegator2)) +
		(uint64(111) - readStakerAmountForVotingPowerTest(tx, delegator3))
	logs := tx.Txn().Logs()
	assertStakerSlashedLogForVotingPowerTest(t, logs, slashEpoch, validatorA, validatorA, 1, 10, 1_000, 1_000, 990)
	assertStakerSlashedLogForVotingPowerTest(t, logs, slashEpoch, validatorA, delegator1, 2, 4, 333, 333, 329)
	assertStakerSlashedLogForVotingPowerTest(t, logs, slashEpoch, validatorA, delegator2, 2, 7, 777, 777, 770)
	assertStakerSlashedLogForVotingPowerTest(t, logs, slashEpoch, validatorA, delegator3, 2, 1, 111, 111, 110)
	require.Zero(t, readPoSU64StateForVotingPowerTest(tx, "xgr.pos.slashed", slashEpoch, validatorA))
	require.Zero(t, readPoSBigStateForVotingPowerTest(tx, "xgr.pos.slash.amount", slashEpoch, validatorA).Sign())
	require.Equal(t, wantSlash, reductions)
	require.Equal(t, uint64(990), readStakerAmountForVotingPowerTest(tx, validatorA), "two wei of rounding dust are assigned to the first slashable positions in deterministic staker order")
	require.Equal(t, uint64(329), readStakerAmountForVotingPowerTest(tx, delegator1))
	require.Equal(t, uint64(770), readStakerAmountForVotingPowerTest(tx, delegator2))
	require.Equal(t, uint64(110), readStakerAmountForVotingPowerTest(tx, delegator3))
	require.Equal(t, uint64(1_209), readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 8))
	require.Equal(t, uint64(1_209), readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 9))
	require.Equal(t, readStakerAmountForVotingPowerTest(tx, delegator1)+readStakerAmountForVotingPowerTest(tx, delegator2)+readStakerAmountForVotingPowerTest(tx, delegator3), readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 8))
	require.Equal(t, uint64(2_000), readStakerAmountForVotingPowerTest(tx, validatorB))
	require.Equal(t, uint64(4_199), txnBalanceUint64ForVotingPowerTest(tx, staking.AddrStakingContract))

	backend := &backendIBFT{uptimeCfg: pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000}, forkManager: &votingPowerForkManager{active: func(uint64) bool { return true }}}
	powers, snap, err := backend.snapshotVotingPowers(21, 20, valSet, tx, true)
	require.NoError(t, err)
	require.Equal(t, uint64(2_199), powers[types.AddressToString(validatorA)].Uint64())
	require.Equal(t, uint64(2_000), powers[types.AddressToString(validatorB)].Uint64())
	require.Equal(t, "4199", snap.totalVotingPower)
	require.Equal(t, weightedQuorumThreshold(big.NewInt(4_199)).String(), snap.quorumThreshold)
}

func TestPoS_Slashing_BelowMinimumStakeValidatorStateAndVotingPower(t *testing.T) {
	validatorA := types.StringToAddress("0x1000000000000000000000000000000000000111")
	validatorB := types.StringToAddress("0x1000000000000000000000000000000000000112")
	delegatorA := types.StringToAddress("0x2000000000000000000000000000000000000111")
	valSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(validatorA),
		validators.NewECDSAValidator(validatorB),
	)
	const epochSize uint64 = 10
	const slashEpoch uint64 = 2
	const validatorASelfStake uint64 = 2
	const validatorADelegatedStake uint64 = 199
	const validatorBStake uint64 = 1_000

	tx := newVotingPowerTestTransitionWithFeePoolSplit(t, valSet, 20)
	setStakingConfigForVotingPowerTest(t, tx, epochSize, 0, uint64(valSet.Len()))
	setValidatorSelfStake(t, tx, validatorA, validatorASelfStake)
	setValidatorSelfStake(t, tx, validatorB, validatorBStake)
	setDelegatorStakeForValidator(t, tx, validatorA, delegatorA, validatorADelegatedStake, 1, 0, true)
	setValidatorDelegatedStakeTotals(t, tx, validatorA, validatorADelegatedStake, validatorADelegatedStake)
	setNominalAndEffectiveWeight(tx, validatorA, 10_000, 10_000)
	setNominalAndEffectiveWeight(tx, validatorB, 10_000, 10_000)
	tx.Txn().SetBalance(staking.AddrStakingContract, new(big.Int).SetUint64(validatorASelfStake+validatorADelegatedStake+validatorBStake))

	backend := &backendIBFT{uptimeCfg: pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000}, forkManager: &votingPowerForkManager{active: func(uint64) bool { return true }}}
	assertSnapshot := func(height, parent uint64, wantA, wantB, wantTotal uint64) {
		t.Helper()
		powers, snap, err := backend.snapshotVotingPowers(height, parent, valSet, tx, true)
		require.NoError(t, err)
		require.Equal(t, wantA, powers[types.AddressToString(validatorA)].Uint64())
		require.Equal(t, wantB, powers[types.AddressToString(validatorB)].Uint64())
		require.Equal(t, new(big.Int).SetUint64(wantTotal).String(), snap.totalVotingPower)
		require.Equal(t, weightedQuorumThreshold(new(big.Int).SetUint64(wantTotal)).String(), snap.quorumThreshold)
	}

	require.Equal(t, validatorASelfStake, readStakerAmountForVotingPowerTest(tx, validatorA))
	require.Equal(t, validatorADelegatedStake, readStakerAmountForVotingPowerTest(tx, delegatorA))
	require.Equal(t, validatorADelegatedStake, readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 8))
	require.Equal(t, validatorADelegatedStake, readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 9))
	require.Equal(t, validatorBStake, readStakerAmountForVotingPowerTest(tx, validatorB))
	assertSnapshot(20, 19, validatorASelfStake+validatorADelegatedStake, validatorBStake, validatorASelfStake+validatorADelegatedStake+validatorBStake)

	setPoSEpochValidatorsSnapshotForVotingPowerTest(t, tx, slashEpoch, valSet)
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.stake.snapshot", slashEpoch, validatorA, new(big.Int).SetUint64(validatorASelfStake+validatorADelegatedStake))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.stake.snapshot", slashEpoch, validatorB, new(big.Int).SetUint64(validatorBStake))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.staker.stake.snapshot", slashEpoch, validatorA, new(big.Int).SetUint64(validatorASelfStake))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.staker.stake.snapshot", slashEpoch, delegatorA, new(big.Int).SetUint64(validatorADelegatedStake))
	setPoSBigStateForVotingPowerTest(tx, "xgr.pos.staker.stake.snapshot", slashEpoch, validatorB, new(big.Int).SetUint64(validatorBStake))
	setPoSU64StateForVotingPowerTest(tx, "xgr.pos.prop.slots", slashEpoch, validatorA, 1)
	setPoSU64StateForVotingPowerTest(tx, "xgr.pos.prop.miss", slashEpoch, validatorA, 1)
	setPoSU64StateForVotingPowerTest(tx, "xgr.pos.prop.slots", slashEpoch, validatorB, 1)
	setPoSU64StateForVotingPowerTest(tx, "xgr.pos.prop.miss", slashEpoch, validatorB, 0)

	header := &types.Header{Number: 20}
	headerSigner := &sequenceSigner{extra: &signer.IstanbulExtra{Validators: valSet}}
	var finalizeErr error
	require.NotPanics(t, func() {
		finalizeErr = pos.FinalizeEpoch(header, epochSize, pos.UptimeConfig{}, headerSigner, tx)
	})
	require.NoError(t, finalizeErr)

	const wantSlashAmount uint64 = 2
	const wantValidatorASelfStake uint64 = 1
	const wantDelegatorAStake uint64 = 198
	const wantValidatorAPower uint64 = wantValidatorASelfStake + wantDelegatorAStake
	const wantTotalPower uint64 = wantValidatorAPower + validatorBStake

	logs := tx.Txn().Logs()
	assertStakerSlashedLogForVotingPowerTest(t, logs, slashEpoch, validatorA, validatorA, 1, 1, validatorASelfStake, validatorASelfStake, wantValidatorASelfStake)
	assertStakerSlashedLogForVotingPowerTest(t, logs, slashEpoch, validatorA, delegatorA, 2, 1, validatorADelegatedStake, validatorADelegatedStake, wantDelegatorAStake)
	require.Zero(t, readPoSU64StateForVotingPowerTest(tx, "xgr.pos.slashed", slashEpoch, validatorA))
	require.Zero(t, readPoSBigStateForVotingPowerTest(tx, "xgr.pos.slash.amount", slashEpoch, validatorA).Sign())
	require.LessOrEqual(t, wantSlashAmount, validatorASelfStake+validatorADelegatedStake)
	require.Equal(t, wantValidatorASelfStake, readStakerAmountForVotingPowerTest(tx, validatorA))
	require.Equal(t, wantDelegatorAStake, readStakerAmountForVotingPowerTest(tx, delegatorA))
	require.Equal(t, wantDelegatorAStake, readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 8))
	require.Equal(t, wantDelegatorAStake, readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 9))
	require.Equal(t, readStakerAmountForVotingPowerTest(tx, delegatorA), readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 8))
	require.Equal(t, readStakerAmountForVotingPowerTest(tx, delegatorA), readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 9))
	require.Equal(t, validatorBStake, readStakerAmountForVotingPowerTest(tx, validatorB))
	require.Equal(t, validatorASelfStake+validatorADelegatedStake+validatorBStake-wantSlashAmount, txnBalanceUint64ForVotingPowerTest(tx, staking.AddrStakingContract))
	require.Positive(t, readStakerAmountForVotingPowerTest(tx, validatorA))
	require.Positive(t, readStakerAmountForVotingPowerTest(tx, delegatorA))
	require.False(t, isStakerActiveForVotingPowerTest(tx, validatorA), "zero-uptime validator is deactivated by finalize")
	require.Equal(t, uint64(20), readStakerDeactivatedAtForVotingPowerTest(tx, validatorA))
	assertSnapshot(21, 20, wantValidatorAPower, validatorBStake, wantTotalPower)

	t.Logf("slash amount=%d final validatorA self=%d delegatorA=%d rawDelegated=%d activeDelegated=%d validatorB=%d totalVotingPower=%d",
		wantSlashAmount,
		readStakerAmountForVotingPowerTest(tx, validatorA),
		readStakerAmountForVotingPowerTest(tx, delegatorA),
		readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 8),
		readValidatorDelegatedStakeTotalForVotingPowerTest(tx, validatorA, 9),
		readStakerAmountForVotingPowerTest(tx, validatorB),
		wantTotalPower,
	)
}

func TestWeightedCommittedSeals_StakeAndUptimePowerThreshold(t *testing.T) {
	pool := newTesterAccountPool(t)
	pool.add("A", "B", "C", "D")
	valSet := pool.ValidatorSet()
	hash := types.BytesToHash(crypto.Keccak256([]byte("weighted committed seals power threshold")))
	round := uint64(1)
	proposal := &types.Header{Number: 11}

	type votingPowerConfig struct {
		stake     uint64
		nominal   uint64
		effective uint64
	}

	newWeightedSealBackend := func(powers map[string]votingPowerConfig) (*backendIBFT, *types.Header) {
		t.Helper()

		st := itrie.NewState(itrie.NewMemoryStorage())
		ex := state.NewExecutor(&chain.Params{Forks: chain.AllForksEnabled, BurnContract: map[uint64]types.Address{0: types.ZeroAddress}}, st, hclog.NewNullLogger())
		genesisRoot, err := ex.WriteGenesis(nil, types.Hash{})
		require.NoError(t, err)
		ex.GetHash = func(h *types.Header) state.GetHashByNumber {
			return func(i uint64) types.Hash { return genesisRoot }
		}

		tx, err := ex.BeginTxn(genesisRoot, &types.Header{Number: 10}, types.ZeroAddress)
		require.NoError(t, err)
		contractState, err := stakingHelper.PredeployStakingSC(valSet, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: uint64(valSet.Len()), EpochSize: 2})
		require.NoError(t, err)
		require.NoError(t, tx.SetAccountDirectly(staking.AddrStakingContract, contractState))
		tx.Txn().SetNonce(pos.PosSysAddr, 1)

		for name, power := range powers {
			addr := pool.get(name).Address()
			setValidatorSelfStake(t, tx, addr, power.stake)
			setNominalAndEffectiveWeight(tx, addr, power.nominal, power.effective)
		}

		_, root, err := tx.Commit()
		require.NoError(t, err)

		parent := &types.Header{Number: 10, StateRoot: root}
		parent.ComputeHash()

		backend := &backendIBFT{
			executor:    ex,
			uptimeCfg:   pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000},
			forkManager: &votingPowerForkManager{active: func(uint64) bool { return true }},
		}

		return backend, parent
	}

	committedSealsFrom := func(names ...string) signer.Seals {
		t.Helper()

		seals := signer.SerializedSeal{}
		for _, name := range names {
			acc := pool.get(name)
			s := signer.NewSigner(
				signer.NewECDSAKeyManagerFromKey(acc.priv),
				signer.NewECDSAKeyManagerFromKey(acc.priv),
			)
			seal, err := s.CreateCommittedSeal(hash.Bytes())
			require.NoError(t, err)
			seals = append(seals, seal)
		}

		return &seals
	}

	fullPowerBackend, fullPowerParent := newWeightedSealBackend(map[string]votingPowerConfig{
		"A": {stake: 700, nominal: 10_000, effective: 10_000},
		"B": {stake: 100, nominal: 10_000, effective: 10_000},
		"C": {stake: 100, nominal: 10_000, effective: 10_000},
		"D": {stake: 100, nominal: 10_000, effective: 10_000},
	})
	require.Equal(t, "667", weightedQuorumThreshold(big.NewInt(1_000)).String())
	require.NoError(t, fullPowerBackend.verifyWeightedCommittedPower(11, hash, committedSealsFrom("A"), valSet, proposal, fullPowerParent, &round), "one high-power signer must satisfy weighted quorum even though signer count is low")
	err := fullPowerBackend.verifyWeightedCommittedPower(11, hash, committedSealsFrom("B", "C", "D"), valSet, proposal, fullPowerParent, &round)
	require.ErrorIs(t, err, signer.ErrNotEnoughCommittedSeals, "three low-power signers must not satisfy stake-weighted quorum by validator count alone")
	require.Contains(t, err.Error(), "collected=300")
	require.Contains(t, err.Error(), "quorum=667")

	decayedBackend, decayedParent := newWeightedSealBackend(map[string]votingPowerConfig{
		"A": {stake: 700, nominal: 10_000, effective: 2_858},
		"B": {stake: 100, nominal: 10_000, effective: 10_000},
		"C": {stake: 100, nominal: 10_000, effective: 10_000},
		"D": {stake: 100, nominal: 10_000, effective: 10_000},
	})
	decayedPowers, decayedSnap, err := decayedBackend.effectiveVotingPowerSnapshot(11, valSet, decayedParent)
	require.NoError(t, err)
	require.Equal(t, uint64(200), decayedPowers[types.AddressToString(pool.get("A").Address())].Uint64())
	require.Equal(t, "500", decayedSnap.totalVotingPower)
	require.Equal(t, "334", weightedQuorumThreshold(big.NewInt(500)).String())
	err = decayedBackend.verifyWeightedCommittedPower(11, hash, committedSealsFrom("A", "B"), valSet, proposal, decayedParent, &round)
	require.ErrorIs(t, err, signer.ErrNotEnoughCommittedSeals, "nominally high stake must not count after uptime decay lowers effective power")
	require.Contains(t, err.Error(), "collected=300")
	require.Contains(t, err.Error(), "quorum=334")
	require.NoError(t, decayedBackend.verifyWeightedCommittedPower(11, hash, committedSealsFrom("A", "B", "C", "D"), valSet, proposal, decayedParent, &round), "additional effective power must satisfy weighted quorum after uptime decay")
}

func TestWeightedCommittedSeals_RejectsDuplicateUnknownAndWrongSealPower(t *testing.T) {
	pool := newTesterAccountPool(t)
	pool.add("A", "B", "C", "D", "E")
	valSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(pool.get("A").Address()),
		validators.NewECDSAValidator(pool.get("B").Address()),
		validators.NewECDSAValidator(pool.get("C").Address()),
		validators.NewECDSAValidator(pool.get("D").Address()),
	)
	hash := types.BytesToHash(crypto.Keccak256([]byte("weighted committed seals duplicate unknown wrong hash")))
	wrongHash := types.BytesToHash(crypto.Keccak256([]byte("wrong committed hash")))
	round := uint64(1)
	wrongRound := uint64(2)
	proposal := &types.Header{Number: 11}

	type votingPowerConfig struct{ stake, nominal, effective uint64 }
	newWeightedSealBackend := func(powers map[string]votingPowerConfig) (*backendIBFT, *types.Header) {
		t.Helper()
		st := itrie.NewState(itrie.NewMemoryStorage())
		ex := state.NewExecutor(&chain.Params{Forks: chain.AllForksEnabled, BurnContract: map[uint64]types.Address{0: types.ZeroAddress}}, st, hclog.NewNullLogger())
		genesisRoot, err := ex.WriteGenesis(nil, types.Hash{})
		require.NoError(t, err)
		ex.GetHash = func(h *types.Header) state.GetHashByNumber { return func(i uint64) types.Hash { return genesisRoot } }
		tx, err := ex.BeginTxn(genesisRoot, &types.Header{Number: 10}, types.ZeroAddress)
		require.NoError(t, err)
		contractState, err := stakingHelper.PredeployStakingSC(valSet, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: uint64(valSet.Len()), EpochSize: 2})
		require.NoError(t, err)
		require.NoError(t, tx.SetAccountDirectly(staking.AddrStakingContract, contractState))
		for name, power := range powers {
			addr := pool.get(name).Address()
			setValidatorSelfStake(t, tx, addr, power.stake)
			setNominalAndEffectiveWeight(tx, addr, power.nominal, power.effective)
		}
		_, root, err := tx.Commit()
		require.NoError(t, err)
		parent := &types.Header{Number: 10, StateRoot: root}
		parent.ComputeHash()
		return &backendIBFT{executor: ex, uptimeCfg: pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000}, forkManager: &votingPowerForkManager{active: func(uint64) bool { return true }}}, parent
	}

	sealFor := func(name string, sealHash types.Hash) []byte {
		t.Helper()
		acc := pool.get(name)
		s := signer.NewSigner(signer.NewECDSAKeyManagerFromKey(acc.priv), signer.NewECDSAKeyManagerFromKey(acc.priv))
		seal, err := s.CreateCommittedSeal(sealHash.Bytes())
		require.NoError(t, err)
		return seal
	}
	seals := func(items ...[]byte) signer.Seals {
		t.Helper()
		out := signer.SerializedSeal{}
		for _, item := range items {
			out = append(out, item)
		}
		return &out
	}

	fullBackend, fullParent := newWeightedSealBackend(map[string]votingPowerConfig{
		"A": {stake: 700, nominal: 10_000, effective: 10_000},
		"B": {stake: 100, nominal: 10_000, effective: 10_000},
		"C": {stake: 100, nominal: 10_000, effective: 10_000},
		"D": {stake: 100, nominal: 10_000, effective: 10_000},
	})
	require.NoError(t, fullBackend.verifyWeightedCommittedPower(11, hash, seals(sealFor("A", hash), sealFor("A", hash)), valSet, proposal, fullParent, &round), "duplicate high-power signer is deduplicated; A alone legitimately satisfies quorum")
	require.ErrorIs(t, fullBackend.verifyWeightedCommittedPower(11, hash, seals(sealFor("E", hash)), valSet, proposal, fullParent, &round), signer.ErrNonValidatorCommittedSeal)
	require.Error(t, fullBackend.verifyWeightedCommittedPower(11, hash, seals(sealFor("A", wrongHash)), valSet, proposal, fullParent, &round), "a validator seal over the wrong commit hash must not be accepted for this hash")
	require.Error(t, fullBackend.verifyWeightedCommittedPower(11, hash, seals([]byte{0x01, 0x02, 0x03}), valSet, proposal, fullParent, &round), "malformed ECDSA seal must fail recovery")

	thresholdBackend, thresholdParent := newWeightedSealBackend(map[string]votingPowerConfig{
		"A": {stake: 400, nominal: 10_000, effective: 10_000},
		"B": {stake: 300, nominal: 10_000, effective: 10_000},
		"C": {stake: 200, nominal: 10_000, effective: 10_000},
		"D": {stake: 100, nominal: 10_000, effective: 10_000},
	})
	err := thresholdBackend.verifyWeightedCommittedPower(11, hash, seals(sealFor("B", hash), sealFor("B", hash), sealFor("C", hash)), valSet, proposal, thresholdParent, &round)
	require.ErrorIs(t, err, signer.ErrNotEnoughCommittedSeals, "duplicate B must not be double-counted to cross quorum")
	require.Contains(t, err.Error(), "collected=500")
	require.Contains(t, err.Error(), "quorum=667")

	ordered := seals(sealFor("A", hash), sealFor("B", hash))
	reversed := seals(sealFor("B", hash), sealFor("A", hash))
	require.NoError(t, thresholdBackend.verifyWeightedCommittedPower(11, hash, ordered, valSet, proposal, thresholdParent, &round))
	require.NoError(t, thresholdBackend.verifyWeightedCommittedPower(11, hash, reversed, valSet, proposal, thresholdParent, &round), "signer ordering does not change collected power")
	require.NoError(t, thresholdBackend.verifyWeightedCommittedPower(11, hash, ordered, valSet, proposal, thresholdParent, &wrongRound), "current ECDSA committed-seal power path does not encode round in the seal digest")
}

func TestSnapshotVotingPowers_MicroDecayAsStakeMultiplier_AndRestore(t *testing.T) {
	t.Parallel()
	valSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000002")),
	)
	stateTx := newVotingPowerTestTransition(t, valSet, 10)
	a := valSet.At(0).Addr()
	b := valSet.At(1).Addr()
	setValidatorSelfStake(t, stateTx, a, 10_000)
	setValidatorSelfStake(t, stateTx, b, 30_000)
	setNominalAndEffectiveWeight(stateTx, a, 10_000, 10_000)
	setNominalAndEffectiveWeight(stateTx, b, 10_000, 5_000)
	backend := &backendIBFT{uptimeCfg: pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000}, forkManager: &votingPowerForkManager{active: func(uint64) bool { return true }}}

	res, _, err := backend.snapshotVotingPowers(10, 9, valSet, stateTx, true)
	require.NoError(t, err)
	require.Equal(t, uint64(10_000), res[types.AddressToString(a)].Uint64())
	require.Equal(t, uint64(15_000), res[types.AddressToString(b)].Uint64())

	setNominalAndEffectiveWeight(stateTx, b, 10_000, 10_000)
	res, _, err = backend.snapshotVotingPowers(10, 9, valSet, stateTx, true)
	require.NoError(t, err)
	require.Equal(t, uint64(30_000), res[types.AddressToString(b)].Uint64())
}

func TestSnapshotVotingPowers_FirstPoSBlock_UsesUnitWeights_WhenParentPrePoS(t *testing.T) {
	t.Parallel()
	valSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000002")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000003")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000004")),
	)
	backend := &backendIBFT{}
	powers, snap, err := backend.snapshotVotingPowers(50, 49, valSet, nil, false)
	require.NoError(t, err)
	require.Equal(t, "4", snap.totalVotingPower)
	require.Equal(t, "3", snap.quorumThreshold)
	for i := 0; i < valSet.Len(); i++ {
		addr := valSet.At(uint64(i)).Addr()
		require.Equal(t, uint64(1), powers[types.AddressToString(addr)].Uint64())
	}
}

func TestSnapshotVotingPowers_SecondPoSBlock_MissingStakeFails(t *testing.T) {
	t.Parallel()
	valSet := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001")))
	stateTx := newBareVotingPowerTestTransition(t, 50)
	backend := &backendIBFT{uptimeCfg: pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000}}
	_, _, err := backend.snapshotVotingPowers(51, 50, valSet, stateTx, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing effective stake")
}

func TestSnapshotVotingPowers_UsesConsensusStake_WhenValidatorDeactivated(t *testing.T) {
	t.Parallel()
	val := types.StringToAddress("0x1000000000000000000000000000000000000001")
	valSet := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(val))
	stateTx := newVotingPowerTestTransition(t, valSet, 250)
	setValidatorSelfStake(t, stateTx, val, 10)
	setStakerDeactivatedAt(t, stateTx, val, 200)
	setNominalAndEffectiveWeight(stateTx, val, 10_000, 10_000)
	backend := &backendIBFT{uptimeCfg: pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000}, forkManager: &votingPowerForkManager{active: func(uint64) bool { return true }}}

	res, _, err := backend.snapshotVotingPowers(251, 250, valSet, stateTx, true)
	require.NoError(t, err)
	require.Equal(t, uint64(10), res[types.AddressToString(val)].Uint64())
}

func TestSnapshotVotingPowers_EpochBoundaryDeactivationRegression(t *testing.T) {
	t.Parallel()
	val := types.StringToAddress("0x1000000000000000000000000000000000000001")
	valSet := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(val))
	stateTx := newVotingPowerTestTransition(t, valSet, 250)
	setEpochSizeForVotingPowerTest(t, stateTx, 50)
	setValidatorSelfStake(t, stateTx, val, 10)
	setStakerDeactivatedAt(t, stateTx, val, 200)
	setNominalAndEffectiveWeight(stateTx, val, 10_000, 10_000)
	backend := &backendIBFT{uptimeCfg: pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000}, forkManager: &votingPowerForkManager{active: func(uint64) bool { return true }}}

	_, _, err := backend.snapshotVotingPowers(251, 250, valSet, stateTx, true)
	require.NoError(t, err)
}

func TestConsensusDoesNotReferenceRemovedPoSWrappers(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile(filepath.Join("..", "..", "consensus", "ibft", "ibft.go"))
	require.NoError(t, err)
	require.NotContains(t, string(content), "is"+"PoSActiveAt(")
	require.NotContains(t, string(content), "is"+"StakeWeightedVotingActive(")
	require.NotContains(t, string(content), "is"+"BeaconActiveAt(")
}

func TestPoAToPoSBoundary_DoesNotDeadlockVotingPowerInit(t *testing.T) {
	t.Parallel()
	valSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000002")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000003")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000004")),
	)
	backend := &backendIBFT{}
	powers, snap, err := backend.snapshotVotingPowers(50, 49, valSet, nil, false)
	require.NoError(t, err)
	require.Equal(t, "4", snap.totalVotingPower)
	for i := 0; i < valSet.Len(); i++ {
		addr := valSet.At(uint64(i)).Addr()
		require.Equal(t, uint64(1), powers[types.AddressToString(addr)].Uint64())
	}
}

func TestSnapshotVotingPowers_MicroEpochDutyWithoutSeen_DecaysOnlyAfterBoundary(t *testing.T) {
	t.Parallel()
	valSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000002")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000003")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000004")),
	)
	tx := newVotingPowerTestTransitionWithFeePoolSplit(t, valSet, 110)
	setEpochSizeForVotingPowerTest(t, tx, 100)
	cfg := pos.UptimeConfig{MicroEpochSize: 4, MicroEpochNominalWeight: 10_000, MicroEpochInactivityDecayBps: 9_000}
	backend := &backendIBFT{uptimeCfg: cfg, forkManager: &votingPowerForkManager{active: func(uint64) bool { return true }}}
	for i := 0; i < valSet.Len(); i++ {
		addr := valSet.At(uint64(i)).Addr()
		setValidatorSelfStake(t, tx, addr, 10)
		setNominalAndEffectiveWeight(tx, addr, 10_000, 10_000)
	}

	last := valSet.At(0).Addr()
	tx.Txn().SetState(pos.PosSysAddr, posKeyLastProposer(), addressToHash(last))
	s := &sequenceSigner{proposers: map[uint64]types.Address{
		101: valSet.At(0).Addr(),
		102: valSet.At(0).Addr(),
		103: valSet.At(0).Addr(),
		104: valSet.At(0).Addr(),
		105: valSet.At(0).Addr(),
	}}
	for _, block := range []uint64{101, 102, 103, 104} {
		r := uint64(0)
		extra := &signer.IstanbulExtra{Validators: valSet, RoundNumber: &r, CommittedSeals: &signer.SerializedSeal{}, ParentCommittedSeals: &signer.SerializedSeal{}}
		header := &types.Header{Number: block, ExtraData: extra.MarshalRLPTo(make([]byte, signer.IstanbulExtraVanity))}
		s.extra = extra
		require.NoError(t, pos.RecordBlockUptime(header, 100, valSet, s, cfg, tx))
	}
	target := valSet.At(1).Addr()
	require.Equal(t, uint64(10_000), pos.UptimeEffectiveWeight(tx.Txn(), target))

	powersBefore, snapBefore, err := backend.snapshotVotingPowers(105, 104, valSet, tx, true)
	require.NoError(t, err)
	require.Equal(t, uint64(10), powersBefore[types.AddressToString(target)].Uint64())
	require.Equal(t, "40", snapBefore.totalVotingPower)

	r := uint64(0)
	extra := &signer.IstanbulExtra{Validators: valSet, RoundNumber: &r, CommittedSeals: &signer.SerializedSeal{}, ParentCommittedSeals: &signer.SerializedSeal{}}
	header := &types.Header{Number: 105, ExtraData: extra.MarshalRLPTo(make([]byte, signer.IstanbulExtraVanity))}
	s.extra = extra
	require.NoError(t, pos.RecordBlockUptime(header, 100, valSet, s, cfg, tx))

	require.Equal(t, uint64(9_000), pos.UptimeEffectiveWeight(tx.Txn(), target))
	powersAfter, snapAfter, err := backend.snapshotVotingPowers(106, 105, valSet, tx, true)
	require.NoError(t, err)
	require.Equal(t, uint64(9), powersAfter[types.AddressToString(target)].Uint64())
	require.Equal(t, "39", snapAfter.totalVotingPower)
	require.Equal(t, weightedQuorumThreshold(big.NewInt(39)).String(), snapAfter.quorumThreshold)
}

func TestSnapshotVotingPowers_Height201DeactivatedValidatorSelfStakeRegression(t *testing.T) {
	t.Parallel()
	valSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000002")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000003")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000004")),
	)
	tx := newVotingPowerTestTransition(t, valSet, 200)
	setEpochSizeForVotingPowerTest(t, tx, 50)
	backend := &backendIBFT{uptimeCfg: pos.UptimeConfig{MicroEpochSize: 4, MicroEpochNominalWeight: 10_000}, forkManager: &votingPowerForkManager{active: func(uint64) bool { return true }}}

	for i := 0; i < valSet.Len(); i++ {
		addr := valSet.At(uint64(i)).Addr()
		setValidatorSelfStake(t, tx, addr, 10)
		setNominalAndEffectiveWeight(tx, addr, 10_000, 10_000)
	}

	deactivated := valSet.At(0).Addr()
	setStakerDeactivatedAt(t, tx, deactivated, 55)

	powers, snap, err := backend.snapshotVotingPowers(201, 200, valSet, tx, true)
	require.NoError(t, err)
	require.Equal(t, uint64(10), powers[types.AddressToString(deactivated)].Uint64())
	require.Equal(t, "40", snap.totalVotingPower)
	require.Equal(t, weightedQuorumThreshold(big.NewInt(40)).String(), snap.quorumThreshold)

	effective := contractstore.ReadValidatorEffectiveTotalStakeAt(tx, deactivated, 200)
	voting := contractstore.ReadValidatorVotingStakeAt(tx, deactivated, 200)
	require.Equal(t, int64(0), effective.Int64())
	require.Equal(t, int64(10), voting.Int64())
}

func slashEventTopicForVotingPowerTest() types.Hash {
	return types.BytesToHash(crypto.Keccak256([]byte("StakerSlashed(uint256,address,address,uint8,uint256,uint256,uint256,uint256,uint256,uint256,uint256,address)")))
}

func abiHashU64ForVotingPowerTest(v uint64) types.Hash {
	return types.BytesToHash(logWordBigForVotingPowerTest(new(big.Int).SetUint64(v)))
}

func abiHashAddressForVotingPowerTest(addr types.Address) types.Hash {
	return types.BytesToHash(abiWordAddressForVotingPowerTest(addr))
}

func abiWordAddressForVotingPowerTest(addr types.Address) []byte {
	var out [32]byte
	copy(out[12:], addr[:])
	return out[:]
}

func logWordBigForVotingPowerTest(v *big.Int) []byte {
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

func assertStakerSlashedLogForVotingPowerTest(
	t *testing.T,
	logs []*types.Log,
	epoch uint64,
	validator types.Address,
	staker types.Address,
	role uint8,
	amount uint64,
	stakeSnapshot uint64,
	stakeBefore uint64,
	stakeAfter uint64,
) {
	t.Helper()

	wantTopic := slashEventTopicForVotingPowerTest()
	wantEpoch := abiHashU64ForVotingPowerTest(epoch)
	wantValidator := abiHashAddressForVotingPowerTest(validator)
	wantStaker := abiHashAddressForVotingPowerTest(staker)

	for _, log := range logs {
		if log.Address != pos.PosSysAddr || len(log.Topics) != 4 || log.Topics[0] != wantTopic || log.Topics[1] != wantEpoch || log.Topics[2] != wantValidator || log.Topics[3] != wantStaker {
			continue
		}

		require.Len(t, log.Data, 9*32)
		require.Equal(t, new(big.Int).SetUint64(uint64(role)), logWordBigIntForVotingPowerTest(log.Data, 0))
		require.Equal(t, new(big.Int).SetUint64(amount), logWordBigIntForVotingPowerTest(log.Data, 1))
		require.Equal(t, new(big.Int).SetUint64(stakeSnapshot), logWordBigIntForVotingPowerTest(log.Data, 2))
		require.Equal(t, new(big.Int).SetUint64(stakeBefore), logWordBigIntForVotingPowerTest(log.Data, 3))
		require.Equal(t, new(big.Int).SetUint64(stakeAfter), logWordBigIntForVotingPowerTest(log.Data, 4))
		require.Equal(t, new(big.Int).SetUint64(1), logWordBigIntForVotingPowerTest(log.Data, 5), "slots")
		require.Equal(t, new(big.Int).SetUint64(1), logWordBigIntForVotingPowerTest(log.Data, 6), "missed")
		require.Equal(t, new(big.Int).SetUint64(100), logWordBigIntForVotingPowerTest(log.Data, 7), "slashBps")
		require.Equal(t, abiWordAddressForVotingPowerTest(chain.DefaultBurnedAddress), log.Data[8*32:9*32], "destination")
		return
	}

	require.Failf(t, "missing StakerSlashed log", "epoch=%d validator=%s staker=%s", epoch, validator, staker)
}

func logWordBigIntForVotingPowerTest(data []byte, idx int) *big.Int {
	return new(big.Int).SetBytes(data[idx*32 : (idx+1)*32])
}

func posStateKeyForVotingPowerTest(prefix string, epoch uint64, addr types.Address) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)

	return types.BytesToHash(crypto.Keccak256([]byte(prefix), eb[:], addr.Bytes()))
}

func posEpochValidatorsLenKeyForVotingPowerTest(epoch uint64) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)

	return types.BytesToHash(crypto.Keccak256([]byte("xgr.pos.epoch.validators.len"), eb[:]))
}

func posEpochValidatorKeyForVotingPowerTest(epoch, idx uint64) types.Hash {
	var eb [8]byte
	var ib [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)
	binary.BigEndian.PutUint64(ib[:], idx)

	return types.BytesToHash(crypto.Keccak256([]byte("xgr.pos.epoch.validators.val"), eb[:], ib[:]))
}

func setPoSEpochValidatorsSnapshotForVotingPowerTest(t *testing.T, txn *state.Transition, epoch uint64, vals validators.Validators) {
	t.Helper()
	txn.Txn().SetState(pos.PosSysAddr, posEpochValidatorsLenKeyForVotingPowerTest(epoch), types.BytesToHash(new(big.Int).SetUint64(uint64(vals.Len())).Bytes()))
	for i := 0; i < vals.Len(); i++ {
		txn.Txn().SetState(pos.PosSysAddr, posEpochValidatorKeyForVotingPowerTest(epoch, uint64(i)), addressToHash(vals.At(uint64(i)).Addr()))
	}
}

func setPoSU64StateForVotingPowerTest(txn *state.Transition, prefix string, epoch uint64, addr types.Address, value uint64) {
	var b [32]byte
	binary.BigEndian.PutUint64(b[24:], value)
	txn.Txn().SetState(pos.PosSysAddr, posStateKeyForVotingPowerTest(prefix, epoch, addr), types.BytesToHash(b[:]))
}

func readPoSU64StateForVotingPowerTest(txn *state.Transition, prefix string, epoch uint64, addr types.Address) uint64 {
	h := txn.Txn().GetState(pos.PosSysAddr, posStateKeyForVotingPowerTest(prefix, epoch, addr))
	return binary.BigEndian.Uint64(h[24:])
}

func setPoSBigStateForVotingPowerTest(txn *state.Transition, prefix string, epoch uint64, addr types.Address, value *big.Int) {
	txn.Txn().SetState(pos.PosSysAddr, posStateKeyForVotingPowerTest(prefix, epoch, addr), types.BytesToHash(value.Bytes()))
}

func readPoSBigStateForVotingPowerTest(txn *state.Transition, prefix string, epoch uint64, addr types.Address) *big.Int {
	h := txn.Txn().GetState(pos.PosSysAddr, posStateKeyForVotingPowerTest(prefix, epoch, addr))
	return new(big.Int).SetBytes(h[:])
}

func readStakerAmountForVotingPowerTest(txn *state.Transition, staker types.Address) uint64 {
	h := txn.Txn().GetState(staking.AddrStakingContract, stakingTestStakerSlotWithOffset(staker, 1))
	return new(big.Int).SetBytes(h[:]).Uint64()
}

func readValidatorDelegatedStakeTotalForVotingPowerTest(txn *state.Transition, validator types.Address, slot int64) uint64 {
	base := keccak.Keccak256(nil,
		append(common.PadLeftOrTrim(validator.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(slot).Bytes(), 32)...),
	)
	h := txn.Txn().GetState(staking.AddrStakingContract, types.BytesToHash(base))
	return new(big.Int).SetBytes(h[:]).Uint64()
}

func txnBalanceUint64ForVotingPowerTest(txn *state.Transition, addr types.Address) uint64 {
	return new(big.Int).Set(txn.Txn().GetBalance(addr)).Uint64()
}

func isStakerActiveForVotingPowerTest(txn *state.Transition, staker types.Address) bool {
	h := txn.Txn().GetState(staking.AddrStakingContract, stakingTestStakerSlotWithOffset(staker, 0))
	packed := new(big.Int).SetBytes(h[:])
	active := new(big.Int).Rsh(packed, 8)
	active.And(active, new(big.Int).SetUint64(0xff))

	return active.Sign() != 0
}

func readStakerDeactivatedAtForVotingPowerTest(txn *state.Transition, staker types.Address) uint64 {
	h := txn.Txn().GetState(staking.AddrStakingContract, stakingTestStakerSlotWithOffset(staker, 3))
	return new(big.Int).SetBytes(h[:]).Uint64()
}

func newVotingPowerTestTransition(t *testing.T, vals validators.Validators, block uint64) *state.Transition {
	t.Helper()
	st := itrie.NewState(itrie.NewMemoryStorage())
	ex := state.NewExecutor(&chain.Params{Forks: chain.AllForksEnabled, BurnContract: map[uint64]types.Address{0: types.ZeroAddress}}, st, hclog.NewNullLogger())
	root, err := ex.WriteGenesis(nil, types.Hash{})
	require.NoError(t, err)
	ex.GetHash = func(h *types.Header) state.GetHashByNumber {
		return func(i uint64) types.Hash { return root }
	}
	txn, err := ex.BeginTxn(root, &types.Header{Number: block}, types.ZeroAddress)
	require.NoError(t, err)
	contractState, err := stakingHelper.PredeployStakingSC(vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: uint64(vals.Len()), EpochSize: 2})
	require.NoError(t, err)
	require.NoError(t, txn.SetAccountDirectly(staking.AddrStakingContract, contractState))
	return txn
}

func newVotingPowerTestTransitionWithFeePoolSplit(t *testing.T, vals validators.Validators, block uint64) *state.Transition {
	t.Helper()
	st := itrie.NewState(itrie.NewMemoryStorage())
	forks := chain.AllForksEnabled.Copy()
	forks.SetFork(chain.FeePoolSplit, chain.NewFork(0))
	ex := state.NewExecutor(&chain.Params{Forks: forks, BurnContract: map[uint64]types.Address{0: types.ZeroAddress}}, st, hclog.NewNullLogger())
	root, err := ex.WriteGenesis(nil, types.Hash{})
	require.NoError(t, err)
	ex.GetHash = func(h *types.Header) state.GetHashByNumber {
		return func(i uint64) types.Hash { return root }
	}
	txn, err := ex.BeginTxn(root, &types.Header{Number: block}, types.ZeroAddress)
	require.NoError(t, err)
	contractState, err := stakingHelper.PredeployStakingSC(vals, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: uint64(vals.Len()), EpochSize: 2})
	require.NoError(t, err)
	require.NoError(t, txn.SetAccountDirectly(staking.AddrStakingContract, contractState))
	return txn
}

func newBareVotingPowerTestTransition(t *testing.T, block uint64) *state.Transition {
	t.Helper()
	st := itrie.NewState(itrie.NewMemoryStorage())
	ex := state.NewExecutor(&chain.Params{Forks: chain.AllForksEnabled, BurnContract: map[uint64]types.Address{0: types.ZeroAddress}}, st, hclog.NewNullLogger())
	root, err := ex.WriteGenesis(nil, types.Hash{})
	require.NoError(t, err)
	ex.GetHash = func(h *types.Header) state.GetHashByNumber {
		return func(i uint64) types.Hash { return root }
	}
	txn, err := ex.BeginTxn(root, &types.Header{Number: block}, types.ZeroAddress)
	require.NoError(t, err)
	return txn
}

func setNominalAndEffectiveWeight(txn *state.Transition, addr types.Address, nominal, effective uint64) {
	toHash := func(v uint64) types.Hash {
		var b [32]byte
		binary.BigEndian.PutUint64(b[24:], v)
		return types.BytesToHash(b[:])
	}
	txn.Txn().SetState(pos.PosSysAddr, types.BytesToHash(crypto.Keccak256([]byte("xgr.uptime.nominal.weight"), addr.Bytes())), toHash(nominal))
	txn.Txn().SetState(pos.PosSysAddr, types.BytesToHash(crypto.Keccak256([]byte("xgr.uptime.effective.weight"), addr.Bytes())), toHash(effective))
}

func setValidatorSelfStake(t *testing.T, txn *state.Transition, validator types.Address, stake uint64) {
	t.Helper()
	stakerSlotWithOffset := func(addr types.Address, offset int64) types.Hash {
		base := keccak.Keccak256(nil,
			append(common.PadLeftOrTrim(addr.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(3).Bytes(), 32)...),
		)
		i := new(big.Int).SetBytes(base)
		i.Add(i, big.NewInt(offset))
		return types.BytesToHash(i.Bytes())
	}
	toHashBig := func(v *big.Int) types.Hash { return types.BytesToHash(v.Bytes()) }
	// staker self amount
	txn.Txn().SetState(staking.AddrStakingContract, stakerSlotWithOffset(validator, 1), toHashBig(new(big.Int).SetUint64(stake)))
	// staker validator pointer = self
	txn.Txn().SetState(staking.AddrStakingContract, stakerSlotWithOffset(validator, 4), types.BytesToHash(validator.Bytes()))
	// active from genesis, not deactivated
	txn.Txn().SetState(staking.AddrStakingContract, stakerSlotWithOffset(validator, 2), types.BytesToHash(new(big.Int).SetUint64(1).Bytes()))
	txn.Txn().SetState(staking.AddrStakingContract, stakerSlotWithOffset(validator, 3), types.Hash{})

	base := keccak.Keccak256(nil, append(common.PadLeftOrTrim(validator.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(6).Bytes(), 32)...))
	txn.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(base), types.BytesToHash(new(big.Int).SetUint64(1).Bytes()))
	slot := new(big.Int).SetBytes(keccak.Keccak256(nil, common.PadLeftOrTrim(base, 32)))
	txn.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(slot.Bytes()), types.BytesToHash(validator.Bytes()))
}

func setValidatorSelfStakeRaw(t *testing.T, txn *state.Transition, validator types.Address, stake uint64, joinedAt uint64, mappedValidator types.Address) {
	t.Helper()
	stakerSlotWithOffset := func(addr types.Address, offset int64) types.Hash {
		base := keccak.Keccak256(nil,
			append(common.PadLeftOrTrim(addr.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(3).Bytes(), 32)...),
		)
		idx := new(big.Int).SetBytes(base)
		idx.Add(idx, big.NewInt(offset))
		return types.BytesToHash(idx.Bytes())
	}
	toHashBig := func(v *big.Int) types.Hash { return types.BytesToHash(v.Bytes()) }
	txn.Txn().SetState(staking.AddrStakingContract, stakerSlotWithOffset(validator, 1), toHashBig(new(big.Int).SetUint64(stake)))
	txn.Txn().SetState(staking.AddrStakingContract, stakerSlotWithOffset(validator, 4), types.BytesToHash(mappedValidator.Bytes()))
	txn.Txn().SetState(staking.AddrStakingContract, stakerSlotWithOffset(validator, 2), types.BytesToHash(new(big.Int).SetUint64(joinedAt).Bytes()))
	txn.Txn().SetState(staking.AddrStakingContract, stakerSlotWithOffset(validator, 3), types.Hash{})
}

func setDelegatorStakeForValidator(
	t *testing.T,
	txn *state.Transition,
	validator types.Address,
	delegator types.Address,
	amount uint64,
	joinedAt uint64,
	deactivatedAt uint64,
	active bool,
) {
	t.Helper()
	setStakerStakeForValidator(t, txn, delegator, validator, amount, joinedAt, deactivatedAt, active)
	appendStakerForValidator(t, txn, validator, delegator)
}

func setStakerStakeForValidator(
	t *testing.T,
	txn *state.Transition,
	staker types.Address,
	validator types.Address,
	amount uint64,
	joinedAt uint64,
	deactivatedAt uint64,
	active bool,
) {
	t.Helper()
	packed := big.NewInt(1)
	if active {
		packed.SetBit(packed, 8, 1)
	}
	txn.Txn().SetState(staking.AddrStakingContract, stakingTestStakerSlotWithOffset(staker, 0), types.BytesToHash(packed.Bytes()))
	txn.Txn().SetState(staking.AddrStakingContract, stakingTestStakerSlotWithOffset(staker, 1), types.BytesToHash(new(big.Int).SetUint64(amount).Bytes()))
	txn.Txn().SetState(staking.AddrStakingContract, stakingTestStakerSlotWithOffset(staker, 2), types.BytesToHash(new(big.Int).SetUint64(joinedAt).Bytes()))
	txn.Txn().SetState(staking.AddrStakingContract, stakingTestStakerSlotWithOffset(staker, 3), types.BytesToHash(new(big.Int).SetUint64(deactivatedAt).Bytes()))
	txn.Txn().SetState(staking.AddrStakingContract, stakingTestStakerSlotWithOffset(staker, 4), types.BytesToHash(validator.Bytes()))
}

func appendStakerForValidator(t *testing.T, txn *state.Transition, validator types.Address, staker types.Address) {
	t.Helper()
	stakersByValidatorBase := keccak.Keccak256(nil,
		append(common.PadLeftOrTrim(validator.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(6).Bytes(), 32)...),
	)
	lengthKey := types.BytesToHash(stakersByValidatorBase)
	lengthRaw := txn.Txn().GetState(staking.AddrStakingContract, lengthKey)
	length := new(big.Int).SetBytes(lengthRaw[:]).Uint64()

	arrayBase := new(big.Int).SetBytes(keccak.Keccak256(nil, common.PadLeftOrTrim(stakersByValidatorBase, 32)))
	arraySlot := new(big.Int).Add(arrayBase, new(big.Int).SetUint64(length))
	txn.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(arraySlot.Bytes()), types.BytesToHash(staker.Bytes()))
	txn.Txn().SetState(staking.AddrStakingContract, lengthKey, types.BytesToHash(new(big.Int).SetUint64(length+1).Bytes()))

	stakerIndexOuter := keccak.Keccak256(nil,
		append(common.PadLeftOrTrim(validator.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(7).Bytes(), 32)...),
	)
	stakerIndexInner := append(common.PadLeftOrTrim(staker.Bytes(), 32), common.PadLeftOrTrim(stakerIndexOuter, 32)...)
	txn.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(keccak.Keccak256(nil, stakerIndexInner)), types.BytesToHash(new(big.Int).SetUint64(length+1).Bytes()))
}

func setValidatorDelegatedStakeTotals(t *testing.T, txn *state.Transition, validator types.Address, raw uint64, active uint64) {
	t.Helper()
	setValidatorDelegatedStakeTotal(t, txn, validator, 8, raw)
	setValidatorDelegatedStakeTotal(t, txn, validator, 9, active)
}

func setValidatorDelegatedStakeTotal(t *testing.T, txn *state.Transition, validator types.Address, slot int64, amount uint64) {
	t.Helper()
	base := keccak.Keccak256(nil,
		append(common.PadLeftOrTrim(validator.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(slot).Bytes(), 32)...),
	)
	txn.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(base), types.BytesToHash(new(big.Int).SetUint64(amount).Bytes()))
}

func stakingTestStakerSlotWithOffset(addr types.Address, offset int64) types.Hash {
	base := keccak.Keccak256(nil,
		append(common.PadLeftOrTrim(addr.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(3).Bytes(), 32)...),
	)
	idx := new(big.Int).SetBytes(base)
	idx.Add(idx, big.NewInt(offset))
	return types.BytesToHash(idx.Bytes())
}

func setStakerDeactivatedAt(t *testing.T, txn *state.Transition, staker types.Address, block uint64) {
	t.Helper()
	stakerSlotWithOffset := func(addr types.Address, offset int64) types.Hash {
		base := keccak.Keccak256(nil,
			append(common.PadLeftOrTrim(addr.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(3).Bytes(), 32)...),
		)
		i := new(big.Int).SetBytes(base)
		i.Add(i, big.NewInt(offset))
		return types.BytesToHash(i.Bytes())
	}
	txn.Txn().SetState(staking.AddrStakingContract, stakerSlotWithOffset(staker, 3), types.BytesToHash(new(big.Int).SetUint64(block).Bytes()))
}

func setStakingConfigForVotingPowerTest(t *testing.T, txn *state.Transition, epochSize, minValidators, maxValidators uint64) {
	t.Helper()
	v := new(big.Int).SetUint64(maxValidators)
	v.Lsh(v, 64)
	v.Or(v, new(big.Int).SetUint64(minValidators))
	v.Lsh(v, 64)
	v.Or(v, new(big.Int).SetUint64(epochSize))
	txn.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(big.NewInt(1).Bytes()), types.BytesToHash(v.Bytes()))
}

func setEpochSizeForVotingPowerTest(t *testing.T, txn *state.Transition, epochSize uint64) {
	t.Helper()
	raw := txn.Txn().GetState(staking.AddrStakingContract, types.BytesToHash(big.NewInt(0).Bytes()))
	v := new(big.Int).SetBytes(raw[:])
	mask64 := new(big.Int).SetUint64(^uint64(0))
	clearMask := new(big.Int).Not(mask64)
	v.And(v, clearMask)
	v.Or(v, new(big.Int).SetUint64(epochSize))
	txn.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(big.NewInt(0).Bytes()), types.BytesToHash(v.Bytes()))
}

type votingPowerForkManager struct{ active func(uint64) bool }

func (m *votingPowerForkManager) Initialize() error                       { return nil }
func (m *votingPowerForkManager) Close() error                            { return nil }
func (m *votingPowerForkManager) GetSigner(uint64) (signer.Signer, error) { return nil, nil }
func (m *votingPowerForkManager) GetValidatorStore(uint64) (fork.ValidatorStore, error) {
	return nil, nil
}
func (m *votingPowerForkManager) GetValidators(uint64) (validators.Validators, error) {
	return nil, nil
}
func (m *votingPowerForkManager) GetHooks(uint64) fork.HooksInterface { return nil }
func (m *votingPowerForkManager) IsPosActive(h uint64) bool           { return m.active != nil && m.active(h) }

func TestPoAToPoSBoundaryVotingPowerSemantics(t *testing.T) {
	pool := newTesterAccountPool(t)
	pool.add("A", "B", "C")
	valSet := pool.ValidatorSet()
	const forkHeight uint64 = 10

	st := itrie.NewState(itrie.NewMemoryStorage())
	ex := state.NewExecutor(&chain.Params{Forks: chain.AllForksEnabled, BurnContract: map[uint64]types.Address{0: types.ZeroAddress}}, st, hclog.NewNullLogger())
	genesisRoot, err := ex.WriteGenesis(nil, types.Hash{})
	require.NoError(t, err)
	ex.GetHash = func(*types.Header) state.GetHashByNumber { return func(uint64) types.Hash { return genesisRoot } }

	tx, err := ex.BeginTxn(genesisRoot, &types.Header{Number: forkHeight - 1}, types.ZeroAddress)
	require.NoError(t, err)
	contractState, err := stakingHelper.PredeployStakingSC(valSet, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: uint64(valSet.Len()), EpochSize: 20})
	require.NoError(t, err)
	require.NoError(t, tx.SetAccountDirectly(staking.AddrStakingContract, contractState))
	setValidatorSelfStake(t, tx, pool.get("A").Address(), 700)
	setValidatorSelfStake(t, tx, pool.get("B").Address(), 200)
	setValidatorSelfStake(t, tx, pool.get("C").Address(), 100)
	for _, name := range []string{"A", "B", "C"} {
		setNominalAndEffectiveWeight(tx, pool.get(name).Address(), 10_000, 10_000)
	}
	_, root, err := tx.Commit()
	require.NoError(t, err)

	parentBeforeFork := &types.Header{Number: forkHeight - 1, StateRoot: root}
	parentBeforeFork.ComputeHash()
	parentAtFork := &types.Header{Number: forkHeight, StateRoot: root, ParentHash: parentBeforeFork.Hash}
	parentAtFork.ComputeHash()

	backend := &backendIBFT{
		executor:    ex,
		uptimeCfg:   pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000},
		forkManager: &votingPowerForkManager{active: func(h uint64) bool { return h >= forkHeight }},
	}

	powersAtFork, snapAtFork, err := backend.effectiveVotingPowerSnapshot(forkHeight, valSet, parentBeforeFork)
	require.NoError(t, err)
	require.Equal(t, "3", snapAtFork.totalVotingPower)
	require.Equal(t, "2", snapAtFork.quorumThreshold)
	for _, name := range []string{"A", "B", "C"} {
		require.Equal(t, uint64(1), powersAtFork[types.AddressToString(pool.get(name).Address())].Uint64(), "height F must not read weighted stake from a non-PoS parent")
	}

	powersAfterFork, snapAfterFork, err := backend.effectiveVotingPowerSnapshot(forkHeight+1, valSet, parentAtFork)
	require.NoError(t, err)
	require.Equal(t, uint64(700), powersAfterFork[types.AddressToString(pool.get("A").Address())].Uint64())
	require.Equal(t, uint64(200), powersAfterFork[types.AddressToString(pool.get("B").Address())].Uint64())
	require.Equal(t, uint64(100), powersAfterFork[types.AddressToString(pool.get("C").Address())].Uint64())
	require.Equal(t, "1000", snapAfterFork.totalVotingPower)
	require.Equal(t, "667", snapAfterFork.quorumThreshold)
}

func TestPoAToPoSBoundaryParentCommittedSealWeightedRules(t *testing.T) {
	pool := newTesterAccountPool(t)
	pool.add("A", "B", "C")
	valSet := pool.ValidatorSet()
	const forkHeight uint64 = 10

	st := itrie.NewState(itrie.NewMemoryStorage())
	ex := state.NewExecutor(&chain.Params{Forks: chain.AllForksEnabled, BurnContract: map[uint64]types.Address{0: types.ZeroAddress}}, st, hclog.NewNullLogger())
	genesisRoot, err := ex.WriteGenesis(nil, types.Hash{})
	require.NoError(t, err)
	ex.GetHash = func(*types.Header) state.GetHashByNumber { return func(uint64) types.Hash { return genesisRoot } }

	tx, err := ex.BeginTxn(genesisRoot, &types.Header{Number: forkHeight}, types.ZeroAddress)
	require.NoError(t, err)
	contractState, err := stakingHelper.PredeployStakingSC(valSet, stakingHelper.PredeployParams{MinValidatorCount: 0, MaxValidatorCount: uint64(valSet.Len()), EpochSize: 20})
	require.NoError(t, err)
	require.NoError(t, tx.SetAccountDirectly(staking.AddrStakingContract, contractState))
	setValidatorSelfStake(t, tx, pool.get("A").Address(), 700)
	setValidatorSelfStake(t, tx, pool.get("B").Address(), 200)
	setValidatorSelfStake(t, tx, pool.get("C").Address(), 100)
	for _, name := range []string{"A", "B", "C"} {
		setNominalAndEffectiveWeight(tx, pool.get(name).Address(), 10_000, 10_000)
	}
	_, root, err := tx.Commit()
	require.NoError(t, err)
	posParent := &types.Header{Number: forkHeight, StateRoot: root}
	posParent.ComputeHash()

	backend := &backendIBFT{
		executor:    ex,
		uptimeCfg:   pos.UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10_000},
		forkManager: &votingPowerForkManager{active: func(h uint64) bool { return h >= forkHeight }},
	}
	hash := types.BytesToHash(crypto.Keccak256([]byte("pos boundary committed seals")))
	proposal := &types.Header{Number: forkHeight + 1, ParentHash: posParent.Hash}
	round := uint64(0)
	sealsFrom := func(names ...string) signer.Seals {
		out := signer.SerializedSeal{}
		for _, name := range names {
			s := signer.NewSigner(signer.NewECDSAKeyManagerFromKey(pool.get(name).priv), signer.NewECDSAKeyManagerFromKey(pool.get(name).priv))
			seal, err := s.CreateCommittedSeal(hash.Bytes())
			require.NoError(t, err)
			out = append(out, seal)
		}
		return &out
	}

	require.NoError(t, backend.verifyWeightedCommittedPower(forkHeight+1, hash, sealsFrom("A"), valSet, proposal, posParent, &round))
	err = backend.verifyWeightedCommittedPower(forkHeight+1, hash, sealsFrom("B", "C"), valSet, proposal, posParent, &round)
	require.ErrorIs(t, err, signer.ErrNotEnoughCommittedSeals)
	require.Contains(t, err.Error(), "collected=300")
	require.Contains(t, err.Error(), "quorum=667")

	// Height F is PoS, but its parent is still PoA, so parent committed-seal voting power remains unit-based.
	require.NoError(t, backend.verifyWeightedCommittedPower(forkHeight, hash, sealsFrom("B", "C"), valSet, &types.Header{Number: forkHeight}, &types.Header{Number: forkHeight - 1, StateRoot: root}, &round))
}
