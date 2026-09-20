package evm

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"time"

	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"
	"github.com/umbracle/ethgo/jsonrpc"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/txrelayer"
	"github.com/xgr-network/xgr-node/types"
)

const registryJSONABI = `[
	{"inputs":[{"internalType":"address","name":"validator","type":"address"}],"name":"getValidatorStatus","outputs":[{"internalType":"bool","name":"active","type":"bool"},{"internalType":"uint64","name":"currentSetId","type":"uint64"}],"stateMutability":"view","type":"function"},
	{"inputs":[{"internalType":"address","name":"validator","type":"address"}],"name":"getValidator","outputs":[{"internalType":"bool","name":"active","type":"bool"},{"internalType":"bytes","name":"blsPublicKey","type":"bytes"},{"internalType":"uint256","name":"reserveWei","type":"uint256"}],"stateMutability":"view","type":"function"},
	{"inputs":[],"name":"getValidatorSet","outputs":[{"internalType":"address[]","name":"validators","type":"address[]"},{"internalType":"bytes[]","name":"blsPublicKeys","type":"bytes[]"},{"internalType":"uint64","name":"currentSetId","type":"uint64"}],"stateMutability":"view","type":"function"},
	{"inputs":[],"name":"minimumDeactivationReserveWei","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},
	{"inputs":[],"name":"maxExecutorReimbursementWei","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},
	{"inputs":[{"components":[{"internalType":"uint64","name":"expectedSetId","type":"uint64"},{"internalType":"uint64","name":"validUntil","type":"uint64"},{"internalType":"uint8","name":"action","type":"uint8"},{"internalType":"address","name":"validator","type":"address"},{"internalType":"bytes","name":"blsPublicKey","type":"bytes"},{"internalType":"bytes","name":"blsPublicKeyEIP2537","type":"bytes"}],"internalType":"struct XGRInterchainValidatorRegistry.MembershipTransition","name":"transition","type":"tuple"},{"internalType":"bytes","name":"signerBitmap","type":"bytes"},{"internalType":"bytes","name":"aggregateSignature","type":"bytes"}],"name":"applyMembership","outputs":[],"stateMutability":"payable","type":"function"}
]`

var registryABI = abi.MustNewABI(registryJSONABI)

type ValidatorSet struct {
	Validators    []types.Address
	BLSPublicKeys [][]byte
	SetID         uint64
}

type SubmitResult struct {
	TxHash  string
	SetID   uint64
	Active  bool
	Receipt *ethgo.Receipt
}

type ValidatorDetails struct {
	Active       bool
	BLSPublicKey []byte
	ReserveWei   *big.Int
}

func VerifyDestinationChainID(destination *Destination) error {
	if destination == nil {
		return fmt.Errorf("destination is nil")
	}
	if err := destination.ValidateMembershipRead(); err != nil {
		return err
	}

	client, err := jsonrpc.NewClient(destination.RPCURL)
	if err != nil {
		return fmt.Errorf("create destination RPC client: %w", err)
	}
	got, err := client.Eth().ChainID()
	if err != nil {
		return fmt.Errorf("query destination chain id: %w", err)
	}
	if got == nil || !got.IsUint64() {
		return fmt.Errorf("destination chain id does not fit uint64")
	}
	if got.Uint64() != destination.ChainID {
		return fmt.Errorf("destination chain id mismatch: configured=%d rpc=%d", destination.ChainID, got.Uint64())
	}

	return nil
}

