package pos

import (
	"fmt"
	"math/big"

	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func HasMacroEpochValidatorSet(txn *state.Transition, epoch uint64) bool {
	if txn == nil || epoch == 0 {
		return false
	}

	return u64FromHash(txn.Txn().GetState(PosSysAddr, keyEpochValidatorsLen(epoch))) > 0
}

func StoreMacroEpochValidatorSet(txn *state.Transition, epoch uint64, vals validators.Validators) error {
	if txn == nil {
		return fmt.Errorf("transition is required")
	}
	if epoch == 0 {
		return fmt.Errorf("epoch must be > 0")
	}
	if vals == nil || vals.Len() == 0 {
		return fmt.Errorf("macro epoch validator set is empty for epoch %d", epoch)
	}
	if uint64(vals.Len()) > maxEpochValidatorsSnapshot {
		return fmt.Errorf("epoch validator snapshot exceeds max: have %d max %d", vals.Len(), maxEpochValidatorsSnapshot)
	}
	ensurePosSysAccount(txn)

	type blsPubWrite struct {
		idx uint64
		pub []byte
	}

	blsWrites := make([]blsPubWrite, 0, vals.Len())
	for i := 0; i < vals.Len(); i++ {
		v := vals.At(uint64(i))
		if vals.Type() == validators.BLSValidatorType {
			blsVal, ok := v.(*validators.BLSValidator)
			if !ok || len(blsVal.BLSPublicKey) == 0 {
				return fmt.Errorf("missing bls public key for validator %s at epoch %d", v.Addr(), epoch)
			}
			if _, err := crypto.UnmarshalBLSPublicKey(blsVal.BLSPublicKey); err != nil {
				return fmt.Errorf("invalid bls public key for validator %s at epoch %d: %w", v.Addr(), epoch, err)
			}
			pub := append([]byte(nil), blsVal.BLSPublicKey...)
			blsWrites = append(blsWrites, blsPubWrite{idx: uint64(i), pub: pub})
		}
	}

	txn.Txn().SetState(PosSysAddr, keyEpochValidatorsLen(epoch), u64ToHash(uint64(vals.Len())))
	for i := 0; i < vals.Len(); i++ {
		v := vals.At(uint64(i))
		txn.Txn().SetState(PosSysAddr, keyEpochValidator(epoch, uint64(i)), addrToHash(v.Addr()))
	}
	for _, w := range blsWrites {
		txn.Txn().SetState(PosSysAddr, keyEpochValidatorBLSPub(epoch, w.idx, 0), u64ToHash(uint64(len(w.pub))))
		first := make([]byte, 32)
		copy(first, w.pub[:min(32, len(w.pub))])
		txn.Txn().SetState(PosSysAddr, keyEpochValidatorBLSPub(epoch, w.idx, 1), types.BytesToHash(first))
		second := make([]byte, 32)
		if len(w.pub) > 32 {
			copy(second, w.pub[32:])
		}
		txn.Txn().SetState(PosSysAddr, keyEpochValidatorBLSPub(epoch, w.idx, 2), types.BytesToHash(second))
	}

	return nil
}

func LoadMacroEpochValidatorSet(txn *state.Transition, epoch uint64, validatorType validators.ValidatorType) (validators.Validators, bool, error) {
	if txn == nil || epoch == 0 {
		return nil, false, nil
	}

	addrs := loadEpochValidatorsSnapshot(txn, epoch)
	if len(addrs) == 0 {
		return nil, false, nil
	}

	set := validators.NewValidatorSetFromType(validatorType)
	for idx, addr := range addrs {
		if addr == types.ZeroAddress {
			return nil, false, fmt.Errorf("macro epoch validator set contains zero address at epoch %d", epoch)
		}
		var v validators.Validator
		switch validatorType {
		case validators.ECDSAValidatorType:
			v = validators.NewECDSAValidator(addr)
		case validators.BLSValidatorType:
			pubLen := u64FromHash(txn.Txn().GetState(PosSysAddr, keyEpochValidatorBLSPub(epoch, uint64(idx), 0)))
			if pubLen == 0 {
				return nil, false, fmt.Errorf("missing bls public key for validator %s at epoch %d", addr, epoch)
			}
			p1 := txn.Txn().GetState(PosSysAddr, keyEpochValidatorBLSPub(epoch, uint64(idx), 1)).Bytes()
			p2 := txn.Txn().GetState(PosSysAddr, keyEpochValidatorBLSPub(epoch, uint64(idx), 2)).Bytes()
			pub := append(append([]byte{}, p1...), p2...)
			if uint64(len(pub)) < pubLen {
				return nil, false, fmt.Errorf("invalid bls public key for validator %s at epoch %d", addr, epoch)
			}
			pub = pub[:pubLen]
			if _, err := crypto.UnmarshalBLSPublicKey(pub); err != nil {
				return nil, false, fmt.Errorf("invalid bls public key for validator %s at epoch %d: %w", addr, epoch, err)
			}
			v = validators.NewBLSValidator(addr, pub)
		default:
			return nil, false, fmt.Errorf("unsupported validator type %s", validatorType)
		}
		if err := set.Add(v); err != nil {
			return nil, false, err
		}
	}

	if set.Len() == 0 {
		return nil, false, fmt.Errorf("macro epoch validator set is empty at epoch %d", epoch)
	}

	return set, true, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func HasMacroEpochStakeSnapshot(txn *state.Transition, epoch uint64, validatorSet validators.Validators) bool {
	if txn == nil || epoch == 0 {
		return false
	}
	if validatorSet == nil || validatorSet.Len() == 0 {
		return false
	}

	for i := 0; i < validatorSet.Len(); i++ {
		addr := validatorSet.At(uint64(i)).Addr()
		stake := bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakeSnapshot(epoch, addr)))
		if stake == nil || stake.Sign() <= 0 {
			return false
		}
	}

	return true
}

