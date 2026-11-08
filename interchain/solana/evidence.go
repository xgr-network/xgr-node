package solana

// Source-evidence structural cross-checker for the native XITA SVM gateway.
// Do NOT use these checks alone to authorize Direct-BLS signing. They must be
// combined with independent finalized RPC account-owner/data reads, canonical
// PDA derivation, pinned SBF ELF, and verified inner SPL/fee transitions.
// Legacy Solana transactions only; versioned lookup transactions fail closed.
import (
    "bytes"
    "crypto/sha256"
    "encoding/binary"
    "errors"
    "fmt"
    "math/big"
)

const (
    sourceDispatchBorshLen = 299
    sourceFeeReceiptBorshLen = 83
    bridgeSourceV1Opcode = byte(11) // appended Rust XitaInstruction variant
)
type SourceDispatch struct {
    MessageID [32]byte
    RouteID [32]byte
    Sender [32]byte
    Recipient [32]byte
    SourceTokenAccount [32]byte
    SourceMint [32]byte
    TokenVault [32]byte
    AmountLocal uint64
    AmountCanonical [32]byte
    FeeLamports uint64
    Nonce uint64
    SourceSlot uint64
    ValidatorSetID uint64
    CustodyKind byte
}
type FeeReceipt struct {
    RouteID [32]byte
    MessageID [32]byte
    ValidatorSetID uint64
    FeeLamports uint64
}
func DecodeSourceDispatchBorsh(raw []byte)(SourceDispatch,error){
    var out SourceDispatch
    if len(raw)!=sourceDispatchBorshLen||raw[0]!=0x44||raw[1]!=1 {
        return out,errors.New("invalid XITA source dispatch data length/version")
    }
    p:=2
    for _,dst:=range []*[32]byte{
        &out.MessageID,&out.RouteID,&out.Sender,&out.Recipient,
        &out.SourceTokenAccount,&out.SourceMint,&out.TokenVault,
    } {
        copy(dst[:],raw[p:p+32]);p+=32
    }
    out.AmountLocal=binary.LittleEndian.Uint64(raw[p:p+8]);p+=8
    copy(out.AmountCanonical[:],raw[p:p+32]);p+=32
    for _,dst:=range []*uint64{
        &out.FeeLamports,&out.Nonce,&out.SourceSlot,&out.ValidatorSetID,
    }{
        *dst=binary.LittleEndian.Uint64(raw[p:p+8]);p+=8
    }
    out.CustodyKind=raw[p]
    if p+1!=len(raw)||out.MessageID==([32]byte{})||
        out.RouteID==([32]byte{})||out.Sender==([32]byte{})||
        out.Recipient==([32]byte{})||out.AmountLocal==0||
        out.AmountCanonical==([32]byte{})||out.FeeLamports==0||
        out.Nonce==0||out.SourceSlot==0||out.ValidatorSetID==0||
        (out.CustodyKind!=1&&out.CustodyKind!=2) {
        return SourceDispatch{},errors.New("invalid XITA source dispatch fields")
    }
    return out,nil
}
func DecodeFeeReceiptBorsh(raw []byte)(FeeReceipt,error){
    var out FeeReceipt
    if len(raw)!=sourceFeeReceiptBorshLen||
       raw[0]!=0x4f||raw[1]!=1 {return out,errors.New("invalid XITA fee receipt")}
    copy(out.RouteID[:],raw[3:35])
    copy(out.MessageID[:],raw[35:67])
    out.ValidatorSetID=binary.LittleEndian.Uint64(raw[67:75])
    out.FeeLamports=binary.LittleEndian.Uint64(raw[75:83])
    if out.RouteID==([32]byte{})||out.MessageID==([32]byte{})||
       out.ValidatorSetID==0||out.FeeLamports==0{
        return FeeReceipt{},errors.New("empty fee receipt")
    }
    return out,nil
}
type SourceCandidatePolicy struct {
    ProgramID [32]byte
    Router [32]byte
    RouteID [32]byte
    RoutePDA [32]byte
    FeeVault [32]byte
    SourceFeePDA [32]byte
    ValidatorPDA [32]byte
    NoncePDA [32]byte
    FeeEpochPDA [32]byte
    TokenProgram [32]byte
    SystemProgram [32]byte
    RentSysvar [32]byte
    FeeReceiptPDA [32]byte
    SourceDispatchPDA [32]byte
    Mint [32]byte
    CustodyKind byte
    TokenDecimals uint8
    ExpectedFeeLamports uint64
    ExpectedValidatorSet uint64
}
type TokenBalanceMovement struct{
    SourceBefore uint64
    SourceAfter uint64
    VaultBefore uint64
    VaultAfter uint64
    MintSupplyBefore uint64
    MintSupplyAfter uint64
    FeeVaultBefore uint64
    FeeVaultAfter uint64
}
func equalCanonicalAmount(kind byte,decimals uint8,local uint64,word [32]byte)bool{
    var expected *big.Int
    if kind==1{
        expected=new(big.Int).SetUint64(local)
    }else if kind==2 &&decimals<=9 {
        multiplier:=new(big.Int).Exp(big.NewInt(10),
            big.NewInt(int64(18-decimals)),nil)
        expected=new(big.Int).Mul(new(big.Int).SetUint64(local),multiplier)
    }else{return false}
    if expected.Sign()<=0||expected.BitLen()>256{return false}
    var raw [32]byte
    expected.FillBytes(raw[:])
    return word==raw
}

