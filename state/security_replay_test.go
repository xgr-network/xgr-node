package state_test

import (
	"fmt"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/contracts"
	"github.com/xgr-network/xgr-node/state"
	itrie "github.com/xgr-network/xgr-node/state/immutable-trie"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/types/buildroot"
	"math/big"
	"os"
	"testing"
)

// This exact fixture is also run unmodified on the frozen, unpatched baseline.
// It exercises real trie commit, receipts and ProcessBlock, not a mock execution.
func TestSecurityReplayFixture(t *testing.T) {
	for _, height := range []uint64{99, 100, 101} {
		for _, variant := range []string{"storage", "system"} {
			t.Run(fmt.Sprintf("%d/%s", height, variant), func(t *testing.T) {
				f := chain.AllForksEnabled.Copy()
				f.SetFork("securityAccounting", chain.NewFork(100))
				ex := state.NewExecutor(&chain.Params{Forks: f, ChainID: 1643, BurnContract: map[uint64]types.Address{0: types.ZeroAddress}}, itrie.NewState(itrie.NewMemoryStorage()), hclog.NewNullLogger())
				from, to := types.Address{1}, types.Address{2}
				root, err := ex.WriteGenesis(map[types.Address]*chain.GenesisAccount{
					from: {Balance: new(big.Int).SetUint64(1000000000000000000)},
					to:   {Balance: new(big.Int), Nonce: 1, Code: []byte{0x60, 0x00, 0x35, 0x60, 0x00, 0x55, 0x00}, Storage: map[types.Hash]types.Hash{types.ZeroHash: {31: 1}}},
				}, types.ZeroHash)
				require.NoError(t, err)
				ex.GetHash = func(*types.Header) state.GetHashByNumber { return func(uint64) types.Hash { return types.ZeroHash } }
				txs := []*types.Transaction{fixtureStoreTx(from, to, 0, 0), fixtureStoreTx(from, to, 1, 2)}
				if variant == "system" {
					txs = []*types.Transaction{{Type: types.StateTx, From: contracts.SystemCaller, To: &to, Gas: types.StateTransactionGasLimit, GasPrice: new(big.Int), Value: new(big.Int)}}
				}
				for _, tx := range txs {
					tx.ComputeHash(height)
				}
				tr, err := ex.ProcessBlock(root, &types.Block{Header: &types.Header{Number: height, GasLimit: 30000000, BaseFee: 100000000}, Transactions: txs}, types.Address{3})
				if err != nil {
					require.True(t, height >= 100 && variant == "system" && os.Getenv("XGR_REPLAY_BASELINE") != "1", "unexpected execution rejection: %v", err)
					t.Logf("VECTOR %d %s REJECT %v", height, variant, err)
					return
				}
				if height >= 100 && variant == "system" && os.Getenv("XGR_REPLAY_BASELINE") != "1" {
					t.Fatal("unauthorized StateTx accepted")
				}
				_, after, err := tr.Commit()
				require.NoError(t, err)
				if height < 100 {
					roots := map[string]string{"storage": "0xd5eec5119c5d616bc3c20169c51c55152553524b97f0cd6712bd8e3edaf4fec5", "system": "0x958e3a244da290f4fa53e266b5200b7fcb176c8cb5aeb4b389fc3682f6136bd0"}
					receipts := map[string]string{"storage": "0x9d1a13dbfc0a9d13e37f4634f92006f9b0476ba9be8f91a68e0554ed7503cec3", "system": "0x54cf4f650aac03438b46e6b74d4c6c4a5b1a478d2d5dd2bd3707c2d1d1c61ea6"}
					gas := map[string]uint64{"storage": 24744, "system": 13005}
					require.Equal(t, roots[variant], after.String())
					require.Equal(t, receipts[variant], buildroot.CalculateReceiptsRoot(tr.Receipts()).String())
					require.Equal(t, gas[variant], tr.TotalGas())
				}
				t.Logf("VECTOR %d %s %s %s %d", height, variant, after, buildroot.CalculateReceiptsRoot(tr.Receipts()), tr.TotalGas())
			})
		}
	}
}

func fixtureStoreTx(from, to types.Address, nonce uint64, value byte) *types.Transaction {
	data := make([]byte, 32)
	data[31] = value
	return &types.Transaction{Type: types.LegacyTx, From: from, To: &to, Nonce: nonce, Gas: 100000, GasPrice: big.NewInt(1000000000), Value: new(big.Int), Input: data}
}
