package evm

import (
 "testing"
 "github.com/stretchr/testify/require"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

func TestILNMessagePrefixDeterministicAcrossDifferentSourceHeads(t *testing.T) {
 hook:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 leaves:=[]types.Hash{crypto.Keccak256Hash([]byte("one")),crypto.Keccak256Hash([]byte("two"))}
 tree:=&ilnMerkleAccumulator{}
 for i,leaf:=range leaves {require.NoError(t,tree.insert(uint32(i),leaf))}
 a:=ILNReplicaCheckpoint{ChainID:42161,Hook:hook,Root:tree.root(),Count:2,BlockNumber:128}
 b:=a;b.BlockNumber=192
 ra:=ILNReplica{ChainID:a.ChainID,Hook:hook,Root:a.Root,CheckpointBlock:a.BlockNumber,Leaves:leaves}
 rb:=ra;rb.CheckpointBlock=b.BlockNumber
 pa,ca,err:=VerifiedILNMessagePrefix(ra,a,0);require.NoError(t,err)
 pb,cb,err:=VerifiedILNMessagePrefix(rb,b,0);require.NoError(t,err)
 require.Equal(t,ca,cb)
 require.Equal(t,pa.Leaves,pb.Leaves)
 require.Equal(t,uint64(1),ca.BlockNumber)
 require.Equal(t,uint64(1),ca.Count)
 _,_,err=VerifiedILNMessagePrefix(ra,a,2)
 require.Error(t,err)
 bad:=ra;bad.Leaves=[]types.Hash{leaves[1],leaves[0]}
 _,_,err=VerifiedILNMessagePrefix(bad,a,0)
 require.Error(t,err)
}
