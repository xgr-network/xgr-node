package evm

import (
	"net/url"

	policy "github.com/xgr-network/xgr-node/interchain"
)

// PublicRPCObservation contains only passive diagnostics for a compiled public endpoint.
type PublicRPCObservation struct {
	ChainID           uint64 `json:"chainId"`
	Host              string `json:"host"`
	State             string `json:"state"`
	ObservedUnix      int64  `json:"observedUnix"`
	CooldownUntilUnix int64  `json:"cooldownUntilUnix,omitempty"`
}

// PublicInterchainRPCObservations never exposes custom provider URLs or error text.
func PublicInterchainRPCObservations() []PublicRPCObservation {
	var observations []PublicRPCObservation
	for _, stats := range InterchainRPCProviderStats() {
		if stats.ObservedUnix == 0 {
			continue
		}
		chain, err := policy.PolicyForChainID(stats.ChainID)
		if err != nil {
			continue
		}
		for _, endpoint := range chain.RPCURLs {
			if endpoint != stats.URL {
				continue
			}
			parsed, err := url.Parse(endpoint)
			if err == nil && parsed.Scheme == "https" && parsed.User == nil && parsed.Hostname() != "" {
				observations = append(observations, PublicRPCObservation{
					ChainID: stats.ChainID, Host: parsed.Hostname(), State: stats.Observation,
					ObservedUnix: stats.ObservedUnix, CooldownUntilUnix: stats.CooldownUntilUnix,
				})
			}
			break
		}
	}
	return observations
}
