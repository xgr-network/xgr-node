package blockchain

import (
	"bytes"
	"fmt"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"math/big"
)

// CalculateBaseFeeChecked refuses a local state-read failure. Local snapshot
// availability must never silently choose a different consensus fee floor.
func (b *Blockchain) CalculateBaseFeeChecked(parent *types.Header) (uint64, error) {
	min := chain.MinBaseFee
	reg := chain.EngineRegistryAddress
	if reg != (types.Address{}) {
		sa, ok := b.executor.(interface {
			StateAt(types.Hash) (state.Snapshot, error)
		})
		if !ok {
			return 0, fmt.Errorf("base fee parent state reader unavailable")
		}
		snap, err := sa.StateAt(parent.StateRoot)
		if err != nil {
			return 0, fmt.Errorf("base fee parent state: %w", err)
		}
		checked, ok := snap.(interface {
			GetAccountChecked(types.Address) (*state.Account, error)
			GetStorageChecked(types.Address, types.Hash, types.Hash) (types.Hash, error)
		})
		if !ok {
			return 0, fmt.Errorf("checked base fee snapshot reads unavailable")
		}
		acc, err := checked.GetAccountChecked(reg)
		if err != nil {
			return 0, fmt.Errorf("base fee registry account: %w", err)
		}
		if acc != nil && !bytes.Equal(acc.CodeHash, types.EmptyCodeHash.Bytes()) && !bytes.Equal(acc.CodeHash, types.ZeroHash.Bytes()) {
			raw, err := checked.GetStorageChecked(reg, acc.Root, chain.EngineRegistrySlotKeyMinBaseFee())
			if err != nil {
				return 0, fmt.Errorf("base fee registry storage: %w", err)
			}
			v := new(big.Int).SetBytes(raw.Bytes())
			if v.BitLen() <= 64 {
				min = v.Uint64()
			}
		}
	}
	return b.calculateBaseFeeWithMin(parent, min, true), nil
}
