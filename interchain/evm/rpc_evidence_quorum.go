package evm

import (
 "bytes"
 "fmt"
 "reflect"

 "github.com/xgr-network/xgr-node/types"
)

// GetQuorumVerifiedDirectILNOperation requires two matching, independently
// operated source RPC witnesses before any Direct-BLS vote may be signed.
// Provider failures are skipped, disagreements never count as a match.
// A witness proves consistency with another operator, not a trustless
// light-client proof or Ethereum L1 settlement.
func GetQuorumVerifiedDirectILNOperation(source *Destination,route *CheckpointRoute,
 destinationDomain uint32,messageID types.Hash,sourceBlock uint64,
)(*ILNOperation,[]byte,*ILNRouteSnapshot,error){
 if source==nil{return nil,nil,nil,fmt.Errorf("missing source for RPC quorum")}
 urls:=rpcEndpoints(source)
 if len(urls)<2||len(source.IndependentOperatorIDs)!=len(urls){
  return nil,nil,nil,fmt.Errorf("source RPC quorum requires independent operators")}
 type observation struct{
  operation *ILNOperation
  message []byte
  snapshot *ILNRouteSnapshot
  operators map[string]bool
 }
 var seen []observation
 for i,url:=range urls {
  operator:=source.IndependentOperatorIDs[i]
  if operator=="" {continue}
  // Each full evidence read uses exactly one provider. Never mix a receipt
  // from provider A with a block hash or route getter from provider B.
  copySource:=*source
  copySource.RPCURL=url
  copySource.RPCURLs=[]string{url}
  copySource.IndependentOperatorIDs=[]string{operator}
  op,msg,routeSnapshot,err:=GetDirectILNOperation(&copySource,route,destinationDomain,messageID,sourceBlock)
  if err!=nil{continue} // Outage/lag/unsupported archival RPC: try next.
  if op.CanonicalBlockHash==types.ZeroHash {continue}
  matched:=false
  for j:=range seen{
   prev:=&seen[j]
   if prev.operators[operator] {continue}
   if sameILNWitness(prev.operation,prev.message,prev.snapshot,op,msg,routeSnapshot){
    return op,msg,routeSnapshot,nil
   }
  }
  for j:=range seen {
   if sameILNWitness(seen[j].operation,seen[j].message,seen[j].snapshot,op,msg,routeSnapshot){
    seen[j].operators[operator]=true
    matched=true
    break
   }
  }
  if !matched {seen=append(seen,observation{operation:op,message:msg,snapshot:routeSnapshot,operators:map[string]bool{operator:true}})}
 }
 return nil,nil,nil,fmt.Errorf("independent source RPC quorum not reached: need matching evidence from two operators")
}

func sameILNWitness(a *ILNOperation,am []byte,as *ILNRouteSnapshot,
 b *ILNOperation,bm []byte,bs *ILNRouteSnapshot)bool{
 if a==nil||b==nil||as==nil||bs==nil{return false}
 if a.CanonicalBlockHash==types.ZeroHash||b.CanonicalBlockHash==types.ZeroHash{return false}
 return a.CanonicalBlockHash==b.CanonicalBlockHash &&
  a.TransactionHash==b.TransactionHash && a.LogIndex==b.LogIndex &&
  a.MessageID==b.MessageID && a.RouteID==b.RouteID &&
  a.BlockNumber==b.BlockNumber && a.DestinationDomain==b.DestinationDomain &&
  a.ValidatorFeeWei!=nil && b.ValidatorFeeWei!=nil &&
  a.ValidatorFeeWei.Cmp(b.ValidatorFeeWei)==0 &&
  bytes.Equal(am,bm) && reflect.DeepEqual(as,bs)
}
