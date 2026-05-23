package jsonrpc

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/contracts/abis"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/gasprice"
	"github.com/xgr-network/xgr-node/helper/progress"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/state/runtime"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

type posOverviewMockStore struct {
	head           *types.Header
	blocks         map[uint64]*types.Block
	receipt        map[types.Hash][]*types.Receipt
	stakes         map[types.Address]*big.Int
	activeFlags    map[types.Address]bool
	deactiveBlocks map[types.Address]uint64
	vals           []types.Address
	params         *chain.Params
	storage        map[types.Hash][]byte
	feePoolBalance *big.Int
	minValidators  uint64
	maxValidators  uint64
	threshold      *big.Int
	poolEnabled    map[types.Address]bool
	poolMax        map[types.Address]*big.Int
	poolCommission map[types.Address]uint16
	poolMin        map[types.Address]*big.Int
	stakerInfos    map[types.Address]map[string]interface{}
	delegatorsByV  map[types.Address][]types.Address
	joinedBlocks   map[types.Address]uint64
}

var _ ethStore = (*posOverviewMockStore)(nil)
var _ gasprice.GasStore = (*posOverviewMockStore)(nil)

func (m *posOverviewMockStore) Header() *types.Header { return m.head }
func (m *posOverviewMockStore) GetHeaderByNumber(n uint64) (*types.Header, bool) {
	b, ok := m.blocks[n]
	if !ok {
		return nil, false
	}
	return b.Header, true
}
func (m *posOverviewMockStore) GetBlockByHash(hash types.Hash, full bool) (*types.Block, bool) {
	for _, b := range m.blocks {
		if b.Hash() == hash {
			return b, true
		}
	}
	return nil, false
}
func (m *posOverviewMockStore) GetBlockByNumber(num uint64, full bool) (*types.Block, bool) {
	b, ok := m.blocks[num]
	return b, ok
}
func (m *posOverviewMockStore) ReadTxLookup(types.Hash) (types.Hash, bool) {
	return types.ZeroHash, false
}
func (m *posOverviewMockStore) GetReceiptsByHash(hash types.Hash) ([]*types.Receipt, error) {
	return m.receipt[hash], nil
}
func (m *posOverviewMockStore) GetAvgGasPrice() *big.Int { return big.NewInt(0) }
func (m *posOverviewMockStore) ApplyTxn(_ *types.Header, tx *types.Transaction, _ types.StateOverride, _ bool) (*runtime.ExecutionResult, error) {
	data := tx.Input
	if len(data) < 4 {
		return &runtime.ExecutionResult{ReturnValue: nil}, nil
	}
	joinedAtSelector := crypto.Keccak256([]byte("joinedAtBlock(address)"))[:4]
	if len(data) >= 36 && string(data[:4]) == string(joinedAtSelector) {
		addr := types.BytesToAddress(data[16:36])
		joinedAt := uint64(0)
		if m.joinedBlocks != nil {
			joinedAt = m.joinedBlocks[addr]
		}
		if joinedAt == 0 {
			if info := m.stakerInfos[addr]; info != nil {
				if v, ok := info["joinedAtBlock"].(*big.Int); ok && v != nil {
					joinedAt = v.Uint64()
				}
			}
		}
		return &runtime.ExecutionResult{ReturnValue: uint256Bytes(joinedAt)}, nil
	}

	sig := types.BytesToHash(data[:4])
	validatorsMethod := abis.StakingABI.Methods["validators"]
	accountStakeMethod := abis.StakingABI.Methods["accountStake"]
	minMethod := abis.StakingABI.Methods["minNumValidators"]
	maxMethod := abis.StakingABI.Methods["maxNumValidators"]
	thrMethod := abis.StakingABI.Methods["VALIDATOR_THRESHOLD"]
	validatorInfoMethod := abis.StakingABI.Methods["validatorInfo"]
	validatorSelfStakeMethod := abis.StakingABI.Methods["validatorSelfStake"]
	validatorDelegatedStakeRawMethod := abis.StakingABI.Methods["validatorDelegatedStakeRaw"]
	validatorDelegatedStakeActiveMethod := abis.StakingABI.Methods["validatorDelegatedStakeActive"]
	validatorStakerCountMethod := abis.StakingABI.Methods["validatorStakerCount"]
	validatorStakerAtMethod := abis.StakingABI.Methods["validatorStakerAt"]
	stakerInfoMethod := abis.StakingABI.Methods["stakerInfo"]
	validatorPoolMethod := abis.StakingABI.Methods["validatorPool"]
	joinedAtBlockMethod := abis.StakingABI.Methods["joinedAtBlock"]

	switch sig {
	case types.BytesToHash(validatorsMethod.ID()):
		enc, err := validatorsMethod.Outputs.Encode(map[string]interface{}{"0": toEthgoAddrs(m.vals)})
		if err != nil {
			return nil, err
		}
		return &runtime.ExecutionResult{ReturnValue: enc}, nil
	case types.BytesToHash(accountStakeMethod.ID()):
		decoded, err := accountStakeMethod.Inputs.Decode(data[4:])
		if err != nil {
			return nil, err
		}
		addrEth := decoded.(map[string]interface{})["account"].(ethgo.Address)
		addr := types.Address(addrEth)
		stake := m.stakes[addr]
		if stake == nil {
			stake = big.NewInt(0)
		}
		enc, err := accountStakeMethod.Outputs.Encode(map[string]interface{}{"0": stake})
		if err != nil {
			return nil, err
		}
		return &runtime.ExecutionResult{ReturnValue: enc}, nil
	case types.BytesToHash(minMethod.ID()):
		minV := m.minValidators
		if minV == 0 {
			minV = 1
		}
		enc, err := minMethod.Outputs.Encode(map[string]interface{}{"0": new(big.Int).SetUint64(minV)})
		if err != nil {
			return nil, err
		}
		return &runtime.ExecutionResult{ReturnValue: enc}, nil
	case types.BytesToHash(maxMethod.ID()):
		maxV := m.maxValidators
		if maxV == 0 {
			maxV = 100
		}
		enc, err := maxMethod.Outputs.Encode(map[string]interface{}{"0": new(big.Int).SetUint64(maxV)})
		if err != nil {
			return nil, err
		}
		return &runtime.ExecutionResult{ReturnValue: enc}, nil
	case types.BytesToHash(thrMethod.ID()):
		thr := m.threshold
		if thr == nil {
			thr = big.NewInt(500)
		}
		enc, err := thrMethod.Outputs.Encode(map[string]interface{}{"0": thr})
		if err != nil {
			return nil, err
		}
		return &runtime.ExecutionResult{ReturnValue: enc}, nil

	case types.BytesToHash(validatorInfoMethod.ID()):
		decoded, err := validatorInfoMethod.Inputs.Decode(data[4:])
		if err != nil {
			return nil, err
		}
		addrEth := decoded.(map[string]interface{})["account"].(ethgo.Address)
		addr := types.Address(addrEth)
		stake := m.stakes[addr]
		if stake == nil {
			stake = big.NewInt(0)
		}
		active := m.activeFlags[addr]
		deact := new(big.Int).SetUint64(m.deactiveBlocks[addr])
		enc, err := validatorInfoMethod.Outputs.Encode(map[string]interface{}{
			"exists":             true,
			"active":             active,
			"stakedAmount":       stake,
			"deactivatedAtBlock": deact,
			"blsPubKey":          []byte{},
		})
		if err != nil {
			return nil, err
		}
		return &runtime.ExecutionResult{ReturnValue: enc}, nil
	case types.BytesToHash(validatorSelfStakeMethod.ID()):
		decoded, err := validatorSelfStakeMethod.Inputs.Decode(data[4:])
		if err != nil {
			return nil, err
		}
		addr := types.Address(decoded.(map[string]interface{})["validator"].(ethgo.Address))
		stake := m.stakes[addr]
		if stake == nil {
			stake = big.NewInt(0)
		}
		enc, err := validatorSelfStakeMethod.Outputs.Encode(map[string]interface{}{"0": stake})
		if err != nil {
			return nil, err
		}
		return &runtime.ExecutionResult{ReturnValue: enc}, nil
	case types.BytesToHash(validatorDelegatedStakeRawMethod.ID()):
		decoded, err := validatorDelegatedStakeRawMethod.Inputs.Decode(data[4:])
		if err != nil {
			return nil, err
		}
		addr := types.Address(decoded.(map[string]interface{})["validator"].(ethgo.Address))
		total := big.NewInt(0)
		for _, d := range m.delegatorsByV[addr] {
			if d == addr {
				continue
			}
			info := m.stakerInfos[d]
			if info != nil {
				total.Add(total, info["amount"].(*big.Int))
			}
		}
		enc, err := validatorDelegatedStakeRawMethod.Outputs.Encode(map[string]interface{}{"0": total})
		if err != nil {
			return nil, err
		}
		return &runtime.ExecutionResult{ReturnValue: enc}, nil
	case types.BytesToHash(validatorDelegatedStakeActiveMethod.ID()):
		decoded, err := validatorDelegatedStakeActiveMethod.Inputs.Decode(data[4:])
		if err != nil {
			return nil, err
		}
		addr := types.Address(decoded.(map[string]interface{})["validator"].(ethgo.Address))
		total := big.NewInt(0)
		for _, d := range m.delegatorsByV[addr] {
			if d == addr {
				continue
			}
			info := m.stakerInfos[d]
			if info != nil && info["active"].(bool) {
				total.Add(total, info["amount"].(*big.Int))
			}
		}
		enc, err := validatorDelegatedStakeActiveMethod.Outputs.Encode(map[string]interface{}{"0": total})
		if err != nil {
			return nil, err
		}
		return &runtime.ExecutionResult{ReturnValue: enc}, nil
	case types.BytesToHash(validatorStakerCountMethod.ID()):
		decoded, err := validatorStakerCountMethod.Inputs.Decode(data[4:])
		if err != nil {
			return nil, err
		}
		addr := types.Address(decoded.(map[string]interface{})["validator"].(ethgo.Address))
		enc, err := validatorStakerCountMethod.Outputs.Encode(map[string]interface{}{"0": new(big.Int).SetUint64(uint64(len(m.delegatorsByV[addr])))})
		if err != nil {
			return nil, err
		}
		return &runtime.ExecutionResult{ReturnValue: enc}, nil
	case types.BytesToHash(validatorStakerAtMethod.ID()):
		decoded, err := validatorStakerAtMethod.Inputs.Decode(data[4:])
		if err != nil {
			return nil, err
		}
		addr := types.Address(decoded.(map[string]interface{})["validator"].(ethgo.Address))
		idx := decoded.(map[string]interface{})["idx"].(*big.Int).Uint64()
		delegators := m.delegatorsByV[addr]
		if idx >= uint64(len(delegators)) {
			return &runtime.ExecutionResult{ReturnValue: nil}, nil
		}
		enc, err := validatorStakerAtMethod.Outputs.Encode(map[string]interface{}{"0": ethgo.Address(delegators[idx])})
		if err != nil {
			return nil, err
		}
		return &runtime.ExecutionResult{ReturnValue: enc}, nil
	case types.BytesToHash(stakerInfoMethod.ID()):
		decoded, err := stakerInfoMethod.Inputs.Decode(data[4:])
		if err != nil {
			return nil, err
		}
		addr := types.Address(decoded.(map[string]interface{})["account"].(ethgo.Address))
		info := m.stakerInfos[addr]
		if info == nil {
			return &runtime.ExecutionResult{ReturnValue: nil}, nil
		}
		enc, err := stakerInfoMethod.Outputs.Encode(map[string]interface{}{"0": info})
		if err != nil {
			return nil, err
		}
		return &runtime.ExecutionResult{ReturnValue: enc}, nil
	case types.BytesToHash(validatorPoolMethod.ID()):
		decoded, err := validatorPoolMethod.Inputs.Decode(data[4:])
		if err != nil {
			return nil, err
		}
		addr := types.Address(decoded.(map[string]interface{})["validator"].(ethgo.Address))
		max := m.poolMax[addr]
		if max == nil {
			max = big.NewInt(0)
		}
		enc, err := validatorPoolMethod.Outputs.Encode(map[string]interface{}{"0": map[string]interface{}{
			"exists":                 true,
			"active":                 m.activeFlags[addr],
			"delegationEnabled":      m.poolEnabled[addr],
			"maxTotalDelegatedStake": max,
			"minDelegatorStake": func() *big.Int {
				if m.poolMin != nil && m.poolMin[addr] != nil {
					return m.poolMin[addr]
				}
				return big.NewInt(0)
			}(),
			"commissionBps": m.poolCommission[addr],
			"blsPubKey":     []byte{},
		}})
		if err != nil {
			return nil, err
		}
		return &runtime.ExecutionResult{ReturnValue: enc}, nil
	case types.BytesToHash(joinedAtBlockMethod.ID()):
		decoded, err := joinedAtBlockMethod.Inputs.Decode(data[4:])
		if err != nil {
			return nil, err
		}
		addr := types.Address(decoded.(map[string]interface{})["account"].(ethgo.Address))
		joined := big.NewInt(0)
		if info := m.stakerInfos[addr]; info != nil {
			if v, ok := info["joinedAtBlock"].(*big.Int); ok {
				joined = v
			}
		}
		enc, err := joinedAtBlockMethod.Outputs.Encode(map[string]interface{}{"0": joined})
		if err != nil {
			return nil, err
		}
		return &runtime.ExecutionResult{ReturnValue: enc}, nil
	default:
		return &runtime.ExecutionResult{ReturnValue: nil}, nil
	}
}
func (m *posOverviewMockStore) GetSyncProgression() *progress.Progression { return nil }
func (m *posOverviewMockStore) GetAccount(_ types.Hash, addr types.Address) (*Account, error) {
	balance := big.NewInt(0)
	if addr == state.FeePoolAddress && m.feePoolBalance != nil {
		balance = new(big.Int).Set(m.feePoolBalance)
	}
	return &Account{Balance: balance, Nonce: 0}, nil
}
func (m *posOverviewMockStore) GetStorage(_ types.Hash, _ types.Address, slot types.Hash) ([]byte, error) {
	if m.storage == nil {
		return nil, nil
	}
	return m.storage[slot], nil
}
func (m *posOverviewMockStore) GetForksInTime(uint64) chain.ForksInTime {
	return chain.AllForksEnabled.At(0)
}
func (m *posOverviewMockStore) GetCode(types.Hash, types.Address) ([]byte, error)  { return nil, nil }
func (m *posOverviewMockStore) GetChainParams() *chain.Params                      { return m.params }
func (m *posOverviewMockStore) AddTx(*types.Transaction) error                     { return nil }
func (m *posOverviewMockStore) GetPendingTx(types.Hash) (*types.Transaction, bool) { return nil, false }
func (m *posOverviewMockStore) GetNonce(types.Address) uint64                      { return 0 }
func (m *posOverviewMockStore) GetBaseFee() uint64                                 { return 0 }
func (m *posOverviewMockStore) FilterExtra(extra []byte) ([]byte, error)           { return extra, nil }
func (m *posOverviewMockStore) MaxPriorityFeePerGas() (*big.Int, error)            { return big.NewInt(0), nil }
func (m *posOverviewMockStore) FeeHistory(uint64, uint64, []float64) (*gasprice.FeeHistoryReturn, error) {
	return nil, nil
}

