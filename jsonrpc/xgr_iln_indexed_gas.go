package jsonrpc

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strings"

 evm "github.com/xgr-network/xgr-node/interchain/evm"
)

// GetILNIndexedGas returns an already-finalized, archived ProcessId receipt
// for optional statistics. Public reads NEVER trigger foreign-chain RPCs.
func (x *xgrEndpoint) GetILNIndexedGas(source,messageID string)(*evm.IndexedILNGas,error){
 operation,err:=x.readILNIndexedOperation(source,messageID)
 if err!=nil{return nil,err}
 name,err:=normalizeIndexedSource(source);if err!=nil{return nil,err}
 id,err:=normalizedILNMessageID(messageID);if err!=nil{return nil,err}
 if x==nil||x.dataDir==""{return nil,fmt.Errorf("ILN read index unavailable")}
 path:=filepath.Join(x.dataDir,"interchain","iln","observed",name,"gas",id+".json")
 info,err:=os.Lstat(path);if err!=nil{return nil,err}
 if !info.Mode().IsRegular()||info.Size()<1||info.Size()>4096{
  return nil,fmt.Errorf("invalid indexed gas observation")
 }
 raw,err:=os.ReadFile(path);if err!=nil{return nil,err}
 var gas evm.IndexedILNGas
 if err=json.Unmarshal(raw,&gas);err!=nil{return nil,err}
 if gas.Version!=1||gas.MessageID!=id||
  gas.EvidenceStatus!="FINALIZED_GAS_READ_MODEL_NOT_BLS_EVIDENCE"||
  gas.DestinationChainID==0||gas.GasUsed<21000||gas.GasUsed>5000000||
  gas.BlockNumber<1{
  return nil,fmt.Errorf("invalid indexed gas evidence")
 }
 for _,s:=range []string{gas.TransactionHash,gas.BlockHash}{
  if _,err=normalizedILNMessageID(s);err!=nil{return nil,err}
 }
 if len(gas.Mailbox)!=42||!strings.HasPrefix(gas.Mailbox,"0x"){
  return nil,fmt.Errorf("invalid indexed gas mailbox")
 }
 deliveryPath:=filepath.Join(x.dataDir,"interchain","iln","observed",name,"delivery",id+".json")
 dRaw,err:=os.ReadFile(deliveryPath);if err!=nil{return nil,err}
 var d evm.IndexedILNDelivery
 if json.Unmarshal(dRaw,&d)!=nil||!d.Delivered||d.MessageID!=id||
  gas.DestinationChainID!=d.DestinationChainID||
  !strings.EqualFold(gas.Mailbox,d.Mailbox)||
  gas.BlockNumber>d.FinalizedBlockNumber||operation.Destination==""{
  return nil,fmt.Errorf("indexed gas does not match final delivery")
 }
 return &gas,nil
}
