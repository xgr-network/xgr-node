package pos

import (
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func microEpochOfBlock(blockNumber, microEpochSize uint64) uint64 {
	if blockNumber == 0 || microEpochSize == 0 {
		return 0
	}

	return (blockNumber - 1) / microEpochSize
}

func applyMicroEpochUptime(txn *state.Transition, valSet validators.Validators, currentBlock uint64, _ types.Address, cfg UptimeConfig) error {
	if txn == nil || valSet == nil || cfg.MicroEpochSize == 0 {
		return nil
	}

	currentME := microEpochOfBlock(currentBlock, cfg.MicroEpochSize)
	lastApplied := u64FromHash(txn.Txn().GetState(PosSysAddr, keyMicroEpochApplied()))

	for me := lastApplied; me < currentME; me++ {
		for i := 0; i < valSet.Len(); i++ {
			addr := valSet.At(uint64(i)).Addr()
			dutyKey := keyMicroEpochDuty(me, addr)
			seenKey := keyMicroEpochSeen(me, addr)
			duty := u64FromHash(txn.Txn().GetState(PosSysAddr, dutyKey)) == 1
			seen := u64FromHash(txn.Txn().GetState(PosSysAddr, seenKey)) == 1

			nominal := UptimeNominalWeight(txn.Txn(), addr)
			if nominal == 0 {
				nominal = cfg.MicroEpochNominalWeight
			}
			eff := UptimeEffectiveWeight(txn.Txn(), addr)
			if eff == 0 {
				eff = nominal
			}

			switch {
			case seen:
				txn.Txn().SetState(PosSysAddr, keyUptimeEffectiveWeight(addr), u64ToHash(nominal))
				txn.Txn().SetState(PosSysAddr, keyUptimeInactivity(addr), u64ToHash(0))
				eff = nominal
			case duty:
				decayed := eff * cfg.MicroEpochInactivityDecayBps / 10000
				if decayed < 1 {
					decayed = 1
				}
				txn.Txn().SetState(PosSysAddr, keyUptimeEffectiveWeight(addr), u64ToHash(decayed))
				incU64State(txn, keyUptimeInactivity(addr), 1)
				eff = decayed
			}

			// Micro-epoch duty/seen flags are working state only; clear them once applied
			// so old flags cannot affect future uptime calculations.
			txn.Txn().SetState(PosSysAddr, dutyKey, types.Hash{})
			txn.Txn().SetState(PosSysAddr, seenKey, types.Hash{})
		}
		txn.Txn().SetState(PosSysAddr, keyMicroEpochApplied(), u64ToHash(me+1))
	}

	return nil
}
