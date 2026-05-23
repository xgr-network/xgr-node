package e2e

import (
	"context"
	"crypto/ecdsa"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/consensus/ibft/fork"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/e2e/framework"
	"github.com/xgr-network/xgr-node/types"
)

func TestPoA_PoS_Cutover_FirstBoundaryDoesNotStall(t *testing.T) {
	const (
		nodeCount     = 4
		epochSize     = uint64(50)
		poSFrom       = epochSize
		poSDeployment = uint64(5)
		premineEth    = 7_000_000
		stakeEth      = 2_000_000
	)
	nextBoundary := poSFrom + epochSize

	ibftManager := framework.NewIBFTServersManager(
		t,
		nodeCount,
		"poa-pos-cutover-boundary-",
		func(_ int, cfg *framework.TestServerConfig) {
			cfg.SetBlockTime(1)
			cfg.SetIBFTBaseTimeout(2)
			cfg.SetEpochSize(epochSize)
			cfg.PremineValidatorBalance(framework.EthToWei(premineEth))
		},
	)

	require.NoError(t, ibftManager.GetServer(0).SwitchIBFTType(fork.PoS, poSFrom, nil, u64ptr(poSDeployment)))

	startCtx, startCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer startCancel()
	ibftManager.StartServers(startCtx)

	servers := ibftManager.ActiveServers()
	require.Empty(t, framework.WaitForServersToSeal(servers, poSDeployment+1), "cluster must seal pre-cutover blocks before staking")

	addrs, keys := collectValidatorCredentials(t, ibftManager, nodeCount)
	for i := 0; i < nodeCount; i++ {
		require.NoError(t, framework.StakeAmount(addrs[i], keys[i], framework.EthToWei(stakeEth), ibftManager.GetServer(0)))
	}

	require.Empty(t, framework.WaitForServersToSeal(servers, poSFrom-1), "cluster must reach the last pre-cutover block")
	require.Empty(t, framework.WaitForServersToSeal(servers, poSFrom), "cluster must seal the cutover boundary block")
	require.Empty(t, framework.WaitForServersToSeal(servers, poSFrom+3), "cluster must continue sealing after cutover boundary")
	require.Empty(t, framework.WaitForServersToSeal(servers, nextBoundary), "cluster must seal the first normal post-cutover PoS boundary")
	require.Empty(t, framework.WaitForServersToSeal(servers, nextBoundary+3), "cluster must keep sealing after the first normal post-cutover PoS boundary")
	assertNoPermanentHeightDivergence(t, servers)
}

func collectValidatorCredentials(t *testing.T, m *framework.IBFTServersManager, nodeCount int) ([]types.Address, []*ecdsa.PrivateKey) {
	t.Helper()

	addrs := make([]types.Address, 0, nodeCount)
	keys := make([]*ecdsa.PrivateKey, 0, nodeCount)

	for i := 0; i < nodeCount; i++ {
		key, err := m.GetServer(i).Config.PrivateKey()
		require.NoError(t, err)

		keys = append(keys, key)
		addrs = append(addrs, crypto.PubKeyToAddress(&key.PublicKey))
	}

	return addrs, keys
}

func u64ptr(v uint64) *uint64 { return &v }

func TestPoA_PoS_Cutover_MicroEpochAlignedDoesNotStall(t *testing.T) {
	const (
		nodeCount             = 4
		epochSize             = uint64(50)
		microEpochSize        = uint64(10)
		macroEpochMicroFactor = uint64(5)
		poSFrom               = epochSize
		poSDeployment         = uint64(5)
		premineEth            = 7_000_000
		stakeEth              = 2_000_000
	)
	ibftManager := framework.NewIBFTServersManager(
		t,
		nodeCount,
		"poa-pos-cutover-microaligned-",
		func(_ int, cfg *framework.TestServerConfig) {
			cfg.SetBlockTime(1)
			cfg.SetIBFTBaseTimeout(2)
			cfg.SetMicroEpochConfig(microEpochSize, macroEpochMicroFactor, 10_000, 9_000)
			cfg.PremineValidatorBalance(framework.EthToWei(premineEth))
		},
	)

	require.NoError(t, ibftManager.GetServer(0).SwitchIBFTType(fork.PoS, poSFrom, nil, u64ptr(poSDeployment)))

	startCtx, startCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer startCancel()
	ibftManager.StartServers(startCtx)

	servers := ibftManager.ActiveServers()
	require.Empty(t, framework.WaitForServersToSeal(servers, poSDeployment+1))

	addrs, keys := collectValidatorCredentials(t, ibftManager, nodeCount)
	for i := 0; i < nodeCount; i++ {
		require.NoError(t, framework.StakeAmount(addrs[i], keys[i], framework.EthToWei(stakeEth), ibftManager.GetServer(0)))
	}

	require.Empty(t, framework.WaitForServersToSeal(servers, 70), "aligned micro-epochs must not stall after cutover")
	assertNoPermanentHeightDivergence(t, servers)
}
