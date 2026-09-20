package evm

import (
	"fmt"
	"math/big"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/xgr-network/xgr-node/types"
)

type OriginContracts struct {
	Mailbox        types.Address
	MerkleTreeHook types.Address
}

func LoadOriginContracts() (*OriginContracts, error) {
	mailboxRaw := strings.TrimSpace(os.Getenv("XGR_INTERCHAIN_ORIGIN_MAILBOX_ADDR"))
	hookRaw := strings.TrimSpace(os.Getenv("XGR_INTERCHAIN_ORIGIN_MERKLE_TREE_HOOK_ADDR"))
	if mailboxRaw == "" && hookRaw == "" {
		return nil, nil
	}
	if mailboxRaw == "" || hookRaw == "" {
		return nil, fmt.Errorf("both XGR_INTERCHAIN_ORIGIN_MAILBOX_ADDR and XGR_INTERCHAIN_ORIGIN_MERKLE_TREE_HOOK_ADDR are required")
	}
	if err := types.IsValidAddress(mailboxRaw); err != nil {
		return nil, fmt.Errorf("XGR_INTERCHAIN_ORIGIN_MAILBOX_ADDR is invalid: %w", err)
	}
	if err := types.IsValidAddress(hookRaw); err != nil {
		return nil, fmt.Errorf("XGR_INTERCHAIN_ORIGIN_MERKLE_TREE_HOOK_ADDR is invalid: %w", err)
	}
	mailbox := types.StringToAddress(mailboxRaw)
	hook := types.StringToAddress(hookRaw)
	if mailbox == types.ZeroAddress || hook == types.ZeroAddress {
		return nil, fmt.Errorf("origin mailbox and merkle tree hook must be non-zero")
	}
	return &OriginContracts{Mailbox: mailbox, MerkleTreeHook: hook}, nil
}

type Destination struct {
	Name                     string
	ChainID                  uint64
	Domain                   uint32
	RegistryAddress          string
	RPCURL                   string
	DeactivationReserveWei   *big.Int
	Confirmations            uint64
	MembershipValiditySeconds uint64
	ExecutorStepDelaySeconds  uint64
}

func Load(name string) (*Destination, error) {
	normalized, err := normalizeName(name)
	if err != nil {
		return nil, err
	}

	prefix := "XGR_INTERCHAIN_" + normalized + "_"

	chainIDRaw := strings.TrimSpace(os.Getenv(prefix + "CHAIN_ID"))
	if chainIDRaw == "" {
		return nil, fmt.Errorf("%sCHAIN_ID is required", prefix)
	}
	chainID, err := strconv.ParseUint(chainIDRaw, 10, 64)
	if err != nil || chainID == 0 {
		return nil, fmt.Errorf("%sCHAIN_ID must be a non-zero uint64", prefix)
	}

	domainRaw := strings.TrimSpace(os.Getenv(prefix + "DOMAIN"))
	if domainRaw == "" {
		return nil, fmt.Errorf("%sDOMAIN is required", prefix)
	}
	domain, err := strconv.ParseUint(domainRaw, 10, 32)
	if err != nil || domain == 0 {
		return nil, fmt.Errorf("%sDOMAIN must be a non-zero uint32", prefix)
	}

	cfg := &Destination{
		Name:            strings.ToLower(strings.TrimSpace(name)),
		ChainID:         chainID,
		Domain:          uint32(domain),
		RegistryAddress: strings.TrimSpace(os.Getenv(prefix + "REGISTRY_ADDR")),
		RPCURL:          strings.TrimSpace(os.Getenv(prefix + "RPC")),
	}

	if cfg.RegistryAddress != "" {
		if err := types.IsValidAddress(cfg.RegistryAddress); err != nil {
			return nil, fmt.Errorf("%sREGISTRY_ADDR is not a valid EVM address: %w", prefix, err)
		}
		if types.StringToAddress(cfg.RegistryAddress) == types.ZeroAddress {
			return nil, fmt.Errorf("%sREGISTRY_ADDR must not be the zero address", prefix)
		}
	}

	if cfg.RPCURL != "" {
		parsed, err := url.Parse(cfg.RPCURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, fmt.Errorf("%sRPC must be a valid http(s) EVM JSON-RPC URL", prefix)
		}
	}

	reserveRaw := strings.TrimSpace(os.Getenv(prefix + "DEACTIVATION_RESERVE_WEI"))
	if reserveRaw != "" {
		reserve, ok := new(big.Int).SetString(reserveRaw, 10)
		if !ok || reserve.Sign() < 0 {
			return nil, fmt.Errorf("%sDEACTIVATION_RESERVE_WEI must be a non-negative integer", prefix)
		}
		cfg.DeactivationReserveWei = reserve
	}

	confirmationsRaw := strings.TrimSpace(os.Getenv(prefix + "CONFIRMATIONS"))
	if confirmationsRaw == "" {
		cfg.Confirmations = 1
	} else {
		confirmations, err := strconv.ParseUint(confirmationsRaw, 10, 64)
		if err != nil || confirmations == 0 {
			return nil, fmt.Errorf("%sCONFIRMATIONS must be a non-zero uint64", prefix)
		}
		cfg.Confirmations = confirmations
	}

	validityRaw := strings.TrimSpace(os.Getenv(prefix + "MEMBERSHIP_VALIDITY_SECONDS"))
	if validityRaw == "" {
		cfg.MembershipValiditySeconds = 300
	} else {
		validity, err := strconv.ParseUint(validityRaw, 10, 64)
		if err != nil || validity == 0 || validity > 600 {
			return nil, fmt.Errorf("%sMEMBERSHIP_VALIDITY_SECONDS must be between 1 and 600", prefix)
		}
		cfg.MembershipValiditySeconds = validity
	}

	executorDelayRaw := strings.TrimSpace(os.Getenv(prefix + "EXECUTOR_STEP_DELAY_SECONDS"))
	if executorDelayRaw == "" {
		cfg.ExecutorStepDelaySeconds = 10
	} else {
		delay, err := strconv.ParseUint(executorDelayRaw, 10, 64)
		if err != nil || delay == 0 {
			return nil, fmt.Errorf("%sEXECUTOR_STEP_DELAY_SECONDS must be a non-zero uint64", prefix)
		}
		cfg.ExecutorStepDelaySeconds = delay
	}
	if cfg.ExecutorStepDelaySeconds >= cfg.MembershipValiditySeconds {
		return nil, fmt.Errorf("%sEXECUTOR_STEP_DELAY_SECONDS must be smaller than membership validity", prefix)
	}

	return cfg, nil
}

