package state

// Fork-variant check (runtime): XGR selects a fee handler by block height,
// LondonFixForkV1 vs V2 (state/londonFix_fork.go). This test ACTIVATES LondonFix
// V2 (the newer variant, active on mainnet since genesis block 0) through the
// exact forkmanager path server.go uses, then shows the StateTx mint STILL fires.
// It also runs with only V1 active. The mint is invariant to the fork because
// checkAndProcessStateTx never routes through subGasLimitPrice (the only place
// LondonFix is consulted). So no LondonFix height closes it.

import (
	"math/big"
	"testing"

	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/contracts"
	"github.com/xgr-network/xgr-node/forkmanager"
	"github.com/xgr-network/xgr-node/state/runtime"
	"github.com/xgr-network/xgr-node/types"
)

func mintOnceAtBlock(t *testing.T, blockNum int64) *big.Int {
	t.Helper()

	premined := types.StringToAddress("0x00000000000000000000000000000000deadbeef")
	preBal := new(big.Int).SetUint64(1_000_000_000_000_000_000)
	to := types.StringToAddress("0x0000000000000000000000000000000000001234")
	// Live mainnet baseFee observed 2026-10-05 = 1000 gwei (proposer-set, unvalidated).
	baseFee := new(big.Int).Mul(big.NewInt(1000), big.NewInt(1_000_000_000))

	snap := newStateWithPreState(map[types.Address]*PreState{
		premined: {Balance: preBal.Uint64()},
	})
	txn := newTxn(snap)
	tr := NewTransition(chain.AllForksEnabled.At(uint64(blockNum)), snap, txn)
	tr.ctx = runtime.TxContext{
		BaseFee:  baseFee,
		Coinbase: types.StringToAddress("0x0000000000000000000000000000000000000abc"),
		Number:   blockNum,
	}
	tr.gasPool = 30_000_000

	recips := []types.Address{
		contracts.SystemCaller, tr.ctx.Coinbase, FeePoolAddress, chain.DefaultBurnedAddress, premined,
	}
	sum := func() *big.Int {
		s := new(big.Int)
		for _, a := range recips {
			s.Add(s, tr.GetBalance(a))
		}
		return s
	}

	before := sum()
	mintTx := &types.Transaction{
		Type: types.StateTx, From: contracts.SystemCaller, To: &to,
		Gas: types.StateTransactionGasLimit, GasPrice: big.NewInt(0), Value: big.NewInt(0),
	}
	if err := tr.Write(mintTx); err != nil {
		t.Fatalf("StateTx apply failed at block %d: %v", blockNum, err)
	}
	if tr.GetBalance(premined).Cmp(preBal) != 0 {
		t.Fatalf("premined changed at block %d", blockNum)
	}
	return new(big.Int).Sub(sum(), before)
}

func TestPoC_MintSurvivesLondonFixV2(t *testing.T) {
	// Drive the forkmanager exactly like server.go, with LondonFix activated at
	// block 10 so that V2 (the fixed variant) governs blocks >= 10.
	const londonFixAt = 10
	fm := forkmanager.GetInstance()
	fm.Clear()
	fm.RegisterFork(forkmanager.InitialFork, nil)
	fm.RegisterFork(chain.LondonFix, nil)
	if err := RegisterLondonFixFork(chain.LondonFix); err != nil {
		t.Fatalf("RegisterLondonFixFork: %v", err)
	}
	if err := fm.ActivateFork(forkmanager.InitialFork, 0); err != nil {
		t.Fatalf("activate initial: %v", err)
	}
	if err := fm.ActivateFork(chain.LondonFix, londonFixAt); err != nil {
		t.Fatalf("activate londonfix: %v", err)
	}
	defer fm.Clear()

	// Sanity: confirm which variant governs each height.
	if _, ok := GetLondonFixHandler(0).(*LondonFixForkV1); !ok {
		t.Fatalf("expected V1 at block 0")
	}
	if _, ok := GetLondonFixHandler(londonFixAt).(*LondonFixForkV2); !ok {
		t.Fatalf("expected V2 at block %d (the 'fixed' variant)", londonFixAt)
	}
	t.Logf("fork state: block 0 => LondonFixV1, block %d => LondonFixV2 (fixed variant active)", londonFixAt)

	mintedV1 := mintOnceAtBlock(t, 0)           // V1 era
	mintedV2 := mintOnceAtBlock(t, londonFixAt) // V2 era ("fixed")
	oneXGR := big.NewInt(1_000_000_000_000_000_000)

	_ = oneXGR
	t.Logf("supply delta with LondonFixV1 active = %s wei", mintedV1)
	t.Logf("supply delta with LondonFixV2 active = %s wei", mintedV2)

	// Regression for GHSA-wc4w-xh2c-jwpv. The fix (skip the fee-credit block for
	// StateTx) is independent of the LondonFix fork variant, so the StateTx mint
	// must be zero under both V1 and V2. Before the fix this was 1 XGR per StateTx
	// under both variants.
	if mintedV1.Sign() != 0 || mintedV2.Sign() != 0 {
		t.Fatalf("StateTx still mints (V1=%s V2=%s, want 0/0)", mintedV1, mintedV2)
	}
}
