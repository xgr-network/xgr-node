package pos

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/big"
	"sort"

	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/helper/common"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	contractstore "github.com/xgr-network/xgr-node/validators/store/contract"
)

var (
	posTransferFn = func(txn *state.Transition, from, to types.Address, amount *big.Int) error {
		return txn.Transfer(from, to, amount)
	}
	finalizeEpochRewardSplitFn = splitValidatorRewardByDelegation
)

// Epoch policy parameters (consensus-critical; fork-gated by FeePoolSplit).
const (
	slashBpsDefault uint64 = 20 // 0.2%

	// Defense-in-depth only.
	// Under current invariants, bootstrap stakes can only participate through emergency mode,
	// and emergency mode disables slashing. Normal slashable validators are Tier-1 eligible
	// and therefore far above this floor. Keep this guard to avoid slashing bootstrap dust
	// if future selection/slashing invariants change.
	noSlashStakeFloorWei uint64 = 10
)

// FinalizeEpoch runs on the tx-free epoch boundary block (height % epochSize == 0).
// It distributes the pooled fees and applies uptime-based penalties.
//
// Design choice: the epoch boundary block itself is treated as a system/finalize block and is NOT
// included in uptime accounting. Uptime counters are derived from RoundNumber (missed proposer slots),
// i.e. blocks 1..(epochEnd-1) for each epoch.
func FinalizeEpoch(header *types.Header, epochSize uint64, uptimeCfg UptimeConfig, headerSigner signer.Signer, txn *state.Transition) error {
	if header == nil || txn == nil {
		posDiagLogger.Debug("FinalizeEpoch skipped: nil input", "headerNil", header == nil, "txnNil", txn == nil)
		return nil
	}

	// Fork-gated: no PoS policy logic before pooling is enabled.
	if !txn.ForksInTime().FeePoolSplit {
		posDiagLogger.Debug("FinalizeEpoch skipped: FeePoolSplit not active", "block", header.Number)
		return nil
	}

	// IMPORTANT: keep PosSysAddr alive (otherwise storage is pruned every block).
	ensurePosSysAccount(txn)
	if epochSize < 2 {
		posDiagLogger.Error("FinalizeEpoch failed: epochSize must be >= 2", "block", header.Number, "epochSize", epochSize)
		return fmt.Errorf("epochSize must be greater than or equal to 2")
	}

	// Only at epoch boundary block (tx-free by hook guard)
	if header.Number == 0 || header.Number%epochSize != 0 {
		posDiagLogger.Debug("FinalizeEpoch skipped: not epoch boundary", "block", header.Number, "epochSize", epochSize)
		return nil
	}

	// Finalize the epoch for blocks up to (header.Number - 1)
	epoch := epochOf(header.Number-1, epochSize)
	if epoch == 0 {
		posDiagLogger.Debug("FinalizeEpoch skipped: epoch=0", "block", header.Number, "epochSize", epochSize)
		return nil
	}

	posDiagLogger.Debug("FinalizeEpoch start", "block", header.Number, "epoch", epoch, "epochSize", epochSize, "hash", header.Hash, "parent", header.ParentHash, "stateRoot", header.StateRoot)

	extra, err := getIBFTExtra(headerSigner, header)
	if err != nil {
		posDiagLogger.Error("FinalizeEpoch failed decoding extra", "block", header.Number, "epoch", epoch, "err", err)
		return fmt.Errorf("decode ibft extra for epoch finalization: %w", err)
	}
	if extra == nil || extra.Validators == nil || extra.Validators.Len() == 0 {
		posDiagLogger.Error("FinalizeEpoch decoded extra but validators are empty", "block", header.Number, "epoch", epoch)
		return fmt.Errorf("decode ibft extra for epoch finalization: empty validator set")
	}
	epochValidators := loadEpochValidatorsSnapshot(txn, epoch)
	if len(epochValidators) == 0 {
		posDiagLogger.Error("FinalizeEpoch failed: missing epoch validator snapshot", "block", header.Number, "epoch", epoch)
		return fmt.Errorf("missing epoch validator snapshot for epoch %d", epoch)
	}
	posDiagLogger.Debug("FinalizeEpoch using stored epoch validator snapshot", "block", header.Number, "epoch", epoch, "validatorsLen", len(epochValidators))
	uptimeLastProcessed := u64FromHash(txn.Txn().GetState(PosSysAddr, keyUptimeLastProcessedSlot()))
	posDiagLogger.Debug("FinalizeEpoch validator source ready", "block", header.Number, "epoch", epoch, "validatorsLen", len(epochValidators), "uptimeLastProcessed", uptimeLastProcessed)

	// Resolve donation address (slash target).
	donationAddr := resolveDonationAddress(txn)
	noSlashMode, hasNoSlashMode := LoadMacroEpochNoSlashMode(txn, epoch)
	if !hasNoSlashMode {
		return fmt.Errorf("missing no-slash mode for epoch %d", epoch)
	}
	slashingEnabled := !noSlashMode
	posDiagLogger.Debug("FinalizeEpoch donation address", "block", header.Number, "epoch", epoch, "donationAddr", donationAddr)
	posDiagLogger.Debug("FinalizeEpoch slashing mode", "block", header.Number, "epoch", epoch, "slashingEnabled", slashingEnabled, "hasNoSlashMode", hasNoSlashMode, "noSlashMode", noSlashMode)

	// Collect per-validator effective weights.
	type vinfo struct {
		addr   types.Address
		weight *big.Int
	}

	infos := make([]vinfo, 0, len(epochValidators))
	sumWeights := new(big.Int)
	slashEvents := make([]posStakerSlashEvent, 0)
	uptimeEvents := make([]posValidatorUptimeFinalizedEvent, 0, len(epochValidators))

	for _, addr := range epochValidators {

		// Proposer-slot based availability (provable from RoundNumber).
		slotsKey := keyProposerSlots(epoch, addr)
		missedKey := keyProposerMissed(epoch, addr)
		slots := u64FromHash(txn.Txn().GetState(PosSysAddr, slotsKey))
		missed := u64FromHash(txn.Txn().GetState(PosSysAddr, missedKey))

		// Diagnostic context: include key material and neighbor-epoch values to isolate
		// cross-path epoch/key mismatches without changing consensus behavior.
		var (
			slotsPrev  uint64
			slotsNext  uint64
			missedPrev uint64
			missedNext uint64
		)
		if epoch > 0 {
			slotsPrev = u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerSlots(epoch-1, addr)))
			missedPrev = u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerMissed(epoch-1, addr)))
		}
		slotsNext = u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerSlots(epoch+1, addr)))
		missedNext = u64FromHash(txn.Txn().GetState(PosSysAddr, keyProposerMissed(epoch+1, addr)))
		posDiagLogger.Debug("FinalizeEpoch validator counters",
			"epoch", epoch,
			"validator", addr,
			"slotsKey", slotsKey,
			"missedKey", missedKey,
			"slots", slots,
			"missed", missed,
			"slotsPrev", slotsPrev,
			"missedPrev", missedPrev,
			"slotsNext", slotsNext,
			"missedNext", missedNext,
		)

		// If the validator had no proposer slots in this epoch (e.g. joined later),
		// do NOT reward and do NOT penalize.
		if slots == 0 {
			posDiagLogger.Debug("FinalizeEpoch validator slots=0", "epoch", epoch, "validator", addr)
			uptimeEvents = append(uptimeEvents, posValidatorUptimeFinalizedEvent{
				Epoch:           epoch,
				Validator:       addr,
				EffectiveWeight: new(big.Int),
				StakeSnapshot:   bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakeSnapshot(epoch, addr))),
			})
			infos = append(infos, vinfo{addr: addr, weight: new(big.Int)})
			continue
		}
		if missed > slots {
			missed = slots
		}
		okSlots := slots - missed
		uptimeBps := okSlots * 10_000 / slots
		if okSlots == 0 {
			setValidatorInactiveInStaking(txn, addr, header.Number)
		}

		stakeSnap, err := getOrInitStakeSnapshot(txn, epoch, addr)
		if err != nil {
			return fmt.Errorf("load stake snapshot for validator %s in epoch %d: %w", addr, epoch, err)
		}

		// Thresholds are applied on okSlots/slots.
		// >= 90% => full weight
		// 80..90 => linear reduced weight: factor = (okSlots/0.9)
		// < 80% => 0 weight (reward-ineligible for this epoch)
		// < 50% => 0 weight + slash(stake)
		eff := new(big.Int)
		rewardEligible := okSlots*10 >= slots*8
		slashed := false
		if !rewardEligible {
			posDiagLogger.Debug("FinalizeEpoch validator below uptime threshold", "epoch", epoch, "validator", addr, "slots", slots, "missed", missed, "okSlots", okSlots)
			if okSlots*2 < slots { // <50%
				if slashingEnabled {
					posDiagLogger.Debug("FinalizeEpoch slashing validator", "epoch", epoch, "validator", addr, "stakeSnap", stakeSnap)
					slashEvent, err := slashByEffectiveStakeFromStakingContract(txn, epoch, addr, donationAddr, stakeSnap, slots, missed, slashBpsDefault, header.Number-1, epochSize)
					if err != nil {
						return fmt.Errorf("slash validator %s in epoch %d: %w", addr, epoch, err)
					}
					if len(slashEvent) > 0 {
						slashed = true
						slashEvents = append(slashEvents, slashEvent...)
					}
				} else {
					posDiagLogger.Debug("FinalizeEpoch slashing skipped: emergency mode active", "epoch", epoch, "validator", addr)
				}
			}
			uptimeEvents = append(uptimeEvents, posValidatorUptimeFinalizedEvent{
				Epoch:           epoch,
				Validator:       addr,
				Slots:           slots,
				Missed:          missed,
				UptimeBps:       uptimeBps,
				EffectiveWeight: eff,
				StakeSnapshot:   new(big.Int).Set(stakeSnap),
				RewardEligible:  false,
				Slashed:         slashed,
			})
			infos = append(infos, vinfo{addr: addr, weight: eff})
			continue
		}

		// factor = 1 for >=90%, else factor = (okSlots/slots) / 0.9 = okSlots*10/(slots*9)
		if okSlots*10 >= slots*9 {
			eff = new(big.Int).Set(stakeSnap)
		} else {
			num := new(big.Int).Mul(stakeSnap, new(big.Int).SetUint64(okSlots*10))
			den := new(big.Int).SetUint64(slots * 9)
			eff = num.Div(num, den)
		}

		uptimeEvents = append(uptimeEvents, posValidatorUptimeFinalizedEvent{
			Epoch:           epoch,
			Validator:       addr,
			Slots:           slots,
			Missed:          missed,
			UptimeBps:       uptimeBps,
			EffectiveWeight: new(big.Int).Set(eff),
			StakeSnapshot:   new(big.Int).Set(stakeSnap),
			RewardEligible:  true,
			Slashed:         false,
		})
		infos = append(infos, vinfo{addr: addr, weight: eff})
		sumWeights.Add(sumWeights, eff)
		posDiagLogger.Debug("FinalizeEpoch validator weight", "epoch", epoch, "validator", addr, "slots", slots, "missed", missed, "weight", eff)
	}

	// Distribute pooled fees proportional to effective weights.
	// Note: if there are no pooled fees, we still enforce jail/slash deterministically.
	poolBal := new(big.Int).Set(txn.Txn().GetBalance(state.FeePoolAddress))
	posDiagLogger.Debug("FinalizeEpoch pool/weights", "block", header.Number, "epoch", epoch, "poolBal", poolBal, "sumWeights", sumWeights)
	if poolBal.Sign() == 0 || sumWeights.Sign() == 0 {
		posDiagLogger.Debug("FinalizeEpoch no payout", "block", header.Number, "epoch", epoch, "poolZero", poolBal.Sign() == 0, "weightsZero", sumWeights.Sign() == 0)
	}

	type plannedReward struct {
		addr  types.Address
		share *big.Int
		parts validatorRewardSplit
	}

	plans := make([]plannedReward, 0, len(infos))
	if poolBal.Sign() > 0 && sumWeights.Sign() > 0 {
		for _, it := range infos {
			if it.weight.Sign() == 0 {
				continue
			}

			share := new(big.Int).Mul(poolBal, it.weight)
			share.Div(share, sumWeights)
			if share.Sign() == 0 {
				posDiagLogger.Debug("FinalizeEpoch zero share for validator", "epoch", epoch, "validator", it.addr, "weight", it.weight)
				continue
			}

			rewardParts, err := finalizeEpochRewardSplitFn(txn, header.Number-1, epochSize, it.addr, share)
			if err != nil {
				return fmt.Errorf("split validator reward for %s in epoch %d: %w", it.addr, epoch, err)
			}
			if err := validateRewardSplit(epoch, it.addr, share, rewardParts); err != nil {
				return err
			}
			plans = append(plans, plannedReward{
				addr:  it.addr,
				share: share,
				parts: rewardParts,
			})
		}
	}

	rewardEvents := make([]posStakerRewardEvent, 0, len(plans))
	totalDistributed := new(big.Int)
	for _, plan := range plans {
		if err := posTransferFn(txn, state.FeePoolAddress, staking.AddrStakingContract, plan.share); err != nil {
			posDiagLogger.Error("FinalizeEpoch transfer failed", "epoch", epoch, "validator", plan.addr, "share", plan.share, "err", err)
			return fmt.Errorf("transfer epoch reward for validator %s in epoch %d: %w", plan.addr, epoch, err)
		}
		rewardEvent := creditStakeReward(txn, epoch, plan.addr, plan.parts)
		rewardEvents = append(rewardEvents, rewardEvent...)
		totalDistributed.Add(totalDistributed, plan.share)
		posDiagLogger.Debug("FinalizeEpoch payout", "epoch", epoch, "validator", plan.addr, "share", plan.share)
	}

	totalSlashed := new(big.Int)
	for _, ev := range slashEvents {
		totalSlashed.Add(totalSlashed, ev.Amount)
	}
	remainder := new(big.Int).Sub(new(big.Int).Set(poolBal), totalDistributed)
	activeValidatorsAfter := countActiveEpochValidators(txn, epochValidators)
	pruneFinalizedEpochUptimeCounters(txn, epoch, epochValidators)

	appendPosEpochFinalizedLog(txn, epoch, header.Number, poolBal, totalDistributed, remainder, totalSlashed, activeValidatorsAfter)
	for _, ev := range uptimeEvents {
		appendPosValidatorUptimeFinalizedLog(txn, ev)
	}
	for _, ev := range rewardEvents {
		appendPosStakerRewardedLog(txn, ev)
	}
	for _, ev := range slashEvents {
		appendPosStakerSlashedLog(txn, ev)
	}

	posDiagLogger.Debug("FinalizeEpoch complete", "block", header.Number, "epoch", epoch)

	return nil
}

