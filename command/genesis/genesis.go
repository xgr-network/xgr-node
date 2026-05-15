package genesis

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/xgr-network/xgr-node/command"
	"github.com/xgr-network/xgr-node/command/genesis/predeploy"
	"github.com/xgr-network/xgr-node/command/helper"
	"github.com/xgr-network/xgr-node/consensus/ibft"
	"github.com/xgr-network/xgr-node/helper/common"
	"github.com/xgr-network/xgr-node/validators"
)

func GetCommand() *cobra.Command {
	genesisCmd := &cobra.Command{
		Use:     "genesis",
		Short:   "Generates the genesis configuration file with the passed in parameters",
		PreRunE: preRunCommand,
		Run:     runCommand,
	}

	setFlags(genesisCmd)
	setLegacyFlags(genesisCmd)

	genesisCmd.AddCommand(
		// genesis predeploy
		predeploy.GetCommand(),
	)

	return genesisCmd
}

func setFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(
		&params.genesisPath,
		dirFlag,
		fmt.Sprintf("./%s", command.DefaultGenesisFileName),
		"the directory for the XGRChain genesis data",
	)

	cmd.Flags().Uint64Var(
		&params.chainID,
		chainIDFlag,
		command.DefaultChainID,
		"the ID of the chain",
	)

	cmd.Flags().StringVar(
		&params.name,
		nameFlag,
		command.DefaultChainName,
		"the name for the chain",
	)

	cmd.Flags().StringArrayVar(
		&params.premine,
		premineFlag,
		[]string{},
		fmt.Sprintf(
			"the premined accounts and balances (format: <address>[:<balance>]). Default premined balance: %d",
			command.DefaultPremineBalance,
		),
	)

	cmd.Flags().Uint64Var(
		&params.blockGasLimit,
		blockGasLimitFlag,
		command.DefaultGenesisGasLimit,
		"the maximum amount of gas used by all transactions in a block",
	)

	cmd.Flags().StringVar(
		&params.burnContract,
		burnContractFlag,
		"",
		"the burn contract block and address (format: <block>:<address>[:<burn destination>])",
	)

	cmd.Flags().StringVar(
		&params.baseFeeConfig,
		genesisBaseFeeConfigFlag,
		command.DefaultGenesisBaseFeeConfig,
		`initial base fee(in wei), base fee elasticity multiplier, and base fee change denominator
		(provided in the following format: [<baseFee>][:<baseFeeEM>][:<baseFeeChangeDenom>]). 
		BaseFeeChangeDenom represents the value to bound the amount the base fee can change between blocks.
		Default BaseFee is 1 Gwei, BaseFeeEM is 2 and BaseFeeChangeDenom is 8.
		Note: BaseFee, BaseFeeEM, and BaseFeeChangeDenom should be greater than 0.`,
	)

	cmd.Flags().StringArrayVar(
		&params.bootnodes,
		command.BootnodeFlag,
		[]string{},
		"multiAddr URL for p2p discovery bootstrap. This flag can be used multiple times",
	)

	cmd.Flags().StringVar(
		&params.consensusRaw,
		command.ConsensusFlag,
		string(command.DefaultConsensus),
		"the consensus protocol to be used",
	)

	cmd.Flags().Uint64Var(
		&params.epochSize,
		epochSizeFlag,
		ibft.DefaultEpochSize,
		"the epoch size for the chain",
	)

	// PoS
	{
		cmd.Flags().BoolVar(
			&params.isPos,
			posFlag,
			false,
			"the flag indicating that the client should use Proof of Stake IBFT. Defaults to "+
				"Proof of Authority if flag is not provided or false",
		)

		cmd.Flags().Uint64Var(
			&params.minNumValidators,
			command.MinValidatorCountFlag,
			1,
			"the minimum number of validators in the validator set for PoS",
		)

		cmd.Flags().Uint64Var(
			&params.maxNumValidators,
			command.MaxValidatorCountFlag,
			common.MaxSafeJSInt,
			"the maximum number of validators in the validator set for PoS",
		)

		cmd.Flags().StringVar(
			&params.validatorsPath,
			command.ValidatorRootFlag,
			command.DefaultValidatorRoot,
			"root path containing validators secrets",
		)

		cmd.Flags().StringVar(
			&params.validatorsPrefixPath,
			command.ValidatorPrefixFlag,
			command.DefaultValidatorPrefix,
			"folder prefix names for validators secrets",
		)

		cmd.Flags().StringArrayVar(
			&params.validators,
			command.ValidatorFlag,
			[]string{},
			"validators defined by user",
		)

		cmd.MarkFlagsMutuallyExclusive(command.ValidatorFlag, command.ValidatorRootFlag)
		cmd.MarkFlagsMutuallyExclusive(command.ValidatorFlag, command.ValidatorPrefixFlag)
	}

	// IBFT Validators
	{
		cmd.Flags().StringVar(
			&params.rawIBFTValidatorType,
			command.IBFTValidatorTypeFlag,
			string(validators.BLSValidatorType),
			"the type of validators in IBFT",
		)
	}

	cmd.Flags().DurationVar(
		&params.blockTime,
		blockTimeFlag,
		defaultBlockTime,
		"the predefined period which determines block creation frequency",
	)
}

// setLegacyFlags sets the legacy flags to preserve backwards compatibility
// with running partners
func setLegacyFlags(cmd *cobra.Command) {
	// Legacy chainid flag
	cmd.Flags().Uint64Var(
		&params.chainID,
		chainIDFlagLEGACY,
		command.DefaultChainID,
		"the ID of the chain",
	)

	_ = cmd.Flags().MarkHidden(chainIDFlagLEGACY)
}

func preRunCommand(cmd *cobra.Command, _ []string) error {
	if err := params.validateFlags(); err != nil {
		return err
	}

	helper.SetRequiredFlags(cmd, params.getRequiredFlags())

	return params.initRawParams()
}

func runCommand(cmd *cobra.Command, _ []string) {
	outputter := command.InitializeOutputter(cmd)
	defer outputter.WriteOutput()

	var err error

	_, _ = outputter.Write([]byte(fmt.Sprintf("%s\n", common.IBFTImportantNotice)))
	err = params.generateGenesis()

	if err != nil {
		outputter.SetError(err)

		return
	}

	outputter.SetCommandResult(params.getResult())
}
