package validators

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"reflect"
	"strings"

	"github.com/spf13/cobra"
	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"

	"github.com/xgr-network/xgr-node/command"
	"github.com/xgr-network/xgr-node/contracts/abis"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/txrelayer"
	"github.com/xgr-network/xgr-node/types"
)

type params struct {
	jsonRPC string
}

func GetCommand() *cobra.Command {
	p := &params{}

	cmd := &cobra.Command{
		Use:   "validators",
		Short: "Show staking contract validator set and stake balances",
		RunE: func(cmd *cobra.Command, _ []string) error {
			outputter := command.InitializeOutputter(cmd)
			defer outputter.WriteOutput()

			relayer, err := txrelayer.NewTxRelayer(txrelayer.WithIPAddress(p.jsonRPC))
			if err != nil {
				outputter.SetError(err)
				return nil
			}

			minV, err := callUint(relayer, "minNumValidators")
			if err != nil {
				outputter.SetError(fmt.Errorf("failed to query minNumValidators: %w", err))
				return nil
			}

			maxV, err := callUint(relayer, "maxNumValidators")
			if err != nil {
				outputter.SetError(fmt.Errorf("failed to query maxNumValidators: %w", err))
				return nil
			}

			thr, err := callUint(relayer, "VALIDATOR_THRESHOLD")
			if err != nil {
				outputter.SetError(fmt.Errorf("failed to query VALIDATOR_THRESHOLD: %w", err))
				return nil
			}

			addrs, err := callAddresses(relayer, "validators")
			if err != nil {
				outputter.SetError(err)
				return nil
			}

			bls, _ := callBytesArray(relayer, "validatorBLSPublicKeys")

			rows := make([]ValidatorRow, 0, len(addrs))
			for i, a := range addrs {
				stakeAmt, err := callAccountStake(relayer, a)
				if err != nil {
					outputter.SetError(err)
					return nil
				}

				row := ValidatorRow{
					Address:  a.String(),
					StakeWei: stakeAmt.String(),
				}
				if i < len(bls) {
					row.BLSPubKeyHex = "0x" + hex.EncodeToString(bls[i])
				}
				rows = append(rows, row)
			}

			outputter.SetCommandResult(&ValidatorsResult{
				MinValidatorCount:     minV.String(),
				MaxValidatorCount:     maxV.String(),
				ValidatorThresholdWei: thr.String(),
				Validators:            rows,
			})
			return nil
		},
	}

	cmd.Flags().StringVar(&p.jsonRPC, "jsonrpc", "http://127.0.0.1:8545", "JSON-RPC endpoint")
	return cmd
}

func callBytes(relayer txrelayer.TxRelayer, method string, input []byte) ([]byte, error) {
	m := abis.StakingABI.Methods[method]
	if m == nil {
		return nil, fmt.Errorf("staking ABI missing %s", method)
	}

	resp, err := relayer.Call(ethgo.ZeroAddress, ethgo.Address(staking.AddrStakingContract), input)
	if err != nil {
		return nil, err
	}

	b, err := hexToBytes(resp)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func callUint(relayer txrelayer.TxRelayer, method string) (*big.Int, error) {
	m := abis.StakingABI.Methods[method]
	if m == nil {
		return nil, fmt.Errorf("staking ABI missing %s", method)
	}
	b, err := callBytes(relayer, method, m.ID())
	if err != nil {
		return nil, err
	}
	decoded, err := m.Outputs.Decode(b)
	if err != nil {
		return nil, err
	}
	mp, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("decode %s: unexpected type", method)
	}
	v, ok := asBigInt(mp["0"])
	if !ok {
		return nil, fmt.Errorf("decode %s: missing value", method)
	}

	return v, nil
}

func asBigInt(val interface{}) (*big.Int, bool) {
	switch v := val.(type) {
	case *big.Int:
		if v == nil {
			return nil, false
		}

		return v, true
	case uint64:
		return new(big.Int).SetUint64(v), true
	case uint32:
		return new(big.Int).SetUint64(uint64(v)), true
	case uint16:
		return new(big.Int).SetUint64(uint64(v)), true
	case uint8:
		return new(big.Int).SetUint64(uint64(v)), true
	default:
		return nil, false
	}
}

