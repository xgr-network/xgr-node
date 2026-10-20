package runtime

import (
 "encoding/json"
 "os"
 "path/filepath"
 "sort"
 "time"

 "github.com/armon/go-metrics"
 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
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
func (w *Worker) writeILNIndexerHealth() {
 now:=time.Now().Unix()
 health:=ilnIndexerHealth{Version:1,UpdatedUnix:now,WorkerAlive:true,Routes:[]ilnRouteHealth{}}
 w.mu.Lock()
 routes:=make([]*evm.CheckpointRoute,0,len(w.routes))
 for _,r:=range w.routes{routes=append(routes,r)}
 w.mu.Unlock()
 sort.Slice(routes,func(i,j int)bool{return routes[i].Name<routes[j].Name})
 for _,route:=range routes{
  if route==nil{continue}
  rh:=ilnRouteHealth{Route:route.Name,Source:route.SourceNetwork,Destination:route.Destination,State:"unavailable",UpdatedUnix:now}
  source:=w.destinations[route.SourceNetwork]
  destination:=w.destinations[route.Destination]
  if source==nil||destination==nil{rh.Error="route network unavailable"
  }else if head,err:=getConfirmedILNHead(source);err!=nil{
   rh.Error="source RPC unavailable: "+err.Error()
  }else{
   rh.SourceHead=head
   if ctx,err:=w.verifiedILNReplicaRoute(route);err==nil{
    rh.CheckpointBlock=ctx.canonical.BlockNumber
    rh.Leaves=ctx.canonical.Count
    rh.LocalLeaves=uint64(len(ctx.replica.Leaves))
    rh.State="ok"
   }else{
    rh.State="catching-up"
    rh.Error=err.Error()
    // Report only local candidate progress. Never count it as verified.
    epoch:=head/32*32
    if epoch>0{
     if snapshot,e:=getILNRouteAtBlock(source,destination.Domain,route.RouteID,head);e==nil{
      snapshot.BlockNumber=epoch
      if cp,e:=getConfirmedILNCheckpoint(source,snapshot);e==nil&&cp.Root!=types.ZeroHash{
       canonical:=evm.ILNReplicaCheckpoint{ChainID:source.ChainID,Hook:snapshot.Route.MerkleTreeHook,Root:cp.Root,Count:uint64(cp.Index)+1,BlockNumber:cp.BlockNumber}
       rh.CheckpointBlock=cp.BlockNumber
       rh.Leaves=canonical.Count
       candidate:=w.ilnReplicaCandidate(canonical)
       rh.LocalLeaves=uint64(len(candidate.Leaves))
      }
     }
    }
   }
   w.mu.Lock()
   for _,snapshot:=range w.ilnReplicaSync {if snapshot!=nil && snapshot.canonical.ChainID==source.ChainID && snapshot.canonical.Count==rh.Leaves {rh.PendingPeers=len(snapshot.attempts);break}}
   w.mu.Unlock()
  }
  if rh.Leaves>=rh.LocalLeaves{rh.LagLeaves=rh.Leaves-rh.LocalLeaves}
  health.Routes=append(health.Routes,rh)
  labels:=[]metrics.Label{{Name:"route",Value:rh.Route},{Name:"source",Value:rh.Source}}
  metrics.SetPrecisionGaugeWithLabels([]string{"interchain","iln","replica","ready"},boolMetric(rh.State=="ok"),labels)
  metrics.SetPrecisionGaugeWithLabels([]string{"interchain","iln","replica","lag_leaves"},float64(rh.LagLeaves),labels)
  metrics.SetPrecisionGaugeWithLabels([]string{"interchain","iln","source_rpc","up"},boolMetric(rh.State!="unavailable"),labels)
 }
 metrics.SetPrecisionGauge([]string{"interchain","iln","worker","up"},1)
 path:=filepath.Join(w.ilnDir(),"indexer-health.json")
 if err:=os.MkdirAll(filepath.Dir(path),0o770);err!=nil{return}
 raw,err:=json.Marshal(health);if err!=nil{return}
 f,err:=os.CreateTemp(filepath.Dir(path),".health-*");if err!=nil{return}
 defer os.Remove(f.Name())
 if _,err=f.Write(raw);err!=nil{f.Close();return}
 if err=f.Close();err!=nil{return}
 _=os.Rename(f.Name(),path)
}
func boolMetric(b bool)float64{if b{return 1};return 0}
