package fork

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/hashicorp/go-hclog"
	"github.com/xgr-network/xgr-node/consensus/ibft/hook"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/contracts/staking"
	stakingHelper "github.com/xgr-network/xgr-node/helper/staking"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
	"github.com/xgr-network/xgr-node/validators/store"
	contractstore "github.com/xgr-network/xgr-node/validators/store/contract"
)

var (
	ErrTxInLastEpochOfBlock = errors.New("block must not have transactions in the last of epoch")
)

func chainShouldWriteTransactions(a, b hook.ShouldWriteTransactionsFunc) hook.ShouldWriteTransactionsFunc {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	return func(height uint64) bool {
		return a(height) && b(height)
	}
}

func chainVerifyBlock(a, b hook.VerifyBlockFunc) hook.VerifyBlockFunc {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	return func(block *types.Block) error {
		if err := a(block); err != nil {
			return err
		}
		return b(block)
	}
}

func chainPreCommitState(a, b hook.PreCommitStateFunc) hook.PreCommitStateFunc {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	return func(header *types.Header, txn *state.Transition) error {
		if err := a(header, txn); err != nil {
			return err
		}
		return b(header, txn)
	}
}

func chainPostInsertBlock(a, b hook.PostInsertBlockFunc) hook.PostInsertBlockFunc {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	return func(block *types.Block) error {
		if err := a(block); err != nil {
			return err
		}
		return b(block)
	}
}

// HeaderModifier is an interface for the struct that modifies block header for additional process
type HeaderModifier interface {
	ModifyHeader(*types.Header, types.Address) error
	VerifyHeader(*types.Header) error
	ProcessHeader(*types.Header) error
}

// registerHeaderModifierHooks registers hooks to modify header by validator store
func registerHeaderModifierHooks(
	hooks *hook.Hooks,
	validatorStore store.ValidatorStore,
) {
	if modifier, ok := validatorStore.(HeaderModifier); ok {
		hooks.ModifyHeaderFunc = modifier.ModifyHeader
		hooks.VerifyHeaderFunc = modifier.VerifyHeader
		hooks.ProcessHeaderFunc = modifier.ProcessHeader
	}
}

// Updatable is an interface for the struct that updates validators in the middle
type Updatable interface {
	// UpdateValidatorSet updates validators forcibly
	// in order that new validators are available from the given height
	UpdateValidatorSet(validators.Validators, uint64) error
}

// registerUpdateValidatorsHooks registers hooks to update validators in the middle
func registerUpdateValidatorsHooks(
	hooks *hook.Hooks,
	validatorStore store.ValidatorStore,
	validators validators.Validators,
	fromHeight uint64,
) {
	if us, ok := validatorStore.(Updatable); ok {
		next := func(b *types.Block) error {
			if fromHeight != b.Number()+1 {
				return nil
			}

			// update validators if the block height is the one before beginning height
			return us.UpdateValidatorSet(validators, fromHeight)
		}

		hooks.PostInsertBlockFunc = chainPostInsertBlock(hooks.PostInsertBlockFunc, next)
	}
}

// registerPoSVerificationHooks registers that hooks to prevent the last epoch block from having transactions
func registerTxInclusionGuardHooks(
	hooks *hook.Hooks,
	epochSize uint64,
	uptimeCfg pos.UptimeConfig,
	getSigner func(uint64) (signer.Signer, error),
	firstPoSFrom uint64,
) {
	isLastEpoch := func(height uint64) bool {
		return height > 0 && height%epochSize == 0
	}

	guardShouldWrite := func(height uint64) bool {
		return !isLastEpoch(height) || shouldSkipEpochFinalizationBeforePoS(height, firstPoSFrom)
	}

	guardVerify := func(block *types.Block) error {
		if !isLastEpoch(block.Number()) {
			return nil
		}

		if shouldSkipEpochFinalizationBeforePoS(block.Number(), firstPoSFrom) {
			return nil
		}

		if len(block.Transactions) == 0 {
			return ErrTxInLastEpochOfBlock
		}

		if len(block.Transactions) == 1 && pos.IsEpochFinalizationSystemTx(block.Transactions[0], block.Number()) {
			return nil
		}

		return ErrTxInLastEpochOfBlock
	}

	// 1) user-tx-free last epoch block once PoS finalization is active
	hooks.ShouldWriteTransactionFunc = chainShouldWriteTransactions(hooks.ShouldWriteTransactionFunc, guardShouldWrite)
	hooks.VerifyBlockFunc = chainVerifyBlock(hooks.VerifyBlockFunc, guardVerify)

	// 2) deterministic native epoch-finalize on the epoch boundary block
	hooks.PreCommitStateFunc = chainPreCommitState(hooks.PreCommitStateFunc, func(header *types.Header, txn *state.Transition) error {
		if !isLastEpoch(header.Number) {
			return nil
		}
		if shouldSkipEpochFinalizationBeforePoS(header.Number, firstPoSFrom) {
			return nil
		}
		if getSigner == nil {
			return fmt.Errorf("missing signer getter")
		}
		headerSigner, err := getSigner(header.Number)
		if err != nil {
			return err
		}
		if headerSigner == nil {
			return fmt.Errorf("missing signer")
		}
		finalizationTx := pos.EpochFinalizationSystemTx(header.Number)

		if err := pos.FinalizeEpoch(header, epochSize, uptimeCfg, headerSigner, txn); err != nil {
			return err
		}

		return txn.WriteSystemReceipt(finalizationTx)
	})
}

