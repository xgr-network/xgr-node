package evm

import (
	"fmt"
	"math/big"

	"github.com/xgr-network/xgr-node/types"
)

type MembershipPoll struct {
	Set          *ValidatorSet
	SetError     error
	Minimum      *big.Int
	Local        *ValidatorDetails
	ReserveError error
}

func destinationRegistryView(destination *Destination) ILNView {
	return func(address types.Address, calldata []byte) ([]byte, error) {
		return DestinationCall(destination, address, calldata)
	}
}

// PollMembershipRegistry reads the periodic membership state using a single
// provider verification. The set and reserve checks have independent results.
func PollMembershipRegistry(destination *Destination, validator types.Address) (MembershipPoll, error) {
	if err := destination.ValidateMembershipRead(); err != nil {
		return MembershipPoll{}, err
	}
	view := destinationRegistryView(destination)
	if destination.ChainID != 1643 {
		client, err := newVerifiedRPCClient(destination)
		if err != nil {
			return MembershipPoll{}, err
		}
		defer client.Close()
		read := rpcILNView(client)
		view = func(address types.Address, calldata []byte) ([]byte, error) {
			if address == types.ZeroAddress {
				return nil, fmt.Errorf("invalid destination EVM view")
			}
			if bound, ok := client.(*boundedILNRPC); ok {
				if err := rpcCooldown(destination, bound.endpoint); err != nil {
					return nil, err
				}
			}
			raw, err := read(address, calldata)
			if err != nil {
				recordInterchainRPCReadForClient(destination, client, err)
			}
			return raw, err
		}
	}

	registry := types.StringToAddress(destination.RegistryAddress)
	var poll MembershipPoll
	poll.Set, poll.SetError = readValidatorSet(destination, registry, view)
	poll.Minimum, poll.ReserveError = readMinimumDeactivationReserve(registry, view)
	if poll.ReserveError != nil {
		poll.ReserveError = fmt.Errorf("read on-chain deactivation minimum: %w", poll.ReserveError)
		return poll, nil
	}
	poll.Local, poll.ReserveError = readValidator(destination, registry, validator, view)
	return poll, nil
}
