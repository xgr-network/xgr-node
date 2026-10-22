package evm

import (
    "fmt"
    "math/big"

    "github.com/umbracle/ethgo"
    "github.com/xgr-network/xgr-node/types"
)

// GetConfirmedILNHookCheckpoint observes the current Hook root/count using
// ONLY latest state. It is a content anchor for independently reconstructed
// Merkle leaves, not proof that the entire current tree has finalized.
// Each BLS vote is independently restricted to a confirmed original Gateway
// operation/receipt and its immutable insertion prefix.
func GetConfirmedILNHookCheckpoint(source *Destination,snapshot *ILNRouteSnapshot)(*ConfirmedCheckpoint,error){
    if source==nil||snapshot==nil||snapshot.Route.MerkleTreeHook==types.ZeroAddress||
        snapshot.Route.Mailbox==types.ZeroAddress {
        return nil,fmt.Errorf("missing canonical ILN route hook/mailbox")
    }
    client,err:=ilnSourceClient(source)
    if err!=nil{return nil,err}
    confirmed,err:=confirmedILNHead(client,source)
    if err!=nil{return nil,err}
    if snapshot.BlockNumber==0||snapshot.BlockNumber>confirmed {
        return nil,fmt.Errorf("ILN source route observation is not confirmed")
    }
    before,err:=client.Eth().BlockNumber()
    if err!=nil{return nil,err}
    hook:=snapshot.Route.MerkleTreeHook
    mailbox,err:=callILNAddressGetter(client,hook,"mailbox()",ethgo.Latest)
    if err!=nil{return nil,fmt.Errorf("read latest Hook mailbox: %w",err)}
    if mailbox!=snapshot.Route.Mailbox {
        return nil,fmt.Errorf("Hook mailbox differs from canonical source route")
    }
    read:=func(method string)([]byte,error){
        out,err:=callILNSignatureView(client,hook,method,ethgo.Latest)
        if err!=nil{return nil,err}
        if len(out)!=32{return nil,fmt.Errorf("invalid latest Hook %s response",method)}
        return out,nil
    }
    count,err:=read("count()")
    if err!=nil{return nil,err}
    n:=new(big.Int).SetBytes(count)
    if n.BitLen()>32{return nil,fmt.Errorf("Hook count exceeds uint32")}
    var root types.Hash
    if n.Sign()!=0{
        encoded,readErr:=read("root()")
        if readErr!=nil{return nil,readErr}
        root=types.BytesToHash(encoded)
        if root==types.ZeroHash{return nil,fmt.Errorf("zero latest Hook root")}
    }
    again,err:=read("count()")
    if err!=nil{return nil,err}
    after,err:=client.Eth().BlockNumber()
    if err!=nil{return nil,err}
    if before!=after||new(big.Int).SetBytes(again).Cmp(n)!=0 {
        return nil,fmt.Errorf("Hook updated while observing root/count: retry")
    }
    if n.Sign()==0{return &ConfirmedCheckpoint{BlockNumber:after},nil}
    return &ConfirmedCheckpoint{Root:root,Index:uint32(n.Uint64()-1),BlockNumber:after},nil
}