func shouldSkipEpochFinalizationBeforePoS(boundaryBlock, firstPoSFrom uint64) bool {
	if boundaryBlock == 0 {
		return false
	}

	return boundaryBlock-1 < firstPoSFrom
}

// registerStakingContractDeploymentHooks registers hooks
// to deploy or update staking contract
func registerStakingContractDeploymentHooks(
	hooks *hook.Hooks,
	fork *IBFTFork,
	epochSize uint64,
) {
	deployOrUpdate := func(header *types.Header, txn *state.Transition) error {
		// safe check
		if header.Number != fork.Deployment.Value {
			return nil
		}

		// Bootstrap validator set:
		// At PoA -> PoS transition, validators may not have staked yet.
		// The contract must still contain the current validator list (with BLS keys)
		// to keep the chain live. Stake filtering is applied later by fetcher.
		bootstrapVals := fork.Validators
		if bootstrapVals == nil || bootstrapVals.Len() == 0 {
			parsedBootstrapVals, err := parseValidatorsFromHeader(header, fork.ValidatorType)
			if err != nil {
				return fmt.Errorf(
					"staking predeploy bootstrap validator fallback failed at block %d (validatorType=%s): %w",
					header.Number,
					fork.ValidatorType,
					err,
				)
			}
			bootstrapVals = parsedBootstrapVals

			if bootstrapVals == nil || bootstrapVals.Len() == 0 {
				return fmt.Errorf(
					"staking predeploy bootstrap validator fallback returned empty validator set at block %d",
					header.Number,
				)
			}
		}

		params, err := getPreDeployParams(fork, epochSize, uint64(bootstrapVals.Len()))
		if err != nil {
			return fmt.Errorf(
				"staking predeploy params invalid at block %d (bootstrapValidators=%d): %w",
				header.Number,
				bootstrapVals.Len(),
				err,
			)
		}

		contractState, err := stakingHelper.PredeployStakingSCBootstrap(
			bootstrapVals,
			params,
		)
		if err != nil {
			return fmt.Errorf(
				"staking predeploy bootstrap failed at block %d (validators=%d min=%d max=%d epochSize=%d): %w",
				header.Number,
				bootstrapVals.Len(),
				params.MinValidatorCount,
				params.MaxValidatorCount,
				params.EpochSize,
				err,
			)
		}

		if txn.AccountExists(staking.AddrStakingContract) {
			// Update bytecode (if needed) and ensure storage is bootstrapped exactly once.
			// Use the precomputed runtime code (contractState.Code), not creation bytecode.
			if err := txn.SetCodeDirectly(staking.AddrStakingContract, contractState.Code); err != nil {
				return fmt.Errorf("staking predeploy code update failed at block %d: %w", header.Number, err)
			}

			// If the contract storage is not initialized yet (e.g. maxNumValidators == 0),
			// apply the bootstrap storage map.
			// When uninitialized, stale bootstrap slots can still exist from a previous
			// partial recovery and must be overwritten to match the deterministic
			// bootstrap snapshot.
			if stakingHelper.IsUninitializedStakingStorage(txn, staking.AddrStakingContract) {
				for k, v := range contractState.Storage {
					txn.Txn().SetState(staking.AddrStakingContract, k, v)
				}
			}

			return nil
		}

		// Deploy contract with fully bootstrapped storage.
		if err := txn.SetAccountDirectly(staking.AddrStakingContract, contractState); err != nil {
			return fmt.Errorf("staking predeploy account deployment failed at block %d: %w", header.Number, err)
		}

		return nil
	}
	// Deploy/update must run first in the deployment block, before any other pre-commit hooks.
	hooks.PreCommitStateFunc = chainPreCommitState(deployOrUpdate, hooks.PreCommitStateFunc)
}

