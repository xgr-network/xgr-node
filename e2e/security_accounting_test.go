package e2e

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/e2e/framework"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Local-only four-validator PoS test. H=24 is a test height, never a mainnet schedule.
func TestSecurityAccountingFourValidators(t *testing.T) {
	const epoch = uint64(8)
	m := framework.NewIBFTServersManager(t, 4, IBFTDirPrefix, func(_ int, c *framework.TestServerConfig) {
		c.PremineValidatorBalance(framework.EthToWei(100000000))
		applyPoS3Config(c, epoch, 1, 4)
	})
	t.Cleanup(m.StopServers)
	servers := make([]*framework.TestServer, 4)
	for n := range servers {
		servers[n] = m.GetServer(n)
	}
	// Manager has already generated the single shared local genesis.
	path := filepath.Join(servers[0].Config.RootDir, "genesis.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &doc))
	params := doc["params"].(map[string]interface{})
	forks := params["forks"].(map[string]interface{})
	forks["securityAccounting"] = map[string]interface{}{"block": 24}
	data, err = json.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	m.StartServers(ctx)
	for _, s := range servers {
		key, err := s.Config.PrivateKey()
		require.NoError(t, err)
		addr := crypto.PubKeyToAddress(&key.PublicKey)
		require.NoError(t, framework.StakeAmount(addr, key, framework.EthToWei(2000000), s))
	}
	require.Empty(t, framework.WaitForServersToSealWithTimeout(servers, 34, 3*time.Minute))
	for _, height := range []uint64{23, 24, 25, 32, 34} {
		var expected ethgo.Hash
		for n, s := range servers {
			block, err := s.JSONRPC().Eth().GetBlockByNumber(ethgo.BlockNumber(height), false)
			require.NoError(t, err)
			require.NotNil(t, block)
			if n == 0 {
				expected = block.Hash
			} else {
				require.Equal(t, expected, block.Hash, "validator %d height %d", n, height)
			}
		}
	}
	assertCommonHeadHashAtLowestHeight(t, servers)
}
