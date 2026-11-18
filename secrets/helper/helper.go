package helper

import (
	"errors"
	"fmt"

	"github.com/hashicorp/go-hclog"
	libp2pCrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/helper/hex"
	"github.com/xgr-network/xgr-node/network"
	"github.com/xgr-network/xgr-node/secrets"
	"github.com/xgr-network/xgr-node/secrets/awsssm"
	"github.com/xgr-network/xgr-node/secrets/gcpssm"
	"github.com/xgr-network/xgr-node/secrets/hashicorpvault"
	"github.com/xgr-network/xgr-node/secrets/local"
	"github.com/xgr-network/xgr-node/types"
)

// SetupLocalSecretsManager is a helper method for boilerplate local secrets manager setup
func SetupLocalSecretsManager(dataDir string) (secrets.SecretsManager, error) {
	return local.SecretsManagerFactory(
		nil, // Local secrets manager doesn't require a config
		&secrets.SecretsManagerParams{
			Logger: hclog.NewNullLogger(),
			Extra: map[string]interface{}{
				secrets.Path: dataDir,
			},
		},
	)
}

// setupHashicorpVault is a helper method for boilerplate hashicorp vault secrets manager setup
func setupHashicorpVault(
	secretsConfig *secrets.SecretsManagerConfig,
) (secrets.SecretsManager, error) {
	return hashicorpvault.SecretsManagerFactory(
		secretsConfig,
		&secrets.SecretsManagerParams{
			Logger: hclog.NewNullLogger(),
		},
	)
}

// setupAWSSSM is a helper method for boilerplate aws ssm secrets manager setup
func setupAWSSSM(
	secretsConfig *secrets.SecretsManagerConfig,
) (secrets.SecretsManager, error) {
	return awsssm.SecretsManagerFactory(
		secretsConfig,
		&secrets.SecretsManagerParams{
			Logger: hclog.NewNullLogger(),
		},
	)
}

// setupGCPSSM is a helper method for boilerplate Google Cloud Computing secrets manager setup
func setupGCPSSM(
	secretsConfig *secrets.SecretsManagerConfig,
) (secrets.SecretsManager, error) {
	return gcpssm.SecretsManagerFactory(
		secretsConfig,
		&secrets.SecretsManagerParams{
			Logger: hclog.NewNullLogger(),
		},
	)
}

// InitECDSAValidatorKey creates new ECDSA key and set as a validator key
func InitECDSAValidatorKey(secretsManager secrets.SecretsManager) (types.Address, error) {
	if secretsManager.HasSecret(secrets.ValidatorKey) {
		return types.ZeroAddress, fmt.Errorf(`secrets "%s" has been already initialized`, secrets.ValidatorKey)
	}

	validatorKey, validatorKeyEncoded, err := crypto.GenerateAndEncodeECDSAPrivateKey()
	if err != nil {
		return types.ZeroAddress, err
	}

	address := crypto.PubKeyToAddress(&validatorKey.PublicKey)

	// Write the validator private key to the secrets manager storage
	if setErr := secretsManager.SetSecret(
		secrets.ValidatorKey,
		validatorKeyEncoded,
	); setErr != nil {
		return types.ZeroAddress, setErr
	}

	return address, nil
}

// InitInterchainECDSAValidatorKey creates an independent secp256k1
// XITA message-signing key, not the XGR PoS consensus validator key.
// The secret manager enforces no replacement and the local backend saves it
// with the same restricted file permissions as validator-bls.key.
func InitInterchainECDSAValidatorKey(sm secrets.SecretsManager) (types.Address, error) {
 if sm == nil {
  return types.ZeroAddress, fmt.Errorf("interchain ECDSA secrets manager missing")
 }
 if sm.HasSecret(secrets.ValidatorInterchainECDSAKey) {
  return types.ZeroAddress, fmt.Errorf("interchain ECDSA signing key is already initialized")
 }
 if !sm.HasSecret(secrets.ValidatorKey) {
  return types.ZeroAddress, fmt.Errorf("XGR PoS validator identity must exist before interchain signing key generation")
 }
 key, raw, err := crypto.GenerateAndEncodeECDSAPrivateKey()
 if err != nil { return types.ZeroAddress, err }
 address := crypto.PubKeyToAddress(&key.PublicKey)
 posAddress, err := LoadValidatorAddress(sm)
 if err != nil { return types.ZeroAddress, err }
 if address == posAddress || posAddress == types.ZeroAddress {
  return types.ZeroAddress, fmt.Errorf("interchain signing key must differ from XGR PoS validator identity")
 }
 if err := sm.SetSecret(secrets.ValidatorInterchainECDSAKey, raw); err != nil {
  return types.ZeroAddress, fmt.Errorf("store separate interchain ECDSA signer: %w", err)
 }
 return address, nil
}

