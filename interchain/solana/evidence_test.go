package solana

import (
    "crypto/sha256"
    "encoding/binary"
    "strings"
    "testing"
)
func pubkey(v byte)[32]byte{
    var key [32]byte
    key[31]=v
    return key
}
func fixtureCandidate()(Observation,SourceDispatch,FeeReceipt,TokenBalanceMovement,SourceCandidatePolicy){
    p:=SourceCandidatePolicy{
        ProgramID:pubkey(17),
        Router:pubkey(2),RoutePDA:pubkey(3),
        FeeVault:pubkey(4),SourceFeePDA:pubkey(5),
        ValidatorPDA:pubkey(6),NoncePDA:pubkey(7),
        FeeEpochPDA:pubkey(8),FeeReceiptPDA:pubkey(9),
        SourceDispatchPDA:pubkey(10),
        Mint:pubkey(12),TokenProgram:pubkey(14),
        SystemProgram:pubkey(15),RentSysvar:pubkey(16),
        RouteID:pubkey(91),
        CustodyKind:1,TokenDecimals:9,
        ExpectedFeeLamports:10,ExpectedValidatorSet:7,
    }
    d:=SourceDispatch{
        MessageID:pubkey(30),RouteID:p.RouteID,Sender:pubkey(1),
        Recipient:pubkey(33),SourceTokenAccount:pubkey(11),
        SourceMint:p.Mint,TokenVault:pubkey(13),AmountLocal:100,
        AmountCanonical:func()(x [32]byte){
            binary.BigEndian.PutUint64(x[24:],100)
            return x
        }(),
        FeeLamports:10,Nonce:1,SourceSlot:99,
        ValidatorSetID:7,CustodyKind:1,
    }
    f:=FeeReceipt{
        RouteID:d.RouteID,MessageID:d.MessageID,
        ValidatorSetID:7,FeeLamports:10,
    }
    movements:=TokenBalanceMovement{
        SourceBefore:200,SourceAfter:100,
        VaultBefore:0,VaultAfter:100,
        FeeVaultBefore:300,FeeVaultAfter:310,
    }
    accounts:=[][32]byte{
        d.Sender,p.Router,p.RoutePDA,p.FeeVault,p.SourceFeePDA,
        p.ValidatorPDA,p.NoncePDA,p.FeeEpochPDA,
        p.FeeReceiptPDA,p.SourceDispatchPDA,
        d.SourceTokenAccount,p.Mint,d.TokenVault,
        p.TokenProgram,p.SystemProgram,p.RentSysvar,p.ProgramID,
    }
    raw:=[]byte{1}
    raw=append(raw,make([]byte,64)...)
    raw=append(raw,1,0,0,byte(len(accounts)))
    for _,k:=range accounts{raw=append(raw,k[:]...)}
    raw=append(raw,make([]byte,32)...)
    raw=append(raw,1,16,16)
    for i:=byte(0);i<16;i++{raw=append(raw,i)}
    ix:=make([]byte,41)
    ix[0]=bridgeSourceV1Opcode
    copy(ix[1:33],d.Recipient[:])
    binary.LittleEndian.PutUint64(ix[33:],d.AmountLocal)
    raw=append(raw,byte(len(ix)))
    raw=append(raw,ix...)
    o:=Observation{
        Signature:strings.Repeat("1",64),Slot:99,
        RawTransaction:raw,TransactionSHA256:sha256.Sum256(raw),
        IndependentWitnesses:2,
    }
    return o,d,f,movements,p
}
func TestCandidateStructuralRequiresAllPinnedAccountsAndSourceMovement(t *testing.T){
    o,d,f,m,p:=fixtureCandidate()
    if err:=CheckSourceEvidenceCandidate(o,d,f,m,p);err!=nil{
        t.Fatalf("matching proof candidate rejected: %v",err)
    }
    changed:=d;changed.CustodyKind=2
    if CheckSourceEvidenceCandidate(o,changed,f,m,p)==nil{
        t.Fatal("fake burn accepted")
    }
    changed=d;changed.Recipient=[32]byte{1}
    if CheckSourceEvidenceCandidate(o,changed,f,m,p)==nil{
        t.Fatal("foreign recipient accepted")
    }
    wrongFee:=f;wrongFee.FeeLamports++
    if CheckSourceEvidenceCandidate(o,d,wrongFee,m,p)==nil{
        t.Fatal("fee mismatch accepted")
    }
    missing:=p;missing.ExpectedFeeLamports=11
    if CheckSourceEvidenceCandidate(o,d,f,m,missing)==nil{
        t.Fatal("forged fee policy accepted")
    }
    wrongMovement:=m;wrongMovement.VaultAfter=99
    if CheckSourceEvidenceCandidate(o,d,f,wrongMovement,p)==nil{
        t.Fatal("missing locked principal accepted")
    }
    wrongMovement=m;wrongMovement.FeeVaultAfter--
    if CheckSourceEvidenceCandidate(o,d,f,wrongMovement,p)==nil{
        t.Fatal("underpaid source native fee accepted")
    }
    other:=o;other.IndependentWitnesses=1
    if CheckSourceEvidenceCandidate(other,d,f,m,p)==nil{
        t.Fatal("single operator accepted")
    }
    other=o;other.TransactionSHA256=[32]byte{}
    if CheckSourceEvidenceCandidate(other,d,f,m,p)==nil{
        t.Fatal("modified observation accepted")
    }
    other=o;other.RawTransaction=append([]byte(nil),o.RawTransaction...)
    other.RawTransaction[len(other.RawTransaction)-1]^=0x04
    other.TransactionSHA256=sha256.Sum256(other.RawTransaction)
    if CheckSourceEvidenceCandidate(other,d,f,m,p)==nil{
        t.Fatal("tampered bridge amount accepted")
    }
    other=o;other.RawTransaction=append([]byte(nil),o.RawTransaction...)
    other.RawTransaction[66+3+1+3*32] ^= 1
    other.TransactionSHA256=sha256.Sum256(other.RawTransaction)
    if CheckSourceEvidenceCandidate(other,d,f,m,p)==nil{
        t.Fatal("substituted destination program/account accepted")
    }
}
func TestBorshSourceDispatchAndFeeReceiptStrictDecoder(t *testing.T){
    _,d,f,_,_:=fixtureCandidate()
    data:=[]byte{0x44,1}
    for _,x:=range [][32]byte{
        d.MessageID,d.RouteID,d.Sender,d.Recipient,
        d.SourceTokenAccount,d.SourceMint,d.TokenVault,
    }{data=append(data,x[:]...)}
    var number [8]byte
    binary.LittleEndian.PutUint64(number[:],d.AmountLocal)
    data=append(data,number[:]...)
    data=append(data,d.AmountCanonical[:]...)
    for _,value:=range []uint64{d.FeeLamports,d.Nonce,d.SourceSlot,d.ValidatorSetID}{
        binary.LittleEndian.PutUint64(number[:],value)
        data=append(data,number[:]...)
    }
    data=append(data,d.CustodyKind)
    got,err:=DecodeSourceDispatchBorsh(data)
    if err!=nil||got!=d{t.Fatalf("decode dispatch failed: %v",err)}
    if _,err:=DecodeSourceDispatchBorsh(data[:len(data)-1]);err==nil{
        t.Fatal("truncated dispatch accepted")
    }
    receipt:=[]byte{0x4f,1,42}
    receipt=append(receipt,f.RouteID[:]...)
    receipt=append(receipt,f.MessageID[:]...)
    for _,value:=range []uint64{f.ValidatorSetID,f.FeeLamports}{
        binary.LittleEndian.PutUint64(number[:],value)
        receipt=append(receipt,number[:]...)
    }
    fee,err:=DecodeFeeReceiptBorsh(receipt)
    if err!=nil||fee!=f{t.Fatalf("decode fee receipt failed: %v",err)}
    receipt[0]^=1
    if _,err:=DecodeFeeReceiptBorsh(receipt);err==nil{
        t.Fatal("invalid receipt tag accepted")
    }
}
