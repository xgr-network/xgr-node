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

func TestIndexedDestinationReadsFinalizedDeliveryWithoutSigning(t *testing.T){
 const chain=uint64(56)
 router:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 mailbox:=types.StringToAddress("0x2222222222222222222222222222222222222222")
 message:=types.StringToHash("0x"+strings.Repeat("a",64))
 blockHash:="0x"+strings.Repeat("b",64)
 var delivered atomic.Bool
 var corrupt atomic.Bool
 var reads atomic.Int32
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  var req struct {
   ID json.RawMessage `json:"id"`
   Method string `json:"method"`
   Params []json.RawMessage `json:"params"`
  }
  if json.NewDecoder(r.Body).Decode(&req)!=nil{http.Error(w,"bad json",400);return}
  reads.Add(1)
  w.Header().Set("Content-Type","application/json")
  reply:=func(v interface{}){
   data,_:=json.Marshal(v)
   _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":%s}`,req.ID,data)
  }
  switch req.Method{
  case "eth_chainId":reply("0x38")
  case "eth_getBlockByNumber":
   var tag string
   _=json.Unmarshal(req.Params[0],&tag)
   if tag!="finalized"&&tag!="0x64"{http.Error(w,"unexpected block",400);return}
   h:=blockHash
   if corrupt.Load()&&tag=="0x64"{h="0x"+strings.Repeat("c",64)}
   reply(json.RawMessage(testFinalityBlock(100,h)))
  case "eth_call":
   var arg struct {To string `json:"to"`;Data string `json:"data"`}
   _=json.Unmarshal(req.Params[0],&arg)
   expectedTag:=""
   _=json.Unmarshal(req.Params[1],&expectedTag)
   if expectedTag!="0x64"{http.Error(w,"unsafe call tag",400);return}
   if strings.EqualFold(arg.To,router.String()){
    reply("0x"+strings.Repeat("0",24)+strings.TrimPrefix(mailbox.String(),"0x"))
   }else if strings.EqualFold(arg.To,mailbox.String()){
    v:="0"
    if delivered.Load(){v="1"}
    reply("0x"+strings.Repeat("0",63)+v)
   }else{http.Error(w,"unknown contract",400)}
  default: http.Error(w,"unexpected rpc",400)
  }
 }))
 defer srv.Close()
 dest:=&Destination{Name:"bsc",ChainID:chain,Domain:56,
  RPCURLs:[]string{srv.URL}}
 item,err:=GetIndexedILNDelivery(dest,router,message)
 require.NoError(t,err)
 require.Equal(t,mailbox.String(),item.Mailbox)
 require.Equal(t,uint64(100),item.FinalizedBlockNumber)
 require.False(t,item.Delivered)
 require.Positive(t,reads.Load())
 delivered.Store(true)
 got,err:=GetIndexedILNDelivery(dest,router,message)
 require.NoError(t,err)
 require.True(t,got.Delivered)
 require.Equal(t,"FINALIZED_READ_MODEL_NOT_BLS_EVIDENCE",got.EvidenceStatus)
 corrupt.Store(true)
 _,err=GetIndexedILNDelivery(dest,router,message)
 require.Error(t,err,"conflicting finalized head must fail closed")
}
