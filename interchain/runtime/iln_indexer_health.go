package runtime

import (
 "encoding/json"
 "os"
 "path/filepath"
 "sort"
 "time"

 "github.com/armon/go-metrics"
 evm "github.com/xgr-network/xgr-node/interchain/evm"
)

type ilnRouteHealth struct {
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
type ilnIndexerHealth struct {
 Version int `json:"version"`
 UpdatedUnix int64 `json:"updatedUnix"`
 WorkerAlive bool `json:"workerAlive"`
 Routes []ilnRouteHealth `json:"routes"`
}
// recordILNReplicaVerified only accepts a successfully independently
// verified source checkpoint and complete local Merkle prefix.
func (w *Worker) recordILNReplicaVerified(route *evm.CheckpointRoute,c evm.ILNReplicaCheckpoint,head uint64){
 if route==nil{return}
 w.mu.Lock();defer w.mu.Unlock()
 if w.ilnRouteHealthByName==nil{w.ilnRouteHealthByName=make(map[string]ilnRouteHealth)}
 w.ilnRouteHealthByName[route.Name]=ilnRouteHealth{
  Route:route.Name,Source:route.SourceNetwork,Destination:route.Destination,
  State:"ok",SourceHead:head,CheckpointBlock:c.BlockNumber,Leaves:c.Count,
  LocalLeaves:c.Count,LagLeaves:0,UpdatedUnix:time.Now().Unix(),
 }
}
// A temporary RPC error must NEVER continue to report the route as ready.
// Keep previously verified counts only as diagnostic, not as authorization.
func (w *Worker) recordILNReplicaFailure(route *evm.CheckpointRoute,err error){
 if route==nil{return}
 w.mu.Lock();defer w.mu.Unlock()
 if w.ilnRouteHealthByName==nil{w.ilnRouteHealthByName=make(map[string]ilnRouteHealth)}
 rh:=w.ilnRouteHealthByName[route.Name]
 rh.Route=route.Name;rh.Source=route.SourceNetwork;rh.Destination=route.Destination
 rh.State="catching-up";rh.UpdatedUnix=time.Now().Unix()
 rh.Error=err.Error()
 rh.LocalLeaves=0
 if rh.Leaves>0{rh.LagLeaves=rh.Leaves}
 w.ilnRouteHealthByName[route.Name]=rh
}
// Health must be observational, not a second independent source chain scan.
// Each 10s heartbeat exports the outcome of the last 30s checkpoint poll.
func (w *Worker) writeILNIndexerHealth() {
 now:=time.Now().Unix()
 health:=ilnIndexerHealth{Version:1,UpdatedUnix:now,WorkerAlive:true,Routes:[]ilnRouteHealth{}}
 w.mu.Lock()
 routes:=make([]*evm.CheckpointRoute,0,len(w.routes))
 for _,r:=range w.routes{routes=append(routes,r)}
 observed:=make(map[string]ilnRouteHealth,len(w.ilnRouteHealthByName))
 for k,v:=range w.ilnRouteHealthByName{observed[k]=v}
 w.mu.Unlock()
 sort.Slice(routes,func(i,j int)bool{return routes[i].Name<routes[j].Name})
 for _,route:=range routes{
  if route==nil{continue}
  rh,ok:=observed[route.Name]
  if !ok {
   rh=ilnRouteHealth{Route:route.Name,Source:route.SourceNetwork,
    Destination:route.Destination,State:"catching-up",Error:"awaiting first independently verified checkpoint",UpdatedUnix:now}
  }
  if now-rh.UpdatedUnix>90 || rh.UpdatedUnix>now+30{
   rh.State="unavailable";rh.Error="source checkpoint observation stale";rh.LocalLeaves=0
   if rh.Leaves>0{rh.LagLeaves=rh.Leaves}
  }
  health.Routes=append(health.Routes,rh)
  labels:=[]metrics.Label{{Name:"route",Value:rh.Route},{Name:"source",Value:rh.Source}}
  metrics.SetGaugeWithLabels([]string{"interchain","iln","replica","ready"},boolMetric(rh.State=="ok"),labels)
  metrics.SetGaugeWithLabels([]string{"interchain","iln","replica","lag_leaves"},float32(rh.LagLeaves),labels)
  metrics.SetGaugeWithLabels([]string{"interchain","iln","source_rpc","up"},boolMetric(rh.State=="ok"),labels)
 }
 metrics.SetGauge([]string{"interchain","iln","worker","up"},1)
 path:=filepath.Join(w.ilnDir(),"indexer-health.json")
 if err:=os.MkdirAll(filepath.Dir(path),0o770);err!=nil{return}
 raw,err:=json.Marshal(health);if err!=nil{return}
 f,err:=os.CreateTemp(filepath.Dir(path),".health-*");if err!=nil{return}
 defer os.Remove(f.Name())
 if _,err=f.Write(raw);err!=nil{f.Close();return}
 if err=f.Close();err!=nil{return}
 _=os.Rename(f.Name(),path)
}
func boolMetric(b bool)float32{if b{return 1};return 0}
