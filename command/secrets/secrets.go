package secrets

import (
	"github.com/spf13/cobra"
	"github.com/xgr-network/xgr-node/command/helper"
	"github.com/xgr-network/xgr-node/command/secrets/generate"
	initCmd "github.com/xgr-network/xgr-node/command/secrets/init"
	"github.com/xgr-network/xgr-node/command/secrets/output"
)

func GetCommand() *cobra.Command {
	secretsCmd := &cobra.Command{
		Use:   "secrets",
		Short: "Top level SecretsManager command for interacting with secrets functionality. Only accepts subcommands.",
	}

	helper.RegisterGRPCAddressFlag(secretsCmd)

	registerSubcommands(secretsCmd)

	return secretsCmd
}

func registerSubcommands(baseCmd *cobra.Command) {
	baseCmd.AddCommand(
		// secrets init
		initCmd.GetCommand(),
		// secrets generate
		generate.GetCommand(),
		// secrets output public data
		output.GetCommand(),
	)
}
