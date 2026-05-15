package pos

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func TestCollectSignedAddresses_BLSAggregatedSeal(t *testing.T) {
	v1 := validators.NewBLSValidator(types.StringToAddress("0x11"), validators.BLSValidatorPublicKey("pk1"))
	v2 := validators.NewBLSValidator(types.StringToAddress("0x22"), validators.BLSValidatorPublicKey("pk2"))
	v3 := validators.NewBLSValidator(types.StringToAddress("0x33"), validators.BLSValidatorPublicKey("pk3"))

	bitmap := new(big.Int)
	bitmap.SetBit(bitmap, 1, 1)
	bitmap.SetBit(bitmap, 2, 1)

	extra := &signer.IstanbulExtra{
		Validators: validators.NewBLSValidatorSet(v1, v2, v3),
		ParentCommittedSeals: &signer.AggregatedSeal{
			Bitmap:    bitmap,
			Signature: []byte("aggregate-signature"),
		},
	}

	seen := collectSignedAddresses(extra, types.StringToHash("0xabc"), nil)
	require.Len(t, seen, 2)
	_, ok2 := seen[v2.Addr()]
	require.True(t, ok2)
	_, ok3 := seen[v3.Addr()]
	require.True(t, ok3)
}

func TestShouldSkipUptimeAccounting_BLSBitmapWithoutValidators(t *testing.T) {
	bitmap := new(big.Int)
	bitmap.SetBit(bitmap, 0, 1)

	extra := &signer.IstanbulExtra{
		Validators: validators.NewBLSValidatorSet(),
		ParentCommittedSeals: &signer.AggregatedSeal{
			Bitmap:    bitmap,
			Signature: []byte("aggregate-signature"),
		},
	}

	require.True(t, shouldSkipUptimeAccounting(extra, nil, map[types.Address]struct{}{}))
}

func TestShouldSkipUptimeAccounting_BLSBitmapWithCollectedSigners(t *testing.T) {
	v1 := validators.NewBLSValidator(types.StringToAddress("0x11"), validators.BLSValidatorPublicKey("pk1"))
	bitmap := new(big.Int)
	bitmap.SetBit(bitmap, 0, 1)

	extra := &signer.IstanbulExtra{
		Validators: validators.NewBLSValidatorSet(v1),
		ParentCommittedSeals: &signer.AggregatedSeal{
			Bitmap:    bitmap,
			Signature: []byte("aggregate-signature"),
		},
	}

	require.False(t, shouldSkipUptimeAccounting(extra, nil, map[types.Address]struct{}{v1.Addr(): {}}))
}
