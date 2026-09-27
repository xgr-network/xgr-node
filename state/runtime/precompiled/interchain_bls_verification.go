package precompiled

import (
	"bytes"
	"errors"
	"math/big"

	"github.com/umbracle/ethgo/abi"

	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/crypto"
	interchainVerification "github.com/xgr-network/xgr-node/interchain/verification"
	"github.com/xgr-network/xgr-node/state/runtime"
	"github.com/xgr-network/xgr-node/types"
)

const (
	interchainBLSMaxInputBytes   = 64 * 1024
	interchainBLSMaxMessageBytes = 4 * 1024
	interchainBLSMaxValidators   = 512

	interchainBLSBaseGas    uint64 = 200_000
	interchainBLSGasPerByte uint64 = 60
)

var (
	errInterchainBLSInvalidInput = errors.New("invalid interchain BLS verification input")
	interchainBLSInputABIType    = abi.MustNewType("tuple(bytes, bytes[], bytes, bytes)")
	interchainBLSVerifySelector  = crypto.Keccak256([]byte("verify(bytes,bytes[],bytes,bytes)"))[:4]
)

// interchainBLSVerification verifies the canonical XGR BLS12-381 aggregate
// signature format used by native interchain attestations.
//
// ABI input:
//
//	verify(bytes message, bytes[] compressedPublicKeys, bytes signerBitmap, bytes compressedAggregateSignature)
//
// The precompile intentionally exposes standard Solidity function-call semantics:
// 4-byte selector followed by ABI encoded arguments.
// The signer bitmap is a canonical big-endian validator-index bitmap.
// Return value is ABI-encoded bool.
type interchainBLSVerification struct{}

func (c *interchainBLSVerification) gas(input []byte, _ *chain.ForksInTime) uint64 {
	n := len(input)
	if n > interchainBLSMaxInputBytes {
		n = interchainBLSMaxInputBytes
	}
	return interchainBLSBaseGas + uint64(n)*interchainBLSGasPerByte
}

func (c *interchainBLSVerification) run(
	input []byte,
	_ types.Address,
	_ runtime.Host,
) ([]byte, error) {
	if len(input) < 4 || len(input) > interchainBLSMaxInputBytes {
		return nil, errInterchainBLSInvalidInput
	}
	if !bytes.Equal(input[:4], interchainBLSVerifySelector) {
		return nil, errInterchainBLSInvalidInput
	}

	raw, err := abi.Decode(interchainBLSInputABIType, input[4:])
	if err != nil {
		return nil, err
	}
	data, ok := raw.(map[string]interface{})
	if !ok || len(data) != 4 {
		return nil, errInterchainBLSInvalidInput
	}

	message, ok := data["0"].([]byte)
	if !ok || len(message) == 0 || len(message) > interchainBLSMaxMessageBytes {
		return abiBoolFalse, nil
	}
	publicKeys, ok := data["1"].([][]byte)
	if !ok || len(publicKeys) == 0 || len(publicKeys) > interchainBLSMaxValidators {
		return abiBoolFalse, nil
	}
	bitmapRaw, ok := data["2"].([]byte)
	if !ok || len(bitmapRaw) == 0 {
		return abiBoolFalse, nil
	}
	aggregateSignature, ok := data["3"].([]byte)
	if !ok {
		return abiBoolFalse, nil
	}

	maxBitmapLength := (len(publicKeys) + 7) / 8
	if len(bitmapRaw) > maxBitmapLength || bitmapRaw[0] == 0 {
		return abiBoolFalse, nil
	}
	for _, key := range publicKeys {
		if len(key) != interchainVerification.BLSPublicKeyCompressedLength {
			return abiBoolFalse, nil
		}
	}
	if len(aggregateSignature) != interchainVerification.BLSSignatureCompressedLength {
		return abiBoolFalse, nil
	}

	bitmap := new(big.Int).SetBytes(bitmapRaw)
	if err := interchainVerification.VerifyAggregatedQuorum(
		publicKeys,
		bitmap,
		aggregateSignature,
		message,
	); err != nil {
		return abiBoolFalse, nil
	}
	return abiBoolTrue, nil
}
