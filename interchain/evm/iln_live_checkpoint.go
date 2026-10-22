package evm

import (
    "fmt"
    "math/big"

    "github.com/umbracle/ethgo"
    "github.com/xgr-network/xgr-node/types"
)

// GetConfirmedILNHookCheckpoint reads the CURRENT Hyperlane Hook root/count.
// It never requests numeric historical RPC state. Callers must independently
// reconstruct this entire Merkle tree, and apply their configured confirmation
// delay to observations before signing or accepting an ILN quorum vote.
func GetConfirmedILNHookCheckpoint(source *Destination, snapshot *ILNRouteSnapshot) (*ConfirmedCheckpoint,error) {
    if source==nil || snapshot==nil || snapshot.Route.MerkleTreeHook==types.ZeroAddress ||
        snapshot.Route.Mailbox==types.ZeroAddress {
        return nil,fmt.Errorf("missing canonical ILN source hook/mailbox")
    }
    client,err:=ilnSourceClient(source)
    if err!=nil{return nil,err}
    mailbox,err:=callILNAddressGetter(client,snapshot.Route.MerkleTreeHook,"mailbox()",ethgo.Latest)
    if err!=nil{return nil,fmt.Errorf("read current Hook mailbox: %w",err)}
    if mailbox!=snapshot.Route.Mailbox{return nil,fmt.Errorf("ILN Hook mailbox differs from canonical registry")}
    count,err:=callILNSignatureView(client,snapshot.Route.MerkleTreeHook,"count()",ethgo.Latest)
    if err!=nil{return nil,err}
    if len(count)!=32{return nil,fmt.Errorf("invalid current Hook count")}
    n:=new(big.Int).SetBytes(count)
    if !n.IsUint64() || n.BitLen()>32{return nil,fmt.Errorf("invalid current Hook leaf count")}
    var root types.Hash
    if n.Sign()>0 {
        data,readErr:=callILNSignatureView(client,snapshot.Route.MerkleTreeHook,"root()",ethgo.Latest)
        if readErr!=nil{return nil,readErr}
        if len(data)!=32{return nil,fmt.Errorf("invalid current Hook root response")}
        root=types.BytesToHash(data)
        if root==types.ZeroHash{return nil,fmt.Errorf("zero current Hook root")}
    }
    // The tip can advance between RPC calls. A second count check refuses
    // torn root/count pairs without any archived eth_call.
    after,err:=callILNSignatureView(client,snapshot.Route.MerkleTreeHook,"count()",ethgo.Latest)
    if err!=nil||len(after)!=32||new(big.Int).SetBytes(after).Cmp(n)!=0{
        return nil,fmt.Errorf("Hook count advanced during checkpoint observation: %v",err)
    }
    observed,err:=confirmedILNHead(client,source)
    if err!=nil{return nil,err}
    if observed==0 || snapshot.BlockNumber>observed {
        return nil,fmt.Errorf("source has no confirmed checkpoint observation")
    }
    if n.Sign()==0{return &ConfirmedCheckpoint{BlockNumber:observed},nil}
    return &ConfirmedCheckpoint{Root:root,Index:uint32(n.Uint64()-1),BlockNumber:observed},nil
}