func TestResolveIBFTEpochSize_PoSAndLegacyBehavior(t *testing.T) {
	tests := []struct {
		name    string
		params  *chain.Params
		want    uint64
		wantErr bool
	}{
		{
			name: "PoS uses micro epoch product even when legacy epochSize exists",
			params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
				"epochSize":             float64(100000),
				"microEpochSize":        float64(10),
				"macroEpochMicroFactor": float64(5),
				"types":                 []interface{}{map[string]interface{}{"type": "PoS", "from": float64(1)}},
			}}},
			want: 50,
		},
		{
			name: "PoS with only legacy epochSize returns error",
			params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
				"epochSize": float64(100000),
				"types":     []interface{}{map[string]interface{}{"type": "PoS", "from": float64(1)}},
			}}},
			wantErr: true,
		},
		{
			name: "PoS with microEpochSize=0 returns error",
			params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
				"epochSize":             float64(100000),
				"microEpochSize":        float64(0),
				"macroEpochMicroFactor": float64(5),
				"types":                 []interface{}{map[string]interface{}{"type": "PoS", "from": float64(1)}},
			}}},
			wantErr: true,
		},
		{
			name: "PoS with macroEpochMicroFactor=0 returns error",
			params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
				"epochSize":             float64(100000),
				"microEpochSize":        float64(10),
				"macroEpochMicroFactor": float64(0),
				"types":                 []interface{}{map[string]interface{}{"type": "PoS", "from": float64(1)}},
			}}},
			wantErr: true,
		},
		{
			name: "Legacy non-PoS allows epochSize",
			params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
				"epochSize": float64(123),
				"types":     []interface{}{map[string]interface{}{"type": "PoA", "from": float64(0)}},
			}}},
			want: 123,
		},
		{
			name: "PoS micro epoch product overflow returns error",
			params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
				"microEpochSize":        float64(1 << 63),
				"macroEpochMicroFactor": float64(3),
				"types":                 []interface{}{map[string]interface{}{"type": "PoS", "from": float64(1)}},
			}}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveIBFTEpochSize(tt.params)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestEth_GetPosValidatorsOverview(t *testing.T) {
	v1 := types.StringToAddress("0x1000000000000000000000000000000000000001")
	v2 := types.StringToAddress("0x1000000000000000000000000000000000000002")
	v3 := types.StringToAddress("0x1000000000000000000000000000000000000003")
	d1 := types.StringToAddress("0x2000000000000000000000000000000000000001")
	d2 := types.StringToAddress("0x2000000000000000000000000000000000000002")
	d3 := types.StringToAddress("0x2000000000000000000000000000000000000003")

	b9 := newTestBlock(9, types.StringToHash("0x09"))
	b10 := newTestBlock(10, types.StringToHash("0x10"))

	store := &posOverviewMockStore{
		head: b10.Header,
		blocks: map[uint64]*types.Block{
			9:  b9,
			10: b10,
		},
		receipt: map[types.Hash][]*types.Receipt{
			b9.Hash():  {},
			b10.Hash(): {},
		},
		stakes: map[types.Address]*big.Int{
			v1: big.NewInt(100),
			v2: big.NewInt(600),
			v3: big.NewInt(600),
		},
		activeFlags:    map[types.Address]bool{v1: true, v2: false, v3: false},
		deactiveBlocks: map[types.Address]uint64{v2: 9, v3: 9},
		vals:           []types.Address{v1, v2},
		stakerInfos: map[types.Address]map[string]interface{}{
			d1: {"amount": big.NewInt(30), "active": true},
			d2: {"amount": big.NewInt(20), "active": false},
			d3: {"amount": big.NewInt(40), "active": true},
		},
		delegatorsByV: map[types.Address][]types.Address{
			v1: {d1, d2},
			v2: {d3},
		},
		params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
			"epochSize":             float64(5),
			"types":                 []interface{}{map[string]interface{}{"type": "PoS", "from": float64(8)}},
			"microEpochSize":        float64(1),
			"macroEpochMicroFactor": float64(5),
		}}},
		storage: map[types.Hash][]byte{
			keyProposerSlots(2, v2):   uint256Bytes(10),
			keyProposerMissed(2, v2):  uint256Bytes(3),
			keySlashed(2, v2):         uint256Bytes(1),
			keyRewardTotal(2):         uint256Bytes(777),
			keyValidatorReward(2, v2): uint256Bytes(42),
			keyEpochValidatorsLen(2):  uint256Bytes(1),
			keyEpochValidator(2, 0):   uint256Address(v2),
		},
		feePoolBalance: big.NewInt(333),
	}

	ep := &Eth{logger: hclog.NewNullLogger(), store: store}
	mode := "current"
	resIntf, err := ep.GetPosValidatorsOverview(&mode)
	require.NoError(t, err)

	res, ok := resIntf.(*posOverviewResponse)
	require.True(t, ok)
	require.Equal(t, argUint64(10), res.BlockNumber)
	require.Equal(t, "2bc", (*big.Int)(res.TotalCurrentStake).Text(16))
	require.Equal(t, (*big.Int)(res.TotalCurrentStake), (*big.Int)(res.TotalValidatorSelfStake))
	require.Equal(t, big.NewInt(90), (*big.Int)(res.TotalDelegatedRawStake))
	require.Equal(t, big.NewInt(70), (*big.Int)(res.TotalDelegatedActiveStake))
	require.Equal(t, big.NewInt(770), (*big.Int)(res.TotalActiveCurrentStake))
	require.Equal(t, argUint64(5), res.EpochSize)
	require.Equal(t, argUint64(2), res.CurrentEpoch)
	require.Equal(t, argUint64(2), res.LastFinalizedEpoch)
	require.Equal(t, argUint64(2), res.ReportedEpoch)
	require.Equal(t, argUint64(6), res.ReportedEpochStartBlock)
	require.Equal(t, argUint64(10), res.ReportedEpochEndBlock)
	require.NotNil(t, res.CurrentEpochPendingRewards)
	require.Equal(t, "14d", (*big.Int)(res.CurrentEpochPendingRewards).Text(16))

	require.True(t, res.PoSActive)
	require.NotNil(t, res.PoSFromBlock)
	require.Equal(t, argUint64(8), *res.PoSFromBlock)
	require.NotNil(t, res.RewardIneligibleCount)
	require.NotNil(t, res.SlashedCount)
	require.Equal(t, argUint64(1), *res.RewardIneligibleCount)
	require.Equal(t, argUint64(0), *res.SlashedCount)
	require.True(t, res.RewardIneligibleStatusExact)
	require.False(t, res.SlashStatusExact)
	require.False(t, res.LastRoundStakeExact)
	require.NotNil(t, res.LastRoundDistributedStake)
	require.Equal(t, "0", (*big.Int)(res.LastRoundDistributedStake).Text(16))
	validatorByAddress := make(map[types.Address]posValidatorOverview, len(res.Validators))
	for _, val := range res.Validators {
		validatorByAddress[val.Address] = val
	}

	val1, ok := validatorByAddress[v1]
	require.True(t, ok)
	require.Equal(t, big.NewInt(100), (*big.Int)(val1.SelfStake))
	require.Equal(t, big.NewInt(50), (*big.Int)(val1.DelegatedRawStake))
	require.Equal(t, big.NewInt(30), (*big.Int)(val1.DelegatedActiveStake))
	require.Equal(t, big.NewInt(130), (*big.Int)(val1.TotalActiveCurrentStake))

	var foundV2 bool
	for _, val := range res.Validators {
		if val.Address == v2 {
			foundV2 = true
			require.False(t, val.CurrentlyValidating)
			require.False(t, val.StakingActive)
			require.Equal(t, big.NewInt(600), (*big.Int)(val.SelfStake))
			require.Equal(t, big.NewInt(40), (*big.Int)(val.DelegatedRawStake))
			require.Equal(t, big.NewInt(40), (*big.Int)(val.DelegatedActiveStake))
			require.Equal(t, big.NewInt(640), (*big.Int)(val.TotalActiveCurrentStake))
			require.NotNil(t, val.DeactivatedAtBlock)
			require.Equal(t, argUint64(9), *val.DeactivatedAtBlock)
			require.NotNil(t, val.UnstakeAvailableAtBlock)
			require.Equal(t, argUint64(10), *val.UnstakeAvailableAtBlock)
			require.True(t, val.CanUnstakeNow)
			require.True(t, val.WasValidatorLastEpoch)
			require.Nil(t, val.ReportedEpochReward)
			require.NotNil(t, val.RewardIneligible)
			require.NotNil(t, val.Slashed)
			require.True(t, *val.RewardIneligible)
			require.False(t, *val.Slashed)
		}
	}
	require.True(t, foundV2)
}

