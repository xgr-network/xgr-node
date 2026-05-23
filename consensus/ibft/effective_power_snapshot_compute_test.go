package ibft

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func TestComputeVotingPowersFromStakeSnapshot_UnitVotingWithoutStakeSnapshot(t *testing.T) {
	t.Parallel()

	valSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000002")),
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000003")),
	)

	powers, _, err := computeVotingPowersFromStakeSnapshot(11, valSet, nil, nil, false, pos.UptimeConfig{MicroEpochNominalWeight: 10_000})
	require.NoError(t, err)
	for i := 0; i < valSet.Len(); i++ {
		addr := valSet.At(uint64(i)).Addr()
		require.Equal(t, uint64(1), powers[types.AddressToString(addr)].Uint64())
	}
}

func TestComputeVotingPowersFromStakeSnapshot_MissingZeroNegativeStakeErrors(t *testing.T) {
	t.Parallel()

	a := types.StringToAddress("0x1000000000000000000000000000000000000001")
	b := types.StringToAddress("0x1000000000000000000000000000000000000002")
	c := types.StringToAddress("0x1000000000000000000000000000000000000003")
	valSet := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(a), validators.NewECDSAValidator(b), validators.NewECDSAValidator(c))
	tx := newBareVotingPowerTestTransition(t, 10)
	setNominalAndEffectiveWeight(tx, a, 10_000, 10_000)
	setNominalAndEffectiveWeight(tx, b, 10_000, 10_000)
	setNominalAndEffectiveWeight(tx, c, 10_000, 10_000)

	_, _, err := computeVotingPowersFromStakeSnapshot(11, valSet, map[types.Address]*big.Int{a: big.NewInt(100), b: big.NewInt(200)}, tx, true, pos.UptimeConfig{MicroEpochNominalWeight: 10_000})
	require.Error(t, err)

	_, _, err = computeVotingPowersFromStakeSnapshot(11, valSet, map[types.Address]*big.Int{a: big.NewInt(100), b: big.NewInt(0), c: big.NewInt(1)}, tx, true, pos.UptimeConfig{MicroEpochNominalWeight: 10_000})
	require.Error(t, err)

	_, _, err = computeVotingPowersFromStakeSnapshot(11, valSet, map[types.Address]*big.Int{a: big.NewInt(100), b: big.NewInt(-1), c: big.NewInt(1)}, tx, true, pos.UptimeConfig{MicroEpochNominalWeight: 10_000})
	require.Error(t, err)
}

func TestComputeVotingPowersFromStakeSnapshot_ExtraStakeIgnoredAndDecayDynamic(t *testing.T) {
	t.Parallel()

	a := types.StringToAddress("0x1000000000000000000000000000000000000001")
	b := types.StringToAddress("0x1000000000000000000000000000000000000002")
	c := types.StringToAddress("0x1000000000000000000000000000000000000003")
	d := types.StringToAddress("0x1000000000000000000000000000000000000004")
	valSet := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(a), validators.NewECDSAValidator(b), validators.NewECDSAValidator(c))

	stakeSnapshot := map[types.Address]*big.Int{a: big.NewInt(100), b: big.NewInt(200), c: big.NewInt(300), d: big.NewInt(999)}

	tx1 := newBareVotingPowerTestTransition(t, 10)
	setNominalAndEffectiveWeight(tx1, a, 10_000, 10_000)
	setNominalAndEffectiveWeight(tx1, b, 10_000, 10_000)
	setNominalAndEffectiveWeight(tx1, c, 10_000, 10_000)

	p1, _, err := computeVotingPowersFromStakeSnapshot(11, valSet, stakeSnapshot, tx1, true, pos.UptimeConfig{MicroEpochNominalWeight: 10_000})
	require.NoError(t, err)
	_, ok := p1[types.AddressToString(d)]
	require.False(t, ok)
	require.Equal(t, uint64(200), p1[types.AddressToString(b)].Uint64())

	tx2 := newBareVotingPowerTestTransition(t, 10)
	setNominalAndEffectiveWeight(tx2, a, 10_000, 5_000)
	setNominalAndEffectiveWeight(tx2, b, 10_000, 10_000)
	setNominalAndEffectiveWeight(tx2, c, 10_000, 10_000)

	p2, _, err := computeVotingPowersFromStakeSnapshot(11, valSet, stakeSnapshot, tx2, true, pos.UptimeConfig{MicroEpochNominalWeight: 10_000})
	require.NoError(t, err)
	require.NotEqual(t, p1[types.AddressToString(a)].Uint64(), p2[types.AddressToString(a)].Uint64())
	require.Equal(t, uint64(200), p2[types.AddressToString(b)].Uint64())
}

func TestComputeVotingPowersFromStakeSnapshot_WeightedModeRequiresDecayTx(t *testing.T) {
	t.Parallel()

	a := types.StringToAddress("0x1000000000000000000000000000000000000001")
	b := types.StringToAddress("0x1000000000000000000000000000000000000002")
	c := types.StringToAddress("0x1000000000000000000000000000000000000003")
	valSet := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(a), validators.NewECDSAValidator(b), validators.NewECDSAValidator(c))

	_, _, err := computeVotingPowersFromStakeSnapshot(11, valSet, map[types.Address]*big.Int{a: big.NewInt(100), b: big.NewInt(200), c: big.NewInt(300)}, nil, true, pos.UptimeConfig{MicroEpochNominalWeight: 10_000})
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires decay state")
}

func TestComputeVotingPowersFromStakeSnapshot_WeightedModeRequiresStakeSnapshot(t *testing.T) {
	t.Parallel()

	a := types.StringToAddress("0x1000000000000000000000000000000000000001")
	b := types.StringToAddress("0x1000000000000000000000000000000000000002")
	c := types.StringToAddress("0x1000000000000000000000000000000000000003")
	valSet := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(a), validators.NewECDSAValidator(b), validators.NewECDSAValidator(c))
	tx := newBareVotingPowerTestTransition(t, 10)
	setNominalAndEffectiveWeight(tx, a, 10_000, 10_000)
	setNominalAndEffectiveWeight(tx, b, 10_000, 10_000)
	setNominalAndEffectiveWeight(tx, c, 10_000, 10_000)

	_, _, err := computeVotingPowersFromStakeSnapshot(11, valSet, nil, tx, true, pos.UptimeConfig{MicroEpochNominalWeight: 10_000})
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires stake snapshot")
}

func TestComputeVotingPowersFromStakeSnapshot_RejectsEmptyValidatorSet(t *testing.T) {
	t.Parallel()

	_, _, err := computeVotingPowersFromStakeSnapshot(11, nil, nil, nil, false, pos.UptimeConfig{MicroEpochNominalWeight: 10_000})
	require.Error(t, err)
	require.Contains(t, err.Error(), "validator set is empty")

	emptySet := validators.NewECDSAValidatorSet()
	_, _, err = computeVotingPowersFromStakeSnapshot(11, emptySet, nil, nil, false, pos.UptimeConfig{MicroEpochNominalWeight: 10_000})
	require.Error(t, err)
	require.Contains(t, err.Error(), "validator set is empty")
}
