package runtime

import (
 "testing"

 "github.com/stretchr/testify/require"
 "github.com/xgr-network/xgr-node/crypto"
 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

func TestV322FinalityDoesNotTrustPeerOrTip(t *testing.T){
 key:=crypto.Keccak256Hash([]byte("canonical-source-leaf"))
 hook:=types.StringToAddress("0x5555555555555555555555555555555555555555")
 c:=evm.ILNReplicaCheckpoint{ChainID:42161,Hook:hook,Root:ilnTestCanonicalRoot(key),Count:1,BlockNumber:120}
 replica:=evm.ILNReplica{ChainID:c.ChainID,Hook:hook,Root:c.Root,Leaves:[]types.Hash{key},CheckpointBlock:120}
 source:=&evm.Destination{ChainID:42161}
 old:=getConfirmedILNHead
 head:=uint64(110)
 getConfirmedILNHead=func(*evm.Destination)(uint64,error){return head,nil}
 t.Cleanup(func(){getConfirmedILNHead=old})
 w:=&Worker{dataDir:t.TempDir()}
 n,err:=w.observedILNFinalizedCount(source,c,replica)
 require.NoError(t,err)
 require.Zero(t,n,"tip-only observations cannot authorize an ILN BLS vote")
 c.BlockNumber=131
 replica.CheckpointBlock=131
 head=121
 n,err=w.observedILNFinalizedCount(source,c,replica)
 require.NoError(t,err)
 require.EqualValues(t,1,n,"same reconstructed root matured through source confirmations")
 corrupt:=replica
 corrupt.Leaves=[]types.Hash{crypto.Keccak256Hash([]byte("poisoned"))}
 _,err=w.observedILNFinalizedCount(source,c,corrupt)
 require.Error(t,err,"peer-supplied malicious leaf never matures")
 other:=c
 other.ChainID=8453
 _,err=w.observedILNFinalizedCount(source,other,replica)
 require.Error(t,err,"cross-chain snapshot cannot authorize another chain")
}
