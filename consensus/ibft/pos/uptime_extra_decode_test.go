package pos

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	xcrypto "github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func TestGetIBFTExtra_UsesProvidedSignerForECDSAExtra(t *testing.T) {
	v := types.StringToAddress("0x9991")
	set := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v))
	header := makeUptimeHeader(42, set)

	extra, err := getIBFTExtra(testHeaderSigner(), header)
	require.NoError(t, err)
	require.NotNil(t, extra)
	require.NotNil(t, extra.Validators)
	require.Equal(t, 1, extra.Validators.Len())
	require.Equal(t, v, extra.Validators.At(0).Addr())
}

func TestGetIBFTExtra_ReturnsErrorOnMalformedExtraPayload(t *testing.T) {
	header := &types.Header{
		Number:    43,
		ExtraData: []byte{0x01, 0x02, 0x03, 0x04},
	}

	extra, err := getIBFTExtra(testHeaderSigner(), header)
	require.Error(t, err)
	require.Nil(t, extra)
}

func TestGetIBFTExtra_UsesProvidedSignerForBLSExtra(t *testing.T) {
	v := validators.NewBLSValidator(types.StringToAddress("0x9992"), validators.BLSValidatorPublicKey("pk1"))
	set := validators.NewBLSValidatorSet(v)
	header := makeUptimeHeader(44, set)

	ecdsaExtra, ecdsaErr := getIBFTExtra(testHeaderSigner(), header)
	require.Error(t, ecdsaErr)
	require.Nil(t, ecdsaExtra)

	ecdsaKey, err := xcrypto.GenerateECDSAKey()
	require.NoError(t, err)
	blsKey, err := xcrypto.GenerateBLSKey()
	require.NoError(t, err)
	blsKeyManager := signer.NewBLSKeyManagerFromKeys(ecdsaKey, blsKey)
	blsSigner := signer.NewSigner(blsKeyManager, blsKeyManager)

	blsExtra, err := getIBFTExtra(blsSigner, header)
	require.NoError(t, err)
	require.NotNil(t, blsExtra)
	require.Equal(t, 1, blsExtra.Validators.Len())
	require.Equal(t, v.Addr(), blsExtra.Validators.At(0).Addr())
}
