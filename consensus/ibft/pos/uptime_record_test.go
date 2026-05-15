package pos

import (
	"errors"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func makeUptimeHeader(number uint64, set validators.Validators) *types.Header {
	extra := &signer.IstanbulExtra{
		Validators:           set,
		CommittedSeals:       &signer.SerializedSeal{},
		ParentCommittedSeals: &signer.SerializedSeal{},
	}

	return &types.Header{
		Number:    number,
		ExtraData: extra.MarshalRLPTo(make([]byte, signer.IstanbulExtraVanity)),
	}
}

func makeUptimeHeaderWithFixedSigner(number uint64, set validators.Validators, proposer types.Address) (*types.Header, *fixedSigner) {
	r := uint64(0)
	extra := &signer.IstanbulExtra{
		Validators:           set,
		RoundNumber:          &r,
		CommittedSeals:       &signer.SerializedSeal{},
		ParentCommittedSeals: &signer.SerializedSeal{},
	}

	return &types.Header{
		Number:    number,
		ExtraData: extra.MarshalRLPTo(make([]byte, signer.IstanbulExtraVanity)),
	}, &fixedSigner{proposer: proposer, extra: extra}
}

func TestRecordBlockUptime_ReturnsErrorWhenEpochValidatorSnapshotFreezeFails(t *testing.T) {
	v := types.StringToAddress("0x1111")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	header := makeUptimeHeader(11, set)
	txn := newPosTestTransitionWithStaking(t, v)

	origEnsure := ensureEpochValidatorsSnapshotFn
	ensureEpochValidatorsSnapshotFn = func(_ *state.Transition, _ uint64, _ validators.Validators) (bool, error) {
		return false, nil
	}
	defer func() { ensureEpochValidatorsSnapshotFn = origEnsure }()

	err := RecordBlockUptime(header, 10, set, testHeaderSigner(), UptimeConfig{}, txn)
	require.Error(t, err)
	require.Contains(t, err.Error(), "freeze epoch validator snapshot")
}

func TestRecordBlockUptime_PropagatesEpochValidatorSnapshotFreezeError(t *testing.T) {
	v := types.StringToAddress("0x1112")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	header := makeUptimeHeader(11, set)
	txn := newPosTestTransitionWithStaking(t, v)

	origEnsure := ensureEpochValidatorsSnapshotFn
	ensureEpochValidatorsSnapshotFn = func(_ *state.Transition, _ uint64, _ validators.Validators) (bool, error) {
		return false, errors.New("snapshot cap exceeded")
	}
	defer func() { ensureEpochValidatorsSnapshotFn = origEnsure }()

	err := RecordBlockUptime(header, 10, set, testHeaderSigner(), UptimeConfig{}, txn)
	require.Error(t, err)
	require.Contains(t, err.Error(), "freeze epoch validator snapshot")
	require.Contains(t, err.Error(), "snapshot cap exceeded")
}

func TestRecordBlockUptime_IdempotentWhenEpochValidatorSnapshotExists(t *testing.T) {
	v := types.StringToAddress("0x2222")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	header, fs := makeUptimeHeaderWithFixedSigner(11, set, v)
	txn := newPosTestTransitionWithStaking(t, v)
	epoch := epochOf(header.Number, 10)
	requireSnapshotCreated(t, txn, epoch, set)

	origEnsure := ensureEpochValidatorsSnapshotFn
	ensureEpochValidatorsSnapshotFn = func(_ *state.Transition, _ uint64, _ validators.Validators) (bool, error) {
		t.Fatalf("ensureEpochValidatorsSnapshot should not be called when snapshot already exists")
		return false, nil
	}
	defer func() { ensureEpochValidatorsSnapshotFn = origEnsure }()

	err := RecordBlockUptime(header, 10, set, fs, UptimeConfig{}, txn)
	require.NoError(t, err)
	require.Equal(t, header.Number, u64FromHash(txn.Txn().GetState(PosSysAddr, keyUptimeLastProcessedSlot())))
	slots := u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerSlots(epoch, v)))
	require.NoError(t, RecordBlockUptime(header, 10, set, fs, UptimeConfig{}, txn))
	require.Equal(t, slots, u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerSlots(epoch, v))))
}

