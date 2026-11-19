package ecdsa

import (
	stdECDSA "crypto/ecdsa"
	"fmt"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/types"
)

// SignDirectV5 prepares a local typed ECDSA signature for ONE canonical ILN
// message. This helper does NOT check source receipts or PoS eligibility:
// callers MUST first satisfy all existing BLS path evidence checks and the
// selected chain's V5 validator-set/replay policy. It is never an RPC signer.
func SignDirectV5(key *stdECDSA.PrivateKey, message protocol.XITADirectMessage) ([]byte, []byte, error) {
	if key == nil {
		return nil, nil, ErrInvalidLifecycle
	}
	raw, err := EncodeDirect(message)
	if err != nil {
		return nil, nil, err
	}
	sig, err := signTyped(key, raw)
	return raw, sig, err
}

// AssembleDirectQuorumV5 recovers every vote against the validator ECDSA
// addresses from the destination's independently verified V5 registry epoch.
// Never substitute BLS public keys or a validator list from another chain.
func AssembleDirectQuorumV5(message protocol.XITADirectMessage, signers []types.Address, votes []VoteV5) ([]byte, []byte, error) {
	raw, err := EncodeDirect(message)
	if err != nil {
		return nil, nil, err
	}
	return assembleTyped(raw, signers, votes)
}
func VerifyDirectQuorumV5(message protocol.XITADirectMessage, signers []types.Address, bitmap, sigs []byte) bool {
	raw, err := EncodeDirect(message)
	return err == nil && verifyTypedQuorum(raw, signers, bitmap, sigs)
}

func VerifyDirectVoteV5(message protocol.XITADirectMessage, signer types.Address, signature []byte) bool {
	raw, err := EncodeDirect(message)
	return err == nil && verifyOne(raw, signer, signature)
}

// The existing SourceRegistry v3.1.5 on each destination uses the same
// canonical proposal payload but selects a scheme-specific signature verifier.
// V5 ECDSA signs keccak256(proposal.MarshalBinary()) with no Ethereum prefix.
func SignSourceFeeV5(key *stdECDSA.PrivateKey, proposal protocol.ILNSourceFeeProposal) ([]byte, []byte, error) {
	if key == nil {
		return nil, nil, ErrInvalidLifecycle
	}
	payload, err := proposal.MarshalBinary()
	if err != nil {
		return nil, nil, fmt.Errorf("invalid XDC source fee proposal: %w", err)
	}
	sig, err := signTyped(key, payload)
	return payload, sig, err
}
func AssembleSourceFeeQuorumV5(proposal protocol.ILNSourceFeeProposal, signers []types.Address, votes []VoteV5) ([]byte, []byte, error) {
	payload, err := proposal.MarshalBinary()
	if err != nil {
		return nil, nil, err
	}
	return assembleTyped(payload, signers, votes)
}
func VerifySourceFeeQuorumV5(proposal protocol.ILNSourceFeeProposal, signers []types.Address, bitmap, sigs []byte) bool {
	payload, err := proposal.MarshalBinary()
	return err == nil && verifyTypedQuorum(payload, signers, bitmap, sigs)
}

func VerifySourceFeeVoteV5(proposal protocol.ILNSourceFeeProposal, signer types.Address, signature []byte) bool {
	raw, err := proposal.MarshalBinary()
	return err == nil && verifyOne(raw, signer, signature)
}
