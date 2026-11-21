package evm

import (
 "bytes"
 "encoding/json"
 "fmt"
 "strings"

 "github.com/umbracle/ethgo"
 "github.com/xgr-network/xgr-node/types"
)

const maxILNIndexedReceiptBytes = 256 << 10

// Operational read model, not consensus, not a BLS witness and NOT a source
// of custody authority. A single chain-ID-verified provider supplies this
// finalized transaction snapshot only when a Gateway event was discovered.
// The separate signing path still verifies TWO full independent RPC witnesses.
type IndexedILNSourceReceipt struct {
 Version int `json:"version"`
 SourceChainID uint64 `json:"sourceChainId"`
 SourceTxHash string `json:"sourceTxHash"`
 SourceBlockNumber uint64 `json:"sourceBlockNumber"`
 SourceBlockHash string `json:"sourceBlockHash"`
 SourceRouter string `json:"sourceRouter"`
 DestinationRouter string `json:"destinationRouter"`
 SourceMailbox string `json:"sourceMailbox"`
 Transaction json.RawMessage `json:"transaction"`
 Receipt json.RawMessage `json:"receipt"`
 SourceBlock json.RawMessage `json:"sourceBlock"`
 FinalizedHeadBlock json.RawMessage `json:"finalizedHeadBlock"`
 VerifiedAs string `json:"verifiedAs"`
}

// GetIndexedILNSourceReceipt is called only by an explicitly opted-in
// operational indexer. No calldata, wallet key or user URL is accepted.
func GetIndexedILNSourceReceipt(source *Destination,op ILNOperation,gateway types.Address)(
 *IndexedILNSourceReceipt,error){
 if source==nil||source.ChainID==0||source.ChainID==1643||
  gateway==types.ZeroAddress||op.TransactionHash==types.ZeroHash||
  op.MessageID==types.ZeroHash||op.BlockNumber==0||
  op.RouteID==types.ZeroHash||op.ValidatorFeeWei==nil||
  op.ValidatorFeeWei.Sign()<=0 {
  return nil,fmt.Errorf("invalid external ILN source receipt locator")
 }
 client,head,err:=newVerifiedRPCClientWithHead(source)
 if err!=nil{return nil,err}
 defer client.Close()
 if op.BlockNumber>head{return nil,fmt.Errorf("indexed operation beyond finalized source head")}
 var receiptRaw json.RawMessage
 if err=client.Call("eth_getTransactionReceipt",&receiptRaw,op.TransactionHash.String());err!=nil{return nil,err}
 if len(receiptRaw)<2||len(receiptRaw)>maxILNIndexedReceiptBytes||
  bytes.Equal(receiptRaw,[]byte("null")) {
  return nil,fmt.Errorf("missing or oversized indexed source receipt")
 }
 var receipt ethgo.Receipt
 if err=json.Unmarshal(receiptRaw,&receipt);err!=nil{return nil,err}
 if receipt.TransactionHash!=ethgo.Hash(op.TransactionHash)||
  receipt.BlockNumber!=op.BlockNumber||receipt.BlockHash==ethgo.ZeroHash||
  receipt.Status!=uint64(types.ReceiptSuccess){
  return nil,fmt.Errorf("indexed source receipt identity/status mismatch")
 }
 count:=0
 for _,log:=range receipt.Logs{
  if log==nil||log.Removed||
   log.TransactionHash!=ethgo.Hash(op.TransactionHash)||
   log.BlockNumber!=op.BlockNumber{return nil,fmt.Errorf("untrusted indexed receipt log")}
  if types.Address(log.Address)!=gateway{continue}
  item,e:=parseILNOperationLog(log)
  if e!=nil{continue}
  if item.MessageID==op.MessageID&&item.RouteID==op.RouteID&&
    item.DestinationDomain==op.DestinationDomain&&
    item.ValidatorFeeWei!=nil&&item.ValidatorFeeWei.Cmp(op.ValidatorFeeWei)==0&&
    item.LogIndex==op.LogIndex{count++}
 }
 if count!=1{return nil,fmt.Errorf("indexed receipt has no unique Gateway operation")}
 var txRaw json.RawMessage
 if err=client.Call("eth_getTransactionByHash",&txRaw,op.TransactionHash.String());err!=nil{return nil,err}
 if len(txRaw)<2||len(txRaw)>maxILNIndexedReceiptBytes||bytes.Equal(txRaw,[]byte("null")){
  return nil,fmt.Errorf("indexed source transaction missing")
 }
 // A source read model needs the canonical transaction locator and sender,
 // NOT a fully signature-decoded ethgo.Transaction (legacy and typed RPC
 // transaction encoding differs by chain/provider).
 var tx struct{
  Hash string `json:"hash"`
  From string `json:"from"`
  BlockHash string `json:"blockHash"`
  BlockNumber string `json:"blockNumber"`
 }
 if err=json.Unmarshal(txRaw,&tx);err!=nil{return nil,err}
 if !strings.EqualFold(tx.Hash,op.TransactionHash.String())||
    types.IsValidAddress(tx.From)!=nil||
    types.StringToAddress(tx.From)==types.ZeroAddress||
    !strings.EqualFold(tx.BlockHash,receipt.BlockHash.String())||
    tx.BlockNumber!=fmt.Sprintf("0x%x",op.BlockNumber){
  return nil,fmt.Errorf("indexed transaction hash/sender/block mismatch")
 }
 var sourceBlockRaw json.RawMessage
 if err=client.Call("eth_getBlockByNumber",&sourceBlockRaw,
  fmt.Sprintf("0x%x",op.BlockNumber),false);err!=nil{return nil,err}
 var sourceBlock ethgo.Block
 if err=json.Unmarshal(sourceBlockRaw,&sourceBlock);err!=nil{return nil,err}
 if sourceBlock.Number!=op.BlockNumber||sourceBlock.Hash!=receipt.BlockHash{
  return nil,fmt.Errorf("indexed receipt is not in canonical finalized block")
 }
 var safeBlockRaw json.RawMessage
 if err=client.Call("eth_getBlockByNumber",&safeBlockRaw,
  fmt.Sprintf("0x%x",head),false);err!=nil{return nil,err}
 var safeBlock ethgo.Block
 if err=json.Unmarshal(safeBlockRaw,&safeBlock);err!=nil{return nil,err}
 if safeBlock.Number!=head||safeBlock.Hash==ethgo.ZeroHash{
  return nil,fmt.Errorf("indexed source finality head unavailable")
 }
 route,err:=getILNRouteAtBlock(client,source,op.DestinationDomain,op.RouteID,op.BlockNumber,true)
 if err!=nil{return nil,fmt.Errorf("indexed route binding unavailable: %w",err)}
 if route.Route.Gateway!=gateway||route.Route.DestinationRouter==types.ZeroAddress||
  route.Route.SourceRouter==types.ZeroAddress||route.Route.Mailbox==types.ZeroAddress{
  return nil,fmt.Errorf("indexed source Registry route/gateway mismatch")
 }
 return &IndexedILNSourceReceipt{
  Version:1,SourceChainID:source.ChainID,
  SourceTxHash:op.TransactionHash.String(),
  SourceBlockNumber:op.BlockNumber,
  SourceBlockHash:receipt.BlockHash.String(),
  SourceRouter:route.Route.SourceRouter.String(),
  DestinationRouter:route.Route.DestinationRouter.String(),
  SourceMailbox:route.Route.Mailbox.String(),
  Transaction:txRaw,Receipt:receiptRaw,
  SourceBlock:sourceBlockRaw,FinalizedHeadBlock:safeBlockRaw,
  VerifiedAs:"FINALIZED_SINGLE_OPERATOR_OBSERVATION_NOT_BLS_EVIDENCE",
 },nil
}

