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
	contractstore "github.com/xgr-network/xgr-node/validators/store/contract"
)

func makeFinalizeHeader(number uint64, epochSize uint64, set validators.Validators) *types.Header {
	extra := &signer.IstanbulExtra{
		Validators:           set,
		CommittedSeals:       &signer.SerializedSeal{},
		ParentCommittedSeals: &signer.SerializedSeal{},
	}

	return &types.Header{
		Number:    number,
		Timestamp: number * epochSize,
		ExtraData: extra.MarshalRLPTo(make([]byte, signer.IstanbulExtraVanity)),
	}
}

func TestFinalizeEpoch_ZeroSlotsDoesNotDeactivateValidator(t *testing.T) {
	t.Parallel()

	validator := types.StringToAddress("0xabc1")
	txn := newPosTestTransitionWithStaking(t, validator)
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validator))
	header := makeFinalizeHeader(20, 10, set)
	epoch := epochOf(header.Number-1, 10)
	requireSnapshotCreated(t, txn, epoch, set)

	require.NoError(t, FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn))

	packed := txn.Txn().GetState(staking.AddrStakingContract, stakingMetaPackedKey(validator))
	require.True(t, isValidatorMetaActive(bigFromHash(packed)))
	require.Equal(t, uint64(0), u64FromHash(txn.Txn().GetState(staking.AddrStakingContract, stakingDeactivatedAtBlockKey(validator))))
}

func TestFinalizeEpoch_DeactivatedAtBlockIsIdempotent(t *testing.T) {
	t.Parallel()

	validator := types.StringToAddress("0xabc2")
	txn := newPosTestTransitionWithStaking(t, validator)

	setValidatorInactiveInStaking(txn, validator, 100)
	require.Equal(t, uint64(100), u64FromHash(txn.Txn().GetState(staking.AddrStakingContract, stakingDeactivatedAtBlockKey(validator))))

	setValidatorInactiveInStaking(txn, validator, 200)
	require.Equal(t, uint64(100), u64FromHash(txn.Txn().GetState(staking.AddrStakingContract, stakingDeactivatedAtBlockKey(validator))))
}

func TestFinalizeEpoch_ReturnsErrorOnDecodeFailure(t *testing.T) {
	validator := types.StringToAddress("0xabc3")
	txn := newPosTestTransitionWithStaking(t, validator)
	header := &types.Header{
		Number:    20,
		Timestamp: 200,
		ExtraData: []byte{0x1, 0x2, 0x3},
	}

	err := FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn)
	require.Error(t, err)
}

func TestFinalizeEpoch_ReturnsErrorOnEmptyValidators(t *testing.T) {
	validator := types.StringToAddress("0xabc4")
	txn := newPosTestTransitionWithStaking(t, validator)
	header := makeFinalizeHeader(20, 10, validators.NewECDSAValidatorSet())

	err := FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn)
	require.Error(t, err)
}

func TestFinalizeEpoch_ReturnsErrorOnMissingEpochValidatorSnapshot(t *testing.T) {
	validator := types.StringToAddress("0xabca")
	txn := newPosTestTransitionWithStaking(t, validator)
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validator))
	header := makeFinalizeHeader(20, 10, set)
	txn.Txn().SetState(PosSysAddr, keyUptimeLastProcessedSlot(), u64ToHash(header.Number-1))

	err := FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing epoch validator snapshot for epoch 2")
}

func TestFinalizeEpoch_MissingSnapshotForPrePoSEpochAtCutoverBoundaryErrors(t *testing.T) {
	t.Parallel()

	validator := types.StringToAddress("0xabcb")
	txn := newPosTestTransitionWithStaking(t, validator)
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validator))
	header := makeFinalizeHeader(50, 50, set)

	require.Error(t, FinalizeEpoch(header, 50, UptimeConfig{}, testHeaderSigner(), txn))
}

