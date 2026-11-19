package jsonrpc

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/xgr-network/xgr-node/interchain/authorization"
	interchainEVM "github.com/xgr-network/xgr-node/interchain/evm"
)

// interchainActiveValidatorRPC is an entry in the ordered, destination-side
// Registry V3 active set. The array order is canonical: quorum bitmaps use
// these indices, so never sort this list by address.
type interchainActiveValidatorRPC struct {
	Address      string `json:"address"`
	BLSPublicKey string `json:"blsPublicKey,omitempty"`
	ECDSASigner  string `json:"ecdsaSigner,omitempty"`
}

// interchainValidatorsRPC describes the on-chain interchain membership set of
// ONE configured destination, not the (potentially different) XGR PoS set.
// Registration does not imply that a node is currently online or eligible
// in XGR PoS; this response is an on-chain membership snapshot, not telemetry.
type interchainValidatorsRPC struct {
	Destination     string                         `json:"destination"`
	ChainID         uint64                         `json:"chainId"`
	Domain          uint32                         `json:"domain"`
	Registry        string                         `json:"registry"`
	SignatureScheme string                         `json:"signatureScheme"`
	SetID           uint64                         `json:"setId"`
	Count           int                            `json:"count"`
	QuorumThreshold int                            `json:"quorumThreshold"`
	Validators      []interchainActiveValidatorRPC `json:"validators"`
}

// GetInterchainValidators exposes the latest active set from a configured
// destination's on-chain validator registry through xgr_getInterchainValidators.
// It performs no signing, gossip, state mutation or transaction submission.
// Only destination configurations already declared on this node are accepted:
// callers can never supply an arbitrary RPC URL or contract address.
func (x *xgrEndpoint) GetInterchainValidators(destinationName string) (*interchainValidatorsRPC, error) {
	if x == nil {
		return nil, fmt.Errorf("XGR interchain RPC endpoint is unavailable")
	}

	name := strings.ToLower(strings.TrimSpace(destinationName))
	if name == "" {
		return nil, fmt.Errorf("interchain destination name is required")
	}
	configured, err := interchainEVM.LoadAll()
	if err != nil {
		return nil, fmt.Errorf("read configured interchain destinations: %w", err)
	}
	var destination *interchainEVM.Destination
	for _, candidate := range configured {
		if candidate != nil && strings.EqualFold(candidate.Name, name) {
			destination = candidate
			break
		}
	}
	if destination == nil {
		return nil, fmt.Errorf("interchain destination %q is not configured on this node", destinationName)
	}
	if err := destination.ValidateMembershipRead(); err != nil {
		return nil, err
	}
	set, err := interchainEVM.GetValidatorSet(destination)
	if err != nil {
		return nil, fmt.Errorf("read current interchain validator registry: %w", err)
	}
	return formatInterchainValidators(destination, set)
}

func formatInterchainValidators(destination *interchainEVM.Destination, set *interchainEVM.ValidatorSet) (*interchainValidatorsRPC, error) {
	if destination == nil || set == nil {
		return nil, fmt.Errorf("invalid interchain registry validator set")
	}
	adapter, err := destination.AuthorizationAdapter()
	if err != nil {
		return nil, err
	}
	if set.Scheme != "" && set.Scheme != adapter.Scheme() {
		return nil, fmt.Errorf("interchain registry signature scheme differs from configured destination")
	}
	canonical := set.AuthorizationSet()
	if err := adapter.ValidateSet(canonical); err != nil {
		return nil, fmt.Errorf("invalid interchain registry validator set: %w", err)
	}
	out := &interchainValidatorsRPC{
		Destination:     destination.Name,
		ChainID:         destination.ChainID,
		Domain:          destination.Domain,
		Registry:        destination.RegistryAddress,
		SignatureScheme: adapter.Scheme(),
		SetID:           set.SetID,
		Count:           len(set.Validators),
		// Solidity Registry V3: ceil(2*n/3), with one-validator sets allowed.
		QuorumThreshold: (2*len(set.Validators) + 2) / 3,
		Validators:      make([]interchainActiveValidatorRPC, 0, len(set.Validators)),
	}
	for i, addr := range set.Validators {
		validator := interchainActiveValidatorRPC{Address: addr.String()}
		if adapter.Scheme() == authorization.SchemeBLS {
			validator.BLSPublicKey = "0x" + hex.EncodeToString(canonical.PublicKeys[i])
		} else {
			validator.ECDSASigner = addr.String()
		}
		out.Validators = append(out.Validators, validator)
	}
	return out, nil
}