// LoadAll discovers configured EVM destinations from XGR_INTERCHAIN_<NAME>_CHAIN_ID.
// Only destination-specific values are read here; XGR origin-chain identity and validator
// identity remain sourced from the running node.
func LoadAll() ([]*Destination, error) {
	names := make(map[string]struct{})
	for _, entry := range os.Environ() {
		key := entry
		if idx := strings.IndexByte(entry, '='); idx >= 0 {
			key = entry[:idx]
		}
		if !strings.HasPrefix(key, "XGR_INTERCHAIN_") || !strings.HasSuffix(key, "_CHAIN_ID") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "XGR_INTERCHAIN_"), "_CHAIN_ID")
		if name == "" {
			continue
		}
		names[name] = struct{}{}
	}

	keys := make([]string, 0, len(names))
	for name := range names {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	out := make([]*Destination, 0, len(keys))
	domains := make(map[uint32]string, len(keys))
	for _, name := range keys {
		cfg, err := Load(strings.ToLower(name))
		if err != nil {
			return nil, err
		}
		if previous, exists := domains[cfg.Domain]; exists {
			return nil, fmt.Errorf(
				"interchain destination domain %d is configured more than once (%s, %s)",
				cfg.Domain, previous, cfg.Name,
			)
		}
		domains[cfg.Domain] = cfg.Name
		out = append(out, cfg)
	}

	return out, nil
}

func (d *Destination) ValidateMembershipRead() error {
	if d == nil {
		return fmt.Errorf("EVM interchain destination config is nil")
	}
	if d.ChainID == 0 {
		return fmt.Errorf("destination chain ID is required for interchain membership reads")
	}
	if d.Domain == 0 {
		return fmt.Errorf("destination domain is required for interchain membership reads")
	}
	if d.RegistryAddress == "" {
		return fmt.Errorf("registry address is required for interchain membership reads")
	}
	if d.RPCURL == "" {
		return fmt.Errorf("destination EVM RPC is required for interchain membership reads")
	}

	return nil
}

func (d *Destination) ValidateSubmission() error {
	if err := d.ValidateMembershipRead(); err != nil {
		return err
	}
	if d.Confirmations == 0 {
		return fmt.Errorf("at least one destination confirmation is required")
	}
	if d.MembershipValiditySeconds == 0 || d.MembershipValiditySeconds > 600 {
		return fmt.Errorf("destination membership validity must be between 1 and 600 seconds")
	}
	if d.ExecutorStepDelaySeconds == 0 || d.ExecutorStepDelaySeconds >= d.MembershipValiditySeconds {
		return fmt.Errorf("destination executor step delay must be non-zero and below membership validity")
	}
	return nil
}

func (d *Destination) ValidateActivation() error {
	if err := d.ValidateSubmission(); err != nil {
		return err
	}
	if d.DeactivationReserveWei == nil {
		return fmt.Errorf("deactivation reserve is required for activation")
	}
	return nil
}

func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("interchain destination name is required")
	}

	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(unicode.ToUpper(r))
		case r == '-' || r == '_':
			b.WriteByte('_')
		default:
			return "", fmt.Errorf("invalid interchain destination name %q", name)
		}
	}

	return b.String(), nil
}