func StoreMacroEpochNoSlashMode(txn *state.Transition, epoch uint64, noSlash bool) error {
	if txn == nil {
		return fmt.Errorf("transition is required")
	}
	if epoch == 0 {
		return fmt.Errorf("epoch must be > 0")
	}
	ensurePosSysAccount(txn)

	mode := uint64(2)
	if noSlash {
		mode = 1
	}
	txn.Txn().SetState(PosSysAddr, keyEpochNoSlashMode(epoch), u64ToHash(mode))

	return nil
}

func LoadMacroEpochNoSlashMode(txn *state.Transition, epoch uint64) (bool, bool) {
	if txn == nil || epoch == 0 {
		return false, false
	}

	raw := u64FromHash(txn.Txn().GetState(PosSysAddr, keyEpochNoSlashMode(epoch)))
	switch raw {
	case 1:
		return true, true
	case 2:
		return false, true
	default:
		return false, false
	}
}

func StoreMacroEpochStakeSnapshot(txn *state.Transition, epoch uint64, stakes map[types.Address]*big.Int) error {
	if txn == nil {
		return fmt.Errorf("transition is required")
	}
	if epoch == 0 {
		return fmt.Errorf("epoch must be > 0")
	}
	if stakes == nil {
		return fmt.Errorf("stake snapshot is nil for epoch %d", epoch)
	}
	ensurePosSysAccount(txn)

	stakeCopies := make(map[types.Address]*big.Int, len(stakes))
	for addr, stake := range stakes {
		if stake == nil || stake.Sign() <= 0 {
			return fmt.Errorf("non-positive stake for validator %s at epoch %d", addr, epoch)
		}
		stakeCopies[addr] = new(big.Int).Set(stake)
	}

	for addr, stake := range stakeCopies {
		txn.Txn().SetState(PosSysAddr, keyStakeSnapshot(epoch, addr), bigToHash(stake))
	}

	return nil
}

func LoadMacroEpochStakeSnapshot(txn *state.Transition, epoch uint64, validatorSet validators.Validators) (map[types.Address]*big.Int, bool, error) {
	if txn == nil || epoch == 0 {
		return nil, false, nil
	}
	if validatorSet == nil || validatorSet.Len() == 0 {
		return nil, false, fmt.Errorf("validator set is empty at epoch %d", epoch)
	}

	out := make(map[types.Address]*big.Int, validatorSet.Len())
	for i := 0; i < validatorSet.Len(); i++ {
		addr := validatorSet.At(uint64(i)).Addr()
		stake := bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakeSnapshot(epoch, addr)))
		if stake == nil || stake.Sign() <= 0 {
			return nil, false, fmt.Errorf("missing non-positive stake for validator %s at epoch %d", addr, epoch)
		}
		out[addr] = new(big.Int).Set(stake)
	}

	return out, true, nil
}

func EnsureMacroEpochSnapshots(
	txn *state.Transition,
	epoch uint64,
	validatorSet validators.Validators,
	stakeSnapshot map[types.Address]*big.Int,
) error {
	if txn == nil {
		return fmt.Errorf("transition is required")
	}
	if epoch == 0 {
		return fmt.Errorf("epoch must be > 0")
	}
	if validatorSet == nil || validatorSet.Len() == 0 {
		return fmt.Errorf("validator set is empty at epoch %d", epoch)
	}
	ensurePosSysAccount(txn)

	for i := 0; i < validatorSet.Len(); i++ {
		addr := validatorSet.At(uint64(i)).Addr()
		stake := stakeSnapshot[addr]
		if stake == nil || stake.Sign() <= 0 {
			return fmt.Errorf("missing non-positive stake for validator %s at epoch %d", addr, epoch)
		}
	}

	if HasMacroEpochValidatorSet(txn, epoch) {
		loadedSet, ok, err := LoadMacroEpochValidatorSet(txn, epoch, validatorSet.Type())
		if err != nil {
			return err
		}
		if !ok || loadedSet == nil {
			return fmt.Errorf("macro epoch validator snapshot missing at epoch %d", epoch)
		}
		if !loadedSet.Equal(validatorSet) {
			return fmt.Errorf("conflicting macro epoch validator snapshot at epoch %d", epoch)
		}
	} else if err := StoreMacroEpochValidatorSet(txn, epoch, validatorSet); err != nil {
		return err
	}

	if HasMacroEpochStakeSnapshot(txn, epoch, validatorSet) {
		loadedStake, ok, err := LoadMacroEpochStakeSnapshot(txn, epoch, validatorSet)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("macro epoch stake snapshot missing at epoch %d", epoch)
		}
		for i := 0; i < validatorSet.Len(); i++ {
			addr := validatorSet.At(uint64(i)).Addr()
			exp := stakeSnapshot[addr]
			got := loadedStake[addr]
			if exp == nil || got == nil || exp.Cmp(got) != 0 {
				return fmt.Errorf("conflicting macro epoch stake snapshot for validator %s at epoch %d", addr, epoch)
			}
		}
		return nil
	}

	return StoreMacroEpochStakeSnapshot(txn, epoch, stakeSnapshot)
}