func registerCutoverMacroSnapshotHooks(
	hooks *hook.Hooks,
	fork *IBFTFork,
	epochSize uint64,
) {
	targetBlock := fork.From.Value
	if fork.From.Value == 0 {
		targetBlock = 1
	}

	writeCutoverSnapshots := func(header *types.Header, txn *state.Transition) error {
		if header == nil || txn == nil {
			return nil
		}
		if header.Number != targetBlock {
			return nil
		}

		bootstrapVals := fork.Validators
		if bootstrapVals == nil || bootstrapVals.Len() == 0 {
			parsedBootstrapVals, err := parseValidatorsFromHeader(header, fork.ValidatorType)
			if err != nil {
				return fmt.Errorf("cutover snapshot validator fallback failed at block %d: %w", header.Number, err)
			}
			bootstrapVals = parsedBootstrapVals
			if bootstrapVals == nil || bootstrapVals.Len() == 0 {
				return fmt.Errorf("cutover snapshot validator fallback returned empty set at block %d", header.Number)
			}
		}

		return ensureCutoverMacroSnapshots(txn, header, epochSize, bootstrapVals)
	}

	hooks.PreCommitStateFunc = chainPreCommitState(hooks.PreCommitStateFunc, writeCutoverSnapshots)
}

func registerRegularMacroSnapshotHooks(
	hooks *hook.Hooks,
	fork *IBFTFork,
	epochSize uint64,
) {
	writeRegularSnapshots := func(header *types.Header, txn *state.Transition) error {
		if header == nil || txn == nil {
			return nil
		}
		if header.Number <= fork.From.Value || header.Number%epochSize != 0 {
			return nil
		}

		nextEpoch := (header.Number / epochSize) + 1
		var (
			selection *contractstore.ValidatorSelection
			err       error
		)
		switch fork.ValidatorType {
		case validators.BLSValidatorType:
			selection, err = contractstore.FetchBLSValidatorSelection(txn, types.ZeroAddress)
		case validators.ECDSAValidatorType:
			selection, err = contractstore.FetchECDSAValidatorSelection(txn, types.ZeroAddress)
		default:
			return fmt.Errorf("unsupported validator type: %s", fork.ValidatorType)
		}
		if err != nil {
			return fmt.Errorf("regular snapshot validator fetch failed at block %d epoch %d: %w", header.Number, nextEpoch, err)
		}
		vals := selection.Validators
		if vals == nil || vals.Len() == 0 {
			return fmt.Errorf("regular snapshot validator fetch returned empty set at block %d epoch %d", header.Number, nextEpoch)
		}
		stakes := make(map[types.Address]*big.Int, vals.Len())
		for i := 0; i < vals.Len(); i++ {
			addr := vals.At(uint64(i)).Addr()
			stake := contractstore.ReadValidatorVotingStakeAt(txn, addr, header.Number)
			if stake == nil || stake.Sign() <= 0 {
				return fmt.Errorf("missing non-positive voting stake for validator %s at regular epoch %d", addr, nextEpoch)
			}
			stakes[addr] = new(big.Int).Set(stake)
		}
		if err := pos.StoreMacroEpochNoSlashMode(txn, nextEpoch, selection.NoSlash); err != nil {
			return fmt.Errorf("store macro epoch no-slash mode at block %d epoch %d: %w", header.Number, nextEpoch, err)
		}
		if err := pos.EnsureMacroEpochSnapshots(txn, nextEpoch, vals, stakes); err != nil {
			return fmt.Errorf("ensure regular macro epoch snapshots at block %d epoch %d: %w", header.Number, nextEpoch, err)
		}

		hclog.L().Named("ibft-regular-snapshot").Debug("regular macro snapshot writer invoked",
			"header", header.Number,
			"epoch", nextEpoch,
			"validators", vals.Len(),
			"noSlash", selection.NoSlash,
			"stakes", len(stakes),
		)

		return nil
	}

	hooks.PreCommitStateFunc = chainPreCommitState(hooks.PreCommitStateFunc, writeRegularSnapshots)
}

