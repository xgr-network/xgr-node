package ibft

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
)

func TestValidateUptimeConfig(t *testing.T) {
	t.Run("disabled micro epoch is valid", func(t *testing.T) {
		require.NoError(t, validateUptimeConfig(pos.UptimeConfig{MicroEpochSize: 0}))
	})

	t.Run("enabled micro epoch requires nominal weight", func(t *testing.T) {
		err := validateUptimeConfig(pos.UptimeConfig{MicroEpochSize: 8, MicroEpochInactivityDecayBps: 9000, MicroEpochNominalWeight: 0})
		require.Error(t, err)
	})

	t.Run("enabled micro epoch requires decay bps", func(t *testing.T) {
		err := validateUptimeConfig(pos.UptimeConfig{MicroEpochSize: 8, MicroEpochInactivityDecayBps: 0, MicroEpochNominalWeight: 10000})
		require.Error(t, err)
	})

	t.Run("enabled micro epoch rejects decay over 10000", func(t *testing.T) {
		err := validateUptimeConfig(pos.UptimeConfig{MicroEpochSize: 8, MicroEpochInactivityDecayBps: 10001, MicroEpochNominalWeight: 10000})
		require.Error(t, err)
	})
}

func TestValidateEpochSize(t *testing.T) {
	require.EqualError(t, validateEpochSize(0), "epochSize must be greater than or equal to 2")
	require.EqualError(t, validateEpochSize(1), "epochSize must be greater than or equal to 2")
	require.NoError(t, validateEpochSize(2))
}