func GetValidatorSet(destination *Destination) (*ValidatorSet, error) {
	if err := VerifyDestinationChainID(destination); err != nil {
		return nil, err
	}

	client, err := jsonrpc.NewClient(destination.RPCURL)
	if err != nil {
		return nil, err
	}
	method := registryABI.Methods["getValidatorSet"]
	if method == nil {
		return nil, fmt.Errorf("registry ABI missing getValidatorSet")
	}

	to := ethgo.Address(types.StringToAddress(destination.RegistryAddress))
	raw, err := client.Eth().Call(&ethgo.CallMsg{To: &to, Data: method.ID()}, ethgo.Latest)
	if err != nil {
		return nil, fmt.Errorf("call getValidatorSet: %w", err)
	}
	decodedRaw, err := decodeRPCHex(raw)
	if err != nil {
		return nil, err
	}
	decoded, err := method.Outputs.Decode(decodedRaw)
	if err != nil {
		return nil, fmt.Errorf("decode getValidatorSet: %w", err)
	}
	values, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("decode getValidatorSet: unexpected type")
	}

	addressesValue, ok := firstDecoded(values, "validators", "0")
	if !ok {
		return nil, fmt.Errorf("decode getValidatorSet: missing validators")
	}
	keysValue, ok := firstDecoded(values, "blsPublicKeys", "1")
	if !ok {
		return nil, fmt.Errorf("decode getValidatorSet: missing BLS public keys")
	}
	setIDValue, ok := firstDecoded(values, "currentSetId", "2")
	if !ok {
		return nil, fmt.Errorf("decode getValidatorSet: missing set id")
	}

	addresses, err := decodeAddressArray(addressesValue)
	if err != nil {
		return nil, err
	}
	keys, err := decodeBytesArrayValue(keysValue)
	if err != nil {
		return nil, err
	}
	setID, ok := uint64Value(setIDValue)
	if !ok {
		return nil, fmt.Errorf("decode getValidatorSet: invalid set id")
	}
	if len(addresses) != len(keys) {
		return nil, fmt.Errorf("decode getValidatorSet: validator/key length mismatch %d/%d", len(addresses), len(keys))
	}

	return &ValidatorSet{Validators: addresses, BLSPublicKeys: keys, SetID: setID}, nil
}

func GetValidatorStatusForDestination(destination *Destination, validator types.Address) (*ValidatorStatus, error) {
	if err := VerifyDestinationChainID(destination); err != nil {
		return nil, err
	}
	return GetValidatorStatus(destination.RPCURL, destination.RegistryAddress, validator)
}

func GetValidator(destination *Destination, validator types.Address) (*ValidatorDetails, error) {
	if err := VerifyDestinationChainID(destination); err != nil {
		return nil, err
	}
	client, err := jsonrpc.NewClient(destination.RPCURL)
	if err != nil {
		return nil, err
	}
	method := registryABI.Methods["getValidator"]
	if method == nil {
		return nil, fmt.Errorf("registry ABI missing getValidator")
	}
	to := ethgo.Address(types.StringToAddress(destination.RegistryAddress))
	input, err := method.Inputs.Encode(map[string]interface{}{
		"validator": ethgo.Address(validator),
	})
	if err != nil {
		return nil, fmt.Errorf("encode getValidator: %w", err)
	}
	raw, err := client.Eth().Call(&ethgo.CallMsg{To: &to, Data: append(method.ID(), input...)}, ethgo.Latest)
	if err != nil {
		return nil, fmt.Errorf("call getValidator: %w", err)
	}
	decodedRaw, err := decodeRPCHex(raw)
	if err != nil {
		return nil, err
	}
	decoded, err := method.Outputs.Decode(decodedRaw)
	if err != nil {
		return nil, fmt.Errorf("decode getValidator: %w", err)
	}
	values, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("decode getValidator: unexpected type")
	}
	activeValue, ok := firstDecoded(values, "active", "0")
	if !ok {
		return nil, fmt.Errorf("decode getValidator: missing active")
	}
	active, ok := activeValue.(bool)
	if !ok {
		return nil, fmt.Errorf("decode getValidator: invalid active")
	}
	keyValue, ok := firstDecoded(values, "blsPublicKey", "1")
	if !ok {
		return nil, fmt.Errorf("decode getValidator: missing BLS public key")
	}
	key, ok := keyValue.([]byte)
	if !ok {
		return nil, fmt.Errorf("decode getValidator: invalid BLS public key")
	}
	reserveValue, ok := firstDecoded(values, "reserveWei", "2")
	if !ok {
		return nil, fmt.Errorf("decode getValidator: missing reserve")
	}
	reserve, ok := reserveValue.(*big.Int)
	if !ok || reserve == nil || reserve.Sign() < 0 {
		return nil, fmt.Errorf("decode getValidator: invalid reserve")
	}
	return &ValidatorDetails{
		Active: active,
		BLSPublicKey: append([]byte(nil), key...),
		ReserveWei: new(big.Int).Set(reserve),
	}, nil
}

