package state

import (
	"fmt"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/contracts"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/state/runtime"
	"github.com/xgr-network/xgr-node/types"
	"math/big"
	"testing"
)

func TestSecurityOriginalStateAndReverts(t *testing.T) {
	a, k := types.Address{1}, types.Hash{2}
	tx := newTestTxn(map[types.Address]*PreState{a: {Nonce: 1}})
	tx.SetState(a, k, types.Hash{1})
	tx.beginTransaction(true)
	require.Equal(t, types.Hash{1}, tx.GetCommittedState(a, k))
	snapshot := tx.Snapshot()
	tx.SetState(a, k, types.Hash{2})
	require.Equal(t, types.Hash{1}, tx.GetCommittedState(a, k))
	require.NoError(t, tx.RevertToSnapshot(snapshot))
	require.Equal(t, types.Hash{1}, tx.GetState(a, k))
	tx.SetState(a, k, types.ZeroHash)
	require.NoError(t, tx.CleanDeleteObjects(true))
	tx.beginTransaction(true)
	require.Equal(t, types.ZeroHash, tx.GetCommittedState(a, k))
	tx.SetState(a, k, types.Hash{3})
	require.Equal(t, types.ZeroHash, tx.GetCommittedState(a, k))
	tx.Suicide(a)
	require.NoError(t, tx.CleanDeleteObjects(true))
	tx.beginTransaction(true)
	tx.CreateAccount(a)
	tx.SetState(a, k, types.Hash{4})
	require.Equal(t, types.ZeroHash, tx.GetCommittedState(a, k))
}

func TestSecurityRefundArithmetic(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprint(secure), func(t *testing.T) {
			tx := newTestTxn(nil)
			tx.beginTransaction(secure)
			snap := tx.Snapshot()
			tx.SubRefund(1)
			if secure {
				require.ErrorIs(t, tx.refundError(), ErrRefundAccounting)
				require.Zero(t, tx.GetRefund())
			} else {
				require.Equal(t, ^uint64(0), tx.GetRefund())
			}
			require.NoError(t, tx.RevertToSnapshot(snap))
			require.NoError(t, tx.refundError())
			tx.AddRefund(^uint64(0))
			tx.AddRefund(1)
			if secure {
				require.ErrorIs(t, tx.refundError(), ErrRefundAccounting)
				require.Equal(t, ^uint64(0), tx.GetRefund())
			} else {
				require.Zero(t, tx.GetRefund())
			}
		})
	}
}

// F1 balance invariant based on ngjupeng's original transition-level regression.
func TestSecurityStateTxForkBoundary(t *testing.T) {
	forks := chain.Forks{chain.SecurityAccounting: chain.NewFork(100)}
	for _, height := range []uint64{99, 100, 101} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			snap := newStateWithPreState(nil)
			tr := NewTransition(forks.At(height), snap, newTxn(snap))
			tr.logger = hclog.NewNullLogger()
			tr.ctx = runtime.TxContext{BaseFee: big.NewInt(100000000000), Number: int64(height), Coinbase: types.Address{1}}
			tr.gasPool = 30000000
			to := types.Address{2}
			tx := &types.Transaction{Type: types.StateTx, From: contracts.SystemCaller, To: &to, Gas: types.StateTransactionGasLimit, GasPrice: new(big.Int), Value: new(big.Int)}
			err := tr.Write(tx)
			total := new(big.Int)
			for _, a := range []types.Address{contracts.SystemCaller, tr.ctx.Coinbase, FeePoolAddress, chain.DefaultBurnedAddress} {
				total.Add(total, tr.GetBalance(a))
			}
			if height < 100 {
				require.NoError(t, err)
				require.Equal(t, new(big.Int).Mul(new(big.Int).SetUint64(tx.Gas), tr.ctx.BaseFee), total)
			} else {
				require.ErrorContains(t, err, "unauthorized")
				require.Zero(t, total.Sign())
			}
		})
	}
}

func storageTransition(original types.Hash, height uint64, config chain.ForksInTime, revert bool) (*Transition, types.Address, types.Address) {
	eoa, contract := types.Address{1}, types.Address{2}
	code := []byte{0x60, 0x00, 0x35, 0x60, 0x00, 0x55, 0x00}
	if revert {
		code = []byte{0x60, 0x00, 0x35, 0x60, 0x00, 0x55, 0x60, 0x00, 0x60, 0x00, 0xfd}
	}
	hash := crypto.Keccak256(code)
	snap := &committedSnapshot{accounts: map[types.Address]*Account{
		eoa:      {Balance: new(big.Int).SetUint64(1000000000000000000), Root: emptyStateHash, CodeHash: types.EmptyCodeHash.Bytes()},
		contract: {Balance: new(big.Int), Nonce: 1, Root: types.Hash{1}, CodeHash: hash},
	}, storage: map[types.Address]map[types.Hash]types.Hash{contract: {types.ZeroHash: original}}, code: map[types.Hash][]byte{types.BytesToHash(hash): code}}
	tr := NewTransition(config, snap, NewTxn(snap))
	tr.logger = hclog.NewNullLogger()
	tr.ctx = runtime.TxContext{BaseFee: new(big.Int), Number: int64(height), Coinbase: types.Address{3}, GasLimit: 30000000}
	tr.gasPool = 30000000
	return tr, eoa, contract
}
func storeTx(from, to types.Address, nonce uint64, value byte) *types.Transaction {
	data := make([]byte, 32)
	data[31] = value
	return &types.Transaction{Type: types.LegacyTx, From: from, To: &to, Nonce: nonce, Gas: 100000, GasPrice: big.NewInt(1000000000), Value: new(big.Int), Input: data}
}

