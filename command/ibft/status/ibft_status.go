package status

import (
	"context"

	"github.com/spf13/cobra"
	"github.com/xgr-network/xgr-node/command"
	"github.com/xgr-network/xgr-node/command/helper"
	ibftOp "github.com/xgr-network/xgr-node/consensus/ibft/proto"
	empty "google.golang.org/protobuf/types/known/emptypb"
)

func GetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Returns the current validator key of the IBFT client",
		Run:   runCommand,
	}
}

func runCommand(cmd *cobra.Command, _ []string) {
	outputter := command.InitializeOutputter(cmd)
	defer outputter.WriteOutput()

	statusResponse, err := getIBFTStatus(helper.GetGRPCAddress(cmd))
	if err != nil {
		outputter.SetError(err)

		return
	}

	outputter.SetCommandResult(&IBFTStatusResult{
		ValidatorKey: statusResponse.Key,
	})
}

func getIBFTStatus(grpcAddress string) (*ibftOp.IbftStatusResp, error) {
	client, err := helper.GetIBFTOperatorClientConnection(
		grpcAddress,
	)
	if err != nil {
		return nil, err
	}

	return client.Status(context.Background(), &empty.Empty{})
}
