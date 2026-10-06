package evm

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/xgr-network/xgr-node/types"
)

// CheckpointRoute is an explicit v3.1.2 ILN hop between two configured
// networks. Canonical contract addresses and fee state are never stored here;
// they are read from the source network's quorum-governed ILN registry.
type CheckpointRoute struct {
	Name          string
	SourceNetwork string
	Destination   string
}

func (r *CheckpointRoute) Validate() error {
	if r == nil {
		return fmt.Errorf("checkpoint route is nil")
	}
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("checkpoint route name is required")
	}
	if strings.TrimSpace(r.SourceNetwork) == "" {
		return fmt.Errorf("checkpoint route source network is required")
	}
	if _, err := normalizeName(r.SourceNetwork); err != nil {
		return fmt.Errorf("checkpoint route source network is invalid: %w", err)
	}
	if strings.TrimSpace(r.Destination) == "" {
		return fmt.Errorf("checkpoint route destination is required")
	}
	if _, err := normalizeName(r.Destination); err != nil {
		return fmt.Errorf("checkpoint route destination is invalid: %w", err)
	}
	return nil
}

type ConfirmedCheckpoint struct {
	Root        types.Hash
	Index       uint32
	BlockNumber uint64
}

// LoadCheckpointRoutes discovers explicit v3.1.2 ILN routes from
// XGR_INTERCHAIN_ROUTE_<NAME>_{SOURCE_NETWORK,DESTINATION}.
//
// There is intentionally no implicit fallback. Concrete Gateway, Mailbox,
// MerkleTreeHook, destination router and validator fee are canonical on-chain
// registry state and must not be duplicated in route ENV configuration.
func LoadCheckpointRoutes(networks []*Destination) ([]*CheckpointRoute, error) {
	byName := make(map[string]*Destination, len(networks))
	for _, network := range networks {
		if network == nil {
			continue
		}
		byName[network.Name] = network
	}

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
		return nil, nil
	}

	keys := make([]string, 0, len(names))
	for name := range names {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	out := make([]*CheckpointRoute, 0, len(keys))
	for _, envName := range keys {
		normalized, err := normalizeName(strings.ToLower(envName))
		if err != nil {
			return nil, err
		}
		prefix := "XGR_INTERCHAIN_ROUTE_" + normalized + "_"
		sourceNetwork := strings.ToLower(strings.TrimSpace(os.Getenv(prefix + "SOURCE_NETWORK")))
		destination := strings.ToLower(strings.TrimSpace(os.Getenv(prefix + "DESTINATION")))

		if sourceNetwork == "" {
			return nil, fmt.Errorf("%sSOURCE_NETWORK is required in v3.1.2", prefix)
		}
		if destination == "" {
			return nil, fmt.Errorf("%sDESTINATION is required", prefix)
		}
		source := byName[sourceNetwork]
		if source == nil {
			return nil, fmt.Errorf("%sSOURCE_NETWORK %q is not a configured network", prefix, sourceNetwork)
		}
		if byName[destination] == nil {
			return nil, fmt.Errorf("%sDESTINATION %q is not a configured network", prefix, destination)
		}
		if strings.TrimSpace(source.ILNRegistryAddress) == "" {
			return nil, fmt.Errorf(
				"%sSOURCE_NETWORK %q has no ILN_REGISTRY_ADDR; v3.1.2 does not fall back to legacy checkpoint signing",
				prefix, sourceNetwork,
			)
		}

		for _, suffix := range []string{
			"SOURCE_TYPE", "SOURCE_CHAIN_ID", "SOURCE_DOMAIN", "SOURCE_RPC",
			"SOURCE_MAILBOX_ADDR", "SOURCE_MERKLE_TREE_HOOK_ADDR", "SOURCE_CONFIRMATIONS",
		} {
			if strings.TrimSpace(os.Getenv(prefix+suffix)) != "" {
				return nil, fmt.Errorf(
					"%s%s is legacy v3.1.1 configuration and is not accepted in v3.1.2",
					prefix, suffix,
				)
			}
		}

		route := &CheckpointRoute{
			Name:          strings.ToLower(strings.TrimSpace(envName)),
			SourceNetwork: sourceNetwork,
			Destination:   destination,
		}
		if err := route.Validate(); err != nil {
			return nil, fmt.Errorf("checkpoint route %q is invalid: %w", route.Name, err)
		}
		out = append(out, route)
	}

	return out, nil
}
