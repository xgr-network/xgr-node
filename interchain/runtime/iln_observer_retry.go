package runtime

import (
 "bytes"
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "sort"
 "strings"
 "time"

 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

// Observer retries are durable LOCAL hints, not votes. Every replay takes the
// original signed-message code path, including independent source receipts,
// finality, current membership, fee allocation and destination delivery checks.
const (
 observerRetryMaxPerRoute=4096
 observerRetryMaxPerPoll=24
 observerRetryMaxPerRoutePoll=2
 observerRetryMinInterval=30*time.Second
 observerRetryMaxBytes=1024
)

type observerRetryRecord struct {
 Source string
 Route string
 RouteID string
 Gateway string
 DestinationDomain uint32
 MessageID string
 SourceBlock uint64
 SourceTxHash string
 LogIndex uint64
}

func observerSafeSegment(v string)bool{
 if len(v)==0||len(v)>160||v=="."||v==".."||strings.HasPrefix(v,"."){return false}
 for _,c:=range v{
  if !(c>='a'&&c<='z'||c>='A'&&c<='Z'||c>='0'&&c<='9'||c=='-'||c=='_'||c=='.'){return false}
 }
 return true
}

func (w *Worker) observerRetryDir(sourceName,routeName string)(string,error){
 if w==nil||w.dataDir==""||!observerSafeSegment(sourceName)||!observerSafeSegment(routeName){
  return "",fmt.Errorf("invalid observer retry identity")
 }
 return filepath.Join(w.ilnDir(),"observer-retries",sourceName,routeName),nil
}

func observerRetryIdentity(source *evm.Destination,route observerRoute,op evm.ILNOperation)(observerRetryRecord,error){
 if source==nil||route.Route==nil||source.Name==""||op.BlockNumber==0||
  route.Route.RouteID==types.ZeroHash||route.Gateway==types.ZeroAddress||
  route.DestinationDomain==0||route.DestinationDomain!=op.DestinationDomain||
  route.Route.RouteID!=op.RouteID||op.MessageID==types.ZeroHash||
  op.TransactionHash==types.ZeroHash{
  return observerRetryRecord{},fmt.Errorf("invalid observed retry identity")
 }
 return observerRetryRecord{
  Source:source.Name,Route:route.Route.Name,RouteID:route.Route.RouteID.String(),
  Gateway:route.Gateway.String(),DestinationDomain:route.DestinationDomain,
  MessageID:op.MessageID.String(),SourceBlock:op.BlockNumber,
  SourceTxHash:op.TransactionHash.String(),LogIndex:op.LogIndex,
 },nil
}

// Atomic, fsynced admission is the ONLY reason a shared cursor may pass a
// failed verification. Any disk failure or conflicting duplicate is fatal to
// that window. Never expire queued work merely because it is old.
func (w *Worker) queueObserverRetry(source *evm.Destination,route observerRoute,op evm.ILNOperation)error{
 entry,err:=observerRetryIdentity(source,route,op)
 if err!=nil{return err}
 dir,err:=w.observerRetryDir(entry.Source,entry.Route)
 if err!=nil{return err}
 if err=os.MkdirAll(dir,0o770);err!=nil{return err}
 raw,err:=json.Marshal(entry)
 if err!=nil||len(raw)>observerRetryMaxBytes{return fmt.Errorf("invalid observer retry journal record")}
 path:=filepath.Join(dir,strings.TrimPrefix(entry.MessageID,"0x")+".json")
 existing,err:=os.ReadFile(path)
 if err==nil{
  if !bytes.Equal(existing,raw){return fmt.Errorf("conflicting immutable observer retry identity")}
  return nil
 }
 if !os.IsNotExist(err){return err}
 files,err:=os.ReadDir(dir)
 if err!=nil{return err}
 if len(files)>=observerRetryMaxPerRoute{return fmt.Errorf("observer retry journal capacity reached; source cursor held")}
 f,err:=os.CreateTemp(dir,".observer-retry-*")
 if err!=nil{return err}
 defer os.Remove(f.Name())
 if err=f.Chmod(0o660);err!=nil{f.Close();return err}
 if _,err=f.Write(raw);err!=nil{f.Close();return err}
 if err=f.Sync();err!=nil{f.Close();return err}
 if err=f.Close();err!=nil{return err}
 if err=os.Rename(f.Name(),path);err!=nil{return err}
 d,err:=os.Open(dir)
 if err!=nil{return err}
 syncErr:=d.Sync()
 closeErr:=d.Close()
 if syncErr!=nil{return syncErr}
 return closeErr
}

func (w *Worker) observerRetryCount(sourceName,routeName string)(int,error){
 dir,err:=w.observerRetryDir(sourceName,routeName)
 if err!=nil{return 0,err}
 entries,err:=os.ReadDir(dir)
 if os.IsNotExist(err){return 0,nil}
 if err!=nil{return 0,err}
 count:=0
 for _,entry:=range entries{
  if !entry.IsDir()&&strings.HasSuffix(entry.Name(),".json"){count++}
 }
 return count,nil
}

func validObserverRetryRecord(entry observerRetryRecord,route observerRoute,source *evm.Destination,filename string)bool{
 if source==nil||route.Route==nil{return false}
 return entry.Source==source.Name&&entry.Route==route.Route.Name&&
  entry.RouteID==route.Route.RouteID.String()&&entry.Gateway==route.Gateway.String()&&
  entry.DestinationDomain==route.DestinationDomain&&entry.SourceBlock>0&&
  len(entry.MessageID)==66&&strings.HasPrefix(entry.MessageID,"0x")&&
  entry.MessageID!="0x"+strings.Repeat("0",64)&&
  entry.SourceTxHash!="0x"+strings.Repeat("0",64)&&
  len(entry.SourceTxHash)==66&&strings.HasPrefix(entry.SourceTxHash,"0x")&&
  filename==strings.TrimPrefix(entry.MessageID,"0x")+".json"&&
  observerHexLower(entry.MessageID[2:])&&observerHexLower(entry.SourceTxHash[2:])
}
func observerHexLower(s string)bool{
 if len(s)!=64{return false}
 for _,c:=range s{
  if !(c>='0'&&c<='9'||c>='a'&&c<='f'){return false}
 }
 return true
}

// Fair bounded replay: at most two messages per route and 24 per source poll.
// Failed records are rotated by mtime, never discarded, re-signed blindly,
// or retried at 2s RPC polling frequency.
func (w *Worker) retryObserverSource(source *evm.Destination,routes []observerRoute)error{
 if source==nil{return fmt.Errorf("missing observer retry source")}
 processed:=0
 for _,route:=range routes{
  if processed>=observerRetryMaxPerPoll{break}
  if route.Route==nil{return fmt.Errorf("missing observer retry route")}
  dir,err:=w.observerRetryDir(source.Name,route.Route.Name)
  if err!=nil{return err}
  files,err:=os.ReadDir(dir)
  if os.IsNotExist(err){continue}
  if err!=nil{return err}
  type candidate struct{name string;modified time.Time}
  pending:=make([]candidate,0,len(files))
  for _,item:=range files{
   if item.IsDir()||!strings.HasSuffix(item.Name(),".json"){continue}
   info,e:=item.Info()
   if e!=nil{return e}
   pending=append(pending,candidate{name:item.Name(),modified:info.ModTime()})
  }
  sort.Slice(pending,func(i,j int)bool{
   if pending[i].modified.Equal(pending[j].modified){return pending[i].name<pending[j].name}
   return pending[i].modified.Before(pending[j].modified)
  })
  for _,item:=range pending{
   if processed>=observerRetryMaxPerPoll{break}
   if time.Since(item.modified)<observerRetryMinInterval{continue}
   path:=filepath.Join(dir,item.name)
   raw,err:=os.ReadFile(path)
   if err!=nil{return err}
   if len(raw)==0||len(raw)>observerRetryMaxBytes{return fmt.Errorf("invalid observer retry journal record")}
   var entry observerRetryRecord
   if err=json.Unmarshal(raw,&entry);err!=nil{return fmt.Errorf("corrupt observer retry journal: %w",err)}
   if !validObserverRetryRecord(entry,route,source,item.name){
    return fmt.Errorf("observer retry journal route/message identity mismatch")
   }
   processed++
   err=observerSignMessage(w,route.Route,types.StringToHash(entry.MessageID),entry.SourceBlock)
   if err==nil||err.Error()=="Interchain message already delivered"{
    if err=os.Remove(path);err!=nil{return err}
    d,e:=os.Open(dir)
    if e!=nil{return e}
    syncErr:=d.Sync()
    closeErr:=d.Close()
    if syncErr!=nil{return syncErr}
    if closeErr!=nil{return closeErr}
    continue
   }
   now:=time.Now()
   if err=os.Chtimes(path,now,now);err!=nil{return err}
   // One failing proof is not an authorization to sign or to stop unrelated
   // source operations. The durable file remains until canonical success.
  }
 }
 return nil
}
