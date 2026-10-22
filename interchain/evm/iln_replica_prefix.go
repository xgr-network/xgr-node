package evm

import (
    "fmt"

    "github.com/xgr-network/xgr-node/types"
)

// VerifiedILNMessagePrefix deterministically derives the Merkle checkpoint
// immediately after a message's canonical Hook insertion. The full local tree
// MUST first reconstruct the independently observed source Hook root.
// Later unrelated insertions cannot change this prefix checkpoint.
//
// insertionBlock must come from a confirmed source Gateway transaction whose
// receipt independently proves the SAME message/index; this function never
// treats a peer-provided block or root as canonical source evidence.
func VerifiedILNMessagePrefix(
    replica ILNReplica, full ILNReplicaCheckpoint,
    index uint32, insertionBlock uint64,
) (ILNReplica, ILNReplicaCheckpoint, error) {
    if err := VerifyILNReplica(replica, full); err != nil {
        return ILNReplica{}, ILNReplicaCheckpoint{}, err
    }
    if insertionBlock == 0 || insertionBlock > full.BlockNumber ||
        uint64(index) >= full.Count || full.Count > 1<<20 {
        return ILNReplica{}, ILNReplicaCheckpoint{}, fmt.Errorf("%w: invalid confirmed insertion position", ErrILNReplicaUnverified)
    }
    prefixLeaves := append([]types.Hash(nil), replica.Leaves[:uint64(index)+1]...)
    tree := &ilnMerkleAccumulator{}
    for i, leaf := range prefixLeaves {
        if leaf == types.ZeroHash {
            return ILNReplica{}, ILNReplicaCheckpoint{}, fmt.Errorf("%w: zero message ID", ErrILNReplicaUnverified)
        }
        if err := tree.insert(uint32(i), leaf); err != nil {
            return ILNReplica{}, ILNReplicaCheckpoint{}, err
        }
    }
    prefix := ILNReplicaCheckpoint{
        ChainID: full.ChainID, Hook: full.Hook,
        Count: uint64(len(prefixLeaves)), Root: tree.root(),
        BlockNumber: insertionBlock,
    }
    material := ILNReplica{
        ChainID: prefix.ChainID, Hook: prefix.Hook,
        Root: prefix.Root, CheckpointBlock: insertionBlock,
        Leaves: prefixLeaves,
    }
    if err := VerifyILNReplica(material, prefix); err != nil {
        return ILNReplica{}, ILNReplicaCheckpoint{}, err
    }
    return material, prefix, nil
}
