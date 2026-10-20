package evm

import (
    "testing"

    "github.com/stretchr/testify/require"
    "github.com/xgr-network/xgr-node/types"
)

// Baseline for the live-hook v3.2.0 reader: the previously deployed Base hook
// uses one insertion (index zero) for the known pending test message.
func TestILNFirstLeafCheckpointRoot(t *testing.T) {
    messageID := types.StringToHash("0xf735484813a91eda5c60e959f4038b97ea518de5a9e92de440bd5612c1f387a5")
    tree := &ilnMerkleAccumulator{}
    require.NoError(t, tree.insert(0, messageID))
    require.Equal(t, uint64(1), tree.count)
    require.Equal(t,
        types.StringToHash("0xc7d2d213bf598c06c27dbc083bfe19fa9fddafe9c1f962736241b2d9de7fc283"),
        tree.root())
}
