package jsonrpc

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"

	"github.com/umbracle/ethgo"
	"github.com/xgr-network/xgr-node/chain"
	ibftFork "github.com/xgr-network/xgr-node/consensus/ibft/fork"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/contracts/abis"
	stakingHelper "github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/state/runtime"
	"github.com/xgr-network/xgr-node/types"
	validatorTypes "github.com/xgr-network/xgr-node/validators"
)

type stakingQueryRuntime struct {
	store      ethStore
	header     *types.Header
	nonPayable bool
}

func (s *stakingQueryRuntime) Apply(tx *types.Transaction) (*runtime.ExecutionResult, error) {
	return s.store.ApplyTxn(s.header, tx, nil, s.nonPayable)
}

func (s *stakingQueryRuntime) GetNonce(addr types.Address) uint64 {
	return s.store.GetNonce(addr)
}

func (s *stakingQueryRuntime) SetNonPayable(nonPayable bool) {
	s.nonPayable = nonPayable
}

type posValidatorOverview struct {
	Address                          types.Address `json:"address"`
	JoinedAtBlock                    *argUint64    `json:"joinedAtBlock,omitempty"`
	JoinEffectiveAtBlock             *argUint64    `json:"joinEffectiveAtBlock,omitempty"`
	CurrentStake                     *argBig       `json:"currentStake"`
	SelfStake                        *argBig       `json:"selfStake,omitempty"`
	DelegatedRawStake                *argBig       `json:"delegatedRawStake,omitempty"`
	DelegatedActiveStake             *argBig       `json:"delegatedActiveStake,omitempty"`
	TotalActiveCurrentStake          *argBig       `json:"totalActiveCurrentStake,omitempty"`
	CurrentlyValidating              bool          `json:"currentlyValidating"`
	StakingActive                    bool          `json:"stakingActive"`
	DeactivatedAtBlock               *argUint64    `json:"deactivatedAtBlock,omitempty"`
	DeactivateEffectiveAtBlock       *argUint64    `json:"deactivateEffectiveAtBlock,omitempty"`
	UnstakeAvailableAtBlock          *argUint64    `json:"unstakeAvailableAtBlock,omitempty"`
	CanUnstakeNow                    bool          `json:"canUnstakeNow"`
	WasValidatorLastEpoch            bool          `json:"wasValidatorLastEpoch"`
	ReportedEpochReward              *argBig       `json:"reportedEpochReward,omitempty"`
	ReportedEpochRewardValidatorNet  *argBig       `json:"reportedEpochRewardValidatorNet,omitempty"`
	ReportedEpochRewardCommission    *argBig       `json:"reportedEpochRewardCommission,omitempty"`
	ReportedEpochRewardDelegatorsNet *argBig       `json:"reportedEpochRewardDelegatorsNet,omitempty"`

	RewardIneligible     *bool      `json:"rewardIneligible,omitempty"`
	Slashed              *bool      `json:"slashed,omitempty"`
	MicroNominalWeight   *argUint64 `json:"microNominalWeight,omitempty"`
	MicroEffectiveWeight *argUint64 `json:"microEffectiveWeight,omitempty"`
	MicroInactivity      *argUint64 `json:"microInactivity,omitempty"`

	ProposalUptimeLast3EpochsBps       *argUint64 `json:"proposalUptimeLast3EpochsBps,omitempty"`
	ProposalUptimeLast10EpochsBps      *argUint64 `json:"proposalUptimeLast10EpochsBps,omitempty"`
	ProposalUptimeLast3EpochsPercent   *argUint64 `json:"proposalUptimeLast3EpochsPercent,omitempty"`
	ProposalUptimeLast10EpochsPercent  *argUint64 `json:"proposalUptimeLast10EpochsPercent,omitempty"`
	ProposalUptimeLast3EpochsObserved  argUint64  `json:"proposalUptimeLast3EpochsObserved"`
	ProposalUptimeLast10EpochsObserved argUint64  `json:"proposalUptimeLast10EpochsObserved"`
}

type posOverviewResponse struct {
	BlockNumber                 argUint64  `json:"blockNumber"`
	EpochSize                   argUint64  `json:"epochSize"`
	MicroEpochSize              argUint64  `json:"microEpochSize"`
	CurrentMicroEpoch           argUint64  `json:"currentMicroEpoch"`
	CurrentMicroEpochStartBlock argUint64  `json:"currentMicroEpochStartBlock"`
	CurrentMicroEpochEndBlock   argUint64  `json:"currentMicroEpochEndBlock"`
	CurrentEpoch                argUint64  `json:"currentEpoch"`
	LastFinalizedEpoch          argUint64  `json:"lastFinalizedEpoch"`
	ReportedEpoch               argUint64  `json:"reportedEpoch"`
	ReportedEpochStartBlock     argUint64  `json:"reportedEpochStartBlock"`
	ReportedEpochEndBlock       argUint64  `json:"reportedEpochEndBlock"`
	CurrentEpochPendingRewards  *argBig    `json:"currentEpochPendingRewards"`
	StakingContractBalance      *argBig    `json:"stakingContractBalance"`
	MinimumNumValidators        argUint64  `json:"minimumNumValidators"`
	MaximumNumValidators        argUint64  `json:"maximumNumValidators"`
	ValidatorThreshold          *argBig    `json:"validatorThreshold"`
	TotalCurrentStake           *argBig    `json:"totalCurrentStake"`
	TotalValidatorSelfStake     *argBig    `json:"totalValidatorSelfStake"`
	TotalDelegatedRawStake      *argBig    `json:"totalDelegatedRawStake"`
	TotalDelegatedActiveStake   *argBig    `json:"totalDelegatedActiveStake"`
	TotalActiveCurrentStake     *argBig    `json:"totalActiveCurrentStake"`
	RewardIneligibleCount       *argUint64 `json:"rewardIneligibleCount,omitempty"`
	SlashedCount                *argUint64 `json:"slashedCount,omitempty"`

	RewardIneligibleStatusExact bool    `json:"rewardIneligibleStatusExact"`
	SlashStatusExact            bool    `json:"slashStatusExact"`
	LastRoundStakeExact         bool    `json:"lastRoundStakeExact"`
	LastRoundDistributedStake   *argBig `json:"lastRoundDistributedStake,omitempty"`

	MonitoringNotes []string               `json:"monitoringNotes"`
	Validators      []posValidatorOverview `json:"validators"`
	PoSActive       bool                   `json:"posActive"`
	PoSFromBlock    *argUint64             `json:"posFromBlock,omitempty"`
}

type posDelegatorEntry struct {
	Delegator            types.Address `json:"delegator"`
	Amount               *argBig       `json:"amount"`
	EpochEffectiveAmount *argBig       `json:"epochEffectiveAmount,omitempty"`
	ReportedEpochReward  *argBig       `json:"reportedEpochReward,omitempty"`
	Active               bool          `json:"active"`
	JoinedAtBlock        *argUint64    `json:"joinedAtBlock,omitempty"`
	DeactivatedAtBlock   *argUint64    `json:"deactivatedAtBlock,omitempty"`
	EffectiveAtPoint     bool          `json:"effectiveAtPoint"`
}

