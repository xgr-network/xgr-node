package evm

import (
 "fmt"
 "testing"
 "github.com/stretchr/testify/require"
)

func TestILNValidatorReplicaProofIndependentOfRelayerE2E(t *testing.T){
 for _,size:=range []int{1,2,3,31,32,33,1024,4096,10000}{
  t.Run(fmt.Sprintf("messages-%d",size),func(t *testing.T){
   checkpoint,chunks:=makeILNReplicaFixture(t,size)
   snapshot,err:=AssembleILNReplica(checkpoint,chunks)
   require.NoError(t,err)
   for _,target:=range []int{0,size/2,size-1}{
    proof,err:=BuildILNReplicaProof(snapshot,checkpoint,uint32(target))
    require.NoError(t,err)
    current:=snapshot.Leaves[target]
    for level,sibling:=range proof {
     if (uint64(target)>>level)&1==1{current=ilnMerkleParent(sibling,current)}else{current=ilnMerkleParent(current,sibling)}
    }
    require.Equal(t,checkpoint.Root,current)
   }
   _,err=BuildILNReplicaProof(snapshot,checkpoint,uint32(size))
   require.Error(t,err)
   wrong:=checkpoint
   wrong.Count++
   _,err=BuildILNReplicaProof(snapshot,wrong,0)
   require.ErrorIs(t,err,ErrILNReplicaUnverified)
  })
 }
}
