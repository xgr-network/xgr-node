package ibft

import (
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	"github.com/xgr-network/xgr-node/state"
	itrie "github.com/xgr-network/xgr-node/state/immutable-trie"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func TestStakeSnapshotHeight_UnitModeReturnsZero(t *testing.T) {
	t.Parallel()

	h, err := (&backendIBFT{}).stakeSnapshotHeight(12, nil, false)
	require.NoError(t, err)
	require.Equal(t, uint64(0), h)
}

func TestStakeSnapshotHeight_PosWithoutEpochUsesParentHeaderNumber(t *testing.T) {
	t.Parallel()

	h, err := (&backendIBFT{}).stakeSnapshotHeight(100, &types.Header{Number: 77}, true)
	require.NoError(t, err)
	require.Equal(t, uint64(77), h)
}

func TestStakeSnapshotHeight_PosUsesEpochBoundarySnapshot(t *testing.T) {
	t.Parallel()

	backend := &backendIBFT{epochSize: 10}

	h, err := backend.stakeSnapshotHeight(32, &types.Header{Number: 31}, true)
	require.NoError(t, err)
	require.Equal(t, uint64(30), h)

	h, err = backend.stakeSnapshotHeight(40, &types.Header{Number: 39}, true)
	require.NoError(t, err)
	require.Equal(t, uint64(39), h)
}

func TestStakeSnapshotHeight_PosClampsToFirstActiveHeight(t *testing.T) {
	t.Parallel()

	backend := &backendIBFT{
		epochSize:   20,
		forkManager: &votingPowerForkManager{active: func(h uint64) bool { return h >= 15 }},
	}

	h, err := backend.stakeSnapshotHeight(17, &types.Header{Number: 16}, true)
	require.NoError(t, err)
	require.Equal(t, uint64(15), h)
}

func TestEffectiveVotingPowerSnapshot_RequiresBlockchainLookupForHistoricalStakeSnapshot(t *testing.T) {
	t.Parallel()

	st := itrie.NewState(itrie.NewMemoryStorage())
	ex := state.NewExecutor(&chain.Params{Forks: chain.AllForksEnabled, BurnContract: map[uint64]types.Address{0: types.ZeroAddress}}, st, hclog.NewNullLogger())
	root, err := ex.WriteGenesis(nil, types.Hash{})
	require.NoError(t, err)
	ex.GetHash = func(*types.Header) state.GetHashByNumber { return func(uint64) types.Hash { return root } }

	backend := &backendIBFT{
		executor:    ex,
		epochSize:   10,
		uptimeCfg:   pos.UptimeConfig{MicroEpochNominalWeight: 10_000},
		forkManager: &votingPowerForkManager{active: func(uint64) bool { return true }},
	}
	valSet := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001")),
	)

	_, _, err = backend.effectiveVotingPowerSnapshot(32, valSet, &types.Header{Number: 31, StateRoot: root})
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires blockchain header lookup")
}

func TestStakeSnapshotHeight_PosHeightZeroWithoutParentFails(t *testing.T) {
	t.Parallel()

	_, err := (&backendIBFT{}).stakeSnapshotHeight(0, nil, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires parent state")
}
