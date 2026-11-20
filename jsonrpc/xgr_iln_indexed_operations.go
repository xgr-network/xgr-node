package jsonrpc

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "sort"
 "strconv"
 "strings"
)

// Read-only discovery data: NEVER a canonical receipt, settlement, delivery
// proof, validator witness or signing authorization.
type ilnIndexedOperationRPC struct {
 Version int `json:"version"`
 Cursor string `json:"cursor"`
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

func normalizeIndexedSource(source string)(string,error){
 name,err:=normalizeInterchainRPCChain(source)
 if err!=nil{return "",err}
 if len(name)<1||len(name)>40||strings.Contains(name,"_")||
  name[0]<'a'||name[0]>'z'{
  return "",fmt.Errorf("invalid indexed source")
 }
 for _,ch:=range name{
  if (ch<'a'||ch>'z')&&(ch<'0'||ch>'9')&&ch!='-'{
   return "",fmt.Errorf("invalid indexed source")
  }
 }
 return name,nil
}
func normalizeIndexerCursor(cursor string)(string,error){
 if cursor==""{return "",nil}
 if len(cursor)!=20{return "",fmt.Errorf("invalid ILN index cursor")}
 for _,ch:=range cursor{if ch<'0'||ch>'9'{return "",fmt.Errorf("invalid ILN index cursor")}}
 n,err:=strconv.ParseUint(cursor,10,64)
 if err!=nil||n==0||fmt.Sprintf("%020d",n)!=cursor{
  return "",fmt.Errorf("invalid ILN index cursor")
 }
 return cursor,nil
}
func indexOperationPath(dataDir,source,messageID string)string{
 return filepath.Join(dataDir,"interchain","iln","observed",source,messageID+".json")
}
func readIndexedOperationFile(path,source,id string)(*ilnIndexedOperationRPC,error){
 info,err:=os.Lstat(path)
 if err!=nil{return nil,err}
 if !info.Mode().IsRegular()||info.Size()<1||info.Size()>4096{
  return nil,fmt.Errorf("invalid ILN index record")
 }
 raw,err:=os.ReadFile(path);if err!=nil{return nil,err}
 var op ilnIndexedOperationRPC
 if err=json.Unmarshal(raw,&op);err!=nil{return nil,err}
 if op.Version!=1||op.Source!=source||op.MessageID!=id||
  op.Route==""||op.Destination==""||op.ValidatorFeeWei==""||
  op.SourceBlockNumber==0||op.DestinationDomain==0||
  op.EvidenceStatus!="UNTRUSTED_FINALITY_BOUNDED_HINT"{
  return nil,fmt.Errorf("invalid ILN index identity or status")
 }
 if _,err=normalizeIndexerCursor(op.Cursor);err!=nil{return nil,err}
 if op.Cursor==""{return nil,fmt.Errorf("missing index sequence")}
 for _,hash:=range []string{op.SourceTxHash,op.RouteID}{
  if _,err=normalizedILNMessageID(hash);err!=nil{return nil,err}
 }
 if len(op.Gateway)!=42||!strings.HasPrefix(op.Gateway,"0x"){
  return nil,fmt.Errorf("invalid indexed Gateway")
 }
 for _,ch:=range op.Gateway[2:]{if !strings.ContainsRune("0123456789abcdefABCDEF",ch){
  return nil,fmt.Errorf("invalid indexed Gateway")
 }}
 return &op,nil
}
func (x *xgrEndpoint) readILNIndexedOperation(source,id string)(*ilnIndexedOperationRPC,error){
 if x==nil||x.dataDir==""{return nil,fmt.Errorf("ILN index unavailable")}
 normalized,err:=normalizeIndexedSource(source);if err!=nil{return nil,err}
 id,err=normalizedILNMessageID(id);if err!=nil{return nil,err}
 path:=indexOperationPath(x.dataDir,normalized,id)
 return readIndexedOperationFile(path,normalized,id)
}

// GetILNIndexedOperation reads a single durable, untrusted source locator.
func (x *xgrEndpoint) GetILNIndexedOperation(source,messageID string)(*ilnIndexedOperationRPC,error){
 return x.readILNIndexedOperation(source,messageID)
}

// GetILNIndexedOperations returns records ordered by a per-source append
// sequence. Message-ID sorting CANNOT serve as an incremental feed because a
// later discovered route may backfill an earlier block or lower message hash.
func (x *xgrEndpoint) GetILNIndexedOperations(source,cursor string,limit uint64)(*ilnIndexedOperationsRPC,error){
 normalized,err:=normalizeIndexedSource(source);if err!=nil{return nil,err}
 cursor,err=normalizeIndexerCursor(cursor);if err!=nil{return nil,err}
 if limit<1||limit>100{return nil,fmt.Errorf("index page limit must be 1..100")}
 if x==nil||x.dataDir==""{return nil,fmt.Errorf("ILN index unavailable")}
 dir:=filepath.Join(x.dataDir,"interchain","iln","observed",normalized,"feed")
 entries,err:=os.ReadDir(dir)
 if os.IsNotExist(err){return &ilnIndexedOperationsRPC{Version:1,Source:normalized,Operations:[]ilnIndexedOperationRPC{}},nil}
 if err!=nil{return nil,err}
 names:=make([]string,0,len(entries))
 for _,entry:=range entries{
  name:=entry.Name()
  if entry.IsDir()||!strings.HasSuffix(name,".json")||len(name)!=25{continue}
  seq:=strings.TrimSuffix(name,".json")
  if _,err=normalizeIndexerCursor(seq);err!=nil{continue}
  if cursor!=""&&seq<=cursor{continue}
  names=append(names,seq)
 }
 sort.Strings(names)
 // An invalid cursor or missing feed entry is never an empty history page.
 // No automatic pruning is supported while clients depend on these cursors.
 expected:=uint64(1)
 if cursor!=""{
  n,e:=strconv.ParseUint(cursor,10,64)
  if e!=nil||n==^uint64(0){return nil,fmt.Errorf("invalid feed cursor")}
  cursorFile:=filepath.Join(dir,cursor+".json")
  stat,e:=os.Lstat(cursorFile)
  if e!=nil||!stat.Mode().IsRegular(){return nil,fmt.Errorf("unknown or pruned feed cursor")}
  expected=n+1
 }
 out:=&ilnIndexedOperationsRPC{Version:1,Source:normalized,Operations:[]ilnIndexedOperationRPC{}}
 for _,sequence:=range names{
  n,e:=strconv.ParseUint(sequence,10,64)
  if e!=nil||n!=expected{return nil,fmt.Errorf("ILN index feed sequence gap")}
  expected++
  path:=filepath.Join(dir,sequence+".json")
  info,err:=os.Lstat(path)
  if err!=nil||!info.Mode().IsRegular()||info.Size()<1||info.Size()>4096{
   return nil,fmt.Errorf("invalid ILN feed record")
  }
  raw,err:=os.ReadFile(path);if err!=nil{return nil,err}
  var item ilnIndexedOperationRPC
  if err=json.Unmarshal(raw,&item);err!=nil{return nil,err}
  id,err:=normalizedILNMessageID(item.MessageID);if err!=nil{return nil,err}
  verified,err:=x.readILNIndexedOperation(normalized,id)
  if err!=nil||verified.Cursor!=sequence||*verified!=item{
   return nil,fmt.Errorf("ILN index feed and immutable operation mismatch")
  }
  out.Operations=append(out.Operations,item)
  if len(out.Operations)==int(limit){
   if len(names)>len(out.Operations){out.NextCursor=sequence}
   break
  }
 }
 return out,nil
}
