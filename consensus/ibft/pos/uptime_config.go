package pos

import "encoding/json"

const (
	keyMicroEpochSize               = "microEpochSize"
	keyMicroEpochInactivityDecayBps = "microEpochInactivityDecayBps"
	keyMicroEpochNominalWeightUnits = "microEpochNominalWeightUnits"
	keyMacroEpochMicroFactor        = "macroEpochMicroFactor"
)

// UptimeConfig holds the micro-epoch uptime/liveness configuration used by PoS.
type UptimeConfig struct {
	MicroEpochSize               uint64
	MicroEpochInactivityDecayBps uint64
	MicroEpochNominalWeight      uint64
	MacroEpochMicroFactor        uint64
	ChainID                      int64
}

// DefaultUptimeConfig returns default micro-epoch uptime/liveness parameters.
func DefaultUptimeConfig() UptimeConfig {
	return UptimeConfig{
		MicroEpochSize:               0,
		MicroEpochInactivityDecayBps: 9000,
		MicroEpochNominalWeight:      10000,
	}
}

// ParseUptimeConfig parses uptime/micro-epoch configuration from IBFT config.
func ParseUptimeConfig(ibftConfig map[string]interface{}) UptimeConfig {
	cfg := DefaultUptimeConfig()
	if ibftConfig == nil {
		return cfg
	}

	readOK := func(key string) (uint64, bool) {
		raw, ok := ibftConfig[key]
		if !ok || raw == nil {
			return 0, false
		}
		n, ok := raw.(float64)
		if !ok || n < 0 {
			return 0, false
		}
		return uint64(n), true
	}

	if v, ok := readOK(keyMicroEpochSize); ok {
		cfg.MicroEpochSize = v
	}

	if v, ok := readOK(keyMicroEpochInactivityDecayBps); ok {
		cfg.MicroEpochInactivityDecayBps = v
	}

	if v, ok := readOK(keyMicroEpochNominalWeightUnits); ok {
		cfg.MicroEpochNominalWeight = v
	}

	if v, ok := readOK(keyMacroEpochMicroFactor); ok {
		cfg.MacroEpochMicroFactor = v
	}

	return cfg
}

func (c UptimeConfig) Enabled() bool {
	return c.MicroEpochSize > 0 && c.MicroEpochNominalWeight > 0
}

func MarshalUptimeConfigJSON(cfg UptimeConfig) ([]byte, error) {
	return json.Marshal(cfg)
}
