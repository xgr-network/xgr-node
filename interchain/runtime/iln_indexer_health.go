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

type ilnRouteHealth struct{
 Route string `json:"route"`
 Source string `json:"source"`
 Destination string `json:"destination"`
 State string `json:"state"`
 SourceHead uint64 `json:"sourceHead"`
 ScanCursor uint64 `json:"scanCursor"`
 BlockLag uint64 `json:"blockLag"`
 SourceHeadKnown bool `json:"sourceHeadKnown"`
 ScanCursorKnown bool `json:"scanCursorKnown"`
 LastSuccessfulScanUnix int64 `json:"lastSuccessfulScanUnix,omitempty"`
 UpdatedUnix int64 `json:"updatedUnix"`
 Error string `json:"error,omitempty"`
}
type ilnIndexerHealth struct{
 Version int `json:"version"`
 UpdatedUnix int64 `json:"updatedUnix"`
 WorkerAlive bool `json:"workerAlive"`
 Routes []ilnRouteHealth `json:"routes"`
}

// Block lag measures how much of the CONFIRMED Gateway operation event stream
// remains unscanned. It is not a token/merkle backlog. Every poll runs
// independently of any quorum request.
func (w *Worker) recordILNDirectScan(route *evm.CheckpointRoute,head,cursor uint64){
 if route==nil{return}
 now:=time.Now().Unix()
 state:="ok"
 if cursor<head{state="catching-up"}
 w.mu.Lock()
 if w.ilnRouteHealthByName==nil{w.ilnRouteHealthByName=make(map[string]ilnRouteHealth)}
 w.ilnRouteHealthByName[route.Name]=ilnRouteHealth{
  Route:route.Name,Source:route.SourceNetwork,Destination:route.Destination,
  SourceHead:head,ScanCursor:cursor,BlockLag:head-cursor,State:state,UpdatedUnix:now,
  SourceHeadKnown:true,ScanCursorKnown:true,LastSuccessfulScanUnix:now,
 }
 w.mu.Unlock()
}

// Never erase a previously observed cursor/head on a temporary RPC error.
func (w *Worker) recordILNDirectScanFailure(route *evm.CheckpointRoute,err error){
 if route==nil||err==nil{return}
 w.mu.Lock()
 if w.ilnRouteHealthByName==nil{w.ilnRouteHealthByName=make(map[string]ilnRouteHealth)}
 rh:=w.ilnRouteHealthByName[route.Name]
 // Recover persistent diagnostic cursor even if the first external RPC after
 // restart is rate-limited. Never substitute it for independently verified head.
 if !rh.ScanCursorKnown {
  cursor,exists,readErr:=w.readILNCursor("_direct_"+route.Name)
  if readErr==nil&&exists {rh.ScanCursor=cursor;rh.ScanCursorKnown=true}
 }
 rh.Route=route.Name;rh.Source=route.SourceNetwork;rh.Destination=route.Destination
 rh.State="unavailable";rh.Error=err.Error()
 rh.UpdatedUnix=time.Now().Unix()
 w.ilnRouteHealthByName[route.Name]=rh
 w.mu.Unlock()
}
func (w *Worker) writeILNIndexerHealth(){
 now:=time.Now().Unix()
 health:=ilnIndexerHealth{Version:2,UpdatedUnix:now,WorkerAlive:true,Routes:[]ilnRouteHealth{}}
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
  if !ok{
   rh=ilnRouteHealth{Route:route.Name,Source:route.SourceNetwork,
    Destination:route.Destination,State:"catching-up",
    Error:"awaiting independent confirmed Gateway operation scan",UpdatedUnix:now}
  }
  if !rh.ScanCursorKnown {
   // Health-only disk recovery; does not affect the scanner or signature path.
   cursor,exists,readErr:=w.readILNCursor("_direct_"+route.Name)
   if readErr==nil&&exists {rh.ScanCursor=cursor;rh.ScanCursorKnown=true}
  }
  if now-rh.UpdatedUnix>90||rh.UpdatedUnix>now+30{
   rh.State="unavailable";rh.Error="source Gateway operation scan stale"
  }
  health.Routes=append(health.Routes,rh)
  labels:=[]metrics.Label{{Name:"route",Value:rh.Route},{Name:"source",Value:rh.Source}}
  metrics.SetGaugeWithLabels([]string{"interchain","iln","direct","ready"},
      boolMetric(rh.State=="ok"),labels)
  metrics.SetGaugeWithLabels([]string{"interchain","iln","direct","lag_blocks"},
      float32(rh.BlockLag),labels)
  metrics.SetGaugeWithLabels([]string{"interchain","iln","source_rpc","up"},
      boolMetric(rh.State!="unavailable"),labels)
 }
 metrics.SetGauge([]string{"interchain","iln","worker","up"},1)
 path:=filepath.Join(w.ilnDir(),"indexer-health.json")
 if os.MkdirAll(filepath.Dir(path),0o770)!=nil{return}
 raw,err:=json.Marshal(health);if err!=nil{return}
 f,err:=os.CreateTemp(filepath.Dir(path),".health-*");if err!=nil{return}
 defer os.Remove(f.Name())
 if _,err=f.Write(raw);err!=nil{f.Close();return}
 if err=f.Sync();err!=nil{f.Close();return}
 if err=f.Close();err!=nil{return}
 _=os.Rename(f.Name(),path)
}
func boolMetric(b bool)float32{if b{return 1};return 0}