func callAddresses(relayer txrelayer.TxRelayer, method string) ([]types.Address, error) {
	m := abis.StakingABI.Methods[method]
	if m == nil {
		return nil, fmt.Errorf("staking ABI missing %s", method)
	}
	b, err := callBytes(relayer, method, m.ID())
	if err != nil {
		return nil, err
	}
	decoded, err := m.Outputs.Decode(b)
	if err != nil {
		return nil, err
	}
	mp, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("decode %s: unexpected type", method)
	}
	wa, ok := mp["0"].([]ethgo.Address)
	if !ok {
		return nil, fmt.Errorf("decode %s: unexpected array type", method)
	}
	out := make([]types.Address, len(wa))
	for i := range wa {
		out[i] = types.Address(wa[i])
	}
	return out, nil
}

func callBytesArray(relayer txrelayer.TxRelayer, method string) ([][]byte, error) {
	m := abis.StakingABI.Methods[method]
	if m == nil {
		return nil, fmt.Errorf("staking ABI missing %s", method)
	}
	b, err := callBytes(relayer, method, m.ID())
	if err != nil {
		return nil, err
	}
	decoded, err := m.Outputs.Decode(b)
	if err != nil {
		return nil, err
	}
	arrRaw, err := firstOutputValue(m, decoded)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", method, err)
	}
	arr, err := decodeBytesArray(arrRaw)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", method, err)
	}
	return arr, nil
}

func firstOutputValue(method *abi.Method, decoded interface{}) (interface{}, error) {
	mp, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected type")
	}

	if val, ok := mp["0"]; ok {
		return val, nil
	}

	outputs := method.Outputs.TupleElems()
	if len(outputs) > 0 {
		if name := outputs[0].Name; name != "" {
			if val, ok := mp[name]; ok {
				return val, nil
			}
		}
	}

	if len(mp) == 1 {
		for _, val := range mp {
			return val, nil
		}
	}

	return nil, fmt.Errorf("missing output value")
}

func decodeBytesArray(raw interface{}) ([][]byte, error) {
	v := reflect.ValueOf(raw)
	if !v.IsValid() {
		return nil, fmt.Errorf("unexpected bytes[] type")
	}
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		return nil, fmt.Errorf("unexpected bytes[] type")
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
		b, err := hexToBytes(v)
		if err != nil {
			return nil, fmt.Errorf("invalid hex value")
		}

		return b, nil
	}

	rv := reflect.ValueOf(raw)
	if !rv.IsValid() {
		return nil, fmt.Errorf("unexpected item type")
	}

	kind := rv.Kind()
	if kind == reflect.Array || kind == reflect.Slice {
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			b := make([]byte, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				b[i] = byte(rv.Index(i).Uint())
			}

			return b, nil
		}
	}

	return nil, fmt.Errorf("unexpected item type")
}
func callAccountStake(relayer txrelayer.TxRelayer, addr types.Address) (*big.Int, error) {
	m := abis.StakingABI.Methods["accountStake"]
	if m == nil {
		return nil, fmt.Errorf("staking ABI missing accountStake")
	}
	inp, err := m.Inputs.Encode(map[string]interface{}{"account": ethgo.Address(addr)})
	if err != nil {
		return nil, err
	}
	b, err := callBytes(relayer, "accountStake", append(m.ID(), inp...))
	if err != nil {
		return nil, err
	}
	decoded, err := m.Outputs.Decode(b)
	if err != nil {
		return nil, err
	}
	mp, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("decode accountStake: unexpected type")
	}
	v, ok := mp["0"].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("decode accountStake: missing value")
	}
	return v, nil
}

func hexToBytes(s string) ([]byte, error) {
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return []byte{}, nil
	}
	return hex.DecodeString(s)
}
