package e2e

import (
	"math/big"
	"testing"

	"github.com/coinbase/kryptology/pkg/signatures/bls/bls_sig"
	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"

	"github.com/xgr-network/xgr-node/contracts"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/e2e/framework"
)

func TestInterchainBLSVerificationPrecompileE2E(t *testing.T) {
	message := []byte("XGR_INTERCHAIN_CHECKPOINT_V1-e2e")
	publicKeys := make([][]byte, 3)
	signatures := make([]*bls_sig.Signature, 0, 2)

	for i := 0; i < 3; i++ {
		sk, err := crypto.GenerateBLSKey()
		require.NoError(t, err)
		publicKeys[i], err = crypto.BLSSecretKeyToPubkeyBytes(sk)
		require.NoError(t, err)

		if i < 2 {
			rawSig, err := crypto.SignByBLS(sk, message)
			require.NoError(t, err)
			sig, err := crypto.UnmarshalBLSSignature(rawSig)
			require.NoError(t, err)
			signatures = append(signatures, sig)
		}
	}

	aggregate, err := bls_sig.NewSigPop().AggregateSignatures(signatures...)
	require.NoError(t, err)
	aggregateRaw, err := aggregate.MarshalBinary()
	require.NoError(t, err)

	bitmap := big.NewInt(3).Bytes()
	inputType := abi.MustNewType("tuple(bytes, bytes[], bytes, bytes)")
	inputArgs, err := inputType.Encode([4]interface{}{message, publicKeys, bitmap, aggregateRaw})
	require.NoError(t, err)
	selector := crypto.Keccak256([]byte("verify(bytes,bytes[],bytes,bytes)"))[:4]
	input := append(append([]byte(nil), selector...), inputArgs...)

	servers := framework.NewTestServers(t, 1, func(config *framework.TestServerConfig) {
		config.SetConsensus(framework.ConsensusDev)
	})
	server := servers[0]

	target := ethgo.Address(contracts.InterchainBLSVerificationPrecompile)
	got, err := server.JSONRPC().Eth().Call(
		&ethgo.CallMsg{To: &target, Data: input},
		ethgo.Latest,
	)
	require.NoError(t, err)
	require.Equal(
		t,
		"0x0000000000000000000000000000000000000000000000000000000000000001",
		got,
	)

	badArgs, err := inputType.Encode(
		[4]interface{}{[]byte("wrong-message"), publicKeys, bitmap, aggregateRaw},
	)
	require.NoError(t, err)
	badInput := append(append([]byte(nil), selector...), badArgs...)
	got, err = server.JSONRPC().Eth().Call(
		&ethgo.CallMsg{To: &target, Data: badInput},
		ethgo.Latest,
	)
	require.NoError(t, err)
	require.Equal(
		t,
		"0x0000000000000000000000000000000000000000000000000000000000000000",
		got,
	)
}
