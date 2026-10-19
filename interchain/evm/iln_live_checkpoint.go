package evm

import (
    "fmt"
    "math/big"

    "github.com/umbracle/ethgo"
    "github.com/xgr-network/xgr-node/types"
)

// GetConfirmedILNHookCheckpoint is the non-archive Hyperlane checkpoint reader.
// It mirrors the confirmed root/count lookup used in xgr-node v3.1.1.
// IMPORTANT: this does NOT prove that an arbitrary message belongs to the root.
// Callers must independently verify a message-specific inclusion proof and the
// fee-qualified Gateway operation before requesting a validator signature.
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
    if confirmed > uint64(1<<63-1) {
        return nil, fmt.Errorf("ILN confirmed block exceeds RPC block range")
    }
    at := ethgo.BlockNumber(confirmed)
    mailbox, err := callILNAddressGetter(client, snapshot.Route.MerkleTreeHook, "mailbox()", at)
    if err != nil { return nil, fmt.Errorf("read confirmed ILN hook mailbox: %w", err) }
    if mailbox != snapshot.Route.Mailbox {
        return nil, fmt.Errorf("confirmed ILN hook mailbox differs from canonical route")
    }
    count, err := callILNSignatureView(client, snapshot.Route.MerkleTreeHook, "count()", at)
    if err != nil { return nil, fmt.Errorf("read confirmed ILN hook count: %w", err) }
    if len(count) != 32 { return nil, fmt.Errorf("noncanonical ILN hook count response") }
    n := new(big.Int).SetBytes(count)
    if n.Sign() == 0 { return &ConfirmedCheckpoint{BlockNumber: confirmed}, nil }
    if n.BitLen() > 32 { return nil, fmt.Errorf("ILN hook count exceeds uint32") }
    root, err := callILNSignatureView(client, snapshot.Route.MerkleTreeHook, "root()", at)
    if err != nil { return nil, fmt.Errorf("read confirmed ILN hook root: %w", err) }
    if len(root) != 32 || types.BytesToHash(root) == types.ZeroHash {
        return nil, fmt.Errorf("noncanonical ILN hook root response")
    }
    return &ConfirmedCheckpoint{Root: types.BytesToHash(root), Index: uint32(n.Uint64()-1), BlockNumber: confirmed}, nil
}
