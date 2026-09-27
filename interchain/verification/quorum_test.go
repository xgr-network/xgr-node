package verification

import (
	"math/big"
	"testing"

	"github.com/coinbase/kryptology/pkg/signatures/bls/bls_sig"
	"github.com/stretchr/testify/require"

	"github.com/xgr-network/xgr-node/crypto"
)

func TestVerifyAggregatedQuorum(t *testing.T) {
	message := []byte("XGR_INTERCHAIN_CHECKPOINT_V1-direct")
	keys := make([][]byte, 4)
	signatures := make([]*bls_sig.Signature, 0, 3)

	for i := 0; i < 4; i++ {
		sk, err := crypto.GenerateBLSKey()
		require.NoError(t, err)
		keys[i], err = crypto.BLSSecretKeyToPubkeyBytes(sk)
		require.NoError(t, err)
		if i < 3 {
			raw, err := crypto.SignByBLS(sk, message)
			require.NoError(t, err)
			sig, err := crypto.UnmarshalBLSSignature(raw)
			require.NoError(t, err)
			signatures = append(signatures, sig)
		}
	}

	aggregate, err := bls_sig.NewSigPop().AggregateSignatures(signatures...)
	require.NoError(t, err)
	aggregateRaw, err := aggregate.MarshalBinary()
	require.NoError(t, err)

	require.NoError(t, VerifyAggregatedQuorum(keys, big.NewInt(0x07), aggregateRaw, message))
	require.Error(t, VerifyAggregatedQuorum(keys, big.NewInt(0x03), aggregateRaw, message))
	require.Error(t, VerifyAggregatedQuorum(keys, big.NewInt(0x10), aggregateRaw, message))
	require.Error(t, VerifyAggregatedQuorum(keys, big.NewInt(0x07), aggregateRaw, []byte("wrong")))
}

func TestQuorumThreshold(t *testing.T) {
	tests := []struct {
		n    int
		want int
	}{
		{1, 1},
		{2, 2},
		{3, 2},
		{4, 3},
		{5, 4},
		{6, 4},
	}
	for _, tc := range tests {
		got, err := QuorumThreshold(tc.n)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
	_, err := QuorumThreshold(0)
	require.ErrorIs(t, err, ErrEmptyValidatorSet)
}
