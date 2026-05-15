package staking

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"

	"github.com/umbracle/ethgo"

	"github.com/umbracle/ethgo/abi"
	"github.com/xgr-network/xgr-node/contracts/abis"
	"github.com/xgr-network/xgr-node/state/runtime"
	"github.com/xgr-network/xgr-node/types"
)

const (
	methodValidators             = "validators"
	methodValidatorBLSPublicKeys = "validatorBLSPublicKeys"
	methodAccountStake           = "accountStake"
	methodMinimumNumValidators   = "minNumValidators"
	methodMaximumNumValidators   = "maxNumValidators"
	methodValidatorThreshold     = "VALIDATOR_THRESHOLD"
	methodValidatorInfo          = "validatorInfo"
	methodStakerInfo             = "stakerInfo"
)

var (
	// staking contract address
	AddrStakingContract = types.StringToAddress("1001")

	// Gas limit used when querying the validator set
	queryGasLimit uint64 = 1000000

	ErrMethodNotFoundInABI = errors.New("method not found in ABI")
	ErrFailedTypeAssertion = errors.New("failed type assertion")
)

// TxQueryHandler is a interface to call view method in the contract
type TxQueryHandler interface {
	Apply(*types.Transaction) (*runtime.ExecutionResult, error)
	GetNonce(types.Address) uint64
	SetNonPayable(nonPayable bool)
}

// decodeWeb3ArrayOfBytes is a helper function to parse the data
// representing array of bytes in contract result
func decodeWeb3ArrayOfBytes(method *abi.Method, result interface{}) ([][]byte, error) {
	mapResult, ok := result.(map[string]interface{})
	if !ok {
		return nil, ErrFailedTypeAssertion
	}

	value, ok := mapResult["0"]
	if !ok {
		outputs := method.Outputs.TupleElems()
		if len(outputs) > 0 {
			if name := outputs[0].Name; name != "" {
				value, ok = mapResult[name]
			}
		}
	}
	if !ok && len(mapResult) == 1 {
		for _, v := range mapResult {
			value = v
			ok = true
		}
	}
	if !ok {
		return nil, ErrFailedTypeAssertion
	}

	bytesArray, err := decodeBytesArray(value)
	if err != nil {
		return nil, ErrFailedTypeAssertion
	}

	return bytesArray, nil
}

func decodeBytesArray(raw interface{}) ([][]byte, error) {
	v := reflect.ValueOf(raw)
	if !v.IsValid() {
		return nil, fmt.Errorf("unexpected value")
	}
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		return nil, fmt.Errorf("unexpected value")
	}

	out := make([][]byte, 0, v.Len())
	for i := 0; i < v.Len(); i++ {
		b, err := toBytes(v.Index(i).Interface())
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}

	return out, nil
}

func toBytes(raw interface{}) ([]byte, error) {
	switch v := raw.(type) {
	case []byte:
		return v, nil
	case string:
		v = strings.TrimPrefix(v, "0x")
		if v == "" {
			return []byte{}, nil
		}
		return hex.DecodeString(v)
	}

	rv := reflect.ValueOf(raw)
	if !rv.IsValid() {
		return nil, fmt.Errorf("unexpected item type")
	}

	if (rv.Kind() == reflect.Array || rv.Kind() == reflect.Slice) && rv.Type().Elem().Kind() == reflect.Uint8 {
		b := make([]byte, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			b[i] = byte(rv.Index(i).Uint())
		}

		return b, nil
	}

	return nil, fmt.Errorf("unexpected item type")
}

// createCallViewTx is a helper function to create a transaction to call view method
func createCallViewTx(
	from types.Address,
	contractAddress types.Address,
	methodID []byte,
	nonce uint64,
) *types.Transaction {
	return &types.Transaction{
		From:     from,
		To:       &contractAddress,
		Input:    methodID,
		Nonce:    nonce,
		Gas:      queryGasLimit,
		Value:    big.NewInt(0),
		GasPrice: big.NewInt(0),
	}
}

// DecodeValidators parses contract call result and returns array of address
func DecodeValidators(method *abi.Method, returnValue []byte) ([]types.Address, error) {
	decodedResults, err := method.Outputs.Decode(returnValue)
	if err != nil {
		return nil, err
	}

	results, ok := decodedResults.(map[string]interface{})
	if !ok {
		return nil, errors.New("failed type assertion from decodedResults to map")
	}

	web3Addresses, ok := results["0"].([]ethgo.Address)

	if !ok {
		return nil, errors.New("failed type assertion from results[0] to []ethgo.Address")
	}

	addresses := make([]types.Address, len(web3Addresses))
	for idx, waddr := range web3Addresses {
		addresses[idx] = types.Address(waddr)
	}

	return addresses, nil
}

