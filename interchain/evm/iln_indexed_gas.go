package evm

import (
 "encoding/json"
 "fmt"
 "strings"

 "github.com/umbracle/ethgo"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

// Gas is optional operational statistics, never part of settlement or
// an independent BLS witness. At most one exact ProcessId message per window.
type IndexedILNGas struct {
 Version int `json:"version"`
 DestinationChainID uint64 `json:"destinationChainId"`
 Mailbox string `json:"mailbox"`
 MessageID string `json:"messageId"`
 TransactionHash string `json:"transactionHash"`
 BlockNumber uint64 `json:"blockNumber"`
 BlockHash string `json:"blockHash"`
 GasUsed uint64 `json:"gasUsed"`
 EvidenceStatus string `json:"evidenceStatus"`
}

const indexedGasWindow=150
var indexedProcessIDTopic=types.Hash(crypto.Keccak256Hash([]byte("ProcessId(bytes32)")))

// ScanIndexedILNGasWindow searches an EXACT indexed message topic and
// verified Mailbox address, never all destination ProcessId logs on idle.
// The caller persists a monotone backwards window cursor for crash recovery.
func ScanIndexedILNGasWindow(destination *Destination,mailbox types.Address,
 messageID types.Hash,from,to uint64)(*IndexedILNGas,error){
 if destination==nil||destination.ChainID==0||mailbox==types.ZeroAddress||
  messageID==types.ZeroHash||from<1||to<from||to-from>=indexedGasWindow{
  return nil,fmt.Errorf("invalid bounded destination gas window")
 }
 if destination.ChainID==1643{
  reader,err:=destination.nativeAccess()
  if err!=nil{return nil,err}
  head,err:=reader.NativeXGRHead()
  if err!=nil||to>head{return nil,fmt.Errorf("native gas window beyond committed head")}
  var found *IndexedILNGas
  for number:=from;number<=to;number++{
   block,e:=reader.NativeXGRBlock(number)
   if e!=nil||block==nil||block.Number!=number||
    block.Hash==types.ZeroHash||len(block.Transactions)!=len(block.Receipts){
    return nil,fmt.Errorf("native gas block/receipt incomplete")
   }
   for i,receipt:=range block.Receipts{
    if receipt==nil||receipt.Status==nil||block.Transactions[i]==nil{
     return nil,fmt.Errorf("invalid native gas receipt")
    }
    for _,log:=range receipt.Logs{
     if log==nil{return nil,fmt.Errorf("invalid native gas log")}
     if log.Address!=mailbox||len(log.Topics)!=2||
      log.Topics[0]!=indexedProcessIDTopic||log.Topics[1]!=messageID{continue}
     if found!=nil||*receipt.Status!=types.ReceiptSuccess||
      receipt.GasUsed<21000||receipt.GasUsed>5000000 {
      return nil,fmt.Errorf("ambiguous or invalid native ProcessId gas receipt")
     }
     found=&IndexedILNGas{Version:1,DestinationChainID:1643,
      Mailbox:mailbox.String(),MessageID:messageID.String(),
      TransactionHash:block.Transactions[i].Hash.String(),
      BlockNumber:number,BlockHash:block.Hash.String(),
      GasUsed:receipt.GasUsed,
      EvidenceStatus:"FINALIZED_GAS_READ_MODEL_NOT_BLS_EVIDENCE"}
    }
   }
  }
  return found,nil
 }
 client,head,err:=newVerifiedRPCClientWithHead(destination)
 if err!=nil{return nil,err}
 defer client.Close()
 if to>head{return nil,fmt.Errorf("indexed gas window beyond safe head")}
 filter:=map[string]interface{}{
  "address":mailbox.String(),
  "topics":[]string{indexedProcessIDTopic.String(),messageID.String()},
  "fromBlock":fmt.Sprintf("0x%x",from),
  "toBlock":fmt.Sprintf("0x%x",to),
 }
 var logs []json.RawMessage
 if err=client.Call("eth_getLogs",&logs,filter);err!=nil{return nil,err}
 if len(logs)==0{return nil,nil}
 if len(logs)!=1{return nil,fmt.Errorf("ambiguous indexed ProcessId logs")}
 var log struct{
  Address string `json:"address"`
  Topics []string `json:"topics"`
  TransactionHash string `json:"transactionHash"`
  BlockHash string `json:"blockHash"`
  BlockNumber string `json:"blockNumber"`
  Removed bool `json:"removed"`
 }
 if err=json.Unmarshal(logs[0],&log);err!=nil{return nil,err}
 if log.Removed||!strings.EqualFold(log.Address,mailbox.String())||
  len(log.Topics)!=2||
  !strings.EqualFold(log.Topics[0],indexedProcessIDTopic.String())||
  !strings.EqualFold(log.Topics[1],messageID.String()){
  return nil,fmt.Errorf("invalid indexed ProcessId log identity")
 }
 var receipt ethgo.Receipt
 if err=client.Call("eth_getTransactionReceipt",&receipt,log.TransactionHash);err!=nil{return nil,err}
 if receipt.TransactionHash.String()!=log.TransactionHash||
  receipt.Status!=uint64(types.ReceiptSuccess)||
  receipt.BlockHash.String()!=log.BlockHash||
  receipt.GasUsed<21000||receipt.GasUsed>5000000 {
  return nil,fmt.Errorf("ProcessId gas receipt canonical identity mismatch")
 }
 number:=receipt.BlockNumber
 if number<from||number>to||fmt.Sprintf("0x%x",number)!=log.BlockNumber{
  return nil,fmt.Errorf("ProcessId gas receipt outside scanned finalized range")
 }
 block,err:=ilnRPCBlock(client,ethgo.BlockNumber(number),false)
 if err!=nil||block==nil||block.Number!=number||block.Hash!=receipt.BlockHash{
  return nil,fmt.Errorf("ProcessId gas receipt block not canonical")
 }
 return &IndexedILNGas{Version:1,DestinationChainID:destination.ChainID,
  Mailbox:mailbox.String(),MessageID:messageID.String(),
  TransactionHash:receipt.TransactionHash.String(),BlockNumber:number,
  BlockHash:receipt.BlockHash.String(),GasUsed:receipt.GasUsed,
  EvidenceStatus:"FINALIZED_GAS_READ_MODEL_NOT_BLS_EVIDENCE"},nil
}
