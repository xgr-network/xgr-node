package e2e

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/e2e/framework"
	"github.com/xgr-network/xgr-node/types"
)

type posOverviewE2E struct {
	BlockNumber       uint64 `json:"blockNumber"`
	MicroEpochSize    uint64 `json:"microEpochSize"`
	CurrentMicroEpoch uint64 `json:"currentMicroEpoch"`
	CurrentEpoch      uint64 `json:"currentEpoch"`
	MicroEpochStart   uint64 `json:"currentMicroEpochStartBlock"`
	MicroEpochEnd     uint64 `json:"currentMicroEpochEndBlock"`
	Validators        []struct {
		Address              types.Address `json:"address"`
		CurrentlyValidating  bool          `json:"currentlyValidating"`
		StakingActive        bool          `json:"stakingActive"`
		MicroNominalWeight   *uint64       `json:"microNominalWeight,omitempty"`
		MicroEffectiveWeight *uint64       `json:"microEffectiveWeight,omitempty"`
		MicroInactivity      *uint64       `json:"microInactivity,omitempty"`
	} `json:"validators"`
}

type pollAttempt struct {
	Overview *posOverviewE2E
	Raw      string
	Err      string
}

func TestPoS_MicroWeightDecayAndRestore(t *testing.T) {
	n := 4
	defaultBalance := framework.EthToWei(7_000_000)
	stakeAmount := framework.EthToWei(3_000_000)
	ibftManager := framework.NewIBFTServersManager(t, n, IBFTDirPrefix, func(_ int, config *framework.TestServerConfig) {
		config.SetMicroEpochConfig(10, 1, 10_000, 9_000)
		config.PremineValidatorBalance(defaultBalance)
		config.SetIBFTPoS(true)
		config.SetMinValidatorCount(3)
		config.SetMaxValidatorCount(uint64(n))
	})
	t.Cleanup(func() { ibftManager.StopServers() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ibftManager.StartServers(ctx)

	servers := make([]*framework.TestServer, 0, n)
	keys := make([]*ecdsa.PrivateKey, 0, n)
	addrs := make([]types.Address, 0, n)
	for i := 0; i < n; i++ {
		s := ibftManager.GetServer(i)
		k, err := s.Config.PrivateKey()
		require.NoError(t, err)
		servers = append(servers, s)
		keys = append(keys, k)
		addrs = append(addrs, crypto.PubKeyToAddress(&k.PublicKey))
		require.NoError(t, framework.StakeAmount(addrs[i], keys[i], stakeAmount, s))
	}
	require.Empty(t, framework.WaitForServersToSeal(servers, 20))

	history := make([]pollAttempt, 0, 5)
	appendHistory := func(a pollAttempt) {
		history = append(history, a)
		if len(history) > 5 {
			history = history[1:]
		}
	}
	logHistory := func() {
		for i, h := range history {
			if h.Overview == nil {
				t.Logf("overview[%d] err=%s raw=%s", i, h.Err, h.Raw)
				continue
			}
			o := h.Overview
			t.Logf("overview[%d] block=%d validators=%d err=%s raw=%s", i, o.BlockNumber, len(o.Validators), h.Err, h.Raw)
			for j, v := range o.Validators {
				t.Logf("overview[%d].validator[%d] addr=%s stakingActive=%t currentlyValidating=%t microNominalWeight=%s microEffectiveWeight=%s microInactivity=%s",
					i, j, v.Address.String(), v.StakingActive, v.CurrentlyValidating, optionalUint64(v.MicroNominalWeight), optionalUint64(v.MicroEffectiveWeight), optionalUint64(v.MicroInactivity))
			}
		}
	}
	observerIdx := 0
	poll := func(pred func(posOverviewE2E) bool, timeout time.Duration) posOverviewE2E {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			o, raw, err := getOverview(servers[observerIdx])
			if err != nil {
				appendHistory(pollAttempt{Raw: raw, Err: err.Error()})
				time.Sleep(2 * time.Second)
				continue
			}

			appendHistory(pollAttempt{Overview: &o, Raw: raw})
			if pred(o) {
				return o
			}
			time.Sleep(2 * time.Second)
		}
		logHistory()
		require.FailNow(t, "timeout waiting for overview condition")
		return posOverviewE2E{}
	}

	initial := poll(func(o posOverviewE2E) bool {
		if o.MicroEpochSize == 0 {
			return false
		}
		overviewIndexByAddr := make(map[types.Address]int, len(o.Validators))
		for i, v := range o.Validators {
			overviewIndexByAddr[v.Address] = i
		}
		missing := make([]types.Address, 0, len(addrs))
		for _, addr := range addrs {
			idx, ok := overviewIndexByAddr[addr]
			if !ok {
				missing = append(missing, addr)
				continue
			}
			v := o.Validators[idx]
			if !v.CurrentlyValidating {
				return false
			}
			if v.MicroNominalWeight == nil || *v.MicroNominalWeight == 0 || v.MicroEffectiveWeight == nil || *v.MicroEffectiveWeight != *v.MicroNominalWeight {
				return false
			}
			if v.MicroInactivity != nil && *v.MicroInactivity != 0 {
				return false
			}
		}
		if len(missing) > 0 {
			t.Logf("expected validators missing from overview: %v", missing)
			return false
		}
		return true
	}, 150*time.Second)
	offlineIdx := -1
	for i := range addrs {
		for _, v := range initial.Validators {
			if v.Address == addrs[i] && v.StakingActive && v.CurrentlyValidating {
				offlineIdx = i
				break
			}
		}
		if offlineIdx >= 0 {
			break
		}
	}
	if offlineIdx < 0 {
		t.Skip("no active+currentlyValidating validator found for offline decay scenario")
	}
	offlineAddr := addrs[offlineIdx]
	offlineStartEpoch := initial.CurrentMicroEpoch
	observerIdx = 0
	if offlineIdx == observerIdx {
		observerIdx = 1
	}
	t.Logf("micro-overview offline node index=%d addr=%s observer node index=%d", offlineIdx, offlineAddr, observerIdx)
	ibftManager.StopServer(offlineIdx)

	deactivated := poll(func(o posOverviewE2E) bool {
		if o.CurrentMicroEpoch <= offlineStartEpoch {
			return false
		}
		for _, v := range o.Validators {
			if v.CurrentlyValidating {
				if v.MicroNominalWeight == nil || *v.MicroNominalWeight == 0 || v.MicroEffectiveWeight == nil || v.MicroInactivity == nil {
					return false
				}
			}
		}
		for _, v := range o.Validators {
			if v.Address == offlineAddr {
				t.Logf("deactivate-wait block=%d currentEpoch=%d microEpochSize=%d currentMicroEpoch=%d offlineAddr=%s stakingActive=%t currentlyValidating=%t microNominalWeight=%s microEffectiveWeight=%s microInactivity=%s",
					o.BlockNumber, o.CurrentEpoch, o.MicroEpochSize, o.CurrentMicroEpoch, offlineAddr, v.StakingActive, v.CurrentlyValidating, optionalUint64(v.MicroNominalWeight), optionalUint64(v.MicroEffectiveWeight), optionalUint64(v.MicroInactivity))
				return !v.CurrentlyValidating || !v.StakingActive
			}
		}
		return false
	}, 120*time.Second)

	require.NoError(t, ibftManager.GetServer(offlineIdx).Start(context.Background()))
	afterRestart := poll(func(o posOverviewE2E) bool {
		if o.CurrentMicroEpoch <= deactivated.CurrentMicroEpoch {
			return false
		}
		for _, v := range o.Validators {
			if v.CurrentlyValidating {
				if v.MicroNominalWeight == nil || *v.MicroNominalWeight == 0 || v.MicroEffectiveWeight == nil || v.MicroInactivity == nil {
					return false
				}
			}
		}
		return true
	}, 150*time.Second)

	heightBefore := afterRestart.BlockNumber
	require.Eventually(t, func() bool {
		h, err := servers[0].JSONRPC().Eth().BlockNumber()
		return err == nil && h > heightBefore
	}, 60*time.Second, 2*time.Second)
}

func getOverview(srv *framework.TestServer) (posOverviewE2E, string, error) {
	var out posOverviewE2E
	var raw map[string]any
	err := srv.JSONRPC().Call("eth_getPosValidatorsOverview", &raw, nil)
	if err != nil {
		return out, "", fmt.Errorf("overview call failed: %w", err)
	}
	rawJSON, _ := json.Marshal(raw)
	blockNumber, err := parseHexUint64(raw, "blockNumber")
	if err != nil {
		return out, string(rawJSON), fmt.Errorf("decode blockNumber failed: %w", err)
	}
	microEpochSize, err := parseHexUint64(raw, "microEpochSize")
	if err != nil {
		return out, string(rawJSON), fmt.Errorf("decode microEpochSize failed: %w", err)
	}
	currentMicroEpoch, err := parseHexUint64(raw, "currentMicroEpoch")
	if err != nil {
		return out, string(rawJSON), fmt.Errorf("decode currentMicroEpoch failed: %w", err)
	}
	currentEpoch, err := parseHexUint64(raw, "currentEpoch")
	if err != nil {
		return out, string(rawJSON), fmt.Errorf("decode currentEpoch failed: %w", err)
	}
	microEpochStart, err := parseHexUint64(raw, "currentMicroEpochStartBlock")
	if err != nil {
		return out, string(rawJSON), fmt.Errorf("decode currentMicroEpochStartBlock failed: %w", err)
	}
	microEpochEnd, err := parseHexUint64(raw, "currentMicroEpochEndBlock")
	if err != nil {
		return out, string(rawJSON), fmt.Errorf("decode currentMicroEpochEndBlock failed: %w", err)
	}
	out.BlockNumber = blockNumber
	out.MicroEpochSize = microEpochSize
	out.CurrentMicroEpoch = currentMicroEpoch
	out.CurrentEpoch = currentEpoch
	out.MicroEpochStart = microEpochStart
	out.MicroEpochEnd = microEpochEnd

	rawValidators, ok := raw["validators"].([]any)
	if !ok {
		return out, string(rawJSON), fmt.Errorf("decode validators failed: expected array, got %T", raw["validators"])
	}
	out.Validators = make([]struct {
		Address              types.Address `json:"address"`
		CurrentlyValidating  bool          `json:"currentlyValidating"`
		StakingActive        bool          `json:"stakingActive"`
		MicroNominalWeight   *uint64       `json:"microNominalWeight,omitempty"`
		MicroEffectiveWeight *uint64       `json:"microEffectiveWeight,omitempty"`
		MicroInactivity      *uint64       `json:"microInactivity,omitempty"`
	}, 0, len(rawValidators))
	for i, rawValidator := range rawValidators {
		vm, ok := rawValidator.(map[string]any)
		if !ok {
			return out, string(rawJSON), fmt.Errorf("decode validator[%d] failed: expected object, got %T", i, rawValidator)
		}
		var v struct {
			Address              types.Address `json:"address"`
			CurrentlyValidating  bool          `json:"currentlyValidating"`
			StakingActive        bool          `json:"stakingActive"`
			MicroNominalWeight   *uint64       `json:"microNominalWeight,omitempty"`
			MicroEffectiveWeight *uint64       `json:"microEffectiveWeight,omitempty"`
			MicroInactivity      *uint64       `json:"microInactivity,omitempty"`
		}
		addressRaw, ok := vm["address"].(string)
		if !ok {
			return out, string(rawJSON), fmt.Errorf("decode validator[%d].address failed: expected string, got %T", i, vm["address"])
		}
		v.Address = types.StringToAddress(addressRaw)
		validating, ok := vm["currentlyValidating"].(bool)
		if !ok {
			return out, string(rawJSON), fmt.Errorf("decode validator[%d].currentlyValidating failed: expected bool, got %T", i, vm["currentlyValidating"])
		}
		v.CurrentlyValidating = validating
		if stakingActive, ok := vm["stakingActive"].(bool); ok {
			v.StakingActive = stakingActive
		}
		v.MicroNominalWeight, err = parseOptionalHexUint64(vm, "microNominalWeight")
		if err != nil {
			return out, string(rawJSON), fmt.Errorf("decode validator[%d].microNominalWeight failed: %w", i, err)
		}
		v.MicroEffectiveWeight, err = parseOptionalHexUint64(vm, "microEffectiveWeight")
		if err != nil {
			return out, string(rawJSON), fmt.Errorf("decode validator[%d].microEffectiveWeight failed: %w", i, err)
		}
		v.MicroInactivity, err = parseOptionalHexUint64(vm, "microInactivity")
		if err != nil {
			return out, string(rawJSON), fmt.Errorf("decode validator[%d].microInactivity failed: %w", i, err)
		}
		out.Validators = append(out.Validators, v)
	}

	return out, string(rawJSON), nil
}

func parseHexUint64(m map[string]any, field string) (uint64, error) {
	value, ok := m[field]
	if !ok {
		return 0, fmt.Errorf("missing field %q", field)
	}
	return decodeUint64(value)
}

func parseOptionalHexUint64(m map[string]any, field string) (*uint64, error) {
	value, ok := m[field]
	if !ok || value == nil {
		return nil, nil
	}
	v, err := decodeUint64(value)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func decodeUint64(value any) (uint64, error) {
	switch typed := value.(type) {
	case string:
		if strings.HasPrefix(typed, "0x") || strings.HasPrefix(typed, "0X") {
			v, err := strconv.ParseUint(typed[2:], 16, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid hex uint64 %q: %w", typed, err)
			}
			return v, nil
		}
		v, err := strconv.ParseUint(typed, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid decimal uint64 %q: %w", typed, err)
		}
		return v, nil
	case float64:
		return uint64(typed), nil
	default:
		return 0, fmt.Errorf("unsupported type %T", value)
	}
}

func optionalUint64(v *uint64) string {
	if v == nil {
		return "<nil>"
	}
	return strconv.FormatUint(*v, 10)
}
