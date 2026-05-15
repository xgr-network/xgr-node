package ibft

import (
	"math/big"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHasWeightedQuorum_ThresholdMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		total     uint64
		collected uint64
		ok        bool
	}{
		{name: "total=0,signed=0", total: 0, collected: 0, ok: false},
		{name: "total=5,signed=0", total: 5, collected: 0, ok: false},
		{name: "total=0,signed=1", total: 0, collected: 1, ok: false},
		{name: "total=5,signed=3", total: 5, collected: 3, ok: false},
		{name: "total=5,signed=4", total: 5, collected: 4, ok: true},
		{name: "total=6,signed=4", total: 6, collected: 4, ok: true},
		{name: "total=6,signed=3", total: 6, collected: 3, ok: false},
		{name: "total=8,signed=5", total: 8, collected: 5, ok: false},
		{name: "total=8,signed=6", total: 8, collected: 6, ok: true},
		{name: "total=9,signed=6", total: 9, collected: 6, ok: true},
		{name: "total=9,signed=5", total: 9, collected: 5, ok: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(
				t,
				tc.ok,
				hasWeightedQuorum(
					new(big.Int).SetUint64(tc.collected),
					new(big.Int).SetUint64(tc.total),
				),
			)
		})
	}
}

func TestEqualWeights_PoAAndWeightedSemantics(t *testing.T) {
	t.Parallel()

	cases := []struct {
		totalPower       uint64
		weightedQuorum   uint64
		classicPoAQuorum int
	}{
		// Weighted PoS quorum intentionally uses ceil(2/3 * totalVotingPower).
		// For N=3 equal unit weights this differs from classic IBFT
		// OptimalQuorumSize, which requires the full set when F=0.
		{totalPower: 1, weightedQuorum: 1, classicPoAQuorum: 1},
		{totalPower: 2, weightedQuorum: 2, classicPoAQuorum: 2},
		{totalPower: 3, weightedQuorum: 2, classicPoAQuorum: 3},
		{totalPower: 5, weightedQuorum: 4, classicPoAQuorum: 4},
		{totalPower: 6, weightedQuorum: 4, classicPoAQuorum: 4},
		{totalPower: 8, weightedQuorum: 6, classicPoAQuorum: 6},
		{totalPower: 9, weightedQuorum: 6, classicPoAQuorum: 6},
	}

	for _, tc := range cases {
		tc := tc
		t.Run("N="+strconv.FormatUint(tc.totalPower, 10), func(t *testing.T) {
			t.Parallel()

			pool := newTesterAccountPool(t, int(tc.totalPower))
			require.Equal(t, tc.classicPoAQuorum, OptimalQuorumSize(pool.ValidatorSet()))
			require.Equal(
				t,
				new(big.Int).SetUint64(tc.weightedQuorum),
				weightedQuorumThreshold(new(big.Int).SetUint64(tc.totalPower)),
			)
		})
	}
}

func TestSnapshotVotingPowers_UsesUnifiedWeightedThreshold(t *testing.T) {
	t.Parallel()

	pool := newTesterAccountPool(t, 6)
	backend := &backendIBFT{}

	_, snapshot, err := backend.snapshotVotingPowers(1, 0, pool.ValidatorSet(), nil, false)
	require.NoError(t, err)
	require.Equal(t, "6", snapshot.totalVotingPower)
	require.Equal(t, "4", snapshot.quorumThreshold)
}
