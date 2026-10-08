package evm

import (
 "fmt"
 "math/big"

 "github.com/umbracle/ethgo"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

// ILNRouteAdded is emitted only by the quorum-governed source registry.
type ILNRouteAdded struct {
 DestinationDomain uint32
 RouteID types.Hash
 Gateway types.Address
 BlockNumber uint64
}

// GetILNRegistryActivationBlock establishes a bounded, authoritative event
// backfill start. New registries expose deployment block immutably. Existing
// registries may use an operator-configured exact deployment height. Neither
// path grants route mutation rights; every event still comes from the registry.
func GetILNRegistryActivationBlock(source *Destination) (uint64,error) {
 client,err:=ilnSourceClient(source)
 if err!=nil{return 0,err}
 confirmed,err:=confirmedILNHead(client,source)
 if err!=nil{return 0,err}
 registry:=types.StringToAddress(source.ILNRegistryAddress)
 raw,callErr:=callILNSignatureView(client,registry,"activationBlock()",ethgo.BlockNumber(confirmed))
 if callErr==nil&&len(raw)==32{
  block:=new(big.Int).SetBytes(raw)
  if !block.IsUint64()||block.Sign()<=0||block.Uint64()>confirmed{
   return 0,fmt.Errorf("invalid or unconfirmed ILN registry activation block")
  }
  if source.ILNRegistryActivationBlock!=0&&source.ILNRegistryActivationBlock!=block.Uint64(){
   return 0,fmt.Errorf("configured ILN registry deployment block conflicts with on-chain activationBlock")
  }
  return block.Uint64(),nil
 }
 if source.ILNRegistryActivationBlock==0 {
  return 0,fmt.Errorf("registry activationBlock() unavailable: supply an exact ILN_REGISTRY_ACTIVATION_BLOCK for legacy registry")
 }
 if source.ILNRegistryActivationBlock>confirmed {
  return 0,fmt.Errorf("configured ILN registry activation block is not yet confirmed")
 }
 return source.ILNRegistryActivationBlock,nil
}

// GetConfirmedILNRouteAdds retrieves bounded, source-confirmed registry
// RouteAdded events. Every discovered route MUST additionally pass a canonical
// getRoute read and Gateway binding before the worker activates it.
func GetConfirmedILNRouteAdds(source *Destination, fromBlock, toBlock uint64) ([]ILNRouteAdded,error) {
 client,err:=ilnSourceClient(source)
 if err!=nil{return nil,err}
 confirmed,err:=confirmedILNHead(client,source)
 if err!=nil{return nil,err}
 if fromBlock==0||toBlock<fromBlock||toBlock>confirmed||toBlock-fromBlock+1>maxILNLogRange {
  return nil,fmt.Errorf("invalid confirmed ILN discovery log range")
 }
 registry:=types.StringToAddress(source.ILNRegistryAddress)
 topic:=ethgo.Hash(crypto.Keccak256Hash([]byte("RouteAdded(uint32,bytes32,address,address,address,uint256,uint64)")))
 filter:=&ethgo.LogFilter{Address:[]ethgo.Address{ethgo.Address(registry)},Topics:[][]*ethgo.Hash{{&topic}}}
 filter.SetFromUint64(fromBlock)
 filter.SetToUint64(toBlock)
 logs,err:=client.Eth().GetLogs(filter)
 if err!=nil{return nil,fmt.Errorf("read ILN registry RouteAdded events: %w",err)}
 result:=make([]ILNRouteAdded,0,len(logs))
 for _,log:=range logs {
  if log==nil||log.Removed||len(log.Topics)!=4||log.Topics[0]!=topic||types.Address(log.Address)!=registry {return nil,fmt.Errorf("invalid ILN RouteAdded log")}
  domain:=new(big.Int).SetBytes(log.Topics[1][:])
  if !domain.IsUint64()||domain.Sign()<=0||domain.Uint64()>uint64(^uint32(0)){return nil,fmt.Errorf("invalid ILN discovered destination domain")}
  routeID:=types.Hash(log.Topics[2]);gateway:=types.BytesToAddress(log.Topics[3][12:])
  if routeID==types.ZeroHash||gateway==types.ZeroAddress||log.BlockNumber==0{return nil,fmt.Errorf("invalid discovered ILN route identity")}
  result=append(result,ILNRouteAdded{DestinationDomain:uint32(domain.Uint64()),RouteID:routeID,Gateway:gateway,BlockNumber:log.BlockNumber})
 }
 return result,nil
}