// GetIndexedNativeILNSourceReceipt extracts the same canonical route binding
// from the LOCAL committed IBFT chain for XGR->external routes. It performs
// ZERO foreign RPC reads, and it is not a source witness for any EVM chain.
func GetIndexedNativeILNSourceReceipt(source *Destination,op ILNOperation)(
 *IndexedILNSourceReceipt,error){
 if source==nil||source.ChainID!=1643||op.MessageID==types.ZeroHash||
  op.RouteID==types.ZeroHash||op.BlockNumber==0||
  op.TransactionHash==types.ZeroHash{
  return nil,fmt.Errorf("invalid native XGR source observation")
 }
 reader,err:=source.nativeAccess()
 if err!=nil{return nil,err}
 verified,_,snapshot,err:=VerifyNativeXGROperation(reader,source,
  &CheckpointRoute{RouteID:op.RouteID},op.DestinationDomain,
  op.MessageID,op.BlockNumber)
 if err!=nil{return nil,err}
 if verified.TransactionHash!=op.TransactionHash||
  verified.CanonicalBlockHash==types.ZeroHash{
  return nil,fmt.Errorf("native source receipt identity mismatch")
 }
 return &IndexedILNSourceReceipt{
  Version:1,SourceChainID:1643,SourceTxHash:op.TransactionHash.String(),
  SourceBlockNumber:op.BlockNumber,SourceBlockHash:verified.CanonicalBlockHash.String(),
  SourceRouter:snapshot.Route.SourceRouter.String(),
  DestinationRouter:snapshot.Route.DestinationRouter.String(),
  SourceMailbox:snapshot.Route.Mailbox.String(),
  VerifiedAs:"FINALIZED_LOCAL_IBFT_OBSERVATION_NOT_BLS_EVIDENCE",
 },nil
}
