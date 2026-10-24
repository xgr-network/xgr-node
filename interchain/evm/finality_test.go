package evm

import (
 "encoding/json"
 "fmt"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"

 "github.com/stretchr/testify/require"
 "github.com/umbracle/ethgo"
 "github.com/umbracle/ethgo/jsonrpc"
)

// testFinalityBlock supplies all fields required by ethgo's strict block JSON decoder.
func testFinalityBlock(height uint64, hash string) string {
 return fmt.Sprintf(`{"number":"0x%x","hash":"%s",
  "parentHash":"0x%s","sha3Uncles":"0x%s",
  "transactionsRoot":"0x%s","stateRoot":"0x%s","receiptsRoot":"0x%s",
  "miner":"0x%s","gasLimit":"0x1","gasUsed":"0x0","mixHash":"0x%s",
  "nonce":"0x0000000000000000","timestamp":"0x1","difficulty":"0x0",
  "extraData":"0x","transactions":[],"uncles":[]}`,
  height,hash,strings.Repeat("0",64),strings.Repeat("0",64),
  strings.Repeat("0",64),strings.Repeat("0",64),strings.Repeat("0",64),
  strings.Repeat("0",40),strings.Repeat("0",64))
}

func TestSafeFinalityNoFallbackToLatest(t *testing.T){
 safeAvailable:=true
 mismatched:=false
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  var req struct{ID json.RawMessage `json:"id"`;Method string `json:"method"`;Params []json.RawMessage `json:"params"`}
  require.NoError(t,json.NewDecoder(r.Body).Decode(&req))
  w.Header().Set("Content-Type","application/json")
  switch req.Method {
  case "eth_getBlockByNumber":
   var tag string
   require.NoError(t,json.Unmarshal(req.Params[0],&tag))
   if tag=="safe"&&!safeAvailable {
    _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"error":{"code":-32602,"message":"unsupported safe"}}`,req.ID)
    return
   }
   hash:="0x"+strings.Repeat("1",64)
   if tag!="safe"&&mismatched{hash="0x"+strings.Repeat("2",64)}
   _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":%s}`,req.ID,testFinalityBlock(138,hash))
  case "eth_blockNumber":
   t.Error("must not query latest or a confirmation depth")
  default:
   t.Errorf("unexpected JSON-RPC %s",req.Method)
  }
 }))
 defer server.Close()
 client,err:=jsonrpc.NewClient(server.URL)
 require.NoError(t,err)
 d:=&Destination{Name:"base",ChainID:8453,Domain:8453,RPCURL:server.URL}
 head,err:=finalizedILNHead(client,d)
 require.NoError(t,err)
 require.Equal(t,uint64(138),head)
 safeAvailable=false
 _,err=finalizedILNHead(client,d)
 require.Error(t,err,"unsupported safe MUST fail closed")
 safeAvailable=true
 mismatched=true
 _,err=finalizedILNHead(client,d)
 require.ErrorContains(t,err,"hash")
 d.ChainID=9999
 _,err=finalizedILNHead(client,d)
 require.Error(t,err)
}

func TestIBFTFinalityUsesCommittedLatestHead(t *testing.T){
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  var req struct{ID json.RawMessage `json:"id"`;Method string `json:"method"`;Params []json.RawMessage `json:"params"`}
  require.NoError(t,json.NewDecoder(r.Body).Decode(&req))
  var tag string
  require.NoError(t,json.Unmarshal(req.Params[0],&tag))
  require.Equal(t,"latest",tag)
  _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":%s}`,req.ID,
    testFinalityBlock(101,"0x"+strings.Repeat("3",64)))
 }))
 defer server.Close()
 client,err:=jsonrpc.NewClient(server.URL);require.NoError(t,err)
 head,err:=finalizedILNHead(client,&Destination{Name:"xgr",ChainID:1643,Domain:1643,RPCURL:server.URL})
 require.NoError(t,err)
 require.Equal(t,uint64(101),head)
}

func TestReceiptMustMatchFinalizedCanonicalBlockHash(t *testing.T) {
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  var req struct{ID json.RawMessage `json:"id"`;Method string `json:"method"`;Params []json.RawMessage `json:"params"`}
  require.NoError(t,json.NewDecoder(r.Body).Decode(&req))
  _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":%s}`,req.ID,
   testFinalityBlock(77,"0x"+strings.Repeat("1",64)))
 }))
 defer srv.Close()
 client,err:=jsonrpc.NewClient(srv.URL);require.NoError(t,err)
 src:=&Destination{Name:"base",ChainID:8453,Domain:8453,RPCURL:srv.URL}
 blockHash:=ethgo.Hash{}
 for i:=range blockHash {blockHash[i]=0x11}
 receipt:=&ethgo.Receipt{Status:1,BlockNumber:77,BlockHash:blockHash}
 require.NoError(t,verifyFinalizedReceipt(client,src,receipt))
 receipt.BlockHash[0]=0x22
 require.ErrorContains(t,verifyFinalizedReceipt(client,src,receipt),"canonical")
 receipt.BlockHash[0]=0x11
 receipt.BlockNumber=78
 require.Error(t,verifyFinalizedReceipt(client,src,receipt))
}
