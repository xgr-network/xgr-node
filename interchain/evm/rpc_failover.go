package evm

import (
 "fmt"
 "sync"
 "time"
 "github.com/umbracle/ethgo/jsonrpc"
)

// Interchain RPC failover never changes chain/domain, registry, finality policy
// or cryptographic verification. A single RPC client is used per operation:
// callers MUST verify all related receipts and blocks against that client.
const primaryProbeInterval = 30*time.Second
const primaryRecoveryProbes = 3

type rpcHealth struct {
 mu sync.Mutex
 selected int
 lastProbe time.Time
 healthy int
}
var rpcHealthByChain sync.Map

func rpcState(d *Destination)*rpcHealth{
 key:=fmt.Sprintf("%d/%s",d.ChainID,d.RegistryAddress)
 state,_:=rpcHealthByChain.LoadOrStore(key,&rpcHealth{})
 return state.(*rpcHealth)
}
func rpcEndpoints(d *Destination)[]string{
 if d==nil{return nil}
 if len(d.RPCURLs)>0{return d.RPCURLs}
 if d.RPCURL!=""{return []string{d.RPCURL}}
 return nil
}
func rpcDialVerified(d *Destination,url string)(*jsonrpc.Client,error){
 client,err:=jsonrpc.NewClient(url)
 if err!=nil{return nil,err}
 chainID,err:=client.Eth().ChainID()
 if err!=nil||chainID==nil||!chainID.IsUint64()||chainID.Uint64()!=d.ChainID{
  client.Close()
  return nil,fmt.Errorf("interchain RPC chain identity failed for %s: %v",d.Name,err)
 }
 // safe/IBFT finality is strictly enforced without downgrades. The numeric
 // block returned by this provider must match the native finality block hash.
 if _,err=finalizedILNHead(client,d);err!=nil{
  client.Close()
  return nil,fmt.Errorf("interchain RPC finality unavailable for %s: %w",d.Name,err)
 }
 return client,nil
}
// newVerifiedRPCClient selects one provider for a complete read operation.
// On a failing primary it immediately tests the fallback. The primary is
// probed at most every 30s and restored only after three successful probes.
// Selection is per validator process and never changes PoS/IBFT RPC.
func newVerifiedRPCClient(d *Destination)(*jsonrpc.Client,error){
 endpoints:=rpcEndpoints(d)
 if len(endpoints)==0{return nil,fmt.Errorf("interchain RPC endpoint missing")}
 if len(endpoints)==1{return jsonrpc.NewClient(endpoints[0])}
 state:=rpcState(d)
 state.mu.Lock()
 defer state.mu.Unlock()
 now:=time.Now()
 if state.selected>0 {
  if now.Sub(state.lastProbe)>=primaryProbeInterval{
   state.lastProbe=now
   p,err:=rpcDialVerified(d,endpoints[0])
   if err==nil{
    state.healthy++
    if state.healthy>=primaryRecoveryProbes{
     state.selected=0
     state.healthy=0
     return p,nil
    }
    p.Close()
   }else{state.healthy=0}
  }
  fallback,err:=rpcDialVerified(d,endpoints[state.selected])
  if err==nil{return fallback,nil}
  state.healthy=0
  // If selected fallback is unavailable, probe all remaining endpoints.
  for idx,url:=range endpoints{
   if idx==state.selected{continue}
   candidate,e:=rpcDialVerified(d,url)
   if e==nil{state.selected=idx;state.lastProbe=now;return candidate,nil}
  }
  return nil,fmt.Errorf("all interchain RPC endpoints failed for %s: %w",d.Name,err)
 }
 primary,err:=rpcDialVerified(d,endpoints[0])
 if err==nil{return primary,nil}
 for idx:=1;idx<len(endpoints);idx++{
  fallback,e:=rpcDialVerified(d,endpoints[idx])
  if e==nil{
   state.selected=idx
   state.lastProbe=now
   state.healthy=0
   return fallback,nil
  }
 }
 return nil,fmt.Errorf("all interchain RPC endpoints failed for %s: %w",d.Name,err)
}
func selectVerifiedRPCURL(d *Destination)(string,error){
 client,err:=newVerifiedRPCClient(d)
 if err!=nil{return "",err}
 defer client.Close()
 // Existing txrelayer accepts only a URL; this URL is verified before a
 // submission, but transaction broadcasting is NEVER automatically retried.
 urls:=rpcEndpoints(d)
 state:=rpcState(d)
 state.mu.Lock()
 idx:=state.selected
 state.mu.Unlock()
 if idx<0||idx>=len(urls){return "",fmt.Errorf("invalid selected interchain RPC index")}
 return urls[idx],nil
}
