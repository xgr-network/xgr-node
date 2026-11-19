package evm

import (
 "encoding/binary"
 "encoding/hex"
 "encoding/json"
 "fmt"
 "net/http"
 "net/http/httptest"
 "strings"
 "sync/atomic"
 "testing"

 "github.com/stretchr/testify/require"
 "github.com/umbracle/ethgo"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

// End-to-end SOURCE-RPC fixture: these are intentionally independent
// JSON-RPC transports, not a stubbed evidence reader. The production
// GetQuorumVerifiedDirectILNOperationByTx path must fetch a canonical
// receipt and verify every route/Gateway/Dispatch/FeeVault binding on EACH.
type pinnedReceiptRPCFixture struct {
 chain uint64
 registry, gateway, router, mailbox, hook, destination, vault, governance, factory types.Address
 operation ILNOperation
 receipt *ethgo.Receipt
 logRequests atomic.Int32
 rpcCalls atomic.Int32
 rejected atomic.Bool
 badCanonical atomic.Bool
 badFeeAllocation atomic.Bool
 badReceipt atomic.Bool
 badHeadTag atomic.Bool
}

func newPinnedReceiptRPCFixture(chain uint64)*pinnedReceiptRPCFixture{
 op,receipt,gateway,mailbox,router,destination:=directSourceReceiptFixture()
 // Authenticated message includes source domain; generate a different
 // actual message ID for each of the four externally verified chains.
 binary.BigEndian.PutUint32(receipt.Logs[0].Data[64+5:64+9],uint32(chain))
 message:=receipt.Logs[0].Data[64:64+90]
 id:=crypto.Keccak256Hash(message)
 receipt.Logs[1].Topics[2]=ethgo.Hash(id)
 op.MessageID=id
 blockHash:=types.StringToHash("0x"+strings.Repeat("1",64))
 receipt.BlockHash=ethgo.Hash(blockHash)
 return &pinnedReceiptRPCFixture{
  chain:chain,registry:types.StringToAddress("0x5555555555555555555555555555555555555555"),
  gateway:gateway,router:router,mailbox:mailbox,destination:destination,
  hook:types.StringToAddress("0x6666666666666666666666666666666666666666"),
  vault:types.StringToAddress("0x7777777777777777777777777777777777777777"),
  governance:types.StringToAddress("0x8888888888888888888888888888888888888888"),
  factory:types.StringToAddress("0x9999999999999999999999999999999999999999"),
  operation:op,receipt:receipt,
 }
}

func pinnedWord(v uint64)string{return fmt.Sprintf("%064x",v)}
func pinnedAddress(v types.Address)string{return fmt.Sprintf("%064x",v.Bytes())}

func (f *pinnedReceiptRPCFixture) routeResult()string {
 fields:=[]string{
  pinnedWord(f.chain),pinnedWord(f.chain),pinnedAddress(f.gateway),
  pinnedAddress(f.router),pinnedAddress(f.mailbox),pinnedAddress(f.hook),
  pinnedAddress(f.destination),pinnedWord(1234),pinnedWord(1),
 }
 return "0x"+strings.Join(fields,"")
}

func (f *pinnedReceiptRPCFixture) receiptResult()interface{}{
 logs:=make([]map[string]interface{},0,len(f.receipt.Logs))
 for _,l:=range f.receipt.Logs{
  topics:=make([]string,len(l.Topics))
  for i,t:=range l.Topics {topics[i]=t.String()}
  logs=append(logs,map[string]interface{}{
   "address":l.Address.String(),"topics":topics,"data":"0x"+hex.EncodeToString(l.Data),
   "blockNumber":"0x4d","blockHash":f.receipt.BlockHash.String(),
   "transactionHash":f.operation.TransactionHash.String(),
   "transactionIndex":"0x0","logIndex":fmt.Sprintf("0x%x",l.LogIndex),
   "removed":false,
  })
 }
 tx:=f.operation.TransactionHash.String()
 if f.badReceipt.Load(){tx="0x"+strings.Repeat("f",64)}
 return map[string]interface{}{
  "transactionHash":tx,"transactionIndex":"0x0",
  "blockHash":f.receipt.BlockHash.String(),"blockNumber":"0x4d",
  "status":"0x1","cumulativeGasUsed":"0x1","gasUsed":"0x1",
  "from":"0x"+strings.Repeat("a",40),"to":f.gateway.String(),
  "contractAddress":nil,
  "logsBloom":"0x"+strings.Repeat("0",512),
  "logs":logs,
 }
}

func (f *pinnedReceiptRPCFixture) handle(t *testing.T,w http.ResponseWriter,r *http.Request){
 f.rpcCalls.Add(1)
 var req struct{ID json.RawMessage `json:"id"`;Method string `json:"method"`;Params []json.RawMessage `json:"params"`}
 if err:=json.NewDecoder(r.Body).Decode(&req);err!=nil{
  http.Error(w,"invalid JSON-RPC",400);return
 }
 w.Header().Set("Content-Type","application/json")
 if f.rejected.Load(){http.Error(w,"rate limited",429);return}
 reply:=func(result interface{}){
  data,_:=json.Marshal(result)
  _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":%s}`,req.ID,data)
 }
 rpcError:=func(code int,msg string){
  _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"error":{"code":%d,"message":%q}}`,req.ID,code,msg)
 }
 switch req.Method{
 case "eth_chainId":
  reply(fmt.Sprintf("0x%x",f.chain))
 case "eth_getBlockByNumber":
  var tag string
  if len(req.Params)!=2||json.Unmarshal(req.Params[0],&tag)!=nil {
   rpcError(-32602,"invalid block params");return
  }
  expected:="safe"
  if f.chain==56{expected="finalized"}
  if tag==expected {
   if f.badHeadTag.Load(){rpcError(-32602,"native finality tag unsupported");return}
   reply(json.RawMessage(testFinalityBlock(138,"0x"+strings.Repeat("1",64))))
   return
  }
  if tag=="0x8a"{
   hash:="0x"+strings.Repeat("1",64)
   if f.badCanonical.Load(){hash="0x"+strings.Repeat("2",64)}
   reply(json.RawMessage(testFinalityBlock(138,hash)));return
  }
  if tag=="0x4d"{
   hash:="0x"+strings.Repeat("1",64)
   if f.badCanonical.Load(){hash="0x"+strings.Repeat("2",64)}
   reply(json.RawMessage(testFinalityBlock(77,hash)));return
  }
  rpcError(-32602,"forbidden finality downgrade or incorrect block tag: "+tag)
 case "eth_getTransactionReceipt":
  if len(req.Params)!=1 {rpcError(-32602,"missing transaction locator");return}
  var tx string
  if json.Unmarshal(req.Params[0],&tx)!=nil||!strings.EqualFold(tx,f.operation.TransactionHash.String()){
   reply(nil);return
  }
  reply(f.receiptResult())
 case "eth_getLogs":
  f.logRequests.Add(1)
  rpcError(-32601,"eth_getLogs intentionally unavailable: receipt pinning must not use it")
 case "eth_call":
  if len(req.Params)!=2{rpcError(-32602,"invalid eth_call params");return}
  var call struct{To string `json:"to"`;Data string `json:"data"`}
  if json.Unmarshal(req.Params[0],&call)!=nil||len(call.Data)<10{
   rpcError(-32602,"invalid eth_call");return
  }
  var block string
  if json.Unmarshal(req.Params[1],&block)!=nil||block!="latest"{
   rpcError(-32602,"only current append-only binding reads supported");return
  }
  selector:=strings.ToLower(call.Data[:10])
  sel:=func(s string)string{return crypto.Keccak256Hash([]byte(s)).String()[:10]}
  to:=strings.ToLower(call.To)
  value:=""
  switch {
  case to==f.registry.String()&&selector==sel("factory()"):value="0x"+pinnedAddress(f.factory)
  case to==f.registry.String()&&selector==strings.ToLower("0x"+hex.EncodeToString(ilnRegistryABI.Methods["getRoute"].ID())):value=f.routeResult()
  case to==f.registry.String()&&selector==sel("governanceRegistry()"):value="0x"+pinnedAddress(f.governance)
  case to==f.gateway.String()&&selector==sel("ilnRegistry()"):value="0x"+pinnedAddress(f.registry)
  case to==f.gateway.String()&&selector==sel("warpRouter()"):value="0x"+pinnedAddress(f.router)
  case to==f.gateway.String()&&selector==sel("mailbox()"):value="0x"+pinnedAddress(f.mailbox)
  case to==f.gateway.String()&&selector==sel("dispatchHook()"):value="0x"+pinnedAddress(f.hook)
  case to==f.gateway.String()&&selector==sel("feeVault()"):value="0x"+pinnedAddress(f.vault)
  case to==f.vault.String()&&selector==sel("gateway()"):value="0x"+pinnedAddress(f.gateway)
  case to==f.vault.String()&&selector==sel("routeId()"):value=f.operation.RouteID.String()
  case to==f.vault.String()&&selector==sel("destinationDomain()"):value="0x"+pinnedWord(1643)
  case to==f.vault.String()&&selector==sel("validatorRegistry()"):value="0x"+pinnedAddress(f.governance)
  case to==f.vault.String()&&selector==sel("allocatedOperation(bytes32)"):
   if f.badFeeAllocation.Load(){value="0x"+pinnedWord(0)}else{value="0x"+pinnedWord(1)}
  case to==f.vault.String()&&selector==sel("allocatedSetId(bytes32)"):value="0x"+pinnedWord(3)
  default:
   rpcError(-32602,"unexpected address / selector: "+to+" "+selector);return
  }
  reply(value)
 default:
  rpcError(-32601,"unexpected method: "+req.Method)
 }
}

func TestReceiptPinnedDirectBLSFullRPCSecurityMatrix(t *testing.T){
 for _,tc:=range []struct{name string; chain uint64}{
  {"bsc_finalized",56},{"base_safe",8453},{"arbitrum_safe",42161},{"optimism_safe",10},
 }{
  t.Run(tc.name,func(t *testing.T){
   a:=newPinnedReceiptRPCFixture(tc.chain)
   b:=newPinnedReceiptRPCFixture(tc.chain)
   sa:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){a.handle(t,w,r)}))
   sb:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){b.handle(t,w,r)}))
   defer sa.Close();defer sb.Close()
   route:=&CheckpointRoute{RouteID:a.operation.RouteID}
   source:=&Destination{
    Name:tc.name,ChainID:tc.chain,Domain:uint32(tc.chain),
    ILNRegistryAddress:a.registry.String(),
    RPCURLs:[]string{sa.URL,sb.URL},
    IndependentOperatorIDs:[]string{"operator-a","operator-b"},
   }
   run:=func()*ILNOperation{
    op,serialized,snapshot,err:=GetQuorumVerifiedDirectILNOperationByTx(
     source,route,1643,a.operation.MessageID,a.operation.BlockNumber,
     a.operation.TransactionHash)
    require.NoError(t,err)
    require.Equal(t,a.operation.MessageID,op.MessageID)
    require.Equal(t,a.operation.TransactionHash,op.TransactionHash)
    require.Equal(t,a.operation.RouteID,snapshot.Route.Key.RouteID)
    require.Equal(t,uint64(77),op.BlockNumber)
    require.Equal(t,a.operation.MessageID,crypto.Keccak256Hash(serialized))
    require.Zero(t,a.logRequests.Load(),"receipt-pinned witness MUST NOT invoke eth_getLogs")
    require.Zero(t,b.logRequests.Load(),"second independent witness MUST NOT invoke eth_getLogs")
    return op
   }
   run()
   require.Positive(t,a.rpcCalls.Load())
   require.Positive(t,b.rpcCalls.Load(),"both operators must fully verify the source")
   b.rejected.Store(true)
   _,_,_,err:=GetQuorumVerifiedDirectILNOperationByTx(source,route,1643,a.operation.MessageID,
    a.operation.BlockNumber,a.operation.TransactionHash)
   require.ErrorContains(t,err,"quorum not reached","ONE available operator may never authorize signing")
   b.rejected.Store(false)
   b.badCanonical.Store(true)
   _,_,_,err=GetQuorumVerifiedDirectILNOperationByTx(source,route,1643,a.operation.MessageID,
    a.operation.BlockNumber,a.operation.TransactionHash)
   require.ErrorContains(t,err,"quorum not reached","conflicting canonical chain heads must fail closed")
   b.badCanonical.Store(false)
   b.badFeeAllocation.Store(true)
   _,_,_,err=GetQuorumVerifiedDirectILNOperationByTx(source,route,1643,a.operation.MessageID,
    a.operation.BlockNumber,a.operation.TransactionHash)
   require.ErrorContains(t,err,"quorum not reached","missing FeeVault allocation must fail closed")
   b.badFeeAllocation.Store(false)
   b.badReceipt.Store(true)
   _,_,_,err=GetQuorumVerifiedDirectILNOperationByTx(source,route,1643,a.operation.MessageID,
    a.operation.BlockNumber,a.operation.TransactionHash)
   require.ErrorContains(t,err,"quorum not reached","one forged receipt must fail closed")
   b.badReceipt.Store(false)
   b.badHeadTag.Store(true)
   _,_,_,err=GetQuorumVerifiedDirectILNOperationByTx(source,route,1643,a.operation.MessageID,
    a.operation.BlockNumber,a.operation.TransactionHash)
   require.ErrorContains(t,err,"quorum not reached","no finality-tag downgrade")
   b.badHeadTag.Store(false)
   // A locator differing by even one bit must not silently fall back to
   // log search, and a mismatched claimed block must never pass.
   _,_,_,err=GetQuorumVerifiedDirectILNOperationByTx(source,route,1643,a.operation.MessageID,
    a.operation.BlockNumber,types.StringToHash("0x"+strings.Repeat("f",64)))
   require.ErrorContains(t,err,"quorum not reached")
   _,_,_,err=GetQuorumVerifiedDirectILNOperationByTx(source,route,1643,a.operation.MessageID,
    a.operation.BlockNumber+1,a.operation.TransactionHash)
   require.ErrorContains(t,err,"quorum not reached")
   require.Zero(t,a.logRequests.Load())
   require.Zero(t,b.logRequests.Load())
  })
 }
}