func countActiveEpochValidators(txn *state.Transition, validators []types.Address) uint64 {
	var active uint64
	for _, validator := range validators {
		if readStakerActive(txn, validator) {
			active++
		}
	}

	return active
}

func collectEpochValidatorStakers(txn *state.Transition, validators []types.Address) []types.Address {
	stakers := make([]types.Address, 0, len(validators))
	for _, validator := range validators {
		stakers = append(stakers, validator)
		stakers = append(stakers, readValidatorStakers(txn, validator)...)
	}

	return stakers
}

func resolveDonationAddress(txn *state.Transition) types.Address {
	// Slashed stake is always burned at 0x...0666.
	return chain.DefaultBurnedAddress
}

func getOrInitStakeSnapshot(txn *state.Transition, epoch uint64, addr types.Address) (*big.Int, error) {
	if txn == nil {
		return nil, fmt.Errorf("nil transition")
	}

	key := keyStakeSnapshot(epoch, addr)
	cur := bigFromHash(txn.Txn().GetState(PosSysAddr, key))
	if cur.Sign() != 0 {
		return cur, nil
	}

	blockNumber := uint64(0)
	if txn != nil {
		blockNumber = uint64(txn.GetTxContext().Number)
		if blockNumber > 0 {
			blockNumber--
		}
	}

	stakeAmt := contractstore.ReadValidatorEffectiveTotalStakeAt(txn, addr, blockNumber)
	txn.Txn().SetState(PosSysAddr, key, bigToHash(stakeAmt))
	return stakeAmt, nil
}

