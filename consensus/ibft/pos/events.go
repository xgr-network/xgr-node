package pos

import (
	"math/big"

	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
)

var (
	posEventEpochFinalizedTopic           = eventTopic("EpochFinalized(uint256,uint256,uint256,uint256,uint256,uint256,uint256)")
	posEventStakerRewardedTopic           = eventTopic("StakerRewarded(uint256,address,address,uint8,uint256,uint256,uint256,uint256)")
	posEventStakerSlashedTopic            = eventTopic("StakerSlashed(uint256,address,address,uint8,uint256,uint256,uint256,uint256,uint256,uint256,uint256,address)")
	posEventValidatorUptimeFinalizedTopic = eventTopic("ValidatorUptimeFinalized(uint256,address,uint256,uint256,uint256,uint256,uint256,bool,bool)")
)

type posStakerRewardEvent struct {
	Epoch         uint64
	Validator     types.Address
	Staker        types.Address
	Role          uint8
	Amount        *big.Int
	StakeSnapshot *big.Int
	StakeBefore   *big.Int
	StakeAfter    *big.Int
}

type posValidatorUptimeFinalizedEvent struct {
	Epoch           uint64
	Validator       types.Address
	Slots           uint64
	Missed          uint64
	UptimeBps       uint64
	EffectiveWeight *big.Int
	StakeSnapshot   *big.Int
	RewardEligible  bool
	Slashed         bool
}

type posStakerSlashEvent struct {
	Epoch         uint64
	Validator     types.Address
	Staker        types.Address
	Role          uint8
	Amount        *big.Int
	StakeSnapshot *big.Int
	StakeBefore   *big.Int
	StakeAfter    *big.Int
	Slots         uint64
	Missed        uint64
	SlashBps      uint64
	Destination   types.Address
}

func eventTopic(signature string) types.Hash {
	return types.BytesToHash(crypto.Keccak256([]byte(signature)))
}

func abiWordBig(v *big.Int) []byte {
	var out [32]byte
	if v == nil || v.Sign() == 0 {
		return out[:]
	}
	b := v.Bytes()
	if len(b) > 32 {
		b = b[len(b)-32:]
	}
	copy(out[32-len(b):], b)
	return out[:]
}

func abiWordU64(v uint64) []byte { return abiWordBig(new(big.Int).SetUint64(v)) }
func abiWordU8(v uint8) []byte   { return abiWordBig(new(big.Int).SetUint64(uint64(v))) }

func abiWordBool(v bool) []byte {
	if !v {
		return abiWordU64(0)
	}

	return abiWordU64(1)
}

func abiWordAddress(addr types.Address) []byte {
	var out [32]byte
	copy(out[12:], addr[:])
	return out[:]
}

func abiHashU64(v uint64) types.Hash               { return types.BytesToHash(abiWordU64(v)) }
func abiHashAddress(addr types.Address) types.Hash { return types.BytesToHash(abiWordAddress(addr)) }

func abiData(words ...[]byte) []byte {
	out := make([]byte, 0, len(words)*32)
	for _, word := range words {
		out = append(out, word...)
	}
	return out
}

func appendPosSystemLog(txn *state.Transition, topics []types.Hash, data []byte) {
	if txn == nil || txn.GetTxContext().NonPayable {
		return
	}
	txn.EmitLog(PosSysAddr, topics, data)
}

func appendPosEpochFinalizedLog(txn *state.Transition, epoch, blockNumber uint64, poolBalanceBefore, totalDistributed, poolRemainder, totalSlashed *big.Int, activeValidators uint64) {
	appendPosSystemLog(txn,
		[]types.Hash{posEventEpochFinalizedTopic, abiHashU64(epoch), abiHashU64(blockNumber)},
		abiData(abiWordBig(poolBalanceBefore), abiWordBig(totalDistributed), abiWordBig(poolRemainder), abiWordBig(totalSlashed), abiWordU64(activeValidators)),
	)
}

func appendPosValidatorUptimeFinalizedLog(txn *state.Transition, ev posValidatorUptimeFinalizedEvent) {
	appendPosSystemLog(txn,
		[]types.Hash{posEventValidatorUptimeFinalizedTopic, abiHashU64(ev.Epoch), abiHashAddress(ev.Validator)},
		abiData(abiWordU64(ev.Slots), abiWordU64(ev.Missed), abiWordU64(ev.UptimeBps), abiWordBig(ev.EffectiveWeight), abiWordBig(ev.StakeSnapshot), abiWordBool(ev.RewardEligible), abiWordBool(ev.Slashed)),
	)
}

func appendPosStakerRewardedLog(txn *state.Transition, ev posStakerRewardEvent) {
	appendPosSystemLog(txn,
		[]types.Hash{posEventStakerRewardedTopic, abiHashU64(ev.Epoch), abiHashAddress(ev.Validator), abiHashAddress(ev.Staker)},
		abiData(abiWordU8(ev.Role), abiWordBig(ev.Amount), abiWordBig(ev.StakeSnapshot), abiWordBig(ev.StakeBefore), abiWordBig(ev.StakeAfter)),
	)
}

func appendPosStakerSlashedLog(txn *state.Transition, ev posStakerSlashEvent) {
	appendPosSystemLog(txn,
		[]types.Hash{posEventStakerSlashedTopic, abiHashU64(ev.Epoch), abiHashAddress(ev.Validator), abiHashAddress(ev.Staker)},
		abiData(abiWordU8(ev.Role), abiWordBig(ev.Amount), abiWordBig(ev.StakeSnapshot), abiWordBig(ev.StakeBefore), abiWordBig(ev.StakeAfter), abiWordU64(ev.Slots), abiWordU64(ev.Missed), abiWordU64(ev.SlashBps), abiWordAddress(ev.Destination)),
	)
}
