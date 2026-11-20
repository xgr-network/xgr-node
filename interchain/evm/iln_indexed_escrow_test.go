package evm

import (
 "encoding/json"
 "fmt"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"

 "github.com/stretchr/testify/require"
 "github.com/xgr-network/xgr-node/crypto"
)

func TestReadOnlyIndexedExternalEscrowUsesOneFinalizedHead(t *testing.T){
 router:="0x"+strings.Repeat("1",40)
 token:="0x"+strings.Repeat("2",40)
 blockHash:="0x"+strings.Repeat("3",64)
 rpcCalls:=0
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  rpcCalls++
  var req struct{ID json.RawMessage `json:"id"`;Method string `json:"method"`;Params []json.RawMessage `json:"params"`}
  if json.NewDecoder(r.Body).Decode(&req)!=nil{http.Error(w,"bad json",400);return}
  w.Header().Set("Content-Type","application/json")
  reply:=func(v interface{}){x,_:=json.Marshal(v);_,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":%s}`,req.ID,x)}
  switch req.Method{
  case "eth_chainId":reply("0xa4b1")
  case "eth_blockNumber":reply("0x74")
  case "eth_getBlockByNumber":
   var tag string
   _=json.Unmarshal(req.Params[0],&tag)
   if tag!="safe"&&tag!="0x64"{http.Error(w,"wrong head",400);return}
   reply(json.RawMessage(testFinalityBlock(100,blockHash)))
  case "eth_getCode":
   var tag string
   _=json.Unmarshal(req.Params[1],&tag)
   if tag!="0x64"{http.Error(w,"non-finalized code read",400);return}
   reply("0x60006000")
  case "eth_call":
   var call struct{To string `json:"to"`;Data string `json:"data"`}
   _=json.Unmarshal(req.Params[0],&call)
   var tag string
   _=json.Unmarshal(req.Params[1],&tag)
   if tag!="0x64"{http.Error(w,"non-finalized call",400);return}
   sel:=func(v string)string{return fmt.Sprintf("0x%x",crypto.Keccak256([]byte(v))[:4])}
   if call.To==router&&strings.HasPrefix(call.Data,sel("token()")){
    reply("0x"+strings.Repeat("0",24)+strings.TrimPrefix(token,"0x"))
   }else if call.To==token&&strings.HasPrefix(call.Data,sel("decimals()")){
    reply("0x"+fmt.Sprintf("%064x",18))
   }else if call.To==token&&strings.HasPrefix(call.Data,sel("balanceOf(address)")){
    reply("0x"+fmt.Sprintf("%064x",100))
   }else{http.Error(w,"unexpected escrow call",400)}
  default:http.Error(w,"unexpected method",400)
  }
 }))
 defer srv.Close()
 chain:=&Destination{Name:"arbitrum",ChainID:42161,Domain:42161,RPCURLs:[]string{srv.URL}}
 watch:=IndexedILNEscrowWatch{Asset:"ARB",Chain:"arbitrum",Kind:"erc20",Router:router,Token:token}
 got,err:=GetIndexedILNEscrowSnapshot(chain,watch)
 require.NoError(t,err)
 require.Equal(t,uint64(100),got.FinalizedHeadBlock)
 require.Equal(t,uint64(116),got.LatestHeadBlock)
 require.Equal(t,blockHash,got.FinalizedHeadHash)
 require.Equal(t,"0x"+fmt.Sprintf("%064x",100),got.TokenBalance)
 require.Equal(t,"FINALIZED_ESCROW_READ_MODEL_NOT_BLS_EVIDENCE",got.EvidenceStatus)
 require.Positive(t,rpcCalls)
 watch.Router="0x"+strings.Repeat("0",40)
 _,err=GetIndexedILNEscrowSnapshot(chain,watch)
 require.Error(t,err)
}
