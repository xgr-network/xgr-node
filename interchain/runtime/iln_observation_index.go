package runtime

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "regexp"
 "sort"
 "strconv"
 "strings"

 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

var ilnIndexSourceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// Read-model facts, never a receipt witness, finality proof, settlement,
// consensus input or authorization for BLS signing. Cursor is a monotonic
// per-source append sequence: hash ordering would permanently lose late routes.
type ilnIndexedOperation struct {
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

// create-only atomic write: never silently replace an earlier source identity.
func writeILNIndexFile(path string,data []byte)error{
 dir:=filepath.Dir(path)
 tmp,err:=os.CreateTemp(dir,".index-*")
 if err!=nil{return err}
 defer os.Remove(tmp.Name())
 if _,err=tmp.Write(data);err!=nil{tmp.Close();return err}
 if err=tmp.Sync();err!=nil{tmp.Close();return err}
 if err=tmp.Close();err!=nil{return err}
 // Link is atomic and fails if the destination already exists. The caller
 // checks for conflict rather than allowing an overwrite from stale records.
 return os.Link(tmp.Name(),path)
}

func ilnNextIndexCursor(dir string)(string,error){
 entries,err:=os.ReadDir(dir)
 if err!=nil{return "",err}
 var top uint64
 for _,entry:=range entries{
  name:=entry.Name()
  if entry.IsDir()||!strings.HasSuffix(name,".json")||len(name)!=25{continue}
  n,err:=strconv.ParseUint(name[:20],10,64)
  if err!=nil||name!=fmt.Sprintf("%020d.json",n){continue}
  if n>top{top=n}
 }
 if top==^uint64(0){return "",fmt.Errorf("ILN observation sequence exhausted")}
 return fmt.Sprintf("%020d",top+1),nil
}

func (w *Worker) persistILNIndexedOperation(source *evm.Destination,
 route *evm.CheckpointRoute,gateway types.Address,op evm.ILNOperation)error{
 if w==nil||source==nil||route==nil||!ilnIndexSourceName.MatchString(source.Name)||
  op.MessageID==types.ZeroHash||op.TransactionHash==types.ZeroHash||
  op.RouteID!=route.RouteID||op.ValidatorFeeWei==nil||
  op.ValidatorFeeWei.Sign()<=0||op.BlockNumber==0||gateway==types.ZeroAddress{
  return fmt.Errorf("invalid ILN index observation")
 }
 dir:=filepath.Join(w.ilnDir(),"observed",source.Name)
 feedDir:=filepath.Join(dir,"feed")
 if err:=os.MkdirAll(feedDir,0o770);err!=nil{return err}
 identity:=strings.ToLower(op.MessageID.String())+".json"
 path:=filepath.Join(dir,identity)
 item:=ilnIndexedOperation{
  Version:1,Source:source.Name,Destination:route.Destination,
  Route:route.Name,Gateway:gateway.String(),RouteID:op.RouteID.String(),
  MessageID:op.MessageID.String(),SourceTxHash:op.TransactionHash.String(),
  SourceBlockNumber:op.BlockNumber,SourceLogIndex:op.LogIndex,
  DestinationDomain:op.DestinationDomain,
  ValidatorFeeWei:op.ValidatorFeeWei.String(),
  EvidenceStatus:"UNTRUSTED_FINALITY_BOUNDED_HINT",
 }
 var raw []byte
 old,err:=os.ReadFile(path)
 if err==nil{
  var existing ilnIndexedOperation
  if json.Unmarshal(old,&existing)!=nil||existing.Cursor==""{
   return fmt.Errorf("invalid persisted ILN observation")
  }
  item.Cursor=existing.Cursor
  raw,err=json.Marshal(item);if err!=nil{return err}
  if string(old)!=string(raw){
   return fmt.Errorf("conflicting persisted ILN observation")
  }
 }else if !os.IsNotExist(err){return err
 }else{
  item.Cursor,err=ilnNextIndexCursor(feedDir);if err!=nil{return err}
  raw,err=json.Marshal(item);if err!=nil{return err}
  if err=writeILNIndexFile(path,raw);err!=nil{return err}
 }
 feedPath:=filepath.Join(feedDir,item.Cursor+".json")
 // Replaying after a crash between the immutable lookup and append journal
 // completes the missing feed entry before any historical cursor advances.
 existing,err:=os.ReadFile(feedPath)
 if err==nil{
  if string(existing)!=string(raw){return fmt.Errorf("conflicting ILN observation feed sequence")}
  return nil
 }
 if !os.IsNotExist(err){return err}
 return writeILNIndexFile(feedPath,raw)
}

// A fully replayable query cursor is mandatory. Late-discovered historical
// routes are assigned a NEW append cursor, never a past block/hash offset.
func ilnObservedFeedCursors(entries []os.DirEntry)[]string{
 names:=make([]string,0,len(entries))
 for _,entry:=range entries{
  if entry.IsDir(){continue}
  name:=entry.Name()
  if len(name)!=25||!strings.HasSuffix(name,".json"){continue}
  n,err:=strconv.ParseUint(name[:20],10,64)
  if err==nil&&n>0&&name==fmt.Sprintf("%020d.json",n){
   names=append(names,name[:20])
  }
 }
 sort.Strings(names)
 return names
}
