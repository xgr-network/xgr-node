package runtime

import (
 "encoding/json"
 "math/big"
 "os"
 "path/filepath"
 "strings"
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
 require.Equal(t,"00000000000000000001",saved.Cursor)
 journal:=filepath.Join(dir,"interchain","iln","observed","base","feed",saved.Cursor+".json")
 feedRaw,err:=os.ReadFile(journal);require.NoError(t,err)
 require.Equal(t,string(raw),string(feedRaw))
 // A crash between identity and feed creation can replay without loss.
 require.NoError(t,os.Remove(journal))
 require.NoError(t,w.persistILNIndexedOperation(source,route,gateway,op))
 restored,err:=os.ReadFile(journal);require.NoError(t,err)
 require.Equal(t,string(raw),string(restored))
 op.TransactionHash=types.StringToHash("0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
 require.ErrorContains(t,w.persistILNIndexedOperation(source,route,gateway,op),"conflicting")
}

func TestILNIndexFeedDoesNotOrderByMessageHash(t *testing.T){
 w:=&Worker{dataDir:t.TempDir()}
 src:=&evm.Destination{Name:"bsc",ChainID:56}
 route:=&evm.CheckpointRoute{Name:"bsc_to_xgr",SourceNetwork:"bsc",
  Destination:"xgr",RouteID:types.StringToHash("0x"+strings.Repeat("a",64))}
 addr:=types.StringToAddress("0x"+strings.Repeat("1",40))
 mk:=func(msg,tx string,block uint64) evm.ILNOperation{
  return evm.ILNOperation{RouteID:route.RouteID,MessageID:types.StringToHash(msg),
   TransactionHash:types.StringToHash(tx),BlockNumber:block,DestinationDomain:1643,
   ValidatorFeeWei:big.NewInt(1)}
 }
 newest:=mk("0x"+strings.Repeat("f",64),"0x"+strings.Repeat("c",64),900)
 older:=mk("0x"+strings.Repeat("b",64),"0x"+strings.Repeat("d",64),100)
 require.NoError(t,w.persistILNIndexedOperation(src,route,addr,newest))
 require.NoError(t,w.persistILNIndexedOperation(src,route,addr,older))
 root:=filepath.Join(w.ilnDir(),"observed","bsc","feed")
 first,err:=os.ReadFile(filepath.Join(root,"00000000000000000001.json"))
 require.NoError(t,err)
 second,err:=os.ReadFile(filepath.Join(root,"00000000000000000002.json"))
 require.NoError(t,err)
 require.Contains(t,string(first),newest.MessageID.String())
 require.Contains(t,string(second),older.MessageID.String())
 // A late route can backfill an older block without ever being hidden by
 // the cursor of a previously queried newer message hash.
}
