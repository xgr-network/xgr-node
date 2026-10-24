package evm

import (
 "fmt"
 "math/big"
 "github.com/umbracle/ethgo"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

func NativeXGRRoute(reader NativeXGRReader,source *Destination,domain uint32,id types.Hash,block uint64,old bool)(*ILNRouteSnapshot,error){
 if reader==nil||source==nil||source.ChainID!=1643{return nil,fmt.Errorf("native XGR route reader unavailable")}
 head,err:=reader.NativeXGRHead()
 if err!=nil{return nil,err}
 if block==0||block>head{return nil,fmt.Errorf("route block beyond local IBFT finality")}
 b,err:=reader.NativeXGRBlock(head)
 if err!=nil{return nil,err}
 if b==nil||b.View==nil{return nil,fmt.Errorf("local XGR contract state unavailable")}
 return getILNRouteAtBlockWithView(b.View,source,domain,id,block,old)
}

func NativeXGRScanWindow(reader NativeXGRReader,source *Destination,domain uint32,id types.Hash,
 cursor uint64,exists bool,window uint64)(head,from,to uint64,operations []ILNOperation,err error){
 if reader==nil||source==nil||source.ChainID!=1643||window==0||window>maxILNLogRange{
  err=fmt.Errorf("invalid native XGR scan");return
 }
 head,err=reader.NativeXGRHead()
 if err!=nil{return}
 if !exists&&head>128{cursor=head-128}
 if cursor>head{err=fmt.Errorf("native XGR cursor beyond committed head");return}
 if cursor==head{return}
 from=cursor+1
 to=cursor+window
 if to<cursor||to>head{to=head}
 snapshot,e:=NativeXGRRoute(reader,source,domain,id,head,false)
 if e!=nil{err=e;return}
 topic:=types.Hash(crypto.Keccak256Hash([]byte(ilnOperationEventSignature)))
 for number:=from;number<=to;number++{
  block,e:=reader.NativeXGRBlock(number)
  if e!=nil{err=e;return}
  if block==nil||block.Hash==types.ZeroHash||len(block.Transactions)!=len(block.Receipts){
   err=fmt.Errorf("native scan block missing");return
  }
  var logIndex uint64
  for i,receipt:=range block.Receipts{
   if receipt==nil||receipt.Status==nil||block.Transactions[i]==nil{
    err=fmt.Errorf("invalid native scan receipt");return
   }
   for _,entry:=range receipt.Logs{
    if entry==nil{err=fmt.Errorf("nil native scan log");return}
    if entry.Address==snapshot.Route.Gateway&&len(entry.Topics)>0&&entry.Topics[0]==topic{
     if *receipt.Status!=types.ReceiptSuccess{err=fmt.Errorf("native Gateway operation reverted");return}
     log:=&ethgo.Log{Address:ethgo.Address(entry.Address),BlockNumber:number,
       TransactionHash:ethgo.Hash(block.Transactions[i].Hash),LogIndex:logIndex,Data:append([]byte(nil),entry.Data...)}
     for _,value:=range entry.Topics{log.Topics=append(log.Topics,ethgo.Hash(value))}
     op,e:=parseILNOperationLog(log)
     if e!=nil{err=e;return}
     if op.RouteID==id&&op.DestinationDomain==domain{operations=append(operations,op)}
    }
    logIndex++
   }
  }
 }
 return
}

func NativeXGRRouteAdds(reader NativeXGRReader,source *Destination,from,to uint64)([]ILNRouteAdded,error){
 if reader==nil||source==nil||source.ChainID!=1643||from==0||to<from||to-from+1>maxILNLogRange{
  return nil,fmt.Errorf("invalid native registry scan")
 }
 head,err:=reader.NativeXGRHead()
 if err!=nil{return nil,err}
 if to>head{return nil,fmt.Errorf("native registry scan above IBFT committed head")}
 registry:=types.StringToAddress(source.ILNRegistryAddress)
 topic:=types.Hash(crypto.Keccak256Hash([]byte("RouteAdded(uint32,bytes32,address,address,address,uint256,uint64)")))
 out:=make([]ILNRouteAdded,0)
 for number:=from;number<=to;number++{
  block,err:=reader.NativeXGRBlock(number)
  if err!=nil{return nil,err}
  if block==nil||block.Hash==types.ZeroHash||len(block.Transactions)!=len(block.Receipts){
   return nil,fmt.Errorf("invalid native route block")
  }
  for _,receipt:=range block.Receipts{
   if receipt==nil||receipt.Status==nil{return nil,fmt.Errorf("invalid native route receipt")}
   for _,entry:=range receipt.Logs{
    if entry==nil{return nil,fmt.Errorf("nil native route log")}
    if entry.Address!=registry||len(entry.Topics)==0||entry.Topics[0]!=topic{continue}
    if *receipt.Status!=types.ReceiptSuccess||len(entry.Topics)!=4{
     return nil,fmt.Errorf("invalid native RouteAdded receipt")}
    domain:=new(big.Int).SetBytes(entry.Topics[1][:])
    if !domain.IsUint64()||domain.Sign()<=0||domain.Uint64()>uint64(^uint32(0)){
     return nil,fmt.Errorf("invalid native RouteAdded domain")
    }
    routeID:=entry.Topics[2]
    gateway:=types.BytesToAddress(entry.Topics[3][12:])
    if routeID==types.ZeroHash||gateway==types.ZeroAddress{return nil,fmt.Errorf("invalid native RouteAdded identity")}
    out=append(out,ILNRouteAdded{DestinationDomain:uint32(domain.Uint64()),RouteID:routeID,Gateway:gateway,BlockNumber:number})
   }
  }
 }
 return out,nil
}
