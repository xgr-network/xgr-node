package pos

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func TestMicroEpochOfBlock(t *testing.T) {
	require.Equal(t, uint64(0), microEpochOfBlock(0, 8))
	require.Equal(t, uint64(0), microEpochOfBlock(1, 8))
	require.Equal(t, uint64(0), microEpochOfBlock(8, 8))
	require.Equal(t, uint64(1), microEpochOfBlock(9, 8))
	require.Equal(t, uint64(1), microEpochOfBlock(16, 8))
	require.Equal(t, uint64(2), microEpochOfBlock(17, 8))
}

func TestApplyMicroEpochUptime_DutySeenSemantics(t *testing.T) {
	txn := newPosTestTransition(t)
	v0 := types.StringToAddress("0x1001")
	v1 := types.StringToAddress("0x1002")
	v2 := types.StringToAddress("0x1003")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v0), validators.NewECDSAValidator(v1), validators.NewECDSAValidator(v2))
	cfg := UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10000, MicroEpochInactivityDecayBps: 9000}
	for _, a := range []types.Address{v0, v1, v2} {
		txn.Txn().SetState(PosSysAddr, keyUptimeNominalWeight(a), u64ToHash(10000))
		txn.Txn().SetState(PosSysAddr, keyUptimeEffectiveWeight(a), u64ToHash(10000))
	}
	txn.Txn().SetState(PosSysAddr, keyMicroEpochDuty(0, v0), u64ToHash(1))
	txn.Txn().SetState(PosSysAddr, keyMicroEpochDuty(0, v1), u64ToHash(1))
	txn.Txn().SetState(PosSysAddr, keyMicroEpochSeen(0, v1), u64ToHash(1))

	require.NoError(t, applyMicroEpochUptime(txn, set, 3, types.ZeroAddress, cfg))
	require.Equal(t, uint64(9000), UptimeEffectiveWeight(txn.Txn(), v0))
	require.Equal(t, uint64(10000), UptimeEffectiveWeight(txn.Txn(), v1))
	require.Equal(t, uint64(10000), UptimeEffectiveWeight(txn.Txn(), v2))
	require.Zero(t, u64FromHash(txn.Txn().GetState(PosSysAddr, keyMicroEpochDuty(0, v0))))
	require.Zero(t, u64FromHash(txn.Txn().GetState(PosSysAddr, keyMicroEpochDuty(0, v1))))
	require.Zero(t, u64FromHash(txn.Txn().GetState(PosSysAddr, keyMicroEpochSeen(0, v1))))
}

func TestApplyMicroEpochUptime_RestoreUsesNominalNotEffective(t *testing.T) {
	txn := newPosTestTransition(t)
	v := types.StringToAddress("0x2001")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	cfg := UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10000, MicroEpochInactivityDecayBps: 9000}
	txn.Txn().SetState(PosSysAddr, keyUptimeNominalWeight(v), u64ToHash(10000))
	txn.Txn().SetState(PosSysAddr, keyUptimeEffectiveWeight(v), u64ToHash(5000))
	txn.Txn().SetState(PosSysAddr, keyMicroEpochSeen(0, v), u64ToHash(1))
	require.NoError(t, applyMicroEpochUptime(txn, set, 3, types.ZeroAddress, cfg))
	require.Equal(t, uint64(10000), UptimeEffectiveWeight(txn.Txn(), v))
}

func TestApplyMicroEpochUptime_DoesNotRestoreFromCurrentBlockActual(t *testing.T) {
	txn := newPosTestTransition(t)
	v := types.StringToAddress("0x3001")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	cfg := UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10000, MicroEpochInactivityDecayBps: 9000}
	txn.Txn().SetState(PosSysAddr, keyUptimeNominalWeight(v), u64ToHash(10000))
	txn.Txn().SetState(PosSysAddr, keyUptimeEffectiveWeight(v), u64ToHash(10000))
	txn.Txn().SetState(PosSysAddr, keyMicroEpochDuty(0, v), u64ToHash(1))
	require.NoError(t, applyMicroEpochUptime(txn, set, 3, v, cfg))
	require.Equal(t, uint64(9000), UptimeEffectiveWeight(txn.Txn(), v))
	require.Equal(t, uint64(1), u64FromHash(txn.Txn().GetState(PosSysAddr, keyUptimeInactivity(v))))
}