func getOrInitStakerStakeSnapshot(txn *state.Transition, epoch uint64, addr types.Address) (*big.Int, error) {
	if txn == nil {
		return nil, fmt.Errorf("nil transition")
	}

	key := keyStakerStakeSnapshot(epoch, addr)
	cur := bigFromHash(txn.Txn().GetState(PosSysAddr, key))
	if cur.Sign() != 0 {
		return cur, nil
	}

	blockNumber := uint64(0)
	if txn != nil {
		blockNumber = uint64(txn.GetTxContext().Number)
		if blockNumber > 0 {
			blockNumber--
		}
	}

	stakeAmt := contractstore.ReadStakerEffectiveStakeAt(txn, addr, blockNumber)
	txn.Txn().SetState(PosSysAddr, key, bigToHash(stakeAmt))

	return stakeAmt, nil
}

type slashPosition struct {
	addr   types.Address
	amount *big.Int
	active bool
}

type slashAllocation struct {
	position slashPosition
	slash    *big.Int
}

func slashByEffectiveStakeFromStakingContract(txn *state.Transition, epoch uint64, validator, donationAddr types.Address, stakeSnap *big.Int, slots, missed, slashBps, blockNumber, epochSize uint64) ([]posStakerSlashEvent, error) {
	if slashBps == 0 {
		posDiagLogger.Debug("slashByEffectiveStakeFromStakingContract skipped: slashBps=0", "epoch", epoch, "validator", validator)
		return nil, nil
	}
	if stakeSnap == nil || stakeSnap.Sign() == 0 {
		posDiagLogger.Debug("slashByEffectiveStakeFromStakingContract skipped: empty stake snapshot", "epoch", epoch, "validator", validator)
		return nil, nil
	}
	if stakeSnap.Cmp(new(big.Int).SetUint64(noSlashStakeFloorWei)) <= 0 {
		posDiagLogger.Debug("slashByEffectiveStakeFromStakingContract skipped: at no-slash floor", "epoch", epoch, "validator", validator, "stakeSnap", stakeSnap, "floorWei", noSlashStakeFloorWei)
		return nil, nil
	}

	totalSlash := new(big.Int).Mul(stakeSnap, new(big.Int).SetUint64(slashBps))
	totalSlash.Div(totalSlash, new(big.Int).SetUint64(10_000))
	if totalSlash.Sign() == 0 {
		posDiagLogger.Debug("slashByEffectiveStakeFromStakingContract skipped: slash amount zero", "epoch", epoch, "validator", validator)
		return nil, nil
	}

	effectiveTotalStake := new(big.Int)
	positions := make([]slashPosition, 0, 1)
	if isStakerEffectiveAt(txn, validator, blockNumber, epochSize) {
		selfStake, err := readStakeWeightAtEpoch(txn, epoch, validator)
		if err != nil {
			return nil, err
		}
		positions = append(positions, slashPosition{addr: validator, amount: selfStake, active: true})
		effectiveTotalStake.Add(effectiveTotalStake, selfStake)
	}
	delegations, err := readValidatorEffectiveDelegationsAt(txn, epoch, validator, blockNumber, epochSize)
	if err != nil {
		return nil, err
	}
	for _, delegation := range delegations {
		positions = append(positions, slashPosition{
			addr:   delegation.Delegator,
			amount: delegation.Amount,
			active: readStakerActive(txn, delegation.Delegator),
		})
		effectiveTotalStake.Add(effectiveTotalStake, delegation.Amount)
	}
	if effectiveTotalStake.Sign() == 0 {
		posDiagLogger.Debug("slashByEffectiveStakeFromStakingContract skipped: effective stake zero", "epoch", epoch, "validator", validator)
		return nil, nil
	}

	if totalSlash.Cmp(effectiveTotalStake) > 0 {
		totalSlash.Set(effectiveTotalStake)
	}

	contractBal := new(big.Int).Set(txn.Txn().GetBalance(staking.AddrStakingContract))
	if contractBal.Sign() == 0 {
		posDiagLogger.Debug("slashByEffectiveStakeFromStakingContract skipped: staking contract balance zero", "epoch", epoch, "validator", validator)
		return nil, nil
	}
	if totalSlash.Cmp(contractBal) > 0 {
		totalSlash.Set(contractBal)
	}
	if totalSlash.Sign() == 0 {
		posDiagLogger.Debug("slashByEffectiveStakeFromStakingContract skipped: slash amount zero after caps", "epoch", epoch, "validator", validator)
		return nil, nil
	}

	remaining := new(big.Int).Set(totalSlash)
	allocations := make([]slashAllocation, 0, len(positions))
	for _, position := range positions {
		part := new(big.Int).Mul(new(big.Int).Set(totalSlash), position.amount)
		part.Div(part, effectiveTotalStake)
		if part.Cmp(position.amount) > 0 {
			return nil, fmt.Errorf("slash amount exceeds stake for %s: have %s, need %s", position.addr, position.amount, part)
		}
		allocations = append(allocations, slashAllocation{
			position: position,
			slash:    part,
		})
		remaining.Sub(remaining, part)
	}

	sort.Slice(allocations, func(i, j int) bool {
		return bytes.Compare(allocations[i].position.addr.Bytes(), allocations[j].position.addr.Bytes()) < 0
	})

	for remaining.Sign() > 0 {
		progress := false
		for i := range allocations {
			if allocations[i].slash.Cmp(allocations[i].position.amount) >= 0 {
				continue
			}
			allocations[i].slash.Add(allocations[i].slash, big.NewInt(1))
			remaining.Sub(remaining, big.NewInt(1))
			progress = true
			if remaining.Sign() == 0 {
				break
			}
		}
		if !progress {
			return nil, fmt.Errorf("unable to distribute slash remainder %s", remaining)
		}
	}

	stakerEvents := make([]posStakerSlashEvent, 0, len(allocations))
	for _, allocation := range allocations {
		if allocation.slash.Sign() == 0 {
			continue
		}
		curStake := new(big.Int).Set(readStakedAmount(txn, allocation.position.addr))
		if allocation.slash.Cmp(curStake) > 0 {
			return nil, fmt.Errorf("slash underflow for %s: have %s, need %s", allocation.position.addr, curStake, allocation.slash)
		}
		newStake := new(big.Int).Sub(curStake, allocation.slash)
		if err := posTransferFn(txn, staking.AddrStakingContract, donationAddr, allocation.slash); err != nil {
			return nil, fmt.Errorf("transfer slash amount: %w", err)
		}
		writeStakedAmount(txn, allocation.position.addr, newStake)
		if allocation.position.addr != validator {
			if err := reduceValidatorDelegatedAggregate(txn, validator, allocation.slash, allocation.position.active); err != nil {
				return nil, err
			}
		}
		role := uint8(2)
		if allocation.position.addr == validator {
			role = 1
		}
		stakerEvents = append(stakerEvents, posStakerSlashEvent{
			Epoch: epoch, Validator: validator, Staker: allocation.position.addr, Role: role,
			Amount: new(big.Int).Set(allocation.slash), StakeSnapshot: new(big.Int).Set(allocation.position.amount),
			StakeBefore: curStake, StakeAfter: newStake, Slots: slots, Missed: missed, SlashBps: slashBps, Destination: donationAddr,
		})
	}

	posDiagLogger.Debug("slashByEffectiveStakeFromStakingContract applied", "epoch", epoch, "validator", validator, "donationAddr", donationAddr, "amount", totalSlash)

	return stakerEvents, nil
}

