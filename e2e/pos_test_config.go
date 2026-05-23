package e2e

import "github.com/xgr-network/xgr-node/e2e/framework"

// applyPoS3Config configures PoS test nodes with explicit micro-epoch IBFT settings.
// For legacy epoch-only tests, one macro epoch equals one micro epoch (factor=1).
func applyPoS3Config(config *framework.TestServerConfig, epochSize, minValidators, maxValidators uint64) {
	config.SetEpochSize(epochSize)
	config.SetMicroEpochConfig(epochSize, 1, 10_000, 9_000)
	config.SetIBFTPoS(true)
	config.SetMinValidatorCount(minValidators)
	config.SetMaxValidatorCount(maxValidators)
}
