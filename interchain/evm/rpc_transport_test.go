package evm

import (
 "context"
 "encoding/json"
 "fmt"
 "io"
 "net/http"
 "net/http/httptest"
 "strings"
 "sync/atomic"
 "testing"
 "time"

 "github.com/stretchr/testify/require"
)

func TestILNRPCRequestCancellationOnWorkerShutdown(t *testing.T){
 started:=make(chan struct{})
 releaseHandler:=make(chan struct{})
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  // Drain the request body so net/http can observe client cancellation.
  // An explicit cleanup channel also guarantees the test server can close
  // even if the underlying network does not propagate a disconnect.
  _,_=io.Copy(io.Discard,r.Body)
  close(started)
  select{
  case <-r.Context().Done():
  case <-releaseHandler:
  }
 }))
 defer func(){close(releaseHandler);server.Close()}()
 parent,cancel:=context.WithCancel(context.Background())
 client,err:=newBoundedILNRPC(parent,server.URL)
 require.NoError(t,err)
 defer client.Close()
 done:=make(chan error,1)
 go func(){var v string;done<-client.Call("eth_chainId",&v)}()
 select{case <-started:case <-time.After(2*time.Second):t.Fatal("RPC did not start")}
 cancel()
 select{
 case err:=<-done:require.Error(t,err);require.True(t,isILNRPCCancelled(err)||strings.Contains(err.Error(),"canceled"),err.Error())
 case <-time.After(2*time.Second):t.Fatal("RPC did not cancel after validator shutdown")
 }
}

func TestILNRPCRejectsOversizedResponseAndMismatchedID(t *testing.T){
 var calls atomic.Int32
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  call:=calls.Add(1)
  w.Header().Set("Content-Type","application/json")
  if call==1{
   fmt.Fprint(w,`{"jsonrpc":"2.0","id":987654321,"result":"0x1"}`);return
  }
  request:=struct{ID uint64 `json:"id"`}{}
  require.NoError(t,json.NewDecoder(r.Body).Decode(&request))
  fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%d,"result":"%s"}`,request.ID,strings.Repeat("f",ilnRPCMaxResponseBytes))
 }))
 defer server.Close()
 client,err:=newBoundedILNRPC(context.Background(),server.URL)
 require.NoError(t,err)
 defer client.Close()
 var v string
 require.ErrorContains(t,client.Call("eth_chainId",&v),"response id mismatch")
 require.ErrorContains(t,client.Call("eth_chainId",&v),"exceeds")
}
