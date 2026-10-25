package evm

import (
 "bytes"
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "math/big"
 "net"
 "net/http"
 "strconv"
 "strings"
 "sync"
 "sync/atomic"
 "time"

 "github.com/umbracle/ethgo"
 ethjsonrpc "github.com/umbracle/ethgo/jsonrpc"
)

// M-04: All external Interchain JSON-RPC calls have a REAL cancellation
// deadline. The legacy ethgo fasthttp transport cannot cancel its Do call.
// Use only this stdlib transport for Interchain; do not modify regular RPC,
// TxPool or the IBFT consensus networking implementation.
const (
 ilnRPCRequestTimeout = 12*time.Second
 ilnRPCDialTimeout = 4*time.Second
 ilnRPCMaxResponseBytes = 16<<20
)

type ilnRPCClient interface {
 Call(method string,out interface{},params ...interface{}) error
 Close() error
}

// The scope is shared by per-provider evidence clients, including the
// single-operator copies used in full independent BLS witness verification.
// Copying Destination never copies a sync.Mutex or loses shutdown context.
type ilnRPCScope struct{ctx context.Context}

func (d *Destination) SetILNRPCScope(ctx context.Context){
 if d!=nil {d.rpcScope=&ilnRPCScope{ctx:ctx}}
}
func (d *Destination) ilnRPCParent()context.Context{
 if d!=nil&&d.rpcScope!=nil&&d.rpcScope.ctx!=nil{return d.rpcScope.ctx}
 return context.Background()
}

var ilnHTTPTransport = &http.Transport{
 Proxy:http.ProxyFromEnvironment,
 DialContext:(&net.Dialer{Timeout:ilnRPCDialTimeout,KeepAlive:30*time.Second}).DialContext,
 ForceAttemptHTTP2:true,MaxIdleConns:32,MaxIdleConnsPerHost:4,
 MaxConnsPerHost:8,IdleConnTimeout:30*time.Second,
 TLSHandshakeTimeout:4*time.Second,ResponseHeaderTimeout:10*time.Second,
 ExpectContinueTimeout:1*time.Second,
}

type boundedILNRPC struct{
 endpoint string
 client *http.Client
 ctx context.Context
 cancel context.CancelFunc
 once sync.Once
 sequence atomic.Uint64
}

func newBoundedILNRPC(parent context.Context,endpoint string)(*boundedILNRPC,error){
 if parent==nil{parent=context.Background()}
 if !strings.HasPrefix(endpoint,"https://")&&!strings.HasPrefix(endpoint,"http://"){
  return nil,fmt.Errorf("invalid Interchain HTTP RPC transport")
 }
 ctx,cancel:=context.WithCancel(parent)
 return &boundedILNRPC{
  endpoint:endpoint,client:&http.Client{Transport:ilnHTTPTransport},
  ctx:ctx,cancel:cancel,
 },nil
}
func (c *boundedILNRPC)Close()error{
 if c!=nil{c.once.Do(c.cancel)}
 return nil
}

// Call enforces cancellation for the *real network I/O*, not merely for a
// goroutine waiting on an uncancellable RPC. Oversized responses fail closed
// before allocating unbounded receipt/log buffers.
func (c *boundedILNRPC)Call(method string,out interface{},params ...interface{})error{
 if c==nil||method==""||out==nil{return fmt.Errorf("invalid Interchain RPC request")}
 ctx,cancel:=context.WithTimeout(c.ctx,ilnRPCRequestTimeout)
 defer cancel()
 id:=c.sequence.Add(1)
 payload,err:=json.Marshal(map[string]interface{}{
  "jsonrpc":"2.0","id":id,"method":method,"params":params,
 })
 if err!=nil{return err}
 req,err:=http.NewRequestWithContext(ctx,http.MethodPost,c.endpoint,bytes.NewReader(payload))
 if err!=nil{return err}
 req.Header.Set("Content-Type","application/json")
 resp,err:=c.client.Do(req)
 if err!=nil{return fmt.Errorf("Interchain RPC %s: %w",method,err)}
 defer resp.Body.Close()
 if resp.StatusCode!=http.StatusOK{
  return fmt.Errorf("Interchain RPC %s HTTP %d",method,resp.StatusCode)
 }
 body,err:=io.ReadAll(io.LimitReader(resp.Body,ilnRPCMaxResponseBytes+1))
 if err!=nil{return err}
 if len(body)>ilnRPCMaxResponseBytes{
  return fmt.Errorf("Interchain RPC %s response exceeds %d bytes",method,ilnRPCMaxResponseBytes)
 }
 var response map[string]json.RawMessage
 if err=json.Unmarshal(body,&response);err!=nil{return fmt.Errorf("invalid Interchain RPC response: %w",err)}
 var responseID uint64
 if err=json.Unmarshal(response["id"],&responseID);err!=nil||responseID!=id {
  return fmt.Errorf("Interchain RPC %s response id mismatch",method)
 }
 if rawError,ok:=response["error"];ok&&string(rawError)!="null"{
  var rpcErr struct{Code int `json:"code"`;Message string `json:"message"`}
  if json.Unmarshal(rawError,&rpcErr)!=nil{return fmt.Errorf("invalid Interchain RPC error encoding")}
  return fmt.Errorf("Interchain RPC %s (%d): %s",method,rpcErr.Code,rpcErr.Message)
 }
 result,ok:=response["result"]
 if !ok{return fmt.Errorf("missing Interchain RPC %s result",method)}
 if err=json.Unmarshal(result,out);err!=nil{return fmt.Errorf("invalid Interchain RPC %s result: %w",method,err)}
 return nil
}

