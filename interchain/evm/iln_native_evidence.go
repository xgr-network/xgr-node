package evm

import (
 "fmt"
 "math/big"

 "github.com/umbracle/ethgo"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

// NativeXGRBlock represents one locally committed canonical IBFT block.
// Its contract reader executes at the referenced committed state root.
type NativeXGRBlock struct {
 Number uint64
 Hash types.Hash
 Transactions []*types.Transaction
 Receipts []*types.Receipt
 View ILNView
}

// NativeXGRReader is implemented by the node's read-only IBFT adapter.
// It must reject syncing, missing receipts and noncanonical block hashes.
type NativeXGRReader interface {
 NativeXGRHead() (uint64,error)
 NativeXGRBlock(number uint64) (*NativeXGRBlock,error)
}

// VerifyNativeXGROperation requires zero external RPC reads. It reconstructs
// the same canonical Gateway/Mailbox/FeeVault evidence as remote witnesses,
// pinned to locally committed IBFT state. Never trust peer-supplied receipts.
func VerifyNativeXGROperation(
 reader NativeXGRReader,source *Destination,route *CheckpointRoute,
 destinationDomain uint32,messageID types.Hash,sourceBlock uint64,
)(*ILNOperation,[]byte,*ILNRouteSnapshot,error){
 if reader==nil||source==nil||source.ChainID!=1643||source.Domain!=1643||
    route==nil||route.RouteID==types.ZeroHash||messageID==types.ZeroHash||sourceBlock==0{
  return nil,nil,nil,fmt.Errorf("invalid native XGR evidence identity")
 }
 head,err:=reader.NativeXGRHead()
 if err!=nil{return nil,nil,nil,err}
 if sourceBlock>head{return nil,nil,nil,fmt.Errorf("native XGR source block not IBFT-committed")}
 block,err:=reader.NativeXGRBlock(sourceBlock)
 if err!=nil{return nil,nil,nil,err}
 if block==nil||block.Number!=sourceBlock||block.Hash==types.ZeroHash||
    block.View==nil||len(block.Transactions)!=len(block.Receipts){
  return nil,nil,nil,fmt.Errorf("native XGR block/receipts unavailable or inconsistent")
 }
 snapshot,err:=getILNRouteAtBlockWithView(block.View,source,destinationDomain,route.RouteID,sourceBlock,true)
 if err!=nil{return nil,nil,nil,err}
 topic:=types.Hash(crypto.Keccak256Hash([]byte(ilnOperationEventSignature)))
 var found *ILNOperation
 var proofReceipt *ethgo.Receipt
 var logIndex uint64
 for txIndex,receipt:=range block.Receipts {
  tx:=block.Transactions[txIndex]
  if tx==nil||receipt==nil||receipt.Status==nil{return nil,nil,nil,fmt.Errorf("invalid canonical native receipt")}
  logs:=make([]*ethgo.Log,0,len(receipt.Logs))
  for _,entry:=range receipt.Logs{
   if entry==nil{return nil,nil,nil,fmt.Errorf("nil native receipt log")}
   log:=&ethgo.Log{Address:ethgo.Address(entry.Address),Data:append([]byte(nil),entry.Data...),
      TransactionHash:ethgo.Hash(tx.Hash),BlockNumber:sourceBlock,LogIndex:logIndex}
   for _,v:=range entry.Topics{log.Topics=append(log.Topics,ethgo.Hash(v))}
   logs=append(logs,log)
   if types.Address(log.Address)==snapshot.Route.Gateway && len(log.Topics)==4&&
      types.Hash(log.Topics[0])==topic{
    parsed,e:=parseILNOperationLog(log)
    if e!=nil{return nil,nil,nil,e}
    if parsed.RouteID==route.RouteID && parsed.DestinationDomain==destinationDomain && parsed.MessageID==messageID{
     if found!=nil{return nil,nil,nil,fmt.Errorf("duplicate native Gateway operation")}
     value:=parsed
     found=&value
     proofReceipt=&ethgo.Receipt{TransactionHash:ethgo.Hash(tx.Hash),BlockHash:ethgo.Hash(block.Hash),
       BlockNumber:sourceBlock,Status:uint64(*receipt.Status),Logs:logs}
    }
   }
   logIndex++
  }
  if found!=nil&&proofReceipt!=nil&&types.Hash(proofReceipt.TransactionHash)==tx.Hash{
   proofReceipt.Logs=logs
  }
 }
 if found==nil||proofReceipt==nil{return nil,nil,nil,fmt.Errorf("native Gateway operation not found")}
 if proofReceipt.Status!=uint64(types.ReceiptSuccess){return nil,nil,nil,fmt.Errorf("native Gateway receipt reverted")}
 raw,err:=VerifyDirectILNReceipt(*found,proofReceipt,source.Domain,
  snapshot.Route.Gateway,snapshot.Route.Mailbox,snapshot.Route.SourceRouter,snapshot.Route.DestinationRouter)
 if err!=nil{return nil,nil,nil,err}
 if err=verifyFeeAllocationWithView(block.View,source,snapshot,messageID,sourceBlock,head);err!=nil{
  return nil,nil,nil,fmt.Errorf("native FeeVault allocation: %w",err)
 }
 if found.ValidatorFeeWei==nil||found.ValidatorFeeWei.Cmp(big.NewInt(0))<=0{
  return nil,nil,nil,fmt.Errorf("invalid native validator fee")
 }
 found.CanonicalBlockHash=block.Hash
 return found,raw,snapshot,nil
}