// QueryValidators is a helper function to get validator addresses from contract
func QueryValidators(t TxQueryHandler, from types.Address) ([]types.Address, error) {
	method, ok := abis.StakingABI.Methods[methodValidators]
	if !ok {
		return nil, ErrMethodNotFoundInABI
	}

	t.SetNonPayable(true)
	res, err := t.Apply(createCallViewTx(
		from,
		AddrStakingContract,
		method.ID(),
		t.GetNonce(from),
	))

	if err != nil {
		return nil, err
	}

	if res.Failed() {
		return nil, res.Err
	}

	return DecodeValidators(method, res.ReturnValue)
}

// decodeBLSPublicKeys parses contract call result and returns array of bytes
func decodeBLSPublicKeys(
	method *abi.Method,
	returnValue []byte,
) ([][]byte, error) {
	decodedResults, err := method.Outputs.Decode(returnValue)
	if err != nil {
		return nil, err
	}

	blsPublicKeys, err := decodeWeb3ArrayOfBytes(method, decodedResults)
	if err != nil {
		return nil, err
	}

	return blsPublicKeys, nil
}

// QueryBLSPublicKeys is a helper function to get BLS Public Keys from contract
func QueryBLSPublicKeys(t TxQueryHandler, from types.Address) ([][]byte, error) {
	method, ok := abis.StakingABI.Methods[methodValidatorBLSPublicKeys]
	if !ok {
		return nil, ErrMethodNotFoundInABI
	}

	t.SetNonPayable(true)
	res, err := t.Apply(createCallViewTx(
		from,
		AddrStakingContract,
		method.ID(),
		t.GetNonce(from),
	))

	if err != nil {
		return nil, err
	}

	if res.Failed() {
		return nil, res.Err
	}

	return decodeBLSPublicKeys(method, res.ReturnValue)
}

// QueryAccountStake returns account stake amount for the given validator address.
func QueryAccountStake(t TxQueryHandler, from, validator types.Address) (*big.Int, error) {
	method, ok := abis.StakingABI.Methods[methodAccountStake]
	if !ok {
		return nil, ErrMethodNotFoundInABI
	}

	methodInput, err := method.Inputs.Encode(map[string]interface{}{
		"account": ethgo.Address(validator),
	})
	if err != nil {
		return nil, err
	}

	t.SetNonPayable(true)
	res, err := t.Apply(createCallViewTx(
		from,
		AddrStakingContract,
		append(method.ID(), methodInput...),
		t.GetNonce(from),
	))
	if err != nil {
		return nil, err
	}

	if res.Failed() {
		return nil, res.Err
	}

	decodedResults, err := method.Outputs.Decode(res.ReturnValue)
	if err != nil {
		return nil, err
	}

	mapResult, ok := decodedResults.(map[string]interface{})
	if !ok {
		return nil, ErrFailedTypeAssertion
	}

	stake, ok := mapResult["0"].(*big.Int)
	if !ok {
		return nil, ErrFailedTypeAssertion
	}

	return stake, nil
}

// ValidatorInfo is a decoded snapshot of validatorInfo(account).
type ValidatorInfo struct {
	Exists             bool
	Active             bool
	StakedAmount       *big.Int
	DeactivatedAtBlock *big.Int
	BLSPubKey          []byte
}

type StakerInfo struct {
	Exists             bool
	Active             bool
	Amount             *big.Int
	JoinedAtBlock      *big.Int
	DeactivatedAtBlock *big.Int
	Validator          types.Address
}

// QueryValidatorInfo returns validator metadata for the given account.
func QueryValidatorInfo(t TxQueryHandler, from, validator types.Address) (*ValidatorInfo, error) {
	method, ok := abis.StakingABI.Methods[methodValidatorInfo]
	if !ok {
		return nil, ErrMethodNotFoundInABI
	}

	methodInput, err := method.Inputs.Encode(map[string]interface{}{
		"account": ethgo.Address(validator),
	})
	if err != nil {
		return nil, err
	}

	t.SetNonPayable(true)
	res, err := t.Apply(createCallViewTx(
		from,
		AddrStakingContract,
		append(method.ID(), methodInput...),
		t.GetNonce(from),
	))
	if err != nil {
		return nil, err
	}
	if res.Failed() {
		return nil, res.Err
	}

	decodedResults, err := method.Outputs.Decode(res.ReturnValue)
	if err != nil {
		return nil, err
	}
	mapResult, ok := decodedResults.(map[string]interface{})
	if !ok {
		return nil, ErrFailedTypeAssertion
	}

	exists, ok := mapResult["0"].(bool)
	if !ok {
		exists, ok = mapResult["exists"].(bool)
		if !ok {
			return nil, ErrFailedTypeAssertion
		}
	}
	active, ok := mapResult["1"].(bool)
	if !ok {
		active, ok = mapResult["active"].(bool)
		if !ok {
			return nil, ErrFailedTypeAssertion
		}
	}
	stakedAmount, ok := mapResult["2"].(*big.Int)
	if !ok {
		stakedAmount, ok = mapResult["stakedAmount"].(*big.Int)
		if !ok {
			return nil, ErrFailedTypeAssertion
		}
	}
	deact, ok := mapResult["3"].(*big.Int)
	if !ok {
		deact, ok = mapResult["deactivatedAtBlock"].(*big.Int)
		if !ok {
			return nil, ErrFailedTypeAssertion
		}
	}
	bls, ok := mapResult["4"].([]byte)
	if !ok {
		bls, ok = mapResult["blsPubKey"].([]byte)
		if !ok {
			return nil, ErrFailedTypeAssertion
		}
	}

	return &ValidatorInfo{
		Exists:             exists,
		Active:             active,
		StakedAmount:       stakedAmount,
		DeactivatedAtBlock: deact,
		BLSPubKey:          bls,
	}, nil
}

