package txpool

import (
	"github.com/spf13/cobra"
	"github.com/xgr-network/xgr-node/command/helper"
	"github.com/xgr-network/xgr-node/command/txpool/status"
	"github.com/xgr-network/xgr-node/command/txpool/subscribe"
)

func GetCommand() *cobra.Command {
	txPoolCmd := &cobra.Command{
		Use:   "txpool",
		Short: "Top level command for interacting with the transaction pool. Only accepts subcommands.",
	}

	helper.RegisterGRPCAddressFlag(txPoolCmd)

	registerSubcommands(txPoolCmd)

	return txPoolCmd
}

func registerSubcommands(baseCmd *cobra.Command) {
	baseCmd.AddCommand(
		// txpool status
		status.GetCommand(),
		// txpool subscribe
		subscribe.GetCommand(),
	)
}