func TestRecordBlockUptime_ReturnsErrorWhenStakeSnapshotFreezeFails(t *testing.T) {
	v := types.StringToAddress("0x3333")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	header := makeUptimeHeader(11, set)
	txn := newPosTestTransitionWithStaking(t, v)

	origStake := getOrInitStakeSnapshotFn
	getOrInitStakeSnapshotFn = func(_ *state.Transition, _ uint64, _ types.Address) (*big.Int, error) {
		return nil, errors.New("injected stake snapshot failure")
	}
	defer func() { getOrInitStakeSnapshotFn = origStake }()

	err := RecordBlockUptime(header, 10, set, testHeaderSigner(), UptimeConfig{}, txn)
	require.Error(t, err)
	require.Contains(t, err.Error(), "freeze stake snapshot")
}

func TestRecordBlockUptime_UsesHeaderValidatorSetWhenEpochValidatorsUnavailable(t *testing.T) {
	v := types.StringToAddress("0x4444")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	header, fs := makeUptimeHeaderWithFixedSigner(11, set, v)
	txn := newPosTestTransitionWithStaking(t, v)

	origEnsure := ensureEpochValidatorsSnapshotFn
	var capturedLen int
	ensureEpochValidatorsSnapshotFn = func(st *state.Transition, epoch uint64, got validators.Validators) (bool, error) {
		if got != nil {
			capturedLen = got.Len()
		}

		return origEnsure(st, epoch, got)
	}
	defer func() { ensureEpochValidatorsSnapshotFn = origEnsure }()

	err := RecordBlockUptime(header, 10, nil, fs, UptimeConfig{}, txn)
	require.NoError(t, err)
	require.Equal(t, set.Len(), capturedLen)
}

func TestRecordBlockUptime_DoesNotRefreezeEpochSnapshotWhenReprocessingWithMissingEpochValidators(t *testing.T) {
	v := types.StringToAddress("0x5555")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	header, fs := makeUptimeHeaderWithFixedSigner(11, set, v)
	txn := newPosTestTransitionWithStaking(t, v)

	origEnsure := ensureEpochValidatorsSnapshotFn
	calls := 0
	ensureEpochValidatorsSnapshotFn = func(st *state.Transition, epoch uint64, got validators.Validators) (bool, error) {
		calls++
		return origEnsure(st, epoch, got)
	}
	defer func() { ensureEpochValidatorsSnapshotFn = origEnsure }()

	require.NoError(t, RecordBlockUptime(header, 10, nil, fs, UptimeConfig{}, txn))
	require.NoError(t, RecordBlockUptime(header, 10, nil, fs, UptimeConfig{}, txn))
	require.Equal(t, 1, calls)
}

func TestRecordBlockUptime_FreezesEpochSnapshotsOnceAndKeepsFinalizeBasisStable(t *testing.T) {
	v1 := types.StringToAddress("0x7771")
	v2 := types.StringToAddress("0x7772")
	set := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(v1),
		validators.NewECDSAValidator(v2),
	)
	headerA, fsA := makeUptimeHeaderWithFixedSigner(11, set, v1) // first non-boundary block of epoch 2
	headerB, fsB := makeUptimeHeaderWithFixedSigner(12, set, v1)
	txn := newPosTestTransitionWithStakingValidators(t, set, 1, 10)
	epoch := epochOf(headerA.Number, 10)

	writeStakedAmount(txn, v1, big.NewInt(100))
	writeStakedAmount(txn, v2, big.NewInt(200))

	origEnsure := ensureEpochValidatorsSnapshotFn
	origStake := getOrInitStakeSnapshotFn
	origStakerStake := getOrInitStakerStakeSnapshotFn
	calls := 0
	currentStake := map[types.Address]*big.Int{
		v1: big.NewInt(100),
		v2: big.NewInt(200),
	}
	ensureEpochValidatorsSnapshotFn = func(st *state.Transition, ep uint64, got validators.Validators) (bool, error) {
		calls++
		return origEnsure(st, ep, got)
	}
	getOrInitStakeSnapshotFn = func(st *state.Transition, ep uint64, addr types.Address) (*big.Int, error) {
		key := keyStakeSnapshot(ep, addr)
		cur := bigFromHash(st.Txn().GetState(PosSysAddr, key))
		if cur.Sign() != 0 {
			return cur, nil
		}
		v, ok := currentStake[addr]
		if !ok {
			v = big.NewInt(0)
		}
		st.Txn().SetState(PosSysAddr, key, bigToHash(v))
		return new(big.Int).Set(v), nil
	}
	getOrInitStakerStakeSnapshotFn = func(st *state.Transition, ep uint64, addr types.Address) (*big.Int, error) {
		key := keyStakerStakeSnapshot(ep, addr)
		cur := bigFromHash(st.Txn().GetState(PosSysAddr, key))
		if cur.Sign() != 0 {
			return cur, nil
		}
		v, ok := currentStake[addr]
		if !ok {
			v = big.NewInt(0)
		}
		st.Txn().SetState(PosSysAddr, key, bigToHash(v))
		return new(big.Int).Set(v), nil
	}
	defer func() {
		ensureEpochValidatorsSnapshotFn = origEnsure
		getOrInitStakeSnapshotFn = origStake
		getOrInitStakerStakeSnapshotFn = origStakerStake
	}()

	require.NoError(t, RecordBlockUptime(headerA, 10, set, fsA, UptimeConfig{}, txn))
	require.Equal(t, 1, calls)

	// Mutate staking state after the initial epoch freeze.
	writeStakedAmount(txn, v1, big.NewInt(999))
	currentStake[v1] = big.NewInt(999)
	setValidatorInactiveInStaking(txn, v2, 12)
	require.False(t, isValidatorMetaActive(bigFromHash(txn.Txn().GetState(staking.AddrStakingContract, stakingMetaPackedKey(v2)))))

	require.NoError(t, RecordBlockUptime(headerB, 10, set, fsB, UptimeConfig{}, txn))
	require.Equal(t, 1, calls, "epoch snapshot must only be frozen once per epoch")

	// Snapshot-backed finalize basis must stay frozen to the first processed block state.
	require.Equal(t, int64(100), bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakeSnapshot(epoch, v1))).Int64())
	require.Equal(t, int64(200), bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakeSnapshot(epoch, v2))).Int64())
	require.ElementsMatch(t, []types.Address{v1, v2}, loadEpochValidatorsSnapshot(txn, epoch))
}

