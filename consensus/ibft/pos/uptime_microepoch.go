package pos

import (
	"fmt"

	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

type microEpochValidatorSetResolver func(me uint64) (validators.Validators, error)

func microEpochOfBlock(blockNumber, microEpochSize uint64) uint64 {
	if blockNumber == 0 || microEpochSize == 0 {
		return 0
	}

	return (blockNumber - 1) / microEpochSize
}

func applyMicroEpochUptime(txn *state.Transition, currentBlock uint64, cfg UptimeConfig, resolve microEpochValidatorSetResolver) error {
	if txn == nil || cfg.MicroEpochSize == 0 {
		return nil
	}
	if resolve == nil {
		return fmt.Errorf("micro-epoch validator set resolver is nil")
	}

	currentME := microEpochOfBlock(currentBlock, cfg.MicroEpochSize)
	lastApplied := u64FromHash(txn.Txn().GetState(PosSysAddr, keyMicroEpochApplied()))

	for me := lastApplied; me < currentME; me++ {
		valSet, err := resolve(me)
		if err != nil {
			return err
		}
		if valSet == nil || valSet.Len() == 0 {
			return fmt.Errorf("empty validator set for micro-epoch %d", me)
		}

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

		}
		txn.Txn().SetState(PosSysAddr, keyMicroEpochApplied(), u64ToHash(me+1))
		pruneAppliedMicroEpochMarkers(txn, me, valSet)
	}

	return nil
}

func pruneAppliedMicroEpochMarkers(txn *state.Transition, me uint64, valSet validators.Validators) {
	if txn == nil || valSet == nil || valSet.Len() == 0 {
		return
	}
	zero := types.ZeroHash
	for i := 0; i < valSet.Len(); i++ {
		addr := valSet.At(uint64(i)).Addr()
		txn.Txn().SetState(PosSysAddr, keyMicroEpochSeen(me, addr), zero)
		txn.Txn().SetState(PosSysAddr, keyMicroEpochDuty(me, addr), zero)
	}
}
