package jsonrpc

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "sort"
 "strings"
)

// This RPC exposes only finality-bounded discovery hints from the local
// observer. It MUST NEVER claim receipt, BLS quorum, delivery, or TVL
// verification. Caller-supplied keys are strictly normalized.
type ilnIndexedOperationRPC struct {
 Version int `json:"version"`
 Source string `json:"source"`
 Destination string `json:"destination"`
 Route string `json:"route"`
 Gateway string `json:"gateway"`
 RouteID string `json:"routeId"`
 MessageID string `json:"messageId"`
 SourceTxHash string `json:"sourceTxHash"`
 SourceBlockNumber uint64 `json:"sourceBlockNumber"`
 SourceLogIndex uint64 `json:"sourceLogIndex"`
 DestinationDomain uint32 `json:"destinationDomain"`
 ValidatorFeeWei string `json:"validatorFeeWei"`
 EvidenceStatus string `json:"evidenceStatus"`
}

type ilnIndexedOperationsRPC struct {
 Version int `json:"version"`
 Source string `json:"source"`
 Operations []ilnIndexedOperationRPC `json:"operations"`
 NextCursor string `json:"nextCursor,omitempty"`
}

func (x *xgrEndpoint) readILNIndexedOperation(source,id string)(*ilnIndexedOperationRPC,error){
 if x==nil||x.dataDir==""{return nil,fmt.Errorf("ILN index unavailable")}
 normalized,err:=normalizeInterchainRPCChain(source)
 if err!=nil{return nil,err}
 if len(normalized)>40||strings.Contains(normalized,"_"){
  return nil,fmt.Errorf("invalid indexed source")
 }
 id,err=normalizedILNMessageID(id)
 if err!=nil{return nil,err}
 path:=filepath.Join(x.dataDir,"interchain","iln","observed",normalized,id+".json")
 info,err:=os.Lstat(path)
 if err!=nil{return nil,err}
 if !info.Mode().IsRegular()||info.Size()<1||info.Size()>4096 {
  return nil,fmt.Errorf("invalid ILN index record")
 }
 raw,err:=os.ReadFile(path);if err!=nil{return nil,err}
 var op ilnIndexedOperationRPC
 if err=json.Unmarshal(raw,&op);err!=nil{return nil,err}
 if op.Version!=1||op.Source!=normalized||
  !strings.EqualFold(op.MessageID,id)||op.SourceTxHash==""||
  op.Route==""||op.Gateway==""||op.RouteID==""||
  op.ValidatorFeeWei==""||op.SourceBlockNumber==0||
  op.EvidenceStatus!="UNTRUSTED_FINALITY_BOUNDED_HINT"{
  return nil,fmt.Errorf("invalid ILN index record identity")
 }
 return &op,nil
}

// GetILNIndexedOperation reads a single bounded source event projection.
// The response is an UNTRUSTED locator, NEVER source proof or authorization.
func (x *xgrEndpoint) GetILNIndexedOperation(source,messageID string)(*ilnIndexedOperationRPC,error){
 return x.readILNIndexedOperation(source,messageID)
}

// GetILNIndexedOperations is a bounded cursor-paginated read from local
// finality-observer disk. A crashed/lagging node can return incomplete data;
// clients must check GetILNIndexerStatus independently and fail closed.
func (x *xgrEndpoint) GetILNIndexedOperations(source,cursor string,limit uint64)(*ilnIndexedOperationsRPC,error){
 normalized,err:=normalizeInterchainRPCChain(source)
 if err!=nil{return nil,err}
 if len(normalized)>40||strings.Contains(normalized,"_"){
  return nil,fmt.Errorf("invalid indexed source")
 }
 if limit<1||limit>100{return nil,fmt.Errorf("index page limit must be 1..100")}
 if cursor!=""{
  cursor,err=normalizedILNMessageID(cursor)
  if err!=nil{return nil,err}
 }
 if x==nil||x.dataDir==""{return nil,fmt.Errorf("ILN index unavailable")}
 dir:=filepath.Join(x.dataDir,"interchain","iln","observed",normalized)
 entries,err:=os.ReadDir(dir)
 if os.IsNotExist(err){return &ilnIndexedOperationsRPC{Version:1,Source:normalized,Operations:[]ilnIndexedOperationRPC{}},nil}
 if err!=nil{return nil,err}
 names:=make([]string,0,len(entries))
 for _,entry:=range entries{
  name:=entry.Name()
  if entry.IsDir()||!strings.HasSuffix(name,".json")||len(name)!=71{continue}
  id:=strings.TrimSuffix(name,".json")
  if _,err=normalizedILNMessageID(id);err!=nil{continue}
  if cursor!=""&&id<=cursor{continue}
  names=append(names,id)
 }
 sort.Strings(names)
 out:=&ilnIndexedOperationsRPC{Version:1,Source:normalized,Operations:[]ilnIndexedOperationRPC{}}
 for _,id:=range names{
  item,err:=x.readILNIndexedOperation(normalized,id)
  if err!=nil{return nil,err}
  out.Operations=append(out.Operations,*item)
  if len(out.Operations)==int(limit){
   if len(names)>len(out.Operations){out.NextCursor=id}
   break
  }
 }
 return out,nil
}
