package evm

import (
	"bytes"
	"fmt"
	"math/big"
	"time"

	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/interchain/authorization"
	"github.com/xgr-network/xgr-node/txrelayer"
	"github.com/xgr-network/xgr-node/types"
)

// XGR Interchain ValidatorRegistry V3 requires an on-chain proof of key
// possession before a separately quorum-authorized membership ADD.
var possessionABI = abi.MustNewABI(`[
	{"inputs":[{"name":"validator","type":"address"},{"name":"blsKey","type":"bytes"},{"name":"blsKeyEIP2537","type":"bytes"},{"name":"possessionProof","type":"bytes"}],"name":"provePossession","outputs":[],"stateMutability":"nonpayable","type":"function"},
	{"inputs":[{"name":"keyHash","type":"bytes32"}],"name":"possessionVerified","outputs":[{"name":"","type":"bool"}],"stateMutability":"view","type":"function"}
]`)

var ecdsaPossessionABI = abi.MustNewABI(`[
 {"name":"provePossession","type":"function","stateMutability":"nonpayable","inputs":[{"name":"validator","type":"address"},{"name":"proof","type":"bytes"}],"outputs":[]},
 {"name":"possessionVerified","type":"function","stateMutability":"view","inputs":[{"name":"validator","type":"address"}],"outputs":[{"name":"","type":"bool"}]}
]`)

// The following ABI is solely a local encoder for Solidity's
// keccak256(abi.encode(validator, blsKey, blsKeyEIP2537)). It does NOT
// represent an on-chain function to call.
var possessionKeyEncoder = abi.MustNewABI(`[
	{"inputs":[{"name":"validator","type":"address"},{"name":"blsKey","type":"bytes"},{"name":"blsKeyEIP2537","type":"bytes"}],"name":"encodePossessionKey","outputs":[],"stateMutability":"pure","type":"function"}
]`)

func possessionKeyHash(payload protocol.MembershipPayload) (types.Hash, error) {
	method := possessionKeyEncoder.Methods["encodePossessionKey"]
	if method == nil {
		return types.ZeroHash, fmt.Errorf("missing V3 possession hash encoder")
	}
	encoded, err := method.Inputs.Encode(map[string]interface{}{
		"validator":     ethgo.Address(payload.Validator),
		"blsKey":        payload.BLSPublicKey,
		"blsKeyEIP2537": payload.BLSPublicKeyEIP2537,
	})
	if err != nil {
		return types.ZeroHash, fmt.Errorf("encode V3 possession key: %w", err)
	}
	return crypto.Keccak256Hash(encoded), nil
}

// readPossessionVerified retains the V3 getter for existing BLS callers.
func readPossessionVerified(destination *Destination, payload protocol.MembershipPayload) (bool, error) {
	adapter, err := destination.AuthorizationAdapter()
	if err != nil {
		return false, err
	}
	if adapter.Scheme() != authorization.SchemeBLS {
		return false, fmt.Errorf("BLS possession API requires BLS configuration")
	}
	return readPossessionVerifiedAuthorization(destination, authorization.Membership{
		Validator: payload.Validator, BLSPublicKey: payload.BLSPublicKey,
		BLSPublicKeyEIP2537: payload.BLSPublicKeyEIP2537,
	})
}

// An unavailable mapping or malformed response never counts as unverified
// possession: it is an integration error and prevents transaction submission.
func readPossessionVerifiedAuthorization(destination *Destination, payload authorization.Membership) (bool, error) {
	adapter, err := destination.AuthorizationAdapter()
	if err != nil {
		return false, err
	}
	if destination.ChainID != 1643 {
		if err := VerifyDestinationChainID(destination); err != nil {
			return false, err
		}
	}
	method := possessionABI.Methods["possessionVerified"]
	var values map[string]interface{}
	if adapter.Scheme() == authorization.SchemeBLS {
		hash, err := possessionKeyHash(payload.BLS())
		if err != nil {
			return false, err
		}
		values = map[string]interface{}{"keyHash": [32]byte(hash)}
	} else {
		method = ecdsaPossessionABI.Methods["possessionVerified"]
		values = map[string]interface{}{"validator": ethgo.Address(payload.Validator)}
	}
	if method == nil {
		return false, fmt.Errorf("registry ABI missing possessionVerified")
	}
	encoded, err := method.Inputs.Encode(values)
	if err != nil {
		return false, fmt.Errorf("encode possession getter: %w", err)
	}
	decoded, err := DestinationCall(destination, types.StringToAddress(destination.RegistryAddress), append(method.ID(), encoded...))
	if err != nil {
		return false, fmt.Errorf("query possession: %w", err)
	}
	if len(decoded) != 32 || !bytes.Equal(decoded[:31], make([]byte, 31)) || decoded[31] > 1 {
		return false, fmt.Errorf("malformed possession flag")
	}
	return decoded[31] == 1, nil
}

