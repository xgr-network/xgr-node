package interchain

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/xgr-network/xgr-node/command"
	"github.com/xgr-network/xgr-node/command/helper"
	interchainRuntime "github.com/xgr-network/xgr-node/interchain/runtime"
)

type setActiveParams struct {
	dataDir   string
	chain     string
	active    bool
	activeRaw string
	timeout   time.Duration
}

type SetActiveResult struct {
	Validator         string `json:"validator"`
	Chain             string `json:"chain"`
	RequestedActive   bool   `json:"requestedActive"`
	FinalActive       bool   `json:"finalActive"`
	Changed           bool   `json:"changed"`
	SetID             uint64 `json:"setId"`
	TransactionHash   string `json:"transactionHash,omitempty"`
	DestinationCommit bool   `json:"destinationCommitted"`
}

func (r *SetActiveResult) GetOutput() string {
	rows := []string{
		fmt.Sprintf("Validator|%s", r.Validator),
		fmt.Sprintf("Destination|%s", r.Chain),
		fmt.Sprintf("Requested active|%t", r.RequestedActive),
		fmt.Sprintf("Final active|%t", r.FinalActive),
		fmt.Sprintf("Changed|%t", r.Changed),
		fmt.Sprintf("Set ID|%d", r.SetID),
		fmt.Sprintf("Destination committed|%t", r.DestinationCommit),
	}
	if r.TransactionHash != "" {
		rows = append(rows, fmt.Sprintf("Transaction hash|%s", r.TransactionHash))
	}

	note := "\nThe running XGR validator worker validates XGR PoS eligibility, collects the current interchain quorum, submits the destination transaction, and verifies the resulting destination state.\n"
	if !r.Changed {
		note += "No destination transaction was required because the validator was already in the requested state.\n"
	}

	return "\n[IBFT INTERCHAIN SET-ACTIVE]\n" + helper.FormatKV(rows) + note
}

var enqueueAndWait = interchainRuntime.EnqueueAndWait

func GetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "interchain",
		Short: "Manage optional XGR interchain validator participation on configured EVM destinations",
	}

	cmd.AddCommand(getSetActiveCommand())
	cmd.AddCommand(getBootstrapProofCommand())

	return cmd
}

func getSetActiveCommand() *cobra.Command {
	p := &setActiveParams{}

	cmd := &cobra.Command{
		Use:   "set-active",
		Short: "Activate or deactivate this XGR validator on an interchain destination",
		Long: "Queues an interchain membership request to the running XGR validator node. " +
			"The node validates canonical XGR PoS state, signs with the existing validator BLS key, " +
			"collects the current interchain quorum, submits the destination transaction, and verifies final state.",
		Example: "  # Activate this validator for Base\n" +
			"  xgrchain ibft interchain set-active --chain base --active true --data-dir ./data\n\n" +
			"  # Voluntarily deactivate this validator\n" +
			"  xgrchain ibft interchain set-active --chain base --active false --data-dir ./data",
		Args: cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if strings.TrimSpace(p.dataDir) == "" {
				return fmt.Errorf("--data-dir is required")
			}
			if strings.TrimSpace(p.chain) == "" {
				return fmt.Errorf("--chain is required")
			}

			switch strings.ToLower(strings.TrimSpace(p.activeRaw)) {
			case "true":
				p.active = true
			case "false":
				p.active = false
			default:
				return fmt.Errorf("--active must be explicitly set to true or false")
			}
			if p.timeout <= 0 {
				return fmt.Errorf("--timeout must be greater than zero")
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			outputter := command.InitializeOutputter(cmd)
			defer outputter.WriteOutput()

			result, err := executeSetActive(p)
			if err != nil {
				outputter.SetError(err)
				return nil
			}
			outputter.SetCommandResult(result)
			return nil
		},
	}

	cmd.Flags().StringVar(&p.dataDir, "data-dir", "", "data directory of the running XGR validator node")
	cmd.Flags().StringVar(&p.chain, "chain", "", "configured EVM interchain destination name, for example base")
	cmd.Flags().StringVar(&p.activeRaw, "active", "", "requested interchain active state: true or false")
	cmd.Flags().DurationVar(&p.timeout, "timeout", 2*time.Minute, "maximum time to wait for quorum, destination receipt, and final-state verification")
	_ = cmd.MarkFlagRequired("data-dir")
	_ = cmd.MarkFlagRequired("chain")
	_ = cmd.MarkFlagRequired("active")

	return cmd
}

func executeSetActive(p *setActiveParams) (*SetActiveResult, error) {
	result, err := enqueueAndWait(p.dataDir, strings.ToLower(strings.TrimSpace(p.chain)), p.active, p.timeout)
	if err != nil {
		return nil, err
	}

	return &SetActiveResult{
		Validator:         result.Validator,
		Chain:             strings.ToLower(strings.TrimSpace(p.chain)),
		RequestedActive:   p.active,
		FinalActive:       result.Active,
		Changed:           result.Changed,
		SetID:             result.SetID,
		TransactionHash:   result.TxHash,
		DestinationCommit: result.Active == p.active,
	}, nil
}
