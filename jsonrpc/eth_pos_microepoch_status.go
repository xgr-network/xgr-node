package jsonrpc

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	stakingHelper "github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

type posMicroEpochStatusResponse struct {
	BlockNumber                 argUint64                     `json:"blockNumber"`
	Enabled                     bool                          `json:"enabled"`
	ConfigStatus                string                        `json:"configStatus"`
	MicroEpochSize              argUint64                     `json:"microEpochSize"`
	CurrentMicroEpoch           argUint64                     `json:"currentMicroEpoch"`
	CurrentMicroEpochStartBlock argUint64                     `json:"currentMicroEpochStartBlock"`
	CurrentMicroEpochEndBlock   argUint64                     `json:"currentMicroEpochEndBlock"`
	LastAppliedMicroEpoch       argUint64                     `json:"lastAppliedMicroEpoch"`
	NominalWeightUnits          argUint64                     `json:"nominalWeightUnits"`
	InactivityDecayBps          argUint64                     `json:"inactivityDecayBps"`
	Validators                  []posMicroEpochValidatorState `json:"validators"`
}

type posMicroEpochValidatorState struct {
	Address                types.Address `json:"address"`
	UptimeNominalWeight    argUint64     `json:"uptimeNominalWeight"`
	UptimeEffectiveWeight  argUint64     `json:"uptimeEffectiveWeight"`
	UptimeInactivity       argUint64     `json:"uptimeInactivity"`
	MicroNominalWeight     argUint64     `json:"microNominalWeight"`
	MicroEffectiveWeight   argUint64     `json:"microEffectiveWeight"`
	MicroInactivity        argUint64     `json:"microInactivity"`
	WeightStateInitialized bool          `json:"weightStateInitialized"`
}

// GetPosMicroEpochStatus exposes the block-based micro-epoch uptime/weight state.
// It is intentionally independent from the removed beacon/recovery path.
func (e *Eth) GetPosMicroEpochStatus() (interface{}, error) {
	head := e.store.Header()
	if head == nil {
		return nil, fmt.Errorf("header has a nil value")
	}

	cfg := readMicroEpochConfigFromChain(e.store.GetChainParams().Engine)
	enabled := cfg.microEpochSize > 0 && cfg.nominalWeightUnits > 0
	status := "active"
	if !enabled {
		status = "micro-epoch uptime is not configured or disabled"
	}

	currentME := uint64(0)
	startBlock := uint64(0)
	endBlock := uint64(0)
	if cfg.microEpochSize > 0 && head.Number > 0 {
		currentME = (head.Number - 1) / cfg.microEpochSize
		startBlock = currentME*cfg.microEpochSize + 1
		endBlock = (currentME + 1) * cfg.microEpochSize
	}

	rt := &stakingQueryRuntime{store: e.store, header: head}
	validators, err := stakingHelper.QueryValidators(rt, types.ZeroAddress)
	if err != nil {
		return nil, err
	}

	items := make([]posMicroEpochValidatorState, 0, len(validators))
	for _, addr := range validators {
		nominal := readBigFromPosSys(e, head.StateRoot, keyUptimeNominalWeight(addr)).Uint64()
		effective := readBigFromPosSys(e, head.StateRoot, keyUptimeEffectiveWeight(addr)).Uint64()
		inactivity := readBigFromPosSys(e, head.StateRoot, keyUptimeInactivity(addr)).Uint64()
		items = append(items, posMicroEpochValidatorState{
			Address:                addr,
			UptimeNominalWeight:    argUint64(nominal),
			UptimeEffectiveWeight:  argUint64(effective),
			UptimeInactivity:       argUint64(inactivity),
			MicroNominalWeight:     argUint64(nominal),
			MicroEffectiveWeight:   argUint64(effective),
			MicroInactivity:        argUint64(inactivity),
			WeightStateInitialized: nominal > 0 || effective > 0 || inactivity > 0,
		})
	}

	return &posMicroEpochStatusResponse{
		BlockNumber:                 argUint64(head.Number),
		Enabled:                     enabled,
		ConfigStatus:                status,
		MicroEpochSize:              argUint64(cfg.microEpochSize),
		CurrentMicroEpoch:           argUint64(currentME),
		CurrentMicroEpochStartBlock: argUint64(startBlock),
		CurrentMicroEpochEndBlock:   argUint64(endBlock),
		LastAppliedMicroEpoch:       argUint64(readBigFromPosSys(e, head.StateRoot, keyMicroEpochAppliedStatus()).Uint64()),
		NominalWeightUnits:          argUint64(cfg.nominalWeightUnits),
		InactivityDecayBps:          argUint64(cfg.inactivityDecayBps),
		Validators:                  items,
	}, nil
}

func keyMicroEpochAppliedStatus() types.Hash {
	return types.BytesToHash(crypto.Keccak256([]byte("xgr.uptime.microepoch.last.applied")))
}

type microEpochConfigView struct {
	microEpochSize     uint64
	nominalWeightUnits uint64
	inactivityDecayBps uint64
}

func readMicroEpochConfigFromChain(engine map[string]interface{}) microEpochConfigView {
	return microEpochConfigView{
		microEpochSize:     firstConfigUint(engine, "microEpochSize", "microEpochSlots", "uptimeSlotsPerEpoch"),
		nominalWeightUnits: firstConfigUint(engine, "microEpochNominalWeightUnits", "nominalWeightUnits", "uptimeNominalWeightUnits"),
		inactivityDecayBps: firstConfigUint(engine, "microEpochInactivityDecayBps", "inactivityDecayBps", "uptimeInactivityDecayBps"),
	}
}

func firstConfigUint(root interface{}, keys ...string) uint64 {
	for _, key := range keys {
		if value, ok := findConfigValue(root, key); ok {
			if n, ok := coerceConfigUint(value); ok {
				return n
			}
		}
	}
	return 0
}

func findConfigValue(node interface{}, key string) (interface{}, bool) {
	switch v := node.(type) {
	case map[string]interface{}:
		if raw, ok := v[key]; ok {
			return raw, true
		}
		for _, child := range v {
			if raw, ok := findConfigValue(child, key); ok {
				return raw, true
			}
		}
	case []interface{}:
		for _, child := range v {
			if raw, ok := findConfigValue(child, key); ok {
				return raw, true
			}
		}
	}
	return nil, false
}

func coerceConfigUint(raw interface{}) (uint64, bool) {
	switch v := raw.(type) {
	case uint64:
		return v, true
	case uint:
		return uint64(v), true
	case uint32:
		return uint64(v), true
	case int:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case int64:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case int32:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case float64:
		if v < 0 || math.Trunc(v) != v {
			return 0, false
		}
		return uint64(v), true
	case float32:
		f := float64(v)
		if f < 0 || math.Trunc(f) != f {
			return 0, false
		}
		return uint64(f), true
	case json.Number:
		n, err := v.Int64()
		if err != nil || n < 0 {
			return 0, false
		}
		return uint64(n), true
	case string:
		t := strings.TrimSpace(v)
		if t == "" {
			return 0, false
		}
		base := 10
		if strings.HasPrefix(t, "0x") || strings.HasPrefix(t, "0X") {
			base = 16
			t = t[2:]
		}
		n, err := strconv.ParseUint(t, base, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}
