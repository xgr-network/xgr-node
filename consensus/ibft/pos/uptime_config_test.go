package pos

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseUptimeConfig_NewKeys(t *testing.T) {
	cfg := ParseUptimeConfig(map[string]interface{}{
		keyMicroEpochSize:               float64(8),
		keyMicroEpochInactivityDecayBps: float64(9000),
		keyMicroEpochNominalWeightUnits: float64(10000),
	})

	require.Equal(t, uint64(8), cfg.MicroEpochSize)
	require.Equal(t, uint64(9000), cfg.MicroEpochInactivityDecayBps)
	require.Equal(t, uint64(10000), cfg.MicroEpochNominalWeight)
}

func TestParseUptimeConfig_LegacyBeaconKeysIgnored(t *testing.T) {
	cfg := ParseUptimeConfig(map[string]interface{}{
		"beaconSlotsPerEpoch":      float64(8),
		"beaconInactivityDecayBps": float64(9000),
		"beaconNominalWeightUnits": float64(10000),
	})

	require.Equal(t, uint64(0), cfg.MicroEpochSize)
	require.Equal(t, uint64(9000), cfg.MicroEpochInactivityDecayBps)
	require.Equal(t, uint64(10000), cfg.MicroEpochNominalWeight)
}

func TestDefaultUptimeConfig(t *testing.T) {
	cfg := DefaultUptimeConfig()

	require.Equal(t, uint64(0), cfg.MicroEpochSize)
	require.Equal(t, uint64(9000), cfg.MicroEpochInactivityDecayBps)
	require.Equal(t, uint64(10000), cfg.MicroEpochNominalWeight)
}
