package evm

import (
 "fmt"
 "sort"

 "github.com/umbracle/ethgo"
 

 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

// ILNObservedGatewayOperation is a discovery hint with an explicit emitting
// Gateway address. It never substitutes for a canonical fee-qualified receipt
// and two independently verified full source witnesses before BLS signing.
type ILNObservedGatewayOperation struct {
 Gateway types.Address
 Operation ILNOperation
}

// ConfirmedILNMultiGatewayWindow queries a single finality-checked source
// provider ONCE for any number of gateway addresses within the provider's
// bounded log window. Route and message evidence remains separately verified.
// Providers rejecting a large batch cause an error and MUST NOT move a cursor.
func ConfirmedILNMultiGatewayWindow(
 source *Destination, gateways []types.Address,
 fromBlock,toBlock uint64,
)(head uint64,events []ILNObservedGatewayOperation,err error){
 if source==nil||source.ChainID==1643{
  return 0,nil,fmt.Errorf("external EVM source required for gateway log scanning")
 }
 var client ilnRPCClient
 client,head,err=newVerifiedRPCClientWithHead(source)
 if err!=nil{return}
 defer client.Close()
 events,err=confirmedILNMultiGatewayLogsOnClient(client,gateways,fromBlock,toBlock,head)
 if err!=nil{recordInterchainRPCReadFailure(source)}
 return
}

func confirmedILNMultiGatewayLogsOnClient(
 client ilnRPCClient, gateways []types.Address,
 fromBlock,toBlock,confirmed uint64,
)([]ILNObservedGatewayOperation,error){
 if client==nil{return nil,fmt.Errorf("gateway event client missing")}
 if fromBlock==0||toBlock<fromBlock||toBlock>confirmed||toBlock-fromBlock+1>maxILNLogRange{
  return nil,fmt.Errorf("invalid or non-finalized gateway scan window")
 }
 if len(gateways)==0{return nil,nil}
 seenGateway:=make(map[types.Address]bool,len(gateways))
 addresses:=make([]string,0,len(gateways))
 for _,gateway:=range gateways{
  if gateway==types.ZeroAddress{return nil,fmt.Errorf("zero gateway address")}
  if !seenGateway[gateway]{
   seenGateway[gateway]=true
   addresses=append(addresses,gateway.String())
  }
 }
 topic:=ethgo.Hash(crypto.Keccak256Hash([]byte(ilnOperationEventSignature)))
 // IMPORTANT: ethgo.LogFilter.MarshalJSON silently omits the Address
 // slice with this ethgo version. Use an explicit JSON-RPC argument instead
 // and verify the returned emitter against our bounded address allowlist.
 filter:=map[string]interface{}{
  "address":addresses,
  "fromBlock":fmt.Sprintf("0x%x",fromBlock),
  "toBlock":fmt.Sprintf("0x%x",toBlock),
  "topics":[]interface{}{topic.String()},
 }
 var logs []*ethgo.Log
 err:=client.Call("eth_getLogs",&logs,filter)
 if err!=nil{return nil,fmt.Errorf("multi-Gateway eth_getLogs: %w",err)}
 events:=make([]ILNObservedGatewayOperation,0,len(logs))
 seenEvent:=make(map[struct{block uint64; tx types.Hash; index uint64}]bool,len(logs))
 for _,log:=range logs{
  if log==nil||log.BlockNumber<fromBlock||log.BlockNumber>toBlock||
   !seenGateway[types.Address(log.Address)]{
   return nil,fmt.Errorf("gateway log outside requested finalized filter")
  }
  operation,err:=parseILNOperationLog(log)
  if err!=nil{return nil,err}
  if operation.TransactionHash==types.ZeroHash{
   return nil,fmt.Errorf("missing source transaction hash in gateway event")
  }
  key:=struct{block uint64;tx types.Hash;index uint64}{
   operation.BlockNumber,operation.TransactionHash,operation.LogIndex,
  }
  if seenEvent[key]{continue}
  seenEvent[key]=true
  events=append(events,ILNObservedGatewayOperation{
   Gateway:types.Address(log.Address),Operation:operation,
  })
 }
 sort.Slice(events,func(i,j int)bool{
  a,b:=events[i].Operation,events[j].Operation
  if a.BlockNumber!=b.BlockNumber{return a.BlockNumber<b.BlockNumber}
  if a.LogIndex!=b.LogIndex{return a.LogIndex<b.LogIndex}
  return a.TransactionHash.String()<b.TransactionHash.String()
 })
 return events,nil
}

// ScanILNMultiGatewayWindow verifies the source's finalized head just ONCE
// and queries all discovered Gateways on that same verified provider.
func ScanILNMultiGatewayWindow(
 source *Destination,gateways []types.Address,cursor,window uint64,
)(head,from,to uint64,events []ILNObservedGatewayOperation,err error){
 if source==nil||source.ChainID==1643||window==0||window>maxILNLogRange{
  return 0,0,0,nil,fmt.Errorf("invalid external observer window")
 }
 client,h,e:=newVerifiedRPCClientWithHead(source)
 if e!=nil{return 0,0,0,nil,e}
 defer client.Close()
 head=h
 if cursor>head{return head,0,0,nil,fmt.Errorf("confirmed source head behind observer cursor")}
 if cursor==head{return head,0,0,nil,nil}
 from=cursor+1
 to=cursor+window
 if to<cursor||to>head{to=head}
 events,err=confirmedILNMultiGatewayLogsOnClient(client,gateways,from,to,head)
 if err!=nil{recordInterchainRPCReadFailure(source)}
 return
}

// ScanNativeXGRMultiGatewayWindow walks each locally committed IBFT
// block/receipt just once regardless of number of token routes.
func ScanNativeXGRMultiGatewayWindow(reader NativeXGRReader,source *Destination,
 gateways []types.Address,cursor,window uint64,
)(head,from,to uint64,events []ILNObservedGatewayOperation,err error){
 if reader==nil||source==nil||source.ChainID!=1643||window==0||window>maxILNLogRange{
  return 0,0,0,nil,fmt.Errorf("invalid native multi-Gateway scan")
 }
 head,err=reader.NativeXGRHead()
 if err!=nil{return}
 if cursor>head{return head,0,0,nil,fmt.Errorf("native observer cursor ahead of IBFT head")}
 if cursor==head{return head,0,0,nil,nil}
 from=cursor+1
 to=cursor+window
 if to<cursor||to>head{to=head}
 watched:=make(map[types.Address]struct{},len(gateways))
 for _,gateway:=range gateways{
  if gateway==types.ZeroAddress{return head,0,0,nil,fmt.Errorf("invalid zero gateway")}
  watched[gateway]=struct{}{}
 }
 topic:=types.Hash(crypto.Keccak256Hash([]byte(ilnOperationEventSignature)))
 for number:=from;number<=to;number++{
  block,e:=reader.NativeXGRBlock(number)
  if e!=nil{return head,0,0,nil,e}
  if block==nil||block.Hash==types.ZeroHash||len(block.Transactions)!=len(block.Receipts){
   return head,0,0,nil,fmt.Errorf("missing canonical native block receipts")}
  var logIndex uint64
  for i,receipt:=range block.Receipts{
   if receipt==nil||receipt.Status==nil||block.Transactions[i]==nil{
    return head,0,0,nil,fmt.Errorf("missing native receipt status")}
   for _,entry:=range receipt.Logs{
    if entry==nil{return head,0,0,nil,fmt.Errorf("nil native log")}
    if _,ok:=watched[entry.Address];ok&&len(entry.Topics)>0&&entry.Topics[0]==topic{
     if *receipt.Status!=types.ReceiptSuccess{
      return head,0,0,nil,fmt.Errorf("reverted native Gateway operation")}
     log:=&ethgo.Log{Address:ethgo.Address(entry.Address),BlockNumber:number,
       TransactionHash:ethgo.Hash(block.Transactions[i].Hash),LogIndex:logIndex,
       Data:append([]byte(nil),entry.Data...)}
     for _,topic:=range entry.Topics{log.Topics=append(log.Topics,ethgo.Hash(topic))}
     operation,e:=parseILNOperationLog(log)
     if e!=nil{return head,0,0,nil,e}
     events=append(events,ILNObservedGatewayOperation{Gateway:entry.Address,Operation:operation})
    }
    logIndex++
   }
  }
 }
 return
}
