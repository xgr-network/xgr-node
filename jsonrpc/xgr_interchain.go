package jsonrpc

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
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
	AggregateSignature           string `json:"aggregateSignature"`
	AggregateSignatureCompressed string `json:"aggregateSignatureCompressed"`
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
	MerkleTreeHook               string `json:"merkleTreeHook"`
	DestinationRouter            string `json:"destinationRouter"`
	ValidatorFeeWei              string `json:"validatorFeeWei"`
	AuthorizedMessageID          string `json:"authorizedMessageId"`
	Root                         string `json:"root"`
	Index                        uint32 `json:"index"`
	Payload                      string `json:"payload"`
	SignerBitmap                 string `json:"signerBitmap"`
	AggregateSignature           string `json:"aggregateSignature"`
	AggregateSignatureCompressed string `json:"aggregateSignatureCompressed"`
}

func newXGREndpoint(base *xgrsvc.XGR, dataDir string) *xgrEndpoint {
	return &xgrEndpoint{XGR: base, dataDir: dataDir}
}

// GetInterchainAttestation returns the latest completed native XGR interchain
// checkpoint attestation for one configured route. It is read-only and
// never triggers signing.
func (x *xgrEndpoint) GetInterchainAttestation(chain string) (*interchainAttestationRPC, error) {
	normalized, err := normalizeInterchainRPCChain(chain)
	if err != nil {
		return nil, err
	}
	return x.readInterchainAttestation(filepath.Join(
		x.dataDir, "interchain", "attestations", normalized, "latest.json",
	))
}

// GetILNInterchainAttestation returns the completed v3.1.3 attestation for
// one fee-qualified Hyperlane message ID. It is read-only.
func (x *xgrEndpoint) GetILNInterchainAttestation(
	chain string,
	messageID string,
) (*interchainAttestationRPC, error) {
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
	return x.readInterchainAttestation(filepath.Join(
		x.dataDir, "interchain", "attestations", normalized, messageID+".json",
	))
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
	if out.Version != "XGR_ILN_CHECKPOINT_V2" ||
		out.Chain == "" ||
		out.RouteID == "" ||
		out.AuthorizedMessageID == "" ||
		out.SourceRouter == "" ||
		out.Root == "" ||
		out.Payload == "" ||
		out.AggregateSignature == "" {
		return nil, fmt.Errorf("interchain attestation is incomplete")
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
		out.Payload == "" || out.SignerBitmap == "" ||
		out.AggregateSignature == "" {
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
	return &out, nil
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
