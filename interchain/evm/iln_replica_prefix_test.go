package evm

import (
    "testing"

    "github.com/stretchr/testify/require"
    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

func TestV322ImmutableInsertionPrefixFailsClosed(t *testing.T) {
    a:=crypto.Keccak256Hash([]byte("first"))
    b:=crypto.Keccak256Hash([]byte("second"))
    hook:=types.StringToAddress("0x1111111111111111111111111111111111111111")
    tree:=&ilnMerkleAccumulator{}
    require.NoError(t,tree.insert(0,a))
    require.NoError(t,tree.insert(1,b))
    full:=ILNReplicaCheckpoint{ChainID:42161,Hook:hook,Root:tree.root(),Count:2,BlockNumber:192}
    replica:=ILNReplica{ChainID:42161,Hook:hook,Root:full.Root,Leaves:[]types.Hash{a,b},CheckpointBlock:192}
    prefix,proof,err:=VerifiedILNMessagePrefix(replica,full,0,128)
    require.NoError(t,err)
    require.Equal(t,uint64(1),proof.Count)
    require.Equal(t,uint64(128),proof.BlockNumber)
    require.NotEqual(t,full.Root,proof.Root)
    require.NoError(t,VerifyILNReplica(prefix,proof))
    _,_,err=VerifiedILNMessagePrefix(replica,full,1,193)
    require.Error(t,err)
    _,_,err=VerifiedILNMessagePrefix(replica,full,2,128)
    require.Error(t,err)
    corrupted:=replica
    corrupted.Leaves=append([]types.Hash(nil),replica.Leaves...)
    corrupted.Leaves[0]=crypto.Keccak256Hash([]byte("malicious"))
    _,_,err=VerifiedILNMessagePrefix(corrupted,full,0,128)
    require.Error(t,err)
}