// EncodePossessionCalldata validates the candidate proof against the configured
// scheme before selecting the corresponding on-chain ABI. No key is generated
// or loaded here: the local validator supplies the already-created proof.
func EncodePossessionCalldata(destination *Destination, payload authorization.Membership, proof []byte) ([]byte, error) {
	adapter, err := validateMembershipDestination(destination, payload)
	if err != nil {
		return nil, err
	}
	if err := adapter.VerifyPossession(payload, proof); err != nil {
		return nil, fmt.Errorf("invalid candidate possession proof: %w", err)
	}
	method := possessionABI.Methods["provePossession"]
	values := map[string]interface{}{"validator": ethgo.Address(payload.Validator)}
	if adapter.Scheme() == authorization.SchemeBLS {
		formatted, err := FormatAggregateSignature(destination, proof)
		if err != nil {
			return nil, err
		}
		values["blsKey"] = payload.BLSPublicKey
		values["blsKeyEIP2537"] = payload.BLSPublicKeyEIP2537
		values["possessionProof"] = formatted
	} else {
		method = ecdsaPossessionABI.Methods["provePossession"]
		values["proof"] = proof
	}
	if method == nil {
		return nil, fmt.Errorf("registry ABI missing provePossession")
	}
	encoded, err := method.Inputs.Encode(values)
	if err != nil {
		return nil, fmt.Errorf("encode possession: %w", err)
	}
	return append(append([]byte(nil), method.ID()...), encoded...), nil
}

// EnsureValidatorPossession preserves the BLS entrypoint while sharing the
// same candidate, deployment, transaction and post-state checks.
func EnsureValidatorPossession(destination *Destination, txKey ethgo.Key, payload protocol.MembershipPayload, proof []byte) error {
	adapter, err := destination.AuthorizationAdapter()
	if err != nil {
		return err
	}
	if adapter.Scheme() != authorization.SchemeBLS {
		return fmt.Errorf("BLS possession API requires BLS configuration")
	}
	return EnsureValidatorPossessionAuthorization(destination, txKey, authorization.Membership{
		OriginChainID: payload.OriginChainID, DestinationDomain: payload.DestinationDomain,
		ExpectedSetID: payload.SetID, ValidUntil: payload.ValidUntil, Action: payload.Action,
		Validator: payload.Validator, BLSPublicKey: payload.BLSPublicKey,
		BLSPublicKeyEIP2537: payload.BLSPublicKeyEIP2537,
	}, proof)
}

// EnsureValidatorPossessionAuthorization is idempotent only after on-chain
// confirmation. Admission uses the candidate's existing validator transaction
// key; permissionless contract calls do not substitute an executor identity.
func EnsureValidatorPossessionAuthorization(destination *Destination, txKey ethgo.Key, payload authorization.Membership, proof []byte) error {
	if txKey == nil {
		return fmt.Errorf("destination executor key missing")
	}
	// Admission is always initiated by the candidate's own XGR PoS validator
	// key. Although provePossession is permissionless on-chain, this CLI path
	// must never silently substitute a centrally operated relayer key.
	if types.Address(txKey.Address()) != payload.Validator {
		return fmt.Errorf("destination executor is not the local validator candidate")
	}
	if payload.Action != protocol.ActionAddValidator {
		return fmt.Errorf("validator possession only applies to validator ADD")
	}
	if err := destination.ValidateActivation(); err != nil {
		return err
	}
	calldata, err := EncodePossessionCalldata(destination, payload, proof)
	if err != nil {
		return err
	}
	verified, err := readPossessionVerifiedAuthorization(destination, payload)
	if err != nil {
		return err
	}
	if verified {
		return nil
	}

	if destination.ChainID == 1643 {
		to := types.StringToAddress(destination.RegistryAddress)
		receipt, err := DestinationSubmit(destination, txKey, to, calldata, big.NewInt(0))
		if err != nil {
			return err
		}
		if receipt == nil {
			return fmt.Errorf("native XGR possession receipt unavailable")
		}
		verified, err = readPossessionVerifiedAuthorization(destination, payload)
		if err != nil {
			return err
		}
		if !verified {
			return fmt.Errorf("native XGR possession not recorded")
		}
		return nil
	}
	verifiedRPCURL, endpointErr := selectVerifiedRPCURL(destination)
	if endpointErr != nil {
		return endpointErr
	}
	relayer, err := txrelayer.NewTxRelayer(
		txrelayer.WithIPAddress(verifiedRPCURL),
		txrelayer.WithReceiptTimeout(500*time.Millisecond),
	)
	if err != nil {
		return err
	}
	to := ethgo.Address(types.StringToAddress(destination.RegistryAddress))
	tx := &ethgo.Transaction{
		From: txKey.Address(), To: &to,
		Input: calldata,
		Value: big.NewInt(0), Type: ethgo.TransactionDynamicFee,
	}
	if _, err := prepareAndCheckGas(relayer.Client(), tx, txKey.Address()); err != nil {
		return err
	}
	receipt, err := relayer.SendTransaction(tx, txKey)
	if err != nil {
		return fmt.Errorf("submit validator possession transaction: %w", err)
	}
	if receipt == nil || receipt.Status != uint64(types.ReceiptSuccess) {
		return fmt.Errorf("validator possession transaction reverted")
	}
	if err := waitForFinality(destination, receipt); err != nil {
		return err
	}
	verified, err = readPossessionVerifiedAuthorization(destination, payload)
	if err != nil {
		return err
	}
	if !verified {
		return fmt.Errorf("validator possession not recorded after confirmed receipt")
	}
	return nil
}
