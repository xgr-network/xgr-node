package runtime

import (
 "fmt"
 "sort"

 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

// externalObserverCursor keeps v3.3.3 durable per-route cursors during the
// migration to a source-chain observer. A newly discovered route can lag far
// behind an established route without losing its historical Gateway events.
type externalObserverCursor struct {
 route *evm.CheckpointRoute
 gateway types.Address
 destinationDomain uint32
 cursor uint64
}

// scanDirectILNExternalSource replaces N per-route eth_getLogs requests with
// one batched source query. It advances EACH existing durable route cursor
// only after complete signing checks, local vote persistence and processing.
// The observer event is a hint; signDirectILNMessage re-verifies independent
// full canonical source receipts from two different RPC operators.
func (w *Worker) scanDirectILNExternalSource(
 source *evm.Destination, routes []*evm.CheckpointRoute,
) error {
 if source==nil||source.ChainID==1643{return fmt.Errorf("external ILN observer requires L2 source")}
 if len(routes)==0{return nil}
 confirmedHead,err:=w.sourceILNHead(source)
 if err!=nil{return err}
 items:=make([]externalObserverCursor,0,len(routes))
 gateways:=make([]types.Address,0,len(routes))
 bounds:=make([]observerRoute,0,len(routes))
 for _,route:=range routes {
  if route==nil{return fmt.Errorf("nil source route")}
  destination:=w.destinations[route.Destination]
  if destination==nil{return fmt.Errorf("source route destination unavailable: %s",route.Name)}
  cursor,exists,e:=w.readILNCursor("_direct_"+route.Name)
  if e!=nil{return e}
  if !exists {
   cursor,e=directILNInitialCursor(source)
   if e!=nil{return e}
  }
  if cursor>confirmedHead{return fmt.Errorf("source safe head regressed below route cursor %s",route.Name)}
  snapshot,e:=w.sourceILNRouteAtBlock(source,destination.Domain,route.RouteID,confirmedHead)
  if e!=nil{return fmt.Errorf("verify source route %s: %w",route.Name,e)}
  if !validILNSourceRouteNotice(source,destination.Domain,route.RouteID,snapshot) {
   return fmt.Errorf("invalid source Gateway binding for %s",route.Name)
  }
  gateway:=snapshot.Route.Gateway
  items=append(items,externalObserverCursor{
   route:route,gateway:gateway,destinationDomain:destination.Domain,cursor:cursor,
  })
  gateways=append(gateways,gateway)
  bounds=append(bounds,observerRoute{
   Route:route,Gateway:gateway,DestinationDomain:destination.Domain,
  })
 }
 sort.Slice(items,func(i,j int)bool{return items[i].route.Name<items[j].route.Name})
 cursor:=items[0].cursor
 for _,item:=range items {if item.cursor<cursor{cursor=item.cursor}}
 // Keep a finite RPC budget; provider-specific failures leave every affected
 // route cursor unchanged and existing 429/503 failover/backoff in control.
 for attempts:=0;attempts<directILNMaxWindowsPerPoll&&cursor<confirmedHead;attempts++{
  if !w.state.IsSynchronized(){return fmt.Errorf("XGR validator lost synchronization during source observer")}
  window:=directILNWindow(confirmedHead-cursor,false)
  if window>512{window=512}
  from:=cursor+1
  to:=cursor+window
  if to<cursor||to>confirmedHead{to=confirmedHead}
  head,events,e:=evm.ConfirmedILNMultiGatewayWindow(source,gateways,from,to)
  if e!=nil{return e}
  if head<to||head<cursor{return fmt.Errorf("source safe head regressed during multi-Gateway scan")}
  ops:=make([]evm.ILNOperation,0,len(events))
  addrs:=make([]types.Address,0,len(events))
  for _,entry:=range events{
   ops=append(ops,entry.Operation)
   addrs=append(addrs,entry.Gateway)
  }
  dispatched,e:=dispatchObservedILNOperations(bounds,ops,addrs)
  if e!=nil{return e}
  grouped:=make(map[string][]evm.ILNOperation,len(items))
  for _,entry:=range dispatched{grouped[entry.Route.Name]=append(grouped[entry.Route.Name],entry.Operation)}
  // A failed route stops THIS window without committing its cursor. Routes
  // committed earlier remain replay-safe; repeated votes are idempotent.
  for i:=range items{
   item:=&items[i]
   if item.cursor>=to{continue}
   for _,op:=range grouped[item.route.Name]{
    if op.BlockNumber<=item.cursor{continue}
    e=w.signDirectILNMessage(item.route,op.MessageID,op.BlockNumber)
    if e!=nil&&e.Error()!="Interchain message already delivered"{
     return fmt.Errorf("source observer processing route %s: %w",item.route.Name,e)
    }
   }
   if e=w.writeILNCursor("_direct_"+item.route.Name,to);e!=nil{return e}
   item.cursor=to
   w.recordILNDirectScan(item.route,head,to)
  }
  cursor=to
 }
 return nil
}
