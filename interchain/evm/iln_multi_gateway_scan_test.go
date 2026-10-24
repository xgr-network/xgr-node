package evm

import (
 "encoding/json"
 "fmt"
 "net/http"
 "net/http/httptest"
 "testing"

 "github.com/stretchr/testify/require"
 "github.com/umbracle/ethgo/jsonrpc"

 "github.com/xgr-network/xgr-node/types"
)

// This checks the real JSON-RPC transport and filter shape; the single
// provider is only a discovery source and is NEVER a full BLS witness.
func TestMultiGatewayScanOneRPCForThreeGateways(t *testing.T){
 rpcCalls:=0
 var filter map[string]interface{}
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  var req struct{
   ID json.RawMessage `json:"id"`
   Method string `json:"method"`
   Params []json.RawMessage `json:"params"`
  }
  if err:=json.NewDecoder(r.Body).Decode(&req);err!=nil{http.Error(w,"bad RPC",400);return}
  if req.Method!="eth_getLogs"{http.Error(w,"unexpected method",400);return}
  rpcCalls++
  require.Len(t,req.Params,1)
  require.NoError(t,json.Unmarshal(req.Params[0],&filter))
  w.Header().Set("Content-Type","application/json")
  fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":[]}`,req.ID)
 }))
 defer server.Close()
 client,err:=jsonrpc.NewClient(server.URL)
 require.NoError(t,err)
 defer client.Close()
 gateways:=[]types.Address{
  types.StringToAddress("0x1111111111111111111111111111111111111111"),
  types.StringToAddress("0x2222222222222222222222222222222222222222"),
  types.StringToAddress("0x3333333333333333333333333333333333333333"),
 }
 events,err:=confirmedILNMultiGatewayLogsOnClient(client,append(gateways,gateways[1]),100,120,120)
 require.NoError(t,err)
 require.Empty(t,events)
 require.Equal(t,1,rpcCalls)
 addresses,ok:=filter["address"].([]interface{})
 require.True(t,ok,"actual eth_getLogs filter: %#v",filter)
 require.Len(t,addresses,3,"deduplicate gateway filters before RPC")
 require.Equal(t,"0x64",filter["fromBlock"])
 require.Equal(t,"0x78",filter["toBlock"])
}
func TestMultiGatewayRejectsUnsafeWindowWithoutRPC(t *testing.T){
 _,err:=confirmedILNMultiGatewayLogsOnClient(nil,nil,100,101,100)
 require.Error(t,err)
}
