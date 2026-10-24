package runtime

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "sort"
 "strings"

 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

// knownObserverRoute reads the durable catalog written only after independent
// Registry validation during permissionless discovery. Uncataloged configured
// routes keep their existing per-route scanner, never a guessed Gateway.
func (w *Worker) knownObserverRoute(source *evm.Destination,route *evm.CheckpointRoute)(observerRoute,error){
 if w==nil||source==nil||route==nil{return observerRoute{},fmt.Errorf("missing observer route")}
 dest:=w.destinations[route.Destination]
 if dest==nil||dest.Domain==0{return observerRoute{},fmt.Errorf("unconfigured observer destination")}
 name:=fmt.Sprintf("%d-%s.json",dest.Domain,strings.TrimPrefix(route.RouteID.String(),"0x"))
 raw,err:=os.ReadFile(filepath.Join(w.ilnRouteCatalogDir(source),name))
 if err!=nil{return observerRoute{},err}
 var record ilnDiscoveredRoute
 if err=json.Unmarshal(raw,&record);err!=nil{return observerRoute{},err}
 if record.SourceNetwork!=source.Name||record.SourceDomain!=source.Domain||
  record.DestinationDomain!=dest.Domain||!strings.EqualFold(record.RouteID,route.RouteID.String())||
  !types.IsValidAddressString(record.Gateway){
  return observerRoute{},fmt.Errorf("mismatched observer Registry catalog route")
 }
 gateway:=types.StringToAddress(record.Gateway)
 if gateway==types.ZeroAddress{return observerRoute{},fmt.Errorf("zero catalog gateway")}
 return observerRoute{Route:route,Gateway:gateway,DestinationDomain:dest.Domain},nil
}

// scanSharedILNSource safely migrates on the fly using existing durable
// per-route cursors. A new route begins at its registry activation block;
// the group always starts at the minimum cursor and only advances after all
// dispatched signing tasks succeed. A crash replays idempotent BLS votes.
func (w *Worker) scanSharedILNSource(source *evm.Destination,routes []observerRoute)error{
 if source==nil||len(routes)==0{return fmt.Errorf("empty observer source")}
 cursors:=make(map[string]uint64,len(routes))
 var cursor uint64
 gateways:=make([]types.Address,0,len(routes))
 for i,r:=range routes{
  if r.Route==nil{return fmt.Errorf("nil observer route")}
  cur,exists,err:=w.readILNCursor("_direct_"+r.Route.Name)
  if err!=nil{return err}
  if !exists {
   if source.ILNRegistryActivationBlock==0{return fmt.Errorf("source Registry activation not available")}
   cur=source.ILNRegistryActivationBlock-1
  }
  cursors[r.Route.Name]=cur
  if i==0||cur<cursor{cursor=cur}
  gateways=append(gateways,r.Gateway)
 }
 // The configured route set is immutable for this poll; newly discovered
 // routes enter at the next tick with their independent historical cursor.
 read:=func(cur uint64,exists bool,window uint64)(uint64,uint64,uint64,[]evm.ILNOperation,error){
  return 0,0,0,nil,fmt.Errorf("not used: observer has gateway provenance")
 }
 _=read
 var native evm.NativeXGRReader
 if source.ChainID==1643{
  r,err:=w.nativeReader(source)
  if err!=nil{return err}
  native=r
 }
 budget:=directILNRemoteWorkBudget
 if source.ChainID==1643{budget=directILNNativeWorkBudget}
 var head uint64
 for attempt:=0;attempt<directILNMaxWindowsPerPoll&&budget>0;attempt++{
  window:=directILNWindow(0,source.ChainID==1643)
  if attempt>0&&head>cursor{window=directILNWindow(head-cursor,source.ChainID==1643)}
  if window>budget{window=budget}
  var from,to uint64
  var events []evm.ILNObservedGatewayOperation
  var err error
  if source.ChainID==1643{
   head,from,to,events,err=evm.ScanNativeXGRMultiGatewayWindow(native,source,gateways,cursor,window)
  }else{
   head,from,to,events,err=evm.ScanILNMultiGatewayWindow(source,gateways,cursor,window)
  }
  if err!=nil{return err}
  if head<cursor{return fmt.Errorf("observer confirmed head regressed")}
  if from==0{
   if head!=cursor||to!=0||len(events)!=0{return fmt.Errorf("invalid empty observer window")}
   break
  }
  if from!=cursor+1||to<from||to>head||to-from+1>window{
   return fmt.Errorf("invalid observer scan range")
  }
  ops:=make([]evm.ILNOperation,0,len(events))
  origins:=make([]types.Address,0,len(events))
  for _,event:=range events{
   ops=append(ops,event.Operation)
   origins=append(origins,event.Gateway)
  }
  dispatched,err:=dispatchObservedILNOperations(routes,ops,origins)
  if err!=nil{return err}
  for _,event:=range dispatched{
   if event.Operation.BlockNumber<=cursors[event.Route.Name]{continue}
   err:=w.signDirectILNMessage(event.Route,event.Operation.MessageID,event.Operation.BlockNumber)
   if err!=nil&&err.Error()!="Interchain message already delivered"{return err}
  }
  // Update every route cursor after all proof checks complete. A failure
  // halfway through cursor commits repeats events, never skips any.
  for _,r:=range routes{
   if cursors[r.Route.Name]>=to{continue}
   if err:=w.writeILNCursor("_direct_"+r.Route.Name,to);err!=nil{return err}
   cursors[r.Route.Name]=to
  }
  cursor=to
  budget-=to-from+1
  if cursor>=head{break}
 }
 for _,r:=range routes{w.recordILNDirectScan(r.Route,head,cursors[r.Route.Name])}
 return nil
}

// scanILNObserverRoutes processes all routes with a trusted source catalog
// in one chain-level scan. Legacy/no-catalog routes retain the prior path;
// no synthetic gateway and no forced migration of existing route cursors.
func (w *Worker) scanILNObserverRoutes(active []*evm.CheckpointRoute)map[string]error{
 failures:=make(map[string]error)
 grouped:=make(map[string][]observerRoute)
 fallback:=make([]*evm.CheckpointRoute,0)
 for _,route:=range active{
  if route==nil{continue}
  source:=w.destinations[route.SourceNetwork]
  if source==nil{failures[route.Name]=fmt.Errorf("missing source");continue}
  binding,err:=w.knownObserverRoute(source,route)
  if err!=nil{fallback=append(fallback,route);continue}
  grouped[route.SourceNetwork]=append(grouped[route.SourceNetwork],binding)
 }
 names:=make([]string,0,len(grouped))
 for name:=range grouped{names=append(names,name)}
 sort.Strings(names)
 for _,name:=range names{
  routes:=grouped[name]
  if len(routes)==1{fallback=append(fallback,routes[0].Route);continue}
  err:=w.scanSharedILNSource(w.destinations[name],routes)
  if err!=nil{
   for _,route:=range routes{failures[route.Route.Name]=err}
  }
 }
 for _,route:=range fallback{
  if err:=w.scanDirectILNOperations(route);err!=nil{failures[route.Name]=err}
 }
 return failures
}
