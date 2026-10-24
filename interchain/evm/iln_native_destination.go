package evm

import (
 "fmt"
 "math/big"

 "github.com/umbracle/ethgo"
 "github.com/xgr-network/xgr-node/types"
)

// NativeXGRAccess is implemented ONLY by the locally running IBFT node.
// It does not make HTTP or JSON-RPC requests and cannot be supplied by peers.
// State-changing calls must be signed by the caller and admitted to the
// node's regular transaction pool; no consensus path is modified.
type NativeXGRAccess interface {
 NativeXGRReader
 NativeXGRRead(address types.Address, calldata []byte)([]byte,error)
 NativeXGRBalance(address types.Address)(*big.Int,error)
 NativeXGREstimateCost(sender,recipient types.Address,calldata []byte,value *big.Int)(*big.Int,error)
 NativeXGRSubmit(key ethgo.Key,recipient types.Address,calldata []byte,value *big.Int)(*ethgo.Receipt,error)
 NativeXGRVerifyReceipt(*ethgo.Receipt) error
}

func (d *Destination) nativeAccess()(NativeXGRAccess,error){
 if d==nil||d.ChainID!=1643||d.Domain!=1643 {
  return nil,fmt.Errorf("not a native XGR destination")
 }
 if d.NativeXGR==nil{
  return nil,fmt.Errorf("native XGR IBFT backend unavailable")
 }
 return d.NativeXGR,nil
}

func (d *Destination) nativeView()(ILNView,error){
 backend,err:=d.nativeAccess()
 if err!=nil{return nil,err}
 return backend.NativeXGRRead,nil
}

// DestinationCall is the canonical read-only destination contract entrypoint.
// XGR reads the validator's local state; L2 reads retain verified RPC failover.
func DestinationCall(d *Destination, address types.Address, calldata []byte)([]byte,error){
 if d==nil||address==types.ZeroAddress||len(calldata)<4 {
  return nil,fmt.Errorf("invalid destination EVM view")
 }
 if d.ChainID==1643 {
  reader,err:=d.nativeAccess()
  if err!=nil{return nil,err}
  return reader.NativeXGRRead(address,calldata)
 }
 if err:=d.ValidateMembershipRead();err!=nil{return nil,err}
 client,err:=newVerifiedRPCClient(d)
 if err!=nil{return nil,err}
 defer client.Close()
 raw,err:=callILNView(client,address,calldata,ethgo.Latest)
 if err!=nil{
  recordInterchainRPCReadError(d,d.RPCURL,err)
  return nil,err
 }
 return raw,nil
}

// DestinationSubmit routes XGR transactions directly into the local TxPool.
// This never automatically resubmits a possibly accepted transaction.
func DestinationSubmit(d *Destination,key ethgo.Key,to types.Address,data []byte,value *big.Int)(*ethgo.Receipt,error){
 if d==nil||d.ChainID!=1643{return nil,fmt.Errorf("native XGR submission requires chain 1643")}
 if to==types.ZeroAddress||key==nil||len(data)<4||value==nil||value.Sign()<0{
  return nil,fmt.Errorf("invalid XGR submission")
 }
 backend,err:=d.nativeAccess()
 if err!=nil{return nil,err}
 receipt,err:=backend.NativeXGRSubmit(key,to,data,value)
 if err!=nil{return nil,err}
 if receipt==nil||receipt.Status!=uint64(types.ReceiptSuccess){
  return nil,fmt.Errorf("native XGR transaction failed or has no final receipt")
 }
 if err:=backend.NativeXGRVerifyReceipt(receipt);err!=nil{return nil,err}
 return receipt,nil
}