type posValidatorDelegatorsResponse struct {
	Validator                  types.Address       `json:"validator"`
	SelfStake                  *argBig             `json:"selfStake"`
	DelegatedRaw               *argBig             `json:"delegatedRaw"`
	DelegatedActive            *argBig             `json:"delegatedActive"`
	DelegatedActiveCurrent     *argBig             `json:"delegatedActiveCurrent"`
	TotalActiveCurrentStake    *argBig             `json:"totalActiveCurrentStake"`
	SelfStakeLive              *argBig             `json:"selfStakeLive,omitempty"`
	DelegatedLiveRaw           *argBig             `json:"delegatedLiveRaw,omitempty"`
	DelegatedLiveActive        *argBig             `json:"delegatedLiveActive,omitempty"`
	TotalLiveStake             *argBig             `json:"totalLiveStake,omitempty"`
	SelfStakeEpochEffective    *argBig             `json:"selfStakeEpochEffective,omitempty"`
	DelegatedEpochEffective    *argBig             `json:"delegatedEpochEffective,omitempty"`
	TotalEpochEffectiveStake   *argBig             `json:"totalEpochEffectiveStake,omitempty"`
	DelegationEnabled          bool                `json:"delegationEnabled"`
	MaxTotalDelegatedStake     *argBig             `json:"maxTotalDelegatedStake"`
	MinDelegatorStake          *argBig             `json:"minDelegatorStake"`
	EffectiveMinDelegatorStake *argBig             `json:"effectiveMinDelegatorStake"`
	CommissionBps              argUint64           `json:"commissionBps"`
	Delegators                 []posDelegatorEntry `json:"delegators"`
}

