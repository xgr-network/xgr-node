package contract

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/helper/common"
	"github.com/xgr-network/xgr-node/helper/keccak"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

const (
	validatorThresholdSlot       = int64(0)
	configSlot                   = int64(1)
	validatorsSlot               = int64(2)
	stakerSlot                   = int64(3)
	stakersByValidatorSlot       = int64(6)
	validatorDelegatedActiveSlot = int64(9)
)

type weightedAddr struct {
	addr  types.Address
	stake *big.Int
}

type weightedBLS struct {
	addr  types.Address
	stake *big.Int
	bls   []byte
}

// FetchValidators fetches validators from staking state and applies filtering policy.
func FetchValidators(
	validatorType validators.ValidatorType,
	transition *state.Transition,
	_ types.Address,
) (validators.Validators, error) {
	switch validatorType {
	case validators.ECDSAValidatorType:
		return FetchECDSAValidators(transition, types.ZeroAddress)
	case validators.BLSValidatorType:
		return FetchBLSValidators(transition, types.ZeroAddress)
	}

	return nil, fmt.Errorf("unsupported validator type: %s", validatorType)
}

func FetchECDSAValidators(transition *state.Transition, _ types.Address) (validators.Validators, error) {
	valAddrs := readValidators(transition)
	minCount := readMinNumValidators(transition)
	maxCount := readMaxNumValidators(transition)
	threshold := readValidatorThreshold(transition)

	eligible := validators.NewECDSAValidatorSet()
	eligibleWeighted := make([]weightedAddr, 0, len(valAddrs))
	emergency := validators.NewECDSAValidatorSet()
	emergencyWeighted := make([]weightedAddr, 0, len(valAddrs))

	for _, addr := range valAddrs {
		candidate := validators.NewECDSAValidator(addr)
		selfStake := readStakerAmount(transition, addr)
		totalStake := ReadValidatorEffectiveTotalStakeAt(transition, addr, currentBlockNumber(transition))
		votingStake := ReadValidatorVotingStakeAt(transition, addr, currentBlockNumber(transition))

		// Emergency validator selection intentionally retains inactive-but-staked validators and uses
		// the same canonical stake basis as consensus voting power.
		if votingStake.Sign() > 0 {
			emergencyWeighted = append(emergencyWeighted, weightedAddr{addr: addr, stake: votingStake})
		}

		if isValidatorActive(transition, addr) {
			if err := emergency.Add(candidate); err != nil {
				return nil, err
			}
		}

		if !isValidatorActive(transition, addr) {
			continue
		}
		if selfStake.Cmp(validatorSelfStakeMinWei()) < 0 || totalStake.Cmp(threshold) < 0 {
			continue
		}
		if err := eligible.Add(candidate); err != nil {
			return nil, err
		}

		eligibleWeighted = append(eligibleWeighted, weightedAddr{addr: addr, stake: totalStake})
	}

	if uint64(eligible.Len()) < minCount {
		if uint64(emergency.Len()) >= minCount {
			return trimECDSASetToMax(emergency, maxCount), nil
		}

		res, err := weightedECDSASet(emergencyWeighted)
		if err != nil {
			return nil, err
		}

		return trimECDSASetToMax(res, maxCount), nil
	}

	if maxCount > 0 && uint64(eligible.Len()) > maxCount {
		res, err := weightedECDSASet(eligibleWeighted)
		if err != nil {
			return nil, err
		}

		return trimECDSASetToMax(res, maxCount), nil
	}

	return eligible, nil
}

// IsEmergencyModeActive returns true when the normal eligibility filter would
// produce a validator set below the configured minimum validator count.
// In this mode, the fetcher falls back to broader selection rules.
func IsEmergencyModeActive(transition *state.Transition) bool {
	valAddrs := readValidators(transition)
	minCount := readMinNumValidators(transition)
	threshold := readValidatorThreshold(transition)

	eligibleCount := uint64(0)
	for _, addr := range valAddrs {
		if !isValidatorActive(transition, addr) {
			continue
		}

		selfStake := readStakerAmount(transition, addr)
		totalStake := ReadValidatorEffectiveTotalStakeAt(transition, addr, currentBlockNumber(transition))
		if selfStake.Cmp(validatorSelfStakeMinWei()) < 0 || totalStake.Cmp(threshold) < 0 {
			continue
		}

		eligibleCount++
	}

	return eligibleCount < minCount
}

