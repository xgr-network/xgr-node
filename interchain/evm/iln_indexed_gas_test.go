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

func TestIndexedGasOnlyLooksUpExactFinalizedMessage(t *testing.T){
 mailbox:=types.StringToAddress("0x"+strings.Repeat("1",40))
 message:=types.StringToHash("0x"+strings.Repeat("a",64))
 tx:="0x"+strings.Repeat("b",64)
 hash:="0x"+strings.Repeat("c",64)
 var scans atomic.Int32
 var wrong atomic.Bool
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  var req struct {ID json.RawMessage `json:"id"`; Method string `json:"method"`;Params []json.RawMessage `json:"params"`}
  if json.NewDecoder(r.Body).Decode(&req)!=nil{http.Error(w,"bad json",400);return}
  reply:=func(x interface{}){data,_:=json.Marshal(x);w.Header().Set("Content-Type","application/json");_,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":%s}`,req.ID,data)}
  switch req.Method{
  case "eth_chainId":reply("0x38")
  case "eth_getBlockByNumber":
   var tag string
   _=json.Unmarshal(req.Params[0],&tag)
   if tag!="finalized"&&tag!="0x4d"{http.Error(w,"bad finalized block",400);return}
   reply(json.RawMessage(testFinalityBlock(77,hash)))
  case "eth_getLogs":
   scans.Add(1)
   var filter struct {
    Address string `json:"address"`
    Topics []string `json:"topics"`
    FromBlock string `json:"fromBlock"`
    ToBlock string `json:"toBlock"`
   }
   _=json.Unmarshal(req.Params[0],&filter)
   if filter.Address!=mailbox.String()||len(filter.Topics)!=2||
    filter.Topics[1]!=message.String()||filter.FromBlock!="0x4d"||
    filter.ToBlock!="0x4d"{
    http.Error(w,"non-exact indexed log filter",400);return
   }
   reply([]interface{}{map[string]interface{}{
    "address":mailbox.String(),"topics":[]string{indexedProcessIDTopic.String(),message.String()},
    "transactionHash":tx,"blockHash":hash,"blockNumber":"0x4d","removed":false,
   }})
  case "eth_getTransactionReceipt":
   observed:=hash
   if wrong.Load(){observed="0x"+strings.Repeat("d",64)}
   reply(map[string]interface{}{
    "transactionHash":tx,"transactionIndex":"0x0",
    "blockHash":observed,"blockNumber":"0x4d","status":"0x1",
    "gasUsed":"0x5208","cumulativeGasUsed":"0x5208",
    "from":"0x"+strings.Repeat("a",40),"to":mailbox.String(),"contractAddress":nil,
    "logsBloom":"0x"+strings.Repeat("0",512),
    "logs":[]interface{}{},
   })
  default: http.Error(w,"not an approved indexed gas method",400)
  }
 }))
 defer srv.Close()
 chain:=&Destination{Name:"bsc",ChainID:56,Domain:56,RPCURLs:[]string{srv.URL}}
 result,err:=ScanIndexedILNGasWindow(chain,mailbox,message,77,77)
 require.NoError(t,err)
 require.Equal(t,tx,result.TransactionHash)
 require.Equal(t,uint64(21000),result.GasUsed)
 require.Equal(t,"FINALIZED_GAS_READ_MODEL_NOT_BLS_EVIDENCE",result.EvidenceStatus)
 require.Equal(t,int32(1),scans.Load(),"only exact ProcessId lookups")
 wrong.Store(true)
 _,err=ScanIndexedILNGasWindow(chain,mailbox,message,77,77)
 require.Error(t,err,"a forged ProcessId receipt must fail closed")
 _,err=ScanIndexedILNGasWindow(chain,mailbox,message,1,500)
 require.Error(t,err,"gas window may never exceed 150 blocks")
}
