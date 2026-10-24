package runtime

import (
 "fmt"
 "sort"

 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

// observerRoute describes a verified source-chain Registry route. A gateway
// log alone is untrusted: it is discovery input, never BLS signing evidence.
type observerRoute struct {
 Route *evm.CheckpointRoute
 Gateway types.Address
 DestinationDomain uint32
}

type observerRouteKey struct {
 Gateway types.Address
 DestinationDomain uint32
 RouteID types.Hash
}

type observerDispatch struct {
 Route *evm.CheckpointRoute
 Operation evm.ILNOperation
}

// dispatchObservedILNOperations provides deterministic, duplicate-free local
// fan-out of events fetched once per source chain. It never signs or attests:
// each routed operation still passes the existing full independent source
// evidence verification in signDirectILNMessage.
func dispatchObservedILNOperations(routes []observerRoute, operations []evm.ILNOperation,
 gateways []types.Address) ([]observerDispatch,error) {
 if len(routes)==0&&len(operations)==0{return nil,nil}
 if len(gateways)!=len(operations){return nil,fmt.Errorf("observer operation/gateway count mismatch")}
 index:=make(map[observerRouteKey]*evm.CheckpointRoute,len(routes))
 for _,r:=range routes{
  if r.Route==nil||r.Route.RouteID==types.ZeroHash||r.Gateway==types.ZeroAddress||
   r.DestinationDomain==0{return nil,fmt.Errorf("invalid observer route binding")}
  key:=observerRouteKey{Gateway:r.Gateway,DestinationDomain:r.DestinationDomain,RouteID:r.Route.RouteID}
  if old:=index[key];old!=nil&&old.Name!=r.Route.Name{
   return nil,fmt.Errorf("ambiguous observer route identity")}
  index[key]=r.Route
 }
 type dedupKey struct{
  RouteName string
  TransactionHash types.Hash
  LogIndex uint64
  MessageID types.Hash
 }
 seen:=make(map[dedupKey]bool,len(operations))
 out:=make([]observerDispatch,0,len(operations))
 for i,op:=range operations{
  if op.RouteID==types.ZeroHash||op.MessageID==types.ZeroHash||op.TransactionHash==types.ZeroHash||
   op.BlockNumber==0||op.ValidatorFeeWei==nil||op.ValidatorFeeWei.Sign()<=0{
   return nil,fmt.Errorf("invalid observed Gateway event")}
  route:=index[observerRouteKey{Gateway:gateways[i],DestinationDomain:op.DestinationDomain,RouteID:op.RouteID}]
  if route==nil{continue} // Unknown route: catalog/Registry discovery owns registration.
  key:=dedupKey{RouteName:route.Name,TransactionHash:op.TransactionHash,
   LogIndex:op.LogIndex,MessageID:op.MessageID}
  if seen[key]{continue}
  seen[key]=true
  out=append(out,observerDispatch{Route:route,Operation:op})
 }
 sort.Slice(out,func(i,j int)bool{
  a,b:=out[i].Operation,out[j].Operation
  if a.BlockNumber!=b.BlockNumber{return a.BlockNumber<b.BlockNumber}
  if a.LogIndex!=b.LogIndex{return a.LogIndex<b.LogIndex}
  return out[i].Route.Name<out[j].Route.Name
 })
 return out,nil
}
