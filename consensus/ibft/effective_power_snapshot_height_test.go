package ibft

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/types"
)

func TestStakeSnapshotHeight_UnitModeReturnsZero(t *testing.T) {
	t.Parallel()

	h, err := stakeSnapshotHeight(12, nil, false)
	require.NoError(t, err)
	require.Equal(t, uint64(0), h)
}

func TestStakeSnapshotHeight_PosUsesParentHeaderNumber(t *testing.T) {
	t.Parallel()

	h, err := stakeSnapshotHeight(100, &types.Header{Number: 77}, true)
	require.NoError(t, err)
	require.Equal(t, uint64(77), h)
}

func TestStakeSnapshotHeight_PosHeightZeroWithoutParentFails(t *testing.T) {
	t.Parallel()

	_, err := stakeSnapshotHeight(0, nil, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires parent state")
}