// GetPosValidatorsOverview returns a deterministic PoS validator overview.
//
// Important: reward eligibility/slash and epoch reward distribution are not exposed directly by
// the current staking ABI, so this endpoint does NOT fabricate these values.
func (e *Eth) GetPosValidatorsOverview(reportEpoch *string) (interface{}, error) {
	head := e.store.Header()
	if head == nil {
		return nil, fmt.Errorf("header has a nil value")
	}

	epochSize, err := resolveIBFTEpochSize(e.store.GetChainParams())
	if err != nil {
		return nil, err
	}
	posFrom, hasPoS := resolvePoSFromBlock(e.store.GetChainParams())

	currentEpoch := epochOf(head.Number, epochSize)
	lastFinalizedEpoch := head.Number / epochSize
	reportedEpoch := currentEpoch
	if reportEpoch != nil {
		switch *reportEpoch {
		case "current":
			reportedEpoch = currentEpoch
		case "lastFinalized":
			reportedEpoch = lastFinalizedEpoch
		default:
			return nil, fmt.Errorf("invalid reportEpoch %q (expected \"current\" or \"lastFinalized\")", *reportEpoch)
		}
	}
	if hasPoS && head.Number < posFrom {
		return nil, fmt.Errorf("PoS is not active yet (activates at block %d)", posFrom)
	}

	reportedEpochStart := uint64(0)
	reportedEpochEnd := uint64(0)
	if reportedEpoch > 0 {
		reportedEpochStart = (reportedEpoch-1)*epochSize + 1
		reportedEpochEnd = reportedEpoch * epochSize
		if reportedEpoch == currentEpoch {
			reportedEpochEnd = head.Number
		}
	}

	runtimeAdapter := &stakingQueryRuntime{store: e.store, header: head}

	validators, err := stakingHelper.QueryValidators(runtimeAdapter, types.ZeroAddress)
	if err != nil {
		return nil, err
	}

	minV, err := stakingHelper.QueryMinimumNumValidators(runtimeAdapter, types.ZeroAddress)
	if err != nil {
		return nil, err
	}
	maxV, err := stakingHelper.QueryMaximumNumValidators(runtimeAdapter, types.ZeroAddress)
	if err != nil {
		return nil, err
	}
	threshold, err := stakingHelper.QueryValidatorThreshold(runtimeAdapter, types.ZeroAddress)
	if err != nil {
		return nil, err
	}

	infos := make(map[types.Address]*posValidatorOverview, len(validators))
	totalCurrentStake := big.NewInt(0)
	totalValidatorSelfStake := big.NewInt(0)
	totalDelegatedRawStake := big.NewInt(0)
	totalDelegatedActiveStake := big.NewInt(0)
	totalActiveCurrentStake := big.NewInt(0)
	contractEpochNow := uint64(0)
	currentEpochSnapshot := readValidatorsFromConsensusHeader(e, head.Number)
	lastFinalizedEpochEnd := uint64(0)
	if lastFinalizedEpoch > 0 {
		lastFinalizedEpochEnd = lastFinalizedEpoch * epochSize
		if lastFinalizedEpoch == currentEpoch {
			lastFinalizedEpochEnd = head.Number
		}
	}
	lastFinalizedSnapshot := readValidatorsForEpoch(e, head.StateRoot, lastFinalizedEpoch, lastFinalizedEpochEnd)
	if len(lastFinalizedSnapshot) == 0 && lastFinalizedEpoch > 0 && epochSize > 0 {
		lastFinalizedSnapshot = readValidatorsFromConsensusHeader(e, lastFinalizedEpoch*epochSize-1)
	}
	if epochSize > 0 {
		contractEpochNow = head.Number / epochSize
	}

	for _, addr := range validators {
		stakeAmt, stakeErr := stakingHelper.QueryAccountStake(runtimeAdapter, types.ZeroAddress, addr)
		if stakeErr != nil {
			return nil, stakeErr
		}
		totalCurrentStake.Add(totalCurrentStake, stakeAmt)

		delegatedRaw, err := callStakeUintMethod(runtimeAdapter, "validatorDelegatedStakeRaw", addr)
		if err != nil {
			return nil, err
		}
		delegatedActive, err := callStakeUintMethod(runtimeAdapter, "validatorDelegatedStakeActive", addr)
		if err != nil {
			return nil, err
		}
		totalActive := new(big.Int).Add(new(big.Int).Set(stakeAmt), delegatedActive)

		totalValidatorSelfStake.Add(totalValidatorSelfStake, stakeAmt)
		totalDelegatedRawStake.Add(totalDelegatedRawStake, delegatedRaw)
		totalDelegatedActiveStake.Add(totalDelegatedActiveStake, delegatedActive)
		totalActiveCurrentStake.Add(totalActiveCurrentStake, totalActive)

		entry := &posValidatorOverview{
			Address:                 addr,
			CurrentStake:            argBigPtr(new(big.Int).Set(stakeAmt)),
			SelfStake:               argBigPtr(new(big.Int).Set(stakeAmt)),
			DelegatedRawStake:       argBigPtr(new(big.Int).Set(delegatedRaw)),
			DelegatedActiveStake:    argBigPtr(new(big.Int).Set(delegatedActive)),
			TotalActiveCurrentStake: argBigPtr(totalActive),
		}
		if joinedAt, jErr := queryJoinedAtBlockRuntime(runtimeAdapter, addr); jErr == nil && joinedAt > 0 {
			entry.JoinedAtBlock = argUintPtr(joinedAt)
			if epochSize > 0 {
				joinEpoch := joinedAt / epochSize
				entry.JoinEffectiveAtBlock = argUintPtr((joinEpoch + 1) * epochSize)
			}
		}

		if vinfo, vErr := stakingHelper.QueryValidatorInfo(runtimeAdapter, types.ZeroAddress, addr); vErr == nil && vinfo != nil {
			entry.StakingActive = vinfo.Active

			deactBlock := uint64(0)
			if vinfo.DeactivatedAtBlock != nil && vinfo.DeactivatedAtBlock.Sign() > 0 {
				deactBlock = vinfo.DeactivatedAtBlock.Uint64()
				entry.DeactivatedAtBlock = argUintPtr(deactBlock)

				if epochSize > 0 {
					deactEpoch := deactBlock / epochSize
					entry.UnstakeAvailableAtBlock = argUintPtr((deactEpoch + 1) * epochSize)
					entry.CanUnstakeNow = !vinfo.Active && contractEpochNow > deactEpoch
				}
			}

			entry.CurrentlyValidating = false
			if deactBlock > 0 {
				if epochSize > 0 {
					deactEpoch := deactBlock / epochSize
					entry.DeactivateEffectiveAtBlock = argUintPtr((deactEpoch + 1) * epochSize)
				}
			}
		} else {
			entry.CurrentlyValidating = false
		}

		// Finalized reward/slash history is emitted as PosSysAddr system logs, not retained
		// in PosSysAddr storage. RPC exposes live staking state only.
		last3 := computeProposalWindowUptimeMetrics(e, head.StateRoot, entry, reportedEpoch, epochSize, 3)
		entry.ProposalUptimeLast3EpochsObserved = argUint64(last3.observedSlots)
		if last3.uptimeBps != nil {
			entry.ProposalUptimeLast3EpochsBps = argUintPtr(*last3.uptimeBps)
			pct := *last3.uptimeBps / 100
			entry.ProposalUptimeLast3EpochsPercent = argUintPtr(pct)
		}
		last10 := computeProposalWindowUptimeMetrics(e, head.StateRoot, entry, reportedEpoch, epochSize, 10)
		entry.ProposalUptimeLast10EpochsObserved = argUint64(last10.observedSlots)
		if last10.uptimeBps != nil {
			entry.ProposalUptimeLast10EpochsBps = argUintPtr(*last10.uptimeBps)
			pct := *last10.uptimeBps / 100
			entry.ProposalUptimeLast10EpochsPercent = argUintPtr(pct)
		}
		_, entry.WasValidatorLastEpoch = lastFinalizedSnapshot[addr]
		nomW := readBigFromPosSys(e, head.StateRoot, keyUptimeNominalWeight(addr)).Uint64()
		effW := readBigFromPosSys(e, head.StateRoot, keyUptimeEffectiveWeight(addr)).Uint64()
		inact := readBigFromPosSys(e, head.StateRoot, keyUptimeInactivity(addr)).Uint64()
		if nomW > 0 || effW > 0 || inact > 0 {
			entry.MicroNominalWeight = argUintPtr(nomW)
			entry.MicroEffectiveWeight = argUintPtr(effW)
			entry.MicroInactivity = argUintPtr(inact)
		}

		infos[addr] = entry
	}

	// Source-of-truth: current consensus header validator snapshot only (no fallback heuristics).
	for addr, entry := range infos {
		_, entry.CurrentlyValidating = currentEpochSnapshot[addr]

	}

	reportedEpochValidators := readValidatorsForEpoch(e, head.StateRoot, reportedEpoch, reportedEpochEnd)
	if len(reportedEpochValidators) == 0 {
		switch {
		case reportedEpoch == currentEpoch:
			reportedEpochValidators = currentEpochSnapshot
		case reportedEpoch > 0 && epochSize > 0:
			reportedEpochValidators = readValidatorsFromConsensusHeader(e, reportedEpoch*epochSize-1)
		}
	}
	currentEpochPendingRewards := new(big.Int)
	if feePoolAcc, accErr := e.store.GetAccount(head.StateRoot, state.FeePoolAddress); accErr == nil && feePoolAcc != nil && feePoolAcc.Balance != nil {
		currentEpochPendingRewards = new(big.Int).Set(feePoolAcc.Balance)
	}
	stakingContractBalance := new(big.Int)
	if stakingAcc, accErr := e.store.GetAccount(head.StateRoot, stakingHelper.AddrStakingContract); accErr == nil && stakingAcc != nil && stakingAcc.Balance != nil {
		stakingContractBalance = new(big.Int).Set(stakingAcc.Balance)
	}

	validatorList := make([]posValidatorOverview, 0, len(infos))
	var rewardIneligibleCount uint64
	var slashedCount uint64
	for _, entry := range infos {
		_, wasValidatorInReportedEpoch := reportedEpochValidators[entry.Address]

		rewardIneligible := false
		if wasValidatorInReportedEpoch {
			rewardIneligible = isValidatorRewardIneligibleInEpoch(e, head.StateRoot, entry.Address, reportedEpoch)
		}

		slashed := false

		entry.RewardIneligible = &rewardIneligible
		entry.Slashed = &slashed
		if rewardIneligible {
			rewardIneligibleCount++
		}
		if slashed {
			slashedCount++
		}

		validatorList = append(validatorList, *entry)
	}
	sort.Slice(validatorList, func(i, j int) bool {
		return validatorList[i].Address.String() < validatorList[j].Address.String()
	})

	lastRoundDistributedStake := new(big.Int)

	cfg := readMicroEpochConfigFromChain(e.store.GetChainParams().Engine)
	microEpochSize := cfg.microEpochSize
	validatorCount := uint64(len(currentEpochSnapshot))
	if validatorCount == 0 {
		validatorCount = uint64(len(validatorList))
	}
	if microEpochSize > 0 && validatorCount > 0 && microEpochSize < validatorCount {
		microEpochSize = 0
	}
	currentMicroEpoch := uint64(0)
	currentMicroEpochStart := uint64(0)
	currentMicroEpochEnd := uint64(0)
	if microEpochSize > 0 && head.Number > 0 {
		currentMicroEpoch = (head.Number - 1) / microEpochSize
		currentMicroEpochStart = currentMicroEpoch*microEpochSize + 1
		currentMicroEpochEnd = (currentMicroEpoch + 1) * microEpochSize
	}

	res := &posOverviewResponse{
		BlockNumber:                 argUint64(head.Number),
		EpochSize:                   argUint64(epochSize),
		MicroEpochSize:              argUint64(microEpochSize),
		CurrentMicroEpoch:           argUint64(currentMicroEpoch),
		CurrentMicroEpochStartBlock: argUint64(currentMicroEpochStart),
		CurrentMicroEpochEndBlock:   argUint64(currentMicroEpochEnd),
		CurrentEpoch:                argUint64(currentEpoch),
		LastFinalizedEpoch:          argUint64(lastFinalizedEpoch),
		ReportedEpoch:               argUint64(reportedEpoch),
		ReportedEpochStartBlock:     argUint64(reportedEpochStart),
		ReportedEpochEndBlock:       argUint64(reportedEpochEnd),
		CurrentEpochPendingRewards:  argBigPtr(currentEpochPendingRewards),
		StakingContractBalance:      argBigPtr(stakingContractBalance),
		MinimumNumValidators:        argUint64(minV),
		MaximumNumValidators:        argUint64(maxV),
		ValidatorThreshold:          argBigPtr(threshold),
		TotalCurrentStake:           argBigPtr(totalCurrentStake),
		TotalValidatorSelfStake:     argBigPtr(totalValidatorSelfStake),
		TotalDelegatedRawStake:      argBigPtr(totalDelegatedRawStake),
		TotalDelegatedActiveStake:   argBigPtr(totalDelegatedActiveStake),
		TotalActiveCurrentStake:     argBigPtr(totalActiveCurrentStake),
		RewardIneligibleCount:       argUintPtr(rewardIneligibleCount),
		SlashedCount:                argUintPtr(slashedCount),
		RewardIneligibleStatusExact: true,
		SlashStatusExact:            false,
		LastRoundStakeExact:         false,
		LastRoundDistributedStake:   argBigPtr(lastRoundDistributedStake),
		MonitoringNotes: []string{
			"currentlyValidating is derived strictly from current consensus header validator set (no heuristic fallback)",
			"wasValidatorLastEpoch prefers epoch-keyed PoS state and falls back to last-finalized epoch-end header snapshot if state is missing",
			"joinEffectiveAtBlock/deactivateEffectiveAtBlock are deterministic epoch-boundary activation/deactivation markers",
			"rewardIneligible is computed only for validators present in reportedEpoch set, using proposer slots/missed counters",
			"proposalUptimeLast{3,10}Epochs* are rolling proposer-duty reliability metrics (not lifetime uptime); epochs outside join/deactivation-effective bounds are excluded",
			"proposalUptime...Observed reports total proposer duties observed in each window; if 0 then uptime fields are omitted (no fake 100%)",
			"Historical rewards/slashes/staker stake-after values are emitted as PosSysAddr system logs and must be indexed from receipts. PoS RPC exposes live staking state and optionally current/pending epoch working data only. Finalized epoch working state is deleted after FinalizeEpoch.",
			"currentEpochPendingRewards is the live FeePool balance that will be distributed at the next epoch switch",
			"stakingContractBalance is the live on-chain balance of the staking contract and helps detect reward-credit mismatches",
			"unstakeAvailableAtBlock/canUnstakeNow are derived from validatorInfo.deactivatedAtBlock and epoch size",
			"all returned numeric stake metrics are deterministic and derived directly from contract state + events",
			"micro* fields expose the current micro-epoch uptime accounting weights used as multiplier in the weighted PoS path",
			"microEpoch* fields describe the current block-based micro-epoch used for uptime weight accounting",
		},
		Validators: validatorList,
		PoSActive:  !hasPoS || head.Number >= posFrom,
	}
	if hasPoS {
		res.PoSFromBlock = argUintPtr(posFrom)
	}

	return res, nil
}