func ensureCutoverMacroSnapshots(txn *state.Transition, header *types.Header, epochSize uint64, vals validators.Validators) error {
	epoch := ((header.Number + 1 - 1) / epochSize) + 1
	stakes := make(map[types.Address]*big.Int, vals.Len())
	for i := 0; i < vals.Len(); i++ {
		addr := vals.At(uint64(i)).Addr()
		stake := contractstore.ReadValidatorVotingStakeAt(txn, addr, header.Number)
		if stake == nil || stake.Sign() <= 0 {
			return fmt.Errorf("missing non-positive stake for validator %s at cutover epoch %d", addr, epoch)
		}
		stakes[addr] = new(big.Int).Set(stake)
	}
	if err := pos.StoreMacroEpochNoSlashMode(txn, epoch, false); err != nil {
		return fmt.Errorf("store macro epoch no-slash mode at cutover block %d epoch %d: %w", header.Number, epoch, err)
	}

	if err := pos.EnsureMacroEpochSnapshots(txn, epoch, vals, stakes); err != nil {
		return fmt.Errorf("ensure macro epoch snapshots at cutover block %d epoch %d: %w", header.Number, epoch, err)
	}
	hclog.L().Named("ibft-cutover").Debug("cutover macro snapshot writer invoked",
		"header", header.Number,
		"epoch", epoch,
		"validators", vals.Len(),
		"stakes", len(stakes),
		"hasValidatorSnapshot", pos.HasMacroEpochValidatorSet(txn, epoch),
		"hasStakeSnapshot", pos.HasMacroEpochStakeSnapshot(txn, epoch, vals),
	)

	return nil
}

func parseValidatorsFromHeader(
	header *types.Header,
	validatorType validators.ValidatorType,
) (validators.Validators, error) {
	if header == nil {
		return nil, fmt.Errorf("header is nil")
	}

	if len(header.ExtraData) < signer.IstanbulExtraVanity {
		return nil, fmt.Errorf(
			"extra-data shorter than vanity length (%d < %d)",
			len(header.ExtraData),
			signer.IstanbulExtraVanity,
		)
	}

	extra := &signer.IstanbulExtra{
		Validators: validators.NewValidatorSetFromType(validatorType),
	}

	switch validatorType {
	case validators.ECDSAValidatorType:
		extra.CommittedSeals = &signer.SerializedSeal{}
		extra.ParentCommittedSeals = &signer.SerializedSeal{}
	case validators.BLSValidatorType:
		extra.CommittedSeals = &signer.AggregatedSeal{}
		extra.ParentCommittedSeals = &signer.AggregatedSeal{}
	default:
		return nil, fmt.Errorf("unsupported validator type: %s", validatorType)
	}

	if err := extra.UnmarshalRLP(header.ExtraData[signer.IstanbulExtraVanity:]); err != nil {
		return nil, fmt.Errorf("unable to decode IBFT extra: %w", err)
	}

	if extra.Validators == nil || extra.Validators.Len() == 0 {
		return nil, fmt.Errorf("decoded validator set is empty")
	}

	return extra.Validators, nil
}

// getPreDeployParams returns PredeployParams for Staking Contract from IBFTFork
func getPreDeployParams(
	fork *IBFTFork,
	epochSize uint64,
	bootstrapValidatorCount uint64,
) (stakingHelper.PredeployParams, error) {
	if fork == nil {
		return stakingHelper.PredeployParams{}, fmt.Errorf("PoS staking predeploy requires fork configuration")
	}

	if bootstrapValidatorCount == 0 {
		return stakingHelper.PredeployParams{}, fmt.Errorf("PoS staking predeploy requires at least one bootstrap validator")
	}

	minValidatorCount := bootstrapValidatorCount
	if fork.MinValidatorCount != nil {
		minValidatorCount = fork.MinValidatorCount.Value
	}

	maxValidatorCount := uint64(0)
	if fork.MaxValidatorCount != nil {
		maxValidatorCount = fork.MaxValidatorCount.Value
	} else {
		maxValidatorCount = bootstrapValidatorCount
	}

	if maxValidatorCount > pos.MaxEpochValidatorsSnapshot {
		return stakingHelper.PredeployParams{}, fmt.Errorf(
			"invalid staking predeploy params: max validator count (%d) exceeds epoch snapshot max (%d)",
			maxValidatorCount,
			pos.MaxEpochValidatorsSnapshot,
		)
	}
	if minValidatorCount > maxValidatorCount {
		return stakingHelper.PredeployParams{}, fmt.Errorf(
			"invalid staking predeploy params: min validator count (%d) exceeds max (%d)",
			minValidatorCount,
			maxValidatorCount,
		)
	}
	if minValidatorCount > bootstrapValidatorCount {
		return stakingHelper.PredeployParams{}, fmt.Errorf(
			"invalid staking predeploy params: min validator count (%d) exceeds bootstrap validator count (%d)",
			minValidatorCount,
			bootstrapValidatorCount,
		)
	}

	return stakingHelper.PredeployParams{
		MinValidatorCount: minValidatorCount,
		MaxValidatorCount: maxValidatorCount,
		EpochSize:         epochSize,
	}, nil
}
