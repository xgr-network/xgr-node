package crypto

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xgr-network/xgr-node/types"
)

func TestFrontierSigner(t *testing.T) {
	signer := &FrontierSigner{}

	toAddress := types.StringToAddress("1")
	key, err := GenerateECDSAKey()
	assert.NoError(t, err)

	txn := &types.Transaction{
		To:       &toAddress,
		Value:    big.NewInt(10),
		GasPrice: big.NewInt(0),
	}
	signedTx, err := signer.SignTx(txn, key)
	assert.NoError(t, err)

	from, err := signer.Sender(signedTx)
	assert.NoError(t, err)
	assert.Equal(t, from, PubKeyToAddress(&key.PublicKey))
}