func MinimumDeactivationReserve(destination *Destination) (*big.Int, error) {
	if err := VerifyDestinationChainID(destination); err != nil {
		return nil, err
	}
	client, err := jsonrpc.NewClient(destination.RPCURL)
	if err != nil {
		return nil, err
	}
	method := registryABI.Methods["minimumDeactivationReserveWei"]
	if method == nil {
		return nil, fmt.Errorf("registry ABI missing minimumDeactivationReserveWei")
	}
	to := ethgo.Address(types.StringToAddress(destination.RegistryAddress))
	raw, err := client.Eth().Call(&ethgo.CallMsg{To: &to, Data: method.ID()}, ethgo.Latest)
	if err != nil {
		return nil, fmt.Errorf("call minimumDeactivationReserveWei: %w", err)
	}
	b, err := decodeRPCHex(raw)
	if err != nil {
		return nil, err
	}
	decoded, err := method.Outputs.Decode(b)
	if err != nil {
		return nil, err
	}
	values, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("decode minimumDeactivationReserveWei: unexpected type")
	}
	value, ok := firstDecoded(values, "", "0")
	if !ok {
		for _, v := range values {
			value, ok = v, true
			break
		}
	}
	reserve, ok := value.(*big.Int)
	if !ok || reserve == nil || reserve.Sign() < 0 {
		return nil, fmt.Errorf("decode minimumDeactivationReserveWei: invalid value")
	}
	return new(big.Int).Set(reserve), nil
}

func MaximumExecutorReimbursement(destination *Destination) (*big.Int, error) {
	if err := VerifyDestinationChainID(destination); err != nil {
		return nil, err
	}
	client, err := jsonrpc.NewClient(destination.RPCURL)
	if err != nil {
		return nil, err
	}
	method := registryABI.Methods["maxExecutorReimbursementWei"]
	if method == nil {
		return nil, fmt.Errorf("registry ABI missing maxExecutorReimbursementWei")
	}
	to := ethgo.Address(types.StringToAddress(destination.RegistryAddress))
	raw, err := client.Eth().Call(&ethgo.CallMsg{To: &to, Data: method.ID()}, ethgo.Latest)
	if err != nil {
		return nil, fmt.Errorf("call maxExecutorReimbursementWei: %w", err)
	}
	b, err := decodeRPCHex(raw)
	if err != nil {
		return nil, err
	}
	decoded, err := method.Outputs.Decode(b)
	if err != nil {
		return nil, err
	}
	values, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("decode maxExecutorReimbursementWei: unexpected type")
	}
	value, ok := firstDecoded(values, "", "0")
	if !ok {
		for _, v := range values {
			value, ok = v, true
			break
		}
	}
	capWei, ok := value.(*big.Int)
	if !ok || capWei == nil || capWei.Sign() < 0 {
		return nil, fmt.Errorf("decode maxExecutorReimbursementWei: invalid value")
	}
	return new(big.Int).Set(capWei), nil
}

