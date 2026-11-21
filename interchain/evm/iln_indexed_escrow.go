package evm

import (
 "fmt"
 "strings"
 "time"

 "github.com/umbracle/ethgo"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

// Operational escrow probes are local-policy read models. They only return
// raw, height-pinned RPC observations. XITA must independently verify contract
// code hashes, token identity and lock/unlock accounting before quoting TVL.
type IndexedILNEscrowWatch struct{
 Asset string `json:"asset"`
 Chain string `json:"chain"`
 Kind string `json:"kind"`
 Router string `json:"router"`
 Token string `json:"token,omitempty"`
}
type IndexedILNEscrowSnapshot struct{
 Version int `json:"version"`
 Asset string `json:"asset"`
 Chain string `json:"chain"`
 ChainID uint64 `json:"chainId"`
 Kind string `json:"kind"`
 Router string `json:"router"`
 Token string `json:"token,omitempty"`
 FinalizedHeadBlock uint64 `json:"finalizedHeadBlock"`
 LatestHeadBlock uint64 `json:"latestHeadBlock"`
 FinalizedHeadHash string `json:"finalizedHeadHash"`
 ObservedAt string `json:"observedAt"`
 RouterCode string `json:"routerCode"`
 RouterBalance string `json:"routerBalance,omitempty"`
 PendingBalance string `json:"pendingBalance,omitempty"`
 RouterToken string `json:"routerToken,omitempty"`
 TokenCode string `json:"tokenCode,omitempty"`
 TokenDecimals string `json:"tokenDecimals,omitempty"`
 TokenBalance string `json:"tokenBalance,omitempty"`
 EvidenceStatus string `json:"evidenceStatus"`
}

func indexedILNCall(client ilnRPCClient,addr types.Address,sig string,extra []byte,head uint64)(string,error){
 hash:=crypto.Keccak256([]byte(sig))
 data:=append(append([]byte(nil),hash[:4]...),extra...)
 to:=ethgo.Address(addr)
 return ilnRPCCall(client,&ethgo.CallMsg{To:&to,Data:data},ethgo.BlockNumber(head))
}

// GetIndexedILNEscrowSnapshot is a SINGLE foreign-chain operational snapshot.
// Validator and BLS code never consume it. It rejects native XGR (which must
// be read from the same node's committed IBFT store, not external RPC).
func GetIndexedILNEscrowSnapshot(chain *Destination,watch IndexedILNEscrowWatch)(
 *IndexedILNEscrowSnapshot,error){
 if chain==nil||chain.ChainID==0||chain.ChainID==1643||
  watch.Chain!=chain.Name||watch.Asset==""||
  types.IsValidAddress(watch.Router)!=nil||
  types.StringToAddress(watch.Router)==types.ZeroAddress{
  return nil,fmt.Errorf("invalid escrow watch or source chain")
 }
 if watch.Kind!="native"&&watch.Kind!="erc20"{
  return nil,fmt.Errorf("invalid escrow asset kind")
 }
 if watch.Kind=="native"&&watch.Token!=""{
  return nil,fmt.Errorf("native escrow must not have token address")
 }
 if watch.Kind=="erc20"&&(types.IsValidAddress(watch.Token)!=nil||
  types.StringToAddress(watch.Token)==types.ZeroAddress){
  return nil,fmt.Errorf("invalid escrow ERC20 token address")
 }
 client,head,err:=newVerifiedRPCClientWithHead(chain)
 if err!=nil{return nil,err}
 defer client.Close()
 if head<1{return nil,fmt.Errorf("no finalized escrow head")}
 block,err:=ilnRPCBlock(client,ethgo.BlockNumber(head),false)
 if err!=nil||block==nil||block.Hash==ethgo.ZeroHash||block.Number!=head{
  return nil,fmt.Errorf("escrow finality head unavailable")
 }
 // The XITA collateral reconciler separately checks its historical cursor
 // against the ordinary live head minus protocol confirmations. Do not feed
 // the finalized height AS the live height or confirmations get subtracted
 // twice. All contract balances themselves stay pinned to the safe block.
 var latestHex string
 if err=client.Call("eth_blockNumber",&latestHex);err!=nil{return nil,err}
 latest,err:=ilnRPCHexUint(latestHex)
 if err!=nil||latest<head{return nil,fmt.Errorf("invalid latest escrow head")}
 tag:=fmt.Sprintf("0x%x",head)
 router:=types.StringToAddress(watch.Router)
 var routerCode string
 if err=client.Call("eth_getCode",&routerCode,router.String(),tag);err!=nil{return nil,err}
 if len(routerCode)<4||!strings.HasPrefix(routerCode,"0x"){
  return nil,fmt.Errorf("missing canonical escrow router code")
 }
 out:=&IndexedILNEscrowSnapshot{Version:1,Asset:watch.Asset,
  Chain:watch.Chain,ChainID:chain.ChainID,Kind:watch.Kind,
  Router:router.String(),Token:watch.Token,
  FinalizedHeadBlock:head,LatestHeadBlock:latest,FinalizedHeadHash:block.Hash.String(),
  ObservedAt:time.Now().UTC().Format(time.RFC3339),
  RouterCode:routerCode,
  EvidenceStatus:"FINALIZED_ESCROW_READ_MODEL_NOT_BLS_EVIDENCE",
 }
 if watch.Kind=="native"{
  var balance string
  if err=client.Call("eth_getBalance",&balance,router.String(),tag);err!=nil{return nil,err}
  pending,e:=indexedILNCall(client,router,"totalPendingNativeXGR()",nil,head)
  if e!=nil{return nil,e}
  out.RouterBalance=balance
  out.PendingBalance=pending
 }else{
  token:=types.StringToAddress(watch.Token)
  address,e:=indexedILNCall(client,router,"token()",nil,head)
  if e!=nil{return nil,e}
  var code string
  if err=client.Call("eth_getCode",&code,token.String(),tag);err!=nil{return nil,err}
  decimals,e:=indexedILNCall(client,token,"decimals()",nil,head)
  if e!=nil{return nil,e}
  // balanceOf(router), NOT balanceOf(token).
  word:=make([]byte,32)
  copy(word[12:],router.Bytes())
  balance,e:=indexedILNCall(client,token,"balanceOf(address)",word,head)
  if e!=nil{return nil,e}
  out.RouterToken=address
  out.TokenCode=code
  out.TokenDecimals=decimals
  out.TokenBalance=balance
 }
 after,err:=ilnRPCBlock(client,ethgo.BlockNumber(head),false)
 if err!=nil||after==nil||after.Hash!=block.Hash||after.Number!=head{
  return nil,fmt.Errorf("reorganized indexed escrow snapshot")
 }
 return out,nil
}
