package jsonrpc

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "time"
)

type ilnIndexerRouteStatusRPC struct{
 Route string `json:"route"`
 Source string `json:"source"`
 Destination string `json:"destination"`
 State string `json:"state"`
 SourceHead uint64 `json:"sourceHead"`
 CheckpointBlock uint64 `json:"checkpointBlock"`
 Leaves uint64 `json:"leaves"`
 LocalLeaves uint64 `json:"localLeaves"`
 LagLeaves uint64 `json:"lagLeaves"`
 PendingPeers int `json:"pendingPeers"`
 UpdatedUnix int64 `json:"updatedUnix"`
 Error string `json:"error,omitempty"`
}
type ilnIndexerStatusRPC struct {
 Version int `json:"version"`
 UpdatedUnix int64 `json:"updatedUnix"`
 WorkerAlive bool `json:"workerAlive"`
 State string `json:"state"`
 Routes []ilnIndexerRouteStatusRPC `json:"routes"`
}
func (x *xgrEndpoint) GetILNIndexerStatus()(*ilnIndexerStatusRPC,error){
 if x==nil||x.dataDir==""{return nil,fmt.Errorf("ILN monitoring data dir unavailable")}
 path:=filepath.Join(x.dataDir,"interchain","iln","indexer-health.json")
 info,err:=os.Stat(path)
 if err!=nil{return nil,fmt.Errorf("ILN monitoring not yet available: %w",err)}
 if !info.Mode().IsRegular()||info.Size()<=0||info.Size()>1<<20{return nil,fmt.Errorf("invalid ILN health snapshot")}
 raw,err:=os.ReadFile(path)
 if err!=nil{return nil,err}
 var status ilnIndexerStatusRPC
 if err:=json.Unmarshal(raw,&status);err!=nil{return nil,err}
 if status.Version!=1||status.UpdatedUnix<=0||status.Routes==nil{
  return nil,fmt.Errorf("invalid ILN health schema")
 }
 if time.Since(time.Unix(status.UpdatedUnix,0))>2*time.Minute ||
    time.Now().Before(time.Unix(status.UpdatedUnix,0).Add(-30*time.Second)){
   status.State="stalled";status.WorkerAlive=false
 }else if !status.WorkerAlive{status.State="stalled"
 }else{status.State="ok"}
 for _,route:=range status.Routes{
  if route.State=="unavailable"{status.State="degraded"}
  if route.State=="catching-up"&&status.State=="ok"{status.State="catching-up"}
 }
 return &status,nil
}
