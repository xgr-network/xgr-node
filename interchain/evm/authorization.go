package evm

import (
	"fmt"
	"math/big"
	"math/bits"

	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/interchain/authorization"
	"github.com/xgr-network/xgr-node/types"
)

var ecdsaMembershipABI = abi.MustNewABI(`[
 {"name":"applyMembership","type":"function","stateMutability":"payable","inputs":[
  {"name":"transition","type":"tuple","components":[
   {"name":"expectedSetId","type":"uint64"},{"name":"validUntil","type":"uint64"},
   {"name":"action","type":"uint8"},{"name":"validator","type":"address"}]},
  {"name":"signerBitmap","type":"bytes"},{"name":"signatures","type":"bytes"}],"outputs":[]}
]`)

// FormatQuorumProof converts only the signature representation required by the
// selected verifier. Bitmap bytes retain their scheme-specific ordering and
// length; in particular, ECDSA bitmaps must never pass through big.Int.Bytes.
// Cryptographic verification belongs to the typed authorization adapter and
// is performed before every state-changing submission.
func FormatQuorumProof(destination *Destination, proof authorization.Proof) (authorization.Proof, error) {
	adapter, err := destination.AuthorizationAdapter()
	if err != nil {
		return authorization.Proof{}, err
	}
	if len(proof.Bitmap) == 0 || len(proof.Signature) == 0 {
		return authorization.Proof{}, fmt.Errorf("missing quorum bitmap or signature")
	}
	out := authorization.Proof{Bitmap: append([]byte(nil), proof.Bitmap...)}
	switch adapter.Scheme() {
	case authorization.SchemeBLS:
		if proof.Bitmap[0] == 0 {
			return authorization.Proof{}, fmt.Errorf("noncanonical BLS signer bitmap")
		}
		out.Signature, err = FormatAggregateSignature(destination, proof.Signature)
	case authorization.SchemeECDSA:
		if len(proof.Bitmap) > 8 {
			return authorization.Proof{}, fmt.Errorf("ECDSA signer bitmap exceeds 64 validators")
		}
		count := 0
		for _, b := range proof.Bitmap {
			count += bits.OnesCount8(b)
		}
		if count == 0 || len(proof.Signature) != 65*count {
			return authorization.Proof{}, fmt.Errorf("ECDSA signer bitmap/signature count mismatch")
		}
		for offset := 0; offset < len(proof.Signature); offset += 65 {
			sig := proof.Signature[offset : offset+65]
			if (sig[64] != 27 && sig[64] != 28) || !crypto.ValidateSignatureValues(
				big.NewInt(int64(sig[64]-27)), new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:64]), true) {
				return authorization.Proof{}, fmt.Errorf("noncanonical ECDSA quorum signature")
			}
		}
		out.Signature = append([]byte(nil), proof.Signature...)
	default:
		return authorization.Proof{}, fmt.Errorf("unsupported authorization scheme")
	}
	return out, err
}

func validateMembershipDestination(destination *Destination, membership authorization.Membership) (*authorization.Adapter, error) {
	adapter, err := destination.AuthorizationAdapter()
	if err != nil {
		return nil, err
	}
	if membership.DestinationDomain != destination.Domain {
		return nil, fmt.Errorf("payload destination domain mismatch: payload=%d configured=%d", membership.DestinationDomain, destination.Domain)
	}
	registry := types.StringToAddress(destination.RegistryAddress)
	if registry == types.ZeroAddress {
		return nil, fmt.Errorf("membership registry is not configured")
	}
	if (adapter.Scheme() == authorization.SchemeECDSA || membership.Registry != types.ZeroAddress) && membership.Registry != registry {
		return nil, fmt.Errorf("membership payload registry differs from configured destination")
	}
	if _, err := adapter.EncodeMembership(membership); err != nil {
		return nil, err
	}
	return adapter, nil
}

// EncodeMembershipCalldata preserves the deployed BLS tuple ABI and selects
// the ECDSA tuple solely through the configured adapter. It does not sign or
// submit and does not decide whether the proof has current-set authority.
func EncodeMembershipCalldata(destination *Destination, membership authorization.Membership, proof authorization.Proof) ([]byte, error) {
	adapter, err := validateMembershipDestination(destination, membership)
	if err != nil {
		return nil, err
	}
	formatted, err := FormatQuorumProof(destination, proof)
	if err != nil {
		return nil, err
	}
	transition := map[string]interface{}{
		"expectedSetId": new(big.Int).SetUint64(membership.ExpectedSetID),
		"validUntil":    new(big.Int).SetUint64(membership.ValidUntil),
		"action":        uint8(membership.Action), "validator": ethgo.Address(membership.Validator),
	}
	method := registryABI.Methods["applyMembership"]
	values := map[string]interface{}{"transition": transition, "signerBitmap": formatted.Bitmap}
	if adapter.Scheme() == authorization.SchemeBLS {
		transition["blsPublicKey"] = membership.BLSPublicKey
		transition["blsPublicKeyEIP2537"] = membership.BLSPublicKeyEIP2537
		values["aggregateSignature"] = formatted.Signature
	} else {
		method = ecdsaMembershipABI.Methods["applyMembership"]
		values["signatures"] = formatted.Signature
	}
	if method == nil {
		return nil, fmt.Errorf("registry ABI missing applyMembership")
	}
	input, err := method.Inputs.Encode(values)
	if err != nil {
		return nil, fmt.Errorf("encode applyMembership: %w", err)
	}
	return append(append([]byte(nil), method.ID()...), input...), nil
}
