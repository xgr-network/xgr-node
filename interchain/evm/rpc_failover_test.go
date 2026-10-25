package evm

import (
 "encoding/json"
 "fmt"
 "net/http"
 "net/http/httptest"
 "strings"
 "sync/atomic"
 "testing"
 "github.com/stretchr/testify/require"
    "github.com/xgr-network/xgr-node/types"
)

func testRPCProvider(chain uint64,supportSafe bool,calls *atomic.Int32)*httptest.Server{
 return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  calls.Add(1)
  var req struct{ID json.RawMessage `json:"id"`;Method string `json:"method"`;Params []json.RawMessage `json:"params"`}
  if json.NewDecoder(r.Body).Decode(&req)!=nil{http.Error(w,"bad RPC",400);return}
  w.Header().Set("Content-Type","application/json")
  if req.Method=="eth_chainId"{
   fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":"0x%x"}`,req.ID,chain);return
  }
  if req.Method=="eth_getBlockByNumber"{
   var tag string
   if len(req.Params)>0{_ = json.Unmarshal(req.Params[0],&tag)}
   if tag=="safe"&&!supportSafe{
    fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"error":{"code":-32602,"message":"unsupported safe"}}`,req.ID);return
   }
   fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":%s}`,req.ID,
    testFinalityBlock(138,"0x"+strings.Repeat("1",64)));return
  }
  fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"method not supported"}}`,req.ID)
 }))
}

func TestInterchainRPCFallbackRejectsWrongChainAndUnsupportedSafe(t *testing.T){
 var pCalls,fCalls atomic.Int32
 primary:=testRPCProvider(1,true,&pCalls);defer primary.Close()
 fallback:=testRPCProvider(8453,true,&fCalls);defer fallback.Close()
 d:=&Destination{Name:"base",ChainID:8453,Domain:8453,
  RegistryAddress:"0x3333333333333333333333333333333333333333",
  RPCURLs:[]string{primary.URL,fallback.URL}}
 c,err:=newVerifiedRPCClient(d);require.NoError(t,err);c.Close()
 require.Equal(t,1,rpcState(d).selected)
 require.Positive(t,pCalls.Load());require.Positive(t,fCalls.Load())
 primarySafe:=testRPCProvider(8453,false,&pCalls);defer primarySafe.Close()
 d.RegistryAddress="0x4444444444444444444444444444444444444444"
 d.RPCURLs=[]string{primarySafe.URL,fallback.URL}
 c,err=newVerifiedRPCClient(d);require.NoError(t,err);c.Close()
 require.Equal(t,1,rpcState(d).selected)
}

func TestInterchainRPCFallbackFailClosedWhenNoSafeProvider(t *testing.T){
 var a,b atomic.Int32
 first:=testRPCProvider(8453,false,&a);defer first.Close()
 second:=testRPCProvider(42161,true,&b);defer second.Close()
 d:=&Destination{Name:"base",ChainID:8453,Domain:8453,
  RegistryAddress:"0x5555555555555555555555555555555555555555",
  RPCURLs:[]string{first.URL,second.URL}}
 c,err:=newVerifiedRPCClient(d)
 require.ErrorContains(t,err,"all interchain RPC endpoints failed")
 require.Nil(t,c)
}

func TestRPCProvider429CooldownAndIndependentEndpointRecovery(t *testing.T) {
    // Explicit 429 cooldown never affects another independent endpoint.
    d := &Destination{Name:"base",ChainID:8453,Domain:8453,
        RegistryAddress:"0x7777777777777777777777777777777777777777",
        RPCURLs:[]string{"https://rate-limit.invalid/rpc","https://healthy.invalid/rpc"}}
    rpcRecordFailure(d,d.RPCURLs[0],fmt.Errorf("HTTP 429: too many requests"),true)
    require.ErrorContains(t,rpcCooldown(d,d.RPCURLs[0]),"cooldown")
    require.NoError(t,rpcCooldown(d,d.RPCURLs[1]))
    before:=InterchainRPCProviderStats()
    found:=false
    for _,item:=range before {
        if item.ChainID==8453 && item.URL==d.RPCURLs[0] {
            require.Positive(t,item.RateLimited)
            require.Positive(t,item.ReadFailures)
            require.Positive(t,item.CooldownUntilUnix)
            found=true
        }
    }
    require.True(t,found)
    // A successful chain-ID + safe-head preflight must NOT clear a 429
    // raised by a later eth_getLogs request.
    rpcRecordReady(d,d.RPCURLs[0])
    require.ErrorContains(t,rpcCooldown(d,d.RPCURLs[0]),"cooldown")
    require.NoError(t,rpcCooldown(d,d.RPCURLs[1]))
}

func TestRPCProviderCooldownSkipsBlockedPrimary(t *testing.T) {
    var primaryCalls,fallbackCalls atomic.Int32
    primary:=testRPCProvider(8453,true,&primaryCalls)
    defer primary.Close()
    fallback:=testRPCProvider(8453,true,&fallbackCalls)
    defer fallback.Close()
    d:=&Destination{Name:"base",ChainID:8453,Domain:8453,
        RegistryAddress:"0x8888888888888888888888888888888888888888",
        RPCURLs:[]string{primary.URL,fallback.URL}}
    rpcRecordFailure(d,primary.URL,fmt.Errorf("429 rate limited"),true)
    client,head,err:=newVerifiedRPCClientWithHead(d)
    require.NoError(t,err)
    require.Equal(t,uint64(138),head)
    client.Close()
    require.Equal(t,int32(0),primaryCalls.Load())
    require.Positive(t,fallbackCalls.Load())
}

func TestCaughtUpDirectScanDoesNotReadRouteOrLogs(t *testing.T) {
    var calls atomic.Int32
    server:=testRPCProvider(8453,true,&calls)
    defer server.Close()
    source:=&Destination{Name:"base",ChainID:8453,Domain:8453,
        RegistryAddress:"0x9999999999999999999999999999999999999999",
        RPCURL:server.URL,
        ILNRegistryAddress:"0x1111111111111111111111111111111111111111"}
    routeID:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
    head,from,to,ops,err:=ScanDirectILNOperationWindow(source,1643,routeID,138,true,80)
    require.NoError(t,err)
    require.Equal(t,uint64(138),head)
    require.Zero(t,from)
    require.Zero(t,to)
    require.Empty(t,ops)
    // Exactly one chainId, one safe block and one numeric hash comparison.
    require.Equal(t,int32(3),calls.Load())
}

func TestGetLogs429SurvivesSuccessfulSafeHeadPreflight(t *testing.T){
 // Global provider metrics are deliberately process-scoped. An isolated
 // endpoint makes repeated -count runs independent of previous test state.
 server:=httptest.NewServer(http.NotFoundHandler())
 defer server.Close()
 d:=&Destination{Name:"base",ChainID:8453,Domain:8453,
  RPCURLs:[]string{server.URL}}
 url:=d.RPCURLs[0]
 rpcRecordFailure(d,url,fmt.Errorf("Interchain RPC eth_getLogs HTTP 429"),true)
 p:=rpcProviderState(d,url)
 p.mu.Lock()
 first:=p.readConsecutive
 p.mu.Unlock()
 require.Equal(t,uint32(1),first)
 // A successful safe-head check must not clear the log-specific cooldown.
 rpcRecordReady(d,url)
 require.ErrorContains(t,rpcCooldown(d,url),"cooldown")
 p.mu.Lock()
 after:=p.readConsecutive
 p.mu.Unlock()
 require.Equal(t,first,after)
 rpcRecordFailure(d,url,fmt.Errorf("Interchain RPC eth_getLogs HTTP 429"),true)
 p.mu.Lock()
 twice:=p.readConsecutive
 p.mu.Unlock()
 require.Equal(t,uint32(2),twice)
}