func TestFinalizeEpoch_DoesNotSkipMissingSnapshotOutsideMigrationCase(t *testing.T) {
	t.Parallel()

	validator := types.StringToAddress("0xabcc")
	txn := newPosTestTransitionWithStaking(t, validator)
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validator))
	header := makeFinalizeHeader(150, 50, set)

	err := FinalizeEpoch(header, 50, UptimeConfig{}, testHeaderSigner(), txn)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing epoch validator snapshot for epoch 3")
}

func TestFinalizeEpoch_ReturnsErrorOnRewardTransferFailure(t *testing.T) {
	validator := types.StringToAddress("0xabc5")
	txn := newPosTestTransitionWithStaking(t, validator)
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validator))
	header := makeFinalizeHeader(20, 10, set)
	epoch := epochOf(header.Number-1, 10)
	requireSnapshotCreated(t, txn, epoch, set)
	originalStake := new(big.Int).Set(readStakedAmount(txn, validator))

	txn.Txn().SetState(PosSysAddr, keyProposerSlots(epoch, validator), u64ToHash(1))
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, validator), bigToHash(big.NewInt(100)))
	txn.Txn().SetBalance(state.FeePoolAddress, big.NewInt(100))

	origTransfer := posTransferFn
	posTransferFn = func(txn *state.Transition, from, to types.Address, amount *big.Int) error {
		if from == state.FeePoolAddress && to == staking.AddrStakingContract {
			return errors.New("injected transfer failure")
		}
		return origTransfer(txn, from, to, amount)
	}
	defer func() {
		posTransferFn = origTransfer
	}()

	err := FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn)
	require.Error(t, err)
	require.Equal(t, 0, readStakedAmount(txn, validator).Cmp(originalStake))
}

func TestFinalizeEpoch_ZeroUptimeMarksValidatorInactiveAndFetcherExcludesFromEligiblePath(t *testing.T) {
	v1 := types.StringToAddress("0xabc6")
	v2 := types.StringToAddress("0xabc7")
	set := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(v1),
		validators.NewECDSAValidator(v2),
	)
	txn := newPosTestTransitionWithStakingValidatorsAtBlock(t, set, 1, 10, 21)
	header := makeFinalizeHeader(20, 10, set)
	epoch := epochOf(header.Number-1, 10)
	requireSnapshotCreated(t, txn, epoch, set)

	// Ensure both pass strict threshold before uptime filtering in finalize (values in wei).
	wei := big.NewInt(1_000_000_000_000_000_000)
	highStakeWei := new(big.Int).Mul(big.NewInt(3_000_000), wei) // 3,000,000 XGR
	writeStakedAmount(txn, v1, highStakeWei)
	writeStakedAmount(txn, v2, highStakeWei)
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, v1), bigToHash(highStakeWei))
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, v2), bigToHash(highStakeWei))
	txn.Txn().SetState(PosSysAddr, keyStakerStakeSnapshot(epoch, v1), bigToHash(highStakeWei))
	txn.Txn().SetState(PosSysAddr, keyStakerStakeSnapshot(epoch, v2), bigToHash(highStakeWei))

	// v1 has zero uptime for this epoch; v2 is healthy.
	txn.Txn().SetState(PosSysAddr, keyProposerSlots(epoch, v1), u64ToHash(1))
	txn.Txn().SetState(PosSysAddr, keyProposerMissed(epoch, v1), u64ToHash(1))
	txn.Txn().SetState(PosSysAddr, keyProposerSlots(epoch, v2), u64ToHash(1))
	txn.Txn().SetState(PosSysAddr, keyProposerMissed(epoch, v2), u64ToHash(0))

	require.NoError(t, FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn))
	logs := txn.Txn().Logs()
	require.NotEmpty(t, logs)
	require.Equal(t, posEventEpochFinalizedTopic, logs[0].Topics[0])
	require.Equal(t, uint64(1), new(big.Int).SetBytes(logs[0].Data[4*32:5*32]).Uint64(), "EpochFinalized.activeValidators must reflect post-finalization active validators")

	packed := bigFromHash(txn.Txn().GetState(staking.AddrStakingContract, stakingMetaPackedKey(v1)))
	require.False(t, isValidatorMetaActive(packed))
	require.Equal(t, uint64(header.Number), u64FromHash(txn.Txn().GetState(staking.AddrStakingContract, stakingDeactivatedAtBlockKey(v1))))
	require.Greater(t, readStakedAmount(txn, v1).Sign(), 0, "validator must remain in registry/state with stake")
	require.False(t, contractstore.IsEmergencyModeActive(txn), "fetcher must use normal strict eligible path in this test")

	fetched, err := contractstore.FetchECDSAValidators(txn, types.ZeroAddress)
	require.NoError(t, err)
	require.Equal(t, 1, fetched.Len())
	require.Equal(t, v2, fetched.At(0).Addr(), "inactive validator must not be in the regular eligible set")
}

