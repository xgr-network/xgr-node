package solana

// Strict, read-only legacy Solana transaction decoding for independent RPC
// source-evidence candidate evaluation. A decoded instruction does NOT
// constitute proven XITA escrow/fee evidence or permission for BLS signing.
import (
    "bytes"
    "errors"
    "fmt"
)

var ErrUnsupportedWire = errors.New("unsupported Solana wire transaction")

type Instruction struct {
    ProgramID [32]byte
    AccountIndices []uint8
    Data []byte
}
type WireTransaction struct {
    Signatures int
    SignerPubkeys [][32]byte
    AccountKeys [][32]byte
    Instructions []Instruction
    MessageVersion int // -1 legacy; 0 v0 (not supported until lookup resolution)
}
func shortvec(data []byte,pos *int) (int,error) {
    v:=0
    for i:=0;i<3;i++ {
        if *pos>=len(data) {return 0,ErrUnsupportedWire}
        b:=data[*pos]
        *pos++
        v|=int(b&0x7f)<<(7*i)
        if b&0x80==0 {
            if i>0 && v < 1<<(7*i) {return 0,ErrUnsupportedWire}
            if v>65535 {return 0,ErrUnsupportedWire}
            return v,nil
        }
    }
    return 0,ErrUnsupportedWire
}
func take(data []byte,pos *int,n int)([]byte,error){
    if n<0 || *pos<0 || n>len(data)-*pos{return nil,ErrUnsupportedWire}
    b:=data[*pos:*pos+n]
    *pos+=n
    return b,nil
}
func decodeWireTransaction(raw []byte,primarySig [64]byte)(WireTransaction,error) {
    var out WireTransaction
    out.MessageVersion=-1
    pos:=0
    count,err:=shortvec(raw,&pos)
    if err!=nil||count<1||count>16 {return out,ErrUnsupportedWire}
    signatureBytes,err:=take(raw,&pos,count*64)
    if err!=nil||!bytes.Equal(signatureBytes[:64],primarySig[:]) {
        return out,errors.New("Solana first transaction signature mismatch")
    }
    out.Signatures=count
    if pos>=len(raw){return out,ErrUnsupportedWire}
    if raw[pos]&0x80!=0 {
        // Versioned v0 transactions need authenticated address-table lookup
        // resolution; never silently treat them as legacy.
        return out,ErrUnsupportedWire
    }
    header,err:=take(raw,&pos,3)
    if err!=nil||int(header[0])!=count ||
       header[1]>header[0] {return out,ErrUnsupportedWire}
    n,err:=shortvec(raw,&pos)
    if err!=nil||n<1||n>256||count>n||
       int(header[2])>n-count {return out,ErrUnsupportedWire}
    out.AccountKeys=make([][32]byte,n)
    out.SignerPubkeys=make([][32]byte,count)
    for i:=range out.AccountKeys {
        key,err:=take(raw,&pos,32)
        if err!=nil{return WireTransaction{},err}
        copy(out.AccountKeys[i][:],key)
        if i<count{out.SignerPubkeys[i]=out.AccountKeys[i]}
    }
    if _,err=take(raw,&pos,32);err!=nil{return WireTransaction{},err}
    instructionCount,err:=shortvec(raw,&pos)
    if err!=nil||instructionCount>256{return WireTransaction{},ErrUnsupportedWire}
    for i:=0;i<instructionCount;i++{
        programIdx,err:=take(raw,&pos,1)
        if err!=nil||int(programIdx[0])>=n{return WireTransaction{},ErrUnsupportedWire}
        nAccounts,err:=shortvec(raw,&pos)
        if err!=nil||nAccounts>256{return WireTransaction{},ErrUnsupportedWire}
        indexes,err:=take(raw,&pos,nAccounts)
        if err!=nil{return WireTransaction{},err}
        for _,idx:=range indexes {
            if int(idx)>=n{return WireTransaction{},ErrUnsupportedWire}
        }
        dataLen,err:=shortvec(raw,&pos)
        if err!=nil||dataLen>16384{return WireTransaction{},ErrUnsupportedWire}
        data,err:=take(raw,&pos,dataLen)
        if err!=nil{return WireTransaction{},err}
        out.Instructions=append(out.Instructions,Instruction{
            ProgramID:out.AccountKeys[int(programIdx[0])],
            AccountIndices:append([]uint8(nil),indexes...),
            Data:append([]byte(nil),data...),
        })
    }
    if pos!=len(raw){
        return WireTransaction{},fmt.Errorf("trailing Solana legacy message bytes: %w",ErrUnsupportedWire)
    }
    return out,nil
}

// Confirm that a finalized, agreed-upon transaction invoked an EXACT pinned
// executable Program ID and a specified instruction variant. This is just a
// structural precondition for escrow/fee verification, never its replacement.
func (o Observation) ContainsPinnedInstruction(program [32]byte, discriminator []byte) bool {
    if program==([32]byte{})||len(discriminator)==0 {return false}
    sig,err:=decodeSignature(o.Signature)
    if err!=nil{return false}
    tx,err:=decodeWireTransaction(o.RawTransaction,sig)
    if err!=nil{return false}
    for _,ix:=range tx.Instructions {
        if ix.ProgramID==program&&bytes.HasPrefix(ix.Data,discriminator){
            return true
        }
    }
    return false
}
