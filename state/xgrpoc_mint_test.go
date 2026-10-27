package state

// Regression test for GHSA-wc4w-xh2c-jwpv: unbacked native XGR mint via StateTx
// fee accounting.
//
// apply() dispatches StateTx to checkAndProcessStateTx(), which (unlike the
// normal-tx path's checkAndProcessTx -> subGasLimitPrice) performs NO upfront
// gas debit. Before the fix, the post-exec fee block still ran for a StateTx
// (NonPayable=false) and, because a zero-gasPrice StateTx has gasPrice fall back
// to BaseFee, credited GasLeft*BaseFee to msg.From (SystemCaller) plus the
// GasUsed*BaseFee fee split, creating msg.Gas*BaseFee of native XGR from nothing.
//
// The fix skips the fee-credit block for StateTx. This test asserts the fixed
// invariant: a StateTx changes total supply by zero and credits SystemCaller
// nothing, while the premined account stays untouched.

import (
	"math/big"
	"testing"

	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/contracts"
	"github.com/xgr-network/xgr-node/state/runtime"
	"github.com/xgr-network/xgr-node/types"
)

func TestPoC_StateTxUnbackedMint(t *testing.T) {
	// A "premined" EOA holding real supply — must be untouched (proves no debit).
	premined := types.StringToAddress("0x00000000000000000000000000000000deadbeef")
	preBal := new(big.Int).SetUint64(1_000_000_000_000_000_000) // 1 XGR

	// StateTx recipient (any account; empty code is fine — Call2 returns immediately).
	to := types.StringToAddress("0x0000000000000000000000000000000000001234")

	// Proposer-controlled BaseFee. verifyHeaderImpl never validates it, so a
	// malicious proposer may set it arbitrarily high; we use the 100 gwei floor
	// here just to keep the numbers legible.
	baseFee := new(big.Int).Mul(big.NewInt(100), big.NewInt(1_000_000_000)) // 100 gwei

	snap := newStateWithPreState(map[types.Address]*PreState{
		premined: {Balance: preBal.Uint64()},
		// SystemCaller / coinbase / feepool / burn all start empty (unfunded).
	})
	txn := newTxn(snap)
	tr := NewTransition(chain.ForksInTime{}, snap, txn)
	tr.ctx = runtime.TxContext{
		BaseFee:  baseFee,
		Coinbase: types.StringToAddress("0x0000000000000000000000000000000000000abc"),
		Number:   1,
	}
	tr.gasPool = 30_000_000

	// Every address that can receive credit in the fee block.
	recips := []types.Address{
		contracts.SystemCaller,          // refund of GasLeft*gasPrice
		tr.ctx.Coinbase,                 // validator fee
		FeePoolAddress,                  // pooled validator fee (if FeePoolSplit)
		chain.DefaultBurnedAddress,      // burn + donation (donation addr == burn addr)
		premined,                        // must NOT change
	}

	sumBalances := func() *big.Int {
		s := new(big.Int)
		for _, a := range recips {
			s.Add(s, tr.GetBalance(a))
		}
		return s
	}

	before := sumBalances()

	// The forged StateTx: type 0x7f, from = SystemCaller, gasPrice = 0 (mandatory),
	// gas = StateTransactionGasLimit. In a real attack this is injected into a block
	// body by a modified proposer; recoverFromFieldsInBlock skips sig recovery for
	// StateTx, so From=SystemCaller is accepted with no key.
	mintTx := &types.Transaction{
		Type:     types.StateTx,
		From:     contracts.SystemCaller,
		To:       &to,
		Gas:      types.StateTransactionGasLimit, // 1_000_000
		GasPrice: big.NewInt(0),
		Value:    big.NewInt(0),
	}

	if err := tr.Write(mintTx); err != nil {
		t.Fatalf("StateTx apply failed: %v", err)
	}

	after := sumBalances()
	minted := new(big.Int).Sub(after, before)

	t.Logf("BaseFee            = %s wei (100 gwei)", baseFee)
	t.Logf("total supply before= %s wei", before)
	t.Logf("total supply after = %s wei", after)
	t.Logf("supply delta       = %s wei", minted)
	t.Logf("SystemCaller now   = %s wei", tr.GetBalance(contracts.SystemCaller))
	t.Logf("premined EOA now   = %s wei (was %s)", tr.GetBalance(premined), preBal)

	// Regression for GHSA-wc4w-xh2c-jwpv. Before the fix the StateTx fee-credit
	// block minted exactly Gas*BaseFee (here 0.1 XGR at the 100 gwei floor) with
	// nothing debited. After the fix, a StateTx must credit nothing:
	//   1) total supply is unchanged (no mint),
	//   2) SystemCaller (which paid nothing) receives nothing,
	//   3) the premined account is untouched.
	if minted.Sign() != 0 {
		t.Fatalf("StateTx changed total supply by %s wei (want 0) - unbacked mint not fixed", minted)
	}
	if tr.GetBalance(contracts.SystemCaller).Sign() != 0 {
		t.Fatalf("SystemCaller credited %s wei by a StateTx (want 0)", tr.GetBalance(contracts.SystemCaller))
	}
	if tr.GetBalance(premined).Cmp(preBal) != 0 {
		t.Fatalf("premined EOA changed: %s (want %s)", tr.GetBalance(premined), preBal)
	}
}