func (e *Eth) GetBeaconTimeStatus() (interface{}, error) {
	// Deprecated legacy endpoint; beacon recovery path has been removed.
	return map[string]interface{}{
		"enabled":    false,
		"active":     false,
		"healthy":    false,
		"deprecated": true,
		"reason":     "deprecated",
	}, nil
}

// GetPosValidatorDelegators returns delegator details for one validator.
func (e *Eth) GetPosValidatorDelegators(validator types.Address, reportEpoch *string) (interface{}, error) {
	head := e.store.Header()
	if head == nil {
		return nil, fmt.Errorf("header has a nil value")
	}
	epochSize, err := resolveIBFTEpochSize(e.store.GetChainParams())
	if err != nil {
		return nil, err
	}
	currentEpoch := epochOf(head.Number, epochSize)
	lastFinalizedEpoch := head.Number / epochSize
	reportedEpoch := currentEpoch
	if reportEpoch != nil {
		switch *reportEpoch {
		case "current":
			reportedEpoch = currentEpoch
		case "lastFinalized":
			reportedEpoch = lastFinalizedEpoch
		default:
			return nil, fmt.Errorf("invalid reportEpoch %q (expected \"current\" or \"lastFinalized\")", *reportEpoch)
		}
	}

	rt := &stakingQueryRuntime{store: e.store, header: head}
	selfStake, _ := callStakeUintMethod(rt, "validatorSelfStake", validator)
	delegatedRaw, _ := callStakeUintMethod(rt, "validatorDelegatedStakeRaw", validator)
	delegatedActive, _ := callStakeUintMethod(rt, "validatorDelegatedStakeActive", validator)
	poolConfig, _ := callStakePoolMethod(rt, validator)
	if poolConfig == nil {
		poolConfig = &validatorPoolRPCView{MaxTotalDelegatedStake: new(big.Int)}
	}
	totalActiveCurrent := new(big.Int).Add(new(big.Int).Set(selfStake), delegatedActive)
	delegatedActiveCurrent := new(big.Int).Sub(new(big.Int).Set(totalActiveCurrent), selfStake)
	if delegatedActiveCurrent.Sign() < 0 {
		delegatedActiveCurrent = new(big.Int)
	}

	entries := make([]posDelegatorEntry, 0)
	count, err := callStakeUintMethod(rt, "validatorStakerCount", validator)
	if err != nil {
		return nil, err
	}

	for i := uint64(0); i < count.Uint64(); i++ {
		owner, oErr := callStakeAddressMethod(rt, "validatorStakerAt", validator, i)
		if oErr != nil {
			return nil, oErr
		}
		if owner == validator {
			continue
		}
		staker, sErr := stakingHelper.QueryStakerInfo(rt, types.ZeroAddress, owner)
		if sErr != nil || staker == nil {
			continue
		}
		joined := uint64(0)
		if staker.JoinedAtBlock != nil {
			joined = staker.JoinedAtBlock.Uint64()
		}
		deact := uint64(0)
		if staker.DeactivatedAtBlock != nil {
			deact = staker.DeactivatedAtBlock.Uint64()
		}
		snapAmount := readBigFromPosSys(e, head.StateRoot, keyStakerStakeSnapshot(reportedEpoch, owner))
		entries = append(entries, posDelegatorEntry{
			Delegator:            owner,
			Amount:               argBigPtr(staker.Amount),
			EpochEffectiveAmount: argBigPtr(snapAmount),
			Active:               staker.Active,
			JoinedAtBlock:        argUintPtr(joined),
			DeactivatedAtBlock:   argUintPtr(deact),
			EffectiveAtPoint:     snapAmount.Sign() > 0,
		})
	}
	selfEpochEffective := readBigFromPosSys(e, head.StateRoot, keyStakerStakeSnapshot(reportedEpoch, validator))
	delegatedEpochEffective := new(big.Int)
	for _, entry := range entries {
		if entry.EffectiveAtPoint {
			delegatedEpochEffective.Add(delegatedEpochEffective, (*big.Int)(entry.EpochEffectiveAmount))
		}
	}
	totalEpochEffective := readBigFromPosSys(e, head.StateRoot, keyStakeSnapshot(reportedEpoch, validator))

	sort.Slice(entries, func(i, j int) bool { return entries[i].Delegator.String() < entries[j].Delegator.String() })

	return &posValidatorDelegatorsResponse{
		Validator:                  validator,
		SelfStake:                  argBigPtr(selfStake),
		DelegatedRaw:               argBigPtr(delegatedRaw),
		DelegatedActive:            argBigPtr(delegatedActive),
		DelegatedActiveCurrent:     argBigPtr(delegatedActiveCurrent),
		TotalActiveCurrentStake:    argBigPtr(totalActiveCurrent),
		SelfStakeLive:              argBigPtr(new(big.Int).Set(selfStake)),
		DelegatedLiveRaw:           argBigPtr(new(big.Int).Set(delegatedRaw)),
		DelegatedLiveActive:        argBigPtr(new(big.Int).Set(delegatedActive)),
		TotalLiveStake:             argBigPtr(totalActiveCurrent),
		SelfStakeEpochEffective:    argBigPtr(selfEpochEffective),
		DelegatedEpochEffective:    argBigPtr(delegatedEpochEffective),
		TotalEpochEffectiveStake:   argBigPtr(totalEpochEffective),
		DelegationEnabled:          poolConfig.DelegationEnabled,
		MaxTotalDelegatedStake:     argBigPtr(poolConfig.MaxTotalDelegatedStake),
		MinDelegatorStake:          argBigPtr(poolConfig.MinDelegatorStake),
		EffectiveMinDelegatorStake: argBigPtr(poolConfig.EffectiveMinDelegatorStake),
		CommissionBps:              argUint64(poolConfig.CommissionBps),
		Delegators:                 entries,
	}, nil
}