type validatorRewardSplit struct {
	ValidatorNet                 *big.Int
	Commission                   *big.Int
	DelegatorsNet                *big.Int
	DelegatorsNetAfterCommission *big.Int
	SelfStakeReward              *big.Int
	DelegatorRemainder           *big.Int
	DelegatorParts               []delegationShare
}

type delegationShare struct {
	Delegator types.Address
	Amount    *big.Int
}

func splitValidatorRewardByDelegation(txn *state.Transition, blockNumber, epochSize uint64, validator types.Address, reward *big.Int) (validatorRewardSplit, error) {
	out := validatorRewardSplit{
		ValidatorNet:                 new(big.Int),
		Commission:                   new(big.Int),
		DelegatorsNet:                new(big.Int),
		DelegatorsNetAfterCommission: new(big.Int),
		SelfStakeReward:              new(big.Int),
		DelegatorRemainder:           new(big.Int),
	}
	if reward == nil || reward.Sign() == 0 {
		return out, nil
	}

	epoch := epochOf(blockNumber, epochSize)
	selfStake := new(big.Int)
	if isStakerEffectiveAt(txn, validator, blockNumber, epochSize) {
		var err error
		selfStake, err = readStakeWeightAtEpoch(txn, epoch, validator)
		if err != nil {
			return out, err
		}
	}
	delegations, err := readValidatorEffectiveDelegationsAt(txn, epoch, validator, blockNumber, epochSize)
	if err != nil {
		return out, err
	}
	if len(delegations) == 0 {
		out.ValidatorNet = new(big.Int).Set(reward)
		out.SelfStakeReward = new(big.Int).Set(reward)
		return out, nil
	}

	activeDelegatedTotal := new(big.Int)
	for _, delegation := range delegations {
		activeDelegatedTotal.Add(activeDelegatedTotal, delegation.Amount)
	}
	if activeDelegatedTotal.Sign() == 0 {
		out.ValidatorNet = new(big.Int).Set(reward)
		out.SelfStakeReward = new(big.Int).Set(reward)
		return out, nil
	}

	totalActiveStake := new(big.Int).Add(new(big.Int).Set(selfStake), activeDelegatedTotal)
	if totalActiveStake.Sign() == 0 {
		return out, nil
	}

	selfShare := new(big.Int).Mul(new(big.Int).Set(reward), selfStake)
	selfShare.Div(selfShare, totalActiveStake)
	delegatedShare := new(big.Int).Sub(new(big.Int).Set(reward), selfShare)

	commissionBps := uint64(readValidatorCommissionBps(txn, validator))
	commission := new(big.Int).Mul(new(big.Int).Set(delegatedShare), new(big.Int).SetUint64(commissionBps))
	commission.Div(commission, new(big.Int).SetUint64(10_000))
	delegatorsNet := new(big.Int).Sub(new(big.Int).Set(delegatedShare), commission)

	allocated := new(big.Int)
	out.DelegatorParts = make([]delegationShare, 0, len(delegations))
	for _, delegation := range delegations {
		part := new(big.Int).Mul(new(big.Int).Set(delegatorsNet), delegation.Amount)
		part.Div(part, activeDelegatedTotal)
		if part.Sign() == 0 {
			continue
		}
		allocated.Add(allocated, part)
		out.DelegatorParts = append(out.DelegatorParts, delegationShare{
			Delegator: delegation.Delegator,
			Amount:    part,
		})
	}

	delegatorRemainder := new(big.Int).Sub(new(big.Int).Set(delegatorsNet), allocated)
	out.Commission.Set(commission)
	out.DelegatorsNet.Set(allocated)
	out.DelegatorsNetAfterCommission.Set(delegatorsNet)
	out.SelfStakeReward.Set(selfShare)
	out.DelegatorRemainder.Set(delegatorRemainder)
	out.ValidatorNet.Add(selfShare, commission)
	out.ValidatorNet.Add(out.ValidatorNet, delegatorRemainder)

	return out, nil
}

