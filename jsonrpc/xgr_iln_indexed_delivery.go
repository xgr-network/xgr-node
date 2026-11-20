package jsonrpc

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strings"
 "time"

 evm "github.com/xgr-network/xgr-node/interchain/evm"
)

// Strictly local, pre-indexed destination Mailbox status. Never queries a
// third-party RPC from the public JSON-RPC handler and never signs anything.
// Unknown and stale pending evidence is an error, NOT delivered=false.
func (x *xgrEndpoint) GetILNIndexedDelivery(source,messageID string)(*evm.IndexedILNDelivery,error){
 op,err:=x.readILNIndexedOperation(source,messageID);if err!=nil{return nil,err}
 name,err:=normalizeIndexedSource(source);if err!=nil{return nil,err}
 id,err:=normalizedILNMessageID(messageID);if err!=nil{return nil,err}
 path:=filepath.Join(x.dataDir,"interchain","iln","observed",name,"delivery",id+".json")
 info,err:=os.Lstat(path);if err!=nil{return nil,err}
 if !info.Mode().IsRegular()||info.Size()<1||info.Size()>4096{
  return nil,fmt.Errorf("invalid indexed delivery record")
 }
 raw,err:=os.ReadFile(path);if err!=nil{return nil,err}
 var result evm.IndexedILNDelivery
 if err=json.Unmarshal(raw,&result);err!=nil{return nil,err}
 if result.Version!=1||result.MessageID!=id||
  result.EvidenceStatus!="FINALIZED_READ_MODEL_NOT_BLS_EVIDENCE"||
  result.DestinationRouter==""||result.Mailbox==""||
  result.FinalizedBlockNumber<1||result.FinalizedBlockHash==""||
  op.Destination==""{
  return nil,fmt.Errorf("indexed destination delivery proof is malformed")
 }
 observed,err:=time.Parse(time.RFC3339,result.ObservedAt)
 if err!=nil||time.Until(observed)>30*time.Second{
  return nil,fmt.Errorf("invalid indexed destination delivery timestamp")
 }
 if !result.Delivered&&time.Since(observed)>2*time.Minute{
  return nil,fmt.Errorf("indexed destination delivery still unknown/stale")
 }
 if !strings.HasPrefix(result.FinalizedBlockHash,"0x")||len(result.FinalizedBlockHash)!=66{
  return nil,fmt.Errorf("invalid destination finality block hash")
 }
 return &result,nil
}
