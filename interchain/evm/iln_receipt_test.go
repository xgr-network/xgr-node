package evm

import (
 "encoding/hex"
 "encoding/json"
 "fmt"
 "math/big"
 "net/http"
 "net/http/httptest"
 "testing"

 "github.com/stretchr/testify/require"
 "github.com/umbracle/ethgo/jsonrpc"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

func TestVerifyILNDispatchInReceiptAuthenticatesGatewayAndMailbox(t *testing.T) {
 gateway:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 mailbox:=types.StringToAddress("0x2222222222222222222222222222222222222222")
 router:=types.StringToAddress("0x3333333333333333333333333333333333333333")
 destinationRouter:=types.StringToAddress("0x4444444444444444444444444444444444444444")
 routeID:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 txHash:=types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
 message:=[]byte("genuine warp router mailbox message")
 messageID:=crypto.Keccak256Hash(message)
 fee:=big.NewInt(123)
 op:=ILNOperation{RouteID:routeID,MessageID:messageID,DestinationDomain:1643,ValidatorFeeWei:fee,BlockNumber:77,TransactionHash:txHash,LogIndex:1}

 topic:=func(a types.Address) string{return "0x"+fmt.Sprintf("%064x",new(big.Int).SetBytes(a.Bytes()))}
 word:=func(n *big.Int) []byte{return n.FillBytes(make([]byte,32))}
 bytes32:=func(h types.Hash) string{return h.String()}
 operationTopic:=crypto.Keccak256Hash([]byte(ilnOperationEventSignature))
 dispatchTopic:=crypto.Keccak256Hash([]byte("Dispatch(address,uint32,bytes32,bytes)"))
 dispatchData:=append(word(big.NewInt(32)),word(new(big.Int).SetUint64(uint64(len(message))))...)
 dispatchData=append(dispatchData,message...)
 for len(dispatchData)%32!=0{dispatchData=append(dispatchData,0)}
 gatewayEvent:=map[string]interface{}{
  "address":gateway.String(),"topics":[]string{bytes32(operationTopic),routeID.String(),messageID.String(),fmt.Sprintf("0x%064x",uint64(1643))},
  "data":"0x"+hex.EncodeToString(word(fee)),"blockNumber":"0x4d","logIndex":"0x1","transactionIndex":"0x0",
  "transactionHash":txHash.String(),"removed":false,
 }
 dispatchEvent:=map[string]interface{}{
  "address":mailbox.String(),"topics":[]string{bytes32(dispatchTopic),topic(router),fmt.Sprintf("0x%064x",uint64(1643)),topic(destinationRouter)},
  "data":"0x"+hex.EncodeToString(dispatchData),"blockNumber":"0x4d","logIndex":"0x0","transactionIndex":"0x0",
  "transactionHash":txHash.String(),"removed":false,
 }
 success:=true
 logs:=[]interface{}{dispatchEvent,gatewayEvent}
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  var req struct{ID json.RawMessage `json:"id"`; Method string `json:"method"`}
  require.NoError(t,json.NewDecoder(r.Body).Decode(&req))
  require.Equal(t,"eth_getTransactionReceipt",req.Method)
  status:="0x0";if success{status="0x1"}
  result:=map[string]interface{}{"transactionHash":txHash.String(),"transactionIndex":"0x0",
   "blockNumber":"0x4d","blockHash":"0x"+fmt.Sprintf("%064x",77),"status":status,
   "logs":logs,"from":gateway.String(),"to":gateway.String(),"contractAddress":nil,"gasUsed":"0x5208","cumulativeGasUsed":"0x5208","logsBloom":"0x"+fmt.Sprintf("%0512x",0)}
  raw,_:=json.Marshal(result)
  w.Header().Set("Content-Type","application/json")
  _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":%s}`,req.ID,raw)
 }))
 defer server.Close()
 client,err:=jsonrpc.NewClient(server.URL)
 require.NoError(t,err)
 check:=func()error{return verifyILNDispatchInReceipt(client,op,gateway,mailbox,router,1643,destinationRouter)}
 require.NoError(t,check(),"both canonical events in successful receipt must authorize message")
 logs=[]interface{}{dispatchEvent} // malicious direct WarpRouter caller cannot emit Gateway event
 require.Error(t,check(),"missing canonical Gateway must be rejected")
 logs=[]interface{}{gatewayEvent} // forged source message without canonical dispatch
 require.Error(t,check(),"missing canonical Mailbox.Dispatch must be rejected")
 logs=[]interface{}{dispatchEvent,gatewayEvent}
 success=false
 require.Error(t,check(),"reverted source transaction cannot authorize an ILN quorum")
 success=true
 malformed:=map[string]interface{}{}
 for k,v:=range dispatchEvent{malformed[k]=v}
 malformed["topics"]=[]string{bytes32(dispatchTopic),topic(types.StringToAddress("0x9999999999999999999999999999999999999999")),fmt.Sprintf("0x%064x",uint64(1643)),topic(destinationRouter)}
 logs=[]interface{}{malformed,gatewayEvent}
 require.Error(t,check(),"wrong source WarpRouter must be rejected")
}
