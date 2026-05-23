package ibft

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveEpochSizeAndUptimeConfig(t *testing.T) {
	t.Run("micro enabled derives epoch size 50", func(t *testing.T) {
		epochSize, _, err := resolveEpochSizeAndUptimeConfig(map[string]interface{}{
			"microEpochSize":               float64(10),
			"macroEpochMicroFactor":        float64(5),
			"microEpochNominalWeightUnits": float64(10000),
			"microEpochInactivityDecayBps": float64(9000),
		})
		require.NoError(t, err)
		require.Equal(t, uint64(50), epochSize)
	})

	t.Run("micro enabled derives epoch size 500", func(t *testing.T) {
		epochSize, _, err := resolveEpochSizeAndUptimeConfig(map[string]interface{}{
			"microEpochSize":               float64(25),
			"macroEpochMicroFactor":        float64(20),
			"microEpochNominalWeightUnits": float64(10000),
			"microEpochInactivityDecayBps": float64(9000),
		})
		require.NoError(t, err)
		require.Equal(t, uint64(500), epochSize)
	})

	t.Run("micro enabled rejects explicit epoch size", func(t *testing.T) {
		_, _, err := resolveEpochSizeAndUptimeConfig(map[string]interface{}{
			"type":                         "PoS",
			"epochSize":                    float64(50),
			"microEpochSize":               float64(10),
			"macroEpochMicroFactor":        float64(5),
			"microEpochNominalWeightUnits": float64(10000),
			"microEpochInactivityDecayBps": float64(9000),
		})
		require.EqualError(t, err, "epochSize must not be set; macro epoch size is derived from microEpochSize * macroEpochMicroFactor")
	})

	t.Run("micro enabled requires factor", func(t *testing.T) {
		_, _, err := resolveEpochSizeAndUptimeConfig(map[string]interface{}{
			"type":                         "PoS",
			"microEpochSize":               float64(10),
			"macroEpochMicroFactor":        float64(0),
			"microEpochNominalWeightUnits": float64(10000),
			"microEpochInactivityDecayBps": float64(9000),
		})
		require.EqualError(t, err, "macroEpochMicroFactor is required for PoS")
	})

	t.Run("pos requires micro epoch size", func(t *testing.T) {
		_, _, err := resolveEpochSizeAndUptimeConfig(map[string]interface{}{
			"type":                  "PoS",
			"macroEpochMicroFactor": float64(2),
		})
		require.EqualError(t, err, "microEpochSize is required for PoS")
	})

	t.Run("legacy mode valid", func(t *testing.T) {
		epochSize, _, err := resolveEpochSizeAndUptimeConfig(map[string]interface{}{"epochSize": float64(100000)})
		require.NoError(t, err)
		require.Equal(t, uint64(100000), epochSize)
	})

	t.Run("micro enabled overflow", func(t *testing.T) {
		_, _, err := resolveEpochSizeAndUptimeConfig(map[string]interface{}{
			"microEpochSize":               float64(math.MaxUint64),
			"macroEpochMicroFactor":        float64(2),
			"microEpochNominalWeightUnits": float64(10000),
			"microEpochInactivityDecayBps": float64(9000),
		})
		require.EqualError(t, err, "invalid PoS uptime config: microEpochSize * macroEpochMicroFactor overflows uint64")
	})
}
