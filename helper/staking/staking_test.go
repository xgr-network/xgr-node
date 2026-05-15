package staking

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/contracts/abis"
	"github.com/xgr-network/xgr-node/helper/common"
	"github.com/xgr-network/xgr-node/helper/keccak"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func TestStakingV2PredeploySelectorAndABIConsistency(t *testing.T) {
	t.Parallel()

	const currentSignature = "setValidatorPoolConfig(bool,uint256,uint256,uint16)"
	const oldSignature = "setValidatorPoolConfig(bool,uint256,uint16)"
	const currentSelector = "cb5cf2a2"
	const oldSelector = "db02fcee"

	method := abis.StakingABI.GetMethodBySignature(currentSignature)
	require.NotNil(t, method, "staking ABI must expose the current 4-argument setValidatorPoolConfig")
	require.Equal(t, currentSignature, method.Sig())
	require.Equal(t, currentSelector, hex.EncodeToString(method.ID()))
	require.Nil(t, abis.StakingABI.GetMethodBySignature(oldSignature), "staking ABI must not expose the old 3-argument setValidatorPoolConfig")

	require.Contains(t, StakingSCBytecode, currentSelector, "embedded bytecode must route the current selector")
	require.False(t, strings.Contains(StakingSCBytecode, oldSelector), "embedded bytecode must not route the old selector")
}

func TestStakingV2EmbeddedBytecodeHashPinned(t *testing.T) {
	t.Parallel()

	bytecode, err := hex.DecodeString(StakingSCBytecode)
	require.NoError(t, err)
	hasher := keccak.NewKeccak256()
	_, err = hasher.Write(bytecode)
	require.NoError(t, err)
	require.Equal(t, "510e1cea27394c6b1ef8e53e44909fa57e40d4af480358bfb904bb023867c2d9", hex.EncodeToString(hasher.Sum(nil)))
}