func uint64Ptr(v uint64) *uint64 {
	return &v
}

func boolPtr(v bool) *bool {
	return &v
}

func stringPtr(v string) *string {
	return &v
}

func int64Ptr(v int64) *int64 {
	return &v
}

type recoveryEvidenceCache struct {
	epochStart      uint64
	currentSlot     uint64
	lastScannedNum  uint64
	lastScannedHash types.Hash
	evidence        map[uint64]map[string]struct{}
}

type recoveryOverviewCache struct {
	mu    sync.Mutex
	state *recoveryEvidenceCache
}

func (e *Eth) computeRecoveryOverviewPowers(
	head *types.Header,
	currentValidators map[types.Address]struct{},
) (map[string]uint64, string) {
	store := e.store
	if store == nil || head == nil {
		return nil, "deprecated_legacy_endpoint"
	}
	meta, ok := resolveUptimeMeta(store.GetChainParams())
	if !ok {
		return nil, "deprecated_legacy_endpoint"
	}

	currentSlot := slotAtTimestamp(meta, head.Timestamp)
	if currentSlot == 0 {
		return nil, "deprecated_legacy_endpoint"
	}
	epochStart := epochAtSlot(meta, currentSlot) * meta.slotsPerEpoch

	validatorsByAddr := make(map[string]types.Address, len(currentValidators))
	for addr := range currentValidators {
		validatorsByAddr[strings.ToLower(addr.String())] = addr
	}
	if len(validatorsByAddr) == 0 {
		return nil, "deprecated_legacy_endpoint"
	}

	e.recoveryCache.mu.Lock()
	defer e.recoveryCache.mu.Unlock()

	cache := e.recoveryCache.state
	needReset := cache == nil || cache.epochStart != epochStart || cache.lastScannedNum == 0 || head.Number < cache.lastScannedNum
	if !needReset && cache.lastScannedNum > 0 {
		if cachedHead, ok := store.GetHeaderByNumber(cache.lastScannedNum); !ok || cachedHead == nil || cachedHead.Hash != cache.lastScannedHash {
			needReset = true
		}
	}

	if needReset {
		cache = &recoveryEvidenceCache{
			epochStart:  epochStart,
			currentSlot: currentSlot,
			evidence:    map[uint64]map[string]struct{}{},
		}
		for blockNum := head.Number; blockNum > 0; blockNum-- {
			h, ok := store.GetHeaderByNumber(blockNum)
			if !ok || h == nil {
				continue
			}
			accumulateRecoveryEvidence(store, h, meta, epochStart, currentSlot, cache.evidence)
			if slotAtTimestamp(meta, h.Timestamp) <= epochStart {
				break
			}
		}
		cache.lastScannedNum = head.Number
		cache.lastScannedHash = head.Hash
		e.recoveryCache.state = cache
	}

	if cache.lastScannedNum < head.Number {
		for blockNum := cache.lastScannedNum + 1; blockNum <= head.Number; blockNum++ {
			h, ok := store.GetHeaderByNumber(blockNum)
			if !ok || h == nil {
				continue
			}
			accumulateRecoveryEvidence(store, h, meta, epochStart, currentSlot, cache.evidence)
			cache.lastScannedNum = blockNum
			cache.lastScannedHash = h.Hash
		}
		cache.currentSlot = currentSlot
		e.recoveryCache.state = cache
	}

	evidence := cache.evidence
	if len(evidence) == 0 {
		return nil, "deprecated_legacy_endpoint"
	}

	out := map[string]uint64{}
	for key := range validatorsByAddr {
		missed := uint64(0)
		power := meta.nominalWeightUnits
		for slot := epochStart; slot <= currentSlot; slot++ {
			slotVals := evidence[slot]
			if len(slotVals) == 0 {
				continue
			}
			if _, ok := slotVals[key]; !ok {
				missed++
			}
			if missed > meta.recoveryMissThresholdPerEpoch {
				power = 0
				break
			}
		}
		out[key] = power
	}
	if len(out) == 0 {
		return nil, "deprecated_legacy_endpoint"
	}

	return out, "deprecated_legacy_endpoint"
}

func accumulateRecoveryEvidence(
	store ethStore,
	h *types.Header,
	meta uptimeOverviewMeta,
	epochStart uint64,
	currentSlot uint64,
	evidence map[uint64]map[string]struct{},
) {
	return
}

func extractTopicAddress(topic types.Hash) types.Address {
	return types.BytesToAddress(topic[12:])
}

func resolveIBFTEpochSize(params *chain.Params) (uint64, error) {
	if params == nil {
		return 0, fmt.Errorf("missing chain params")
	}

	rawIBFT, ok := params.Engine["ibft"]
	if !ok {
		return 0, fmt.Errorf("missing ibft engine config")
	}
	ibftMap, ok := rawIBFT.(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("invalid ibft engine config type %T", rawIBFT)
	}

	microEpoch, hasMicro := parsePositiveUint64FromInterface(ibftMap["microEpochSize"])
	macroFactor, hasFactor := parsePositiveUint64FromInterface(ibftMap["macroEpochMicroFactor"])
	_, isPoS := resolvePoSFromBlock(params)
	if isPoS {
		if !hasMicro || !hasFactor {
			return 0, fmt.Errorf("unable to resolve PoS epoch size: missing valid microEpochSize/macroEpochMicroFactor")
		}
		if macroFactor > ^uint64(0)/microEpoch {
			return 0, fmt.Errorf("macro epoch size overflow: microEpochSize=%d macroEpochMicroFactor=%d", microEpoch, macroFactor)
		}
		return microEpoch * macroFactor, nil
	}

	if epochSize, ok := parsePositiveUint64FromInterface(ibftMap["epochSize"]); ok {
		return epochSize, nil
	}

	return 0, fmt.Errorf("unable to resolve IBFT epoch size: missing valid epoch configuration")
}

