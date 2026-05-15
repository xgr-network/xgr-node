package secrets

import (
	"github.com/xgr-network/xgr-node/secrets"
	secretsHelper "github.com/xgr-network/xgr-node/secrets/helper"
	"github.com/xgr-network/xgr-node/types"
)

func InitCloudSecretsManager(secretsConfig *secrets.SecretsManagerConfig) (secrets.SecretsManager, error) {
	return secretsHelper.InitCloudSecretsManager(secretsConfig)
}

func InitECDSAValidatorKey(secretsManager secrets.SecretsManager) (types.Address, error) {
	return secretsHelper.InitECDSAValidatorKey(secretsManager)
}
