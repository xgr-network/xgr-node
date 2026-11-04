package blspop_test

import (
	"math/big"
	"testing"

	"github.com/coinbase/kryptology/pkg/core/curves/native/bls12381"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

// Pin the deployed library's identity rejection without changing historical
// consensus rules or the shared Interchain verification primitive.
func TestConsensusVerifierRejectsInfinitySignatureAndAggregateKey(t *testing.T) {
	key, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	pk, err := crypto.BLSSecretKeyToPubkeyBytes(key)
	require.NoError(t, err)
	ecdsa, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	km := signer.NewBLSKeyManagerFromKeys(ecdsa, key)
	message := []byte("isolated-infinity-regression")
	signature, err := crypto.SignByBLS(key, message)
	require.NoError(t, err)
	one := validators.NewBLSValidatorSet(validators.NewBLSValidator(types.Address{19: 1}, pk))
	infinity := new(bls12381.G2).Identity().ToCompressed()
	_, err = km.VerifyCommittedSeals(&signer.AggregatedSeal{Bitmap: big.NewInt(1), Signature: infinity[:]}, message, one)
	require.Error(t, err, "infinity signature must not pass the real committed-seal verifier")
	var raw [48]byte
	copy(raw[:], pk)
	point, err := new(bls12381.G1).FromCompressed(&raw)
	require.NoError(t, err)
	opposite := new(bls12381.G1).Neg(point).ToCompressed()
	cancelling := validators.NewBLSValidatorSet(validators.NewBLSValidator(types.Address{19: 1}, pk), validators.NewBLSValidator(types.Address{19: 2}, opposite[:]))
	_, err = km.VerifyCommittedSeals(&signer.AggregatedSeal{Bitmap: big.NewInt(3), Signature: signature}, message, cancelling)
	require.Error(t, err, "identity aggregate public key must reject even a regular nonidentity signature")
}
