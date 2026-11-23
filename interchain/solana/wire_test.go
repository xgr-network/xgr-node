package solana

import (
    "bytes"
    "strings"
    "testing"
)
func wireFixture(signatures int) ([]byte,[32]byte) {
    var pid [32]byte
    pid[31]=0x99
    raw:=[]byte{byte(signatures)}
    raw=append(raw,make([]byte,signatures*64)...)
    raw=append(raw,byte(signatures),0,0)
    raw=append(raw,3) // 3 explicit static account keys
    raw=append(raw,bytes.Repeat([]byte{0x11},32)...)
    raw=append(raw,pid[:]...)
    raw=append(raw,bytes.Repeat([]byte{0x33},32)...)
    raw=append(raw,make([]byte,32)...)
    raw=append(raw,1) // one instruction
    raw=append(raw,1) // program key index 1
    raw=append(raw,2,0,2) // two accounts, indexes 0 and 2
    raw=append(raw,3,8,2,3) // canonical 3 byte instruction data
    return raw,pid
}
func TestWireProgramIDAndInstructionArePinned(t *testing.T){
    raw,pid:=wireFixture(1)
    var sig [64]byte
    tx,err:=decodeWireTransaction(raw,sig)
    if err!=nil{t.Fatal(err)}
    if tx.Signatures!=1||len(tx.Instructions)!=1||
       tx.Instructions[0].ProgramID!=pid||
       tx.Instructions[0].AccountIndices[1]!=2{
       t.Fatalf("bad wire parse: %#v",tx)
    }
    obs:=Observation{
        Signature:strings.Repeat("1",64),RawTransaction:raw,
    }
    if !obs.ContainsPinnedInstruction(pid,[]byte{8,2}){
       t.Fatal("pinned program instruction was not recognized")
    }
    fake:=pid;fake[31]^=1
    if obs.ContainsPinnedInstruction(fake,[]byte{8,2}) ||
       obs.ContainsPinnedInstruction(pid,[]byte{8,1}) ||
       obs.ContainsPinnedInstruction(pid,nil) {
       t.Fatal("foreign program/discriminator accepted")
    }
}
func TestWireVersionedMalformedAndMultiSignerPolicy(t *testing.T){
    raw,_:=wireFixture(2)
    var sig [64]byte
    parsed,err:=decodeWireTransaction(raw,sig)
    if err!=nil||parsed.Signatures!=2 {t.Fatalf("valid multisig: %v",err)}
    raw[len(raw)-1]=4
    if _,err:=decodeWireTransaction(raw[:len(raw)-1],sig);err==nil{
        t.Fatal("truncated instruction accepted")
    }
    raw,_=wireFixture(1)
    raw[65]|=0x80
    if _,err:=decodeWireTransaction(raw,sig);err==nil {
        t.Fatal("v0 lookup-bearing tx treated as legacy")
    }
    raw,_=wireFixture(1)
    raw[len(raw)-7]=9 // corrupt program index, outside all static keys
    if _,err:=decodeWireTransaction(raw,sig);err==nil{
        t.Fatal("unknown instruction program accepted")
    }
    raw,_=wireFixture(1)
    raw=append(raw,0x00)
    if _,err:=decodeWireTransaction(raw,sig);err==nil{
        t.Fatal("trailing data accepted")
    }
    raw,_=wireFixture(1)
    sig[0]=1
    if _,err:=decodeWireTransaction(raw,sig);err==nil{
        t.Fatal("transaction signature mismatch accepted")
    }
}
