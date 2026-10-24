package evm

import (
	"bytes"
	"fmt"
	"math/big"
	"time"

	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"
	"github.com/umbracle/ethgo/jsonrpc"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/txrelayer"
	"github.com/xgr-network/xgr-node/types"
)

// XGR Interchain ValidatorRegistry V3 requires an on-chain proof of key
// possession before a separately quorum-authorized membership ADD.
var possessionABI = abi.MustNewABI(`[
	{"inputs":[{"name":"validator","type":"address"},{"name":"blsKey","type":"bytes"},{"name":"blsKeyEIP2537","type":"bytes"},{"name":"possessionProof","type":"bytes"}],"name":"provePossession","outputs":[],"stateMutability":"nonpayable","type":"function"},
	{"inputs":[{"name":"keyHash","type":"bytes32"}],"name":"possessionVerified","outputs":[{"name":"","type":"bool"}],"stateMutability":"view","type":"function"}
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

// readPossessionVerified never treats unavailable ABI or RPC reads as false
// proof authorization. A missing V3 mapping is an integration error.
func readPossessionVerified(destination *Destination, payload protocol.MembershipPayload) (bool, error) {
	if err := VerifyDestinationChainID(destination); err != nil {
		return false, err
	}
	hash, err := possessionKeyHash(payload)
	if err != nil {
		return false, err
	}
	method := possessionABI.Methods["possessionVerified"]
	if method == nil {
		return false, fmt.Errorf("registry V3 ABI missing possessionVerified")
	}
	encoded, err := method.Inputs.Encode(map[string]interface{}{
		"keyHash": [32]byte(hash),
	})
	if err != nil {
		return false, fmt.Errorf("encode V3 possession getter: %w", err)
	}
	client, err := jsonrpc.NewClient(destination.RPCURL)
	if err != nil {
		return false, err
	}
	to := ethgo.Address(types.StringToAddress(destination.RegistryAddress))
	callData := append(method.ID(), encoded...)
	raw, err := client.Eth().Call(&ethgo.CallMsg{To: &to, Data: callData}, ethgo.Latest)
	if err != nil {
		return false, fmt.Errorf("query V3 possession: %w", err)
	}
	decoded, err := decodeRPCHex(raw)
	if err != nil {
		return false, err
	}
	if len(decoded) != 32 || !bytes.Equal(decoded[:31], make([]byte, 31)) || decoded[31] > 1 {
		return false, fmt.Errorf("malformed V3 possession flag")
	}
	return decoded[31] == 1, nil
}

// EnsureValidatorPossession is idempotent *after* an on-chain confirmation.
// The node independently supplies the candidate's BLS proof; no relayer
// private key or per-token route setting is involved.
func EnsureValidatorPossession(destination *Destination, txKey ethgo.Key, payload protocol.MembershipPayload, compressedProof []byte) error {
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
		return fmt.Errorf("V3 possession only applies to validator ADD")
	}
	if err := destination.ValidateActivation(); err != nil {
		return err
	}
	bootstrap, err := protocol.MarshalBootstrapPayload(
		payload.OriginChainID, payload.DestinationDomain,
		payload.Validator, payload.BLSPublicKey, payload.BLSPublicKeyEIP2537,
	)
	if err != nil {
		return err
	}
	if err := crypto.VerifyBLSSignatureFromBytes(payload.BLSPublicKey, compressedProof, bootstrap); err != nil {
		return fmt.Errorf("invalid candidate V3 BLS possession proof: %w", err)
	}
	verified, err := readPossessionVerified(destination, payload)
	if err != nil {
		return err
	}
	if verified {
		return nil
	}
	formatted, err := FormatAggregateSignature(destination, compressedProof)
	if err != nil {
		return err
	}
	method := possessionABI.Methods["provePossession"]
	if method == nil {
		return fmt.Errorf("registry V3 ABI missing provePossession")
	}
	encoded, err := method.Inputs.Encode(map[string]interface{}{
		"validator":       ethgo.Address(payload.Validator),
		"blsKey":          payload.BLSPublicKey,
		"blsKeyEIP2537":   payload.BLSPublicKeyEIP2537,
		"possessionProof": formatted,
	})
	if err != nil {
		return fmt.Errorf("encode V3 possession: %w", err)
	}
	relayer, err := txrelayer.NewTxRelayer(
		txrelayer.WithIPAddress(destination.RPCURL),
		txrelayer.WithReceiptTimeout(500*time.Millisecond),
	)
	if err != nil {
		return err
	}
	to := ethgo.Address(types.StringToAddress(destination.RegistryAddress))
	tx := &ethgo.Transaction{
		From: txKey.Address(), To: &to,
		Input: append(method.ID(), encoded...),
		Value: big.NewInt(0), Type: ethgo.TransactionDynamicFee,
	}
	if _, err := prepareAndCheckGas(relayer.Client(), tx, txKey.Address()); err != nil {
		return err
	}
	receipt, err := relayer.SendTransaction(tx, txKey)
	if err != nil {
		return fmt.Errorf("submit V3 possession transaction: %w", err)
	}
	if receipt == nil || receipt.Status != uint64(types.ReceiptSuccess) {
		return fmt.Errorf("V3 possession transaction reverted")
	}
	if err := waitForFinality(destination, receipt); err != nil {
		return err
	}
	verified, err = readPossessionVerified(destination, payload)
	if err != nil {
		return err
	}
	if !verified {
		return fmt.Errorf("V3 possession not recorded after confirmed receipt")
	}
	return nil
}