// CheckSourceEvidenceCandidate establishes strict, independent invariants
// for a *candidate* source transaction. It is NEVER an authorization for
// BLS signing and does NOT claim that caller-provided RPC state is genuine.
// A future caller MUST obtain every account and transaction meta from 2
// independent finalized Solana RPC operators and verify the immutable PDAs,
// account owners, rent and executed CPI logs before releasing any signature.
func CheckSourceEvidenceCandidate(
    observed Observation,
    dispatch SourceDispatch,
    receipt FeeReceipt,
    balances TokenBalanceMovement,
    policy SourceCandidatePolicy,
)error{
    if observed.Slot==0||observed.IndependentWitnesses<2 ||
        observed.TransactionSHA256!=sha256.Sum256(observed.RawTransaction) ||
        policy.ProgramID==([32]byte{})||
        policy.Router==([32]byte{})||policy.RoutePDA==([32]byte{})||
        policy.RouteID==([32]byte{})||policy.FeeVault==([32]byte{})||
        policy.SourceFeePDA==([32]byte{})||
        policy.ValidatorPDA==([32]byte{})||
        policy.NoncePDA==([32]byte{})||
        policy.FeeEpochPDA==([32]byte{})||
        policy.TokenProgram==([32]byte{})||
        policy.SystemProgram==([32]byte{})||
        policy.RentSysvar==([32]byte{})||
        policy.FeeReceiptPDA==([32]byte{})||
        policy.SourceDispatchPDA==([32]byte{})||
        policy.Mint==([32]byte{})||
        (policy.CustodyKind!=1&&policy.CustodyKind!=2) ||
        policy.ExpectedFeeLamports==0||policy.ExpectedValidatorSet==0{
        return errors.New("source witness policy is incomplete")
    }
    if dispatch.RouteID!=policy.RouteID ||
       receipt.RouteID!=policy.RouteID||
       dispatch.MessageID!=receipt.MessageID ||
       dispatch.SourceMint!=policy.Mint ||
       dispatch.SourceSlot!=observed.Slot||
       dispatch.FeeLamports!=policy.ExpectedFeeLamports||
       receipt.FeeLamports!=dispatch.FeeLamports||
       dispatch.ValidatorSetID!=policy.ExpectedValidatorSet||
       receipt.ValidatorSetID!=dispatch.ValidatorSetID ||
       dispatch.CustodyKind!=policy.CustodyKind||
       dispatch.SourceTokenAccount==([32]byte{})||
       !equalCanonicalAmount(
           dispatch.CustodyKind,policy.TokenDecimals,
           dispatch.AmountLocal,dispatch.AmountCanonical,
       ){
        return errors.New("source/fee evidence mismatch")
    }
    if dispatch.Recipient==([32]byte{}) ||
        !bytes.Equal(dispatch.Recipient[:12],make([]byte,12)){
        return errors.New("destination EVM recipient is not zero-padded")
    }
    if policy.CustodyKind==1 && (
        dispatch.TokenVault==([32]byte{})||
        dispatch.TokenVault==dispatch.SourceTokenAccount||
        balances.VaultAfter<balances.VaultBefore||
        balances.VaultAfter-balances.VaultBefore!=dispatch.AmountLocal){
        return errors.New("collateral escrow increase not verified")
    }
    if policy.CustodyKind==2 && (
        dispatch.TokenVault!=([32]byte{})||
        balances.MintSupplyBefore<balances.MintSupplyAfter||
        balances.MintSupplyBefore-balances.MintSupplyAfter!=dispatch.AmountLocal){
        return errors.New("synthetic wXGR burn decrease not verified")
    }
    if balances.SourceBefore<balances.SourceAfter||
        balances.SourceBefore-balances.SourceAfter!=dispatch.AmountLocal||
        balances.FeeVaultAfter<balances.FeeVaultBefore||
        balances.FeeVaultAfter-balances.FeeVaultBefore!=dispatch.FeeLamports {
        return errors.New("SPL movement or source-native fee mismatch")
    }
    sig,err:=decodeSignature(observed.Signature)
    if err!=nil{return err}
    tx,err:=decodeWireTransaction(observed.RawTransaction,sig)
    if err!=nil{return err}
    if len(tx.Instructions)!=1{
        return errors.New("unsupported batched Solana bridge transactions")
    }
    ix:=tx.Instructions[0]
    if ix.ProgramID!=policy.ProgramID||
        len(ix.Data)!=1+32+8 ||
        ix.Data[0]!=bridgeSourceV1Opcode ||
        !bytes.Equal(ix.Data[1:33],dispatch.Recipient[:])||
        binary.LittleEndian.Uint64(ix.Data[33:])!=dispatch.AmountLocal ||
        len(ix.AccountIndices)!=16{
        return errors.New("transaction did not execute exact XITA BridgeSourceV1")
    }
    // Exact canonical account indexing for a single source operation.
    expected:=[][32]byte{
        dispatch.Sender,policy.Router,policy.RoutePDA,
        policy.FeeVault,policy.SourceFeePDA,
        policy.ValidatorPDA,
        policy.NoncePDA,policy.FeeEpochPDA,
        policy.FeeReceiptPDA,policy.SourceDispatchPDA,
        dispatch.SourceTokenAccount,policy.Mint,
        policy.Router,policy.TokenProgram,policy.SystemProgram,
        policy.RentSysvar,
    }
    if policy.CustodyKind==1 {
        expected[12]=dispatch.TokenVault
    }
    for i,key:=range expected{
        idx:=int(ix.AccountIndices[i])
        if idx>=len(tx.AccountKeys)||tx.AccountKeys[idx]!=key {
            return fmt.Errorf("source transaction account %d does not match pinned evidence",i)
        }
    }
    if len(tx.SignerPubkeys)==0||
        !bytes.Equal(tx.SignerPubkeys[0][:],dispatch.Sender[:]) {
        return errors.New("bridge principal was not self-signed")
    }
    // Nonce/epoch PDAs, receipt ownership and canonical rent evidence are
    // mandatory for the FINAL verifier, but are not derived by this pure
    // parser. This function must NEVER attach to the Direct-BLS worker.
    return nil
}
