package evm

import (
    "fmt"

    "github.com/xgr-network/xgr-node/types"
)

// VerifiedILNMessagePrefix derives the immutable Hyperlane Merkle checkpoint
// immediately after leaf index. The ENTIRE supplied tree must first match
// the independently queried source Hook root/count. The deployed ISM's
// sourceBlockNumber is a nonzero signed field; in v3.2.2 it carries the
// deterministic 1-based insertion sequence, NEVER a source block height.
// Actual source finality is verified independently before signing.
func VerifiedILNMessagePrefix(fullReplica ILNReplica, full ILNReplicaCheckpoint, index uint32) (ILNReplica, ILNReplicaCheckpoint, error) {
    if err:=VerifyILNReplica(fullReplica,full);err!=nil {
        return ILNReplica{},ILNReplicaCheckpoint{},err
    }
    if uint64(index)>=full.Count||full.Count>1<<20 {
        return ILNReplica{},ILNReplicaCheckpoint{},fmt.Errorf("%w: insertion index outside Hook root",ErrILNReplicaUnverified)
    }
    leaves:=append([]types.Hash(nil),fullReplica.Leaves[:uint64(index)+1]...)
    tree:=&ilnMerkleAccumulator{}
    for i,id:=range leaves {
        if err:=tree.insert(uint32(i),id);err!=nil {
            return ILNReplica{},ILNReplicaCheckpoint{},err
        }
    }
    sequence:=uint64(index)+1
    checkpoint:=ILNReplicaCheckpoint{
        ChainID:full.ChainID,Hook:full.Hook,Root:tree.root(),
        Count:sequence,BlockNumber:sequence,
    }
    prefix:=ILNReplica{
        ChainID:checkpoint.ChainID,Hook:checkpoint.Hook,
        Root:checkpoint.Root,CheckpointBlock:checkpoint.BlockNumber,
        Leaves:leaves,
    }
    if err:=VerifyILNReplica(prefix,checkpoint);err!=nil {
        return ILNReplica{},ILNReplicaCheckpoint{},err
    }
    return prefix,checkpoint,nil
}
