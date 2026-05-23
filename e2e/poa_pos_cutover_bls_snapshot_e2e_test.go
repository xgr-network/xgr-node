package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/consensus/ibft/fork"
	"github.com/xgr-network/xgr-node/e2e/framework"
	"github.com/xgr-network/xgr-node/validators"
)

func TestPoA_PoS_Cutover_BLS_DeploymentEqualsPosFrom(t *testing.T) {
	const (
		nodeCount             = 4
		epochSize             = uint64(10)
		microEpochSize        = uint64(10)
		macroEpochMicroFactor = uint64(1)
		posFrom               = uint64(10)
	)

	mgr := framework.NewIBFTServersManager(t, nodeCount, "poa-pos-cutover-bls-", func(_ int, cfg *framework.TestServerConfig) {
		cfg.SetBlockTime(1)
		cfg.SetIBFTBaseTimeout(2)
		cfg.SetEpochSize(epochSize)
		cfg.SetMicroEpochConfig(microEpochSize, macroEpochMicroFactor, 10_000, 9_000)
		cfg.SetValidatorType(validators.BLSValidatorType)
		cfg.PremineValidatorBalance(framework.EthToWei(2_000_000))
	})

	require.NoError(t, mgr.GetServer(0).SwitchIBFTType(fork.PoS, posFrom, nil, u64ptr(posFrom)))

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	mgr.StartServers(ctx)

	servers := mgr.ActiveServers()
	require.Empty(t, framework.WaitForServersToSeal(servers, posFrom+2))

	mgr.StopServer(0)
	restartCtx, restartCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer restartCancel()
	require.NoError(t, mgr.GetServer(0).Start(restartCtx))
	require.NoError(t, mgr.GetServer(0).WaitForReady(restartCtx))

	require.Empty(t, framework.WaitForServersToSeal(servers, posFrom+epochSize+2))

	assertServerLogsDoNotContain(t, servers,
		"missing canonical epoch validator snapshot",
		"staking contract returned empty validator set",
		"unable to build proposal",
		"unauthorized proposer",
		"failed to run sequence",
	)

	assertNoPermanentHeightDivergence(t, servers)
}
