package evm

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublicRPCObservationsKeepCooldownAndExcludeCustomEndpoints(t *testing.T) {
	destination := &Destination{ChainID: 56}
	public := "https://bsc-rpc.publicnode.com"
	private := "https://private.invalid/token"
	publicKey := fmt.Sprintf("%d/%s", destination.ChainID, public)
	privateKey := fmt.Sprintf("%d/%s", destination.ChainID, private)
	t.Cleanup(func() {
		rpcProviderHealthByURL.Delete(publicKey)
		rpcProviderHealthByURL.Delete(privateKey)
	})
	rpcRecordReady(destination, public)
	rpcRecordFailure(destination, public, fmt.Errorf("HTTP 429"), true)
	rpcRecordReady(destination, public)
	rpcRecordReady(destination, private)

	for _, item := range PublicInterchainRPCObservations() {
		require.NotEqual(t, "private.invalid", item.Host)
		if item.ChainID == 56 && item.Host == "bsc-rpc.publicnode.com" {
			require.Equal(t, "rate-limited", item.State)
			require.Positive(t, item.ObservedUnix)
			require.Greater(t, item.CooldownUntilUnix, item.ObservedUnix)
			return
		}
	}
	t.Fatal("public BSC observation missing")
}
