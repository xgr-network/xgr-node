package evm

import (
 "fmt"
 "time"

 "github.com/umbracle/ethgo"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

// A read-model delivery observation; it is NEVER a BLS signing witness and
// never authorizes mint/unlock or an on-chain transaction.
type IndexedILNDelivery struct {
 Version int `json:"version"`
 DestinationChainID uint64 `json:"destinationChainId"`
 DestinationRouter string `json:"destinationRouter"`
 Mailbox string `json:"mailbox"`
 MessageID string `json:"messageId"`
 Delivered bool `json:"delivered"`
 FinalizedBlockNumber uint64 `json:"finalizedBlockNumber"`
 FinalizedBlockHash string `json:"finalizedBlockHash"`
 ObservedAt string `json:"observedAt"`
 EvidenceStatus string `json:"evidenceStatus"`
}

func GetIndexedILNDelivery(destination *Destination,router types.Address,messageID types.Hash)(
 *IndexedILNDelivery,error){
 if destination==nil||destination.ChainID==0||router==types.ZeroAddress||
  messageID==types.ZeroHash{return nil,fmt.Errorf("invalid indexed destination")}
 if destination.ChainID==1643{
  native,err:=destination.nativeAccess()
  if err!=nil{return nil,err}
  head,err:=native.NativeXGRHead()
  if err!=nil||head==0{return nil,fmt.Errorf("native committed destination head unavailable")}
  view:=func(to types.Address,data []byte)([]byte,error){return DestinationCall(destination,to,data)}
  mailbox,err:=viewILNAddress(view,router,"mailbox()")
  if err!=nil{return nil,err}
  delivered,err:=GetILNMessageDelivered(destination,router,messageID)
  if err!=nil{return nil,err}
  block,err:=native.NativeXGRBlock(head)
  if err!=nil||block==nil||block.Hash==types.ZeroHash{
   return nil,fmt.Errorf("native committed destination block unavailable")
  }
  return &IndexedILNDelivery{
   Version:1,DestinationChainID:1643,DestinationRouter:router.String(),Mailbox:mailbox.String(),
   MessageID:messageID.String(),Delivered:delivered,
   FinalizedBlockNumber:head,FinalizedBlockHash:block.Hash.String(),
   ObservedAt:time.Now().UTC().Format(time.RFC3339),
   EvidenceStatus:"FINALIZED_READ_MODEL_NOT_BLS_EVIDENCE",
  },nil
 }
 client,head,err:=newVerifiedRPCClientWithHead(destination)
 if err!=nil{return nil,err}
 defer client.Close()
 if head<1{return nil,fmt.Errorf("destination safe head unavailable")}
 block,err:=ilnRPCBlock(client,ethgo.BlockNumber(head),false)
 if err!=nil||block==nil||block.Number!=head||block.Hash==ethgo.ZeroHash{
  return nil,fmt.Errorf("destination safe block unavailable")
 }
 mailbox,err:=callILNAddressGetter(client,router,"mailbox()",ethgo.BlockNumber(head))
 if err!=nil{return nil,err}
 selector:=crypto.Keccak256([]byte("delivered(bytes32)"))
 data:=append(append([]byte(nil),selector[:4]...),messageID.Bytes()...)
 to:=ethgo.Address(mailbox)
 raw,err:=ilnRPCCall(client,&ethgo.CallMsg{To:&to,Data:data},ethgo.BlockNumber(head))
 if err!=nil{return nil,err}
 decoded,err:=decodeRPCHex(raw)
 if err!=nil||len(decoded)!=32{return nil,fmt.Errorf("invalid finalized delivered boolean")}
 for _,b:=range decoded[:31]{if b!=0{return nil,fmt.Errorf("invalid delivered boolean encoding")}}
 if decoded[31]!=0&&decoded[31]!=1{return nil,fmt.Errorf("invalid delivered boolean value")}
 after,err:=ilnRPCBlock(client,ethgo.BlockNumber(head),false)
 if err!=nil||after==nil||after.Number!=head||after.Hash!=block.Hash{
  return nil,fmt.Errorf("destination safe head changed during observation")
 }
 return &IndexedILNDelivery{
  Version:1,DestinationChainID:destination.ChainID,
  DestinationRouter:router.String(),Mailbox:mailbox.String(),
  MessageID:messageID.String(),Delivered:decoded[31]==1,
  FinalizedBlockNumber:head,FinalizedBlockHash:block.Hash.String(),
  ObservedAt:time.Now().UTC().Format(time.RFC3339),
  EvidenceStatus:"FINALIZED_READ_MODEL_NOT_BLS_EVIDENCE",
 },nil
}
