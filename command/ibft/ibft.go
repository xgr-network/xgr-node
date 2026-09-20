package ibft

import (
	"github.com/spf13/cobra"
	"github.com/xgr-network/xgr-node/command/helper"
	"github.com/xgr-network/xgr-node/command/ibft/candidates"
	"github.com/xgr-network/xgr-node/command/ibft/interchain"
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
		status.GetCommand(),
		snapshot.GetCommand(),
		propose.GetCommand(),
		candidates.GetCommand(),
		_switch.GetCommand(),
		quorum.GetCommand(),
		join.GetCommand(),
		poolconfig.GetCommand(),
		interchain.GetCommand(),
		stake.GetCommand(),
		unstake.GetCommand(),
		withdraw.GetCommand(),
		setactive.GetCommand(),
		validators.GetCommand(),
	)
}
