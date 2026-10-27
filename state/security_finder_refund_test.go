package state

// Regression test for GHSA-2c3c-889q-r8xx: same-block SSTORE refund underflow.
//
// GetCommittedState (txn.go) reads obj.Account.Root, which is the PARENT-block
// root — it is only refreshed at Commit(), and the executor Commits once per
// BLOCK, not per tx (Write() calls CleanDeleteObjects, which resets the per-tx
// refund counter to 0 but does NOT commit the trie). EIP-2200 requires
// `original` = the slot value at the start of THIS transaction. Because Root is
// stale, `original` for every tx in a block is the parent-block value.
//
// Attack (two txs in ONE block, same shared Txn):
//   parent block: slot S = 1 (non-zero)
//   tx1: S -> 0   (legit delete; refund reset to 0 at end of tx1)
//   tx2: S -> 2   in-memory current = 0 (tx1), but original misread as 1 (parent)
//        => branch "original!=0 && current==0" => SubRefund(15000)
//        => refund counter (0) - 15000 underflows to 2^64-15000
//        => UpdateGasUsed clamps the giant refund to GasUsed/2
//        => tx2 billed ~50%, BELOW the 21000 intrinsic floor (impossible in a
//           correct EVM, where tx2 would be StorageAdded 0->2 with NO refund).

import (
	"math/big"
	"testing"

	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/state/runtime"
	"github.com/xgr-network/xgr-node/types"
)

// committedSnapshot serves a fixed "parent block" world state: account metadata,
// committed storage, and contract code. In-memory changes made during the block
// never reach it, so GetStorage always returns the parent-block value — exactly
// the stale-root read that GetCommittedState performs.
type committedSnapshot struct {
	accounts map[types.Address]*Account
	storage  map[types.Address]map[types.Hash]types.Hash
	code     map[types.Hash][]byte
}

func (s *committedSnapshot) GetAccount(addr types.Address) (*Account, error) {
	a, ok := s.accounts[addr]
	if !ok {
		return nil, nil
	}
	return a.Copy(), nil
}

func (s *committedSnapshot) GetStorage(addr types.Address, root types.Hash, key types.Hash) types.Hash {
	if m, ok := s.storage[addr]; ok {
		return m[key]
	}
	return types.Hash{}
}

func (s *committedSnapshot) GetCode(hash types.Hash) ([]byte, bool) {
	c, ok := s.code[hash]
	return c, ok
}

func (s *committedSnapshot) Commit(objs []*Object) (Snapshot, []byte, error) {
	return s, nil, nil
}