func QueryStakerInfo(t TxQueryHandler, from, account types.Address) (*StakerInfo, error) {
	method, ok := abis.StakingABI.Methods[methodStakerInfo]
	if !ok {
		return nil, ErrMethodNotFoundInABI
	}
	methodInput, err := method.Inputs.Encode(map[string]interface{}{"account": ethgo.Address(account)})
	if err != nil {
		return nil, err
	}

	t.SetNonPayable(true)
	res, err := t.Apply(createCallViewTx(from, AddrStakingContract, append(method.ID(), methodInput...), t.GetNonce(from)))
	if err != nil {
		return nil, err
	}
	if res.Failed() {
		return nil, res.Err
	}

	decodedResults, err := method.Outputs.Decode(res.ReturnValue)
	if err != nil {
		return nil, err
	}
	mp, ok := decodedResults.(map[string]interface{})
	if !ok {
		return nil, ErrFailedTypeAssertion
	}
	raw, ok := mp["0"].(map[string]interface{})
	if !ok {
		return nil, ErrFailedTypeAssertion
	}
	out := &StakerInfo{}
	if v, ok := raw["exists"].(bool); ok {
		out.Exists = v
	}
	if v, ok := raw["active"].(bool); ok {
		out.Active = v
	}
	if v, ok := raw["amount"].(*big.Int); ok {
		out.Amount = v
	} else {
		out.Amount = new(big.Int)
	}
	if v, ok := raw["joinedAtBlock"].(*big.Int); ok {
		out.JoinedAtBlock = v
	} else {
		out.JoinedAtBlock = new(big.Int)
	}
	if v, ok := raw["deactivatedAtBlock"].(*big.Int); ok {
		out.DeactivatedAtBlock = v
	} else {
		out.DeactivatedAtBlock = new(big.Int)
	}
	if v, ok := raw["validator"].(ethgo.Address); ok {
		out.Validator = types.Address(v)
	}

	return out, nil
}
func queryUint256NoArgs(t TxQueryHandler, from types.Address, methodName string) (*big.Int, error) {
	method, ok := abis.StakingABI.Methods[methodName]
	if !ok {
		return nil, ErrMethodNotFoundInABI
	}

	t.SetNonPayable(true)
	res, err := t.Apply(createCallViewTx(
		from,
		AddrStakingContract,
		method.ID(),
		t.GetNonce(from),
	))
	if err != nil {
		return nil, err
	}
	if res.Failed() {
		return nil, res.Err
	}

	decodedResults, err := method.Outputs.Decode(res.ReturnValue)
	if err != nil {
		return nil, err
	}
	mapResult, ok := decodedResults.(map[string]interface{})
	if !ok {
		return nil, ErrFailedTypeAssertion
	}
	if val, ok := mapResult["0"].(*big.Int); ok {
		return val, nil
	}
	if valU64, ok := mapResult["0"].(uint64); ok {
		return new(big.Int).SetUint64(valU64), nil
	}
	if valU, ok := mapResult["0"].(uint); ok {
		return new(big.Int).SetUint64(uint64(valU)), nil
	}
	return nil, ErrFailedTypeAssertion
}

// QueryMinimumNumValidators returns the contract's minimum validator count.
func QueryMinimumNumValidators(t TxQueryHandler, from types.Address) (uint64, error) {
	v, err := queryUint256NoArgs(t, from, methodMinimumNumValidators)
	if err != nil {
		return 0, err
	}
	return v.Uint64(), nil
}

// QueryMaximumNumValidators returns the contract's maximum validator count.
func QueryMaximumNumValidators(t TxQueryHandler, from types.Address) (uint64, error) {
	v, err := queryUint256NoArgs(t, from, methodMaximumNumValidators)
	if err != nil {
		return 0, err
	}
	return v.Uint64(), nil
}

// QueryValidatorThreshold returns the stake threshold (in wei) required to be considered eligible.
func QueryValidatorThreshold(t TxQueryHandler, from types.Address) (*big.Int, error) {
	return queryUint256NoArgs(t, from, methodValidatorThreshold)
}
