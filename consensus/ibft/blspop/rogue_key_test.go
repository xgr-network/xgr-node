package blspop_test

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/coinbase/kryptology/pkg/core/curves/native/bls12381"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/consensus/ibft/blspop"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

// This independently recreates the attack described in the advisory, using
// only ephemeral local test keys. It makes no network or filesystem key calls.
// The historical verifier deliberately remains unchanged for replay support.
func TestRogueKeyForgeryAndPossessionAdmission(t *testing.T) {
	victim, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	attacker, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	victimPK, err := crypto.BLSSecretKeyToPubkeyBytes(victim)
	require.NoError(t, err)
	attackerPK, err := crypto.BLSSecretKeyToPubkeyBytes(attacker)
	require.NoError(t, err)
	var victimCompressed, attackerCompressed [48]byte
	copy(victimCompressed[:], victimPK)
	copy(attackerCompressed[:], attackerPK)
	victimPoint, err := new(bls12381.G1).FromCompressed(&victimCompressed)
	require.NoError(t, err)
	attackerPoint, err := new(bls12381.G1).FromCompressed(&attackerCompressed)
	require.NoError(t, err)
	rogue := new(bls12381.G1).Sub(attackerPoint, victimPoint).ToCompressed()
	digest := signer.LegacyCommitDigest(types.StringToHash("0x1234").Bytes())
	forgedSignature, err := crypto.SignByBLS(attacker, digest)
	require.NoError(t, err)
	ecdsaKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	km := signer.NewBLSKeyManagerFromKeys(ecdsaKey, attacker)
	victimAddr, attackerAddr := types.Address{19: 1}, types.Address{19: 2}
	forgedSet := validators.NewBLSValidatorSet(
		validators.NewBLSValidator(victimAddr, victimPK),
		validators.NewBLSValidator(attackerAddr, rogue[:]),
	)
	seal := &signer.AggregatedSeal{Bitmap: big.NewInt(3), Signature: forgedSignature}
	count, err := km.VerifyCommittedSeals(seal, digest, forgedSet)
	require.NoError(t, err, "baseline cryptographic verifier reproduces the reported forgery")
	require.Equal(t, 2, count, "only the attacker signed, but two identities were counted")
	ordinarySet := validators.NewBLSValidatorSet(
		validators.NewBLSValidator(victimAddr, victimPK),
		validators.NewBLSValidator(attackerAddr, attackerPK),
	)
	_, err = km.VerifyCommittedSeals(seal, digest, ordinarySet)
	require.Error(t, err, "ordinary-key negative control must reject the same forgery")
	chainID, contract := big.NewInt(1643), types.StringToAddress(blspop.StakingContractAddress)
	proof, err := crypto.CreateStakingPoP(attacker, chainID, contract, attackerAddr, new(big.Int))
	require.NoError(t, err)
	require.NoError(t, crypto.VerifyStakingPoP(chainID, contract, attackerAddr, attackerPK, proof, new(big.Int)))
	require.Error(t, crypto.VerifyStakingPoP(chainID, contract, attackerAddr, rogue[:], proof, new(big.Int)))
	// Even signing the rogue key's precise possession message with sk_x does
	// not prove possession of the rogue key's unknown discrete logarithm.
	rogueMessage, err := crypto.StakingPoPMessage(chainID, contract, attackerAddr, rogue[:], new(big.Int))
	require.NoError(t, err)
	forgedProof, err := crypto.SignByBLS(attacker, rogueMessage)
	require.NoError(t, err)
	require.Error(t, crypto.VerifyStakingPoP(chainID, contract, attackerAddr, rogue[:], forgedProof, new(big.Int)))
	_, err = blspop.NewManifest(1643, 100, []blspop.Entry{{
		Validator: "0x" + hex.EncodeToString(attackerAddr[:]),
		PublicKey: "0x" + hex.EncodeToString(rogue[:]),
		Proof:     "0x" + hex.EncodeToString(forgedProof),
	}})
	require.Error(t, err, "manifest admission rejects the rogue key")
}
