package interchain

import (
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/xgr-network/xgr-node/command"
	"github.com/xgr-network/xgr-node/command/helper"
	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
	interchainRuntime "github.com/xgr-network/xgr-node/interchain/runtime"
	"github.com/xgr-network/xgr-node/types"
)

type proposalCreateParams struct {
	dataDir           string
	source             string
	destination        string
	proposalType       string
	nonce              uint64
	ttl                time.Duration
	feeWei             string
	gateway            string
	sourceRouter       string
	mailbox            string
	merkleTreeHook     string
	destinationRouter  string
	timeout            time.Duration
}

type proposalApproveParams struct {
	dataDir    string
	proposalID string
	timeout    time.Duration
}

type proposalShowParams struct {
	dataDir    string
	proposalID string
}

type ProposalActionResult struct {
	ProposalID string `json:"proposalId"`
	Approved   bool   `json:"approved,omitempty"`
	Quorum     bool   `json:"quorum,omitempty"`
}

func (r *ProposalActionResult) GetOutput() string {
	return "\n[IBFT INTERCHAIN ILN PROPOSAL]\n" + helper.FormatKV([]string{
		fmt.Sprintf("Proposal ID|%s", r.ProposalID),
		fmt.Sprintf("Approved|%t", r.Approved),
		fmt.Sprintf("Quorum complete|%t", r.Quorum),
	})
}

type ProposalShowResult struct {
	ProposalID        string `json:"proposalId"`
	Type              string `json:"type"`
	Registry          string `json:"registry"`
	SetID             uint64 `json:"setId"`
	Nonce             uint64 `json:"nonce"`
	ValidUntil        uint64 `json:"validUntil"`
	SourceChainID     uint64 `json:"sourceChainId"`
	SourceDomain      uint32 `json:"sourceDomain"`
	DestinationDomain uint32 `json:"destinationDomain"`
	Gateway           string `json:"gateway,omitempty"`
	SourceRouter      string `json:"sourceRouter,omitempty"`
	Mailbox           string `json:"mailbox,omitempty"`
	MerkleTreeHook    string `json:"merkleTreeHook,omitempty"`
	DestinationRouter string `json:"destinationRouter,omitempty"`
	ValidatorFeeWei   string `json:"validatorFeeWei,omitempty"`
	Payload           string `json:"payload"`
}

func (r *ProposalShowResult) GetOutput() string {
	return "\n[IBFT INTERCHAIN ILN PROPOSAL SHOW]\n" + helper.FormatKV([]string{
		fmt.Sprintf("Proposal ID|%s", r.ProposalID),
		fmt.Sprintf("Type|%s", r.Type),
		fmt.Sprintf("Registry|%s", r.Registry),
		fmt.Sprintf("Set ID|%d", r.SetID),
		fmt.Sprintf("Nonce|%d", r.Nonce),
		fmt.Sprintf("Valid until|%d", r.ValidUntil),
		fmt.Sprintf("Source chain ID|%d", r.SourceChainID),
		fmt.Sprintf("Source domain|%d", r.SourceDomain),
		fmt.Sprintf("Destination domain|%d", r.DestinationDomain),
		fmt.Sprintf("Gateway|%s", r.Gateway),
		fmt.Sprintf("Source Warp router|%s", r.SourceRouter),
		fmt.Sprintf("Mailbox|%s", r.Mailbox),
		fmt.Sprintf("MerkleTreeHook|%s", r.MerkleTreeHook),
		fmt.Sprintf("Destination router|%s", r.DestinationRouter),
		fmt.Sprintf("Validator fee wei|%s", r.ValidatorFeeWei),
	})
}

var (
	enqueueGovernanceCreate  = interchainRuntime.EnqueueGovernanceCreateAndWait
	enqueueGovernanceApprove = interchainRuntime.EnqueueGovernanceApproveAndWait
	readGovernanceProposal   = interchainRuntime.ReadGovernanceProposal
	getProposalValidatorSet  = evmInterchain.GetValidatorSet
)

func getProposalCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proposal",
		Short: "Create, inspect, or explicitly approve ILN governance proposals",
	}
	cmd.AddCommand(
		getProposalCreateCommand(),
		getProposalApproveCommand(),
		getProposalShowCommand(),
	)
	return cmd
}

func getProposalCreateCommand() *cobra.Command {
	p := &proposalCreateParams{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create and gossip an ILN governance proposal without approving it",
		Args:  cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if strings.TrimSpace(p.dataDir) == "" {
				return fmt.Errorf("--data-dir is required")
			}
			if strings.TrimSpace(p.source) == "" || strings.TrimSpace(p.destination) == "" {
				return fmt.Errorf("--source and --destination are required")
			}
			if p.nonce == 0 {
				return fmt.Errorf("--nonce must be non-zero")
			}
			if p.ttl <= 0 || p.ttl > 10*time.Minute {
				return fmt.Errorf("--ttl must be between 1s and 10m")
			}
			switch normalizeProposalType(p.proposalType) {
			case protocol.ILNProposalFeeUpdate:
				if strings.TrimSpace(p.feeWei) == "" {
					return fmt.Errorf("--fee-wei is required for fee-update")
				}
			case protocol.ILNProposalRouteAdd:
				for flag, value := range map[string]string{
					"--gateway": p.gateway,
					"--source-router": p.sourceRouter,
					"--mailbox": p.mailbox,
					"--merkle-tree-hook": p.merkleTreeHook,
					"--destination-router": p.destinationRouter,
					"--fee-wei": p.feeWei,
				} {
					if strings.TrimSpace(value) == "" {
						return fmt.Errorf("%s is required for route-add", flag)
					}
				}
			case protocol.ILNProposalRouteEnable, protocol.ILNProposalRouteDisable:
			default:
				return fmt.Errorf("--type must be fee-update, route-add, route-enable, or route-disable")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			outputter := command.InitializeOutputter(cmd)
			defer outputter.WriteOutput()

			result, err := executeProposalCreate(p)
			if err != nil {
				outputter.SetError(err)
				return nil
			}
			outputter.SetCommandResult(result)
			return nil
		},
	}

	cmd.Flags().StringVar(&p.dataDir, "data-dir", "", "data directory of the running XGR validator node")
	cmd.Flags().StringVar(&p.source, "source", "", "configured source network name, for example base")
	cmd.Flags().StringVar(&p.destination, "destination", "", "configured destination network name, for example xgr")
	cmd.Flags().StringVar(&p.proposalType, "type", "", "fee-update, route-add, route-enable, or route-disable")
	cmd.Flags().Uint64Var(&p.nonce, "nonce", 0, "replay-protection nonce expected by the source ILN registry")
	cmd.Flags().DurationVar(&p.ttl, "ttl", 5*time.Minute, "proposal validity window, maximum 10m")
	cmd.Flags().StringVar(&p.feeWei, "fee-wei", "", "native source-chain validator fee in wei/base units")
	cmd.Flags().StringVar(&p.gateway, "gateway", "", "canonical ILN gateway address")
	cmd.Flags().StringVar(&p.sourceRouter, "source-router", "", "canonical source Warp router address")
	cmd.Flags().StringVar(&p.mailbox, "mailbox", "", "canonical source Mailbox address")
	cmd.Flags().StringVar(&p.merkleTreeHook, "merkle-tree-hook", "", "canonical source MerkleTreeHook address")
	cmd.Flags().StringVar(&p.destinationRouter, "destination-router", "", "canonical destination router address")
	cmd.Flags().DurationVar(&p.timeout, "timeout", 2*time.Minute, "maximum time to wait for the local worker to accept and gossip the proposal")
	return cmd
}

