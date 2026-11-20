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
 ilnGasJobsPerTick=4
 ilnGasRetryDelay=5*time.Minute
 ilnGasScanInterval=90*time.Second
)
type indexedGasCursor struct {
 NextTo uint64 `json:"nextTo"`
 LastAttemptUnix int64 `json:"lastAttemptUnix"`
 NextAttemptUnix int64 `json:"nextAttemptUnix"`
 Completed bool `json:"completed"`
}
type indexedGasJob struct {
 Source string
 MessageID string
 Folder string
 Destination string
 Mailbox types.Address
 FirstFinalized uint64
 Cursor indexedGasCursor
}
func saveIndexedGasJSON(path string,value interface{})error{
 data,err:=json.Marshal(value);if err!=nil{return err}
 dir:=filepath.Dir(path)
 if err=os.MkdirAll(dir,0o770);err!=nil{return err}
 temp,err:=os.CreateTemp(dir,".index-gas-*");if err!=nil{return err}
 defer os.Remove(temp.Name())
 if _,err=temp.Write(data);err!=nil{temp.Close();return err}
 if err=temp.Sync();err!=nil{temp.Close();return err}
 if err=temp.Close();err!=nil{return err}
 return os.Rename(temp.Name(),path)
}
func (w *Worker) indexedGasLoop(){
 defer w.wg.Done()
 ticker:=time.NewTicker(time.Minute)
 defer ticker.Stop()
 for{
  if err:=w.updateILNIndexedGas();err!=nil{
   w.logger.Warn("optional ILN gas index unavailable","err",err)
  }
  select{
  case <-w.closeCh:return
  case <-ticker.C:
  }
 }
}
// Scan exact, already-delivered message IDs. Never issue idle Mailbox-wide
// eth_getLogs or retroactively modify transfer/delivery evidence. The
// backwards scan cursor survives crashes; all failed reads retry boundedly.
func (w *Worker) updateILNIndexedGas()error{
 if w==nil||os.Getenv("XGR_ILN_READ_INDEX")!="1"{return nil}
 root:=filepath.Join(w.ilnDir(),"observed")
 sources,err:=os.ReadDir(root)
 if os.IsNotExist(err){return nil}
 if err!=nil{return err}
 now:=time.Now()
 jobs:=make([]indexedGasJob,0)
 for _,source:=range sources{
  if !source.IsDir()||!ilnIndexSourceName.MatchString(source.Name()){continue}
  folder:=filepath.Join(root,source.Name())
  entries,e:=os.ReadDir(folder);if e!=nil{return e}
  for _,entry:=range entries{
   if entry.IsDir()||len(entry.Name())!=71||
    !strings.HasPrefix(entry.Name(),"0x")||!strings.HasSuffix(entry.Name(),".json"){continue}
   id:=strings.TrimSuffix(entry.Name(),".json")
   if len(id)!=66||!strings.HasPrefix(id,"0x"){continue}
   valid:=true
   for _,ch:=range id[2:]{if !strings.ContainsRune("0123456789abcdef",ch){valid=false;break}}
   if !valid{continue}
   deliveryPath:=filepath.Join(folder,"delivery",entry.Name())
   evidence,e:=os.ReadFile(deliveryPath)
   if os.IsNotExist(e){continue}
   if e!=nil{return e}
   var d evm.IndexedILNDelivery
   if json.Unmarshal(evidence,&d)!=nil||d.Version!=1||
    d.MessageID!=id||d.EvidenceStatus!="FINALIZED_READ_MODEL_NOT_BLS_EVIDENCE"{
    return fmt.Errorf("invalid persisted indexed delivery for gas")
   }
   if !d.Delivered{continue}
   gasFile:=filepath.Join(folder,"gas",entry.Name())
   if stat,e:=os.Stat(gasFile);e==nil{
    if !stat.Mode().IsRegular(){return fmt.Errorf("invalid persisted gas file")}
    continue
   }else if !os.IsNotExist(e){return e}
   if types.IsValidAddress(d.Mailbox)!=nil{return fmt.Errorf("invalid indexed delivery mailbox")}
   srcRaw,e:=os.ReadFile(filepath.Join(folder,entry.Name()))
   if e!=nil{return e}
   var op ilnIndexedOperation
   if json.Unmarshal(srcRaw,&op)!=nil||op.MessageID!=id||op.Source!=source.Name(){
    return fmt.Errorf("invalid indexed source gas locator")
   }
   if w.destinations[op.Destination]==nil{continue}
   cursorPath:=filepath.Join(folder,"gas-cursor",entry.Name())
   c:=indexedGasCursor{NextTo:d.FinalizedBlockNumber}
   existing,e:=os.ReadFile(cursorPath)
   if e==nil{
    if json.Unmarshal(existing,&c)!=nil{return fmt.Errorf("invalid gas cursor")}
   }else if !os.IsNotExist(e){return e}
   if c.Completed||c.NextTo==0||c.NextTo>d.FinalizedBlockNumber{continue}
   if c.NextAttemptUnix>now.Unix(){continue}
   jobs=append(jobs,indexedGasJob{Source:source.Name(),MessageID:id,
    Folder:folder,Destination:op.Destination,Mailbox:types.StringToAddress(d.Mailbox),
    FirstFinalized:d.FinalizedBlockNumber,Cursor:c})
  }
 }
 // Oldest due attempt first avoids starving new messages behind a
 // long-lived historical search, even after restarting the Node process.
 sort.Slice(jobs,func(i,j int)bool{
  if jobs[i].Cursor.NextAttemptUnix!=jobs[j].Cursor.NextAttemptUnix{
   return jobs[i].Cursor.NextAttemptUnix<jobs[j].Cursor.NextAttemptUnix
  }
  if jobs[i].Source!=jobs[j].Source{return jobs[i].Source<jobs[j].Source}
  return jobs[i].MessageID<jobs[j].MessageID
 })
 for i,job:=range jobs{
  if i>=ilnGasJobsPerTick{break}
  dest:=w.destinations[job.Destination]
  min:=dest.ILNRegistryActivationBlock
  if min<1{return fmt.Errorf("gas read-model destination activation unavailable")}
  cursor:=job.Cursor
  cursor.LastAttemptUnix=now.Unix()
  cursor.NextAttemptUnix=now.Add(ilnGasScanInterval).Unix()
  cursorPath:=filepath.Join(job.Folder,"gas-cursor",job.MessageID+".json")
  if cursor.NextTo<min{
   cursor.Completed=true
   if err=saveIndexedGasJSON(cursorPath,cursor);err!=nil{return err}
   continue
  }
  from:=min
  if cursor.NextTo-min+1>=150{
   from=cursor.NextTo-150+1
  }
  found,e:=evm.ScanIndexedILNGasWindow(dest,job.Mailbox,
   types.StringToHash(job.MessageID),from,cursor.NextTo)
  if e!=nil{
   // Keep the exact window unchanged and suppress immediate RPC retries.
   cursor.NextAttemptUnix=now.Add(ilnGasRetryDelay).Unix()
   if err=saveIndexedGasJSON(cursorPath,cursor);err!=nil{return err}
   continue
  }
  if found!=nil{
   if found.MessageID!=job.MessageID||
    found.DestinationChainID!=dest.ChainID||
    !strings.EqualFold(found.Mailbox,job.Mailbox.String()){
    return fmt.Errorf("mismatched indexed gas identity")
   }
   if err=saveIndexedGasJSON(filepath.Join(job.Folder,"gas",job.MessageID+".json"),found);err!=nil{
    return err
   }
   continue
  }
  cursor.NextTo=from-1
  if from==min{cursor.Completed=true}
  if err=saveIndexedGasJSON(cursorPath,cursor);err!=nil{return err}
 }
 return nil
}