func parsePositiveUint64FromInterface(raw interface{}) (uint64, bool) {
	v, ok := raw.(float64)
	if !ok || v <= 0 {
		return 0, false
	}
	return uint64(v), true
}

func resolvePoSFromBlock(params *chain.Params) (uint64, bool) {
	if params == nil {
		return 0, false
	}

	rawIBFT, ok := params.Engine["ibft"]
	if !ok {
		return 0, false
	}
	ibftMap, ok := rawIBFT.(map[string]interface{})
	if !ok {
		return 0, false
	}
	rawTypes, ok := ibftMap["types"]
	if ok {
		typesSlice, ok := rawTypes.([]interface{})
		if !ok {
			return 0, false
		}

		var (
			minFrom uint64
			found   bool
		)

		for _, rawType := range typesSlice {
			typeMap, ok := rawType.(map[string]interface{})
			if !ok {
				continue
			}

			tv, ok := typeMap["type"].(string)
			if !ok || tv != "PoS" {
				continue
			}

			fromFloat, ok := typeMap["from"].(float64)
			if !ok || fromFloat < 0 {
				continue
			}

			from := uint64(fromFloat)
			if !found || from < minFrom {
				minFrom = from
				found = true
			}
		}

		return minFrom, found
	}

	tv, ok := ibftMap["type"].(string)
	if !ok || tv != "PoS" {
		return 0, false
	}

	return 0, true
}

type uptimeOverviewMeta struct {
	genesisTimeUnix               uint64
	slotDurationSec               uint64
	slotsPerEpoch                 uint64
	recoveryMissThresholdPerEpoch uint64
	nominalWeightUnits            uint64
}

func resolveUptimeMeta(params *chain.Params) (uptimeOverviewMeta, bool) {
	if params == nil {
		return uptimeOverviewMeta{}, false
	}
	rawIBFT, ok := params.Engine["ibft"]
	if !ok {
		return uptimeOverviewMeta{}, false
	}
	ibftMap, ok := rawIBFT.(map[string]interface{})
	if !ok {
		return uptimeOverviewMeta{}, false
	}
	configMap := ibftMap
	if rawCfg, ok := ibftMap["config"]; ok {
		if nested, ok := rawCfg.(map[string]interface{}); ok && nested != nil {
			configMap = nested
		}
	}
	read := func(key string) uint64 {
		raw, ok := configMap[key]
		if !ok || raw == nil {
			raw, ok = ibftMap[key]
			if !ok || raw == nil {
				return 0
			}
		}
		v, ok := raw.(float64)
		if !ok || v <= 0 {
			return 0
		}
		return uint64(v)
	}
	meta := uptimeOverviewMeta{
		genesisTimeUnix:               read("genesisTime"),
		slotDurationSec:               read("slotDurationSec"),
		slotsPerEpoch:                 read("microEpochSize"),
		recoveryMissThresholdPerEpoch: read("recoveryMissThresholdPerEpoch"),
		nominalWeightUnits:            read("microEpochNominalWeightUnits"),
	}
	if v := read("microEpochSlots"); v > 0 {
		meta.slotsPerEpoch = v
	}
	if v := read("nominalWeightUnits"); v > 0 {
		meta.nominalWeightUnits = v
	}
	if v := read("uptimeNominalWeightUnits"); v > 0 {
		meta.nominalWeightUnits = v
	}
	if meta.slotsPerEpoch == 0 {
		return uptimeOverviewMeta{}, false
	}
	return meta, true
}

func slotAtTimestamp(meta uptimeOverviewMeta, ts uint64) uint64 {
	if meta.slotDurationSec == 0 || ts < meta.genesisTimeUnix {
		return 0
	}
	return (ts - meta.genesisTimeUnix) / meta.slotDurationSec
}

func epochAtSlot(meta uptimeOverviewMeta, slot uint64) uint64 {
	if meta.slotsPerEpoch == 0 {
		return 0
	}
	return slot / meta.slotsPerEpoch
}

var posSysAddr = types.StringToAddress("0x0000000000000000000000000000000000009999")

const (
	keyPrefixPropSlots    = "xgr.pos.prop.slots"
	keyPrefixPropMiss     = "xgr.pos.prop.miss"
	keyPrefixEpochLen     = "xgr.pos.epoch.validators.len"
	keyPrefixEpochVal     = "xgr.pos.epoch.validators.val"
	keyPrefixSlashed      = "xgr.pos.slashed"
	keyPrefixRewardTot    = "xgr.pos.reward.total"
	keyPrefixRewardVal    = "xgr.pos.reward.validator"
	keyPrefixRewardValCom = "xgr.pos.reward.validator.commission"
	keyPrefixRewardDelTot = "xgr.pos.reward.delegators.total"
	keyPrefixRewardDel    = "xgr.pos.reward.delegator"
	keyPrefixUptimeNomW   = "xgr.uptime.nominal.weight"
	keyPrefixUptimeEffW   = "xgr.uptime.effective.weight"
	keyPrefixUptimeInact  = "xgr.uptime.inactivity.count"
	keyPrefixUptimeLastEp = "xgr.uptime.last.epoch"
	keyPrefixUptimeLastSl = "xgr.uptime.last.processed.slot"
)

func epochOf(number, epochSize uint64) uint64 {
	if number == 0 || epochSize == 0 {
		return 0
	}
	if number%epochSize == 0 {
		return number / epochSize
	}
	return number/epochSize + 1
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

func keySlashed(epoch uint64, addr types.Address) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)
	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixSlashed), eb[:], addr.Bytes()))
}

func keyEpochValidatorsLen(epoch uint64) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)
	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixEpochLen), eb[:]))
}

func keyEpochValidator(epoch, idx uint64) types.Hash {
	var eb [8]byte
	var ib [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)
	binary.BigEndian.PutUint64(ib[:], idx)
	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixEpochVal), eb[:], ib[:]))
}

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

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixRewardValCom), eb[:], addr.Bytes()))
}

func keyValidatorDelegatorsRewardTotal(epoch uint64, addr types.Address) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixRewardDelTot), eb[:], addr.Bytes()))
}

func keyDelegatorReward(epoch uint64, validator, delegator types.Address) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)

	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixRewardDel), eb[:], validator.Bytes(), delegator.Bytes()))
}

func keyStakerStakeSnapshot(epoch uint64, addr types.Address) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)
	return types.BytesToHash(crypto.Keccak256([]byte("xgr.pos.staker.stake.snapshot"), eb[:], addr.Bytes()))
}

func keyStakeSnapshot(epoch uint64, addr types.Address) types.Hash {
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], epoch)
	return types.BytesToHash(crypto.Keccak256([]byte("xgr.pos.stake.snapshot"), eb[:], addr.Bytes()))
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

