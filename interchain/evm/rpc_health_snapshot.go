package evm

import (
	"net/url"

	policy "github.com/xgr-network/xgr-node/interchain"
)

// PublicRPCObservation contains passive diagnostics for a compiled public endpoint.
type PublicRPCObservation struct {
	ChainID           uint64 `json:"chainId"`
	Host              string `json:"host"`
	Operator          string `json:"operator"`
	Role              string `json:"role"`
	State             string `json:"state"`
	ObservedUnix      int64  `json:"observedUnix"`
	CooldownUntilUnix int64  `json:"cooldownUntilUnix,omitempty"`
}

// PublicInterchainRPCObservations reports the compiled public inventory and
// overlays existing observations. It never probes or exposes endpoint URLs.
func PublicInterchainRPCObservations() []PublicRPCObservation {
	chains, err := policy.ActivePolicies()
	if err != nil {
		return nil
	}
	type endpointKey struct {
		chainID uint64
		url     string
	}
	statsByEndpoint := make(map[endpointKey]RPCProviderStats)
	for _, stats := range InterchainRPCProviderStats() {
		statsByEndpoint[endpointKey{stats.ChainID, stats.URL}] = stats
	}
	var observations []PublicRPCObservation
	add := func(chainID uint64, endpoint, operator, role string) {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" {
			return
		}
		stats := statsByEndpoint[endpointKey{chainID, endpoint}]
		state := stats.Observation
		if stats.ObservedUnix == 0 {
			state = "unknown"
		}
		observations = append(observations, PublicRPCObservation{
			ChainID: chainID, Host: parsed.Hostname(), Operator: operator, Role: role,
			State: state, ObservedUnix: stats.ObservedUnix, CooldownUntilUnix: stats.CooldownUntilUnix,
		})
	}
	for _, chain := range chains {
		for i, endpoint := range chain.RPCURLs {
			add(chain.ChainID, endpoint, chain.IndependentOperatorIDs[i], "scanner")
		}
		for _, endpoint := range chain.ReceiptWitnessRPCs {
			add(chain.ChainID, endpoint.URL, endpoint.Operator, "receipt-witness")
		}
	}
	return observations
}
