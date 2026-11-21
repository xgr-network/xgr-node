package solana

// Independent Solana PDA derivation and XITA source-message recomputation.
// This is read-only candidate validation, NOT a Direct-BLS signing gate.
// The negative curve check implements Solana's canonical compressed Ed25519
// decompression test; noncanonical y and invalid zero-x sign are rejected.
import (
    "bytes"
    "crypto/sha256"
    "encoding/binary"
    "errors"
    "math/big"

    "golang.org/x/crypto/sha3"
)
const pdaMarker="ProgramDerivedAddress"
var (
    edP=new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1),255),big.NewInt(19))
    edOne=big.NewInt(1)
    edD=func()*big.Int{
        a:=new(big.Int).Neg(big.NewInt(121665))
        den:=new(big.Int).ModInverse(big.NewInt(121666),edP)
        return a.Mul(a,den).Mod(a,edP)
    }()
    edLegendreExp=new(big.Int).Rsh(new(big.Int).Sub(
        new(big.Int).Set(edP),big.NewInt(1)),1)
    edDenExponent=new(big.Int).Sub(new(big.Int).Set(edP),big.NewInt(2))
)
func onEd25519Curve(raw [32]byte)bool{
    be:=raw
    sign:=be[31]>>7
    be[31]&=0x7f
    for i,j:=0,31;i<j;i,j=i+1,j-1{be[i],be[j]=be[j],be[i]}
    y:=new(big.Int).SetBytes(be[:])
    if y.Cmp(edP)>=0{return false}
    yy:=new(big.Int).Mul(y,y)
    yy.Mod(yy,edP)
    numerator:=new(big.Int).Sub(yy,edOne)
    numerator.Mod(numerator,edP)
    denominator:=new(big.Int).Mul(edD,yy)
    denominator.Add(denominator,edOne)
    denominator.Mod(denominator,edP)
    if denominator.Sign()==0{return false}
    inv:=new(big.Int).Exp(denominator,edDenExponent,edP)
    square:=numerator.Mul(numerator,inv)
    square.Mod(square,edP)
    if square.Sign()==0{return sign==0}
    legendre:=new(big.Int).Exp(square,edLegendreExp,edP)
    return legendre.Cmp(edOne)==0
}
func deriveXitaPDA(program [32]byte,seeds ...[]byte)([32]byte,error){
    var empty [32]byte
    if program==empty||len(seeds)>16{return empty,errors.New("bad PDA program/seeds")}
    for _,seed:=range seeds{if len(seed)>32{return empty,errors.New("Solana seed too long")}}
    var b bytes.Buffer
    for _,seed:=range seeds{b.Write(seed)}
    for n:=255;n>=0;n--{
        h:=sha256.New()
        h.Write(b.Bytes())
        h.Write([]byte{byte(n)})
        h.Write(program[:])
        h.Write([]byte(pdaMarker))
        var candidate [32]byte
        copy(candidate[:],h.Sum(nil))
        if !onEd25519Curve(candidate){return candidate,nil}
    }
    return empty,errors.New("no off-curve Solana PDA")
}
var solanaMainnetGenesisBytes=[32]byte{
    0x45,0x29,0x69,0x98,0xa6,0xf8,0xe2,0xa7,
    0x84,0xdb,0x5d,0x9f,0x95,0xe1,0x8f,0xc2,
    0x3f,0x70,0x44,0x1a,0x10,0x39,0x44,0x68,
    0x01,0x08,0x98,0x79,0xb0,0x8c,0x7e,0xf0,
}
func canonicalSourceMessageID(d SourceDispatch,program,router [32]byte)[32]byte{
    h:=sha3.NewLegacyKeccak256()
    h.Write([]byte("XITA_SOLANA_SOURCE_V1"))
    h.Write(solanaMainnetGenesisBytes[:])
    h.Write(program[:])
    h.Write(router[:])
    h.Write(d.RouteID[:])
    h.Write(d.Sender[:])
    h.Write(d.Recipient[:])
    var n [8]byte
    for _,v:=range []uint64{
        d.Nonce,d.SourceSlot,
    } {
        binary.BigEndian.PutUint64(n[:],v)
        h.Write(n[:])
    }
    h.Write(d.AmountCanonical[:])
    for _,v:=range []uint64{d.FeeLamports,d.ValidatorSetID}{
        binary.BigEndian.PutUint64(n[:],v)
        h.Write(n[:])
    }
    var result [32]byte
    copy(result[:],h.Sum(nil))
    return result
}
func checkDerivedSourcePDAs(p SourceCandidatePolicy,d SourceDispatch)error{
    if d.MessageID!=canonicalSourceMessageID(d,p.ProgramID,p.Router){
        return errors.New("source message does not match canonical XITA Keccak256")
    }
    type candidate struct{expected [32]byte;seeds [][]byte}
    var dest [4]byte;binary.BigEndian.PutUint32(dest[:],1643)
    var epoch [8]byte;binary.BigEndian.PutUint64(epoch[:],d.ValidatorSetID)
    pdas:=[]candidate{
        {p.RoutePDA,[][]byte{[]byte("xita-route"),p.Router[:],dest[:]}},
        {p.FeeVault,[][]byte{[]byte("xita-fee-vault"),p.RoutePDA[:]}},
        {p.SourceFeePDA,[][]byte{[]byte("xita-source-fee")}},
        {p.ValidatorPDA,[][]byte{[]byte("xita-trusted-xgr-validators")}},
        {p.NoncePDA,[][]byte{[]byte("xita-source-nonce"),p.RoutePDA[:],d.Sender[:]}},
        {p.FeeEpochPDA,[][]byte{[]byte("xita-fee-epoch"),p.FeeVault[:],epoch[:]}},
        {p.FeeReceiptPDA,[][]byte{[]byte("xita-fee-receipt"),p.FeeVault[:],d.MessageID[:]}},
        {p.SourceDispatchPDA,[][]byte{[]byte("xita-source-dispatch"),d.MessageID[:]}},
    }
    if d.CustodyKind==1 {
        pdas=append(pdas,candidate{
            d.TokenVault,[][]byte{[]byte("xita-collateral-vault"),p.Router[:]},
        })
    }
    for _,item:=range pdas {
        computed,err:=deriveXitaPDA(p.ProgramID,item.seeds...)
        if err!=nil||computed!=item.expected{
            return errors.New("noncanonical Solana XITA program PDA")
        }
    }
    return nil
}
