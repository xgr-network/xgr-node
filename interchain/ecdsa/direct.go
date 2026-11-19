// Package ecdsa implements the explicit secp256k1 authorization adapter.
// Chain activation, evidence, PoS eligibility and local approval remain the
// responsibility of the shared interchain runtime.
package ecdsa

import (
	"errors"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

const Domain = protocol.XITADirectMessageECDSADomain
const MaxValidators = 64
const SignatureSize = 65

var ErrInvalidAuthorization = errors.New("invalid XITA direct ECDSA authorization")

// EncodeDirect uses the common field validation and encoding directly. It
// never constructs or rewrites a BLS authorization.
func EncodeDirect(message protocol.XITADirectMessage) ([]byte, error) {
	return message.MarshalBinaryWithDomain(Domain)
}

// DecodeDirect rejects trailing data, foreign domains and invalid fields.
func DecodeDirect(raw []byte) (protocol.XITADirectMessage, error) {
	var message protocol.XITADirectMessage
	if err := message.UnmarshalBinaryWithDomain(raw, Domain); err != nil {
		return protocol.XITADirectMessage{}, ErrInvalidAuthorization
	}
	return message, nil
}

func Digest(raw []byte) (types.Hash, error) {
	if _, err := DecodeDirect(raw); err != nil {
		return types.ZeroHash, err
	}
	return crypto.Keccak256Hash(raw), nil
}

// VerifyQuorum is retained for raw-wire callers but delegates to the same
// typed verifier as the worker adapter, including duplicate identity checks.
func VerifyQuorum(raw []byte, validators []types.Address, bitmap, sigs []byte) bool {
	message, err := DecodeDirect(raw)
	return err == nil && VerifyDirectQuorumV5(message, validators, bitmap, sigs)
}
