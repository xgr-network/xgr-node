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

func TestPublicRPCInventoryReportsReceiptWitnessesWithoutProbing(t *testing.T) {
	destination := &Destination{ChainID: 56}
	endpoint := "https://bsc.rpc.blxrbdn.com"
	t.Cleanup(func() { rpcProviderHealthByURL.Delete("56/" + endpoint) })
	rpcRecordReady(destination, endpoint)

	byHost := make(map[string]PublicRPCObservation)
	for _, item := range PublicInterchainRPCObservations() {
		if item.ChainID == 56 {
			byHost[item.Host] = item
		}
	}

	require.Equal(t, "scanner", byHost["bsc-rpc.publicnode.com"].Role)
	require.Equal(t, "receipt-witness", byHost["bsc-dataseed.bnbchain.org"].Role)
	require.Equal(t, "unknown", byHost["bsc-dataseed.bnbchain.org"].State)
	require.Zero(t, byHost["bsc-dataseed.bnbchain.org"].ObservedUnix)
	require.Equal(t, "receipt-witness", byHost["bsc.rpc.blxrbdn.com"].Role)
	require.Equal(t, "ok", byHost["bsc.rpc.blxrbdn.com"].State)
	require.Positive(t, byHost["bsc.rpc.blxrbdn.com"].ObservedUnix)
}
