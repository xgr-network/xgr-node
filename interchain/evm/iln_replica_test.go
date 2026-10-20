package evm

import (
 "os"
 "path/filepath"
 "testing"

 "github.com/stretchr/testify/require"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

func makeILNReplicaFixture(t *testing.T,n int)(ILNReplicaCheckpoint,[]ILNReplicaChunk){
 t.Helper()
 hook:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 tree:=&ilnMerkleAccumulator{}
 chunks:=[]ILNReplicaChunk{}
 for i:=0;i<n;i++ {
  id:=crypto.Keccak256Hash([]byte{byte(i>>24),byte(i>>16),byte(i>>8),byte(i)})
  require.NoError(t,tree.insert(uint32(i),id))
  if i%1024==0 {chunks=append(chunks,ILNReplicaChunk{ChainID:8453,Hook:hook,Offset:uint64(i)})}
  chunks[len(chunks)-1].Leaves=append(chunks[len(chunks)-1].Leaves,id)
 }
 return ILNReplicaCheckpoint{ChainID:8453,Hook:hook,Root:tree.root(),Count:uint64(n),BlockNumber:10000},chunks
}
func TestILNReplicaPeerSyncHonestAndMaliciousPeers(t *testing.T){
 canonical,chunks:=makeILNReplicaFixture(t,3000)
 out,err:=AssembleILNReplica(canonical,chunks)
 require.NoError(t,err)
 require.Equal(t,canonical.Root,out.Root)
 path:=filepath.Join(t.TempDir(),"interchain","iln","merkle","base","replica.json")
 require.NoError(t,StoreVerifiedILNReplica(path,out,canonical))
 before,err:=os.ReadFile(path);require.NoError(t,err)
 loaded,err:=LoadVerifiedILNReplica(path,canonical)
 require.NoError(t,err)
 require.Equal(t,out.Leaves,loaded.Leaves)
 bad:=append([]ILNReplicaChunk(nil),chunks...)
 bad[1].Leaves=append([]types.Hash(nil),bad[1].Leaves...)
 bad[1].Leaves[0]=crypto.Keccak256Hash([]byte("poisoned"))
 _,err=AssembleILNReplica(canonical,bad)
 require.ErrorIs(t,err,ErrILNReplicaUnverified)
 require.Equal(t,before,mustReadILNReplica(t,path),"poisoned peer cannot overwrite checkpoint")
 gaps:=append([]ILNReplicaChunk(nil),chunks...)
 gaps[1].Offset++
 _,err=AssembleILNReplica(canonical,gaps)
 require.ErrorIs(t,err,ErrILNReplicaUnverified)
 missing:=chunks[:len(chunks)-1]
 _,err=AssembleILNReplica(canonical,missing)
 require.ErrorIs(t,err,ErrILNReplicaUnverified)
 wrongChain:=append([]ILNReplicaChunk(nil),chunks...)
 wrongChain[0].ChainID=137
 _,err=AssembleILNReplica(canonical,wrongChain)
 require.ErrorIs(t,err,ErrILNReplicaUnverified)
 forged:=canonical
 forged.Root=crypto.Keccak256Hash([]byte("fake root"))
 _,err=AssembleILNReplica(forged,chunks)
 require.ErrorIs(t,err,ErrILNReplicaUnverified)
 // A peer with correct content but an incorrect block anchor cannot
 // impersonate the independently confirmed checkpoint.
 poisoned:=out
 poisoned.CheckpointBlock++
 require.ErrorIs(t,StoreVerifiedILNReplica(path,poisoned,canonical),ErrILNReplicaUnverified)
 require.Equal(t,before,mustReadILNReplica(t,path))
}
func mustReadILNReplica(t *testing.T,path string)[]byte{
 t.Helper();b,err:=os.ReadFile(path);require.NoError(t,err);return b
}
func TestILNReplicaParallelTenThousandLeaves(t *testing.T){
 canonical,chunks:=makeILNReplicaFixture(t,10000)
 require.Len(t,chunks,10)
 out,err:=AssembleILNReplica(canonical,chunks)
 require.NoError(t,err)
 require.Len(t,out.Leaves,10000)
 corrupted:=append([]ILNReplicaChunk(nil),chunks...)
 corrupted[9].Leaves=corrupted[9].Leaves[:len(corrupted[9].Leaves)-1]
 _,err=AssembleILNReplica(canonical,corrupted)
 require.ErrorIs(t,err,ErrILNReplicaUnverified)
}

func TestILNReplicaThreeValidatorColdJoinFailoverE2E(t *testing.T) {
 canonical,chunks:=makeILNReplicaFixture(t,4096)
 // Validator one is corrupt, validator two has lost its final chunk,
 // validator three is honest. New validator must fail closed on two peers
 // before installing data from the independently verified third.
 incoming:=[][]ILNReplicaChunk{
  append([]ILNReplicaChunk(nil),chunks...),
  chunks[:len(chunks)-1],
  chunks,
 }
 poisoned:=incoming[0][0]
 poisoned.Leaves=append([]types.Hash(nil),poisoned.Leaves...)
 poisoned.Leaves[7]=crypto.Keccak256Hash([]byte("byzantine peer"))
 incoming[0][0]=poisoned
 install:=filepath.Join(t.TempDir(),"data","interchain","merkle","8453","replica.json")
 rejected:=0
 for _,peer:=range incoming {
  replica,err:=AssembleILNReplica(canonical,peer)
  if err!=nil {
   rejected++
   _,statErr:=os.Stat(install)
   require.True(t,os.IsNotExist(statErr),"a rejected peer must never create live state")
   continue
  }
  require.NoError(t,StoreVerifiedILNReplica(install,replica,canonical))
 }
 require.Equal(t,2,rejected)
 restarted,err:=LoadVerifiedILNReplica(install,canonical)
 require.NoError(t,err)
 require.Len(t,restarted.Leaves,4096)
 oldBytes:=mustReadILNReplica(t,install)
 // A stale source checkpoint cannot promote a peer's signed-but-wrong root.
 stale:=canonical
 stale.BlockNumber++
 require.ErrorIs(t,StoreVerifiedILNReplica(install,restarted,stale),ErrILNReplicaUnverified)
 require.Equal(t,oldBytes,mustReadILNReplica(t,install))
 // Post-restart local disk corruption must be detected independently.
 require.NoError(t,os.WriteFile(install,[]byte("{\"leaves\":[]}"),0o660))
 _,err=LoadVerifiedILNReplica(install,canonical)
 require.ErrorIs(t,err,ErrILNReplicaUnverified)
}
