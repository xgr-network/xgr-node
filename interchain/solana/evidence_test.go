package solana

import (
    "crypto/sha256"
    "fmt"
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
        Mint:pubkey(12),
        TokenProgram:func()[32]byte{
            k,e:=decodeBase58Pubkey("TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA")
            if e!=nil{panic(e)}
            return k
        }(),
        SystemProgram:[32]byte{},
        RentSysvar:func()[32]byte{
            k,e:=decodeBase58Pubkey("SysvarRent111111111111111111111111111111111")
            if e!=nil{panic(e)}
            return k
        }(),
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
    // Derive all canonical PDAs from the published Solana Rust seeds,
    // never trust user-supplied program-owned account addresses.
    var dom [4]byte
    binary.BigEndian.PutUint32(dom[:],1643)
    var epoch [8]byte
    binary.BigEndian.PutUint64(epoch[:],d.ValidatorSetID)
    must:=func(parts ...[]byte)[32]byte{
        x,e:=deriveXitaPDA(p.ProgramID,parts...)
        if e!=nil{panic(e)}
        return x
    }
    p.RoutePDA=must([]byte("xita-route"),p.Router[:],dom[:])
    p.FeeVault=must([]byte("xita-fee-vault"),p.RoutePDA[:])
    p.SourceFeePDA=must([]byte("xita-source-fee"))
    p.ValidatorPDA=must([]byte("xita-trusted-xgr-validators"))
    p.NoncePDA=must([]byte("xita-source-nonce"),p.RoutePDA[:],d.Sender[:])
    p.FeeEpochPDA=must([]byte("xita-fee-epoch"),p.FeeVault[:],epoch[:])
    d.TokenVault=must([]byte("xita-collateral-vault"),p.Router[:])
    d.MessageID=canonicalSourceMessageID(d,p.ProgramID,p.Router)
    p.FeeReceiptPDA=must([]byte("xita-fee-receipt"),p.FeeVault[:],d.MessageID[:])
    p.SourceDispatchPDA=must([]byte("xita-source-dispatch"),d.MessageID[:])
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
    wrongPDA:=p;wrongPDA.SourceFeePDA=pubkey(88)
    if CheckSourceEvidenceCandidate(o,d,f,m,wrongPDA)==nil{
        t.Fatal("forged source fee PDA accepted")
    }
    wrongPDA=p;wrongPDA.FeeReceiptPDA=pubkey(77)
    if CheckSourceEvidenceCandidate(o,d,f,m,wrongPDA)==nil{
        t.Fatal("forged fee receipt PDA accepted")
    }
    wrongID:=d;wrongID.MessageID=pubkey(70)
    if CheckSourceEvidenceCandidate(o,wrongID,f,m,p)==nil{
        t.Fatal("noncanonical source message ID accepted")
    }
        wrongFee:=f;wrongFee.FeeLamports++
    if CheckSourceEvidenceCandidate(o,d,wrongFee,m,p)==nil{
        t.Fatal("fee mismatch accepted")
    }
    wrongProgram:=p;wrongProgram.SystemProgram=pubkey(99)
    if CheckSourceEvidenceCandidate(o,d,f,m,wrongProgram)==nil{
        t.Fatal("noncanonical System Program accepted")
    }
    wrongProgram=p;wrongProgram.TokenProgram=pubkey(99)
    if CheckSourceEvidenceCandidate(o,d,f,m,wrongProgram)==nil{
        t.Fatal("wrong SPL Token Program accepted")
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

func TestSolanaPDADerivationKnownInputs(t *testing.T){
    // Reference point computed independently with the Solana PDA SHA-256
    // domain and Ed25519 decompression rule, not by hardcoding fake keys.
    var program [32]byte
    for i:=range program{program[i]=17}
    fee,err:=deriveXitaPDA(program,[]byte("xita-source-fee"))
    if err!=nil{t.Fatal(err)}
    expected:="7b17712335a7150af4e04f0f57ff7436106485d7ec7dd49eff6d207988e2b7df"
    if fmt.Sprintf("%x",fee)!=expected{
        t.Fatalf("unexpected Solana PDA %x",fee)
    }
    validator,err:=deriveXitaPDA(program,[]byte("xita-trusted-xgr-validators"))
    if err!=nil{t.Fatal(err)}
    if fmt.Sprintf("%x",validator)!="f4dbfa8c914254b2c239706fdf89cf673dddd49b6fd2305e45b1a1e64ed68a2e"{
        t.Fatalf("unexpected validator PDA %x",validator)
    }
    if _,err:=deriveXitaPDA(program,make([]byte,33));err==nil{
        t.Fatal("overlong PDA seed accepted")
    }
    if _,err:=deriveXitaPDA([32]byte{},[]byte("xita-source-fee"));err==nil{
        t.Fatal("empty program ID accepted")
    }
}