func TestEth_GetPosValidatorsOverview_ProposalRollingUptimeWindows(t *testing.T) {
	v1 := types.StringToAddress("0x1000000000000000000000000000000000000011")
	v2 := types.StringToAddress("0x1000000000000000000000000000000000000012")
	v3 := types.StringToAddress("0x1000000000000000000000000000000000000013")
	b25 := newTestBlock(25, types.StringToHash("0x25"))

	store := &posOverviewMockStore{
		head:    b25.Header,
		blocks:  map[uint64]*types.Block{25: b25},
		receipt: map[types.Hash][]*types.Receipt{b25.Hash(): {}},
		stakes: map[types.Address]*big.Int{
			v1: big.NewInt(1000),
			v2: big.NewInt(2000),
			v3: big.NewInt(3000),
		},
		activeFlags:    map[types.Address]bool{v1: true, v2: true, v3: false},
		deactiveBlocks: map[types.Address]uint64{v3: 18},
		vals:           []types.Address{v1, v2, v3},
		params:         &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{"epochSize": float64(5), "microEpochSize": float64(1), "macroEpochMicroFactor": float64(5), "types": []interface{}{map[string]interface{}{"type": "PoS", "from": float64(1)}}}}},
		joinedBlocks: map[types.Address]uint64{
			v1: 0,
			v2: 15, // joinEffectiveAtBlock=20, epoch 4 is first relevant epoch
			v3: 0,
		},
		storage: map[types.Hash][]byte{
			// v1: last3 (3,4,5) => total=15 missed=3 => 80.00% ; last10 effectively same window with this chain height
			keyProposerSlots(3, v1):  uint256Bytes(5),
			keyProposerMissed(3, v1): uint256Bytes(1),
			keyProposerSlots(4, v1):  uint256Bytes(5),
			keyProposerMissed(4, v1): uint256Bytes(1),
			keyProposerSlots(5, v1):  uint256Bytes(5),
			keyProposerMissed(5, v1): uint256Bytes(1),

			// v2: epoch3 has duties but must be excluded due to joinEffectiveAtBlock=20
			keyProposerSlots(3, v2):  uint256Bytes(9),
			keyProposerMissed(3, v2): uint256Bytes(9),
			keyProposerSlots(4, v2):  uint256Bytes(2),
			keyProposerMissed(4, v2): uint256Bytes(0),
			keyProposerSlots(5, v2):  uint256Bytes(2),
			keyProposerMissed(5, v2): uint256Bytes(1),

			// v3: deactivation effective at block 20, epoch 5 must be excluded
			keyProposerSlots(3, v3):  uint256Bytes(1),
			keyProposerMissed(3, v3): uint256Bytes(0),
			keyProposerSlots(4, v3):  uint256Bytes(1),
			keyProposerMissed(4, v3): uint256Bytes(1),
			keyProposerSlots(5, v3):  uint256Bytes(10),
			keyProposerMissed(5, v3): uint256Bytes(0),
		},
	}

	ep := &Eth{logger: hclog.NewNullLogger(), store: store}
	mode := "current"
	resIntf, err := ep.GetPosValidatorsOverview(&mode)
	require.NoError(t, err)
	res := resIntf.(*posOverviewResponse)
	require.Len(t, res.Validators, 3)

	byAddr := map[types.Address]posValidatorOverview{}
	for _, val := range res.Validators {
		byAddr[val.Address] = val
	}

	require.NotNil(t, byAddr[v1].ProposalUptimeLast3EpochsBps)
	require.Equal(t, argUint64(8000), *byAddr[v1].ProposalUptimeLast3EpochsBps)
	require.Equal(t, argUint64(80), *byAddr[v1].ProposalUptimeLast3EpochsPercent)
	require.Equal(t, argUint64(15), byAddr[v1].ProposalUptimeLast3EpochsObserved)
	require.NotNil(t, byAddr[v1].ProposalUptimeLast10EpochsBps)
	require.Equal(t, argUint64(8000), *byAddr[v1].ProposalUptimeLast10EpochsBps)
	require.Equal(t, argUint64(15), byAddr[v1].ProposalUptimeLast10EpochsObserved)

	require.NotNil(t, byAddr[v2].JoinEffectiveAtBlock)
	require.Equal(t, argUint64(20), *byAddr[v2].JoinEffectiveAtBlock)
	require.NotNil(t, byAddr[v2].ProposalUptimeLast3EpochsBps)
	require.Equal(t, argUint64(7500), *byAddr[v2].ProposalUptimeLast3EpochsBps)
	require.Equal(t, argUint64(4), byAddr[v2].ProposalUptimeLast3EpochsObserved)

	require.NotNil(t, byAddr[v3].DeactivateEffectiveAtBlock)
	require.Equal(t, argUint64(20), *byAddr[v3].DeactivateEffectiveAtBlock)
	require.NotNil(t, byAddr[v3].ProposalUptimeLast3EpochsBps)
	require.Equal(t, argUint64(5000), *byAddr[v3].ProposalUptimeLast3EpochsBps)
	require.Equal(t, argUint64(2), byAddr[v3].ProposalUptimeLast3EpochsObserved)
}

