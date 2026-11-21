package runtime

import (
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

const (
 ilnIndexedDeliveryTick = 60*time.Second
 ilnIndexedDeliveryRetry = 5*time.Minute
 ilnIndexedDeliveryLimit = 8
)
type indexedDeliveryJob struct{
 key string
 folder string
 filename string
 messageID string
 destination *evm.Destination
 router types.Address
 lastObserved time.Time
}
func (w *Worker) indexedDeliveryLoop(){
 defer w.wg.Done()
 retry:=make(map[string]time.Time)
 ticker:=time.NewTicker(ilnIndexedDeliveryTick)
 defer ticker.Stop()
 for{
  if err:=w.updateILNIndexedDeliveries(retry);err!=nil{
   w.logger.Warn("optional ILN delivery read index unavailable","err",err)
  }
  select{case <-w.closeCh:return;case <-ticker.C:}
 }
}

// Once an operation is finality-indexed, monitor destination Mailbox status
// independently of source message delivery. At most 8 due messages / tick,
// oldest first: an older still-open transfer cannot starve newer transfers.
func (w *Worker) updateILNIndexedDeliveries(retry map[string]time.Time)error{
 if w==nil||os.Getenv("XGR_ILN_READ_INDEX")!="1"{return nil}
 root:=filepath.Join(w.ilnDir(),"observed")
 sources,err:=os.ReadDir(root)
 if os.IsNotExist(err){return nil}
 if err!=nil{return err}
 now:=time.Now()
 jobs:=make([]indexedDeliveryJob,0)
 for _,src:=range sources{
  if !src.IsDir()||!ilnIndexSourceName.MatchString(src.Name()){continue}
  folder:=filepath.Join(root,src.Name())
  files,e:=os.ReadDir(folder)
  if e!=nil{return e}
  for _,entry:=range files{
   if entry.IsDir()||len(entry.Name())!=71||!strings.HasSuffix(entry.Name(),".json"){continue}
   id:=strings.TrimSuffix(entry.Name(),".json")
   if len(id)!=66||!strings.HasPrefix(id,"0x"){continue}
   key:=src.Name()+"/"+id
   if until,ok:=retry[key];ok&&now.Before(until){continue}
   raw,e:=os.ReadFile(filepath.Join(folder,entry.Name()))
   if e!=nil{return e}
   var op ilnIndexedOperation
   if json.Unmarshal(raw,&op)!=nil||op.Version!=1||
    op.Source!=src.Name()||!strings.EqualFold(op.MessageID,id)||
    op.EvidenceStatus!="UNTRUSTED_FINALITY_BOUNDED_HINT"{
    return fmt.Errorf("corrupt ILN source index delivery locator")
   }
   dest:=w.destinations[op.Destination]
   if dest==nil{continue}
   evidenceRaw,e:=os.ReadFile(filepath.Join(folder,"evidence",entry.Name()))
   if os.IsNotExist(e){continue}
   if e!=nil{return e}
   var evidence evm.IndexedILNSourceReceipt
   if json.Unmarshal(evidenceRaw,&evidence)!=nil||
    (evidence.VerifiedAs!="FINALIZED_SINGLE_OPERATOR_OBSERVATION_NOT_BLS_EVIDENCE"&&
     evidence.VerifiedAs!="FINALIZED_LOCAL_IBFT_OBSERVATION_NOT_BLS_EVIDENCE")||
    !strings.EqualFold(evidence.SourceTxHash,op.SourceTxHash)||
    types.IsValidAddress(evidence.DestinationRouter)!=nil{
    return fmt.Errorf("invalid archived source route binding")
   }
   router:=types.StringToAddress(evidence.DestinationRouter)
   if router==types.ZeroAddress{return fmt.Errorf("zero destination router in index")}
   statusPath:=filepath.Join(folder,"delivery",entry.Name())
   old,e:=os.ReadFile(statusPath)
   lastObserved:=time.Time{}
   if e==nil{
    var last evm.IndexedILNDelivery
    if json.Unmarshal(old,&last)!=nil||last.MessageID!=op.MessageID||
     last.DestinationChainID!=dest.ChainID||
     !strings.EqualFold(last.DestinationRouter,router.String()){
     return fmt.Errorf("conflicting persisted destination delivery state")
    }
    if last.Delivered{continue}
    lastObserved,e=time.Parse(time.RFC3339,last.ObservedAt)
    if e!=nil{return fmt.Errorf("invalid indexed delivery observation time")}
    if now.Sub(lastObserved)<ilnIndexedDeliveryTick{continue}
   }else if !os.IsNotExist(e){return e}
   jobs=append(jobs,indexedDeliveryJob{
    key:key,folder:folder,filename:entry.Name(),messageID:id,
    destination:dest,router:router,lastObserved:lastObserved,
   })
  }
 }
 sort.Slice(jobs,func(i,j int)bool{
  if !jobs[i].lastObserved.Equal(jobs[j].lastObserved){
   return jobs[i].lastObserved.Before(jobs[j].lastObserved)
  }
  return jobs[i].key<jobs[j].key
 })
 for i,job:=range jobs{
  if i>=ilnIndexedDeliveryLimit{break}
  message:=types.StringToHash(job.messageID)
  snapshot,e:=evm.GetIndexedILNDelivery(job.destination,job.router,message)
  if e!=nil{
   retry[job.key]=now.Add(ilnIndexedDeliveryRetry)
   continue
  }
  delete(retry,job.key)
  if snapshot.MessageID!=job.messageID||
   snapshot.DestinationChainID!=job.destination.ChainID||
   !strings.EqualFold(snapshot.DestinationRouter,job.router.String()){
   return fmt.Errorf("destination delivery index identity mismatch")
  }
  statusDir:=filepath.Join(job.folder,"delivery")
  statusPath:=filepath.Join(statusDir,job.filename)
  if e=os.MkdirAll(statusDir,0o770);e!=nil{return e}
  payload,e:=json.Marshal(snapshot);if e!=nil{return e}
  temp,e:=os.CreateTemp(statusDir,".delivery-*");if e!=nil{return e}
  if _,e=temp.Write(payload);e!=nil{temp.Close();os.Remove(temp.Name());return e}
  if e=temp.Sync();e!=nil{temp.Close();os.Remove(temp.Name());return e}
  if e=temp.Close();e!=nil{os.Remove(temp.Name());return e}
  if e=os.Rename(temp.Name(),statusPath);e!=nil{os.Remove(temp.Name());return e}
 }
 return nil
}
