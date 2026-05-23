package pos

import (
	"encoding/binary"
	"math/big"

	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

// PosSysAddr is a PoS system state address (consensus-owned storage namespace).
// It remains fixed so fork-gating stays deterministic.
var PosSysAddr = types.StringToAddress("0x0000000000000000000000000000000000009999")

const (
	keyPrefixPropSlots       = "xgr.pos.prop.slots"
	keyPrefixPropMiss        = "xgr.pos.prop.miss"
	keyPrefixLastProp        = "xgr.pos.last.proposer"
	keyPrefixEpochValsLen    = "xgr.pos.epoch.validators.len"
	keyPrefixEpochVal        = "xgr.pos.epoch.validators.val"
	keyPrefixEpochValBLSPub  = "xgr.pos.epoch.validators.bls.pub"
	keyPrefixStakeSnap       = "xgr.pos.stake.snapshot"
	keyPrefixEpochNoSlash    = "xgr.pos.epoch.no_slash_mode"
	keyPrefixStakerStakeSnap = "xgr.pos.staker.stake.snapshot"
	keyPrefixSlashed         = "xgr.pos.slashed"
	keyPrefixSlashAmt        = "xgr.pos.slash.amount"
	keyPrefixRewardTot       = "xgr.pos.reward.total"
	keyPrefixRewardVal       = "xgr.pos.reward.validator"
	keyPrefixRewardValComm   = "xgr.pos.reward.validator.commission"
	keyPrefixRewardDelTot    = "xgr.pos.reward.delegators.total"
	keyPrefixRewardDelegator = "xgr.pos.reward.delegator"
	keyPrefixMicroEpApplied  = "xgr.uptime.microepoch.last.applied"
	keyPrefixMicroEpSeen     = "xgr.uptime.microepoch.seen"
	keyPrefixMicroEpDuty     = "xgr.uptime.microepoch.duty"
	keyPrefixUptimeEffW      = "xgr.uptime.effective.weight"
	keyPrefixUptimeNomW      = "xgr.uptime.nominal.weight"
	keyPrefixUptimeInact     = "xgr.uptime.inactivity.count"
	keyPrefixUptimeLastSlot  = "xgr.uptime.last.processed.slot"
)

func epochOf(number, epochSize uint64) uint64 {
	// mirror polygon-edge logic:
	// if number % epochSize == 0 => epoch = number/epochSize
	// else => epoch = number/epochSize + 1
	if number == 0 || epochSize == 0 {
		return 0
	}

	if number%epochSize == 0 {
		return number / epochSize
	}

	return number/epochSize + 1
}

func u64ToHash(v uint64) types.Hash {
	var b [32]byte
	binary.BigEndian.PutUint64(b[24:], v)

	return types.BytesToHash(b[:])
}

func u64FromHash(h types.Hash) uint64 {
	return binary.BigEndian.Uint64(h[24:])
}

func keyProposerSlots(epoch uint64, addr types.Address) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)
	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixPropSlots), eb[:], addr.Bytes()))
}

func keyProposerMissed(epoch uint64, addr types.Address) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)
	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixPropMiss), eb[:], addr.Bytes()))
}

func keyLastProposer() types.Hash {
	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixLastProp)))
}

func keyEpochValidatorsLen(epoch uint64) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixEpochValsLen), eb[:]))
}

func keyEpochValidator(epoch, idx uint64) types.Hash {
	var eb [8]byte
	var ib [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)
	binary.BigEndian.PutUint64(ib[:], idx)

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixEpochVal), eb[:], ib[:]))
}

func keyEpochValidatorBLSPub(epoch, idx uint64, part uint64) types.Hash {
	var eb [8]byte
	var ib [8]byte
	var pb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)
	binary.BigEndian.PutUint64(ib[:], idx)
	binary.BigEndian.PutUint64(pb[:], part)

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixEpochValBLSPub), eb[:], ib[:], pb[:]))
}

func keyStakeSnapshot(epoch uint64, addr types.Address) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixStakeSnap), eb[:], addr.Bytes()))
}

func keyEpochNoSlashMode(epoch uint64) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixEpochNoSlash), eb[:]))
}

func keyStakerStakeSnapshot(epoch uint64, addr types.Address) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixStakerStakeSnap), eb[:], addr.Bytes()))
}

func keySlashed(epoch uint64, addr types.Address) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixSlashed), eb[:], addr.Bytes()))
}

func keySlashAmount(epoch uint64, addr types.Address) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixSlashAmt), eb[:], addr.Bytes()))
}

// Legacy reward-history keys. Do not write these keys. Kept only for tests/audit
// checks that finalized PosSysAddr reward history remains absent.
func keyRewardTotal(epoch uint64) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixRewardTot), eb[:]))
}

func keyValidatorReward(epoch uint64, addr types.Address) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixRewardVal), eb[:], addr.Bytes()))
}

func keyValidatorCommissionReward(epoch uint64, addr types.Address) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixRewardValComm), eb[:], addr.Bytes()))
}

func keyValidatorDelegatorsRewardTotal(epoch uint64, addr types.Address) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixRewardDelTot), eb[:], addr.Bytes()))
}

func keyDelegatorReward(epoch uint64, validator, delegator types.Address) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixRewardDelegator), eb[:], validator.Bytes(), delegator.Bytes()))
}

func keyMicroEpochApplied() types.Hash {
	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixMicroEpApplied)))
}

func keyMicroEpochSeen(me uint64, addr types.Address) types.Hash {
	var mb [8]byte
	binary.BigEndian.PutUint64(mb[:], me)

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixMicroEpSeen), mb[:], addr.Bytes()))
}

func keyMicroEpochDuty(me uint64, addr types.Address) types.Hash {
	var mb [8]byte
	binary.BigEndian.PutUint64(mb[:], me)

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixMicroEpDuty), mb[:], addr.Bytes()))
}

func keyUptimeNominalWeight(addr types.Address) types.Hash {
	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixUptimeNomW), addr.Bytes()))
}

func keyUptimeEffectiveWeight(addr types.Address) types.Hash {
	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixUptimeEffW), addr.Bytes()))
}

func keyUptimeInactivity(addr types.Address) types.Hash {
	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixUptimeInact), addr.Bytes()))
}

func keyUptimeLastProcessedSlot() types.Hash {
	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixUptimeLastSlot)))
}

func bigToHash(v *big.Int) types.Hash {
	if v == nil {
		return types.Hash{}
	}

	b := v.Bytes()
	if len(b) == 0 {
		return types.Hash{}
	}

	var out [32]byte
	if len(b) > 32 {
		copy(out[:], b[len(b)-32:])
	} else {
		copy(out[32-len(b):], b)
	}

	return types.BytesToHash(out[:])
}

func bigFromHash(h types.Hash) *big.Int {
	if h == (types.Hash{}) {
		return new(big.Int)
	}

	return new(big.Int).SetBytes(h[:])
}