func TestEth_GetPosValidatorsOverview_ProposalRollingUptime_NoObservedDuties(t *testing.T) {
	v1 := types.StringToAddress("0x1000000000000000000000000000000000000021")
	b25 := newTestBlock(25, types.StringToHash("0x26"))

	store := &posOverviewMockStore{
		head:           b25.Header,
		blocks:         map[uint64]*types.Block{25: b25},
		receipt:        map[types.Hash][]*types.Receipt{b25.Hash(): {}},
		stakes:         map[types.Address]*big.Int{v1: big.NewInt(1000)},
		activeFlags:    map[types.Address]bool{v1: true},
		deactiveBlocks: map[types.Address]uint64{},
		vals:           []types.Address{v1},
		params:         &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{"epochSize": float64(5), "microEpochSize": float64(1), "macroEpochMicroFactor": float64(5), "types": []interface{}{map[string]interface{}{"type": "PoS", "from": float64(1)}}}}},
	}

	ep := &Eth{logger: hclog.NewNullLogger(), store: store}
	mode := "current"
	resIntf, err := ep.GetPosValidatorsOverview(&mode)
	require.NoError(t, err)
	res := resIntf.(*posOverviewResponse)
	require.Len(t, res.Validators, 1)

	val := res.Validators[0]
	require.Nil(t, val.ProposalUptimeLast3EpochsBps)
	require.Nil(t, val.ProposalUptimeLast10EpochsBps)
	require.Nil(t, val.ProposalUptimeLast3EpochsPercent)
	require.Nil(t, val.ProposalUptimeLast10EpochsPercent)
	require.Equal(t, argUint64(0), val.ProposalUptimeLast3EpochsObserved)
	require.Equal(t, argUint64(0), val.ProposalUptimeLast10EpochsObserved)
}

func TestEth_GetPosValidatorsOverview_LastFinalized(t *testing.T) {
	v1 := types.StringToAddress("0x1000000000000000000000000000000000000001")
	b10 := newTestBlock(10, types.StringToHash("0x10"))

	store := &posOverviewMockStore{
		head:    b10.Header,
		blocks:  map[uint64]*types.Block{10: b10},
		receipt: map[types.Hash][]*types.Receipt{b10.Hash(): {}},
		stakes:  map[types.Address]*big.Int{v1: big.NewInt(100)},
		vals:    []types.Address{v1},
		params:  &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{"epochSize": float64(5), "microEpochSize": float64(1), "macroEpochMicroFactor": float64(5), "types": []interface{}{map[string]interface{}{"type": "PoS", "from": float64(8)}}}}},
		storage: map[types.Hash][]byte{
			keySlashed(2, v1): uint256Bytes(1),
		},
	}

	ep := &Eth{logger: hclog.NewNullLogger(), store: store}
	mode := "lastFinalized"
	resIntf, err := ep.GetPosValidatorsOverview(&mode)
	require.NoError(t, err)

	res := resIntf.(*posOverviewResponse)
	require.Equal(t, argUint64(2), res.ReportedEpoch)
	require.Equal(t, argUint64(6), res.ReportedEpochStartBlock)
	require.Equal(t, argUint64(10), res.ReportedEpochEndBlock)
}

func TestEth_GetPosValidatorsOverview_NoLegacyBeaconSummaryFields(t *testing.T) {
	v1 := types.StringToAddress("0x1000000000000000000000000000000000000001")
	b10 := newTestBlock(10, types.StringToHash("0x10"))

	store := &posOverviewMockStore{
		head:    b10.Header,
		blocks:  map[uint64]*types.Block{10: b10},
		receipt: map[types.Hash][]*types.Receipt{b10.Hash(): {}},
		stakes:  map[types.Address]*big.Int{v1: big.NewInt(100)},
		vals:    []types.Address{v1},
		params:  &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{"epochSize": float64(5), "microEpochSize": float64(1), "macroEpochMicroFactor": float64(5), "types": []interface{}{map[string]interface{}{"type": "PoS", "from": float64(8)}}}}},
		storage: map[types.Hash][]byte{},
	}

	ep := &Eth{logger: hclog.NewNullLogger(), store: store}
	resIntf, err := ep.GetPosValidatorsOverview(nil)
	require.NoError(t, err)
	res := resIntf.(*posOverviewResponse)
	raw, err := json.Marshal(res)
	require.NoError(t, err)

	out := map[string]interface{}{}
	require.NoError(t, json.Unmarshal(raw, &out))
	require.NotContains(t, out, "heartbeatHeight")
	require.NotContains(t, out, "beaconLastAppliedSlot")
	require.NotContains(t, out, "beaconLastAppliedEpoch")
	require.NotContains(t, out, "beaconCurrentWallclockSlot")
	require.NotContains(t, out, "beaconCurrentWallclockEpoch")
	require.NotContains(t, out, "beaconSlotDurationSec")
	require.NotContains(t, out, "beaconGenesisTimeUnix")
	require.NotContains(t, out, "beaconTimeHealthy")
	require.NotContains(t, out, "beaconTimeReason")
	require.NotContains(t, out, "beaconTimeSampleCount")
	require.NotContains(t, out, "recoveryTotalVotingPower")
	require.NotContains(t, out, "recoveryActiveVotingPower")
	require.NotContains(t, out, "recoveryViewSource")
}

func TestEth_GetPosValidatorsOverview_InvalidMode(t *testing.T) {
	b10 := newTestBlock(10, types.StringToHash("0x10"))
	store := &posOverviewMockStore{
		head:    b10.Header,
		blocks:  map[uint64]*types.Block{10: b10},
		receipt: map[types.Hash][]*types.Receipt{b10.Hash(): {}},
		stakes:  map[types.Address]*big.Int{},
		vals:    []types.Address{},
		params:  &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{"epochSize": float64(5), "microEpochSize": float64(1), "macroEpochMicroFactor": float64(5), "types": []interface{}{map[string]interface{}{"type": "PoS", "from": float64(8)}}}}},
	}

	ep := &Eth{logger: hclog.NewNullLogger(), store: store}
	mode := "foo"
	_, err := ep.GetPosValidatorsOverview(&mode)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid reportEpoch")
}

func TestEth_GetPosValidatorsOverview_CurrentlyValidating_UsesEpochSnapshot(t *testing.T) {
	v1 := types.StringToAddress("0x1000000000000000000000000000000000000001")
	v2 := types.StringToAddress("0x1000000000000000000000000000000000000002")
	b10 := newTestBlock(10, types.StringToHash("0x10"))
	extraCurrent := &signer.IstanbulExtra{
		Validators:           validators.NewECDSAValidatorSet(validators.NewECDSAValidator(v1)),
		ProposerSeal:         []byte{},
		CommittedSeals:       &signer.SerializedSeal{},
		ParentCommittedSeals: &signer.SerializedSeal{},
	}
	b10.Header.ExtraData = append(make([]byte, signer.IstanbulExtraVanity), extraCurrent.MarshalRLPTo(nil)...)

	store := &posOverviewMockStore{
		head:    b10.Header,
		blocks:  map[uint64]*types.Block{10: b10},
		receipt: map[types.Hash][]*types.Receipt{b10.Hash(): {}},
		stakes: map[types.Address]*big.Int{
			v1: big.NewInt(100),
			v2: big.NewInt(200),
		},
		activeFlags:    map[types.Address]bool{v1: true, v2: true},
		deactiveBlocks: map[types.Address]uint64{},
		vals:           []types.Address{v1, v2},
		params:         &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{"epochSize": float64(5), "microEpochSize": float64(1), "macroEpochMicroFactor": float64(5), "types": []interface{}{map[string]interface{}{"type": "PoS", "from": float64(8)}}}}},
		minValidators:  2,
		threshold:      big.NewInt(500),
		storage: map[types.Hash][]byte{
			keyEpochValidatorsLen(2): uint256Bytes(1),
			keyEpochValidator(2, 0):  uint256Address(v1),
		},
	}

	ep := &Eth{logger: hclog.NewNullLogger(), store: store}
	resIntf, err := ep.GetPosValidatorsOverview(nil)
	require.NoError(t, err)

	res := resIntf.(*posOverviewResponse)
	require.Len(t, res.Validators, 2)
	found := map[types.Address]bool{}
	wasLast := map[types.Address]bool{}
	for _, val := range res.Validators {
		found[val.Address] = val.CurrentlyValidating
		wasLast[val.Address] = val.WasValidatorLastEpoch
	}
	require.True(t, found[v1])
	require.False(t, found[v2])
	require.True(t, wasLast[v1])
	require.False(t, wasLast[v2])
}

