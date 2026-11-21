package evm

import (
 "encoding/json"
 "fmt"
 "net/http"
 "net/http/httptest"
 "testing"
 "sync/atomic"
 "strings"

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

type nativeMultiGatewayCountingReader struct{reads int}
func (r *nativeMultiGatewayCountingReader) NativeXGRHead()(uint64,error){return 101,nil}
func (r *nativeMultiGatewayCountingReader) NativeXGRBlock(number uint64)(*NativeXGRBlock,error){
 r.reads++
 return &NativeXGRBlock{Number:number,Hash:types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")},nil
}
func TestNativeXGRMultiGatewayUsesOneBlockReadForThreeGateways(t *testing.T){
 reader:=&nativeMultiGatewayCountingReader{}
 src:=&Destination{Name:"xgr",ChainID:1643,Domain:1643}
 gateways:=[]types.Address{
  types.StringToAddress("0x1111111111111111111111111111111111111111"),
  types.StringToAddress("0x2222222222222222222222222222222222222222"),
  types.StringToAddress("0x3333333333333333333333333333333333333333"),
 }
 head,from,to,events,err:=ScanNativeXGRMultiGatewayWindow(reader,src,gateways,100,256)
 require.NoError(t,err)
 require.Equal(t,uint64(101),head)
 require.Equal(t,uint64(101),from)
 require.Equal(t,uint64(101),to)
 require.Empty(t,events)
 require.Equal(t,1,reader.reads,"Gateway route count must not affect native receipt reads")
}

func TestMultiGateway429FailsOverSameWindow(t *testing.T){
 var firstCalls,secondCalls,firstTotal,secondTotal atomic.Int32
 var requested []string
 gateway:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 makeServer:=func(failing bool)*httptest.Server{
  return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
   if failing{firstTotal.Add(1)}else{secondTotal.Add(1)}
   var req struct{ID json.RawMessage `json:"id"`;Method string `json:"method"`;Params []json.RawMessage `json:"params"`}
   if err:=json.NewDecoder(r.Body).Decode(&req);err!=nil{http.Error(w,"bad RPC",400);return}
   w.Header().Set("Content-Type","application/json")
   switch req.Method{
   case "eth_chainId":fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":"0x2105"}`,req.ID)
   case "eth_getBlockByNumber":
    fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":%s}`,req.ID,
     testFinalityBlock(138,"0x"+strings.Repeat("1",64)))
   case "eth_getLogs":
    if failing {firstCalls.Add(1);http.Error(w,"rate limited",429);return}
    secondCalls.Add(1)
    var filter struct{From string `json:"fromBlock"`;To string `json:"toBlock"`}
    if len(req.Params)!=1||json.Unmarshal(req.Params[0],&filter)!=nil{
     http.Error(w,"bad filter",400);return
    }
    requested=append(requested,filter.From+"-"+filter.To)
    fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":[]}`,req.ID)
   default:http.Error(w,"unsupported",400)
   }
  }))
 }
 first:=makeServer(true);defer first.Close()
 second:=makeServer(false);defer second.Close()
 source:=&Destination{Name:"base",ChainID:8453,Domain:8453,
  RegistryAddress:"0x3333333333333333333333333333333333333333",
  RPCURLs:[]string{first.URL,second.URL}}
 session:=NewILNScanSession(source)
 defer session.Close()
 head,from,to,events,err:=session.GatewayWindow([]types.Address{gateway},100,16)
 require.NoError(t,err)
 require.Equal(t,uint64(138),head)
 require.Equal(t,uint64(101),from)
 require.Equal(t,uint64(116),to)
 require.Empty(t,events)
 require.Equal(t,int32(1),firstCalls.Load())
 require.Equal(t,int32(1),secondCalls.Load())
 for cursor:=uint64(116);cursor<128;cursor+=4{
  _,_,_,_,err:=session.GatewayWindow([]types.Address{gateway},cursor,4)
  require.NoError(t,err)
 }
 require.Equal(t,int32(4),firstTotal.Load(),"failed provider verified once")
 require.Equal(t,int32(7),secondTotal.Load(),"fallback verifies once and reads four windows")
 require.Equal(t,[]string{"0x65-0x74","0x75-0x78","0x79-0x7c","0x7d-0x80"},requested)
 require.ErrorContains(t,rpcCooldown(source,first.URL),"cooldown")
 require.NoError(t,rpcCooldown(source,second.URL))
}