func FetchBLSValidators(transition *state.Transition, _ types.Address) (validators.Validators, error) {
	valAddrs := readValidators(transition)
	minCount := readMinNumValidators(transition)
	maxCount := readMaxNumValidators(transition)
	threshold := readValidatorThreshold(transition)

	eligible := validators.NewBLSValidatorSet()
	eligibleWeighted := make([]weightedBLS, 0, len(valAddrs))
	emergency := validators.NewBLSValidatorSet()
	emergencyWeighted := make([]weightedBLS, 0, len(valAddrs))

	for _, addr := range valAddrs {
		blsKey := readMetaBLSPubKey(transition, addr)
		selfStake := readStakerAmount(transition, addr)
		totalStake := ReadValidatorEffectiveTotalStakeAt(transition, addr, currentBlockNumber(transition))
		votingStake := ReadValidatorVotingStakeAt(transition, addr, currentBlockNumber(transition))
		_, blsErr := crypto.UnmarshalBLSPublicKey(blsKey)

		if votingStake.Sign() > 0 && blsErr == nil {
			emergencyWeighted = append(emergencyWeighted, weightedBLS{addr: addr, stake: votingStake, bls: blsKey})
		}

		if isValidatorActive(transition, addr) && blsErr == nil {
			if err := emergency.Add(validators.NewBLSValidator(addr, blsKey)); err != nil {
				return nil, err
			}
		}

		if !isValidatorActive(transition, addr) {
			continue
		}
		if selfStake.Cmp(validatorSelfStakeMinWei()) < 0 || totalStake.Cmp(threshold) < 0 {
			continue
		}
		if blsErr != nil {
			continue
		}
		if err := eligible.Add(validators.NewBLSValidator(addr, blsKey)); err != nil {
			return nil, err
		}

		eligibleWeighted = append(eligibleWeighted, weightedBLS{addr: addr, stake: totalStake, bls: blsKey})
	}

	if uint64(eligible.Len()) < minCount {
		if uint64(emergency.Len()) >= minCount {
			return trimBLSSetToMax(emergency, maxCount), nil
		}

		res, err := weightedBLSSet(emergencyWeighted)
		if err != nil {
			return nil, err
		}

		return trimBLSSetToMax(res, maxCount), nil
	}

	if maxCount > 0 && uint64(eligible.Len()) > maxCount {
		res, err := weightedBLSSet(eligibleWeighted)
		if err != nil {
			return nil, err
		}

		return trimBLSSetToMax(res, maxCount), nil
	}

	return eligible, nil
}

func readValidators(transition *state.Transition) []types.Address {
	if transition == nil {
		return nil
	}
	lenHash := transition.Txn().GetState(staking.AddrStakingContract, types.BytesToHash(big.NewInt(validatorsSlot).Bytes()))
	length := new(big.Int).SetBytes(lenHash[:]).Uint64()
	if length == 0 {
		return nil
	}
	base := keccak.Keccak256(nil, common.PadLeftOrTrim(big.NewInt(validatorsSlot).Bytes(), 32))
	idx := new(big.Int).SetBytes(base)
	out := make([]types.Address, 0, length)
	for i := uint64(0); i < length; i++ {
		slot := new(big.Int).Add(idx, new(big.Int).SetUint64(i))
		raw := transition.Txn().GetState(staking.AddrStakingContract, types.BytesToHash(slot.Bytes()))
		out = append(out, types.BytesToAddress(raw[12:]))
	}
	return out
}

