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
  types.IsValidAddress(record.Gateway)!=nil{
  return observerRoute{},fmt.Errorf("mismatched observer Registry catalog route")
 }
 gateway:=types.StringToAddress(record.Gateway)
 if gateway==types.ZeroAddress{return observerRoute{},fmt.Errorf("zero catalog gateway")}
 return observerRoute{Route:route,Gateway:gateway,DestinationDomain:dest.Domain},nil
}

// Injectable seams are used by deterministic multi-route cursor and request
// count tests; production always binds the canonical scan/signing functions.
var observerRemoteWindow = evm.ScanILNMultiGatewayWindow
var observerNativeWindow = evm.ScanNativeXGRMultiGatewayWindow
var observerSignMessage = func(w *Worker,r *evm.CheckpointRoute,id types.Hash,n uint64)error{
 return w.signDirectILNMessage(r,id,n)
}

// ensureObserverCatalogRoute bootstraps a previously uncataloged configured
// route once from the confirmed source Registry. It does NOT perform a
// route-specific Gateway event scan: once bound, the route joins the shared
// per-source-chain observer. A failed Registry read leaves its cursor intact
// and is retried using existing per-route backoff.
func (w *Worker) ensureObserverCatalogRoute(source *evm.Destination,route *evm.CheckpointRoute,head uint64)(observerRoute,error){
 binding,err:=w.knownObserverRoute(source,route)
 if err==nil{return binding,nil}
 if !os.IsNotExist(err){return observerRoute{},fmt.Errorf("invalid durable observer route catalog for %s: %w",route.Name,err)}
 destination:=w.destinations[route.Destination]
 if destination==nil||destination.Domain==0{return observerRoute{},fmt.Errorf("missing observer route destination")}
 if head==0{return observerRoute{},fmt.Errorf("confirmed source head unavailable for route bootstrap")}
 snapshot,err:=w.sourceILNRouteAtBlock(source,destination.Domain,route.RouteID,head)
 if err!=nil{return observerRoute{},fmt.Errorf("verify uncataloged route %s: %w",route.Name,err)}
 if !validILNSourceRouteNotice(source,destination.Domain,route.RouteID,snapshot){
  return observerRoute{},fmt.Errorf("uncataloged route lacks independently verified source Registry binding: %s",route.Name)
 }
 event:=evm.ILNRouteAdded{DestinationDomain:destination.Domain,RouteID:route.RouteID,
  Gateway:snapshot.Route.Gateway,BlockNumber:head}
 if err=w.rememberILNRouteAdded(source,event);err!=nil{return observerRoute{},err}
 return w.knownObserverRoute(source,route)
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
   head,from,to,events,err=observerNativeWindow(native,source,gateways,cursor,window)
  }else{
   head,from,to,events,err=observerRemoteWindow(source,gateways,cursor,window)
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
  // A poisoned or temporarily unverifiable message must not prevent the
  // other, independently authenticated messages in this SAME scanned window
  // from obtaining their votes. Never commit ANY cursor on a failure: on
  // retry the original window is replayed idempotently, including failed work.
  // This is a partial availability isolation only; durable per-message
  // quarantine/retry is needed before N-01 can be marked fully resolved.
  var firstFailure error
  for _,event:=range dispatched{
   if event.Operation.BlockNumber<=cursors[event.Route.Name]{continue}
   err:=observerSignMessage(w,event.Route,event.Operation.MessageID,event.Operation.BlockNumber)
   if err!=nil&&err.Error()!="Interchain message already delivered"&&firstFailure==nil{
    firstFailure=fmt.Errorf("source message %s on %s: %w",
     event.Operation.MessageID.String(),event.Route.Name,err)
   }
  }
  if firstFailure!=nil{return firstFailure}
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

// scanILNObserverRoutes always dispatches Gateway events through ONE
// source-chain observer. A route absent from the verified catalog is enrolled
// via a bounded, one-time Registry lookup; it NEVER starts an independent
// per-route event scan. Failed onboarding does not advance its durable cursor.
// BLS signatures still require full independent source evidence verification.
func (w *Worker) scanILNObserverRoutes(active []*evm.CheckpointRoute)map[string]error{
 failures:=make(map[string]error)
 bySource:=make(map[string][]*evm.CheckpointRoute)
 for _,route:=range active{
  if route==nil{continue}
  source:=w.destinations[route.SourceNetwork]
  if source==nil{failures[route.Name]=fmt.Errorf("missing source");continue}
  bySource[source.Name]=append(bySource[source.Name],route)
 }
 names:=make([]string,0,len(bySource))
 for name:=range bySource{names=append(names,name)}
 sort.Strings(names)
 for _,name:=range names{
  source:=w.destinations[name]
  configured:=bySource[name]
  sort.Slice(configured,func(i,j int)bool{return configured[i].Name<configured[j].Name})
  bindings:=make([]observerRoute,0,len(configured))
  var verifiedHead uint64
  var headRead bool
  for _,route:=range configured{
   // Cache the confirmed head once per source for bootstrap only. Regular
   // grouped Gateway scanning obtains its own finality-qualified head.
   _,catalogErr:=w.knownObserverRoute(source,route)
   if catalogErr!=nil&&os.IsNotExist(catalogErr)&&!headRead{
    var err error
    verifiedHead,err=w.sourceILNHead(source)
    headRead=true
    if err!=nil{
     for _,remaining:=range configured{
      if _,e:=w.knownObserverRoute(source,remaining);os.IsNotExist(e){failures[remaining.Name]=err}
     }
     break
    }
   }
   binding,err:=w.ensureObserverCatalogRoute(source,route,verifiedHead)
   if err!=nil{failures[route.Name]=err;continue}
   bindings=append(bindings,binding)
  }
  if len(bindings)==0{continue}
  if err:=w.scanSharedILNSource(source,bindings);err!=nil{
   for _,binding:=range bindings{failures[binding.Route.Name]=err}
  }
 }
 return failures
}
