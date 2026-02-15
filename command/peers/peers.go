package peers

import (
	"github.com/spf13/cobra"
	"github.com/xgr-network/xgr-node/command/helper"
	"github.com/xgr-network/xgr-node/command/peers/add"
	"github.com/xgr-network/xgr-node/command/peers/list"
	"github.com/xgr-network/xgr-node/command/peers/status"
)

func GetCommand() *cobra.Command {
	peersCmd := &cobra.Command{
		Use:   "peers",
		Short: "Top level command for interacting with the network peers. Only accepts subcommands.",
	}

	helper.RegisterGRPCAddressFlag(peersCmd)

	registerSubcommands(peersCmd)

	return peersCmd
}

func registerSubcommands(baseCmd *cobra.Command) {
	baseCmd.AddCommand(
		// peers status
		status.GetCommand(),
		// peers list
		list.GetCommand(),
		// peers add
		add.GetCommand(),
	)
}
