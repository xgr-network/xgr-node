package evm

import (
 "fmt"
 "time"
 "github.com/umbracle/ethgo"
 "github.com/umbracle/ethgo/jsonrpc"
 policy "github.com/xgr-network/xgr-node/interchain"
 "github.com/xgr-network/xgr-node/types"
)

// finalizedILNHead is the authoritative upper bound for ALL source discovery
// and signing reads. Base and Arbitrum require "safe"; no confirmation depth
// or fallback to "latest" is ever permitted. XGRChain is IBFT-committed.
func finalizedILNHead(client *jsonrpc.Client, source *Destination)(uint64,error){
 if client==nil||source==nil{return 0,fmt.Errorf("finality client or chain missing")}
 p,err:=policy.PolicyForChainID(source.ChainID)
 if err!=nil{return 0,err}
 if source.Domain!=p.Domain{return 0,fmt.Errorf("finality source chain/domain mismatch")}
 var block *ethgo.Block
 switch p.Finality{
 case policy.FinalityIBFT: block,err=client.Eth().GetBlockByNumber(ethgo.Latest,false)
 case policy.FinalitySafe: err=client.Call("eth_getBlockByNumber",&block,"safe",false)
 default:return 0,fmt.Errorf("unsupported finality rule for chain %d",source.ChainID)
 }
 if err!=nil{return 0,fmt.Errorf("native %s finality head unavailable: %w",p.Finality,err)}
 if block==nil||block.Number==0||block.Hash==(ethgo.Hash{})||block.Number>uint64(1<<63-1){
  return 0,fmt.Errorf("missing or invalid finality block on chain %d",source.ChainID)
 }
 current,err:=client.Eth().GetBlockByNumber(ethgo.BlockNumber(block.Number),false)
 if err!=nil||current==nil||current.Number!=block.Number||current.Hash!=block.Hash{
  return 0,fmt.Errorf("finality head block hash conflicts with canonical numeric block")
 }
 return block.Number,nil
}

// Source receipts must remain canonical at a finality-qualified block.
func verifyFinalizedReceipt(client *jsonrpc.Client, source *Destination, receipt *ethgo.Receipt) error {
 if receipt==nil||receipt.Status!=uint64(types.ReceiptSuccess)||
   receipt.BlockNumber==0||receipt.BlockHash==(ethgo.Hash{}){
  return fmt.Errorf("invalid source receipt or missing block hash")
 }
 head,err:=finalizedILNHead(client,source)
 if err!=nil{return err}
 if receipt.BlockNumber>head{return fmt.Errorf("source receipt beyond chain finality boundary")}
 if receipt.BlockNumber>uint64(1<<63-1){return fmt.Errorf("invalid receipt block number")}
 current,err:=client.Eth().GetBlockByNumber(ethgo.BlockNumber(receipt.BlockNumber),false)
 if err!=nil||current==nil||current.Number!=receipt.BlockNumber||current.Hash!=receipt.BlockHash{
  return fmt.Errorf("source receipt block hash not canonical")
 }
 return nil
}

// Membership/fee transaction confirmation is not consensus-critical.
func waitForFinality(destination *Destination,receipt *ethgo.Receipt)error {
 if receipt==nil{return fmt.Errorf("missing destination receipt")}
 if err:=destination.ValidateSubmission();err!=nil{return err}
 if err:=VerifyDestinationChainID(destination);err!=nil{return err}
 client,err:=jsonrpc.NewClient(destination.RPCURL)
 if err!=nil{return err}
 defer client.Close()
 deadline:=time.Now().Add(5*time.Minute)
 for {
  fresh,readErr:=client.Eth().GetTransactionReceipt(receipt.TransactionHash)
  if readErr==nil&&fresh!=nil {
   if fresh.BlockHash!=receipt.BlockHash||fresh.BlockNumber!=receipt.BlockNumber||
      fresh.Status!=uint64(types.ReceiptSuccess) {
    return fmt.Errorf("destination receipt changed or reverted before finality")
   }
   if verifyFinalizedReceipt(client,destination,fresh)==nil{return nil}
  }
  if time.Now().After(deadline){
   if readErr!=nil{return fmt.Errorf("destination finality receipt unavailable: %w",readErr)}
   return fmt.Errorf("destination transaction not safe/finalized before deadline")
  }
  time.Sleep(2*time.Second)
 }
}
