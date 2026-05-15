package pos

import (
	"github.com/xgr-network/xgr-node/state"
)

// ensurePosSysAccount makes the PoS system storage account non-empty so it won't be pruned
// by CleanDeleteObjects(deleteEmptyObjects=true). StateObject.Empty() ignores storage, so a
// pure "storage-only" account would be deleted every block otherwise.
//
// Consensus-critical: deterministic and idempotent.
func ensurePosSysAccount(txn *state.Transition) {
	if txn == nil {
		return
	}

	if txn.Txn().GetNonce(PosSysAddr) == 0 {
		txn.Txn().SetNonce(PosSysAddr, 1)
	}
}