// Regression originally contributed by ngjupeng, cdfeff66013c8fde03febc574f9c38ee08effea8.
func TestSecuritySameBlockRefund(t *testing.T) {
	eoa := types.StringToAddress("0x00000000000000000000000000000000000011111")
	contract := types.StringToAddress("0x000000000000000000000000000000000000c0de")

	// SSTORE(slot0, calldataload(0)); STOP
	code := []byte{0x60, 0x00, 0x35, 0x60, 0x00, 0x55, 0x00}
	codeHash := crypto.Keccak256(code)

	slot0 := types.Hash{} // key 0x0000...0000
	one := types.BytesToHash(big.NewInt(1).Bytes())

	snap := &committedSnapshot{
		accounts: map[types.Address]*Account{
			eoa: {
				Balance:  new(big.Int).SetUint64(1_000_000_000_000_000_000),
				Nonce:    0,
				Root:     emptyStateHash,
				CodeHash: types.EmptyCodeHash.Bytes(),
			},
			contract: {
				Balance: big.NewInt(0),
				Nonce:   1,
				// Non-empty root so GetCommittedState routes through GetStorage.
				Root:     types.StringToHash("0x1111111111111111111111111111111111111111111111111111111111111111"),
				CodeHash: codeHash,
			},
		},
		storage: map[types.Address]map[types.Hash]types.Hash{
			// PARENT BLOCK: slot S = 1 (non-zero).
			contract: {slot0: one},
		},
		code: map[types.Hash][]byte{
			types.BytesToHash(codeHash): code,
		},
	}

	tr := NewTransition(securityTestForks(), snap, NewTxn(snap))
	tr.ctx = runtime.TxContext{
		BaseFee:  big.NewInt(0),
		Coinbase: types.StringToAddress("0x0000000000000000000000000000000000000abc"),
		Number:   100,
		GasLimit: 30_000_000,
	}
	tr.gasPool = 30_000_000

	mkTx := func(nonce uint64, lastByte byte) *types.Transaction {
		input := make([]byte, 32)
		input[31] = lastByte
		return &types.Transaction{
			Type:     types.LegacyTx,
			From:     eoa, // set directly => Write skips signature recovery
			To:       &contract,
			Nonce:    nonce,
			Gas:      100_000,
			GasPrice: big.NewInt(1_000_000_000), // 1 gwei
			Value:    big.NewInt(0),
			Input:    input,
		}
	}

	// tx1: slot 1 -> 0 (legitimate delete)
	if err := tr.Write(mkTx(0, 0x00)); err != nil {
		t.Fatalf("tx1 failed: %v", err)
	}
	// tx2: slot 0 -> 2 (same block, same shared Txn)
	if err := tr.Write(mkTx(1, 0x02)); err != nil {
		t.Fatalf("tx2 failed: %v", err)
	}

	rc := tr.Receipts()
	if len(rc) != 2 {
		t.Fatalf("expected 2 receipts, got %d", len(rc))
	}
	if *rc[1].Status != types.ReceiptSuccess {
		t.Fatalf("tx2 did not succeed (status %d); cannot claim underbilling", *rc[1].Status)
	}
	exploitGas := rc[1].GasUsed // tx2 (0->2) billed inside the malicious block

	// CONTROL: the identical 0 -> 2 store, run as the first (and only) tx of a
	// fresh block where the parent slot is genuinely 0. Here original==current==0,
	// so it is StorageAdded with NO refund => billed at full price. This is what a
	// correct EVM must charge tx2 above.
	ctrlSnap := &committedSnapshot{
		accounts: map[types.Address]*Account{
			eoa: {
				Balance:  new(big.Int).SetUint64(1_000_000_000_000_000_000),
				Nonce:    0,
				Root:     emptyStateHash,
				CodeHash: types.EmptyCodeHash.Bytes(),
			},
			contract: {
				Balance:  big.NewInt(0),
				Nonce:    1,
				Root:     types.StringToHash("0x1111111111111111111111111111111111111111111111111111111111111111"),
				CodeHash: codeHash,
			},
		},
		storage: map[types.Address]map[types.Hash]types.Hash{
			contract: {slot0: types.Hash{}}, // parent slot genuinely 0
		},
		code: map[types.Hash][]byte{types.BytesToHash(codeHash): code},
	}
	ctrl := NewTransition(securityTestForks(), ctrlSnap, NewTxn(ctrlSnap))
	ctrl.ctx = runtime.TxContext{
		BaseFee:  big.NewInt(0),
		Coinbase: types.StringToAddress("0x0000000000000000000000000000000000000abc"),
		Number:   100,
		GasLimit: 30_000_000,
	}
	ctrl.gasPool = 30_000_000
	if err := ctrl.Write(mkTx(0, 0x02)); err != nil {
		t.Fatalf("control tx failed: %v", err)
	}
	correctGas := ctrl.Receipts()[0].GasUsed

	t.Logf("tx1 (S:1->0) billed gas              = %d (legit delete refund)", rc[0].GasUsed)
	t.Logf("tx2 (S:0->2) billed in same block    = %d", exploitGas)
	t.Logf("same store billed in a CLEAN block   = %d (correct, no refund)", correctGas)
	t.Logf("phantom refund granted to tx2        = %d gas", int64(correctGas)-int64(exploitGas))

	// Regression for GHSA-2c3c-889q-r8xx. A 0->non-zero store earns ZERO EIP-2200
	// refund. Before the fix, tx2's "original" was misread from the parent-block
	// root (1 instead of the post-tx1 value 0), triggering SubRefund(15000) on a
	// zeroed counter -> uint64 underflow -> refund clamped to GasUsed/2, so tx2 was
	// billed ~11,675 (73% below the 43,249 a correct EVM charges). After the fix,
	// GetCommittedState returns the start-of-tx value, so tx2 is billed exactly the
	// control amount with no phantom refund.
	if exploitGas != correctGas {
		t.Fatalf("tx2 billed %d, expected %d (same as a clean-block store) - underflow not fixed", exploitGas, correctGas)
	}
	t.Logf("PROVEN FIXED: same-block 0->non-zero store billed %d == clean-block %d (no phantom refund)", exploitGas, correctGas)
}

func securityTestForks() chain.ForksInTime {
	c := chain.AllForksEnabled.At(0)
	c.SecurityAccounting = true
	return c
}
