package fork

import (
	"github.com/xgr-network/xgr-node/consensus/ibft/hook"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
)

// PoAHookRegisterer that registers hooks for PoA mode
type PoAHookRegister struct {
	getValidatorsStore    func(*IBFTFork) ValidatorStore
	poaForks              IBFTForks
	updateValidatorsForks map[uint64]*IBFTFork
}

// NewPoAHookRegisterer is a constructor of PoAHookRegister
func NewPoAHookRegisterer(
	getValidatorsStore func(*IBFTFork) ValidatorStore,
	forks IBFTForks,
) *PoAHookRegister {
	poaForks := forks.filterByType(PoA)

	updateValidatorsForks := make(map[uint64]*IBFTFork)

	for _, fork := range poaForks {
		if fork.Validators == nil {
			continue
		}

		updateValidatorsForks[fork.From.Value] = fork
	}

	return &PoAHookRegister{
		getValidatorsStore:    getValidatorsStore,
		poaForks:              poaForks,
		updateValidatorsForks: updateValidatorsForks,
	}
}

// RegisterHooks registers hooks of PoA for voting and validators updating
func (r *PoAHookRegister) RegisterHooks(hooks *hook.Hooks, height uint64) {
	if currentFork := r.poaForks.getFork(height); currentFork != nil {
		// in PoA mode currently
		validatorStore := r.getValidatorsStore(currentFork)

		registerHeaderModifierHooks(hooks, validatorStore)
	}

	// update validators in the end of the last block
	if updateValidatorsFork, ok := r.updateValidatorsForks[height+1]; ok {
		validatorStore := r.getValidatorsStore(updateValidatorsFork)

		registerUpdateValidatorsHooks(
			hooks,
			validatorStore,
			updateValidatorsFork.Validators,
			updateValidatorsFork.From.Value,
		)
	}
}

// PoAHookRegisterer that registers hooks for PoS mode
type PoSHookRegister struct {
	posForks            IBFTForks
	epochSize           uint64
	firstPoSFrom        uint64
	hasPoS              bool
	deployContractForks map[uint64]*IBFTFork
	uptimeCfg           pos.UptimeConfig
	getSigner           func(uint64) (signer.Signer, error)
}

// NewPoSHookRegister is a constructor of PoSHookRegister
func NewPoSHookRegister(
	forks IBFTForks,
	epochSize uint64,
	uptimeCfg pos.UptimeConfig,
	getSigner func(uint64) (signer.Signer, error),
) *PoSHookRegister {
	posForks := forks.filterByType(PoS)

	deployContractForks := make(map[uint64]*IBFTFork)
	var (
		firstPoSFrom uint64
		hasPoS       bool
	)

	for _, fork := range posForks {
		if !hasPoS || fork.From.Value < firstPoSFrom {
			firstPoSFrom = fork.From.Value
			hasPoS = true
		}

		if fork.Deployment == nil {
			continue
		}

		deployContractForks[fork.Deployment.Value] = fork
	}

	return &PoSHookRegister{
		posForks:            posForks,
		epochSize:           epochSize,
		firstPoSFrom:        firstPoSFrom,
		hasPoS:              hasPoS,
		deployContractForks: deployContractForks,
		uptimeCfg:           uptimeCfg,
		getSigner:           getSigner,
	}
}

// RegisterHooks registers hooks of PoA for additional block verification and contract deployment
func (r *PoSHookRegister) RegisterHooks(hooks *hook.Hooks, height uint64) {
	currentFork := r.posForks.getFork(height)
	if currentFork != nil {
		// in PoS mode currently
		registerTxInclusionGuardHooks(hooks, r.epochSize, r.uptimeCfg, r.getSigner, r.firstPoSFrom)
		registerRegularMacroSnapshotHooks(hooks, currentFork, r.epochSize)
		registerCutoverMacroSnapshotHooks(hooks, currentFork, r.epochSize)
	}

	if deploymentFork, ok := r.deployContractForks[height]; ok {
		// deploy or update staking contract in deployment height
		registerStakingContractDeploymentHooks(hooks, deploymentFork, r.epochSize)
	}
	if deploymentFork, ok := r.deployContractForks[height+1]; ok {
		// deploy or update staking contract in deployment height
		registerStakingContractDeploymentHooks(hooks, deploymentFork, r.epochSize)
	}
}