func readMinNumValidators(transition *state.Transition) uint64 {
	if transition == nil {
		return 0
	}
	raw := transition.Txn().GetState(staking.AddrStakingContract, types.BytesToHash(big.NewInt(configSlot).Bytes()))
	v := new(big.Int).SetBytes(raw[:])
	v.Rsh(v, 64)
	return lowUint64(v)
}

func readMaxNumValidators(transition *state.Transition) uint64 {
	if transition == nil {
		return 0
	}

	raw := transition.Txn().GetState(staking.AddrStakingContract, types.BytesToHash(big.NewInt(configSlot).Bytes()))
	v := new(big.Int).SetBytes(raw[:])
	v.Rsh(v, 128)

	return lowUint64(v)
}

func readValidatorThreshold(transition *state.Transition) *big.Int {
	if transition == nil {
		return validatorThresholdWei()
	}

	raw := transition.Txn().GetState(staking.AddrStakingContract, types.BytesToHash(big.NewInt(validatorThresholdSlot).Bytes()))
	v := new(big.Int).SetBytes(raw[:])
	if v.Sign() == 0 {
		return validatorThresholdWei()
	}

	return v
}

func weightedECDSASet(weighted []weightedAddr) (validators.Validators, error) {
	sort.Slice(weighted, func(i, j int) bool {
		cmp := weighted[i].stake.Cmp(weighted[j].stake)
		if cmp == 0 {
			return weighted[i].addr.String() < weighted[j].addr.String()
		}
		return cmp > 0
	})

	out := validators.NewECDSAValidatorSet()
	for _, it := range weighted {
		if err := out.Add(validators.NewECDSAValidator(it.addr)); err != nil {
			return nil, err
		}
	}

	return out, nil
}

func trimECDSASetToMax(set validators.Validators, maxCount uint64) validators.Validators {
	if set == nil || maxCount == 0 || uint64(set.Len()) <= maxCount {
		return set
	}

	trimmed := validators.NewECDSAValidatorSet()
	for i := uint64(0); i < maxCount; i++ {
		_ = trimmed.Add(set.At(i))
	}

	return trimmed
}

func weightedBLSSet(weighted []weightedBLS) (validators.Validators, error) {
	sort.Slice(weighted, func(i, j int) bool {
		cmp := weighted[i].stake.Cmp(weighted[j].stake)
		if cmp == 0 {
			return weighted[i].addr.String() < weighted[j].addr.String()
		}
		return cmp > 0
	})

	out := validators.NewBLSValidatorSet()
	for _, it := range weighted {
		if err := out.Add(validators.NewBLSValidator(it.addr, it.bls)); err != nil {
			return nil, err
		}
	}

	return out, nil
}

func trimBLSSetToMax(set validators.Validators, maxCount uint64) validators.Validators {
	if set == nil || maxCount == 0 || uint64(set.Len()) <= maxCount {
		return set
	}

	trimmed := validators.NewBLSValidatorSet()
	for i := uint64(0); i < maxCount; i++ {
		_ = trimmed.Add(set.At(i))
	}

	return trimmed
}

func lowUint64(v *big.Int) uint64 {
	if v == nil {
		return 0
	}

	mask := new(big.Int).SetUint64(^uint64(0))
	t := new(big.Int).And(v, mask)

	return t.Uint64()
}

func isValidatorActive(transition *state.Transition, addr types.Address) bool {
	if readStakerValidator(transition, addr) != addr {
		return false
	}
	return readStakerPacked(transition, addr).Bit(8) == 1
}

func readStakerAmount(transition *state.Transition, addr types.Address) *big.Int {
	raw := transition.Txn().GetState(staking.AddrStakingContract, stakerSlotWithOffset(addr, 1))
	return new(big.Int).SetBytes(raw[:])
}