func TestRecordBlockUptime_ProposerUnavailableDoesNotOverwriteSnapshotOrDoubleCount(t *testing.T) {
	v := types.StringToAddress("0x8881")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	header := makeUptimeHeader(11, set) // empty proposer seal => proposer unavailable path
	txn := newPosTestTransitionWithStakingValidators(t, set, 1, 10)
	epoch := epochOf(header.Number, 10)

	writeStakedAmount(txn, v, big.NewInt(55))

	origEnsure := ensureEpochValidatorsSnapshotFn
	origStake := getOrInitStakeSnapshotFn
	origStakerStake := getOrInitStakerStakeSnapshotFn
	calls := 0
	currentStake := big.NewInt(55)
	ensureEpochValidatorsSnapshotFn = func(st *state.Transition, ep uint64, got validators.Validators) (bool, error) {
		calls++
		return origEnsure(st, ep, got)
	}
	getOrInitStakeSnapshotFn = func(st *state.Transition, ep uint64, addr types.Address) (*big.Int, error) {
		key := keyStakeSnapshot(ep, addr)
		cur := bigFromHash(st.Txn().GetState(PosSysAddr, key))
		if cur.Sign() != 0 {
			return cur, nil
		}
		st.Txn().SetState(PosSysAddr, key, bigToHash(currentStake))
		return new(big.Int).Set(currentStake), nil
	}
	getOrInitStakerStakeSnapshotFn = func(st *state.Transition, ep uint64, addr types.Address) (*big.Int, error) {
		key := keyStakerStakeSnapshot(ep, addr)
		cur := bigFromHash(st.Txn().GetState(PosSysAddr, key))
		if cur.Sign() != 0 {
			return cur, nil
		}
		st.Txn().SetState(PosSysAddr, key, bigToHash(currentStake))
		return new(big.Int).Set(currentStake), nil
	}
	defer func() {
		ensureEpochValidatorsSnapshotFn = origEnsure
		getOrInitStakeSnapshotFn = origStake
		getOrInitStakerStakeSnapshotFn = origStakerStake
	}()

	require.Error(t, RecordBlockUptime(header, 10, set, testHeaderSigner(), UptimeConfig{}, txn))
	require.Equal(t, 1, calls)
	require.Equal(t, int64(55), bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakeSnapshot(epoch, v))).Int64())
	require.Equal(t, uint64(0), u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerSlots(epoch, v))))
	require.Equal(t, uint64(0), u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerMissed(epoch, v))))

	// Reprocessing same block with changed live stake must not rewrite frozen snapshot or double count.
	writeStakedAmount(txn, v, big.NewInt(777))
	currentStake = big.NewInt(777)
	require.Error(t, RecordBlockUptime(header, 10, set, testHeaderSigner(), UptimeConfig{}, txn))
	require.Equal(t, 1, calls)
	require.Equal(t, int64(55), bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakeSnapshot(epoch, v))).Int64())
	require.Equal(t, uint64(0), u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerSlots(epoch, v))))
	require.Equal(t, uint64(0), u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerMissed(epoch, v))))
}
