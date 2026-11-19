package jsonrpc

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/interchain/authorization"
	evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
	xgrsvc "github.com/xgr-network/xgr-node/jsonrpc/xgr"
)

const maxInterchainAttestationBytes int64 = 1 << 20

type xgrEndpoint struct {
	*xgrsvc.XGR
	dataDir string
}

type interchainSourceFeeQuorumRPC struct {
	Version                      string `json:"version"`
	ProposalID                   string `json:"proposalId"`
	SourceChainID                uint64 `json:"sourceChainId"`
	SourceDomain                 uint32 `json:"sourceDomain"`
	Registry                     string `json:"registry"`
	ValidatorFeeWei              string `json:"validatorFeeWei"`
	SetID                        uint64 `json:"setId"`
	Nonce                        uint64 `json:"nonce"`
	ValidUntil                   uint64 `json:"validUntil"`
	Payload                      string `json:"payload"`
	SignerBitmap                 string `json:"signerBitmap"`
	AggregateSignature           string `json:"aggregateSignature,omitempty"`
	AggregateSignatureCompressed string `json:"aggregateSignatureCompressed,omitempty"`
	SignatureScheme              string `json:"signatureScheme,omitempty"`
	Signatures                   string `json:"signatures,omitempty"`
}

type interchainAttestationRPC struct {
	Version                      string `json:"version"`
	Chain                        string `json:"chain"`
	Destination                  string `json:"destination,omitempty"`
	OriginChainID                uint64 `json:"originChainId"`
	OriginDomain                 uint32 `json:"originDomain,omitempty"`
	DestinationDomain            uint32 `json:"destinationDomain"`
	RouteID                      string `json:"routeId"`
	SetID                        uint64 `json:"setId"`
	SourceBlockNumber            uint64 `json:"sourceBlockNumber"`
	Registry                     string `json:"registry"`
	Gateway                      string `json:"gateway"`
	SourceRouter                 string `json:"sourceRouter"`
	Mailbox                      string `json:"mailbox"`
	DestinationRouter            string `json:"destinationRouter"`
	ValidatorFeeWei              string `json:"validatorFeeWei"`
	AuthorizedMessageID          string `json:"authorizedMessageId"`
	DestinationISM               string `json:"destinationIsm"`
	DestinationRegistry          string `json:"destinationRegistry"`
	Payload                      string `json:"payload"`
	SignerBitmap                 string `json:"signerBitmap"`
	AggregateSignature           string `json:"aggregateSignature,omitempty"`
	AggregateSignatureCompressed string `json:"aggregateSignatureCompressed,omitempty"`
	SignatureScheme              string `json:"signatureScheme,omitempty"`
	Signatures                   string `json:"signatures,omitempty"`
}

func newXGREndpoint(base *xgrsvc.XGR, dataDir string) *xgrEndpoint {
	return &xgrEndpoint{XGR: base, dataDir: dataDir}
}

// GetInterchainAttestation returns the latest completed native XGR interchain
// direct-message attestation for one route. It is read-only and
// never triggers signing.
func (x *xgrEndpoint) GetInterchainAttestation(chain string) (*interchainAttestationRPC, error) {
	if x == nil {
		return nil, fmt.Errorf("interchain attestation storage is unavailable")
	}
	normalized, err := normalizeInterchainRPCChain(chain)
	if err != nil {
		return nil, err
	}
	out, err := x.readInterchainAttestation(filepath.Join(
		x.dataDir, "interchain", "attestations", normalized, "latest.json",
	))
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(out.Chain, normalized) {
		return nil, fmt.Errorf("interchain attestation route mismatch")
	}
	return out, nil
}

// GetILNInterchainAttestation returns a completed direct-message attestation for
// one fee-qualified Hyperlane message ID. It is read-only.
func (x *xgrEndpoint) GetILNInterchainAttestation(
	chain string,
	messageID string,
) (*interchainAttestationRPC, error) {
	if x == nil {
		return nil, fmt.Errorf("interchain attestation storage is unavailable")
	}
	normalized, err := normalizeInterchainRPCChain(chain)
	if err != nil {
		return nil, err
	}
	messageID = strings.ToLower(strings.TrimSpace(messageID))
	if len(messageID) != 66 || !strings.HasPrefix(messageID, "0x") {
		return nil, fmt.Errorf("ILN message id must be a 32-byte 0x-prefixed hash")
	}
	for _, r := range messageID[2:] {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return nil, fmt.Errorf("ILN message id must be hexadecimal")
		}
	}
	out, err := x.readInterchainAttestation(filepath.Join(
		x.dataDir, "interchain", "attestations", normalized, messageID+".json",
	))
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(out.Chain, normalized) || !strings.EqualFold(out.AuthorizedMessageID, messageID) {
		return nil, fmt.Errorf("interchain attestation route/message mismatch")
	}
	return out, nil
}

