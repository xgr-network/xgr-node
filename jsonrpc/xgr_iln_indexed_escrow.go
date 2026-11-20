package jsonrpc

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "regexp"
 "time"

 evm "github.com/xgr-network/xgr-node/interchain/evm"
)
var indexedEscrowAssetParam=regexp.MustCompile(`^[A-Z0-9_]{1,32}$`)

// GetILNIndexedEscrow only returns an already captured finality-bound
// operational snapshot. No externally supplied contract address or RPC URL
// and no on-demand foreign-chain query can reach this JSON-RPC method.
func (x *xgrEndpoint) GetILNIndexedEscrow(asset string)(*evm.IndexedILNEscrowSnapshot,error){
 if !indexedEscrowAssetParam.MatchString(asset)||x==nil||x.dataDir==""{
  return nil,fmt.Errorf("invalid indexed escrow asset")
 }
 path:=filepath.Join(x.dataDir,"interchain","iln","indexed-escrows",asset+".json")
 info,err:=os.Lstat(path);if err!=nil{return nil,err}
 if !info.Mode().IsRegular()||info.Size()<=0||info.Size()>512<<10{
  return nil,fmt.Errorf("invalid indexed escrow snapshot")
 }
 raw,err:=os.ReadFile(path);if err!=nil{return nil,err}
 var item evm.IndexedILNEscrowSnapshot
 if err=json.Unmarshal(raw,&item);err!=nil{return nil,err}
 if item.Version!=1||item.Asset!=asset||
  item.EvidenceStatus!="FINALIZED_ESCROW_READ_MODEL_NOT_BLS_EVIDENCE"||
  item.Chain==""||item.ChainID==0||item.Router==""||
  item.FinalizedHeadBlock==0||item.FinalizedHeadHash==""||
  item.RouterCode==""{
  return nil,fmt.Errorf("invalid escrow index identity")
 }
 observed,err:=time.Parse(time.RFC3339,item.ObservedAt)
 if err!=nil||time.Since(observed)>2*time.Minute||time.Until(observed)>30*time.Second{
  return nil,fmt.Errorf("indexed escrow evidence stale")
 }
 if item.Kind=="erc20"&&(item.Token==""||item.RouterToken==""||
  item.TokenCode==""||item.TokenDecimals==""||item.TokenBalance==""){
  return nil,fmt.Errorf("incomplete ERC20 escrow snapshot")
 }
 return &item,nil
}
