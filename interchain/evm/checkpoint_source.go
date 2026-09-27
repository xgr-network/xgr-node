package evm

import (
	"fmt"
	"math/big"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/jsonrpc"

	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

const (
	CheckpointSourceLocal = "local"
	CheckpointSourceEVM   = "evm"
)

// CheckpointSource describes an external EVM / Hyperlane origin whose
// confirmation-delayed MerkleTreeHook checkpoint is attested by XGR validators.
type CheckpointSource struct {
	ChainID        uint64
	Domain         uint32
	RPCURL         string
	Mailbox        types.Address
	MerkleTreeHook types.Address
	Confirmations  uint64
}

// CheckpointRoute binds one uniquely named attestation route to one validator
// destination. Multiple routes may point at the same destination.
type CheckpointRoute struct {
	Name        string
	Destination string
	SourceType  string
	Source      *CheckpointSource
}

func (r *CheckpointRoute) IsLocal() bool {
	return r != nil && r.SourceType == CheckpointSourceLocal
}

func (r *CheckpointRoute) Validate() error {
	if r == nil {
		return fmt.Errorf("checkpoint route is nil")
	}
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("checkpoint route name is required")
	}
	if strings.TrimSpace(r.Destination) == "" {
		return fmt.Errorf("checkpoint route destination is required")
	}
	switch r.SourceType {
	case CheckpointSourceLocal:
		if r.Source != nil {
			return fmt.Errorf("local checkpoint route must not define an external source")
		}
		return nil
	case CheckpointSourceEVM:
		if r.Source == nil {
			return fmt.Errorf("EVM checkpoint route requires a source")
		}
		return r.Source.Validate()
	default:
		return fmt.Errorf("unsupported checkpoint route source type %q", r.SourceType)
	}
}

type ConfirmedCheckpoint struct {
	Root        types.Hash
	Index       uint32
	BlockNumber uint64
}