func (x *xgrEndpoint) readInterchainAttestation(path string) (*interchainAttestationRPC, error) {
	if x == nil || strings.TrimSpace(x.dataDir) == "" {
		return nil, fmt.Errorf("interchain attestation storage is unavailable")
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("interchain attestation not found")
		}
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxInterchainAttestationBytes {
		return nil, fmt.Errorf("interchain attestation file is invalid")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out interchainAttestationRPC
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode interchain attestation: %w", err)
	}
	adapter, err := archiveAuthorizationAdapter(out.SignatureScheme)
	if err != nil {
		return nil, err
	}
	if out.Version != adapter.DirectDomain() || out.Chain == "" || out.RouteID == "" || out.AuthorizedMessageID == "" ||
		out.SourceRouter == "" || out.Payload == "" || out.DestinationISM == "" || out.DestinationRegistry == "" || out.SignerBitmap == "" {
		return nil, fmt.Errorf("direct interchain attestation is incomplete or uses a different signature scheme")
	}
	rawPayload, err := decodeArchiveHex(out.Payload, 2048)
	if err != nil {
		return nil, fmt.Errorf("malformed direct signed payload: %w", err)
	}
	p, err := adapter.DecodeDirect(rawPayload)
	if err != nil {
		return nil, fmt.Errorf("invalid direct signing domain/payload: %w", err)
	}

	if p.SourceChainID != out.OriginChainID || p.SourceDomain != out.OriginDomain ||
		p.DestinationDomain != out.DestinationDomain || p.SetID != out.SetID ||
		p.SourceBlockNumber != out.SourceBlockNumber ||
		!strings.EqualFold(p.RouteID.String(), out.RouteID) ||
		!strings.EqualFold(p.SourceRegistry.String(), out.Registry) ||
		!strings.EqualFold(p.Gateway.String(), out.Gateway) ||
		!strings.EqualFold(p.SourceRouter.String(), out.SourceRouter) ||
		!strings.EqualFold(p.SourceMailbox.String(), out.Mailbox) ||
		!strings.EqualFold(p.DestinationRouter.String(), out.DestinationRouter) ||
		p.ValidatorFeeWei.String() != out.ValidatorFeeWei ||
		!strings.EqualFold(p.MessageID.String(), out.AuthorizedMessageID) ||
		!strings.EqualFold(p.DestinationISM.String(), out.DestinationISM) ||
		!strings.EqualFold(p.DestinationRegistry.String(), out.DestinationRegistry) {
		return nil, fmt.Errorf("Direct archived fields do not match canonical signed bytes")
	}
	if err := validateArchiveProof(adapter, out.SignerBitmap, out.AggregateSignature, out.AggregateSignatureCompressed, out.Signatures); err != nil {
		return nil, err
	}

	return &out, nil
}

// GetILNGovernanceQuorum reads a completed v3.1.5 source-chain fee quorum.
// The RPC method name remains stable; route-governance proposals are removed.
// Reading never creates, approves, signs or executes a proposal.
func (x *xgrEndpoint) GetILNGovernanceQuorum(proposalID string) (*interchainSourceFeeQuorumRPC, error) {
	if x == nil || strings.TrimSpace(x.dataDir) == "" {
		return nil, fmt.Errorf("ILN governance quorum storage is unavailable")
	}
	proposalID = strings.ToLower(strings.TrimSpace(proposalID))
	if len(proposalID) != 66 || !strings.HasPrefix(proposalID, "0x") {
		return nil, fmt.Errorf("ILN governance proposal id must be a 32-byte 0x-prefixed hash")
	}
	for _, r := range proposalID[2:] {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return nil, fmt.Errorf("ILN governance proposal id must be hexadecimal")
		}
	}

	path := filepath.Join(
		x.dataDir, "interchain", "governance", "quorums", proposalID+".json",
	)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("ILN governance quorum not found")
		}
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxInterchainAttestationBytes {
		return nil, fmt.Errorf("ILN governance quorum file is invalid")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out interchainSourceFeeQuorumRPC
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode ILN governance quorum: %w", err)
	}
	if strings.ToLower(out.ProposalID) != proposalID ||
		out.Version != protocol.ILNFeeDomainV315 ||
		out.SourceChainID == 0 || out.SourceDomain == 0 ||
		out.Registry == "" || out.SetID == 0 || out.Nonce == 0 ||
		out.ValidUntil == 0 || out.ValidatorFeeWei == "" ||
		out.Payload == "" || out.SignerBitmap == "" {
		return nil, fmt.Errorf("ILN source fee quorum is incomplete")
	}
	if !strings.HasPrefix(out.Payload, "0x") {
		return nil, fmt.Errorf("ILN source fee quorum payload must be hexadecimal")
	}
	payload, err := hex.DecodeString(out.Payload[2:])
	if err != nil {
		return nil, fmt.Errorf("ILN source fee quorum payload is invalid")
	}
	var proposal protocol.ILNSourceFeeProposal
	if err := proposal.UnmarshalBinary(payload); err != nil {
		return nil, fmt.Errorf("ILN source fee quorum payload is invalid: %w", err)
	}
	if crypto.Keccak256Hash(payload).String() != proposalID ||
		proposal.SourceChainID != out.SourceChainID ||
		proposal.SourceDomain != out.SourceDomain ||
		!strings.EqualFold(proposal.Registry.String(), out.Registry) ||
		proposal.SetID != out.SetID || proposal.Nonce != out.Nonce ||
		proposal.ValidUntil != out.ValidUntil ||
		proposal.ValidatorFeeWei.String() != out.ValidatorFeeWei {
		return nil, fmt.Errorf("ILN source fee quorum does not match signed proposal")
	}
	adapter, err := archiveAuthorizationAdapter(out.SignatureScheme)
	if err != nil {
		return nil, err
	}
	if err := validateArchiveProof(adapter, out.SignerBitmap, out.AggregateSignature, out.AggregateSignatureCompressed, out.Signatures); err != nil {
		return nil, err
	}
	return &out, nil
}