func executeProposalCreate(p *proposalCreateParams) (*ProposalActionResult, error) {
	sourceName := strings.ToLower(strings.TrimSpace(p.source))
	destinationName := strings.ToLower(strings.TrimSpace(p.destination))
	source, err := evmInterchain.Load(sourceName)
	if err != nil {
		return nil, fmt.Errorf("load source network: %w", err)
	}
	if err := source.ValidateILNRead(); err != nil {
		return nil, fmt.Errorf("source ILN configuration: %w", err)
	}
	destination, err := evmInterchain.Load(destinationName)
	if err != nil {
		return nil, fmt.Errorf("load destination network: %w", err)
	}
	set, err := getProposalValidatorSet(destination)
	if err != nil {
		return nil, fmt.Errorf("read destination validator set: %w", err)
	}

	proposalType := normalizeProposalType(p.proposalType)
	route := protocol.ILNRoute{
		Key: protocol.ILNRouteKey{
			SourceChainID:     source.ChainID,
			SourceDomain:      source.Domain,
			DestinationDomain: destination.Domain,
		},
	}
	switch proposalType {
	case protocol.ILNProposalFeeUpdate:
		fee, err := parsePositiveUint256(p.feeWei)
		if err != nil { return nil, err }
		route.ValidatorFeeWei = fee
	case protocol.ILNProposalRouteAdd:
		gateway, err := parseNonZeroAddress("--gateway", p.gateway)
		if err != nil { return nil, err }
		sourceRouter, err := parseNonZeroAddress("--source-router", p.sourceRouter)
		if err != nil { return nil, err }
		mailbox, err := parseNonZeroAddress("--mailbox", p.mailbox)
		if err != nil { return nil, err }
		hook, err := parseNonZeroAddress("--merkle-tree-hook", p.merkleTreeHook)
		if err != nil { return nil, err }
		router, err := parseNonZeroAddress("--destination-router", p.destinationRouter)
		if err != nil { return nil, err }
		fee, err := parsePositiveUint256(p.feeWei)
		if err != nil { return nil, err }
		route.Gateway = gateway
		route.SourceRouter = sourceRouter
		route.Mailbox = mailbox
		route.MerkleTreeHook = hook
		route.DestinationRouter = router
		route.ValidatorFeeWei = fee
		route.Enabled = true
	case protocol.ILNProposalRouteEnable, protocol.ILNProposalRouteDisable:
	default:
		return nil, fmt.Errorf("unsupported proposal type")
	}

	proposal := protocol.ILNGovernanceProposal{
		Type:       proposalType,
		Registry:   types.StringToAddress(source.ILNRegistryAddress),
		SetID:      set.SetID,
		Nonce:      p.nonce,
		ValidUntil: uint64(time.Now().Add(p.ttl).Unix()),
		Route:      route,
	}
	result, err := enqueueGovernanceCreate(p.dataDir, proposal, p.timeout)
	if err != nil {
		return nil, err
	}
	return &ProposalActionResult{
		ProposalID: result.ProposalID,
		Quorum:     result.Quorum,
	}, nil
}

func getProposalApproveCommand() *cobra.Command {
	p := &proposalApproveParams{}
	cmd := &cobra.Command{
		Use:   "approve",
		Short: "Explicitly approve a known ILN governance proposal with this validator BLS key",
		Args:  cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if strings.TrimSpace(p.dataDir) == "" {
				return fmt.Errorf("--data-dir is required")
			}
			if strings.TrimSpace(p.proposalID) == "" {
				return fmt.Errorf("--proposal-id is required")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			outputter := command.InitializeOutputter(cmd)
			defer outputter.WriteOutput()
			result, err := enqueueGovernanceApprove(p.dataDir, p.proposalID, p.timeout)
			if err != nil {
				outputter.SetError(err)
				return nil
			}
			outputter.SetCommandResult(&ProposalActionResult{
				ProposalID: result.ProposalID,
				Approved:   result.Approved,
				Quorum:     result.Quorum,
			})
			return nil
		},
	}
	cmd.Flags().StringVar(&p.dataDir, "data-dir", "", "data directory of the running XGR validator node")
	cmd.Flags().StringVar(&p.proposalID, "proposal-id", "", "32-byte ILN governance proposal ID")
	cmd.Flags().DurationVar(&p.timeout, "timeout", 2*time.Minute, "maximum time to wait for local approval processing")
	return cmd
}

