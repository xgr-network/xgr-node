package runtime

import (
 "encoding/json"
 "math/big"
 "os"
 "path/filepath"
 "testing"

 "github.com/stretchr/testify/require"
 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

func TestILNObservationIndexPersistsAndRejectsConflictingHistory(t *testing.T){
 dir:=t.TempDir()
 w:=&Worker{dataDir:dir}
 source:=&evm.Destination{Name:"base",ChainID:8453}
 route:=&evm.CheckpointRoute{Name:"base_to_xgr",SourceNetwork:"base",
  Destination:"xgr",RouteID:types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}
 gateway:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 op:=evm.ILNOperation{
  MessageID:types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
  TransactionHash:types.StringToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
  RouteID:route.RouteID,DestinationDomain:1643,ValidatorFeeWei:big.NewInt(100),
  BlockNumber:125,LogIndex:2,
 }
 require.NoError(t,w.persistILNIndexedOperation(source,route,gateway,op))
 require.NoError(t,w.persistILNIndexedOperation(source,route,gateway,op))
 path:=filepath.Join(dir,"interchain","iln","observed","base",op.MessageID.String()+".json")
 raw,err:=os.ReadFile(path);require.NoError(t,err)
 var saved ilnIndexedOperation
 require.NoError(t,json.Unmarshal(raw,&saved))
 require.Equal(t,"UNTRUSTED_FINALITY_BOUNDED_HINT",saved.EvidenceStatus)
 require.Equal(t,op.TransactionHash.String(),saved.SourceTxHash)
 require.Equal(t,uint64(125),saved.SourceBlockNumber)
 op.TransactionHash=types.StringToHash("0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
 require.ErrorContains(t,w.persistILNIndexedOperation(source,route,gateway,op),"conflicting")
}
