package evm

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/jsonrpc"

	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

const GetValidatorStatusSignature = "getValidatorStatus(address)"

type ValidatorStatus struct {
	Active bool
	SetID  uint64
}

// GetValidatorStatus reads the canonical XGR interchain membership state
// directly from an EVM destination contract. No transport adapter or relayer
// participates in this read.
func GetValidatorStatus(
	rpcURL string,
	registryAddress string,
	validator types.Address,
) (*ValidatorStatus, error) {
	if strings.TrimSpace(rpcURL) == "" {
		return nil, fmt.Errorf("destination EVM RPC is required")
	}
	if err := types.IsValidAddress(registryAddress); err != nil {
		return nil, fmt.Errorf("invalid interchain registry address: %w", err)
	}

	registry := types.StringToAddress(registryAddress)
	if registry == types.ZeroAddress {
		return nil, fmt.Errorf("interchain registry address must not be zero")
	}

	client, err := jsonrpc.NewClient(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("create destination EVM RPC client: %w", err)
	}

	input := encodeGetValidatorStatusCall(validator)
	to := ethgo.Address(registry)
	raw, err := client.Eth().Call(&ethgo.CallMsg{
		To:   &to,
		Data: input,
	}, ethgo.Latest)
	if err != nil {
		return nil, fmt.Errorf("call getValidatorStatus on destination registry: %w", err)
	}

	return decodeValidatorStatusResult(raw)
}

func encodeGetValidatorStatusCall(validator types.Address) []byte {
	selector := crypto.Keccak256([]byte(GetValidatorStatusSignature))[:4]
	input := make([]byte, 4+32)
	copy(input[:4], selector)
	copy(input[4+12:], validator.Bytes())

	return input
}

func decodeValidatorStatusResult(raw string) (*ValidatorStatus, error) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "0x")
	if raw == "" {
		return nil, fmt.Errorf("empty getValidatorStatus response")
	}

	decoded, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode getValidatorStatus response: %w", err)
	}
	if len(decoded) != 64 {
		return nil, fmt.Errorf("invalid getValidatorStatus response length: got %d want 64", len(decoded))
	}

	for _, b := range decoded[:31] {
		if b != 0 {
			return nil, fmt.Errorf("invalid ABI bool encoding in getValidatorStatus response")
		}
	}
	if decoded[31] != 0 && decoded[31] != 1 {
		return nil, fmt.Errorf("invalid ABI bool value in getValidatorStatus response")
	}

	for _, b := range decoded[32:56] {
		if b != 0 {
			return nil, fmt.Errorf("setId does not fit uint64")
		}
	}

	return &ValidatorStatus{
		Active: decoded[31] == 1,
		SetID:  binary.BigEndian.Uint64(decoded[56:64]),
	}, nil
}