func ilnRPCHexUint(value string)(uint64,error){
 if !strings.HasPrefix(value,"0x"){return 0,fmt.Errorf("non-hex RPC integer")}
 return strconv.ParseUint(value[2:],16,64)
}
func ilnRPCHexBig(value string)(*big.Int,error){
 if !strings.HasPrefix(value,"0x"){return nil,fmt.Errorf("non-hex RPC integer")}
 v,ok:=new(big.Int).SetString(value[2:],16)
 if !ok{return nil,fmt.Errorf("invalid RPC big integer")}
 return v,nil
}

// The helpers below work with either the new cancellation-aware production
// transport or a legacy jsonrpc.Client in existing deterministic unit tests.
// This compatibility boundary is exclusively in the Interchain package.
func ilnRPCChainID(c ilnRPCClient)(*big.Int,error){
 var raw string
 if err:=c.Call("eth_chainId",&raw);err!=nil{return nil,err}
 return ilnRPCHexBig(raw)
}
func ilnRPCBlock(c ilnRPCClient,n ethgo.BlockNumber,full bool)(*ethgo.Block,error){
 var v *ethgo.Block
 err:=c.Call("eth_getBlockByNumber",&v,n.String(),full)
 return v,err
}
func ilnRPCReceipt(c ilnRPCClient,h ethgo.Hash)(*ethgo.Receipt,error){
 var v *ethgo.Receipt
 err:=c.Call("eth_getTransactionReceipt",&v,h)
 return v,err
}
func ilnRPCCall(c ilnRPCClient,msg *ethgo.CallMsg,block ethgo.BlockNumber)(string,error){
 var v string
 err:=c.Call("eth_call",&v,msg,block.String())
 return v,err
}
func ilnRPCLogs(c ilnRPCClient,filter *ethgo.LogFilter)([]*ethgo.Log,error){
 if filter==nil{return nil,fmt.Errorf("nil Interchain log filter")}
 // ethgo v0.1.4 omits the multi-address filter field when marshaling.
 // Explicitly preserve address allowlists and topic constraints.
 params:=make(map[string]interface{})
 if len(filter.Address)==1{params["address"]=filter.Address[0].String()}
 if len(filter.Address)>1{
  a:=make([]string,0,len(filter.Address))
  for _,v:=range filter.Address{a=append(a,v.String())}
  params["address"]=a
 }
 if len(filter.Topics)>0{
  topics:=make([]interface{},len(filter.Topics))
  for i,group:=range filter.Topics{
   if group==nil{topics[i]=nil;continue}
   values:=make([]interface{},len(group))
   for j,topic:=range group{
    if topic!=nil{values[j]=topic.String()}
   }
   topics[i]=values
  }
  params["topics"]=topics
 }
 if filter.From!=nil{params["fromBlock"]=filter.From.String()}
 if filter.To!=nil{params["toBlock"]=filter.To.String()}
 if filter.BlockHash!=nil{params["blockHash"]=filter.BlockHash.String()}
 var logs []*ethgo.Log
 err:=c.Call("eth_getLogs",&logs,params)
 return logs,err
}
func ilnRPCBalance(c ilnRPCClient,addr ethgo.Address,block ethgo.BlockNumberOrHash)(*big.Int,error){
 var raw string
 if err:=c.Call("eth_getBalance",&raw,addr,block.Location());err!=nil{return nil,err}
 return ilnRPCHexBig(raw)
}
func ilnRPCGasPrice(c ilnRPCClient)(uint64,error){
 var raw string
 if err:=c.Call("eth_gasPrice",&raw);err!=nil{return 0,err}
 return ilnRPCHexUint(raw)
}
func ilnRPCPriority(c ilnRPCClient)(*big.Int,error){
 var raw string
 if err:=c.Call("eth_maxPriorityFeePerGas",&raw);err!=nil{return nil,err}
 return ilnRPCHexBig(raw)
}
func ilnRPCFeeHistory(c ilnRPCClient,n uint64,block ethgo.BlockNumber,percentiles []float64)(*ethjsonrpc.FeeHistory,error){
 var result *ethjsonrpc.FeeHistory
 err:=c.Call("eth_feeHistory",&result,n,block.String(),percentiles)
 return result,err
}
func ilnRPCEstimateGas(c ilnRPCClient,msg *ethgo.CallMsg)(uint64,error){
 var raw string
 if err:=c.Call("eth_estimateGas",&raw,msg);err!=nil{return 0,err}
 return ilnRPCHexUint(raw)
}

// Wrapping context.Canceled is essential: worker shutdown must never record
// a cancelled call as a successful Gateway scan / complete source proof.
func isILNRPCCancelled(err error)bool{
 return errors.Is(err,context.Canceled)||errors.Is(err,context.DeadlineExceeded)
}
