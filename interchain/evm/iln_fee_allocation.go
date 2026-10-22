package evm

import (
    "fmt"
    "errors"
    "math/big"

    "github.com/umbracle/ethgo"
    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

// ErrILNFeeNotAllocated is a conclusive non-route leaf, not an RPC outage.
var ErrILNFeeNotAllocated = errors.New("ILN message not allocated by route FeeVault")

// VerifyConfirmedILNFeeAllocation checks the ORIGINAL protocol-level fee
// qualification without replaying historical Gateway logs or confusing the
// optional XITA execution fee with the mandatory ILN validator fee.
//
// The source Gateway is factory/route-bound, owns one immutable route FeeVault,
// and allocates the fee atomically with token movement and Hyperlane dispatch.
// The caller MUST also verify that messageID occurs at the supplied index in
// an independently reconstructed canonical source MerkleTreeHook root.
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
    client, err := ilnSourceClient(source)
    if err != nil { return err }
    confirmed, err := confirmedILNHead(client, source)
    if err != nil { return err }
    if checkpointBlock > confirmed || checkpointBlock > uint64(1<<63-1) {
        return fmt.Errorf("ILN allocation checkpoint is not source confirmed")
    }

    // These addresses are immutable in the verified source contracts; reading
    // their most recent value does not require historical RPC state.
    vault, err := callILNAddressGetter(client, snapshot.Route.Gateway, "feeVault()", ethgo.Latest)
    if err != nil || vault == types.ZeroAddress {
        return fmt.Errorf("ILN route gateway FeeVault unavailable: %v", err)
    }
    gateway, err := callILNAddressGetter(client, vault, "gateway()", ethgo.Latest)
    if err != nil || gateway != snapshot.Route.Gateway {
        return fmt.Errorf("ILN FeeVault immutable gateway does not match source route: %v", err)
    }
    raw, err := callILNSignatureView(client, vault, "routeId()", ethgo.Latest)
    if err != nil || len(raw) != 32 || types.BytesToHash(raw) != snapshot.Route.Key.RouteID {
        return fmt.Errorf("ILN FeeVault routeId mismatch: %v", err)
    }
    raw, err = callILNSignatureView(client, vault, "destinationDomain()", ethgo.Latest)
    if err != nil || len(raw) != 32 ||
        new(big.Int).SetBytes(raw).Cmp(new(big.Int).SetUint64(uint64(snapshot.Route.Key.DestinationDomain))) != 0 {
        return fmt.Errorf("ILN FeeVault destinationDomain mismatch: %v", err)
    }
    governance, err := callILNAddressGetter(client, snapshot.Registry, "governanceRegistry()", ethgo.Latest)
    if err != nil || governance == types.ZeroAddress {
        return fmt.Errorf("ILN source governance registry unavailable: %v", err)
    }
    vaultGovernance, err := callILNAddressGetter(client, vault, "validatorRegistry()", ethgo.Latest)
    if err != nil || vaultGovernance != governance {
        return fmt.Errorf("ILN FeeVault governance authority mismatch: %v", err)
    }

    selector := crypto.Keccak256Hash([]byte("allocatedOperation(bytes32)"))
    data := make([]byte, 4+32)
    copy(data[:4],selector[:4])
    copy(data[4:],messageID.Bytes())
    allocated, err := callILNView(client, vault, data, ethgo.Latest)
    if err != nil || len(allocated) != 32 {
        return fmt.Errorf("ILN allocatedOperation at confirmed checkpoint unavailable: %v", err)
    }
    if new(big.Int).SetBytes(allocated).Cmp(big.NewInt(1)) != 0 {
        return fmt.Errorf("%w at confirmed checkpoint",ErrILNFeeNotAllocated)
    }
    selector = crypto.Keccak256Hash([]byte("allocatedSetId(bytes32)"))
    copy(data[:4],selector[:4])
    set, err := callILNView(client, vault, data, block)
    if err != nil || len(set) != 32 {
        return fmt.Errorf("ILN allocated validator set at checkpoint unavailable: %v", err)
    }
    setID := new(big.Int).SetBytes(set)
    if !setID.IsUint64() || setID.Sign() <= 0 {
        return fmt.Errorf("ILN fee allocation has invalid validator set epoch")
    }
    return nil
}