func readValidatorEffectiveDelegationsAt(txn *state.Transition, epoch uint64, validator types.Address, _ uint64, _ uint64) ([]delegationShare, error) {
	if txn == nil {
		return nil, nil
	}
	stakers := readValidatorStakers(txn, validator)
	out := make([]delegationShare, 0, len(stakers))
	for _, owner := range stakers {
		if owner == validator {
			continue
		}
		amount := bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakerStakeSnapshot(epoch, owner)))
		if amount.Sign() == 0 {
			continue
		}
		out = append(out, delegationShare{Delegator: owner, Amount: amount})
	}

	return out, nil
}

func readStakeWeightAtEpoch(txn *state.Transition, epoch uint64, addr types.Address) (*big.Int, error) {
	if epoch == 0 {
		return nil, fmt.Errorf("invalid epoch for staker snapshot %s", addr)
	}

	snap := bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakerStakeSnapshot(epoch, addr)))
	if snap.Sign() == 0 {
		return nil, fmt.Errorf("missing staker snapshot for epoch %d staker %s", epoch, addr)
	}

	return snap, nil
}

func readValidatorCommissionBps(txn *state.Transition, validator types.Address) uint16 {
	base := crypto.Keccak256(
		common.PadLeftOrTrim(validator.Bytes(), 32),
		common.PadLeftOrTrim(big.NewInt(5).Bytes(), 32),
	)
	idx := new(big.Int).Add(new(big.Int).SetBytes(base), big.NewInt(3))
	raw := txn.Txn().GetState(staking.AddrStakingContract, types.BytesToHash(idx.Bytes()))
	v := new(big.Int).SetBytes(raw[:]).Uint64()
	if v > 10_000 {
		return 10_000
	}

	return uint16(v)
}

