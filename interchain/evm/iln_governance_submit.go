package evm

import (
    "fmt"
    "math/big"
    "time"

    "github.com/umbracle/ethgo"
    "github.com/umbracle/ethgo/abi"

    protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
    "github.com/xgr-network/xgr-node/txrelayer"
    "github.com/xgr-network/xgr-node/types"
)

// ABI tuple must match XGRILNProtocol.SourceFeeProposalV315 exactly.
const ilnApplyGovernanceABIJSON = `[
{"type":"function","name":"applySourceFee","stateMutability":"nonpayable","inputs":[
 {"name":"proposal","type":"tuple","components":[
  {"name":"sourceChainId","type":"uint64"},
  {"name":"sourceDomain","type":"uint32"},
  {"name":"registry","type":"address"},
  {"name":"setId","type":"uint64"},
  {"name":"nonce","type":"uint64"},
  {"name":"validUntil","type":"uint64"},
  {"name":"validatorFeeWei","type":"uint256"}]},
 {"name":"signerBitmap","type":"bytes"},
 {"name":"aggregateSignature","type":"bytes"}
],"outputs":[]}
]`

var ilnApplyGovernanceABI = abi.MustNewABI(ilnApplyGovernanceABIJSON)

type ILNGovernanceSubmitResult struct {
    TxHash string
    Nonce uint64
    SetID uint64
}

// EncodeILNGovernanceCalldata is useful for an independent, permissionless
// executor. It never signs the proposal and never grants an EOA authority.
// The signature must already form a valid on-chain governance quorum.
func EncodeILNGovernanceCalldata(proposal protocol.ILNSourceFeeProposal, signerBitmap *big.Int, compressedSignature []byte, verifierFormat string) ([]byte, error) {
    if signerBitmap == nil || signerBitmap.Sign() <= 0 || signerBitmap.BitLen() > 256 {
        return nil, fmt.Errorf("invalid ILN governance signer bitmap")
    }
    if len(compressedSignature) == 0 {
        return nil, fmt.Errorf("missing ILN governance aggregate signature")
    }
    if _, err := proposal.MarshalBinary(); err != nil {
        return nil, fmt.Errorf("invalid ILN governance proposal: %w", err)
    }
    cfg := &Destination{VerifierFormat: verifierFormat}
    signature, err := FormatAggregateSignature(cfg, compressedSignature)
    if err != nil {
        return nil, err
    }
    method := ilnApplyGovernanceABI.Methods["applySourceFee"]
    if method == nil {return nil,fmt.Errorf("ILN ABI lacks applySourceFee")}
    data,err:=method.Inputs.Encode(map[string]interface{}{
        "proposal":map[string]interface{}{
            "sourceChainId":new(big.Int).SetUint64(proposal.SourceChainID),
            "sourceDomain":proposal.SourceDomain,
            "registry":ethgo.Address(proposal.Registry),
            "setId":new(big.Int).SetUint64(proposal.SetID),
            "nonce":new(big.Int).SetUint64(proposal.Nonce),
            "validUntil":new(big.Int).SetUint64(proposal.ValidUntil),
            "validatorFeeWei":proposal.ValidatorFeeWei,
        },
        "signerBitmap":signerBitmap.Bytes(),
        "aggregateSignature":signature,
    })
    if err != nil { return nil, fmt.Errorf("encode ILN applySourceFee: %w", err) }
    return append(method.ID(), data...), nil
}