// LoadInterchainECDSASignerAddress reads only the dedicated key. It never
// falls back to ValidatorKey, even when that PoS ECDSA key already exists.
func LoadInterchainECDSASignerAddress(sm secrets.SecretsManager) (types.Address,error) {
 if sm == nil { return types.ZeroAddress, fmt.Errorf("interchain signer secret manager missing") }
 if !sm.HasSecret(secrets.ValidatorInterchainECDSAKey) { return types.ZeroAddress,nil }
 raw, err := sm.GetSecret(secrets.ValidatorInterchainECDSAKey)
 if err != nil { return types.ZeroAddress,err }
 key,err:=crypto.BytesToECDSAPrivateKey(raw)
 if err != nil { return types.ZeroAddress,fmt.Errorf("parse dedicated interchain ECDSA key: %w",err) }
 return crypto.PubKeyToAddress(&key.PublicKey),nil
}

func InitBLSValidatorKey(secretsManager secrets.SecretsManager) ([]byte, error) {
	if secretsManager.HasSecret(secrets.ValidatorBLSKey) {
		return nil, fmt.Errorf(`secrets "%s" has been already initialized`, secrets.ValidatorBLSKey)
	}

	blsSecretKey, blsSecretKeyEncoded, err := crypto.GenerateAndEncodeBLSSecretKey()
	if err != nil {
		return nil, err
	}

	// Write the validator private key to the secrets manager storage
	if setErr := secretsManager.SetSecret(
		secrets.ValidatorBLSKey,
		blsSecretKeyEncoded,
	); setErr != nil {
		return nil, setErr
	}

	pubkeyBytes, err := crypto.BLSSecretKeyToPubkeyBytes(blsSecretKey)
	if err != nil {
		return nil, err
	}

	return pubkeyBytes, nil
}

func InitNetworkingPrivateKey(secretsManager secrets.SecretsManager) (libp2pCrypto.PrivKey, error) {
	if secretsManager.HasSecret(secrets.NetworkKey) {
		return nil, fmt.Errorf(`secrets "%s" has been already initialized`, secrets.NetworkKey)
	}

	// Generate the libp2p private key
	libp2pKey, libp2pKeyEncoded, keyErr := network.GenerateAndEncodeLibp2pKey()
	if keyErr != nil {
		return nil, keyErr
	}

	// Write the networking private key to the secrets manager storage
	if setErr := secretsManager.SetSecret(
		secrets.NetworkKey,
		libp2pKeyEncoded,
	); setErr != nil {
		return nil, setErr
	}

	return libp2pKey, keyErr
}

// LoadValidatorAddress loads ECDSA key by SecretsManager and returns validator address
func LoadValidatorAddress(secretsManager secrets.SecretsManager) (types.Address, error) {
	if !secretsManager.HasSecret(secrets.ValidatorKey) {
		return types.ZeroAddress, nil
	}

	encodedKey, err := secretsManager.GetSecret(secrets.ValidatorKey)
	if err != nil {
		return types.ZeroAddress, err
	}

	privateKey, err := crypto.BytesToECDSAPrivateKey(encodedKey)
	if err != nil {
		return types.ZeroAddress, err
	}

	return crypto.PubKeyToAddress(&privateKey.PublicKey), nil
}

// LoadBLSPublicKey loads BLS key by SecretsManager and returns BLS Public Key
func LoadBLSPublicKey(secretsManager secrets.SecretsManager) (string, error) {
	if !secretsManager.HasSecret(secrets.ValidatorBLSKey) {
		return "", nil
	}

	encodedKey, err := secretsManager.GetSecret(secrets.ValidatorBLSKey)
	if err != nil {
		return "", err
	}

	secretKey, err := crypto.BytesToBLSSecretKey(encodedKey)
	if err != nil {
		return "", err
	}

	pubkeyBytes, err := crypto.BLSSecretKeyToPubkeyBytes(secretKey)
	if err != nil {
		return "", err
	}

	return hex.EncodeToHex(pubkeyBytes), nil
}

// LoadNodeID loads Libp2p key by SecretsManager and returns Node ID
func LoadNodeID(secretsManager secrets.SecretsManager) (string, error) {
	if !secretsManager.HasSecret(secrets.NetworkKey) {
		return "", nil
	}

	encodedKey, err := secretsManager.GetSecret(secrets.NetworkKey)
	if err != nil {
		return "", err
	}

	parsedKey, err := network.ParseLibp2pKey(encodedKey)
	if err != nil {
		return "", err
	}

	nodeID, err := peer.IDFromPrivateKey(parsedKey)
	if err != nil {
		return "", err
	}

	return nodeID.String(), nil
}

// InitCloudSecretsManager returns the cloud secrets manager from the provided config
func InitCloudSecretsManager(secretsConfig *secrets.SecretsManagerConfig) (secrets.SecretsManager, error) {
	var secretsManager secrets.SecretsManager

	switch secretsConfig.Type {
	case secrets.HashicorpVault:
		vault, err := setupHashicorpVault(secretsConfig)
		if err != nil {
			return secretsManager, err
		}

		secretsManager = vault
	case secrets.AWSSSM:
		AWSSSM, err := setupAWSSSM(secretsConfig)
		if err != nil {
			return secretsManager, err
		}

		secretsManager = AWSSSM
	case secrets.GCPSSM:
		GCPSSM, err := setupGCPSSM(secretsConfig)
		if err != nil {
			return secretsManager, err
		}

		secretsManager = GCPSSM
	default:
		return secretsManager, errors.New("unsupported secrets manager")
	}

	return secretsManager, nil
}
