package ibft

import (
	"fmt"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/blockchain"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/consensus"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	"github.com/xgr-network/xgr-node/types"
	"math/big"
	"testing"
)

func securityBackend(t *testing.T, h uint64) *backendIBFT {
	f := chain.Forks{chain.SecurityAccounting: chain.NewFork(h)}
	return &backendIBFT{logger: hclog.NewNullLogger(), config: &consensus.Config{Params: &chain.Params{Forks: &f}}, epochSize: 10,
		forkManager: &votingPowerForkManager{active: func(h uint64) bool { return h >= 1 }}, blockchain: blockchain.NewTestBlockchain(t, nil)}
}

func TestSecurityBodyAuthorization(t *testing.T) {
	for _, activation := range []uint64{99, 100, 101} {
		t.Run(fmt.Sprint(activation), func(t *testing.T) {
			i := securityBackend(t, activation)
			for _, height := range []uint64{activation - 1, activation, activation + 1, 110} {
				boundary := height%10 == 0
				b := &types.Block{Header: &types.Header{Number: height}}
				if height < activation {
					require.NoError(t, i.verifySecurityTransactions(b))
					continue
				}
				if boundary {
					require.Error(t, i.verifySecurityTransactions(b))
					tx := pos.EpochFinalizationSystemTx(height)
					b.Transactions = []*types.Transaction{tx}
					require.NoError(t, i.verifySecurityTransactions(b))
					b.Transactions = append(b.Transactions, tx.Copy())
					require.Error(t, i.verifySecurityTransactions(b))
					b.Transactions = []*types.Transaction{tx.Copy()}
					b.Transactions[0].V = big.NewInt(1)
					b.Transactions[0].ComputeHash(height)
					require.Error(t, i.verifySecurityTransactions(b))
					b.Transactions = []*types.Transaction{pos.EpochFinalizationSystemTx(height - 1)}
					require.Error(t, i.verifySecurityTransactions(b))
				} else {
					require.NoError(t, i.verifySecurityTransactions(b))
					b.Transactions = []*types.Transaction{pos.EpochFinalizationSystemTx(height)}
					require.Error(t, i.verifySecurityTransactions(b))
					b.Transactions = []*types.Transaction{{Type: types.StateTx}}
					require.Error(t, i.verifySecurityTransactions(b))
				}
			}
		})
	}
}
func TestSecurityFinalizedPathRejectsBeforeHooks(t *testing.T) {
	i := securityBackend(t, 100)
	// Real sync entry: invalid body must be rejected before any uptime/state hooks.
	b := &types.Block{Header: &types.Header{Number: 101}, Transactions: []*types.Transaction{{Type: types.StateTx}}}
	require.ErrorContains(t, i.PreCommitState(b, nil), "unauthorized StateTx")
	b.Header.Number = 100
	b.Transactions = nil
	require.ErrorContains(t, i.PreCommitState(b, nil), "invalid epoch system transaction")
}
func TestSecurityBaseFeeBoundary(t *testing.T) {
	// No registry configured: deterministic static floor path.
	old := chain.EngineRegistryAddress
	chain.EngineRegistryAddress = types.ZeroAddress
	defer func() { chain.EngineRegistryAddress = old }()
	i := securityBackend(t, 100)
	parent := &types.Header{Number: 99, GasLimit: 10000000, GasUsed: 10000000, BaseFee: 100000000000}
	expected, err := i.securityBaseFee(parent, 100)
	require.NoError(t, err)
	require.Equal(t, uint64(125000000000), expected)
	for _, height := range []uint64{99, 100, 101} {
		h := &types.Header{Number: height, BaseFee: expected + 1}
		if height < 100 {
			require.NoError(t, i.verifySecurityBaseFee(parent, h))
		} else {
			require.ErrorContains(t, i.verifySecurityBaseFee(parent, h), "invalid base fee")
		}
		h.BaseFee = expected
		require.NoError(t, i.verifySecurityBaseFee(parent, h))
	}
}