// SubmitILNGovernance is invoked ONLY following a local explicit execute CLI
// request. No caller-supplied proposal or bitmap bypasses canonical on-chain
// nonce, validator-set, BLS and expiration preflight.
func SubmitILNGovernance(
    source *Destination,
    key ethgo.Key,
    proposal protocol.ILNSourceFeeProposal,
    bitmap *big.Int,
    compressedSignature []byte,
) (*ILNGovernanceSubmitResult, error) {
    if key == nil { return nil, fmt.Errorf("ILN governance executor key is missing") }
    if err := source.ValidateILNRead(); err != nil { return nil, err }
    if err := source.ValidateMembershipRead(); err != nil { return nil, err }
    if source.Confirmations == 0 { return nil, fmt.Errorf("source confirmation policy is required") }
    if err := VerifyDestinationChainID(source); err != nil { return nil, err }
    if proposal.Registry != types.StringToAddress(source.ILNRegistryAddress) ||
       proposal.SourceChainID != source.ChainID ||
       proposal.SourceDomain != source.Domain {
        return nil, fmt.Errorf("ILN governance source/registry does not match configured chain")
    }
    if proposal.ValidUntil <= uint64(time.Now().Unix()) {
        return nil, fmt.Errorf("ILN governance proposal has expired")
    }
    set, err := GetValidatorSet(source)
    if err != nil { return nil, err }
    if proposal.SetID != set.SetID {
        return nil, fmt.Errorf("ILN governance source set rotated: proposal=%d current=%d", proposal.SetID, set.SetID)
    }
    currentNonce, err := GetConfirmedILNGovernanceNonce(source)
    if err != nil { return nil, err }
    if currentNonce == ^uint64(0) || proposal.Nonce != currentNonce+1 {
        return nil, fmt.Errorf("ILN governance nonce stale: proposal=%d chain=%d", proposal.Nonce, currentNonce)
    }
    payload, err := proposal.MarshalBinary()
    if err != nil { return nil, err }
    if bitmap == nil || bitmap.Sign() <= 0 || bitmap.BitLen() > len(set.Validators) {
        return nil, fmt.Errorf("ILN governance signer bitmap invalid for active validator set")
    }
    if err := protocol.VerifyAggregatedQuorum(set.BLSPublicKeys, bitmap, compressedSignature, payload); err != nil {
        return nil, fmt.Errorf("invalid ILN governance BLS quorum: %w", err)
    }
    calldata, err := EncodeILNGovernanceCalldata(proposal, bitmap, compressedSignature, source.VerifierFormat)
    if err != nil { return nil, err }
    relayer, err := txrelayer.NewTxRelayer(
        txrelayer.WithIPAddress(source.RPCURL),
        txrelayer.WithReceiptTimeout(500*time.Millisecond),
    )
    if err != nil { return nil, err }
    to := ethgo.Address(proposal.Registry)
    tx := &ethgo.Transaction{
        From: key.Address(), To: &to, Input: calldata, Value: big.NewInt(0),
        Type: ethgo.TransactionDynamicFee,
    }
    if _, err := prepareAndCheckGas(relayer.Client(), tx, key.Address()); err != nil {
        return nil, fmt.Errorf("ILN governance transaction preflight: %w", err)
    }
    // All quorum permissions are checked again on chain. Any executor may
    // send this calldata; this implementation uses the node's configured
    // ECDSA account only after explicit local CLI authorization.
    receipt, err := relayer.SendTransaction(tx, key)
    if err != nil { return nil, fmt.Errorf("submit ILN governance: %w", err) }
    if receipt == nil || receipt.Status != uint64(types.ReceiptSuccess) {
        return nil, fmt.Errorf("source-chain ILN governance transaction reverted")
    }
    if err := waitForConfirmations(source, receipt); err != nil { return nil, err }
    actualNonce, err := GetConfirmedILNGovernanceNonce(source)
    if err != nil { return nil, fmt.Errorf("verify ILN governance nonce after receipt: %w", err) }
    if actualNonce != proposal.Nonce {
        return nil, fmt.Errorf("ILN governance post-state nonce mismatch: got=%d expected=%d", actualNonce, proposal.Nonce)
    }
    return &ILNGovernanceSubmitResult{TxHash: receipt.TransactionHash.String(), Nonce: actualNonce, SetID: set.SetID}, nil
}