func TestPredeployStakingSC_ValidatesParams(t *testing.T) {
	t.Parallel()

	_, err := PredeployStakingSC(nil, PredeployParams{MinValidatorCount: 1, MaxValidatorCount: 0, EpochSize: 10})
	require.Error(t, err)
	require.Contains(t, err.Error(), "max validator count")

	_, err = PredeployStakingSC(nil, PredeployParams{MinValidatorCount: 5, MaxValidatorCount: 4, EpochSize: 10})
	require.Error(t, err)
	require.Contains(t, err.Error(), "min validator count")

	_, err = PredeployStakingSC(nil, PredeployParams{MinValidatorCount: 1, MaxValidatorCount: 1, EpochSize: 0})
	require.Error(t, err)
	require.Contains(t, err.Error(), "epoch size")

	_, err = PredeployStakingSC(nil, PredeployParams{MinValidatorCount: 1, MaxValidatorCount: 1, EpochSize: 1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "epoch size")

	_, err = PredeployStakingSC(nil, PredeployParams{MinValidatorCount: 1, MaxValidatorCount: 1, EpochSize: 2})
	require.NoError(t, err)
}

func TestPredeployStakingSC_RejectsValidatorSetAboveMax(t *testing.T) {
	t.Parallel()

	vals := validators.NewECDSAValidatorSet(
		&validators.ECDSAValidator{Address: types.StringToAddress("0x1")},
		&validators.ECDSAValidator{Address: types.StringToAddress("0x2")},
	)

	_, err := PredeployStakingSC(vals, PredeployParams{MinValidatorCount: 1, MaxValidatorCount: 1, EpochSize: 10})
	require.Error(t, err)
	require.Contains(t, err.Error(), "validator set size")
}

func TestPredeployStakingSC_AllowsValidParams(t *testing.T) {
	t.Parallel()

	acc, err := PredeployStakingSC(nil, PredeployParams{MinValidatorCount: 1, MaxValidatorCount: 10, EpochSize: 10})
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.NotEmpty(t, acc.Code)
}

func TestPredeployStakingSC_SetsJoinedAtBlockForBootstrapValidators(t *testing.T) {
	t.Parallel()

	addr := types.StringToAddress("0x123")
	vals := validators.NewECDSAValidatorSet(&validators.ECDSAValidator{Address: addr})

	acc, err := PredeployStakingSC(vals, PredeployParams{MinValidatorCount: 1, MaxValidatorCount: 10, EpochSize: 10})
	require.NoError(t, err)

	stakerBase := getAddressMapping(addr, stakerSlot)
	joinedKey := types.BytesToHash(getIndexWithOffset(stakerBase, 2))
	require.Equal(t, types.BytesToHash(big.NewInt(1).Bytes()), acc.Storage[joinedKey])
}

func TestPredeployStakingSC_BootstrapSetsSelfValidatorAndPackedFlags(t *testing.T) {
	t.Parallel()

	addr := types.StringToAddress("0x124")
	vals := validators.NewECDSAValidatorSet(&validators.ECDSAValidator{Address: addr})

	acc, err := PredeployStakingSC(vals, PredeployParams{MinValidatorCount: 1, MaxValidatorCount: 10, EpochSize: 10})
	require.NoError(t, err)

	stakerBase := getAddressMapping(addr, stakerSlot)
	packedKey := types.BytesToHash(stakerBase)
	validatorKey := types.BytesToHash(getIndexWithOffset(stakerBase, 4))
	amountKey := types.BytesToHash(getIndexWithOffset(stakerBase, 1))

	packedHash := acc.Storage[packedKey]
	packed := new(big.Int).SetBytes(packedHash[:])
	require.Equal(t, uint(1), packed.Bit(0), "exists must be set")
	require.Equal(t, uint(1), packed.Bit(8), "active must be set")
	require.Equal(t, types.BytesToHash(addr.Bytes()), acc.Storage[validatorKey], "bootstrap must be self-validator")
	require.Equal(t, types.StringToHash(DefaultStakedBalance), acc.Storage[amountKey], "bootstrap self stake must use default stake")
}

func TestPredeployStakingSC_ConfigAndValidatorsSlotsRemainStable(t *testing.T) {
	t.Parallel()

	addr := types.StringToAddress("0x125")
	vals := validators.NewECDSAValidatorSet(&validators.ECDSAValidator{Address: addr})
	acc, err := PredeployStakingSC(vals, PredeployParams{MinValidatorCount: 3, MaxValidatorCount: 9, EpochSize: 17})
	require.NoError(t, err)

	// slot2 = validators length
	require.Equal(t, types.BytesToHash(big.NewInt(1).Bytes()), acc.Storage[types.BytesToHash(big.NewInt(validatorsSlot).Bytes())])

	// _validators[0]
	validatorsBase := keccak.Keccak256(nil, common.PadLeftOrTrim(big.NewInt(validatorsSlot).Bytes(), 32))
	require.Equal(t, types.BytesToHash(addr.Bytes()), acc.Storage[types.BytesToHash(validatorsBase)])

	// slot1 packs epoch|min|max (uint64 each)
	cfgHash := acc.Storage[types.BytesToHash(big.NewInt(configSlot).Bytes())]
	cfg := new(big.Int).SetBytes(cfgHash[:])
	require.Equal(t, uint64(17), new(big.Int).And(new(big.Int).Set(cfg), big.NewInt(0).SetUint64(^uint64(0))).Uint64())
	tmp := new(big.Int).Rsh(new(big.Int).Set(cfg), 64)
	require.Equal(t, uint64(3), new(big.Int).And(new(big.Int).Set(tmp), big.NewInt(0).SetUint64(^uint64(0))).Uint64())
	require.Equal(t, uint64(9), new(big.Int).Rsh(tmp, 64).Uint64())
}

func TestPredeployStakingSC_BootstrappedValidatorsAppearInValidatorsArray(t *testing.T) {
	t.Parallel()

	v1 := types.StringToAddress("0x126")
	v2 := types.StringToAddress("0x127")
	vals := validators.NewECDSAValidatorSet(
		&validators.ECDSAValidator{Address: v1},
		&validators.ECDSAValidator{Address: v2},
	)
	acc, err := PredeployStakingSC(vals, PredeployParams{MinValidatorCount: 1, MaxValidatorCount: 10, EpochSize: 10})
	require.NoError(t, err)

	require.Equal(t, types.BytesToHash(big.NewInt(2).Bytes()), acc.Storage[types.BytesToHash(big.NewInt(validatorsSlot).Bytes())])
	validatorsBase := keccak.Keccak256(nil, common.PadLeftOrTrim(big.NewInt(validatorsSlot).Bytes(), 32))
	require.Equal(t, types.BytesToHash(v1.Bytes()), acc.Storage[types.BytesToHash(validatorsBase)])
	require.Equal(t, types.BytesToHash(v2.Bytes()), acc.Storage[types.BytesToHash(getIndexWithOffset(validatorsBase, 1))])
}

func TestPredeployStakingSC_BLSBootstrapStoresValidatorAndPoolBLSData(t *testing.T) {
	t.Parallel()

	addr := types.StringToAddress("0x128")
	bls := make([]byte, 48)
	for i := range bls {
		bls[i] = byte(i + 1)
	}
	vals := validators.NewBLSValidatorSet(validators.NewBLSValidator(addr, bls))
	acc, err := PredeployStakingSC(vals, PredeployParams{MinValidatorCount: 1, MaxValidatorCount: 10, EpochSize: 10})
	require.NoError(t, err)

	stakerBase := getAddressMapping(addr, stakerSlot)
	poolBase := getAddressMapping(addr, validatorPoolSlot)

	require.NotEqual(t, types.ZeroHash, acc.Storage[types.BytesToHash(stakerBase)], "staker packed entry must exist")
	require.NotEqual(t, types.ZeroHash, acc.Storage[types.BytesToHash(getIndexWithOffset(stakerBase, 5))], "staker BLS entry must exist")
	require.NotEqual(t, types.ZeroHash, acc.Storage[types.BytesToHash(poolBase)], "pool packed entry must exist")
	require.NotEqual(t, types.ZeroHash, acc.Storage[types.BytesToHash(getIndexWithOffset(poolBase, 1))], "pool BLS entry must exist")
}

func TestPredeployStakingSC_ThresholdSlotUsesTwoMillionXGR(t *testing.T) {
	t.Parallel()

	acc, err := PredeployStakingSC(nil, PredeployParams{MinValidatorCount: 1, MaxValidatorCount: 10, EpochSize: 10})
	require.NoError(t, err)

	threshold := new(big.Int).SetUint64(2_000_000)
	threshold.Mul(threshold, new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	require.Equal(t, types.BytesToHash(threshold.Bytes()), acc.Storage[types.BytesToHash(big.NewInt(validatorThresholdSlot).Bytes())])
}

func TestPredeployStakingSC_DelegatorIndexesUseSlots6And7(t *testing.T) {
	t.Parallel()

	addr := types.StringToAddress("0x129")
	vals := validators.NewECDSAValidatorSet(&validators.ECDSAValidator{Address: addr})
	acc, err := PredeployStakingSC(vals, PredeployParams{MinValidatorCount: 1, MaxValidatorCount: 10, EpochSize: 10})
	require.NoError(t, err)

	require.Equal(t, int64(6), stakersByValidatorSlot)
	require.Equal(t, int64(7), stakerIndexByValSlot)

	stakersByValBase := getAddressMapping(addr, stakersByValidatorSlot)
	require.Equal(t, types.BytesToHash(big.NewInt(1).Bytes()), acc.Storage[types.BytesToHash(stakersByValBase)])

	stakersByValArrayBase := keccak.Keccak256(nil, common.PadLeftOrTrim(stakersByValBase, 32))
	require.Equal(t, types.BytesToHash(addr.Bytes()), acc.Storage[types.BytesToHash(stakersByValArrayBase)])

	stakerIndexOuter := getAddressMapping(addr, stakerIndexByValSlot)
	stakerIndexInner := append(common.PadLeftOrTrim(addr.Bytes(), 32), common.PadLeftOrTrim(stakerIndexOuter, 32)...)
	require.Equal(t, types.BytesToHash(big.NewInt(1).Bytes()), acc.Storage[types.BytesToHash(keccak.Keccak256(nil, stakerIndexInner))])
}
