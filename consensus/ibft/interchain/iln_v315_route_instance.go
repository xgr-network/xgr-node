package interchain

import (
    "encoding/binary"
    "fmt"

    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

const (
    XITARouteInstanceDomainV315 = "XITA_ROUTE_INSTANCE_V315"
    XITARouteSafetyDomainV315   = "XITA_ROUTE_SAFETY_V315"
)

// XITAOpenRouteInstanceID matches XGRILNProtocol.routeInstanceIdV315.
// The address pair namespaces independent open routes for the SAME asset;
// no first claimant can reserve a unique assetId or directed chain pair.
func XITAOpenRouteInstanceID(
    assetID types.Hash,
    sourceChainID uint64, sourceDomain uint32,
    destinationChainID uint64, destinationDomain uint32,
    sourceRouter, destinationRouter types.Address,
) (types.Hash, error) {
    if _, err := XITADirectedRouteID(
        assetID, sourceChainID, sourceDomain,
        destinationChainID, destinationDomain,
    ); err != nil {
        return types.ZeroHash, err
    }
    if sourceRouter == types.ZeroAddress || destinationRouter == types.ZeroAddress {
        return types.ZeroHash, fmt.Errorf("open XITA route requires both router addresses")
    }
    raw := make([]byte, 0, 8*32)
    domain := crypto.Keccak256Hash([]byte(XITARouteInstanceDomainV315))
    raw = append(raw, domain.Bytes()...)
    raw = append(raw, assetID.Bytes()...)
    raw = append(raw, abiWordUint64(sourceChainID)...)
    raw = append(raw, abiWordUint32(sourceDomain)...)
    raw = append(raw, abiWordUint64(destinationChainID)...)
    raw = append(raw, abiWordUint32(destinationDomain)...)
    raw = append(raw, xitaABIAddressWord(sourceRouter)...)
    raw = append(raw, xitaABIAddressWord(destinationRouter)...)
    return crypto.Keccak256Hash(raw), nil
}

func xitaABIAddressWord(address types.Address) []byte {
    word := make([]byte, 32)
    copy(word[12:], address.Bytes())
    return word
}

// XITARouteSafetyProofV315 encodes the exact 21-word abi.encode payload
// consumed by XGRILNRegistryV315.confirmRouteInstance on the SOURCE chain.
// This is a TRANSFER SAFETY attestation of objectively observed remote state,
// not a governance vote granting somebody the exclusive right to register.
//
// IMPORTANT: no node may sign this payload without independently checking
// finalized remote Registry, Factory, Router bytecode and metadata, reciprocal
// route bindings and the original ERC20 collateral backing.
type XITARouteSafetyProofV315 struct {
    SourceChainID         uint64
    SourceDomain          uint32
    SourceRegistry        types.Address
    RouteID               types.Hash
    AssetID               types.Hash
    DestinationChainID    uint64
    DestinationDomain     uint32
    SourceToken           types.Address
    DestinationToken      types.Address
    SourceRouter          types.Address
    DestinationRouter     types.Address
    Gateway               types.Address
    ReverseRouteID        types.Hash
    RemoteRegistry        types.Address
    RemoteFactory         types.Address
    RemoteGateway         types.Address
    LocalRouterCodeHash   types.Hash
    RemoteRouterCodeHash  types.Hash
    SetID                 uint64
    ValidUntil            uint64
}

func (p XITARouteSafetyProofV315) Validate() error {
    id, err := XITAOpenRouteInstanceID(
        p.AssetID, p.SourceChainID, p.SourceDomain,
        p.DestinationChainID, p.DestinationDomain,
        p.SourceRouter, p.DestinationRouter,
    )
    if err != nil || id != p.RouteID {
        return fmt.Errorf("invalid XITA open route identity")
    }
    reverse, err := XITAOpenRouteInstanceID(
        p.AssetID, p.DestinationChainID, p.DestinationDomain,
        p.SourceChainID, p.SourceDomain,
        p.DestinationRouter, p.SourceRouter,
    )
    if err != nil || reverse != p.ReverseRouteID {
        return fmt.Errorf("invalid XITA reciprocal route identity")
    }
    if p.SourceRegistry == types.ZeroAddress ||
        p.Gateway == types.ZeroAddress ||
        p.RemoteRegistry == types.ZeroAddress ||
        p.RemoteFactory == types.ZeroAddress ||
        p.RemoteGateway == types.ZeroAddress ||
        p.LocalRouterCodeHash == types.ZeroHash ||
        p.RemoteRouterCodeHash == types.ZeroHash ||
        p.SetID == 0 || p.ValidUntil == 0 {
        return fmt.Errorf("incomplete XITA route safety proof")
    }
    return nil
}

func (p XITARouteSafetyProofV315) MarshalBinary() ([]byte, error) {
    if err := p.Validate(); err != nil {
        return nil, err
    }
    raw := make([]byte, 0, 21*32)
    domain := crypto.Keccak256Hash([]byte(XITARouteSafetyDomainV315))
    raw = append(raw, domain.Bytes()...)
    raw = append(raw, abiWordUint64(p.SourceChainID)...)
    raw = append(raw, abiWordUint32(p.SourceDomain)...)
    raw = append(raw, xitaABIAddressWord(p.SourceRegistry)...)
    raw = append(raw, p.RouteID.Bytes()...)
    raw = append(raw, p.AssetID.Bytes()...)
    raw = append(raw, abiWordUint64(p.DestinationChainID)...)
    raw = append(raw, abiWordUint32(p.DestinationDomain)...)
    raw = append(raw, xitaABIAddressWord(p.SourceToken)...)
    raw = append(raw, xitaABIAddressWord(p.DestinationToken)...)
    raw = append(raw, xitaABIAddressWord(p.SourceRouter)...)
    raw = append(raw, xitaABIAddressWord(p.DestinationRouter)...)
    raw = append(raw, xitaABIAddressWord(p.Gateway)...)
    raw = append(raw, p.ReverseRouteID.Bytes()...)
    raw = append(raw, xitaABIAddressWord(p.RemoteRegistry)...)
    raw = append(raw, xitaABIAddressWord(p.RemoteFactory)...)
    raw = append(raw, xitaABIAddressWord(p.RemoteGateway)...)
    raw = append(raw, p.LocalRouterCodeHash.Bytes()...)
    raw = append(raw, p.RemoteRouterCodeHash.Bytes()...)
    raw = append(raw, abiWordUint64(p.SetID)...)
    raw = append(raw, abiWordUint64(p.ValidUntil)...)
    if len(raw) != 21*32 {
        return nil, fmt.Errorf("unexpected safety proof payload length")
    }
    return raw, nil
}

func (p XITARouteSafetyProofV315) Hash() (types.Hash, error) {
    raw, err := p.MarshalBinary()
    if err != nil {
        return types.ZeroHash, err
    }
    return crypto.Keccak256Hash(raw), nil
}

// SafetyProofHeader returns the signed expiry and quorum set from the
// fixed-width payload without trusting any user-provided field ordering.
func XITASafetyProofHeader(raw []byte) (setID, validUntil uint64, err error) {
    if len(raw) != 21*32 {
        return 0, 0, fmt.Errorf("invalid XITA route safety proof length")
    }
    domain := crypto.Keccak256Hash([]byte(XITARouteSafetyDomainV315))
    if types.BytesToHash(raw[:32]) != domain {
        return 0, 0, fmt.Errorf("invalid XITA route safety proof domain")
    }
    setID = binary.BigEndian.Uint64(raw[19*32+24 : 20*32])
    validUntil = binary.BigEndian.Uint64(raw[20*32+24 : 21*32])
    if setID == 0 || validUntil == 0 {
        return 0, 0, fmt.Errorf("invalid XITA route safety proof quorum header")
    }
    return setID, validUntil, nil
}
