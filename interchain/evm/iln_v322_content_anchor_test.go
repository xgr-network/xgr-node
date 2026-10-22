package evm

import (
    "testing"

    "github.com/stretchr/testify/require"
    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

// The logical Merkle content must be unaffected by block-time, chain ID,
// or a source chain advancing many epochs without a new insertion.
func TestV322ContentIdenticalPrefixReanchorIsChainAgnostic(t *testing.T) {
    hook:=types.StringToAddress("0x1111111111111111111111111111111111111111")
    one:=crypto.Keccak256Hash([]byte("first"))
    two:=crypto.Keccak256Hash([]byte("second"))
    tree:=&ilnMerkleAccumulator{}
    require.NoError(t,tree.insert(0,one))
    require.NoError(t,tree.insert(1,two))
    root:=tree.root()
    for _,chain:=range []uint64{1643,8453,42161,10,137} {
        t.Run(string(rune('A'+chain%26)),func(t *testing.T) {
            old:=ILNReplica{ChainID:chain,Hook:hook,Root:root,CheckpointBlock:32,Leaves:[]types.Hash{one,two}}
            start:=ILNReplicaCheckpoint{ChainID:chain,Hook:hook,Root:root,Count:2,BlockNumber:32}
            require.NoError(t,VerifyILNReplica(old,start))
            // 1024 source epochs without an insertion.
            next:=start
            next.BlockNumber=32768
            refreshed,err:=ExtendILNReplica(next,old,nil)
            require.NoError(t,err)
            require.Equal(t,root,refreshed.Root)
            require.Equal(t,next.BlockNumber,refreshed.CheckpointBlock)
            require.NoError(t,VerifyILNReplica(refreshed,next))
            poisoned:=old
            poisoned.Leaves=append([]types.Hash(nil),old.Leaves...)
            poisoned.Leaves[1]=crypto.Keccak256Hash([]byte("poison"))
            _,err=ExtendILNReplica(next,poisoned,nil)
            require.ErrorIs(t,err,ErrILNReplicaUnverified)
            // The same root from a different chain or hook cannot be imported.
            cross:=next
            cross.ChainID=chain+1
            _,err=ExtendILNReplica(cross,old,nil)
            require.ErrorIs(t,err,ErrILNReplicaUnverified)
            cross=next
            cross.Hook=types.StringToAddress("0x2222222222222222222222222222222222222222")
            _,err=ExtendILNReplica(cross,old,nil)
            require.ErrorIs(t,err,ErrILNReplicaUnverified)
        })
    }
}