func keyUptimeLastEpoch() types.Hash {
	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixUptimeLastEp)))
}

func keyUptimeLastSlot() types.Hash {
	return types.BytesToHash(crypto.Keccak256([]byte(keyPrefixUptimeLastSl)))
}

func readBigFromPosSys(e *Eth, root types.Hash, slot types.Hash) *big.Int {
	b, err := e.store.GetStorage(root, posSysAddr, slot)
	if err != nil || len(b) == 0 {
		return new(big.Int)
	}
	return new(big.Int).SetBytes(b)
}

func queryJoinedAtBlockRuntime(rt *stakingQueryRuntime, addr types.Address) (uint64, error) {
	if rt == nil {
		return 0, nil
	}
	selector := crypto.Keccak256([]byte("joinedAtBlock(address)"))[:4]
	input := make([]byte, 4+32)
	copy(input[:4], selector)
	copy(input[4+12:], addr.Bytes())

	to := stakingHelper.AddrStakingContract
	tx := &types.Transaction{
		From:     types.ZeroAddress,
		To:       &to,
		Input:    input,
		Nonce:    rt.GetNonce(types.ZeroAddress),
		Gas:      1000000,
		Value:    big.NewInt(0),
		GasPrice: big.NewInt(0),
	}

	rt.SetNonPayable(true)
	res, err := rt.Apply(tx)
	if err != nil {
		return 0, err
	}
	if res == nil || res.Failed() || len(res.ReturnValue) == 0 {
		if res != nil && res.Err != nil {
			return 0, res.Err
		}
		return 0, nil
	}

	return new(big.Int).SetBytes(res.ReturnValue).Uint64(), nil
}

func readValidatorsFromConsensusHeader(e *Eth, blockNumber uint64) map[types.Address]struct{} {
	if e == nil || e.store == nil {
		return nil
	}

	blk, ok := e.store.GetBlockByNumber(blockNumber, false)
	if !ok || blk == nil || blk.Header == nil || len(blk.Header.ExtraData) < signer.IstanbulExtraVanity {
		return nil
	}

	vt := resolveIBFTValidatorType(e.store.GetChainParams(), blockNumber)
	extra, ok := decodeIBFTExtraByType(blk.Header, vt)
	if !ok || extra == nil || extra.Validators == nil || extra.Validators.Len() == 0 {
		return nil
	}

	out := make(map[types.Address]struct{}, extra.Validators.Len())
	for i := 0; i < extra.Validators.Len(); i++ {
		out[extra.Validators.At(uint64(i)).Addr()] = struct{}{}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func decodeIBFTExtraByType(header *types.Header, vt validatorTypes.ValidatorType) (*signer.IstanbulExtra, bool) {
	if header == nil || len(header.ExtraData) < signer.IstanbulExtraVanity {
		return nil, false
	}

	validatorSet := validatorTypes.NewValidatorSetFromType(vt)
	if validatorSet == nil {
		return nil, false
	}

	extra := &signer.IstanbulExtra{Validators: validatorSet}
	switch vt {
	case validatorTypes.ECDSAValidatorType:
		extra.CommittedSeals = &signer.SerializedSeal{}
		extra.ParentCommittedSeals = &signer.SerializedSeal{}
	case validatorTypes.BLSValidatorType:
		extra.CommittedSeals = &signer.AggregatedSeal{}
		extra.ParentCommittedSeals = &signer.AggregatedSeal{}
	default:
		return nil, false
	}

	if err := extra.UnmarshalRLP(header.ExtraData[signer.IstanbulExtraVanity:]); err != nil {
		return nil, false
	}

	return extra, true
}

func resolveIBFTValidatorType(params *chain.Params, height uint64) validatorTypes.ValidatorType {
	if params == nil {
		return validatorTypes.ECDSAValidatorType
	}
	raw, ok := params.Engine["ibft"]
	if !ok {
		return validatorTypes.ECDSAValidatorType
	}
	ibftCfg, ok := raw.(map[string]interface{})
	if !ok {
		return validatorTypes.ECDSAValidatorType
	}
	forks, err := ibftFork.GetIBFTForks(ibftCfg)
	if err != nil || len(forks) == 0 {
		return validatorTypes.ECDSAValidatorType
	}

	for _, f := range forks {
		if f == nil {
			continue
		}
		from := f.From.Value
		to := uint64(^uint64(0))
		if f.To != nil {
			to = f.To.Value
		}
		if height >= from && height <= to {
			if f.ValidatorType != "" {
				return f.ValidatorType
			}
			return validatorTypes.ECDSAValidatorType
		}
	}

	return validatorTypes.ECDSAValidatorType
}

func readValidatorsForEpoch(e *Eth, root types.Hash, epoch uint64, epochEndBlock uint64) map[types.Address]struct{} {
	set := readValidatorsFromPosState(e, root, epoch)
	if len(set) > 0 {
		return set
	}
	if epochEndBlock == 0 {
		return nil
	}
	return readValidatorsFromConsensusHeader(e, epochEndBlock)
}

func readValidatorsFromPosState(e *Eth, root types.Hash, epoch uint64) map[types.Address]struct{} {
	if e == nil || epoch == 0 {
		return nil
	}

	length := readBigFromPosSys(e, root, keyEpochValidatorsLen(epoch)).Uint64()
	if length == 0 {
		return nil
	}

	set := make(map[types.Address]struct{}, length)
	for i := uint64(0); i < length; i++ {
		raw := readBigFromPosSys(e, root, keyEpochValidator(epoch, i)).Bytes()
		if len(raw) == 0 {
			continue
		}

		addr := types.BytesToAddress(raw)
		if addr == types.ZeroAddress {
			continue
		}
		set[addr] = struct{}{}
	}

	if len(set) == 0 {
		return nil
	}

	return set
}

func isValidatorRewardIneligibleInEpoch(e *Eth, root types.Hash, addr types.Address, epoch uint64) bool {
	if epoch == 0 {
		return false
	}

	slots := readBigFromPosSys(e, root, keyProposerSlots(epoch, addr)).Uint64()
	if slots == 0 {
		return false
	}

	missed := readBigFromPosSys(e, root, keyProposerMissed(epoch, addr)).Uint64()
	if missed > slots {
		missed = slots
	}
	okSlots := slots - missed

	return okSlots*10 < slots*8
}

func isValidatorInEpoch(e *Eth, root types.Hash, addr types.Address, epoch uint64) bool {
	if epoch == 0 {
		return false
	}

	if readBigFromPosSys(e, root, keyProposerSlots(epoch, addr)).Sign() > 0 {
		return true
	}
	if readBigFromPosSys(e, root, keyProposerMissed(epoch, addr)).Sign() > 0 {
		return true
	}
	return false
}

type proposalWindowUptimeMetrics struct {
	uptimeBps     *uint64
	observedSlots uint64
}

func computeProposalWindowUptimeMetrics(
	e *Eth,
	root types.Hash,
	entry *posValidatorOverview,
	reportedEpoch uint64,
	epochSize uint64,
	window uint64,
) proposalWindowUptimeMetrics {
	if e == nil || entry == nil || reportedEpoch == 0 || epochSize == 0 || window == 0 {
		return proposalWindowUptimeMetrics{}
	}

	startEpoch := uint64(1)
	if reportedEpoch >= window {
		startEpoch = reportedEpoch - window + 1
	}

	var totalSlots uint64
	var missedSlots uint64
	for epoch := startEpoch; epoch <= reportedEpoch; epoch++ {
		epochEndBlock := epoch * epochSize
		if entry.JoinEffectiveAtBlock != nil && epochEndBlock < uint64(*entry.JoinEffectiveAtBlock) {
			continue
		}
		if entry.DeactivateEffectiveAtBlock != nil && epochEndBlock > uint64(*entry.DeactivateEffectiveAtBlock) {
			continue
		}

		slots := readBigFromPosSys(e, root, keyProposerSlots(epoch, entry.Address)).Uint64()
		if slots == 0 {
			continue
		}
		missed := readBigFromPosSys(e, root, keyProposerMissed(epoch, entry.Address)).Uint64()
		if missed > slots {
			missed = slots
		}
		totalSlots += slots
		missedSlots += missed
	}

	if totalSlots == 0 {
		return proposalWindowUptimeMetrics{}
	}

	okSlots := totalSlots - missedSlots
	bps := (okSlots * 10000) / totalSlots

	return proposalWindowUptimeMetrics{
		uptimeBps:     &bps,
		observedSlots: totalSlots,
	}
}

func callStakeUintMethod(rt *stakingQueryRuntime, methodName string, validator types.Address) (*big.Int, error) {
	method := abis.StakingABI.Methods[methodName]
	if method == nil {
		return nil, fmt.Errorf("staking ABI missing %s", methodName)
	}

	inp, err := method.Inputs.Encode(map[string]interface{}{"validator": ethgo.Address(validator)})
	if err != nil {
		inp, err = method.Inputs.Encode(map[string]interface{}{"account": ethgo.Address(validator)})
		if err != nil {
			return nil, err
		}
	}

	rt.SetNonPayable(true)
	res, err := rt.Apply(&types.Transaction{
		From:     types.ZeroAddress,
		To:       &stakingHelper.AddrStakingContract,
		Input:    append(method.ID(), inp...),
		Nonce:    rt.GetNonce(types.ZeroAddress),
		Gas:      1000000,
		Value:    big.NewInt(0),
		GasPrice: big.NewInt(0),
	})
	if err != nil {
		return nil, err
	}
	if res.Failed() {
		return nil, res.Err
	}
	decoded, err := method.Outputs.Decode(res.ReturnValue)
	if err != nil {
		return nil, err
	}
	mp, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("decode %s failed", methodName)
	}
	val, ok := mp["0"].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("decode %s failed", methodName)
	}
	return val, nil
}