func uint256Bytes(v uint64) []byte {
	out := make([]byte, 32)
	b := new(big.Int).SetUint64(v).Bytes()
	copy(out[32-len(b):], b)
	return out
}

func uint256Address(addr types.Address) []byte {
	out := make([]byte, 32)
	copy(out[12:], addr[:])
	return out
}

func toEthgoAddrs(addrs []types.Address) [][20]byte {
	res := make([][20]byte, len(addrs))
	for i := range addrs {
		res[i] = [20]byte(addrs[i])
	}
	return res
}

func TestEth_GetPosValidatorsOverview_PoSNotActive(t *testing.T) {
	b10 := newTestBlock(10, types.StringToHash("0x10"))

	store := &posOverviewMockStore{
		head:    b10.Header,
		blocks:  map[uint64]*types.Block{10: b10},
		receipt: map[types.Hash][]*types.Receipt{},
		stakes:  map[types.Address]*big.Int{},
		vals:    []types.Address{},
		params:  &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{"epochSize": float64(5), "microEpochSize": float64(1), "macroEpochMicroFactor": float64(5), "types": []interface{}{map[string]interface{}{"type": "PoS", "from": float64(100)}}}}},
	}

	ep := &Eth{logger: hclog.NewNullLogger(), store: store}
	_, err := ep.GetPosValidatorsOverview(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "PoS is not active yet")
}

func TestEth_GetPosValidatorsOverview_RewardIneligibleOnlyForEpochMembers(t *testing.T) {
	v1 := types.StringToAddress("0x1000000000000000000000000000000000000001")
	b10 := newTestBlock(10, types.StringToHash("0x10"))

	store := &posOverviewMockStore{
		head:    b10.Header,
		blocks:  map[uint64]*types.Block{10: b10},
		receipt: map[types.Hash][]*types.Receipt{b10.Hash(): {}},
		stakes:  map[types.Address]*big.Int{v1: big.NewInt(100)},
		vals:    []types.Address{v1},
		params:  &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{"epochSize": float64(5), "microEpochSize": float64(1), "macroEpochMicroFactor": float64(5), "types": []interface{}{map[string]interface{}{"type": "PoS", "from": float64(8)}}}}},
		storage: map[types.Hash][]byte{
			keyProposerSlots(2, v1):  uint256Bytes(10),
			keyProposerMissed(2, v1): uint256Bytes(10),
			keyEpochValidatorsLen(2): uint256Bytes(0), // v1 not in reported epoch validator set
		},
	}

	ep := &Eth{logger: hclog.NewNullLogger(), store: store}
	mode := "current"
	resIntf, err := ep.GetPosValidatorsOverview(&mode)
	require.NoError(t, err)

	res := resIntf.(*posOverviewResponse)
	require.Len(t, res.Validators, 1)
	require.NotNil(t, res.Validators[0].RewardIneligible)
	require.False(t, *res.Validators[0].RewardIneligible)
}

func TestEth_GetPosValidatorDelegators_ReturnsPoolConfig(t *testing.T) {
	validator := types.StringToAddress("0x2000000000000000000000000000000000000001")
	delegator := types.StringToAddress("0x3000000000000000000000000000000000000001")

	head := newTestBlock(12, types.StringToHash("0x12")).Header
	store := &posOverviewMockStore{
		head:           head,
		blocks:         map[uint64]*types.Block{12: newTestBlock(12, types.StringToHash("0x12"))},
		receipt:        map[types.Hash][]*types.Receipt{},
		stakes:         map[types.Address]*big.Int{validator: big.NewInt(2_000_000)},
		activeFlags:    map[types.Address]bool{validator: true},
		deactiveBlocks: map[types.Address]uint64{},
		vals:           []types.Address{validator},
		params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
			"microEpochSize":         float64(1),
			"macroEpochMicroFactor":  float64(5),
			"types": []interface{}{map[string]interface{}{
				"type":              "PoS",
				"validator_type":    "ecdsa",
				"from":              float64(0),
				"deployment":        float64(0),
				"minValidatorCount": float64(1),
				"maxValidatorCount": float64(100),
			}},
			"blockTrackerPollInterv": "1s",
		}}},
		poolEnabled:    map[types.Address]bool{validator: false},
		poolMax:        map[types.Address]*big.Int{validator: big.NewInt(55)},
		poolCommission: map[types.Address]uint16{validator: 321},
		delegatorsByV:  map[types.Address][]types.Address{validator: []types.Address{delegator}},
		stakerInfos: map[types.Address]map[string]interface{}{
			delegator: {
				"exists":             true,
				"active":             true,
				"index":              uint64(0),
				"amount":             big.NewInt(10),
				"joinedAtBlock":      big.NewInt(10),
				"deactivatedAtBlock": big.NewInt(0),
				"validator":          ethgo.Address(validator),
				"blsPubKey":          []byte{},
			},
		},
		storage: map[types.Hash][]byte{
			keyStakeSnapshot(2, validator):       uint256Bytes(200_000),
			keyStakerStakeSnapshot(2, validator): uint256Bytes(200_000),
		},
	}

	e := &Eth{store: store, logger: hclog.NewNullLogger()}
	respAny, err := e.GetPosValidatorDelegators(validator, nil)
	require.NoError(t, err)

	resp := respAny.(*posValidatorDelegatorsResponse)
	require.Equal(t, validator, resp.Validator)
	require.Equal(t, false, resp.DelegationEnabled)
	require.Equal(t, "55", ((*big.Int)(resp.MaxTotalDelegatedStake)).String())
	require.Equal(t, "0", ((*big.Int)(resp.MinDelegatorStake)).String())
	require.Equal(t, "10000000000000000000000", ((*big.Int)(resp.EffectiveMinDelegatorStake)).String())
	require.Equal(t, argUint64(321), resp.CommissionBps)
	require.Equal(t, "10", ((*big.Int)(resp.DelegatedActiveCurrent)).String())
	require.Equal(t, "2000010", ((*big.Int)(resp.TotalActiveCurrentStake)).String())
	require.Len(t, resp.Delegators, 1)
	require.Equal(t, delegator, resp.Delegators[0].Delegator)
}

func TestEth_GetPosValidatorDelegators_UsesRenamedCurrentStakeFieldsInJSON(t *testing.T) {
	validator := types.StringToAddress("0x2000000000000000000000000000000000000002")
	delegator := types.StringToAddress("0x3000000000000000000000000000000000000002")

	head := newTestBlock(20, types.StringToHash("0x20")).Header
	store := &posOverviewMockStore{
		head:           head,
		blocks:         map[uint64]*types.Block{20: newTestBlock(20, types.StringToHash("0x20"))},
		receipt:        map[types.Hash][]*types.Receipt{},
		stakes:         map[types.Address]*big.Int{validator: big.NewInt(200_000)},
		activeFlags:    map[types.Address]bool{validator: true},
		deactiveBlocks: map[types.Address]uint64{},
		vals:           []types.Address{validator},
		params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
			"microEpochSize":        float64(1),
			"macroEpochMicroFactor": float64(5),
			"types": []interface{}{map[string]interface{}{
				"type":           "PoS",
				"validator_type": "ecdsa",
				"from":           float64(0),
			}},
		}}},
		poolEnabled:    map[types.Address]bool{validator: true},
		poolMax:        map[types.Address]*big.Int{validator: big.NewInt(10_000)},
		poolCommission: map[types.Address]uint16{validator: 100},
		delegatorsByV:  map[types.Address][]types.Address{validator: []types.Address{delegator}},
		stakerInfos: map[types.Address]map[string]interface{}{
			delegator: {
				"exists":             true,
				"active":             true,
				"index":              uint64(0),
				"amount":             big.NewInt(10_000),
				"joinedAtBlock":      big.NewInt(10),
				"deactivatedAtBlock": big.NewInt(0),
				"validator":          ethgo.Address(validator),
				"blsPubKey":          []byte{},
			},
		},
	}

	e := &Eth{store: store, logger: hclog.NewNullLogger()}
	respAny, err := e.GetPosValidatorDelegators(validator, nil)
	require.NoError(t, err)

	raw, err := json.Marshal(respAny)
	require.NoError(t, err)
	rawStr := string(raw)
	require.Contains(t, rawStr, "delegatedActiveCurrent")
	require.Contains(t, rawStr, "totalActiveCurrentStake")
	require.NotContains(t, rawStr, "delegatedEffective")
	require.NotContains(t, rawStr, "effectiveTotalStake")
	require.Contains(t, rawStr, "effectiveAtPoint")
}

