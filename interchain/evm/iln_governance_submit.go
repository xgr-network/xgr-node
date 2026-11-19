package evm

import (
	"fmt"
	"math/big"
	"time"

	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/interchain/authorization"
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
	Nonce  uint64
	SetID  uint64
}

// EncodeILNGovernanceCalldata retains the existing BLS encoding API.
func EncodeILNGovernanceCalldata(proposal protocol.ILNSourceFeeProposal, bitmap *big.Int, signature []byte, verifierFormat string) ([]byte, error) {
	if bitmap == nil || bitmap.Sign() <= 0 || bitmap.BitLen() > 256 {
		return nil, fmt.Errorf("invalid ILN governance signer bitmap")
	}
	return EncodeILNGovernanceAuthorizationCalldata(&Destination{SignatureScheme: authorization.SchemeBLS, VerifierFormat: verifierFormat}, proposal,
		authorization.Proof{Bitmap: bitmap.Bytes(), Signature: signature})
}

// EncodeILNGovernanceAuthorizationCalldata never signs the proposal. Both
// registry schemes expose the same source-fee tuple ABI; the adapter controls
// the bitmap and signature encoding without changing source-fee semantics.
func EncodeILNGovernanceAuthorizationCalldata(source *Destination, proposal protocol.ILNSourceFeeProposal, proof authorization.Proof) ([]byte, error) {
	if _, err := proposal.MarshalBinary(); err != nil {
		return nil, fmt.Errorf("invalid ILN governance proposal: %w", err)
	}
	formatted, err := FormatQuorumProof(source, proof)
	if err != nil {
		return nil, err
	}
	method := ilnApplyGovernanceABI.Methods["applySourceFee"]
	if method == nil {
		return nil, fmt.Errorf("ILN ABI lacks applySourceFee")
	}
	data, err := method.Inputs.Encode(map[string]interface{}{
		"proposal": map[string]interface{}{
			"sourceChainId":   new(big.Int).SetUint64(proposal.SourceChainID),
			"sourceDomain":    proposal.SourceDomain,
			"registry":        ethgo.Address(proposal.Registry),
			"setId":           new(big.Int).SetUint64(proposal.SetID),
			"nonce":           new(big.Int).SetUint64(proposal.Nonce),
			"validUntil":      new(big.Int).SetUint64(proposal.ValidUntil),
			"validatorFeeWei": proposal.ValidatorFeeWei,
		},
		"signerBitmap":       formatted.Bitmap,
		"aggregateSignature": formatted.Signature,
	})
	if err != nil {
		return nil, fmt.Errorf("encode ILN applySourceFee: %w", err)
	}
	return append(method.ID(), data...), nil
}

// SubmitILNGovernance is the existing BLS-only compatibility entrypoint.
func SubmitILNGovernance(source *Destination, key ethgo.Key, proposal protocol.ILNSourceFeeProposal, bitmap *big.Int, signature []byte) (*ILNGovernanceSubmitResult, error) {
	adapter, err := source.AuthorizationAdapter()
	if err != nil {
		return nil, err
	}
	if adapter.Scheme() != authorization.SchemeBLS {
		return nil, fmt.Errorf("BLS governance API requires BLS configuration")
	}
	if bitmap == nil || bitmap.Sign() <= 0 {
		return nil, fmt.Errorf("ILN governance signer bitmap invalid")
	}
	return SubmitILNGovernanceAuthorization(source, key, proposal, authorization.Proof{Bitmap: bitmap.Bytes(), Signature: signature})
}

