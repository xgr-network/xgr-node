package crypto

import (
	"bytes"
	"fmt"
	"math/big"

	"github.com/coinbase/kryptology/pkg/signatures/bls/bls_sig"
	"github.com/xgr-network/xgr-node/types"
)

// StakingPoPDomain separates validator admission from consensus signatures and
// XITA admission. The public key is part of the signed statement: a signature
// for another public key cannot be reused as a possession proof.
const StakingPoPDomain = "XGR_STAKING_BLS_POP_V1"

// StakingPoPMessage matches Solidity's keccak256(abi.encode(domainHash,
// chainID, stakingContract, validator, keccak256(publicKey), nonce)). No variable
// length values are concatenated. Both uint256 values use canonical big endian.
func StakingPoPMessage(chainID *big.Int, stakingContract, validator types.Address, publicKey []byte, nonce *big.Int) ([]byte, error) {
	if chainID == nil || chainID.Sign() <= 0 || chainID.BitLen() > 256 ||
		nonce == nil || nonce.Sign() < 0 || nonce.BitLen() > 256 ||
		stakingContract == types.ZeroAddress || validator == types.ZeroAddress {
		return nil, fmt.Errorf("invalid staking PoP context")
	}
	// Reuse the existing strict subgroup/identity checks without changing the
	// historical consensus signature or public key decoding rules.
	if _, err := BLSPublicKeyToEIP2537(publicKey); err != nil {
		return nil, fmt.Errorf("invalid staking PoP public key: %w", err)
	}
	encoded := make([]byte, 6*32)
	copy(encoded[:32], Keccak256([]byte(StakingPoPDomain)))
	chainID.FillBytes(encoded[32:64])
	copy(encoded[64+12:96], stakingContract[:])
	copy(encoded[96+12:128], validator[:])
	copy(encoded[128:160], Keccak256(publicKey))
	nonce.FillBytes(encoded[160:192])
	return Keccak256(encoded), nil
}

// CreateStakingPoP uses only the already loaded local validator key. Callers
// must never serialize the secret into a manifest or log it.
func CreateStakingPoP(key *bls_sig.SecretKey, chainID *big.Int, stakingContract, validator types.Address, nonce *big.Int) ([]byte, error) {
	if key == nil {
		return nil, fmt.Errorf("missing staking BLS key")
	}
	publicKey, err := BLSSecretKeyToPubkeyBytes(key)
	if err != nil {
		return nil, err
	}
	message, err := StakingPoPMessage(chainID, stakingContract, validator, publicKey, nonce)
	if err != nil {
		return nil, err
	}
	return SignByBLS(key, message)
}

func VerifyStakingPoP(chainID *big.Int, stakingContract, validator types.Address, publicKey, proof []byte, nonce *big.Int) error {
	message, err := StakingPoPMessage(chainID, stakingContract, validator, publicKey, nonce)
	if err != nil {
		return err
	}
	if _, err := BLSSignatureToEIP2537(proof); err != nil {
		return fmt.Errorf("invalid staking PoP signature: %w", err)
	}
	// Canonical compression is required in addition to subgroup checks.
	pk, err := UnmarshalBLSPublicKey(publicKey)
	if err != nil {
		return err
	}
	roundTrip, err := pk.MarshalBinary()
	if err != nil || !bytes.Equal(publicKey, roundTrip) {
		return fmt.Errorf("noncanonical staking PoP public key")
	}
	return VerifyBLSSignatureFromBytes(publicKey, proof, message)
}