// Archive decoding is offline and does not grant signing or execution
// authority. An absent scheme is the released BLS archive schema; ECDSA must
// be explicitly tagged. Live configured sets remain mandatory at execution.
func archiveAuthorizationAdapter(scheme string) (*authorization.Adapter, error) {
	if scheme == "" {
		scheme = authorization.SchemeBLS
	}
	return authorization.ForScheme(scheme)
}

func decodeArchiveHex(value string, maxBytes int) ([]byte, error) {
	if !strings.HasPrefix(value, "0x") || len(value) < 4 || len(value)%2 != 0 || len(value) > 2+2*maxBytes {
		return nil, fmt.Errorf("invalid bounded quorum hex")
	}
	return hex.DecodeString(value[2:])
}

// validateArchiveProof checks only canonical representation, never membership
// authority. Raw historical proofs are still AVAILABLE_UNVERIFIED until an
// executor/worker verifies the current canonical destination registry set.
func validateArchiveProof(adapter *authorization.Adapter, bitmap, aggregate, compressed, signatures string) error {
	rawBitmap, err := decodeArchiveHex(bitmap, 256)
	if err != nil {
		return fmt.Errorf("malformed signer bitmap: %w", err)
	}
	destination := &evmInterchain.Destination{SignatureScheme: adapter.Scheme()}
	var rawSignature []byte
	if adapter.Scheme() == authorization.SchemeECDSA {
		if aggregate != "" || compressed != "" {
			return fmt.Errorf("ECDSA archive contains BLS signature fields")
		}
		rawSignature, err = decodeArchiveHex(signatures, 64*65)
		if err != nil {
			return fmt.Errorf("malformed ECDSA signatures: %w", err)
		}
		destination.VerifierFormat = evmInterchain.VerifierFormatECDSA
	} else {
		if signatures != "" {
			return fmt.Errorf("BLS archive contains ECDSA signatures")
		}
		rawSignature, err = decodeArchiveHex(compressed, 96)
		if err != nil || len(rawSignature) != 96 {
			return fmt.Errorf("malformed compressed BLS aggregate")
		}
		destination.VerifierFormat = evmInterchain.VerifierFormatEIP2537
	}
	formatted, err := evmInterchain.FormatQuorumProof(destination, authorization.Proof{Bitmap: rawBitmap, Signature: rawSignature})
	if err != nil {
		return fmt.Errorf("malformed archived quorum proof: %w", err)
	}
	if adapter.Scheme() == authorization.SchemeBLS {
		expanded, err := decodeArchiveHex(aggregate, 256)
		if err != nil || len(expanded) != 256 || !bytes.Equal(expanded, formatted.Signature) {
			return fmt.Errorf("BLS archive has mismatched compressed/expanded aggregate")
		}
	}
	return nil
}

func normalizeInterchainRPCChain(chain string) (string, error) {
	chain = strings.TrimSpace(chain)
	if chain == "" {
		return "", fmt.Errorf("interchain route is required")
	}
	for _, r := range chain {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			continue
		}
		return "", fmt.Errorf("invalid interchain route")
	}
	return strings.ToLower(chain), nil
}
