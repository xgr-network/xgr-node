package helper

import (
	"encoding/hex"
	"fmt"

	ethgowallet "github.com/umbracle/ethgo/wallet"
	"github.com/xgr-network/xgr-node/secrets"
)

func GetECDSAKeyFromSecret(secretsManager secrets.SecretsManager) (*ethgowallet.Key, error) {
	encodedKey, err := secretsManager.GetSecret(secrets.ValidatorKey)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve ecdsa key: %w", err)
	}

	ecdsaRaw, err := hex.DecodeString(string(encodedKey))
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve ecdsa key: %w", err)
	}

	key, err := ethgowallet.NewWalletFromPrivKey(ecdsaRaw)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve ecdsa key: %w", err)
	}

	return key, nil
}
