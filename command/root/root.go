package root

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/xgr-network/xgr-node/command/backup"
	"github.com/xgr-network/xgr-node/command/bridge"
	"github.com/xgr-network/xgr-node/command/genesis"
	"github.com/xgr-network/xgr-node/command/helper"
	"github.com/xgr-network/xgr-node/command/ibft"
	"github.com/xgr-network/xgr-node/command/license"
	"github.com/xgr-network/xgr-node/command/monitor"
	"github.com/xgr-network/xgr-node/command/peers"
	"github.com/xgr-network/xgr-node/command/polybft"
	"github.com/xgr-network/xgr-node/command/polybftsecrets"
	"github.com/xgr-network/xgr-node/command/regenesis"
	"github.com/xgr-network/xgr-node/command/rootchain"
	"github.com/xgr-network/xgr-node/command/secrets"
	"github.com/xgr-network/xgr-node/command/server"
	"github.com/xgr-network/xgr-node/command/status"
	"github.com/xgr-network/xgr-node/command/txpool"
	"github.com/xgr-network/xgr-node/command/version"
)

type RootCommand struct {
	baseCmd *cobra.Command
}

func NewRootCommand() *RootCommand {
	rootCommand := &RootCommand{
		baseCmd: &cobra.Command{
			Short: "XGRChain is a framework for building Ethereum-compatible Blockchain networks",
		},
	}

	helper.RegisterJSONOutputFlag(rootCommand.baseCmd)

	rootCommand.registerSubCommands()

	return rootCommand
}

func (rc *RootCommand) registerSubCommands() {
	rc.baseCmd.AddCommand(
		version.GetCommand(),
		txpool.GetCommand(),
		status.GetCommand(),
		secrets.GetCommand(),
		peers.GetCommand(),
		rootchain.GetCommand(),
		monitor.GetCommand(),
		ibft.GetCommand(),
		backup.GetCommand(),
		genesis.GetCommand(),
		server.GetCommand(),
		license.GetCommand(),
		polybftsecrets.GetCommand(),
		polybft.GetCommand(),
		bridge.GetCommand(),
		regenesis.GetCommand(),
	)
}

func (rc *RootCommand) Execute() {
	if err := rc.baseCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)

		os.Exit(1)
	}
}