// SubmitMembership sends the single destination-chain state-changing transaction.
// ADD locks reserveWei in the registry. REMOVE is non-payable and consumes the
// target validator's already-locked reserve for executor reimbursement.
func SubmitMembership(
	destination *Destination,
	key ethgo.Key,
	payload protocol.MembershipPayload,
	signerBitmap *big.Int,
	aggregateSignature []byte,
	reserveWei *big.Int,
) (*SubmitResult, error) {
	if key == nil {
		return nil, fmt.Errorf("destination transaction key is nil")
	}
	if signerBitmap == nil || signerBitmap.Sign() <= 0 {
		return nil, fmt.Errorf("signer bitmap is empty")
	}
	if len(aggregateSignature) == 0 {
		return nil, fmt.Errorf("aggregate signature is empty")
	}
	if err := destination.ValidateSubmission(); err != nil {
		return nil, err
	}
	if err := VerifyDestinationChainID(destination); err != nil {
		return nil, err
	}
	if payload.DestinationDomain != destination.Domain {
		return nil, fmt.Errorf("payload destination domain mismatch: payload=%d configured=%d", payload.DestinationDomain, destination.Domain)
	}

	if payload.ValidUntil < uint64(time.Now().Unix()) {
		return nil, fmt.Errorf("membership payload expired before destination submission")
	}
	message, err := payload.MarshalBinary()
	if err != nil {
		return nil, err
	}

	currentSet, err := GetValidatorSet(destination)
	if err != nil {
		return nil, err
	}
	if currentSet.SetID != payload.SetID {
		return nil, fmt.Errorf("stale destination set id before submission: payload=%d current=%d", payload.SetID, currentSet.SetID)
	}
	if err := protocol.VerifyAggregatedQuorum(currentSet.BLSPublicKeys, signerBitmap, aggregateSignature, message); err != nil {
		return nil, fmt.Errorf("local aggregate quorum preflight failed: %w", err)
	}

	value := big.NewInt(0)
	switch payload.Action {
	case protocol.ActionAddValidator:
		if err := destination.ValidateActivation(); err != nil {
			return nil, err
		}
		if reserveWei == nil {
			return nil, fmt.Errorf("activation reserve is required")
		}
		minimum, err := MinimumDeactivationReserve(destination)
		if err != nil {
			return nil, err
		}
		if reserveWei.Cmp(minimum) < 0 {
			return nil, fmt.Errorf("activation reserve below registry minimum: have=%s need=%s", reserveWei, minimum)
		}
		value = new(big.Int).Set(reserveWei)
	case protocol.ActionRemoveValidator:
		if reserveWei != nil && reserveWei.Sign() != 0 {
			return nil, fmt.Errorf("remove transition must not attach value")
		}
	default:
		return nil, fmt.Errorf("unsupported membership action %d", payload.Action)
	}

	eip2537Signature, err := crypto.BLSSignatureToEIP2537(aggregateSignature)
	if err != nil {
		return nil, fmt.Errorf("convert aggregate signature to EIP-2537: %w", err)
	}

	method := registryABI.Methods["applyMembership"]
	if method == nil {
		return nil, fmt.Errorf("registry ABI missing applyMembership")
	}
	input, err := method.Inputs.Encode(map[string]interface{}{
		"transition": map[string]interface{}{
			"expectedSetId":       new(big.Int).SetUint64(payload.SetID),
			"validUntil":          new(big.Int).SetUint64(payload.ValidUntil),
			"action":              uint8(payload.Action),
			"validator":           ethgo.Address(payload.Validator),
			"blsPublicKey":        payload.BLSPublicKey,
			"blsPublicKeyEIP2537": payload.BLSPublicKeyEIP2537,
		},
		"signerBitmap":       signerBitmap.Bytes(),
		"aggregateSignature": eip2537Signature,
	})
	if err != nil {
		return nil, fmt.Errorf("encode applyMembership: %w", err)
	}

	relayer, err := txrelayer.NewTxRelayer(
		txrelayer.WithIPAddress(destination.RPCURL),
		txrelayer.WithReceiptTimeout(500*time.Millisecond),
	)
	if err != nil {
		return nil, err
	}
	to := ethgo.Address(types.StringToAddress(destination.RegistryAddress))
	tx := &ethgo.Transaction{
		From:  key.Address(),
		To:    &to,
		Input: append(method.ID(), input...),
		Value: value,
		Type:  ethgo.TransactionDynamicFee,
	}
	estimatedGasCost, err := prepareAndCheckGas(relayer.Client(), tx, key.Address())
	if err != nil {
		return nil, err
	}
	if payload.Action == protocol.ActionRemoveValidator {
		details, err := GetValidator(destination, payload.Validator)
		if err != nil {
			return nil, fmt.Errorf("read removal reserve before submission: %w", err)
		}
		capWei, err := MaximumExecutorReimbursement(destination)
		if err != nil {
			return nil, fmt.Errorf("read executor reimbursement cap: %w", err)
		}
		available := new(big.Int).Set(details.ReserveWei)
		if capWei.Cmp(available) < 0 {
			available.Set(capWei)
		}
		if estimatedGasCost.Cmp(available) > 0 {
			return nil, fmt.Errorf(
				"estimated removal gas cost exceeds reimbursable amount: cost=%s reimbursable=%s",
				estimatedGasCost, available,
			)
		}
	}
	receipt, err := relayer.SendTransaction(tx, key)
	if err != nil {
		return nil, fmt.Errorf("submit destination membership transition: %w", err)
	}
	if receipt == nil || receipt.Status != uint64(types.ReceiptSuccess) {
		return nil, fmt.Errorf("destination membership transition reverted")
	}
	if err := waitForConfirmations(destination, receipt); err != nil {
		return nil, err
	}

	status, err := GetValidatorStatusForDestination(destination, payload.Validator)
	if err != nil {
		return nil, fmt.Errorf("verify destination membership state after receipt: %w", err)
	}
	wantActive := payload.Action == protocol.ActionAddValidator
	if status.Active != wantActive {
		return nil, fmt.Errorf("destination post-state mismatch: active=%t want=%t", status.Active, wantActive)
	}
	if status.SetID != payload.SetID+1 {
		return nil, fmt.Errorf("destination set id did not advance exactly once: got=%d want=%d", status.SetID, payload.SetID+1)
	}

	return &SubmitResult{
		TxHash:  receipt.TransactionHash.String(),
		SetID:   status.SetID,
		Active:  status.Active,
		Receipt: receipt,
	}, nil
}


