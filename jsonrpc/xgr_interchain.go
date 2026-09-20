package jsonrpc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	xgrsvc "github.com/xgr-network/xgr-node/jsonrpc/xgr"
)

const maxInterchainAttestationBytes int64 = 1 << 20

type xgrEndpoint struct {
	*xgrsvc.XGR
	dataDir string
}

type interchainAttestationRPC struct {
	Version                      string `json:"version"`
	Chain                        string `json:"chain"`
	OriginChainID                uint64 `json:"originChainId"`
	DestinationDomain            uint32 `json:"destinationDomain"`
	SetID                        uint64 `json:"setId"`
	Mailbox                      string `json:"mailbox"`
	MerkleTreeHook               string `json:"merkleTreeHook"`
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
// checkpoint attestation for one configured destination. It is read-only and
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

// GetInterchainAttestationByCheckpoint returns one archived completed
// attestation. The relayer may request an exact checkpoint, but the node never
// signs based on RPC input.
func (x *xgrEndpoint) GetInterchainAttestationByCheckpoint(
	chain string,
	setID uint64,
	index uint32,
	root string,
) (*interchainAttestationRPC, error) {
	normalized, err := normalizeInterchainRPCChain(chain)
	if err != nil {
		return nil, err
	}
	if setID == 0 {
		return nil, fmt.Errorf("interchain set id must be non-zero")
	}
	root = strings.ToLower(strings.TrimSpace(root))
	if len(root) != 66 || !strings.HasPrefix(root, "0x") {
		return nil, fmt.Errorf("interchain root must be a 32-byte 0x-prefixed hash")
	}
	for _, r := range root[2:] {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return nil, fmt.Errorf("interchain root must be hexadecimal")
		}
	}
	name := strconv.FormatUint(setID, 10) + "-" + strconv.FormatUint(uint64(index), 10) + "-" + root + ".json"
	return x.readInterchainAttestation(filepath.Join(
		x.dataDir, "interchain", "attestations", normalized, name,
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
	if out.Chain == "" || out.Root == "" || out.Payload == "" || out.AggregateSignature == "" {
		return nil, fmt.Errorf("interchain attestation is incomplete")
	}
	return &out, nil
}

func normalizeInterchainRPCChain(chain string) (string, error) {
	chain = strings.TrimSpace(chain)
	if chain == "" {
		return "", fmt.Errorf("interchain destination is required")
	}
	for _, r := range chain {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			continue
		}
		return "", fmt.Errorf("invalid interchain destination")
	}
	return strings.ToLower(chain), nil
}
