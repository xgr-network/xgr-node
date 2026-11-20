package jsonrpc

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strings"

 evm "github.com/xgr-network/xgr-node/interchain/evm"
)

// GetILNIndexedSourceReceipt returns a LOCAL single-provider FINALIZED source
// observation already captured during indexer ingestion. This never invokes
// an external RPC on read, is not a BLS witness, and cannot authorize funds.
func (x *xgrEndpoint) GetILNIndexedSourceReceipt(source,messageID string)(
 *evm.IndexedILNSourceReceipt,error){
 indexed,err:=x.readILNIndexedOperation(source,messageID)
 if err!=nil{return nil,err}
 if x==nil||x.dataDir==""{return nil,fmt.Errorf("ILN index unavailable")}
 normalized,err:=normalizeIndexedSource(source);if err!=nil{return nil,err}
 id,err:=normalizedILNMessageID(messageID);if err!=nil{return nil,err}
 path:=filepath.Join(x.dataDir,"interchain","iln","observed",normalized,"evidence",id+".json")
 info,err:=os.Lstat(path);if err!=nil{return nil,err}
 if !info.Mode().IsRegular()||info.Size()<1||info.Size()>4<<20{
  return nil,fmt.Errorf("invalid indexed receipt evidence file")
 }
 raw,err:=os.ReadFile(path);if err!=nil{return nil,err}
 var evidence evm.IndexedILNSourceReceipt
 if err=json.Unmarshal(raw,&evidence);err!=nil{return nil,err}
 if evidence.Version!=1||evidence.VerifiedAs!="FINALIZED_SINGLE_OPERATOR_OBSERVATION_NOT_BLS_EVIDENCE"||
  !strings.EqualFold(evidence.SourceTxHash,indexed.SourceTxHash)||
  evidence.SourceBlockNumber!=indexed.SourceBlockNumber||
  len(evidence.Transaction)==0||len(evidence.Receipt)==0||
  len(evidence.SourceBlock)==0||len(evidence.FinalizedHeadBlock)==0{
  return nil,fmt.Errorf("indexed source receipt is incomplete or mismatched")
 }
 return &evidence,nil
}
