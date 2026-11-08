package solana

// Independently corroborated finalized account witnesses. This evidence is
// read-only and is NEVER connected to the Direct-BLS signing worker. It
// must later be combined with a pinned program ID, deterministic receipt
// PDA, immutable ELF and the actual source transaction SPL/fee meta.
import(
    "bytes"
    "context"
    "encoding/base64"
    "errors"
    "fmt"
    "math/big"
    "strings"
)
type FinalizedAccount struct{
    Pubkey [32]byte
    Owner [32]byte
    Lamports uint64
    Data []byte
    ObservedSlot uint64
    IndependentWitnesses int
}
func decodeBase58Pubkey(encoded string)([32]byte,error){
    var out [32]byte
    if len(encoded)<32||len(encoded)>44{
        return out,errors.New("invalid Solana pubkey length")
    }
    n:=new(big.Int)
    radix:=big.NewInt(58)
    for _,c:=range encoded{
        i:=strings.IndexRune(base58Alphabet,c)
        if i<0{return out,errors.New("invalid base58 pubkey")}
        n.Mul(n,radix)
        n.Add(n,big.NewInt(int64(i)))
    }
    leading:=0
    for leading<len(encoded)&&encoded[leading]=='1'{leading++}
    decoded:=append(bytes.Repeat([]byte{0},leading),n.Bytes()...)
    if len(decoded)!=32{return out,errors.New("Solana pubkey not 32 bytes")}
    copy(out[:],decoded)
    return out,nil
}
type accountResult struct{
    Context struct{ Slot uint64 `json:"slot"` } `json:"context"`
    Value *struct {
        Data []string `json:"data"`
        Owner string `json:"owner"`
        Lamports uint64 `json:"lamports"`
        Executable bool `json:"executable"`
    } `json:"value"`
}
func (o *Observer) finalizedAccount(
    ctx context.Context,ep Endpoint,key string,parsed [32]byte,minSlot uint64,
)(FinalizedAccount,error){
    var genesis string
    if err:=o.call(ctx,ep,"getGenesisHash",[]any{},&genesis);err!=nil{
        return FinalizedAccount{},err
    }
    if genesis!=MainnetGenesis{
        return FinalizedAccount{},errors.New("Solana account witness has wrong genesis")
    }
    var reply accountResult
    if err:=o.call(ctx,ep,"getAccountInfo",[]any{
        key,map[string]any{
            "encoding":"base64","commitment":"finalized","minContextSlot":minSlot,
        },
    },&reply);err!=nil{return FinalizedAccount{},err}
    if reply.Context.Slot<minSlot ||reply.Value==nil ||
       reply.Value.Executable||reply.Value.Lamports==0||
       len(reply.Value.Data)!=2||reply.Value.Data[1]!="base64"{
        return FinalizedAccount{},errors.New("Solana account not finalized, live or canonical")
    }
    owner,err:=decodeBase58Pubkey(reply.Value.Owner)
    if err!=nil{return FinalizedAccount{},err}
    raw,err:=base64.StdEncoding.Strict().DecodeString(reply.Value.Data[0])
    if err!=nil||len(raw)==0||len(raw)>65536{
        return FinalizedAccount{},errors.New("invalid Solana receipt account data")
    }
    return FinalizedAccount{
        Pubkey:parsed,Owner:owner,Lamports:reply.Value.Lamports,
        Data:raw,ObservedSlot:reply.Context.Slot,
    },nil
}
/// Immutable source dispatch and fee receipt witnesses are compared across
/// independently operated finalized RPC endpoints. The accounts may have
/// different observed finalized slot heights; their account data, owner and
/// lamports must agree byte-for-byte. Current mutable fee vault and SPL
/// token account balances MUST be read from source transaction META instead,
/// not compared as immutable account snapshots.
func (o *Observer) FinalizedReceiptAccount(
    ctx context.Context,address string,minSlot uint64,expectedOwner [32]byte,
)(FinalizedAccount,error){
    if o==nil||len(o.endpoints)<2||minSlot==0||expectedOwner==([32]byte{}){
        return FinalizedAccount{},errors.New("invalid independent Solana receipt witness policy")
    }
    key,err:=decodeBase58Pubkey(address)
    if err!=nil{return FinalizedAccount{},err}
    var first FinalizedAccount
    for i,ep:=range o.endpoints{
        seen,err:=o.finalizedAccount(ctx,ep,address,key,minSlot)
        if err!=nil{return FinalizedAccount{},fmt.Errorf(
            "Solana account witness %s: %w",ep.OperatorID,err
        )}
        if seen.Owner!=expectedOwner{
            return FinalizedAccount{},errors.New("Solana receipt owner differs from pinned XITA program")
        }
        if i==0{first=seen}else if seen.Owner!=first.Owner||
            seen.Pubkey!=first.Pubkey||seen.Lamports!=first.Lamports||
            !bytes.Equal(seen.Data,first.Data){
            return FinalizedAccount{},errors.New("independent finalized Solana receipt witnesses disagree")
        }
    }
    first.IndependentWitnesses=len(o.endpoints)
    return first,nil
}