func TestSecurityRefundForkBoundaryAndEVMRevert(t *testing.T) {
	for _, profile := range []string{"constantinople", "istanbul", "berlin"} {
		for _, height := range []uint64{99, 100, 101} {
			for _, revert := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/revert=%v", profile, height, revert), func(t *testing.T) {
					c := chain.ForksInTime{Homestead: true, Byzantium: true, Constantinople: true, SecurityAccounting: height >= 100}
					if profile != "constantinople" {
						c.Istanbul = true
					}
					if profile == "berlin" {
						c.EIP2929 = true
					}
					tr, from, to := storageTransition(types.Hash{31: 1}, height, c, revert)
					require.NoError(t, tr.Write(storeTx(from, to, 0, 0)))
					require.NoError(t, tr.Write(storeTx(from, to, 1, 2)))
					control, cf, ct := storageTransition(types.ZeroHash, height, c, revert)
					require.NoError(t, control.Write(storeTx(cf, ct, 0, 2)))
					if revert {
						require.Equal(t, types.ReceiptFailed, *tr.Receipts()[0].Status)
						require.Equal(t, types.Hash{31: 1}, tr.state.GetState(to, types.ZeroHash))
					} else if height >= 100 {
						require.Equal(t, control.Receipts()[0].GasUsed, tr.Receipts()[1].GasUsed)
					} else {
						require.Less(t, tr.Receipts()[1].GasUsed, control.Receipts()[0].GasUsed)
					}
				})
			}
		}
	}
}

func BenchmarkSecurityOriginalState(b *testing.B) {
	for _, n := range []int{1, 100, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			tx := newTestTxn(nil)
			for i := 0; i < n; i++ {
				tx.SetState(types.BytesToAddress(new(big.Int).SetInt64(int64(i+1)).Bytes()), types.ZeroHash, types.Hash{1})
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tx.beginTransaction(true)
				_ = tx.GetCommittedState(types.Address{19: 1}, types.ZeroHash)
			}
		})
	}
}

// Differential reference: execute every three-write transaction against a fresh
// parent snapshot with the previous transaction's final slot value. A shared
// block must charge exactly the same gas, including resets to the original.
func FuzzSecurityStorageTransactionIsolation(f *testing.F) {
	f.Add([]byte{1, 0, 2, 0, 0, 1, 1, 2, 0, 0})
	f.Add([]byte{0, 1, 0, 0, 2, 1, 2, 0, 2, 1, 1, 0, 1})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 4 {
			return
		}
		if len(data) > 61 {
			data = data[:61]
		}
		c := securityTestForks()
		current := data[0] % 3
		shared, from, to := storageTransition(types.Hash{31: current}, 100, c, false)
		code := []byte{0x60, 0x00, 0x35, 0x60, 0x00, 0x55, 0x60, 0x20, 0x35, 0x60, 0x00, 0x55, 0x60, 0x40, 0x35, 0x60, 0x00, 0x55, 0x00}
		shared.state.SetCode(to, code)
		for n, i := 0, 1; i+2 < len(data); n, i = n+1, i+3 {
			control, cf, ct := storageTransition(types.Hash{31: current}, 100, c, false)
			control.state.SetCode(ct, code)
			input := make([]byte, 96)
			for j := 0; j < 3; j++ {
				input[j*32+31] = data[i+j] % 3
			}
			tx := storeTx(from, to, uint64(n), 0)
			tx.Input = input
			ctx := storeTx(cf, ct, 0, 0)
			ctx.Input = input
			require.NoError(t, shared.Write(tx))
			require.NoError(t, control.Write(ctx))
			require.Equal(t, control.Receipts()[0].GasUsed, shared.Receipts()[n].GasUsed)
			require.Equal(t, *control.Receipts()[0].Status, *shared.Receipts()[n].Status)
			current = input[95]
			require.Equal(t, types.Hash{31: current}, shared.state.GetState(to, types.ZeroHash))
		}
	})
}
