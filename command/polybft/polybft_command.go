package polybft

import (
	"github.com/spf13/cobra"
	"github.com/xgr-network/xgr-node/command/rootchain/registration"
	"github.com/xgr-network/xgr-node/command/rootchain/staking"
	"github.com/xgr-network/xgr-node/command/rootchain/supernet"
	"github.com/xgr-network/xgr-node/command/rootchain/supernet/stakemanager"
	"github.com/xgr-network/xgr-node/command/rootchain/validators"
	"github.com/xgr-network/xgr-node/command/rootchain/whitelist"
	"github.com/xgr-network/xgr-node/command/rootchain/withdraw"
	"github.com/xgr-network/xgr-node/command/sidechain/rewards"
	"github.com/xgr-network/xgr-node/command/sidechain/unstaking"
	sidechainWithdraw "github.com/xgr-network/xgr-node/command/sidechain/withdraw"
)

func GetCommand() *cobra.Command {
	polybftCmd := &cobra.Command{
		Use:   "polybft",
		Short: "Polybft command",
	}

	polybftCmd.AddCommand(
		// sidechain (validator set) command to unstake on child chain
		unstaking.GetCommand(),
		// sidechain (validator set) command to withdraw stake on child chain
		sidechainWithdraw.GetCommand(),
		// sidechain (reward pool) command to withdraw pending rewards
		rewards.GetCommand(),
		// rootchain (stake manager) command to withdraw stake
		withdraw.GetCommand(),
		// rootchain (supernet manager) command that queries validator info
		validators.GetCommand(),
		// rootchain (supernet manager) whitelist validator
		whitelist.GetCommand(),
		// rootchain (supernet manager) register validator
		registration.GetCommand(),
		// rootchain (stake manager) stake command
		staking.GetCommand(),
		// rootchain (supernet manager) command for finalizing genesis
		// validator set and enabling staking
		supernet.GetCommand(),
		// rootchain command for deploying stake manager
		stakemanager.GetCommand(),
	)

	return polybftCmd
}
