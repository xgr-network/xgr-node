package ibft

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWeightedQuorum_TwoOfFourEqualWeightsIsNotEnough(t *testing.T) {
	t.Parallel()

	total := big.NewInt(40000)
	quorum := weightedQuorumThreshold(total)
	require.Equal(t, "26667", quorum.String())

	collected := big.NewInt(20000) // 2 of 4 validators with equal 10000 weight each
	require.False(t, hasWeightedQuorum(collected, total))
}
