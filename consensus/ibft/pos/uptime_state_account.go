package pos

import "github.com/xgr-network/xgr-node/types"

// TxnWrapper captures SetState/GetState used by uptime helpers.
type TxnWrapper interface {
	SetState(types.Address, types.Hash, types.Hash)
	GetState(types.Address, types.Hash) types.Hash
}

type nonceTxnWrapper interface {
	GetNonce(types.Address) uint64
	SetNonce(types.Address, uint64)
}

func ensureUptimeStateAccount(tx TxnWrapper) {
	nonceTx, ok := tx.(nonceTxnWrapper)
	if !ok {
		return
	}

	if nonceTx.GetNonce(PosSysAddr) == 0 {
		nonceTx.SetNonce(PosSysAddr, 1)
	}
}
