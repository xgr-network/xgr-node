package evm

import (
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/types"
)

func TestEncodeGetValidatorStatusCall(t *testing.T) {
	validator := types.StringToAddress("0x1000000000000000000000000000000000000001")
	input := encodeGetValidatorStatusCall(validator)

	require.Len(t, input, 36)
	require.Equal(t, validator.Bytes(), input[16:36])
}

func TestDecodeValidatorStatusResult(t *testing.T) {
	raw := make([]byte, 64)
	raw[31] = 1
	binary.BigEndian.PutUint64(raw[56:64], 17)

	status, err := decodeValidatorStatusResult("0x" + hex.EncodeToString(raw))
	require.NoError(t, err)
	require.True(t, status.Active)
	require.Equal(t, uint64(17), status.SetID)
}

func TestDecodeValidatorStatusRejectsBadBool(t *testing.T) {
	raw := make([]byte, 64)
	raw[31] = 2

	_, err := decodeValidatorStatusResult("0x" + hex.EncodeToString(raw))
	require.Error(t, err)
}

func TestDecodeValidatorStatusRejectsOversizedSetID(t *testing.T) {
	raw := make([]byte, 64)
	raw[32] = 1

	_, err := decodeValidatorStatusResult("0x" + hex.EncodeToString(raw))
	require.Error(t, err)
}
