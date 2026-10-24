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