func TestApplyMicroEpochUptime_NoOpWhenMicroEpochSizeZero(t *testing.T) {
	txn := newPosTestTransition(t)
	v := types.StringToAddress("0x4001")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	cfg := UptimeConfig{MicroEpochSize: 0, MicroEpochNominalWeight: 10000, MicroEpochInactivityDecayBps: 9000}
	txn.Txn().SetState(PosSysAddr, keyUptimeEffectiveWeight(v), u64ToHash(7777))
	require.NoError(t, applyMicroEpochUptime(txn, set, 3, v, cfg))
	require.Equal(t, uint64(7777), UptimeEffectiveWeight(txn.Txn(), v))
	require.Equal(t, uint64(0), u64FromHash(txn.Txn().GetState(PosSysAddr, keyMicroEpochApplied())))
}

func TestRecordBlockUptime_MicroEpochMarksDutyAndSeen(t *testing.T) {
	v := types.StringToAddress("0x5001")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	r := uint64(0)
	extra := &signer.IstanbulExtra{Validators: set, RoundNumber: &r, CommittedSeals: &signer.SerializedSeal{}, ParentCommittedSeals: &signer.SerializedSeal{}}
	h := &types.Header{Number: 11, ExtraData: extra.MarshalRLPTo(make([]byte, signer.IstanbulExtraVanity))}
	s := &fixedSigner{proposer: v, extra: extra}
	txn := newPosTestTransitionWithStaking(t, v)
	cfg := UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10000, MicroEpochInactivityDecayBps: 9000}
	require.NoError(t, RecordBlockUptime(h, 10, set, s, cfg, txn))
	me := microEpochOfBlock(h.Number, cfg.MicroEpochSize)
	require.Equal(t, uint64(1), u64FromHash(txn.Txn().GetState(PosSysAddr, keyMicroEpochSeen(me, v))))
	require.Equal(t, uint64(1), u64FromHash(txn.Txn().GetState(PosSysAddr, keyMicroEpochDuty(me, v))))
	require.Equal(t, uint64(10000), UptimeNominalWeight(txn.Txn(), v))
	require.Equal(t, uint64(10000), UptimeEffectiveWeight(txn.Txn(), v))
}

func TestRecordBlockUptime_MicroEpochMissedDutyDecaysAfterBoundary(t *testing.T) {
	txn := newPosTestTransition(t)
	v := types.StringToAddress("0x6001")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	cfg := UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10000, MicroEpochInactivityDecayBps: 9000}
	txn.Txn().SetState(PosSysAddr, keyUptimeNominalWeight(v), u64ToHash(10000))
	txn.Txn().SetState(PosSysAddr, keyUptimeEffectiveWeight(v), u64ToHash(10000))
	txn.Txn().SetState(PosSysAddr, keyMicroEpochDuty(0, v), u64ToHash(1))
	require.NoError(t, applyMicroEpochUptime(txn, set, 3, types.ZeroAddress, cfg))
	require.Equal(t, uint64(9000), UptimeEffectiveWeight(txn.Txn(), v))
}

