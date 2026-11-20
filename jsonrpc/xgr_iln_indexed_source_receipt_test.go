package jsonrpc

import (
 "encoding/json"
 "os"
 "path/filepath"
 "strings"
 "testing"

 "github.com/stretchr/testify/require"
 evm "github.com/xgr-network/xgr-node/interchain/evm"
)

func TestNodeIndexedSourceReceiptIsLocalReadOnlyAndPinned(t *testing.T){
 dir:=t.TempDir()
 source:="bsc"
 id:="0x"+strings.Repeat("a",64)
 tx:="0x"+strings.Repeat("b",64)
 dirPath:=filepath.Join(dir,"interchain","iln","observed",source)
 evidenceDir:=filepath.Join(dirPath,"evidence")
 require.NoError(t,os.MkdirAll(evidenceDir,0o700))
 op:=ilnIndexedOperationRPC{Version:1,Cursor:"00000000000000000001",
  Source:source,Destination:"xgr",Route:"bsc_to_xgr",
  RouteID:"0x"+strings.Repeat("c",64),
  Gateway:"0x"+strings.Repeat("1",40),
  MessageID:id,SourceTxHash:tx,SourceBlockNumber:100,
  DestinationDomain:1643,ValidatorFeeWei:"1",
  EvidenceStatus:"UNTRUSTED_FINALITY_BOUNDED_HINT"}
 raw,err:=json.Marshal(op);require.NoError(t,err)
 require.NoError(t,os.WriteFile(filepath.Join(dirPath,id+".json"),raw,0o600))
 obj:=evm.IndexedILNSourceReceipt{Version:1,SourceChainID:56,
  SourceTxHash:tx,SourceBlockNumber:100,
  SourceBlockHash:"0x"+strings.Repeat("d",64),
  SourceRouter:"0x"+strings.Repeat("4",40),
  DestinationRouter:"0x"+strings.Repeat("5",40),
  SourceMailbox:"0x"+strings.Repeat("6",40),
  Transaction:json.RawMessage(`{"hash":"test"}`),
  Receipt:json.RawMessage(`{"status":"0x1"}`),
  SourceBlock:json.RawMessage(`{"number":"0x64"}`),
  FinalizedHeadBlock:json.RawMessage(`{"number":"0x70"}`),
  VerifiedAs:"FINALIZED_SINGLE_OPERATOR_OBSERVATION_NOT_BLS_EVIDENCE"}
 receiptRaw,err:=json.Marshal(obj);require.NoError(t,err)
 path:=filepath.Join(evidenceDir,id+".json")
 require.NoError(t,os.WriteFile(path,receiptRaw,0o600))
 ep:=newXGREndpoint(nil,dir)
 got,err:=ep.GetILNIndexedSourceReceipt(source,id)
 require.NoError(t,err)
 require.Equal(t,tx,got.SourceTxHash)
 require.Equal(t,"FINALIZED_SINGLE_OPERATOR_OBSERVATION_NOT_BLS_EVIDENCE",got.VerifiedAs)
 obj.SourceTxHash="0x"+strings.Repeat("f",64)
 receiptRaw,err=json.Marshal(obj);require.NoError(t,err)
 require.NoError(t,os.WriteFile(path,receiptRaw,0o600))
 _,err=ep.GetILNIndexedSourceReceipt(source,id)
 require.ErrorContains(t,err,"mismatched")
}
