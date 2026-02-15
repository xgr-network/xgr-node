package validators

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/xgr-network/xgr-node/command"
	"github.com/xgr-network/xgr-node/command/helper"
	"github.com/xgr-network/xgr-node/command/polybftsecrets"
	rootHelper "github.com/xgr-network/xgr-node/command/rootchain/helper"
	sidechainHelper "github.com/xgr-network/xgr-node/command/sidechain"
	"github.com/xgr-network/xgr-node/txrelayer"
	"github.com/xgr-network/xgr-node/types"
)

var (
	params validatorInfoParams
)

func GetCommand() *cobra.Command {
	validatorInfoCmd := &cobra.Command{
		Use:     "validator-info",
		Short:   "Gets validator info",
		PreRunE: runPreRun,
		RunE:    runCommand,
	}

	helper.RegisterJSONRPCFlag(validatorInfoCmd)
	setFlags(validatorInfoCmd)

	return validatorInfoCmd
}

func setFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(
		&params.accountDir,
		polybftsecrets.AccountDirFlag,
		"",
		polybftsecrets.AccountDirFlagDesc,
	)

	cmd.Flags().StringVar(
		&params.accountConfig,
		polybftsecrets.AccountConfigFlag,
		"",
		polybftsecrets.AccountConfigFlagDesc,
	)

	cmd.Flags().StringVar(
		&params.supernetManagerAddress,
		rootHelper.SupernetManagerFlag,
		"",
		rootHelper.SupernetManagerFlagDesc,
	)

	cmd.Flags().StringVar(
		&params.stakeManagerAddress,
		rootHelper.StakeManagerFlag,
		"",
		rootHelper.StakeManagerFlagDesc,
	)

	cmd.Flags().Int64Var(
		&params.chainID,
		polybftsecrets.ChainIDFlag,
		0,
		polybftsecrets.ChainIDFlagDesc,
	)

	cmd.MarkFlagsMutuallyExclusive(polybftsecrets.AccountDirFlag, polybftsecrets.AccountConfigFlag)
}

func runPreRun(cmd *cobra.Command, _ []string) error {
	params.jsonRPC = helper.GetJSONRPCAddress(cmd)

	return params.validateFlags()
}

func runCommand(cmd *cobra.Command, _ []string) error {
	outputter := command.InitializeOutputter(cmd)
	defer outputter.WriteOutput()

	validatorAccount, err := sidechainHelper.GetAccount(params.accountDir, params.accountConfig)
	if err != nil {
		return err
	}

	txRelayer, err := txrelayer.NewTxRelayer(txrelayer.WithIPAddress(params.jsonRPC))
	if err != nil {
		return err
	}

	validatorAddr := validatorAccount.Ecdsa.Address()
	supernetManagerAddr := types.StringToAddress(params.supernetManagerAddress)
	stakeManagerAddr := types.StringToAddress(params.stakeManagerAddress)

	validatorInfo, err := rootHelper.GetValidatorInfo(validatorAddr,
		supernetManagerAddr, stakeManagerAddr, params.chainID, txRelayer)
	if err != nil {
		return fmt.Errorf("failed to get validator info for %s: %w", validatorAddr, err)
	}

	outputter.WriteCommandResult(&validatorsInfoResult{
		Address:     validatorInfo.Address.String(),
		Stake:       validatorInfo.Stake.Uint64(),
		Active:      validatorInfo.IsActive,
		Whitelisted: validatorInfo.IsWhitelisted,
	})

	return nil
}