func getProposalShowCommand() *cobra.Command {
	p := &proposalShowParams{}
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show a locally known ILN governance proposal without signing it",
		Args:  cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if strings.TrimSpace(p.dataDir) == "" {
				return fmt.Errorf("--data-dir is required")
			}
			if strings.TrimSpace(p.proposalID) == "" {
				return fmt.Errorf("--proposal-id is required")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			outputter := command.InitializeOutputter(cmd)
			defer outputter.WriteOutput()
			view, err := readGovernanceProposal(p.dataDir, p.proposalID)
			if err != nil {
				outputter.SetError(err)
				return nil
			}
			outputter.SetCommandResult(&ProposalShowResult{
				ProposalID: view.ProposalID,
				Type: proposalTypeName(view.Type),
				Registry: view.Registry.String(),
				SetID: view.SetID,
				Nonce: view.Nonce,
				ValidUntil: view.ValidUntil,
				SourceChainID: view.SourceChainID,
				SourceDomain: view.SourceDomain,
				DestinationDomain: view.DestinationDomain,
				Gateway: view.Gateway.String(),
				SourceRouter: view.SourceRouter.String(),
				Mailbox: view.Mailbox.String(),
				MerkleTreeHook: view.MerkleTreeHook.String(),
				DestinationRouter: view.DestinationRouter.String(),
				ValidatorFeeWei: view.ValidatorFeeWei,
				Payload: view.Payload,
			})
			return nil
		},
	}
	cmd.Flags().StringVar(&p.dataDir, "data-dir", "", "data directory of the validator node")
	cmd.Flags().StringVar(&p.proposalID, "proposal-id", "", "32-byte ILN governance proposal ID")
	return cmd
}

func normalizeProposalType(value string) protocol.ILNProposalType {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "fee-update":
		return protocol.ILNProposalFeeUpdate
	case "route-add":
		return protocol.ILNProposalRouteAdd
	case "route-enable":
		return protocol.ILNProposalRouteEnable
	case "route-disable":
		return protocol.ILNProposalRouteDisable
	default:
		return 0
	}
}

func proposalTypeName(value protocol.ILNProposalType) string {
	switch value {
	case protocol.ILNProposalFeeUpdate:
		return "FEE_UPDATE"
	case protocol.ILNProposalRouteAdd:
		return "ROUTE_ADD"
	case protocol.ILNProposalRouteEnable:
		return "ROUTE_ENABLE"
	case protocol.ILNProposalRouteDisable:
		return "ROUTE_DISABLE"
	default:
		return fmt.Sprintf("UNKNOWN_%d", value)
	}
}

func parsePositiveUint256(value string) (*big.Int, error) {
	out, ok := new(big.Int).SetString(strings.TrimSpace(value), 10)
	if !ok || out.Sign() <= 0 || out.BitLen() > 256 {
		return nil, fmt.Errorf("--fee-wei must be a positive uint256 decimal integer")
	}
	return out, nil
}

func parseNonZeroAddress(flag, value string) (types.Address, error) {
	value = strings.TrimSpace(value)
	if err := types.IsValidAddress(value); err != nil {
		return types.ZeroAddress, fmt.Errorf("%s is invalid: %w", flag, err)
	}
	out := types.StringToAddress(value)
	if out == types.ZeroAddress {
		return types.ZeroAddress, fmt.Errorf("%s must be non-zero", flag)
	}
	return out, nil
}