func TestRecordBlockUptime_MicroEpochMismatchStillMarksDutyAndDecays(t *testing.T) {
	a := types.StringToAddress("0x7001")
	b := types.StringToAddress("0x7002")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(a), validators.NewECDSAValidator(b))
	txn := newPosTestTransitionWithStakingValidators(t, set, 1, uint64(set.Len()))
	cfg := UptimeConfig{MicroEpochSize: 2, MicroEpochNominalWeight: 10000, MicroEpochInactivityDecayBps: 9000}

	for _, addr := range []types.Address{a, b} {
		txn.Txn().SetState(PosSysAddr, keyUptimeNominalWeight(addr), u64ToHash(10000))
		txn.Txn().SetState(PosSysAddr, keyUptimeEffectiveWeight(addr), u64ToHash(10000))
	}

	// lastProposer=a => expected proposer for round 0 is b. We set actual proposer to a (mismatch).
	setLastProposer(txn, a)
	r := uint64(0)
	extra := &signer.IstanbulExtra{Validators: set, RoundNumber: &r, CommittedSeals: &signer.SerializedSeal{}, ParentCommittedSeals: &signer.SerializedSeal{}}
	h1 := &types.Header{Number: 11, ExtraData: extra.MarshalRLPTo(make([]byte, signer.IstanbulExtraVanity))}
	s := &fixedSigner{proposer: a, extra: extra}
	require.NoError(t, RecordBlockUptime(h1, 10, set, s, cfg, txn))

	me0 := microEpochOfBlock(11, cfg.MicroEpochSize)
	require.Equal(t, uint64(1), u64FromHash(txn.Txn().GetState(PosSysAddr, keyMicroEpochSeen(me0, a))))
	require.Equal(t, uint64(1), u64FromHash(txn.Txn().GetState(PosSysAddr, keyMicroEpochDuty(me0, b))))
	require.Equal(t, uint64(10000), UptimeEffectiveWeight(txn.Txn(), b))

	h2 := &types.Header{Number: 13, ExtraData: extra.MarshalRLPTo(make([]byte, signer.IstanbulExtraVanity))}
	require.NoError(t, RecordBlockUptime(h2, 10, set, s, cfg, txn))

	require.Equal(t, uint64(9000), UptimeEffectiveWeight(txn.Txn(), b))
	require.Equal(t, uint64(1), u64FromHash(txn.Txn().GetState(PosSysAddr, keyUptimeInactivity(b))))
}

type fixedSigner struct {
	proposer types.Address
	extra    *signer.IstanbulExtra
}

func (m *fixedSigner) Type() validators.ValidatorType                                   { return validators.ECDSAValidatorType }
func (m *fixedSigner) Address() types.Address                                           { return m.proposer }
func (m *fixedSigner) InitIBFTExtra(*types.Header, validators.Validators, signer.Seals) {}
func (m *fixedSigner) GetIBFTExtra(*types.Header) (*signer.IstanbulExtra, error)        { return m.extra, nil }
func (m *fixedSigner) GetValidators(*types.Header) (validators.Validators, error) {
	return m.extra.Validators, nil
}
func (m *fixedSigner) WriteProposerSeal(h *types.Header) (*types.Header, error) { return h, nil }
func (m *fixedSigner) EcrecoverFromHeader(*types.Header) (types.Address, error) {
	return m.proposer, nil
}
func (m *fixedSigner) CreateCommittedSeal([]byte) ([]byte, error) { return nil, nil }
func (m *fixedSigner) VerifyCommittedSeal(validators.Validators, types.Address, []byte, []byte) error {
	return nil
}
func (m *fixedSigner) WriteCommittedSeals(*types.Header, uint64, map[types.Address][]byte) (*types.Header, error) {
	return nil, nil
}
func (m *fixedSigner) VerifyCommittedSeals(types.Hash, signer.Seals, validators.Validators, int) error {
	return nil
}
func (m *fixedSigner) VerifyParentCommittedSeals(types.Hash, *types.Header, validators.Validators, int, bool) error {
	return nil
}
func (m *fixedSigner) SignIBFTMessage([]byte) ([]byte, error) { return nil, nil }
func (m *fixedSigner) EcrecoverFromIBFTMessage([]byte, []byte) (types.Address, error) {
	return m.proposer, nil
}
func (m *fixedSigner) CalculateHeaderHash(*types.Header) (types.Hash, error) {
	return types.ZeroHash, nil
}
func (m *fixedSigner) FilterHeaderForHash(h *types.Header) (*types.Header, error) { return h, nil }