func TestPosValidatorDelegatorsResponse_JSONPoolFieldNames(t *testing.T) {
	resp := &posValidatorDelegatorsResponse{
		MaxTotalDelegatedStake:     (*argBig)(big.NewInt(1)),
		MinDelegatorStake:          (*argBig)(big.NewInt(2)),
		EffectiveMinDelegatorStake: (*argBig)(big.NewInt(3)),
	}

	raw, err := json.Marshal(resp)
	require.NoError(t, err)
	rawStr := string(raw)
	require.Contains(t, rawStr, "maxTotalDelegatedStake")
	require.Contains(t, rawStr, "minDelegatorStake")
	require.Contains(t, rawStr, "effectiveMinDelegatorStake")
	require.NotContains(t, rawStr, "maxDelegatedStake")
	require.NotContains(t, rawStr, "minDelegationStake")
	require.NotContains(t, rawStr, "effectiveMinDelegationStake")
}

func TestEth_GetPosValidatorDelegators_ExcludesSelfValidatorAndReturnsRewardFields(t *testing.T) {
	validator := types.StringToAddress("0x2200000000000000000000000000000000000002")
	delegator := types.StringToAddress("0x2300000000000000000000000000000000000002")
	b10 := newTestBlock(10, types.StringToHash("0x22"))

	store := &posOverviewMockStore{
		head:           b10.Header,
		blocks:         map[uint64]*types.Block{10: b10},
		receipt:        map[types.Hash][]*types.Receipt{b10.Hash(): {}},
		stakes:         map[types.Address]*big.Int{validator: big.NewInt(200_000), delegator: big.NewInt(10_000)},
		activeFlags:    map[types.Address]bool{validator: true},
		deactiveBlocks: map[types.Address]uint64{},
		vals:           []types.Address{validator},
		params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
			"epochSize":             float64(5),
			"microEpochSize":        float64(1),
			"macroEpochMicroFactor": float64(5),
			"types":                 []interface{}{map[string]interface{}{"type": "PoS", "from": float64(1)}},
		}}},
		delegatorsByV: map[types.Address][]types.Address{validator: []types.Address{validator, delegator}},
		stakerInfos: map[types.Address]map[string]interface{}{
			validator: {
				"exists":             true,
				"active":             true,
				"index":              uint64(0),
				"amount":             big.NewInt(200_000),
				"joinedAtBlock":      big.NewInt(1),
				"deactivatedAtBlock": big.NewInt(0),
				"validator":          ethgo.Address(validator),
				"blsPubKey":          []byte{},
			},
			delegator: {
				"exists":             true,
				"active":             true,
				"index":              uint64(1),
				"amount":             big.NewInt(10_000),
				"joinedAtBlock":      big.NewInt(8),
				"deactivatedAtBlock": big.NewInt(0),
				"validator":          ethgo.Address(validator),
				"blsPubKey":          []byte{},
			},
		},
		storage: map[types.Hash][]byte{
			keyDelegatorReward(2, validator, delegator): uint256Bytes(123),
		},
	}

	e := &Eth{store: store, logger: hclog.NewNullLogger()}
	mode := "current"
	respAny, err := e.GetPosValidatorDelegators(validator, &mode)
	require.NoError(t, err)

	resp := respAny.(*posValidatorDelegatorsResponse)
	require.Len(t, resp.Delegators, 1)
	require.Equal(t, delegator, resp.Delegators[0].Delegator)
	require.Nil(t, resp.Delegators[0].ReportedEpochReward)
}

func TestEth_GetPosValidatorDelegators_LastFinalizedUsesEpochEffectiveStake(t *testing.T) {
	validator := types.StringToAddress("0x2400000000000000000000000000000000000002")
	liveOnlyDelegatorA := types.StringToAddress("0x2500000000000000000000000000000000000002")
	liveOnlyDelegatorB := types.StringToAddress("0x2500000000000000000000000000000000000003")
	b20 := newTestBlock(20, types.StringToHash("0x24"))
	store := &posOverviewMockStore{
		head:           b20.Header,
		blocks:         map[uint64]*types.Block{20: b20},
		receipt:        map[types.Hash][]*types.Receipt{b20.Hash(): {}},
		stakes:         map[types.Address]*big.Int{validator: big.NewInt(200_000), liveOnlyDelegatorA: big.NewInt(10_000), liveOnlyDelegatorB: big.NewInt(7_000)},
		activeFlags:    map[types.Address]bool{validator: true},
		deactiveBlocks: map[types.Address]uint64{},
		vals:           []types.Address{validator},
		params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
			"epochSize":             float64(10),
			"microEpochSize":        float64(2),
			"macroEpochMicroFactor": float64(5),
			"types":                 []interface{}{map[string]interface{}{"type": "PoS", "from": float64(1)}},
		}}},
		delegatorsByV: map[types.Address][]types.Address{validator: {liveOnlyDelegatorA, liveOnlyDelegatorB}},
		stakerInfos: map[types.Address]map[string]interface{}{
			validator: {
				"exists": true, "active": true, "index": uint64(0), "amount": big.NewInt(200_000),
				"joinedAtBlock": big.NewInt(1), "deactivatedAtBlock": big.NewInt(0), "validator": ethgo.Address(validator), "blsPubKey": []byte{},
			},
			liveOnlyDelegatorA: {
				"exists": true, "active": true, "index": uint64(1), "amount": big.NewInt(10_000),
				"joinedAtBlock": big.NewInt(15), "deactivatedAtBlock": big.NewInt(0), "validator": ethgo.Address(validator), "blsPubKey": []byte{},
			},
			liveOnlyDelegatorB: {
				"exists": true, "active": true, "index": uint64(2), "amount": big.NewInt(7_000),
				"joinedAtBlock": big.NewInt(19), "deactivatedAtBlock": big.NewInt(0), "validator": ethgo.Address(validator), "blsPubKey": []byte{},
			},
		},
		storage: map[types.Hash][]byte{
			keyStakerStakeSnapshot(2, validator):          uint256Bytes(123_456),
			keyStakeSnapshot(2, validator):                uint256Bytes(222_222),
			keyStakerStakeSnapshot(2, liveOnlyDelegatorA): uint256Bytes(3_000),
		},
	}

	e := &Eth{store: store, logger: hclog.NewNullLogger()}
	mode := "lastFinalized"
	respAny, err := e.GetPosValidatorDelegators(validator, &mode)
	require.NoError(t, err)
	resp := respAny.(*posValidatorDelegatorsResponse)
	require.Equal(t, "3000", ((*big.Int)(resp.DelegatedEpochEffective)).String())
	require.Equal(t, "123456", ((*big.Int)(resp.SelfStakeEpochEffective)).String())
	require.Equal(t, "222222", ((*big.Int)(resp.TotalEpochEffectiveStake)).String())
	require.Equal(t, "217000", ((*big.Int)(resp.TotalLiveStake)).String())
	require.Len(t, resp.Delegators, 2)
	require.True(t, resp.Delegators[0].EffectiveAtPoint)
	require.Equal(t, "3000", ((*big.Int)(resp.Delegators[0].EpochEffectiveAmount)).String())
	require.False(t, resp.Delegators[1].EffectiveAtPoint)
	require.Equal(t, "0", ((*big.Int)(resp.Delegators[1].EpochEffectiveAmount)).String())
}

func TestEth_GetPosValidatorDelegators_LastFinalizedKeepsDeactivatedDelegatorEffectiveForFinalizedEpoch(t *testing.T) {
	validator := types.StringToAddress("0x2600000000000000000000000000000000000002")
	delegator := types.StringToAddress("0x2700000000000000000000000000000000000002")
	b20 := newTestBlock(20, types.StringToHash("0x25"))
	store := &posOverviewMockStore{
		head:           b20.Header,
		blocks:         map[uint64]*types.Block{20: b20},
		receipt:        map[types.Hash][]*types.Receipt{b20.Hash(): {}},
		stakes:         map[types.Address]*big.Int{validator: big.NewInt(200_000), delegator: big.NewInt(10_000)},
		activeFlags:    map[types.Address]bool{validator: true, delegator: false},
		deactiveBlocks: map[types.Address]uint64{delegator: 19},
		vals:           []types.Address{validator},
		params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
			"epochSize":             float64(10),
			"microEpochSize":        float64(2),
			"macroEpochMicroFactor": float64(5),
			"types":                 []interface{}{map[string]interface{}{"type": "PoS", "from": float64(1)}},
		}}},
		delegatorsByV: map[types.Address][]types.Address{validator: {delegator}},
		stakerInfos: map[types.Address]map[string]interface{}{
			validator: {
				"exists": true, "active": true, "index": uint64(0), "amount": big.NewInt(200_000),
				"joinedAtBlock": big.NewInt(1), "deactivatedAtBlock": big.NewInt(0), "validator": ethgo.Address(validator), "blsPubKey": []byte{},
			},
			delegator: {
				"exists": true, "active": false, "index": uint64(1), "amount": big.NewInt(10_000),
				"joinedAtBlock": big.NewInt(8), "deactivatedAtBlock": big.NewInt(19), "validator": ethgo.Address(validator), "blsPubKey": []byte{},
			},
		},
		storage: map[types.Hash][]byte{
			keyStakeSnapshot(2, validator):       uint256Bytes(210_000),
			keyStakerStakeSnapshot(2, validator): uint256Bytes(200_000),
			keyStakerStakeSnapshot(2, delegator): uint256Bytes(10_000),
		},
	}

	e := &Eth{store: store, logger: hclog.NewNullLogger()}
	mode := "lastFinalized"
	respAny, err := e.GetPosValidatorDelegators(validator, &mode)
	require.NoError(t, err)
	resp := respAny.(*posValidatorDelegatorsResponse)
	require.Equal(t, "10000", ((*big.Int)(resp.DelegatedEpochEffective)).String())
	require.Equal(t, "210000", ((*big.Int)(resp.TotalEpochEffectiveStake)).String())
	require.Equal(t, "200000", ((*big.Int)(resp.TotalLiveStake)).String())
	require.Len(t, resp.Delegators, 1)
	require.True(t, resp.Delegators[0].EffectiveAtPoint)
}

