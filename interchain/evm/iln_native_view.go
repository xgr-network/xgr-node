package evm

import (
 "fmt"
 "github.com/umbracle/ethgo"
 
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

// ILNView is a read-only EVM call bound to a verified state. It must reject
// reverting contract calls and must never execute state-changing transactions.
type ILNView func(types.Address,[]byte)([]byte,error)

func rpcILNView(client ilnRPCClient) ILNView {
 return func(address types.Address,data []byte)([]byte,error) {
  return callILNView(client,address,data,ethgo.Latest)
 }
}
func viewILNSignature(view ILNView,address types.Address,signature string)([]byte,error){
 if view==nil{return nil,fmt.Errorf("nil ILN view")}
 selector:=crypto.Keccak256([]byte(signature))
 return view(address,selector[:4])
}
func viewILNAddress(view ILNView,address types.Address,signature string)(types.Address,error){
 raw,err:=viewILNSignature(view,address,signature)
 if err!=nil{return types.ZeroAddress,err}
 if len(raw)!=32{return types.ZeroAddress,fmt.Errorf("invalid %s result length %d",signature,len(raw))}
 value:=types.BytesToAddress(raw[12:])
 if value==types.ZeroAddress{return value,fmt.Errorf("zero %s address",signature)}
 return value,nil
}
