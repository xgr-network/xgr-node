package interchain

import (
    "bytes"
    "testing"

    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

func TestXITAOpenRouteInstanceIDsAllowIndependentAssetRoutes(t *testing.T) {
    token := types.StringToAddress("0x0000000000000000000000000000000000000abc")
    asset, err := XITACanonicalAssetID(8453, token, 1)
    if err != nil { t.Fatal(err) }
    localA := types.StringToAddress("0x0000000000000000000000000000000000000111")
    localB := types.StringToAddress("0x0000000000000000000000000000000000000222")
    remote := types.StringToAddress("0x0000000000000000000000000000000000000333")
    routeA, err := XITAOpenRouteInstanceID(asset,8453,8453,1643,1643,localA,remote)
    if err != nil { t.Fatal(err) }
    routeB, err := XITAOpenRouteInstanceID(asset,8453,8453,1643,1643,localB,remote)
    if err != nil { t.Fatal(err) }
    reverse, err := XITAOpenRouteInstanceID(asset,1643,1643,8453,8453,remote,localA)
    if err != nil { t.Fatal(err) }
    if routeA == routeB || routeA == reverse || routeB == reverse {
        t.Fatal("independent route instances must have distinct IDs")
    }
    if _, err := XITAOpenRouteInstanceID(asset,8453,8453,137,137,localA,remote); err == nil {
        t.Fatal("spoke-to-spoke route bypassed XGRChain")
    }
    if _, err := XITAOpenRouteInstanceID(asset,8453,8453,1643,1643,types.ZeroAddress,remote); err == nil {
        t.Fatal("zero router admitted")
    }
}

func TestXITARouteSafetyPayloadMatchesSolidityABISlots(t *testing.T) {
    origin := types.StringToAddress("0x0000000000000000000000000000000000000abc")
    asset, err := XITACanonicalAssetID(8453, origin, 1)
    if err != nil { t.Fatal(err) }
    sourceRouter := types.StringToAddress("0x0000000000000000000000000000000000000111")
    destinationRouter := types.StringToAddress("0x0000000000000000000000000000000000000222")
    id, err := XITAOpenRouteInstanceID(asset,8453,8453,1643,1643,sourceRouter,destinationRouter)
    if err != nil { t.Fatal(err) }
    reverse, err := XITAOpenRouteInstanceID(asset,1643,1643,8453,8453,destinationRouter,sourceRouter)
    if err != nil { t.Fatal(err) }
    sourceRegistry := types.StringToAddress("0x0000000000000000000000000000000000000444")
    p := XITARouteSafetyProofV315{
        SourceChainID:8453, SourceDomain:8453,
        SourceRegistry:sourceRegistry,RouteID:id,AssetID:asset,
        DestinationChainID:1643,DestinationDomain:1643,
        SourceToken:origin,DestinationToken:destinationRouter,
        SourceRouter:sourceRouter,DestinationRouter:destinationRouter,
        Gateway:types.StringToAddress("0x0000000000000000000000000000000000000555"),
        ReverseRouteID:reverse,
        RemoteRegistry:types.StringToAddress("0x0000000000000000000000000000000000000666"),
        RemoteFactory:types.StringToAddress("0x0000000000000000000000000000000000000777"),
        RemoteGateway:types.StringToAddress("0x0000000000000000000000000000000000000888"),
        LocalRouterCodeHash:crypto.Keccak256Hash([]byte("local")),
        RemoteRouterCodeHash:crypto.Keccak256Hash([]byte("remote")),
        SetID:7,ValidUntil:1700000000,
    }
    raw,err := p.MarshalBinary()
    if err != nil { t.Fatal(err) }
    if len(raw) != 21*32 { t.Fatalf("got %d bytes, want 672",len(raw)) }
    domain := crypto.Keccak256Hash([]byte(XITARouteSafetyDomainV315))
    if !bytes.Equal(raw[:32],domain.Bytes()) { t.Fatal("wrong abi slot 0") }
    if !bytes.Equal(raw[32:64],abiWordUint64(8453)) { t.Fatal("wrong source chain slot") }
    if !bytes.Equal(raw[2*32:3*32],abiWordUint32(8453)) { t.Fatal("wrong source domain slot") }
    if !bytes.Equal(raw[3*32:4*32],xitaABIAddressWord(sourceRegistry)) { t.Fatal("wrong source registry slot") }
    if !bytes.Equal(raw[4*32:5*32],id.Bytes()) { t.Fatal("wrong route ID slot") }
    if !bytes.Equal(raw[13*32:14*32],reverse.Bytes()) { t.Fatal("wrong reverse ID slot") }
    if !bytes.Equal(raw[19*32:20*32],abiWordUint64(7)) { t.Fatal("wrong validator set slot") }
    if !bytes.Equal(raw[20*32:21*32],abiWordUint64(1700000000)) { t.Fatal("wrong expiry slot") }
    setID,expiry,err := XITASafetyProofHeader(raw)
    if err != nil || setID != 7 || expiry != 1700000000 { t.Fatal("cannot decode safety header",err) }
    raw[0] ^= 1
    if _,_,err := XITASafetyProofHeader(raw); err == nil { t.Fatal("accepted wrong domain") }
    p.RouteID = types.ZeroHash
    if _,err := p.MarshalBinary(); err == nil { t.Fatal("accepted forged route ID") }
}