func readValidatorStakers(txn *state.Transition, validator types.Address) []types.Address {
	base := crypto.Keccak256(
		common.PadLeftOrTrim(validator.Bytes(), 32),
		common.PadLeftOrTrim(big.NewInt(6).Bytes(), 32),
	)
	lengthRaw := txn.Txn().GetState(staking.AddrStakingContract, types.BytesToHash(base))
	length := new(big.Int).SetBytes(lengthRaw[:]).Uint64()
	if length == 0 {
		return nil
	}
	dataBase := crypto.Keccak256(common.PadLeftOrTrim(base, 32))
	idx := new(big.Int).SetBytes(dataBase)
	out := make([]types.Address, 0, length)
	for i := uint64(0); i < length; i++ {
		slot := new(big.Int).Add(idx, new(big.Int).SetUint64(i))
		raw := txn.Txn().GetState(staking.AddrStakingContract, types.BytesToHash(slot.Bytes()))
		out = append(out, types.BytesToAddress(raw[12:]))
	}

	return out
}

func readStakerJoinedAt(txn *state.Transition, addr types.Address) uint64 {
	raw := txn.Txn().GetState(staking.AddrStakingContract, stakerSlotWithOffset(addr, 2))
	return new(big.Int).SetBytes(raw[:]).Uint64()
}

func readStakerDeactivatedAt(txn *state.Transition, addr types.Address) uint64 {
	raw := txn.Txn().GetState(staking.AddrStakingContract, stakerSlotWithOffset(addr, 3))
	return new(big.Int).SetBytes(raw[:]).Uint64()
}

func readStakerActive(txn *state.Transition, addr types.Address) bool {
	raw := txn.Txn().GetState(staking.AddrStakingContract, stakerSlotWithOffset(addr, 0))
	packed := new(big.Int).SetBytes(raw[:])
	if packed.Sign() == 0 {
		return false
	}

	activeByte := new(big.Int).Rsh(packed, 8)
	activeByte.And(activeByte, new(big.Int).SetUint64(0xff))

	return activeByte.Sign() != 0
}

func isStakerEffectiveAt(txn *state.Transition, addr types.Address, blockNumber, epochSize uint64) bool {
	joined := readStakerJoinedAt(txn, addr)
	if joined == 0 || epochSize == 0 {
		return false
	}
	if blockNumber/epochSize <= joined/epochSize {
		return false
	}
	deactivated := readStakerDeactivatedAt(txn, addr)
	if deactivated == 0 {
		return true
	}

	return blockNumber/epochSize <= deactivated/epochSize
}

func nonNilBig(v *big.Int) *big.Int {
	if v == nil {
		return new(big.Int)
	}

	return v
}