func readMetaBLSPubKey(transition *state.Transition, addr types.Address) []byte {
	base := stakerSlotWithOffset(addr, 5)
	raw := transition.Txn().GetState(staking.AddrStakingContract, base)
	b := raw[:]
	if b[31]%2 == 0 {
		ln := int(b[31] / 2)
		if ln == 0 {
			return nil
		}
		out := make([]byte, ln)
		copy(out, b[:ln])
		return out
	}
	ln := int((new(big.Int).SetBytes(b).Uint64() - 1) / 2)
	if ln <= 0 {
		return nil
	}
	dataBase := keccak.Keccak256(nil, common.PadLeftOrTrim(base.Bytes(), 32))
	slot := new(big.Int).SetBytes(dataBase)
	out := make([]byte, 0, ln)
	for len(out) < ln {
		r := transition.Txn().GetState(staking.AddrStakingContract, types.BytesToHash(slot.Bytes()))
		need := ln - len(out)
		if need > 32 {
			need = 32
		}
		out = append(out, r[:need]...)
		slot.Add(slot, big.NewInt(1))
	}
	return out
}

func readStakerPacked(transition *state.Transition, addr types.Address) *big.Int {
	raw := transition.Txn().GetState(staking.AddrStakingContract, stakerSlotWithOffset(addr, 0))
	return new(big.Int).SetBytes(raw[:])
}

func readStakerValidator(transition *state.Transition, addr types.Address) types.Address {
	raw := transition.Txn().GetState(staking.AddrStakingContract, stakerSlotWithOffset(addr, 4))
	return types.BytesToAddress(raw[12:])
}

func readValidatorDelegatedStakeActive(transition *state.Transition, validator types.Address) *big.Int {
	base := keccak.Keccak256(nil,
		append(common.PadLeftOrTrim(validator.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(validatorDelegatedActiveSlot).Bytes(), 32)...),
	)
	raw := transition.Txn().GetState(staking.AddrStakingContract, types.BytesToHash(base))
	return new(big.Int).SetBytes(raw[:])
}

// ReadValidatorEffectiveTotalStakeAt returns epoch-effective total stake at a block.
// It is used for eligibility, Tier-1 selection, and epoch-based economic views.
// Self stake and delegator stake are counted only when epoch-effective.
// setActive/deactivation can affect inclusion through isStakerEffectiveAt.
func ReadValidatorEffectiveTotalStakeAt(
	transition *state.Transition,
	validator types.Address,
	blockNumber uint64,
) *big.Int {
	if transition == nil || readStakerValidator(transition, validator) != validator {
		return new(big.Int)
	}

	epochSize := readEpochSize(transition)
	total := new(big.Int)
	if isStakerEffectiveAt(transition, validator, blockNumber, epochSize) {
		total.Add(total, readStakerAmount(transition, validator))
	}

	for _, owner := range readValidatorStakers(transition, validator) {
		if owner == validator {
			continue
		}

		if isStakerEffectiveAt(transition, owner, blockNumber, epochSize) {
			total.Add(total, readStakerAmount(transition, owner))
		}
	}

	return total
}

// ReadValidatorVotingStakeAt returns the canonical stake basis for stake-weighted IBFT voting.
// It is only used for validators already selected into the validator set by the fetcher.
// Self stake always counts as raw stake when self-mapped.
// Delegated stake counts only when epoch-effective at blockNumber.
// setActive, deactivation, and join-maturity do not gate self stake here.
func ReadValidatorVotingStakeAt(
	transition *state.Transition,
	validator types.Address,
	blockNumber uint64,
) *big.Int {
	if transition == nil || readStakerValidator(transition, validator) != validator {
		return new(big.Int)
	}

	epochSize := readEpochSize(transition)
	total := new(big.Int).Set(readStakerAmount(transition, validator))
	for _, owner := range readValidatorStakers(transition, validator) {
		if owner == validator {
			continue
		}

		if isStakerEffectiveAt(transition, owner, blockNumber, epochSize) {
			total.Add(total, readStakerAmount(transition, owner))
		}
	}

	return total
}