func prepareAndCheckGas(client *jsonrpc.Client, tx *ethgo.Transaction, sender ethgo.Address) (*big.Int, error) {
	if client == nil || tx == nil {
		return nil, fmt.Errorf("destination gas preflight requires client and transaction")
	}

	estimate, err := client.Eth().EstimateGas(&ethgo.CallMsg{
		From:  sender,
		To:    tx.To,
		Data:  tx.Input,
		Value: tx.Value,
	})
	if err != nil {
		return nil, fmt.Errorf("estimate destination membership gas: %w", err)
	}
	if estimate == 0 {
		return nil, fmt.Errorf("destination membership gas estimate is zero")
	}
	// Match TxRelayer's existing 100%% safety increase so the balance preflight
	// and the transaction that is actually signed use the same gas ceiling.
	tx.Gas = estimate * 2

	var feeCeiling *big.Int
	priority, priorityErr := client.Eth().MaxPriorityFeePerGas()
	feeHistory, historyErr := client.Eth().FeeHistory(1, ethgo.Latest, nil)
	if priorityErr == nil && historyErr == nil && priority != nil &&
		feeHistory != nil && len(feeHistory.BaseFee) > 0 &&
		feeHistory.BaseFee[len(feeHistory.BaseFee)-1] != nil {
		baseFee := feeHistory.BaseFee[len(feeHistory.BaseFee)-1]
		tx.Type = ethgo.TransactionDynamicFee
		tx.MaxPriorityFeePerGas = new(big.Int).Mul(priority, big.NewInt(2))
		tx.MaxFeePerGas = new(big.Int).Mul(
			new(big.Int).Add(new(big.Int).Set(baseFee), priority),
			big.NewInt(2),
		)
		feeCeiling = new(big.Int).Set(tx.MaxFeePerGas)
	} else {
		gasPrice, err := client.Eth().GasPrice()
		if err != nil {
			return nil, fmt.Errorf(
				"destination supports neither EIP-1559 fee discovery nor legacy gas price: %w",
				err,
			)
		}
		if gasPrice > ^uint64(0)/2 {
			return nil, fmt.Errorf("destination legacy gas price overflows safety margin")
		}
		tx.Type = ethgo.TransactionLegacy
		tx.GasPrice = gasPrice * 2
		tx.MaxPriorityFeePerGas = nil
		tx.MaxFeePerGas = nil
		feeCeiling = new(big.Int).SetUint64(tx.GasPrice)
	}

	requiredGas := new(big.Int).Mul(new(big.Int).SetUint64(tx.Gas), feeCeiling)
	required := new(big.Int).Set(requiredGas)
	if tx.Value != nil {
		required.Add(required, tx.Value)
	}

	balance, err := client.Eth().GetBalance(sender, ethgo.Latest)
	if err != nil {
		return nil, fmt.Errorf("query destination gas balance: %w", err)
	}
	if balance.Cmp(required) < 0 {
		return nil, fmt.Errorf(
			"insufficient destination gas balance: have=%s needAtLeast=%s (txGas=%s value=%s)",
			balance,
			required,
			requiredGas,
			func() *big.Int {
				if tx.Value == nil {
					return big.NewInt(0)
				}
				return tx.Value
			}(),
		)
	}

	// Estimate the reimbursable execution cost using the estimated gas rather
	// than the doubled transaction gas limit. MaxFeePerGas already contains the
	// fee safety margin used for submission.
	estimatedCost := new(big.Int).Mul(new(big.Int).SetUint64(estimate), feeCeiling)
	return estimatedCost, nil
}