func validateRewardSplit(epoch uint64, addr types.Address, totalReward *big.Int, split validatorRewardSplit) error {
	totalReward = nonNilBig(totalReward)
	split.ValidatorNet = nonNilBig(split.ValidatorNet)
	split.Commission = nonNilBig(split.Commission)
	split.DelegatorsNetAfterCommission = nonNilBig(split.DelegatorsNetAfterCommission)
	split.SelfStakeReward = nonNilBig(split.SelfStakeReward)
	split.DelegatorRemainder = nonNilBig(split.DelegatorRemainder)

	delegatorsPaid := new(big.Int)
	for _, part := range split.DelegatorParts {
		if part.Amount != nil {
			delegatorsPaid.Add(delegatorsPaid, part.Amount)
		}
	}
	delegatorsNetAfterCommission := new(big.Int).Set(split.DelegatorsNetAfterCommission)
	if delegatorsNetAfterCommission.Sign() == 0 {
		delegatorsNetAfterCommission.Add(delegatorsPaid, split.DelegatorRemainder)
	}
	if new(big.Int).Sub(delegatorsNetAfterCommission, delegatorsPaid).Sign() < 0 {
		return fmt.Errorf("negative delegator remainder for validator %s epoch %d", addr, epoch)
	}
	if split.SelfStakeReward.Sign() < 0 {
		return fmt.Errorf("negative self stake reward for validator %s epoch %d", addr, epoch)
	}
	validatorNetCheck := new(big.Int).Add(new(big.Int).Set(split.SelfStakeReward), split.Commission)
	validatorNetCheck.Add(validatorNetCheck, split.DelegatorRemainder)
	if validatorNetCheck.Cmp(split.ValidatorNet) != 0 {
		return fmt.Errorf("validator reward split mismatch for validator %s epoch %d: self+commission+remainder=%s validatorNet=%s", addr, epoch, validatorNetCheck, split.ValidatorNet)
	}
	shareCheck := new(big.Int).Add(new(big.Int).Set(split.ValidatorNet), delegatorsPaid)
	if shareCheck.Cmp(totalReward) != 0 {
		return fmt.Errorf("validator total reward mismatch for validator %s epoch %d: credited+delegators=%s total=%s", addr, epoch, shareCheck, totalReward)
	}

	return nil
}

func creditStakeReward(txn *state.Transition, epoch uint64, addr types.Address, split validatorRewardSplit) []posStakerRewardEvent {
	split.ValidatorNet = nonNilBig(split.ValidatorNet)
	// Historical reward state is consensus-observable through deterministic PosSysAddr logs,
	// not permanent PosSysAddr storage. stakeSnapshot and stakeAfter let explorers prove
	// eligibility and reconstruct the post-operation personal stake.
	rewardEvents := make([]posStakerRewardEvent, 0, 1+len(split.DelegatorParts))

	if split.ValidatorNet.Sign() > 0 {
		curStake := new(big.Int).Set(readStakedAmount(txn, addr))
		newStake := new(big.Int).Add(new(big.Int).Set(curStake), split.ValidatorNet)
		writeStakedAmount(txn, addr, newStake)
		stakeSnapshot := bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakerStakeSnapshot(epoch, addr)))
		rewardEvents = append(rewardEvents, posStakerRewardEvent{
			Epoch: epoch, Validator: addr, Staker: addr, Role: 1,
			Amount: new(big.Int).Set(split.ValidatorNet), StakeSnapshot: stakeSnapshot,
			StakeBefore: curStake, StakeAfter: newStake,
		})
	}

	for _, part := range split.DelegatorParts {
		if part.Amount == nil || part.Amount.Sign() == 0 {
			continue
		}

		curDelegatorStake := new(big.Int).Set(readStakedAmount(txn, part.Delegator))
		newDelegatorStake := new(big.Int).Add(new(big.Int).Set(curDelegatorStake), part.Amount)
		writeStakedAmount(txn, part.Delegator, newDelegatorStake)
		// Reward eligibility uses the epoch snapshot, while raw/active pool updates use
		// current active status so future voting power reflects current live state.
		addValidatorDelegatedAggregate(txn, addr, part.Amount, readStakerActive(txn, part.Delegator))
		stakeSnapshot := bigFromHash(txn.Txn().GetState(PosSysAddr, keyStakerStakeSnapshot(epoch, part.Delegator)))
		rewardEvents = append(rewardEvents, posStakerRewardEvent{
			Epoch: epoch, Validator: addr, Staker: part.Delegator, Role: 2,
			Amount: new(big.Int).Set(part.Amount), StakeSnapshot: stakeSnapshot,
			StakeBefore: curDelegatorStake, StakeAfter: newDelegatorStake,
		})
	}

	return rewardEvents
}

func pruneFinalizedEpochUptimeCounters(txn *state.Transition, epoch uint64, epochValidators []types.Address) {
	if txn == nil || epoch == 0 || len(epochValidators) == 0 {
		return
	}
	zero := types.ZeroHash
	for _, validator := range epochValidators {
		txn.Txn().SetState(PosSysAddr, keyProposerSlots(epoch, validator), zero)
		txn.Txn().SetState(PosSysAddr, keyProposerMissed(epoch, validator), zero)
	}
}

func readStakedAmount(txn *state.Transition, addr types.Address) *big.Int {
	key := stakingAmountKey(addr)
	h := txn.Txn().GetState(staking.AddrStakingContract, key)
	return new(big.Int).SetBytes(h[:])
}

func writeStakedAmount(txn *state.Transition, addr types.Address, v *big.Int) {
	key := stakingAmountKey(addr)
	txn.Txn().SetState(staking.AddrStakingContract, key, bigToHash(v))
}

