package runtime

import (
 "testing"
 "github.com/stretchr/testify/require"
 "github.com/xgr-network/xgr-node/crypto"
 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

func TestILNSourcePrefixRequiresIndependentConfirmation(t *testing.T) {
 leaf:=crypto.Keccak256Hash([]byte("source message"))
 root:=ilnTestCanonicalRoot(leaf)
 hook:=types.StringToAddress("0x4444444444444444444444444444444444444444")
 source:=&evm.Destination{ChainID:8453}
 c:=evm.ILNReplicaCheckpoint{ChainID:8453,Hook:hook,Root:root,Count:1,BlockNumber:100}
 r:=evm.ILNReplica{ChainID:8453,Hook:hook,Root:root,CheckpointBlock:100,Leaves:[]types.Hash{leaf}}
 head:=uint64(99)
 old:=getConfirmedILNHead
 getConfirmedILNHead=func(*evm.Destination)(uint64,error){return head,nil}
 t.Cleanup(func(){getConfirmedILNHead=old})
 w:=&Worker{dataDir:t.TempDir()}
 ready,err:=w.confirmedILNPrefixCount(source,c,r)
 require.NoError(t,err)
 require.Zero(t,ready,"unfinalized live Hook state must never authorize BLS")
 c.BlockNumber=132;r.CheckpointBlock=132;head=120
 ready,err=w.confirmedILNPrefixCount(source,c,r)
 require.NoError(t,err)
 require.EqualValues(t,1,ready,"the independently observed prefix matured")
 bad:=r;bad.Leaves=[]types.Hash{crypto.Keccak256Hash([]byte("poisoned peer"))}
 _,err=w.confirmedILNPrefixCount(source,c,bad)
 require.Error(t,err,"P2P corruption cannot authorize old or new checkpoints")
 c.ChainID=42161
 _,err=w.confirmedILNPrefixCount(source,c,r)
 require.Error(t,err,"cross-chain proof must fail")
}