// ReadStakerEffectiveStakeAt returns staker amount effective at blockNumber.
// If the staker is not epoch-effective at blockNumber, zero is returned.
func ReadStakerEffectiveStakeAt(
	transition *state.Transition,
	staker types.Address,
	blockNumber uint64,
) *big.Int {
	if transition == nil {
		return new(big.Int)
	}

	epochSize := readEpochSize(transition)
	if !isStakerEffectiveAt(transition, staker, blockNumber, epochSize) {
		return new(big.Int)
	}

	return readStakerAmount(transition, staker)
}

func currentBlockNumber(transition *state.Transition) uint64 {
	if transition == nil {
		return 0
	}

	return uint64(transition.GetTxContext().Number)
}

func readEpochSize(transition *state.Transition) uint64 {
	if transition == nil {
		return 0
	}

	raw := transition.Txn().GetState(staking.AddrStakingContract, types.BytesToHash(big.NewInt(configSlot).Bytes()))
	v := new(big.Int).SetBytes(raw[:])

	return lowUint64(v)
}

func readValidatorStakers(transition *state.Transition, validator types.Address) []types.Address {
	base := keccak.Keccak256(nil,
		append(common.PadLeftOrTrim(validator.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(stakersByValidatorSlot).Bytes(), 32)...),
	)
	baseHash := types.BytesToHash(base)
	lengthRaw := transition.Txn().GetState(staking.AddrStakingContract, baseHash)
	length := new(big.Int).SetBytes(lengthRaw[:]).Uint64()
	if length == 0 {
		return nil
	}

	dataBase := keccak.Keccak256(nil, common.PadLeftOrTrim(base, 32))
	idx := new(big.Int).SetBytes(dataBase)
	out := make([]types.Address, 0, length)
	for i := uint64(0); i < length; i++ {
		slot := new(big.Int).Add(idx, new(big.Int).SetUint64(i))
		raw := transition.Txn().GetState(staking.AddrStakingContract, types.BytesToHash(slot.Bytes()))
		out = append(out, types.BytesToAddress(raw[12:]))
	}

	return out
}

func readStakerJoinedAtBlock(transition *state.Transition, addr types.Address) uint64 {
	raw := transition.Txn().GetState(staking.AddrStakingContract, stakerSlotWithOffset(addr, 2))
	return new(big.Int).SetBytes(raw[:]).Uint64()
}

func readStakerDeactivatedAtBlock(transition *state.Transition, addr types.Address) uint64 {
	raw := transition.Txn().GetState(staking.AddrStakingContract, stakerSlotWithOffset(addr, 3))
	return new(big.Int).SetBytes(raw[:]).Uint64()
}

func isStakerEffectiveAt(transition *state.Transition, addr types.Address, blockNumber, epochSize uint64) bool {
	if !isStakerMatureAt(transition, addr, blockNumber, epochSize) {
		return false
	}

	deact := readStakerDeactivatedAtBlock(transition, addr)
	if deact == 0 {
		return true
	}

	return blockNumber/epochSize <= deact/epochSize
}

func isStakerMatureAt(transition *state.Transition, addr types.Address, blockNumber, epochSize uint64) bool {
	joined := readStakerJoinedAtBlock(transition, addr)
	if joined == 0 {
		return false
	}
	if epochSize == 0 {
		return readStakerPacked(transition, addr).Bit(8) == 1
	}
	return blockNumber/epochSize > joined/epochSize
}

func stakerSlotWithOffset(addr types.Address, offset int64) types.Hash {
	base := keccak.Keccak256(nil,
		append(common.PadLeftOrTrim(addr.Bytes(), 32), common.PadLeftOrTrim(big.NewInt(stakerSlot).Bytes(), 32)...),
	)
	idx := new(big.Int).SetBytes(base)
	idx.Add(idx, big.NewInt(offset))
	return types.BytesToHash(idx.Bytes())
}

func validatorSelfStakeMinWei() *big.Int {
	base := new(big.Int).SetUint64(200_000)
	wei := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	return base.Mul(base, wei)
}

func validatorThresholdWei() *big.Int {
	base := new(big.Int).SetUint64(2_000_000)
	wei := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	return base.Mul(base, wei)
}