func addValidatorDelegatedAggregate(txn *state.Transition, validator types.Address, amount *big.Int, active bool) {
	if amount == nil || amount.Sign() == 0 {
		return
	}

	// StakingV2 storage layout:
	// slot 8: _validatorDelegatedStakeRaw
	// slot 9: _validatorDelegatedStakeActive
	for _, slot := range []int64{8, 9} {
		if slot == 9 && !active {
			continue
		}
		base := crypto.Keccak256(
			common.PadLeftOrTrim(validator.Bytes(), 32),
			common.PadLeftOrTrim(big.NewInt(slot).Bytes(), 32),
		)
		key := types.BytesToHash(base)
		raw := txn.Txn().GetState(staking.AddrStakingContract, key)
		cur := new(big.Int).SetBytes(raw[:])
		cur.Add(cur, amount)
		txn.Txn().SetState(staking.AddrStakingContract, key, bigToHash(cur))
	}
}

func reduceValidatorDelegatedAggregate(txn *state.Transition, validator types.Address, amount *big.Int, active bool) error {
	if amount == nil || amount.Sign() == 0 {
		return nil
	}

	// StakingV2 storage layout:
	// slot 8: _validatorDelegatedStakeRaw
	// slot 9: _validatorDelegatedStakeActive
	for _, slot := range []int64{8, 9} {
		if slot == 9 && !active {
			continue
		}
		base := crypto.Keccak256(
			common.PadLeftOrTrim(validator.Bytes(), 32),
			common.PadLeftOrTrim(big.NewInt(slot).Bytes(), 32),
		)
		key := types.BytesToHash(base)
		raw := txn.Txn().GetState(staking.AddrStakingContract, key)
		cur := new(big.Int).SetBytes(raw[:])
		if amount.Cmp(cur) > 0 {
			return fmt.Errorf("delegated aggregate underflow for validator %s slot %d: have %s, need %s", validator, slot, cur, amount)
		}
		cur.Sub(cur, amount)
		txn.Txn().SetState(staking.AddrStakingContract, key, bigToHash(cur))
	}

	return nil
}

func stakerSlotWithOffset(addr types.Address, offset int64) types.Hash {
	base := crypto.Keccak256(
		common.PadLeftOrTrim(addr.Bytes(), 32),
		common.PadLeftOrTrim(big.NewInt(3).Bytes(), 32),
	)
	idx := new(big.Int).SetBytes(base)
	idx.Add(idx, big.NewInt(offset))

	return types.BytesToHash(idx.Bytes())
}

func setValidatorInactiveInStaking(txn *state.Transition, addr types.Address, blockNum uint64) {
	packedKey := stakingMetaPackedKey(addr)
	packedState := txn.Txn().GetState(staking.AddrStakingContract, packedKey)
	packedVal := new(big.Int).SetBytes(packedState[:])
	if packedVal.Sign() == 0 {
		return
	}
	if !isValidatorMetaActive(packedVal) {
		return
	}

	// ValidatorMeta packed slot layout:
	// byte0: exists, byte1: active, bytes2..9: index
	activeMask := new(big.Int).SetUint64(0xff)
	activeMask.Lsh(activeMask, 8)
	packedVal.AndNot(packedVal, activeMask)
	txn.Txn().SetState(staking.AddrStakingContract, packedKey, bigToHash(packedVal))

	poolKey := stakingPoolConfigPackedKey(addr)
	poolState := txn.Txn().GetState(staking.AddrStakingContract, poolKey)
	poolVal := new(big.Int).SetBytes(poolState[:])
	if poolVal.Sign() != 0 {
		poolVal.AndNot(poolVal, activeMask)
		txn.Txn().SetState(staking.AddrStakingContract, poolKey, bigToHash(poolVal))
	}

	txn.Txn().SetState(staking.AddrStakingContract, stakingDeactivatedAtBlockKey(addr), bigToHash(new(big.Int).SetUint64(blockNum)))
}

func isValidatorMetaActive(packedVal *big.Int) bool {
	if packedVal == nil || packedVal.Sign() == 0 {
		return false
	}

	activeByte := new(big.Int).Rsh(new(big.Int).Set(packedVal), 8)
	activeByte.And(activeByte, new(big.Int).SetUint64(0xff))

	return activeByte.Sign() != 0
}

func stakingMetaPackedKey(addr types.Address) types.Hash {
	base := stakingMetaBase(addr)
	return types.BytesToHash(base.Bytes())
}

func stakingPoolConfigPackedKey(addr types.Address) types.Hash {
	base := crypto.Keccak256(
		common.PadLeftOrTrim(addr.Bytes(), 32),
		common.PadLeftOrTrim(big.NewInt(5).Bytes(), 32),
	)

	return types.BytesToHash(base)
}

func stakingDeactivatedAtBlockKey(addr types.Address) types.Hash {
	base := stakingMetaBase(addr)
	base.Add(base, big.NewInt(3))
	return types.BytesToHash(base.Bytes())
}

func stakingMetaBase(addr types.Address) *big.Int {
	var a [32]byte
	copy(a[12:], addr.Bytes())

	var slot [32]byte
	binary.BigEndian.PutUint64(slot[24:], 3)

	return new(big.Int).SetBytes(crypto.Keccak256(a[:], slot[:]))
}

func stakingAmountKey(addr types.Address) types.Hash {
	// StakingV2 storage layout:
	// slot 0: VALIDATOR_THRESHOLD
	// slot 1: epochSize/minNumValidators/maxNumValidators (packed)
	// slot 2: _validators dynamic array
	// slot 3: mapping(address => Staker) _staker
	//
	// Staker.amount is the second storage slot of the struct => base + 1.
	base := stakingMetaBase(addr)
	base.Add(base, big.NewInt(1))

	return types.BytesToHash(base.Bytes())
}
