package state

import (
	"fmt"
	"math/big"

	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

const StakingPoPMigrationSlot = int64(13)

func StakingPoPVerifiedSlot(validator types.Address, key []byte) types.Hash {
	var encoded [64]byte
	copy(encoded[12:32], validator[:])
	encoded[63] = 11
	outer := crypto.Keccak256(encoded[:])
	return types.BytesToHash(crypto.Keccak256(crypto.Keccak256(key), outer))
}

func StakingPoPNonceSlot(validator types.Address) types.Hash {
	var encoded [64]byte
	copy(encoded[12:32], validator[:])
	encoded[63] = 12
	return types.BytesToHash(crypto.Keccak256(encoded[:]))
}

func (t *Transition) RequireStakingPoP(validator types.Address, key []byte) error {
	if !t.config.StakingBLSPoP {
		return nil
	}
	if len(key) != 48 || t.state.GetState(staking.AddrStakingContract, StakingPoPVerifiedSlot(validator, key)) != types.BytesToHash(big.NewInt(1).Bytes()) {
		return fmt.Errorf("staking BLS possession not verified for validator %s", validator)
	}
	return nil
}
