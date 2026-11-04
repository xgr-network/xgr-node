package e2e

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/xgr-network/xgr-node/chain"
	protocol "github.com/xgr-network/xgr-node/consensus/ibft/blspop"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/e2e/framework"
	"github.com/xgr-network/xgr-node/secrets"
	secretsHelper "github.com/xgr-network/xgr-node/secrets/helper"
	"github.com/xgr-network/xgr-node/validators"
)

// Four sealing validators plus an observing node, entirely on loopback. The
// observer and a fresh post-fork node exercise finalized block import, including
// historical headers and ParentCommittedSeals at H and H+1.
func TestStakingPoP_FourValidatorSingleForkAndFreshSync(t *testing.T) {
	const epochSize = uint64(10)
	const height = uint64(35)
	manager := framework.NewIBFTServersManagerWithGenesisValidators(t, 5, 4, IBFTDirPrefix, func(index int, cfg *framework.TestServerConfig) {
		if index >= 4 {
			cfg.SetIBFTDirPrefix("pop-observer-")
			cfg.SetIBFTDir("pop-observer-0")
		}
		cfg.PremineValidatorBalance(framework.EthToWei(100_000_000))
		applyPoS3Config(cfg, epochSize, 4, 5)
		cfg.SetValidatorType(validators.BLSValidatorType)
		cfg.SetBlockTime(1)
	})
	t.Cleanup(manager.StopServers)
	genesisPath := filepath.Join(manager.GetServer(0).Config.RootDir, "genesis.json")
	genesis, err := chain.ImportFromFile(genesisPath)
	require.NoError(t, err)
	// Compiled Interchain policies point to production chains. This test must
	// construct no external-chain workers and contact only its loopback peers.
	genesis.Params.Engine["ibft"].(map[string]interface{})["interchainRuntimeDisabled"] = true
	entries := []protocol.Entry{}
	for idx := 0; idx < 4; idx++ {
		srv := manager.GetServer(idx)
		sm, err := secretsHelper.SetupLocalSecretsManager(srv.Config.DataDir())
		require.NoError(t, err)
		raw, err := sm.GetSecret(secrets.ValidatorBLSKey)
		require.NoError(t, err)
		key, err := crypto.BytesToBLSSecretKey(raw)
		require.NoError(t, err)
		pk, err := crypto.BLSSecretKeyToPubkeyBytes(key)
		require.NoError(t, err)
		ecdsaKey, err := srv.Config.PrivateKey()
		require.NoError(t, err)
		addr := crypto.PubKeyToAddress(&ecdsaKey.PublicKey)
		proof, err := crypto.CreateStakingPoP(key, big.NewInt(genesis.Params.ChainID), staking.AddrStakingContract, addr, new(big.Int))
		require.NoError(t, err)
		entries = append(entries, protocol.Entry{Validator: "0x" + hex.EncodeToString(addr[:]), PublicKey: "0x" + hex.EncodeToString(pk), Proof: "0x" + hex.EncodeToString(proof)})
	}
	manifest, err := protocol.NewManifest(uint64(genesis.Params.ChainID), height, entries)
	require.NoError(t, err)
	rawManifest, err := manifest.CanonicalJSON()
	require.NoError(t, err)
	verified, err := manifest.Verify(uint64(genesis.Params.ChainID), height)
	require.NoError(t, err)
	genesis.Params.Forks.SetFork(chain.StakingBLSPoP, chain.NewFork(height))
	genesis.Params.StakingBLSPoPMigration = &chain.StakingBLSPoPMigration{Manifest: string(rawManifest), Digest: verified.Digest()}
	rawGenesis, err := json.MarshalIndent(genesis, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(genesisPath, rawGenesis, 0600))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	manager.StartServers(ctx)
	servers := make([]*framework.TestServer, 0, 5)
	stake := framework.EthToWei(2_000_000)
	for idx := 0; idx < 5; idx++ {
		srv := manager.GetServer(idx)
		servers = append(servers, srv)
		if idx < 4 {
			key, err := srv.Config.PrivateKey()
			require.NoError(t, err)
			addr := crypto.PubKeyToAddress(&key.PublicKey)
			require.NoError(t, framework.StakeAmount(addr, key, stake, srv))
		}
	}
	require.Empty(t, framework.WaitForServersToSeal(servers, height+6))
	_, candidate, err := staking.PoPBytecodes()
	require.NoError(t, err)
	var expectedHash, expectedRoot ethgo.Hash
	for idx, srv := range servers {
		block, err := srv.JSONRPC().Eth().GetBlockByNumber(ethgo.BlockNumber(height), false)
		require.NoError(t, err)
		require.NotNil(t, block)
		if idx == 0 {
			expectedHash, expectedRoot = block.Hash, block.StateRoot
		} else {
			require.Equal(t, expectedHash, block.Hash)
			require.Equal(t, expectedRoot, block.StateRoot)
		}
		code, err := srv.JSONRPC().Eth().GetCode(ethgo.Address(staking.AddrStakingContract), ethgo.Latest)
		require.NoError(t, err)
		require.Equal(t, "0x"+hex.EncodeToString(candidate), code)
	}
	for idx := 0; idx < 4; idx++ {
		key, err := manager.GetServer(idx).Config.PrivateKey()
		require.NoError(t, err)
		addr := crypto.PubKeyToAddress(&key.PublicKey)
		info, err := queryStakerInfo(servers[0], addr, addr)
		require.NoError(t, err)
		require.GreaterOrEqual(t, info.Amount.Cmp(stake), 0)
		var mappingInput [64]byte
		copy(mappingInput[12:32], addr[:])
		mappingInput[63] = 3
		amountIndex := new(big.Int).Add(new(big.Int).SetBytes(crypto.Keccak256(mappingInput[:])), big.NewInt(1))
		var amountSlot ethgo.Hash
		amountIndex.FillBytes(amountSlot[:])
		before, err := servers[0].JSONRPC().Eth().GetStorageAt(ethgo.Address(staking.AddrStakingContract), amountSlot, ethgo.BlockNumber(height-1))
		require.NoError(t, err)
		after, err := servers[0].JSONRPC().Eth().GetStorageAt(ethgo.Address(staking.AddrStakingContract), amountSlot, ethgo.BlockNumber(height))
		require.NoError(t, err)
		require.Equal(t, before, after, "fork must preserve self stake without restaking")
	}
	manager.StopServer(3)
	require.NoError(t, manager.GetServer(3).Start(ctx))
	require.NoError(t, manager.GetServer(3).WaitForReady(ctx))
	require.Empty(t, framework.WaitForServersToSeal(servers, height+12))
	seed := servers[0]
	fresh := framework.NewTestServer(t, seed.Config.RootDir, func(cfg *framework.TestServerConfig) {
		cfg.SetConsensus(framework.ConsensusIBFT)
		cfg.SetIBFTDirPrefix("pop-fresh-")
		cfg.SetIBFTDir("pop-fresh-0")
		applyPoS3Config(cfg, epochSize, 4, 5)
		cfg.SetValidatorType(validators.BLSValidatorType)
		cfg.SetBlockTime(1)
	})
	_, err = fresh.SecretsInit()
	require.NoError(t, err)
	t.Cleanup(fresh.Stop)
	require.NoError(t, fresh.Start(ctx))
	require.NoError(t, fresh.WaitForReady(ctx))
	require.Empty(t, framework.WaitForServersToSeal([]*framework.TestServer{fresh}, height+12))
	freshBlock, err := fresh.JSONRPC().Eth().GetBlockByNumber(ethgo.BlockNumber(height), false)
	require.NoError(t, err)
	require.NotNil(t, freshBlock)
	require.Equal(t, expectedHash, freshBlock.Hash)
	require.Equal(t, expectedRoot, freshBlock.StateRoot)
	t.Logf("four validators, observer, restart and fresh sync agree at activation %d: hash=%s stateRoot=%s", height, expectedHash, expectedRoot)
}