func TestFinalizeEpoch_FailsDeterministicallyWhenEpochProducedNoUsableSnapshot(t *testing.T) {
	validator := types.StringToAddress("0xabd0")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validator))
	txn := newPosTestTransitionWithStakingValidators(t, set, 1, 10)

	// Finalize epoch 2 at boundary block 20 without any successful RecordBlockUptime freeze in epoch 2.
	header := makeFinalizeHeader(20, 10, set)
	err := FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn)
	require.EqualError(t, err, "missing epoch validator snapshot for epoch 2")
}

func TestFinalizeEpoch_MissingNoSlashModeErrors(t *testing.T) {
	validator := types.StringToAddress("0xabd1")
	txn := newPosTestTransitionWithStaking(t, validator)
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validator))
	header := makeFinalizeHeader(20, 10, set)
	epoch := epochOf(header.Number-1, 10)

	created, err := ensureEpochValidatorsSnapshot(txn, epoch, set)
	require.NoError(t, err)
	require.True(t, created)

	err = FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn)
	require.EqualError(t, err, "missing no-slash mode for epoch 2")
}

func TestFinalizeEpoch_NoSlashModeTrueSkipsSlashing(t *testing.T) {
	validator := types.StringToAddress("0xabd2")
	txn := newPosTestTransitionWithStaking(t, validator)
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validator))
	header := makeFinalizeHeader(20, 10, set)
	epoch := epochOf(header.Number-1, 10)
	requireSnapshotCreated(t, txn, epoch, set)
	require.NoError(t, StoreMacroEpochNoSlashMode(txn, epoch, true))
	beforeStake := new(big.Int).Set(readStakedAmount(txn, validator))

	txn.Txn().SetState(PosSysAddr, keyProposerSlots(epoch, validator), u64ToHash(1))
	txn.Txn().SetState(PosSysAddr, keyProposerMissed(epoch, validator), u64ToHash(1))
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, validator), bigToHash(big.NewInt(100)))
	txn.Txn().SetState(PosSysAddr, keyStakerStakeSnapshot(epoch, validator), bigToHash(big.NewInt(100)))

	require.NoError(t, FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn))
	require.Equal(t, 0, beforeStake.Cmp(readStakedAmount(txn, validator)))
	for _, lg := range txn.Txn().Logs() {
		require.NotEqual(t, posEventStakerSlashedTopic, lg.Topics[0])
	}
}

func TestFinalizeEpoch_NoSlashModeFalseAllowsSlashing(t *testing.T) {
	validator := types.StringToAddress("0xabd3")
	txn := newPosTestTransitionWithStaking(t, validator)
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validator))
	header := makeFinalizeHeader(20, 10, set)
	epoch := epochOf(header.Number-1, 10)
	requireSnapshotCreated(t, txn, epoch, set)
	require.NoError(t, StoreMacroEpochNoSlashMode(txn, epoch, false))
	txn.Txn().SetState(PosSysAddr, keyProposerSlots(epoch, validator), u64ToHash(1))
	txn.Txn().SetState(PosSysAddr, keyProposerMissed(epoch, validator), u64ToHash(1))
	txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, validator), bigToHash(big.NewInt(100)))
	txn.Txn().SetState(PosSysAddr, keyStakerStakeSnapshot(epoch, validator), bigToHash(big.NewInt(100)))

	require.NoError(t, FinalizeEpoch(header, 10, UptimeConfig{}, testHeaderSigner(), txn))
}
