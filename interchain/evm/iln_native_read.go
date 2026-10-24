package evm

import (
    "fmt"
    "github.com/umbracle/ethgo"
    "github.com/umbracle/ethgo/jsonrpc"
    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

// ilnReadCall provides exactly the same canonical contract-view semantics
// for native IBFT state and remote EVM RPC, without coupling the verifier
// to transport. Native calls must execute against a committed state root.
type ilnReadCall func(types.Address,[]byte,ethgo.BlockNumber)([]byte,error)

func rpcILNRead(client *jsonrpc.Client) ilnReadCall {
    return func(to types.Address,data []byte,block ethgo.BlockNumber)([]byte,error){
        if client==nil{return nil,fmt.Errorf("nil ILN RPC reader")}
        return callILNView(client,to,data,block)
    }
}

func readILNSignature(read ilnReadCall,to types.Address,signature string,block ethgo.BlockNumber)([]byte,error){
    if read==nil{return nil,fmt.Errorf("missing ILN reader")}
    selector:=crypto.Keccak256([]byte(signature))
    return read(to,selector[:4],block)
}

func readILNAddress(read ilnReadCall,to types.Address,signature string,block ethgo.BlockNumber)(types.Address,error){
    raw,err:=readILNSignature(read,to,signature,block)
    if err!=nil{return types.ZeroAddress,err}
    if len(raw)!=32{return types.ZeroAddress,fmt.Errorf("unexpected %s response length %d",signature,len(raw))}
    out:=types.BytesToAddress(raw[12:])
    if out==types.ZeroAddress{return out,fmt.Errorf("%s returned zero address",signature)}
    return out,nil
}
