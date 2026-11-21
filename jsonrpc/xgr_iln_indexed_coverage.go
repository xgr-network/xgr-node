package jsonrpc

import (
 "fmt"
 "os"
 "path/filepath"
 "strconv"
 "strings"
 "time"
)

// The status is a read-model COVERAGE indicator, not independent receipt
// finality or a validator witness. Failure is always incomplete, never zero.
type ilnIndexedCoverageRPC struct {
 Version int `json:"version"`
 Source string `json:"source"`
 IndexedTo uint64 `json:"indexedTo"`
 IndexedKnown bool `json:"indexedKnown"`
 SourceHead uint64 `json:"sourceHead"`
 SourceHeadKnown bool `json:"sourceHeadKnown"`
 Complete bool `json:"complete"`
 UpdatedUnix int64 `json:"updatedUnix"`
 State string `json:"state"`
}

// GetILNIndexedCoverage reports the dedicated indexer's durable historical
// position. XITA must require complete+fresh coverage before retiring a
// legacy source scan; the ordinary validator cursor is deliberately ignored.
func (x *xgrEndpoint) GetILNIndexedCoverage(source string)(*ilnIndexedCoverageRPC,error){
 name,err:=normalizeIndexedSource(source);if err!=nil{return nil,err}
 if x==nil||x.dataDir==""{return nil,fmt.Errorf("ILN read index unavailable")}
 if os.Getenv("XGR_ILN_READ_INDEX")!="1"{
  return nil,fmt.Errorf("dedicated ILN index not enabled")
 }
 status,err:=x.GetILNIndexerStatus();if err!=nil{return nil,err}
 result:=&ilnIndexedCoverageRPC{Version:1,Source:name,State:"unavailable",UpdatedUnix:status.UpdatedUnix}
 if !status.WorkerAlive||status.State=="stalled"{
  return result,nil
 }
 path:=filepath.Join(x.dataDir,"interchain","iln","cursors","_index_"+name+".cursor")
 info,err:=os.Lstat(path)
 if err==nil{
  if !info.Mode().IsRegular()||info.Size()<1||info.Size()>30{
   return nil,fmt.Errorf("invalid index history cursor")
  }
  raw,e:=os.ReadFile(path)
  if e!=nil{return nil,e}
  n,e:=strconv.ParseUint(strings.TrimSpace(string(raw)),10,64)
  if e!=nil{return nil,fmt.Errorf("invalid index history cursor")}
  result.IndexedKnown=true
  result.IndexedTo=n
 }else if !os.IsNotExist(err){return nil,err}
 relevant:=0
 fresh:=true
 head:=uint64(0)
 for _,route:=range status.Routes{
  if route.Source!=name{continue}
  relevant++
  if !route.SourceHeadKnown||route.State=="unavailable"||
   route.LastSuccessfulScanUnix<=0||
   time.Since(time.Unix(route.LastSuccessfulScanUnix,0))>2*time.Minute||
   time.Until(time.Unix(route.LastSuccessfulScanUnix,0))>30*time.Second{
   fresh=false
  }
  if route.SourceHead>head{head=route.SourceHead}
 }
 if relevant==0||!fresh{return result,nil}
 result.SourceHeadKnown=true
 result.SourceHead=head
 if !result.IndexedKnown{result.State="backfilling";return result,nil}
 if result.IndexedTo>head{result.State="invalid";return result,nil}
 if result.IndexedTo<head{result.State="backfilling";return result,nil}
 result.Complete=true
 result.State="complete"
 return result,nil
}