func callStakeAddressMethod(rt *stakingQueryRuntime, methodName string, validator types.Address, idx uint64) (types.Address, error) {
	method := abis.StakingABI.Methods[methodName]
	if method == nil {
		return types.ZeroAddress, fmt.Errorf("staking ABI missing %s", methodName)
	}
	inp, err := method.Inputs.Encode(map[string]interface{}{"validator": ethgo.Address(validator), "idx": new(big.Int).SetUint64(idx)})
	if err != nil {
		return types.ZeroAddress, err
	}
	rt.SetNonPayable(true)
	res, err := rt.Apply(&types.Transaction{
		From:     types.ZeroAddress,
		To:       &stakingHelper.AddrStakingContract,
		Input:    append(method.ID(), inp...),
		Nonce:    rt.GetNonce(types.ZeroAddress),
		Gas:      1000000,
		Value:    big.NewInt(0),
		GasPrice: big.NewInt(0),
	})
	if err != nil {
		return types.ZeroAddress, err
	}
	if res.Failed() {
		return types.ZeroAddress, res.Err
	}
	decoded, err := method.Outputs.Decode(res.ReturnValue)
	if err != nil {
		return types.ZeroAddress, err
	}
	mp, ok := decoded.(map[string]interface{})
	if !ok {
		return types.ZeroAddress, fmt.Errorf("decode %s failed", methodName)
	}
	addr, ok := mp["0"].(ethgo.Address)
	if !ok {
		return types.ZeroAddress, fmt.Errorf("decode %s failed", methodName)
	}

	return types.Address(addr), nil
}

type validatorPoolRPCView struct {
	DelegationEnabled          bool
	MaxTotalDelegatedStake     *big.Int
	MinDelegatorStake          *big.Int
	EffectiveMinDelegatorStake *big.Int
	CommissionBps              uint16
}

func callStakePoolMethod(rt *stakingQueryRuntime, validator types.Address) (*validatorPoolRPCView, error) {
	method := abis.StakingABI.Methods["validatorPool"]
	if method == nil {
		return &validatorPoolRPCView{MaxTotalDelegatedStake: new(big.Int)}, fmt.Errorf("staking ABI missing validatorPool")
	}

	inp, err := method.Inputs.Encode(map[string]interface{}{"validator": ethgo.Address(validator)})
	if err != nil {
		return nil, err
	}

	rt.SetNonPayable(true)
	res, err := rt.Apply(&types.Transaction{
		From:     types.ZeroAddress,
		To:       &stakingHelper.AddrStakingContract,
		Input:    append(method.ID(), inp...),
		Nonce:    rt.GetNonce(types.ZeroAddress),
		Gas:      1000000,
		Value:    big.NewInt(0),
		GasPrice: big.NewInt(0),
	})
	if err != nil {
		return nil, err
	}
	if res.Failed() {
		return nil, res.Err
	}

	decoded, err := method.Outputs.Decode(res.ReturnValue)
	if err != nil {
		return nil, err
	}
	mp, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("decode validatorPool failed")
	}

	out, ok := mp["0"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("decode validatorPool failed")
	}

	enabled, _ := out["delegationEnabled"].(bool)
	maxStake, _ := out["maxTotalDelegatedStake"].(*big.Int)
	if maxStake == nil {
		maxStake = new(big.Int)
	}
	minStake, _ := out["minDelegatorStake"].(*big.Int)
	if minStake == nil {
		minStake = new(big.Int)
	}
	effectiveMin := new(big.Int).Set(minStake)
	if effectiveMin.Sign() == 0 {
		effectiveMin = new(big.Int).Mul(big.NewInt(10000), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	}
	var commission uint16
	switch v := out["commissionBps"].(type) {
	case uint16:
		commission = v
	case *big.Int:
		commission = uint16(v.Uint64())
	}

	return &validatorPoolRPCView{
		DelegationEnabled:          enabled,
		MaxTotalDelegatedStake:     maxStake,
		MinDelegatorStake:          minStake,
		EffectiveMinDelegatorStake: effectiveMin,
		CommissionBps:              commission,
	}, nil
}

func isStakerEffectiveAtPoint(joined, deactivated, blockNumber, epochSize uint64) bool {
	if joined == 0 || epochSize == 0 {
		return false
	}
	if blockNumber/epochSize <= joined/epochSize {
		return false
	}
	if deactivated == 0 {
		return true
	}

	return blockNumber/epochSize <= deactivated/epochSize
}