func TestEth_GetPosValidatorDelegators_LastFinalizedDoesNotFallbackWhenTotalSnapshotMissing(t *testing.T) {
	validator := types.StringToAddress("0x2600000000000000000000000000000000000003")
	delegator := types.StringToAddress("0x2700000000000000000000000000000000000003")
	b20 := newTestBlock(20, types.StringToHash("0x26"))
	store := &posOverviewMockStore{
		head:           b20.Header,
		blocks:         map[uint64]*types.Block{20: b20},
		receipt:        map[types.Hash][]*types.Receipt{b20.Hash(): {}},
		stakes:         map[types.Address]*big.Int{validator: big.NewInt(200_000), delegator: big.NewInt(10_000)},
		activeFlags:    map[types.Address]bool{validator: true, delegator: false},
		deactiveBlocks: map[types.Address]uint64{delegator: 19},
		vals:           []types.Address{validator},
		params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
			"epochSize":             float64(10),
			"microEpochSize":        float64(2),
			"macroEpochMicroFactor": float64(5),
			"types":                 []interface{}{map[string]interface{}{"type": "PoS", "from": float64(1)}},
		}}},
		delegatorsByV: map[types.Address][]types.Address{validator: {delegator}},
		stakerInfos: map[types.Address]map[string]interface{}{
			validator: {"exists": true, "active": true, "index": uint64(0), "amount": big.NewInt(200_000), "joinedAtBlock": big.NewInt(1), "deactivatedAtBlock": big.NewInt(0), "validator": ethgo.Address(validator), "blsPubKey": []byte{}},
			delegator: {"exists": true, "active": false, "index": uint64(1), "amount": big.NewInt(10_000), "joinedAtBlock": big.NewInt(8), "deactivatedAtBlock": big.NewInt(19), "validator": ethgo.Address(validator), "blsPubKey": []byte{}},
		},
		storage: map[types.Hash][]byte{
			keyStakerStakeSnapshot(2, validator): uint256Bytes(200_000),
			keyStakerStakeSnapshot(2, delegator): uint256Bytes(10_000),
		},
	}

	e := &Eth{store: store, logger: hclog.NewNullLogger()}
	mode := "lastFinalized"
	respAny, err := e.GetPosValidatorDelegators(validator, &mode)
	require.NoError(t, err)
	resp := respAny.(*posValidatorDelegatorsResponse)
	require.Equal(t, "10000", ((*big.Int)(resp.DelegatedEpochEffective)).String())
	require.Equal(t, "200000", ((*big.Int)(resp.SelfStakeEpochEffective)).String())
	require.Equal(t, "0", ((*big.Int)(resp.TotalEpochEffectiveStake)).String())
}

func TestEth_GetPosValidatorsOverview_ExcludesDelegatorsFromValidatorList(t *testing.T) {
	validator := types.StringToAddress("0x4000000000000000000000000000000000000001")
	delegator := types.StringToAddress("0x4000000000000000000000000000000000000002")
	b10 := newTestBlock(10, types.StringToHash("0x10"))

	store := &posOverviewMockStore{
		head:           b10.Header,
		blocks:         map[uint64]*types.Block{10: b10},
		receipt:        map[types.Hash][]*types.Receipt{b10.Hash(): {}},
		stakes:         map[types.Address]*big.Int{validator: big.NewInt(200_000), delegator: big.NewInt(10_000)},
		activeFlags:    map[types.Address]bool{validator: true, delegator: true},
		deactiveBlocks: map[types.Address]uint64{},
		vals:           []types.Address{validator},
		params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
			"epochSize":             float64(5),
			"microEpochSize":        float64(1),
			"macroEpochMicroFactor": float64(5),
			"types":                 []interface{}{map[string]interface{}{"type": "PoS", "from": float64(1)}},
		}}},
		delegatorsByV: map[types.Address][]types.Address{validator: []types.Address{delegator}},
		stakerInfos: map[types.Address]map[string]interface{}{
			delegator: {
				"exists":             true,
				"active":             true,
				"index":              uint64(0),
				"amount":             big.NewInt(10_000),
				"joinedAtBlock":      big.NewInt(8),
				"deactivatedAtBlock": big.NewInt(0),
				"validator":          ethgo.Address(validator),
				"blsPubKey":          []byte{},
			},
		},
	}

	ep := &Eth{logger: hclog.NewNullLogger(), store: store}
	resAny, err := ep.GetPosValidatorsOverview(nil)
	require.NoError(t, err)

	res := resAny.(*posOverviewResponse)
	require.Len(t, res.Validators, 1)
	require.Equal(t, validator, res.Validators[0].Address)
}

func TestEth_GetPosValidatorsOverview_ProvidesMicroEpochFields(t *testing.T) {
	validator := types.StringToAddress("0x6000000000000000000000000000000000000001")
	b10 := newTestBlock(10, types.StringToHash("0x10"))

	store := &posOverviewMockStore{
		head:           b10.Header,
		blocks:         map[uint64]*types.Block{10: b10},
		receipt:        map[types.Hash][]*types.Receipt{b10.Hash(): {}},
		stakes:         map[types.Address]*big.Int{validator: big.NewInt(2_000_000)},
		activeFlags:    map[types.Address]bool{validator: true},
		deactiveBlocks: map[types.Address]uint64{},
		vals:           []types.Address{validator},
		params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
			"epochSize":                    float64(5),
			"microEpochSize":               float64(4),
			"macroEpochMicroFactor":        float64(5),
			"microEpochNominalWeightUnits": float64(10000),
			"types":                        []interface{}{map[string]interface{}{"type": "PoS", "from": float64(1)}},
		}}},
	}
	store.storage = map[types.Hash][]byte{
		keyUptimeNominalWeight(validator):   uint256Bytes(100),
		keyUptimeEffectiveWeight(validator): uint256Bytes(100),
		keyUptimeInactivity(validator):      uint256Bytes(0),
	}

	ep := &Eth{logger: hclog.NewNullLogger(), store: store}
	resAny, err := ep.GetPosValidatorsOverview(nil)
	require.NoError(t, err)

	res := resAny.(*posOverviewResponse)
	require.Equal(t, argUint64(4), res.MicroEpochSize)
	require.Equal(t, argUint64(2), res.CurrentMicroEpoch)
	require.Equal(t, argUint64(9), res.CurrentMicroEpochStartBlock)
	require.Equal(t, argUint64(12), res.CurrentMicroEpochEndBlock)
	require.Len(t, res.Validators, 1)
	require.NotNil(t, res.Validators[0].MicroNominalWeight)
	require.NotNil(t, res.Validators[0].MicroEffectiveWeight)
	require.Equal(t, argUint64(100), *res.Validators[0].MicroNominalWeight)
	require.Equal(t, argUint64(100), *res.Validators[0].MicroEffectiveWeight)
	require.NotNil(t, res.Validators[0].MicroInactivity)
	require.Equal(t, argUint64(0), *res.Validators[0].MicroInactivity)

	payload, err := json.Marshal(res)
	require.NoError(t, err)
	text := string(payload)
	for _, forbidden := range []string{
		"effectiveStake", "votingPower", "totalVotingPower", "weightedQuorumThreshold",
		"beaconNominalWeight", "beaconEffectiveWeight", "beaconInactivity",
		"recoveryVotingPower", "recoveryTotalVotingPower", "recoveryActiveVotingPower", "recoveryViewSource",
		"beaconTimeHealthy", "beaconTimeReason", "beaconTimeSampleCount",
	} {
		require.NotContains(t, text, forbidden)
	}
}

func TestEth_GetPosValidatorsOverview_MicroWeights_DefaultWhenStateMissing(t *testing.T) {
	validator := types.StringToAddress("0x6000000000000000000000000000000000000002")
	b10 := newTestBlock(10, types.StringToHash("0x11"))

	store := &posOverviewMockStore{
		head:           b10.Header,
		blocks:         map[uint64]*types.Block{10: b10},
		receipt:        map[types.Hash][]*types.Receipt{b10.Hash(): {}},
		stakes:         map[types.Address]*big.Int{validator: big.NewInt(2_000_000)},
		activeFlags:    map[types.Address]bool{validator: true},
		deactiveBlocks: map[types.Address]uint64{},
		vals:           []types.Address{validator},
		params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
			"epochSize":                    float64(5),
			"microEpochSize":               float64(4),
			"macroEpochMicroFactor":        float64(5),
			"microEpochNominalWeightUnits": float64(10000),
			"types":                        []interface{}{map[string]interface{}{"type": "PoS", "from": float64(1)}},
		}}},
	}
	store.storage = map[types.Hash][]byte{}

	ep := &Eth{logger: hclog.NewNullLogger(), store: store}
	resAny, err := ep.GetPosValidatorsOverview(nil)
	require.NoError(t, err)

	res := resAny.(*posOverviewResponse)
	require.Len(t, res.Validators, 1)
	require.Nil(t, res.Validators[0].MicroNominalWeight)
	require.Nil(t, res.Validators[0].MicroEffectiveWeight)
	require.Nil(t, res.Validators[0].MicroInactivity)

	out, err := json.Marshal(res)
	require.NoError(t, err)
	require.NotContains(t, string(out), "beaconNominalWeight")
	require.NotContains(t, string(out), "votingPower")
}

