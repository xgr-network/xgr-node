package runtime

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"time"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	stakingcontract "github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/interchain/authorization"
	evm "github.com/xgr-network/xgr-node/interchain/evm"
	"github.com/xgr-network/xgr-node/types"
)

var getMembershipValidatorSet = evm.GetValidatorSet

// The V3 worker owns eligibility, evidence, consent and recovery. An explicitly
// configured adapter owns only key identity and the typed signature wire format.
func (w *Worker) authorizationKeys() authorization.Keys {
	return authorization.Keys{ECDSA: w.ecdsaKey, BLS: w.blsRaw}
}

func (w *Worker) localVerificationKey(adapter *authorization.Adapter) []byte {
	if adapter.Scheme() == authorization.SchemeECDSA {
		return append([]byte(nil), w.localAddr[:]...)
	}
	return append([]byte(nil), w.blsPubKey...)
}

func validatorIdentityMatches(adapter *authorization.Adapter, validator types.Address, info *stakingcontract.ValidatorInfo, key []byte) bool {
	if info == nil || !info.Exists {
		return false
	}
	if adapter.Scheme() == authorization.SchemeECDSA {
		return validator != types.ZeroAddress && bytes.Equal(key, validator[:])
	}
	return bytes.Equal(info.BLSPubKey, key)
}

func membershipVerificationKey(adapter *authorization.Adapter, p authorization.Membership) []byte {
	if adapter.Scheme() == authorization.SchemeECDSA {
		return append([]byte(nil), p.Validator[:]...)
	}
	return p.BLSPublicKey
}

func candidateSet(adapter *authorization.Adapter, p authorization.Membership) authorization.Set {
	return authorization.Set{SetID: p.ExpectedSetID, Validators: []types.Address{p.Validator}, PublicKeys: [][]byte{membershipVerificationKey(adapter, p)}}
}

func validateAuthorizationSet(destination *evm.Destination, set *evm.ValidatorSet) (*authorization.Adapter, error) {
	adapter, err := destination.AuthorizationAdapter()
	if err != nil {
		return nil, err
	}
	if set == nil {
		return nil, fmt.Errorf("destination validator set unavailable")
	}
	scheme := set.Scheme
	if scheme == "" {
		scheme = authorization.SchemeBLS
	}
	if scheme != adapter.Scheme() {
		return nil, fmt.Errorf("validator set signature scheme differs from configured destination")
	}
	if err := adapter.ValidateSet(set.AuthorizationSet()); err != nil {
		return nil, err
	}
	return adapter, nil
}

func (w *Worker) membershipPayload(destination *evm.Destination, adapter *authorization.Adapter, setID uint64, action protocol.Action, validator types.Address, key []byte) (authorization.Membership, error) {
	p := authorization.Membership{
		OriginChainID: w.state.OriginChainID(), DestinationDomain: destination.Domain,
		Registry: types.StringToAddress(destination.RegistryAddress), ExpectedSetID: setID,
		ValidUntil: uint64(time.Now().Add(time.Duration(destination.MembershipValiditySeconds) * time.Second).Unix()),
		Action:     action, Validator: validator,
	}
	if adapter.Scheme() == authorization.SchemeBLS {
		p.BLSPublicKey = append([]byte(nil), key...)
		var err error
		p.BLSPublicKeyEIP2537, err = crypto.BLSPublicKeyToEIP2537(key)
		if err != nil {
			return p, err
		}
	}
	return p, nil
}

// Parse only through adapters selected by configured destinations. The peer's
// payload cannot enable another scheme or choose another registry.
func (w *Worker) decodeDirectPayload(raw []byte) (protocol.XITADirectMessage, *evm.Destination, error) {
	var decoded protocol.XITADirectMessage
	var matched *evm.Destination
	for _, destination := range w.destinations {
		adapter, err := destination.AuthorizationAdapter()
		if err != nil {
			continue
		}
		p, err := adapter.DecodeDirect(raw)
		if err != nil || p.DestinationDomain != destination.Domain ||
			p.DestinationRegistry != types.StringToAddress(destination.RegistryAddress) {
			continue
		}
		if matched != nil {
			return decoded, nil, fmt.Errorf("ambiguous configured direct-message destination")
		}
		decoded, matched = p, destination
	}
	if matched == nil {
		return decoded, nil, fmt.Errorf("direct payload does not match a configured destination and signature scheme")
	}
	return decoded, matched, nil
}

// BLS archives keep their released fields byte-for-byte. ECDSA is explicitly
// tagged and never stores packed signatures under a compressed-BLS field.
func encodeProofArchive(adapter *authorization.Adapter, proof authorization.Proof) (scheme, aggregate, compressed, signatures string, err error) {
	expanded, err := adapter.ExpandedSignature(proof)
	if err != nil {
		return "", "", "", "", err
	}
	if adapter.Scheme() == authorization.SchemeECDSA {
		return authorization.SchemeECDSA, "", "", "0x" + hex.EncodeToString(expanded), nil
	}
	return "", "0x" + hex.EncodeToString(expanded), "0x" + hex.EncodeToString(proof.Signature), "", nil
}

func decodeProofArchive(adapter *authorization.Adapter, scheme, bitmap, aggregate, compressed, signatures string) (authorization.Proof, error) {
	proof := authorization.Proof{}
	var err error
	proof.Bitmap, err = ilnUnhexBounded(bitmap, 256)
	if err != nil {
		return proof, err
	}
	if adapter.Scheme() == authorization.SchemeECDSA {
		if scheme != authorization.SchemeECDSA || aggregate != "" || compressed != "" {
			return proof, fmt.Errorf("ECDSA proof archive scheme or representation mismatch")
		}
		proof.Signature, err = ilnUnhexBounded(signatures, 64*65)
		return proof, err
	}
	if (scheme != "" && scheme != authorization.SchemeBLS) || signatures != "" {
		return proof, fmt.Errorf("BLS proof archive scheme or representation mismatch")
	}
	proof.Signature, err = ilnUnhexBounded(compressed, 96)
	if err != nil {
		return proof, err
	}
	expanded, err := adapter.ExpandedSignature(proof)
	if err != nil {
		return proof, err
	}
	archived, err := ilnUnhexBounded(aggregate, 256)
	if err != nil {
		return proof, err
	}
	if !bytes.Equal(expanded, archived) {
		return proof, fmt.Errorf("mismatched expanded and compressed BLS signatures")
	}
	return proof, nil
}
