package verification

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/coinbase/kryptology/pkg/signatures/bls/bls_sig"

	"github.com/xgr-network/xgr-node/crypto"
)

const (
	BLSPublicKeyCompressedLength = 48
	BLSSignatureCompressedLength = 96
)

var (
	ErrEmptyValidatorSet = errors.New("interchain validator set is empty")
	ErrQuorumNotReached  = errors.New("interchain quorum not reached")
	ErrSignerOutOfRange  = errors.New("interchain signer index out of range")
)

func QuorumThreshold(validatorCount int) (int, error) {
	if validatorCount <= 0 {
		return 0, ErrEmptyValidatorSet
	}
	return (2*validatorCount + 2) / 3, nil
}

// VerifyAggregatedQuorum verifies an XGR BLS12-381 aggregate signature against
// a validator-index bitmap. The bitmap uses the same big-endian convention as
// Worker attestations and the destination contracts.
func VerifyAggregatedQuorum(
	publicKeys [][]byte,
	bitmap *big.Int,
	aggregateSignature []byte,
	message []byte,
) error {
	threshold, err := QuorumThreshold(len(publicKeys))
	if err != nil {
		return err
	}
	if bitmap == nil || bitmap.Sign() <= 0 {
		return fmt.Errorf("%w: empty signer bitmap", ErrQuorumNotReached)
	}
	if bitmap.BitLen() > len(publicKeys) {
		return fmt.Errorf(
			"%w: bitmap bit %d exceeds validator set size %d",
			ErrSignerOutOfRange,
			bitmap.BitLen()-1,
			len(publicKeys),
		)
	}
	if len(aggregateSignature) != BLSSignatureCompressedLength {
		return fmt.Errorf(
			"interchain aggregate signature must be %d bytes, got %d",
			BLSSignatureCompressedLength,
			len(aggregateSignature),
		)
	}

	selected := make([]*bls_sig.PublicKey, 0, len(publicKeys))
	for idx, raw := range publicKeys {
		if len(raw) != BLSPublicKeyCompressedLength {
			return fmt.Errorf(
				"interchain public key %d must be %d bytes, got %d",
				idx,
				BLSPublicKeyCompressedLength,
				len(raw),
			)
		}
		if bitmap.Bit(idx) == 0 {
			continue
		}
		pk, err := crypto.UnmarshalBLSPublicKey(raw)
		if err != nil {
			return fmt.Errorf("unmarshal interchain public key %d: %w", idx, err)
		}
		selected = append(selected, pk)
	}
	if len(selected) < threshold {
		return fmt.Errorf("%w: have %d need %d", ErrQuorumNotReached, len(selected), threshold)
	}

	aggregateKey, err := bls_sig.NewSigPop().AggregatePublicKeys(selected...)
	if err != nil {
		return fmt.Errorf("aggregate interchain public keys: %w", err)
	}
	aggregate := &bls_sig.MultiSignature{}
	if err := aggregate.UnmarshalBinary(aggregateSignature); err != nil {
		return fmt.Errorf("unmarshal interchain aggregate signature: %w", err)
	}
	ok, err := bls_sig.NewSigPop().VerifyMultiSignature(aggregateKey, message, aggregate)
	if err != nil {
		return err
	}
	if !ok {
		return crypto.ErrInvalidBLSSignature
	}
	return nil
}