func firstDecoded(values map[string]interface{}, keys ...string) (interface{}, bool) {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func uint64Value(value interface{}) (uint64, bool) {
	switch v := value.(type) {
	case *big.Int:
		return v.Uint64(), v.Sign() >= 0 && v.IsUint64()
	case uint64:
		return v, true
	case uint32:
		return uint64(v), true
	case uint:
		return uint64(v), true
	default:
		return 0, false
	}
}

func decodeAddressArray(value interface{}) ([]types.Address, error) {
	switch v := value.(type) {
	case []ethgo.Address:
		out := make([]types.Address, len(v))
		for i := range v {
			out[i] = types.Address(v[i])
		}
		return out, nil
	case []types.Address:
		return append([]types.Address(nil), v...), nil
	}

	rv := reflect.ValueOf(value)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil, fmt.Errorf("decode validator array: unexpected type %T", value)
	}
	out := make([]types.Address, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		item := rv.Index(i).Interface()
		switch addr := item.(type) {
		case ethgo.Address:
			out = append(out, types.Address(addr))
		case types.Address:
			out = append(out, addr)
		default:
			return nil, fmt.Errorf("decode validator array: unexpected item %T", item)
		}
	}
	return out, nil
}

func decodeBytesArrayValue(value interface{}) ([][]byte, error) {
	if v, ok := value.([][]byte); ok {
		out := make([][]byte, len(v))
		for i := range v {
			out[i] = append([]byte(nil), v[i]...)
		}
		return out, nil
	}

	rv := reflect.ValueOf(value)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil, fmt.Errorf("decode bytes array: unexpected type %T", value)
	}
	out := make([][]byte, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		item, ok := rv.Index(i).Interface().([]byte)
		if !ok {
			return nil, fmt.Errorf("decode bytes array: unexpected item %T", rv.Index(i).Interface())
		}
		out = append(out, append([]byte(nil), item...))
	}
	return out, nil
}
func decodeRPCHex(value string) ([]byte, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	if value == "" {
		return []byte{}, nil
	}
	return hex.DecodeString(value)
}


func waitForConfirmations(destination *Destination, receipt *ethgo.Receipt) error {
	if destination == nil || destination.Confirmations == 0 || receipt == nil {
		return fmt.Errorf("destination confirmation policy or receipt is invalid")
	}
	if err := VerifyDestinationChainID(destination); err != nil {
		return err
	}
	client, err := jsonrpc.NewClient(destination.RPCURL)
	if err != nil {
		return fmt.Errorf("create destination RPC client for confirmations: %w", err)
	}

	target := receipt.BlockNumber + destination.Confirmations
	deadline := time.Now().Add(5 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		height, err := client.Eth().BlockNumber()
		if err == nil && height >= target {
			confirmed, receiptErr := client.Eth().GetTransactionReceipt(receipt.TransactionHash)
			if receiptErr != nil {
				return fmt.Errorf("re-read destination receipt after confirmations: %w", receiptErr)
			}
			if confirmed == nil || confirmed.Status != uint64(types.ReceiptSuccess) {
				return fmt.Errorf("destination receipt is no longer canonical after confirmations")
			}
			if confirmed.BlockNumber != receipt.BlockNumber {
				return fmt.Errorf("destination receipt block changed after confirmations: got=%d want=%d", confirmed.BlockNumber, receipt.BlockNumber)
			}
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("timed out waiting for destination confirmations: %w", err)
			}
			return fmt.Errorf("timed out waiting for destination confirmations: have block %d need %d", height, target)
		}
		<-ticker.C
	}
}
