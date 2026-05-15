package pos

import (
	"fmt"

	"github.com/hashicorp/go-hclog"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

var posDiagLogger = hclog.L().Named("ibft-pos-diag")

var ensureEpochValidatorsSnapshotFn = ensureEpochValidatorsSnapshot
var getOrInitStakeSnapshotFn = getOrInitStakeSnapshot
var getOrInitStakerStakeSnapshotFn = getOrInitStakerStakeSnapshot

const maxFinalRoundForUptime = uint64(1000)

// RecordBlockUptime accounts proposer availability deterministically from the finalized header.
//
// Rationale:
// Counting commit signatures is unsafe if the header only contains quorum seals (subset),
// which can lead to systematic false slashing. RoundNumber is consensus-verified and lets
// us penalize only provable missed proposer slots.
//
// Accounting model (per epoch, per validator):
// - proposerSlots += 1 for each assigned proposer slot (for each round attempt)
// - proposerMissed += 1 for each failed round (r < finalRound)
//
// Notes:
//   - Genesis (block 0) is ignored.
//   - Epoch boundary blocks (height % epochSize == 0) are skipped to keep alignment with FinalizeEpoch
//     (which finalizes epochOf(height-1)).
func RecordBlockUptime(
	header *types.Header,
	epochSize uint64,
	epochValidators validators.Validators,
	headerSigner signer.Signer,
	uptimeCfg UptimeConfig,
	txn *state.Transition,
) error {
	if header == nil || txn == nil {
		return nil
	}

	// Fork-gated: no PoS accounting before pooling overlay is active.
	if !txn.ForksInTime().FeePoolSplit {
		return nil
	}

	// IMPORTANT: keep PosSysAddr alive (otherwise storage is pruned every block).
	ensurePosSysAccount(txn)
	if epochSize < 2 {
		posDiagLogger.Error("RecordBlockUptime failed: epochSize must be >= 2", "block", header.Number, "epochSize", epochSize)
		return fmt.Errorf("epochSize must be greater than or equal to 2")
	}

	if header.Number == 0 {
		return nil
	}

	// Skip the tx-free epoch boundary block. This block exists as a deterministic finalize/system block.
	if header.Number%epochSize == 0 {
		return nil
	}

	epoch := epochOf(header.Number, epochSize)
	if epoch == 0 {
		return nil
	}

	// A single monotonic slot guard replaces unbounded per-block processed keys.
	// Reprocessing the same or an older block is idempotently ignored.
	if u64FromHash(txn.Txn().GetState(PosSysAddr, keyUptimeLastProcessedSlot())) >= header.Number {
		return nil
	}

	extra, err := getIBFTExtra(headerSigner, header)
	if err != nil {
		// Don't brick block import.
		posDiagLogger.Error("RecordBlockUptime cannot decode IBFT extra", "block", header.Number, "epoch", epoch, "err", err)
		return fmt.Errorf("decode IBFT extra for block %d epoch %d: %w", header.Number, epoch, err)
	}

	valSet := epochValidators
	if valSet == nil || valSet.Len() == 0 {
		valSet = extra.Validators
	}
	if valSet == nil || valSet.Len() == 0 {
		posDiagLogger.Error("RecordBlockUptime skipped: validator set is empty", "block", header.Number, "epoch", epoch)
		return fmt.Errorf("validator set empty for block %d epoch %d", header.Number, epoch)
	}
	// Freeze the exact validator membership of this epoch on the first processed
	// non-boundary block. FinalizeEpoch and monitoring for epoch X must use the
	// epoch-X membership, even if set-active=false was toggled during X and only
	// becomes effective at X+1.
	lenKey := keyEpochValidatorsLen(epoch)
	if u64FromHash(txn.Txn().GetState(PosSysAddr, lenKey)) == 0 {
		ok, err := ensureEpochValidatorsSnapshotFn(txn, epoch, valSet)
		if err != nil {
			return fmt.Errorf("freeze epoch validator snapshot for epoch %d: %w", epoch, err)
		}
		if !ok {
			return fmt.Errorf("freeze epoch validator snapshot for epoch %d", epoch)
		}
		if u64FromHash(txn.Txn().GetState(PosSysAddr, lenKey)) == 0 {
			return fmt.Errorf("freeze epoch validator snapshot for epoch %d: missing after write", epoch)
		}
	}

	// Freeze per-epoch stake snapshots as early as possible (first processed block in epoch)
	// for all validators in the consensus set, so last-minute stake changes do not distort
	// epoch reward/slash weights at finalize.
	for i := 0; i < valSet.Len(); i++ {
		addr := valSet.At(uint64(i)).Addr()
		if _, err := getOrInitStakeSnapshotFn(txn, epoch, addr); err != nil {
			return fmt.Errorf("freeze stake snapshot for validator %s in epoch %d: %w", addr, epoch, err)
		}
		if _, err := getOrInitStakerStakeSnapshotFn(txn, epoch, addr); err != nil {
			return fmt.Errorf("freeze staker snapshot for validator %s in epoch %d: %w", addr, epoch, err)
		}
		for _, staker := range readValidatorStakers(txn, addr) {
			if !isStakerEffectiveAt(txn, staker, header.Number-1, epochSize) {
				continue
			}
			if _, err := getOrInitStakerStakeSnapshotFn(txn, epoch, staker); err != nil {
				return fmt.Errorf("freeze staker snapshot for staker %s (validator %s) in epoch %d: %w", staker, addr, epoch, err)
			}
		}
	}

	// Round begins with zero; nil means 0 for backward compatibility.
	finalRound := uint64(0)
	if extra.RoundNumber != nil {
		finalRound = *extra.RoundNumber
	}
	if finalRound > maxFinalRoundForUptime {
		posDiagLogger.Error(
			"RecordBlockUptime finalRound too large, clamping",
			"block", header.Number,
			"epoch", epoch,
			"finalRound", finalRound,
			"maxFinalRoundForUptime", maxFinalRoundForUptime,
		)
		finalRound = maxFinalRoundForUptime
	}
	actual, err := resolveProposer(headerSigner, header, valSet)
	if err != nil {
		posDiagLogger.Error("RecordBlockUptime cannot resolve proposer", "block", header.Number, "epoch", epoch, "err", err)
		return fmt.Errorf("resolve proposer for block %d epoch %d: %w", header.Number, epoch, err)
	}

	// Use chain-persistent proposer context (previous block proposer),
	// instead of deriving it from the current block's actual proposer.
	// Deriving from actual makes expected==actual tautological and can skew rewards.
	lastProp := getLastProposer(txn)

	if uptimeCfg.MicroEpochSize > 0 {
		for i := 0; i < valSet.Len(); i++ {
			addr := valSet.At(uint64(i)).Addr()
			if UptimeNominalWeight(txn.Txn(), addr) == 0 {
				txn.Txn().SetState(PosSysAddr, keyUptimeNominalWeight(addr), u64ToHash(uptimeCfg.MicroEpochNominalWeight))
			}
			if UptimeEffectiveWeight(txn.Txn(), addr) == 0 {
				txn.Txn().SetState(PosSysAddr, keyUptimeEffectiveWeight(addr), u64ToHash(uptimeCfg.MicroEpochNominalWeight))
			}
		}

		currentME := microEpochOfBlock(header.Number, uptimeCfg.MicroEpochSize)
		if actual != types.ZeroAddress {
			txn.Txn().SetState(PosSysAddr, keyMicroEpochSeen(currentME, actual), u64ToHash(1))
		}

		for r := uint64(0); r <= finalRound; r++ {
			p := calcProposer(valSet, r, lastProp)
			if p == types.ZeroAddress {
				continue
			}
			txn.Txn().SetState(PosSysAddr, keyMicroEpochDuty(currentME, p), u64ToHash(1))
		}

		if err := applyMicroEpochUptime(txn, valSet, header.Number, actual, uptimeCfg); err != nil {
			return err
		}
	}

	// Attribute missed proposer slots for each failed round.
	for r := uint64(0); r < finalRound; r++ {
		p := calcProposer(valSet, r, lastProp)
		if p == types.ZeroAddress {
			continue
		}
		incU64State(txn, keyProposerSlots(epoch, p), 1)
		incU64State(txn, keyProposerMissed(epoch, p), 1)
	}
	// Attribute the successful finalized round to the actual finalized proposer.

	incU64State(txn, keyProposerSlots(epoch, actual), 1)

	// Persist the finalized block proposer for the next height proposer selection context.
	setLastProposer(txn, actual)

	// Mark this slot as accounted without creating per-block history.
	txn.Txn().SetState(PosSysAddr, keyUptimeLastProcessedSlot(), u64ToHash(header.Number))

	return nil
}

func calcProposer(set validators.Validators, round uint64, lastProposer types.Address) types.Address {
	if set == nil || set.Len() == 0 {
		return types.ZeroAddress
	}

	var seed uint64
	if lastProposer == types.ZeroAddress {
		seed = round
	} else {
		offset := int64(0)
		if index := set.Index(lastProposer); index != -1 {
			offset = int64(index)
		}
		seed = uint64(offset) + round + 1
	}

	pick := seed % uint64(set.Len())
	return set.At(pick).Addr()
}

func getLastProposer(txn *state.Transition) types.Address {
	if txn == nil {
		return types.ZeroAddress
	}

	raw := txn.Txn().GetState(PosSysAddr, keyLastProposer())
	var addr types.Address
	copy(addr[:], raw[12:])

	return addr
}

func setLastProposer(txn *state.Transition, addr types.Address) {
	if txn == nil || addr == types.ZeroAddress {
		return
	}

	var raw types.Hash
	copy(raw[12:], addr[:])
	txn.Txn().SetState(PosSysAddr, keyLastProposer(), raw)
}

func incU64State(txn *state.Transition, key types.Hash, delta uint64) {
	cur := txn.Txn().GetState(PosSysAddr, key)
	v := u64FromHash(cur)
	v += delta
	txn.Txn().SetState(PosSysAddr, key, u64ToHash(v))
}

func resolveProposer(headerSigner signer.Signer, header *types.Header, set validators.Validators) (types.Address, error) {
	if headerSigner == nil || header == nil {
		return types.ZeroAddress, fmt.Errorf("nil")
	}

	proposer, err := headerSigner.EcrecoverFromHeader(header)
	if err == nil {
		if set != nil && set.Index(proposer) >= 0 {
			return proposer, nil
		}

		return types.ZeroAddress, fmt.Errorf("recovered proposer not in validator set: %s", proposer.String())
	}

	return types.ZeroAddress, fmt.Errorf("proposer seal recovery failed: %w", err)
}

func getIBFTExtra(headerSigner signer.Signer, header *types.Header) (*signer.IstanbulExtra, error) {
	if headerSigner == nil || header == nil {
		return nil, fmt.Errorf("nil")
	}

	extra, err := headerSigner.GetIBFTExtra(header)
	if err != nil {
		posDiagLogger.Error("getIBFTExtra decode failed via signer", "block", header.Number, "err", err)
		return nil, err
	}

	return extra, nil
}
