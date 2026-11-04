package crypto

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/types"
)

func TestStakingPoPContextBinding(t *testing.T) {
	key, err := GenerateBLSKey()
	require.NoError(t, err)
	publicKey, err := BLSSecretKeyToPubkeyBytes(key)
	require.NoError(t, err)
	chainID, nonce := big.NewInt(1643), big.NewInt(0)
	contract, validator := types.StringToAddress("1001"), types.Address{19: 1}
	proof, err := CreateStakingPoP(key, chainID, contract, validator, nonce)
	require.NoError(t, err)
	require.NoError(t, VerifyStakingPoP(chainID, contract, validator, publicKey, proof, nonce))
	tests := []struct {
		name string
		chain *big.Int
		contract, validator types.Address
		nonce *big.Int
	}{
		{"chain", big.NewInt(1887), contract, validator, nonce},
		{"contract", chainID, types.Address{19: 2}, validator, nonce},
		{"validator", chainID, contract, types.Address{19: 3}, nonce},
		{"replay", chainID, contract, validator, big.NewInt(1)},
		{"negativeNonce", chainID, contract, validator, big.NewInt(-1)},
		{"nilChain", nil, contract, validator, nonce},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, VerifyStakingPoP(test.chain, test.contract, test.validator, publicKey, proof, test.nonce))
		})
	}
	otherKey, err := GenerateBLSKey()
	require.NoError(t, err)
	otherPublicKey, err := BLSSecretKeyToPubkeyBytes(otherKey)
	require.NoError(t, err)
	require.Error(t, VerifyStakingPoP(chainID, contract, validator, otherPublicKey, proof, nonce))
	for _, invalid := range [][]byte{nil, {}, make([]byte, 96), proof[:95]} {
		require.Error(t, VerifyStakingPoP(chainID, contract, validator, publicKey, invalid, nonce))
	}
	message, err := StakingPoPMessage(chainID, contract, validator, publicKey, nonce)
	require.NoError(t, err)
	// The consensus signing primitive and compressed signature format are unchanged.
	require.NoError(t, VerifyBLSSignatureFromBytes(publicKey, proof, message))
}