func TestEth_GetPosValidatorsOverview_MicroEpochMissingConfigLeavesFieldsZero(t *testing.T) {
	validator := types.StringToAddress("0x6000000000000000000000000000000000000004")
	b10 := newTestBlock(10, types.StringToHash("0x13"))
	store := &posOverviewMockStore{
		head:        b10.Header,
		blocks:      map[uint64]*types.Block{10: b10},
		receipt:     map[types.Hash][]*types.Receipt{b10.Hash(): {}},
		stakes:      map[types.Address]*big.Int{validator: big.NewInt(2_000_000)},
		activeFlags: map[types.Address]bool{validator: true},
		vals:        []types.Address{validator},
		params:      &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{"epochSize": float64(5)}}},
	}
	ep := &Eth{logger: hclog.NewNullLogger(), store: store}
	resAny, err := ep.GetPosValidatorsOverview(nil)
	require.NoError(t, err)
	res := resAny.(*posOverviewResponse)
	require.Equal(t, argUint64(0), res.MicroEpochSize)
	require.Equal(t, argUint64(0), res.CurrentMicroEpoch)
	require.Equal(t, argUint64(0), res.CurrentMicroEpochStartBlock)
	require.Equal(t, argUint64(0), res.CurrentMicroEpochEndBlock)
}

func TestEth_GetPosValidatorsOverview_MicroEpochConfigTooSmallIsRejected(t *testing.T) {
	v1 := types.StringToAddress("0x6000000000000000000000000000000000000005")
	v2 := types.StringToAddress("0x6000000000000000000000000000000000000006")
	b10 := newTestBlock(10, types.StringToHash("0x14"))
	store := &posOverviewMockStore{
		head:        b10.Header,
		blocks:      map[uint64]*types.Block{10: b10},
		receipt:     map[types.Hash][]*types.Receipt{b10.Hash(): {}},
		stakes:      map[types.Address]*big.Int{v1: big.NewInt(2_000_000), v2: big.NewInt(2_000_000)},
		activeFlags: map[types.Address]bool{v1: true, v2: true},
		vals:        []types.Address{v1, v2},
		params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
			"epochSize":      float64(5),
			"microEpochSize": float64(1),
		}}},
	}
	ep := &Eth{logger: hclog.NewNullLogger(), store: store}
	resAny, err := ep.GetPosValidatorsOverview(nil)
	require.NoError(t, err)
	res := resAny.(*posOverviewResponse)
	require.Equal(t, argUint64(0), res.MicroEpochSize)
}

func TestEth_GetPosValidatorsOverview_MicroWeights_DecayFromState(t *testing.T) {
	validator := types.StringToAddress("0x6000000000000000000000000000000000000003")
	b10 := newTestBlock(10, types.StringToHash("0x12"))

	store := &posOverviewMockStore{
		head:           b10.Header,
		blocks:         map[uint64]*types.Block{10: b10},
		receipt:        map[types.Hash][]*types.Receipt{b10.Hash(): {}},
		stakes:         map[types.Address]*big.Int{validator: big.NewInt(2_000_000)},
		activeFlags:    map[types.Address]bool{validator: true},
		deactiveBlocks: map[types.Address]uint64{},
		vals:           []types.Address{validator},
		params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
			"epochSize":                    float64(5),
			"microEpochSize":               float64(4),
			"macroEpochMicroFactor":        float64(5),
			"microEpochNominalWeightUnits": float64(10000),
			"types":                        []interface{}{map[string]interface{}{"type": "PoS", "from": float64(1)}},
		}}},
	}
	store.storage = map[types.Hash][]byte{
		keyUptimeNominalWeight(validator):   uint256Bytes(10000),
		keyUptimeEffectiveWeight(validator): uint256Bytes(5000),
		keyUptimeInactivity(validator):      uint256Bytes(1),
	}

	ep := &Eth{logger: hclog.NewNullLogger(), store: store}
	resAny, err := ep.GetPosValidatorsOverview(nil)
	require.NoError(t, err)
	res := resAny.(*posOverviewResponse)
	require.Len(t, res.Validators, 1)
	require.Equal(t, argUint64(10000), *res.Validators[0].MicroNominalWeight)
	require.Equal(t, argUint64(5000), *res.Validators[0].MicroEffectiveWeight)
	require.Equal(t, argUint64(1), *res.Validators[0].MicroInactivity)
}

func TestEth_GetPosValidatorsOverview_ProvidesEpochTimingFields(t *testing.T) {
	validator := types.StringToAddress("0x5000000000000000000000000000000000000001")
	b10 := newTestBlock(10, types.StringToHash("0x10"))

	store := &posOverviewMockStore{
		head:           b10.Header,
		blocks:         map[uint64]*types.Block{10: b10},
		receipt:        map[types.Hash][]*types.Receipt{b10.Hash(): {}},
		stakes:         map[types.Address]*big.Int{validator: big.NewInt(2_000_000)},
		activeFlags:    map[types.Address]bool{validator: false},
		deactiveBlocks: map[types.Address]uint64{validator: 9},
		vals:           []types.Address{validator},
		params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
			"epochSize":             float64(5),
			"microEpochSize":        float64(1),
			"macroEpochMicroFactor": float64(5),
			"types":                 []interface{}{map[string]interface{}{"type": "PoS", "from": float64(1)}},
		}}},
		stakerInfos: map[types.Address]map[string]interface{}{
			validator: {
				"exists":             true,
				"active":             false,
				"index":              uint64(0),
				"amount":             big.NewInt(2_000_000),
				"joinedAtBlock":      big.NewInt(8),
				"deactivatedAtBlock": big.NewInt(9),
				"validator":          ethgo.Address(validator),
				"blsPubKey":          []byte{},
			},
		},
	}

	ep := &Eth{logger: hclog.NewNullLogger(), store: store}
	resAny, err := ep.GetPosValidatorsOverview(nil)
	require.NoError(t, err)
	res := resAny.(*posOverviewResponse)
	require.Len(t, res.Validators, 1)
	v := res.Validators[0]
	require.NotNil(t, v.JoinedAtBlock)
	require.NotNil(t, v.JoinEffectiveAtBlock)
	require.NotNil(t, v.DeactivatedAtBlock)
	require.NotNil(t, v.DeactivateEffectiveAtBlock)
	require.NotNil(t, v.UnstakeAvailableAtBlock)
	require.Equal(t, argUint64(8), *v.JoinedAtBlock)
	require.Equal(t, argUint64(10), *v.JoinEffectiveAtBlock)
	require.Equal(t, argUint64(9), *v.DeactivatedAtBlock)
	require.Equal(t, argUint64(10), *v.DeactivateEffectiveAtBlock)
	require.Equal(t, argUint64(10), *v.UnstakeAvailableAtBlock)
	require.True(t, v.CanUnstakeNow)
}

func TestEth_GetPosValidatorDelegators_UsesCustomEffectiveMinDelegatorStake(t *testing.T) {
	validator := types.StringToAddress("0x1111000000000000000000000000000000000001")
	head := newTestBlock(20, types.StringToHash("0x99")).Header
	custom, _ := new(big.Int).SetString("123000000000000000000000", 10)
	store := &posOverviewMockStore{
		head:           head,
		blocks:         map[uint64]*types.Block{20: newTestBlock(20, types.StringToHash("0x99"))},
		receipt:        map[types.Hash][]*types.Receipt{},
		stakes:         map[types.Address]*big.Int{validator: big.NewInt(200_000)},
		activeFlags:    map[types.Address]bool{validator: true},
		vals:           []types.Address{validator},
		params: &chain.Params{Engine: map[string]interface{}{"ibft": map[string]interface{}{
			"microEpochSize":        float64(1),
			"macroEpochMicroFactor": float64(5),
			"types": []interface{}{map[string]interface{}{
				"type":           "PoS",
				"validator_type": "ecdsa",
				"from":           float64(0),
			}},
		}}},
		poolEnabled:    map[types.Address]bool{validator: true},
		poolMax:        map[types.Address]*big.Int{validator: big.NewInt(55)},
		poolMin:        map[types.Address]*big.Int{validator: custom},
		poolCommission: map[types.Address]uint16{validator: 321},
		delegatorsByV:  map[types.Address][]types.Address{validator: {}},
		stakerInfos:    map[types.Address]map[string]interface{}{},
	}
	e := &Eth{store: store, logger: hclog.NewNullLogger()}
	respAny, err := e.GetPosValidatorDelegators(validator, nil)
	require.NoError(t, err)
	resp := respAny.(*posValidatorDelegatorsResponse)
	require.Equal(t, custom.String(), ((*big.Int)(resp.MinDelegatorStake)).String())
	require.Equal(t, custom.String(), ((*big.Int)(resp.EffectiveMinDelegatorStake)).String())
	require.Equal(t, "55", ((*big.Int)(resp.MaxTotalDelegatedStake)).String())
}
