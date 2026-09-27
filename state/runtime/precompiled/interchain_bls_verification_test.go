package precompiled

import (
	"math/big"
	"testing"

	"github.com/coinbase/kryptology/pkg/signatures/bls/bls_sig"
	"github.com/stretchr/testify/require"

	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

func TestInterchainBLSVerification(t *testing.T) {
	message := []byte("XGR_INTERCHAIN_CHECKPOINT_V1-test")
	keys, sigs := generateInterchainBLSKeys(t, 4, message)

	tests := []struct {
		name       string
		message    []byte
		keys       [][]byte
		signerBits []int
		sigs       [][]byte
		want       []byte
	}{
		{
			name:       "valid 3 of 4 quorum",
			message:    message,
			keys:       keys,
			signerBits: []int{0, 1, 2},
			sigs:       sigs[:3],
			want:       abiBoolTrue,
		},
		{
			name:       "insufficient 2 of 4 quorum",
			message:    message,
			keys:       keys,
			signerBits: []int{0, 1},
			sigs:       sigs[:2],
			want:       abiBoolFalse,
		},
		{
			name:       "wrong message",
			message:    []byte("wrong"),
			keys:       keys,
			signerBits: []int{0, 1, 2},
			sigs:       sigs[:3],
			want:       abiBoolFalse,
		},
	}

	verifier := &interchainBLSVerification{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := encodeInterchainBLSInput(t, tt.message, tt.keys, tt.signerBits, tt.sigs)
			out, err := verifier.run(input, types.ZeroAddress, nil)
			require.NoError(t, err)
			require.Equal(t, tt.want, out)
		})
	}
}

func TestInterchainBLSVerificationRejectsMalformedInputs(t *testing.T) {
	message := []byte("checkpoint")
	keys, sigs := generateInterchainBLSKeys(t, 3, message)
	aggregate := aggregateInterchainBLSSignatures(t, sigs[:2])

	encode := func(keys [][]byte, bitmap, signature []byte) []byte {
		raw, err := interchainBLSInputABIType.Encode([4]interface{}{message, keys, bitmap, signature})
		require.NoError(t, err)
		return append(append([]byte(nil), interchainBLSVerifySelector...), raw...)
	}

	verifier := &interchainBLSVerification{}

	cases := []struct {
		name  string
		input []byte
	}{
		{
			name:  "leading zero bitmap",
			input: encode(keys, []byte{0x00, 0x03}, aggregate),
		},
		{
			name:  "bitmap outside validator set",
			input: encode(keys, []byte{0x08}, aggregate),
		},
		{
			name:  "invalid key length",
			input: encode([][]byte{{0x01}, keys[1], keys[2]}, []byte{0x03}, aggregate),
		},
		{
			name:  "invalid signature length",
			input: encode(keys, []byte{0x03}, []byte{0x01}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := verifier.run(tc.input, types.ZeroAddress, nil)
			require.NoError(t, err)
			require.Equal(t, abiBoolFalse, out)
		})
	}
}

func TestInterchainBLSVerificationGasScalesWithInput(t *testing.T) {
	v := &interchainBLSVerification{}
	small := v.gas(make([]byte, 100), nil)
	large := v.gas(make([]byte, 1000), nil)
	require.Greater(t, large, small)
	require.Equal(t, interchainBLSBaseGas+100*interchainBLSGasPerByte, small)
}

func generateInterchainBLSKeys(t *testing.T, count int, message []byte) ([][]byte, [][]byte) {
	t.Helper()
	keys := make([][]byte, count)
	sigs := make([][]byte, count)
	for i := 0; i < count; i++ {
		sk, err := crypto.GenerateBLSKey()
		require.NoError(t, err)
		pk, err := crypto.BLSSecretKeyToPubkeyBytes(sk)
		require.NoError(t, err)
		sig, err := crypto.SignByBLS(sk, message)
		require.NoError(t, err)
		keys[i] = pk
		sigs[i] = sig
	}
	return keys, sigs
}

func encodeInterchainBLSInput(
	t *testing.T,
	message []byte,
	keys [][]byte,
	signerBits []int,
	signatures [][]byte,
) []byte {
	t.Helper()
	bitmap := new(big.Int)
	for _, bit := range signerBits {
		bitmap.SetBit(bitmap, bit, 1)
	}
	aggregate := aggregateInterchainBLSSignatures(t, signatures)
	raw, err := interchainBLSInputABIType.Encode(
		[4]interface{}{message, keys, bitmap.Bytes(), aggregate},
	)
	require.NoError(t, err)
	return append(append([]byte(nil), interchainBLSVerifySelector...), raw...)
}

func aggregateInterchainBLSSignatures(t *testing.T, signatures [][]byte) []byte {
	t.Helper()
	parsed := make([]*bls_sig.Signature, 0, len(signatures))
	for _, raw := range signatures {
		sig, err := crypto.UnmarshalBLSSignature(raw)
		require.NoError(t, err)
		parsed = append(parsed, sig)
	}
	aggregate, err := bls_sig.NewSigPop().AggregateSignatures(parsed...)
	require.NoError(t, err)
	raw, err := aggregate.MarshalBinary()
	require.NoError(t, err)
	return raw
}

func TestInterchainBLSVerificationRejectsUnknownSelector(t *testing.T) {
	verifier := &interchainBLSVerification{}
	input := append([]byte{0xde, 0xad, 0xbe, 0xef}, make([]byte, 128)...)
	out, err := verifier.run(input, types.ZeroAddress, nil)
	require.ErrorIs(t, err, errInterchainBLSInvalidInput)
	require.Nil(t, out)
}