// LoadCheckpointRoutes discovers XGR_INTERCHAIN_ROUTE_<NAME>_*.
// If no explicit routes are configured, one local route per destination is
// synthesized using the destination name. This preserves the existing
// XGR->destination behavior and attestation RPC names.
func LoadCheckpointRoutes(destinations []*Destination) ([]*CheckpointRoute, error) {
	names := make(map[string]struct{})
	for _, entry := range os.Environ() {
		key := entry
		if idx := strings.IndexByte(entry, '='); idx >= 0 {
			key = entry[:idx]
		}
		if !strings.HasPrefix(key, "XGR_INTERCHAIN_ROUTE_") || !strings.HasSuffix(key, "_DESTINATION") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "XGR_INTERCHAIN_ROUTE_"), "_DESTINATION")
		if name != "" {
			names[name] = struct{}{}
		}
	}

	if len(names) == 0 {
		routes := make([]*CheckpointRoute, 0, len(destinations))
		for _, destination := range destinations {
			if destination == nil {
				continue
			}
			routes = append(routes, &CheckpointRoute{
				Name:        destination.Name,
				Destination: destination.Name,
				SourceType:  CheckpointSourceLocal,
			})
		}
		return routes, nil
	}

	keys := make([]string, 0, len(names))
	for name := range names {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	routes := make([]*CheckpointRoute, 0, len(keys))
	for _, envName := range keys {
		route, err := loadCheckpointRoute(strings.ToLower(envName))
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return routes, nil
}

func loadCheckpointRoute(name string) (*CheckpointRoute, error) {
	normalized, err := normalizeName(name)
	if err != nil {
		return nil, err
	}
	prefix := "XGR_INTERCHAIN_ROUTE_" + normalized + "_"

	destination := strings.ToLower(strings.TrimSpace(os.Getenv(prefix + "DESTINATION")))
	if destination == "" {
		return nil, fmt.Errorf("%sDESTINATION is required", prefix)
	}
	if _, err := normalizeName(destination); err != nil {
		return nil, fmt.Errorf("%sDESTINATION is invalid: %w", prefix, err)
	}

	sourceType := strings.ToLower(strings.TrimSpace(os.Getenv(prefix + "SOURCE_TYPE")))
	if sourceType == "" {
		return nil, fmt.Errorf("%sSOURCE_TYPE is required (local or evm)", prefix)
	}

	route := &CheckpointRoute{
		Name:        strings.ToLower(strings.TrimSpace(name)),
		Destination: destination,
		SourceType:  sourceType,
	}

	switch sourceType {
	case CheckpointSourceLocal:
		if hasCheckpointSourceValues(prefix + "SOURCE_") {
			return nil, fmt.Errorf("%sSOURCE_TYPE=local must not define external SOURCE_* fields", prefix)
		}
	case CheckpointSourceEVM:
		source, err := loadCheckpointSource(prefix + "SOURCE_")
		if err != nil {
			return nil, err
		}
		route.Source = source
	default:
		return nil, fmt.Errorf("%sSOURCE_TYPE must be local or evm", prefix)
	}
	if err := route.Validate(); err != nil {
		return nil, fmt.Errorf("checkpoint route %q is invalid: %w", route.Name, err)
	}
	return route, nil
}

func hasCheckpointSourceValues(prefix string) bool {
	for _, suffix := range []string{
		"CHAIN_ID", "DOMAIN", "RPC", "MAILBOX_ADDR", "MERKLE_TREE_HOOK_ADDR", "CONFIRMATIONS",
	} {
		if strings.TrimSpace(os.Getenv(prefix+suffix)) != "" {
			return true
		}
	}
	return false
}

func loadCheckpointSource(prefix string) (*CheckpointSource, error) {
	chainIDRaw := strings.TrimSpace(os.Getenv(prefix + "CHAIN_ID"))
	domainRaw := strings.TrimSpace(os.Getenv(prefix + "DOMAIN"))
	rpcRaw := strings.TrimSpace(os.Getenv(prefix + "RPC"))
	mailboxRaw := strings.TrimSpace(os.Getenv(prefix + "MAILBOX_ADDR"))
	hookRaw := strings.TrimSpace(os.Getenv(prefix + "MERKLE_TREE_HOOK_ADDR"))
	confirmationsRaw := strings.TrimSpace(os.Getenv(prefix + "CONFIRMATIONS"))

	values := []string{chainIDRaw, domainRaw, rpcRaw, mailboxRaw, hookRaw, confirmationsRaw}
	for _, value := range values {
		if value == "" {
			return nil, fmt.Errorf(
				"%s* requires CHAIN_ID, DOMAIN, RPC, MAILBOX_ADDR, MERKLE_TREE_HOOK_ADDR, and CONFIRMATIONS",
				prefix,
			)
		}
	}

	chainID, err := strconv.ParseUint(chainIDRaw, 10, 64)
	if err != nil || chainID == 0 {
		return nil, fmt.Errorf("%sCHAIN_ID must be a non-zero uint64", prefix)
	}
	domain, err := strconv.ParseUint(domainRaw, 10, 32)
	if err != nil || domain == 0 {
		return nil, fmt.Errorf("%sDOMAIN must be a non-zero uint32", prefix)
	}
	confirmations, err := strconv.ParseUint(confirmationsRaw, 10, 64)
	if err != nil || confirmations == 0 {
		return nil, fmt.Errorf("%sCONFIRMATIONS must be a non-zero uint64", prefix)
	}
	if err := types.IsValidAddress(mailboxRaw); err != nil {
		return nil, fmt.Errorf("%sMAILBOX_ADDR is invalid: %w", prefix, err)
	}
	if err := types.IsValidAddress(hookRaw); err != nil {
		return nil, fmt.Errorf("%sMERKLE_TREE_HOOK_ADDR is invalid: %w", prefix, err)
	}

	source := &CheckpointSource{
		ChainID:        chainID,
		Domain:         uint32(domain),
		RPCURL:         rpcRaw,
		Mailbox:        types.StringToAddress(mailboxRaw),
		MerkleTreeHook: types.StringToAddress(hookRaw),
		Confirmations:  confirmations,
	}
	if err := source.Validate(); err != nil {
		return nil, fmt.Errorf("%s configuration is invalid: %w", prefix, err)
	}
	return source, nil
}

func (s *CheckpointSource) Validate() error {
	if s == nil {
		return fmt.Errorf("checkpoint source is nil")
	}
	if s.ChainID == 0 {
		return fmt.Errorf("checkpoint source chain ID must be non-zero")
	}
	if s.Domain == 0 {
		return fmt.Errorf("checkpoint source Hyperlane domain must be non-zero")
	}
	if s.Confirmations == 0 {
		return fmt.Errorf("checkpoint source confirmations must be non-zero")
	}
	if s.Mailbox == types.ZeroAddress || s.MerkleTreeHook == types.ZeroAddress {
		return fmt.Errorf("checkpoint source mailbox and MerkleTreeHook must be non-zero")
	}
	parsed, err := url.Parse(strings.TrimSpace(s.RPCURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("checkpoint source RPC must be a valid http(s) URL")
	}
	return nil
}

func checkpointSourceClient(source *CheckpointSource) (*jsonrpc.Client, error) {
	if err := source.Validate(); err != nil {
		return nil, err
	}
	client, err := jsonrpc.NewClient(source.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("create checkpoint source RPC client: %w", err)
	}
	got, err := client.Eth().ChainID()
	if err != nil {
		return nil, fmt.Errorf("query checkpoint source chain id: %w", err)
	}
	if got == nil || !got.IsUint64() {
		return nil, fmt.Errorf("checkpoint source chain id does not fit uint64")
	}
	if got.Uint64() != source.ChainID {
		return nil, fmt.Errorf(
			"checkpoint source chain id mismatch: configured=%d rpc=%d",
			source.ChainID,
			got.Uint64(),
		)
	}
	return client, nil
}

func VerifyCheckpointSourceChainID(source *CheckpointSource) error {
	_, err := checkpointSourceClient(source)
	return err
}

func GetConfirmedCheckpoint(source *CheckpointSource) (*ConfirmedCheckpoint, error) {
	client, err := checkpointSourceClient(source)
	if err != nil {
		return nil, err
	}

	head, err := client.Eth().BlockNumber()
	if err != nil {
		return nil, fmt.Errorf("query checkpoint source head: %w", err)
	}
	if head < source.Confirmations {
		return nil, fmt.Errorf(
			"checkpoint source has insufficient history: head=%d confirmations=%d",
			head,
			source.Confirmations,
		)
	}
	confirmedBlock := head - source.Confirmations
	block := ethgo.BlockNumber(confirmedBlock)
	if block < 0 || uint64(block) != confirmedBlock {
		return nil, fmt.Errorf("checkpoint source block does not fit ethgo.BlockNumber")
	}

	mailboxRaw, err := callCheckpointSourceView(client, source.MerkleTreeHook, "mailbox()", block)
	if err != nil {
		return nil, fmt.Errorf("read checkpoint source MerkleTreeHook mailbox: %w", err)
	}
	if len(mailboxRaw) != 32 {
		return nil, fmt.Errorf("unexpected checkpoint source mailbox response length %d", len(mailboxRaw))
	}
	if got := types.BytesToAddress(mailboxRaw[12:]); got != source.Mailbox {
		return nil, fmt.Errorf(
			"checkpoint source MerkleTreeHook belongs to mailbox %s, expected %s",
			got,
			source.Mailbox,
		)
	}

	countRaw, err := callCheckpointSourceView(client, source.MerkleTreeHook, "count()", block)
	if err != nil {
		return nil, fmt.Errorf("read checkpoint source MerkleTreeHook count: %w", err)
	}
	if len(countRaw) != 32 {
		return nil, fmt.Errorf("unexpected checkpoint source count response length %d", len(countRaw))
	}
	count := new(big.Int).SetBytes(countRaw)
	if count.Sign() == 0 {
		return &ConfirmedCheckpoint{BlockNumber: confirmedBlock}, nil
	}
	if count.BitLen() > 32 {
		return nil, fmt.Errorf("checkpoint source MerkleTreeHook count exceeds uint32")
	}

	rootRaw, err := callCheckpointSourceView(client, source.MerkleTreeHook, "root()", block)
	if err != nil {
		return nil, fmt.Errorf("read checkpoint source MerkleTreeHook root: %w", err)
	}
	if len(rootRaw) != 32 {
		return nil, fmt.Errorf("unexpected checkpoint source root response length %d", len(rootRaw))
	}
	root := types.BytesToHash(rootRaw)
	if root == types.ZeroHash {
		return nil, fmt.Errorf("checkpoint source MerkleTreeHook returned zero root with non-zero count")
	}

	return &ConfirmedCheckpoint{
		Root:        root,
		Index:       uint32(count.Uint64() - 1),
		BlockNumber: confirmedBlock,
	}, nil
}

func callCheckpointSourceView(
	client *jsonrpc.Client,
	address types.Address,
	signature string,
	block ethgo.BlockNumber,
) ([]byte, error) {
	hash := crypto.Keccak256([]byte(signature))
	to := ethgo.Address(address)
	raw, err := client.Eth().Call(
		&ethgo.CallMsg{To: &to, Data: append([]byte(nil), hash[:4]...)},
		block,
	)
	if err != nil {
		return nil, err
	}
	return decodeRPCHex(raw)
}
