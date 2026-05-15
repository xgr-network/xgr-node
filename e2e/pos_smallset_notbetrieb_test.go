package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/e2e/framework"
)

func TestPoS_3Validators_ClusterStillSeals(t *testing.T) {
	// Liveness baseline (quorum): all selected validators online.
	servers, _, _, _ := setupStakingV2Cluster(t, 3, 5, 1, 3)
	require.Empty(t, framework.WaitForServersToSeal(servers, 14))
}

func TestPoS_3Validators_OneOfflineStallsAsExpected(t *testing.T) {
	// This assertion is about quorum/liveness limits, not tier-based membership selection.
	servers, _, _, manager := setupStakingV2Cluster(t, 3, 5, 1, 3)

	before, err := servers[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)

	manager.StopServer(2)
	assert.Eventually(t, func() bool {
		after, pollErr := servers[0].JSONRPC().Eth().BlockNumber()
		return pollErr == nil && after == before
	}, 5*time.Second, 500*time.Millisecond, "3-validator cluster should stall with one offline in this test profile")
}

func TestPoS_2Validators_BehaviorDocumented(t *testing.T) {
	// This assertion is about quorum/liveness limits, not tier-based membership selection.
	servers, _, _, manager := setupStakingV2Cluster(t, 2, 5, 1, 2)

	require.Empty(t, framework.WaitForServersToSeal(servers, 10))

	heightBeforeStop, err := servers[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	manager.StopServer(1)

	assert.Eventually(t, func() bool {
		heightAfterStop, pollErr := servers[0].JSONRPC().Eth().BlockNumber()
		return pollErr == nil && heightAfterStop == heightBeforeStop
	}, 5*time.Second, 500*time.Millisecond, "2-validator set should stall with one node offline")
}

func TestPoS_1Validator_SingleNodeDeterministicBehavior(t *testing.T) {
	servers, _, _, _ := setupStakingV2Cluster(t, 1, 5, 1, 1)

	require.Empty(t, framework.WaitForServersToSeal(servers, 10))

	first, err := servers[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	require.Empty(t, framework.WaitForServersToSeal(servers, first+5))
	second, err := servers[0].JSONRPC().Eth().BlockNumber()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, second, first+5)
}
