package pos

import (
	"fmt"

	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

const MaxEpochValidatorsSnapshot = uint64(4096)

const maxEpochValidatorsSnapshot = MaxEpochValidatorsSnapshot

func addrToHash(addr types.Address) types.Hash {
	var h types.Hash
	// Left-pad with 12 zero bytes, Solidity-style.
	copy(h[12:], addr[:])

	return h
}

func addrFromHash(h types.Hash) types.Address {
	var a types.Address
	copy(a[:], h[12:])

	return a
}

func ensureEpochValidatorsSnapshot(txn *state.Transition, epoch uint64, vals validators.Validators) (bool, error) {
	if txn == nil || epoch == 0 || vals == nil {
		return false, nil
	}
	if vals.Len() == 0 {
		return false, nil
	}

	lenKey := keyEpochValidatorsLen(epoch)
	if u64FromHash(txn.Txn().GetState(PosSysAddr, lenKey)) != 0 {
		return false, nil
	}

	l := vals.Len()
	if uint64(l) > maxEpochValidatorsSnapshot {
		return false, fmt.Errorf(
			"epoch validator snapshot exceeds max: have %d max %d",
			l,
			maxEpochValidatorsSnapshot,
		)
	}

	txn.Txn().SetState(PosSysAddr, lenKey, u64ToHash(uint64(l)))

	for i := 0; i < l; i++ {
		addr := vals.At(uint64(i)).Addr()
		txn.Txn().SetState(PosSysAddr, keyEpochValidator(epoch, uint64(i)), addrToHash(addr))
	}

	return true, nil
}

func loadEpochValidatorsSnapshot(txn *state.Transition, epoch uint64) []types.Address {
	if txn == nil || epoch == 0 {
		return nil
	}

	l := u64FromHash(txn.Txn().GetState(PosSysAddr, keyEpochValidatorsLen(epoch)))
	if l == 0 {
		return nil
	}
	if l > maxEpochValidatorsSnapshot {
		// Defensive guard: malformed state must not force huge loops in epoch-finalize.
		return nil
	}

	out := make([]types.Address, 0, l)
	for i := uint64(0); i < l; i++ {
		h := txn.Txn().GetState(PosSysAddr, keyEpochValidator(epoch, i))
		out = append(out, addrFromHash(h))
	}

	return out
}
