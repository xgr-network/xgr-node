package ibft

import (
	"github.com/spf13/cobra"
	"github.com/xgr-network/xgr-node/command/helper"
	"github.com/xgr-network/xgr-node/command/ibft/candidates"
	"github.com/xgr-network/xgr-node/command/ibft/join"
	"github.com/xgr-network/xgr-node/command/ibft/poolconfig"
	"github.com/xgr-network/xgr-node/command/ibft/propose"
	"github.com/xgr-network/xgr-node/command/ibft/quorum"
	"github.com/xgr-network/xgr-node/command/ibft/setactive"
	"github.com/xgr-network/xgr-node/command/ibft/snapshot"
	"github.com/xgr-network/xgr-node/command/ibft/stake"
	"github.com/xgr-network/xgr-node/command/ibft/status"
	_switch "github.com/xgr-network/xgr-node/command/ibft/switch"
	"github.com/xgr-network/xgr-node/command/ibft/unstake"
	"github.com/xgr-network/xgr-node/command/ibft/validators"
	"github.com/xgr-network/xgr-node/command/ibft/withdraw"
)

func GetCommand() *cobra.Command {
	ibftCmd := &cobra.Command{
		Use:   "ibft",
		Short: "Top level IBFT command for interacting with the IBFT consensus. Only accepts subcommands.",
	}

	helper.RegisterGRPCAddressFlag(ibftCmd)

	registerSubcommands(ibftCmd)

	return ibftCmd
}

func registerSubcommands(baseCmd *cobra.Command) {
	baseCmd.AddCommand(
		// ibft status
		status.GetCommand(),
		// ibft snapshot
		snapshot.GetCommand(),
		// ibft propose
		propose.GetCommand(),
		// ibft candidates
		candidates.GetCommand(),
		// ibft switch
		_switch.GetCommand(),
		// ibft quorum
		quorum.GetCommand(),
		// ibft join (PoS validator onboarding)
		join.GetCommand(),
		poolconfig.GetCommand(),
		// ibft stake / unstake / withdraw / set-active
		stake.GetCommand(),
		unstake.GetCommand(),
		withdraw.GetCommand(),
		setactive.GetCommand(),
		// ibft validators
		validators.GetCommand(),
	)
}
