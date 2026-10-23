package evm

import (
    "fmt"
    "math/big"

    "github.com/umbracle/ethgo"
    "github.com/xgr-network/xgr-node/types"
)

// GetConfirmedILNHookCheckpoint reads only latest Hook state.
// Finality is established separately by locally maturing the verified prefix.
func GetConfirmedILNHookCheckpoint(source *Destination, snapshot *ILNRouteSnapshot) (*ConfirmedCheckpoint, error) {
    if snapshot == nil || snapshot.Route.MerkleTreeHook == types.ZeroAddress ||
        snapshot.Route.Mailbox == types.ZeroAddress {
        return nil, fmt.Errorf("missing canonical ILN route hook/mailbox")
    }
    client, err := ilnSourceClient(source)
    if err != nil { return nil, err }
    confirmed, err := confirmedILNHead(client, source)
    if err != nil { return nil, err }
    if snapshot.BlockNumber == 0 || snapshot.BlockNumber > confirmed {
        return nil, fmt.Errorf("ILN operation block not sufficiently confirmed")
    }
    // Latest is a CONTENT observation, not a claim of source finality.
    // Validators age the observed prefix by source confirmations before BLS.
    before,err:=client.Eth().BlockNumber()
    if err!=nil{return nil,err}
    mailbox,err:=callILNAddressGetter(client,snapshot.Route.MerkleTreeHook,"mailbox()",ethgo.Latest)
    if err!=nil{return nil,fmt.Errorf("read latest Hook mailbox: %w",err)}
    if mailbox!=snapshot.Route.Mailbox{return nil,fmt.Errorf("Hook mailbox differs from canonical route")}
    count,err:=callILNSignatureView(client,snapshot.Route.MerkleTreeHook,"count()",ethgo.Latest)
    if err!=nil||len(count)!=32{return nil,fmt.Errorf("invalid latest Hook count: %v",err)}
    n:=new(big.Int).SetBytes(count)
    if n.BitLen()>32{return nil,fmt.Errorf("ILN Hook count exceeds uint32")}
    var root types.Hash
    if n.Sign()>0 {
        encoded,readErr:=callILNSignatureView(client,snapshot.Route.MerkleTreeHook,"root()",ethgo.Latest)
        if readErr!=nil||len(encoded)!=32||types.BytesToHash(encoded)==types.ZeroHash{
            return nil,fmt.Errorf("invalid latest Hook root: %v",readErr)
        }
        root=types.BytesToHash(encoded)
    }
    again,err:=callILNSignatureView(client,snapshot.Route.MerkleTreeHook,"count()",ethgo.Latest)
    if err!=nil||len(again)!=32{return nil,fmt.Errorf("latest Hook count unavailable: %v",err)}
    if new(big.Int).SetBytes(again).Cmp(n)!=0{
        return nil,fmt.Errorf("Hook count changed during latest observation; retry")
    }
    // Block production alone does not change a Hyperlane Merkle tree.
    // An unchanged count is insufficient across reorgs: independently read
    // root AGAIN and reject any content change, even at the same index.
    if n.Sign()>0 {
        repeated,readErr:=callILNSignatureView(client,snapshot.Route.MerkleTreeHook,"root()",ethgo.Latest)
        if readErr!=nil||len(repeated)!=32||types.BytesToHash(repeated)!=root{
            return nil,fmt.Errorf("Hook root changed during latest observation; retry: %v",readErr)
        }
    }
    after,err:=client.Eth().BlockNumber()
    if err!=nil{return nil,err}
    if after<before {
        return nil,fmt.Errorf("source RPC head regressed during latest Hook observation; retry")
    }
    // Anchor at the LATER observed head: a prefix is never aged from an
    // earlier block that may predate its insertion. The separate validator-
    // local prefix finality gate is unchanged. No historical eth_call.
    
    if n.Sign()==0{return &ConfirmedCheckpoint{BlockNumber:after},nil}
    return &ConfirmedCheckpoint{Root:root,Index:uint32(n.Uint64()-1),BlockNumber:after},nil
}
