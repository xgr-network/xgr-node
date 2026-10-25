package evm

import (
    "fmt"
    "errors"
    "math/big"

    
    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

// ErrILNFeeNotAllocated means there is no fee-paid source operation, not an RPC outage.
var ErrILNFeeNotAllocated = errors.New("ILN message not allocated by route FeeVault")

// VerifyConfirmedILNFeeAllocation checks the ORIGINAL protocol-level fee
// qualification without replaying historical Gateway logs or confusing the
// optional XITA execution fee with the mandatory ILN validator fee.
//
// The source Gateway is factory/route-bound, owns one immutable route FeeVault,
// and allocates the fee atomically with token movement and Hyperlane dispatch.
// The caller MUST also verify the original successful Gateway operation
// and same-transaction canonical Mailbox Dispatch before BLS signing.
//
// No historical fee amount is inferred: the V315 registry fee may change. The
// FeeVault records the original allocated message and recipient epoch rather
// than an arbitrary fee proposed by a proof producer.
func VerifyConfirmedILNFeeAllocation(
    source *Destination,
    snapshot *ILNRouteSnapshot,
    messageID types.Hash,
    checkpointBlock uint64,
) error {
    if source == nil || snapshot == nil || messageID == types.ZeroHash ||
        checkpointBlock == 0 || snapshot.Registry == types.ZeroAddress ||
        snapshot.Route.Gateway == types.ZeroAddress ||
        snapshot.Route.Key.RouteID == types.ZeroHash ||
        snapshot.Route.Key.DestinationDomain == 0 ||
        snapshot.Route.Key.SourceChainID != source.ChainID ||
        snapshot.Route.Key.SourceDomain != source.Domain {
        return fmt.Errorf("invalid source ILN fee-allocation proof identity")
    }
    client, head, err := newVerifiedRPCClientWithHead(source)
    if err != nil { return err }
    defer client.Close()
    return verifyConfirmedILNFeeAllocationOnClient(client, source, snapshot, messageID, checkpointBlock, head)
}

// verifyConfirmedILNFeeAllocationOnClient stays pinned to the exact provider
// already selected and finality-verified by the independent source witness.
func verifyConfirmedILNFeeAllocationOnClient(
    client ilnRPCClient, source *Destination, snapshot *ILNRouteSnapshot,
    messageID types.Hash, checkpointBlock, confirmed uint64,
) error {
    return verifyFeeAllocationWithView(rpcILNView(client),source,snapshot,messageID,checkpointBlock,confirmed)
}

func verifyFeeAllocationWithView(view ILNView, source *Destination, snapshot *ILNRouteSnapshot,
    messageID types.Hash, checkpointBlock, confirmed uint64) error {
    if view==nil{return fmt.Errorf("nil fee-allocation reader")}
    if source == nil || snapshot == nil || messageID == types.ZeroHash ||
        checkpointBlock == 0 || snapshot.Registry == types.ZeroAddress ||
        snapshot.Route.Gateway == types.ZeroAddress ||
        snapshot.Route.Key.RouteID == types.ZeroHash ||
        snapshot.Route.Key.DestinationDomain == 0 ||
        snapshot.Route.Key.SourceChainID != source.ChainID ||
        snapshot.Route.Key.SourceDomain != source.Domain {
        return fmt.Errorf("invalid source ILN fee-allocation proof identity")
    }
    if checkpointBlock > uint64(1<<63-1) || checkpointBlock>confirmed {
        return fmt.Errorf("ILN allocation source observation exceeds current head")
    }

    // These addresses are immutable in the verified source contracts; reading
    // their most recent value does not require historical RPC state.
    vault, err := viewILNAddress(view, snapshot.Route.Gateway, "feeVault()")
    if err != nil || vault == types.ZeroAddress {
        return fmt.Errorf("ILN route gateway FeeVault unavailable: %v", err)
    }
    gateway, err := viewILNAddress(view, vault, "gateway()")
    if err != nil || gateway != snapshot.Route.Gateway {
        return fmt.Errorf("ILN FeeVault immutable gateway does not match source route: %v", err)
    }
    raw, err := viewILNSignature(view, vault, "routeId()")
    if err != nil || len(raw) != 32 || types.BytesToHash(raw) != snapshot.Route.Key.RouteID {
        return fmt.Errorf("ILN FeeVault routeId mismatch: %v", err)
    }
    raw, err = viewILNSignature(view, vault, "destinationDomain()")
    if err != nil || len(raw) != 32 ||
        new(big.Int).SetBytes(raw).Cmp(new(big.Int).SetUint64(uint64(snapshot.Route.Key.DestinationDomain))) != 0 {
        return fmt.Errorf("ILN FeeVault destinationDomain mismatch: %v", err)
    }
    governance, err := viewILNAddress(view, snapshot.Registry, "governanceRegistry()")
    if err != nil || governance == types.ZeroAddress {
        return fmt.Errorf("ILN source governance registry unavailable: %v", err)
    }
    vaultGovernance, err := viewILNAddress(view, vault, "validatorRegistry()")
    if err != nil || vaultGovernance != governance {
        return fmt.Errorf("ILN FeeVault governance authority mismatch: %v", err)
    }

    selector := crypto.Keccak256Hash([]byte("allocatedOperation(bytes32)"))
    data := make([]byte, 4+32)
    copy(data[:4],selector[:4])
    copy(data[4:],messageID.Bytes())
    allocated, err := view(vault, data)
    if err != nil || len(allocated) != 32 {
        return fmt.Errorf("ILN allocatedOperation at confirmed checkpoint unavailable: %v", err)
    }
    if new(big.Int).SetBytes(allocated).Cmp(big.NewInt(1)) != 0 {
        return fmt.Errorf("%w at confirmed checkpoint",ErrILNFeeNotAllocated)
    }
    selector = crypto.Keccak256Hash([]byte("allocatedSetId(bytes32)"))
    copy(data[:4],selector[:4])
    set, err := view(vault, data)
    if err != nil || len(set) != 32 {
        return fmt.Errorf("ILN allocated validator set at checkpoint unavailable: %v", err)
    }
    setID := new(big.Int).SetBytes(set)
    if !setID.IsUint64() || setID.Sign() <= 0 {
        return fmt.Errorf("ILN fee allocation has invalid validator set epoch")
    }
    return nil
}