// SubmitILNGovernanceAuthorization runs only after an explicit local execute
// request. Both schemes share source binding, nonce, epoch, expiration, gas,
// transaction and receipt checks; cryptographic quorum proof is the adapter's
// responsibility. No execution path signs or approves a proposal.
func SubmitILNGovernanceAuthorization(source *Destination, key ethgo.Key, proposal protocol.ILNSourceFeeProposal, proof authorization.Proof) (*ILNGovernanceSubmitResult, error) {
	if key == nil {
		return nil, fmt.Errorf("ILN governance executor key is missing")
	}
	if err := source.ValidateILNRead(); err != nil {
		return nil, err
	}
	if err := source.ValidateSubmission(); err != nil {
		return nil, err
	}
	adapter, err := source.AuthorizationAdapter()
	if err != nil {
		return nil, err
	}
	if source.ChainID != 1643 {
		if err := VerifyDestinationChainID(source); err != nil {
			return nil, err
		}
	}
	if proposal.Registry != types.StringToAddress(source.ILNRegistryAddress) ||
		proposal.SourceChainID != source.ChainID ||
		proposal.SourceDomain != source.Domain {
		return nil, fmt.Errorf("ILN governance source/registry does not match configured chain")
	}
	if proposal.ValidUntil <= uint64(time.Now().Unix()) {
		return nil, fmt.Errorf("ILN governance proposal has expired")
	}
	set, err := GetValidatorSet(source)
	if err != nil {
		return nil, err
	}
	if proposal.SetID != set.SetID {
		return nil, fmt.Errorf("ILN governance source set rotated: proposal=%d current=%d", proposal.SetID, set.SetID)
	}
	currentNonce, err := GetConfirmedILNGovernanceNonce(source)
	if err != nil {
		return nil, err
	}
	if currentNonce == ^uint64(0) || proposal.Nonce != currentNonce+1 {
		return nil, fmt.Errorf("ILN governance nonce stale: proposal=%d chain=%d", proposal.Nonce, currentNonce)
	}
	if err := adapter.VerifySourceFeeQuorum(set.AuthorizationSet(), proposal, proof); err != nil {
		return nil, fmt.Errorf("invalid ILN governance quorum: %w", err)
	}
	calldata, err := EncodeILNGovernanceAuthorizationCalldata(source, proposal, proof)
	if err != nil {
		return nil, err
	}
	if source.ChainID == 1643 {
		to := proposal.Registry
		receipt, err := DestinationSubmit(source, key, to, calldata, big.NewInt(0))
		if err != nil {
			return nil, err
		}
		if receipt == nil {
			return nil, fmt.Errorf("native XGR governance receipt missing")
		}
		actualNonce, err := GetConfirmedILNGovernanceNonce(source)
		if err != nil {
			return nil, err
		}
		if actualNonce != proposal.Nonce {
			return nil, fmt.Errorf("native XGR governance post-state nonce mismatch")
		}
		return &ILNGovernanceSubmitResult{TxHash: receipt.TransactionHash.String(),
			Nonce: actualNonce, SetID: set.SetID}, nil
	}
	verifiedRPCURL, endpointErr := selectVerifiedRPCURL(source)
	if endpointErr != nil {
		return nil, endpointErr
	}
	relayer, err := txrelayer.NewTxRelayer(
		txrelayer.WithIPAddress(verifiedRPCURL),
		txrelayer.WithReceiptTimeout(500*time.Millisecond),
	)
	if err != nil {
		return nil, err
	}
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
	if err != nil {
		return nil, fmt.Errorf("submit ILN governance: %w", err)
	}
	if receipt == nil || receipt.Status != uint64(types.ReceiptSuccess) {
		return nil, fmt.Errorf("source-chain ILN governance transaction reverted")
	}
	if err := waitForFinality(source, receipt); err != nil {
		return nil, err
	}
	actualNonce, err := GetConfirmedILNGovernanceNonce(source)
	if err != nil {
		return nil, fmt.Errorf("verify ILN governance nonce after receipt: %w", err)
	}
	if actualNonce != proposal.Nonce {
		return nil, fmt.Errorf("ILN governance post-state nonce mismatch: got=%d expected=%d", actualNonce, proposal.Nonce)
	}
	return &ILNGovernanceSubmitResult{TxHash: receipt.TransactionHash.String(), Nonce: actualNonce, SetID: set.SetID}, nil
}
