package contract

import (
	"math/big"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/helper/common"
	"github.com/xgr-network/xgr-node/helper/keccak"
	"github.com/xgr-network/xgr-node/state"
	itrie "github.com/xgr-network/xgr-node/state/immutable-trie"
	"github.com/xgr-network/xgr-node/types"
)

func TestStakingV2StorageSlotsRemainAlignedWithContractLayout(t *testing.T) {
	t.Parallel()

	// These constants must match the deployed StakingV2 bytecode layout used by E2E.
	require.Equal(t, int64(6), stakersByValidatorSlot)
	require.Equal(t, int64(9), validatorDelegatedActiveSlot)
}

func TestStakingV2StorageSlotsMatchSolidityDeclarationOrder(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("../../../contracts/staking/StakingV2.sol")
	require.NoError(t, err)

	layout := stakingV2DeclaredStorageLayout(string(source))
	for name, slot := range map[string]int64{
		"VALIDATOR_THRESHOLD":             validatorThresholdSlot,
		"packedConfig(epochSize,min,max)": configSlot,
		"_validators":                     validatorsSlot,
		"_staker":                         stakerSlot,
		"_stakersByValidator":             stakersByValidatorSlot,
		"_validatorDelegatedStakeActive":  validatorDelegatedActiveSlot,
	} {
		require.Equal(t, slot, layout[name], name)
	}
}
func stakingV2DeclaredStorageLayout(source string) map[string]int64 {
	re := regexp.MustCompile(`(?m)^\s*(?:uint256|uint64|address\[\]|mapping\([^;]+\)|uint16|bool|bytes)\s+(?:public|private|internal)?\s*([A-Za-z_][A-Za-z0-9_]*)\s*;`)
	matches := re.FindAllStringSubmatch(source, -1)
	out := make(map[string]int64)
	slot := int64(0)
	configPacked := false
	for _, match := range matches {
		name := match[1]
		if strings.HasSuffix(name, "TOTAL") || strings.HasPrefix(name, "VALIDATOR_MIN_") || strings.HasPrefix(name, "DELEGATOR_MIN_") || name == "MAX_DELEGATORS_PER_VALIDATOR" || name == "MIN_STAKE" {
			continue
		}

		switch name {
		case "VALIDATOR_THRESHOLD":
			out[name] = slot
			slot++
		case "epochSize", "minNumValidators", "maxNumValidators":
			if !configPacked {
				out["packedConfig(epochSize,min,max)"] = slot
				slot++
				configPacked = true
			}
		case "_validators", "_staker", "_stakers", "_poolConfig", "_stakersByValidator", "_stakerIndexPlusOneByValidator", "_validatorDelegatedStakeRaw", "_validatorDelegatedStakeActive", "_reentrancyStatus":
			out[name] = slot
			slot++
		}
	}

	return out
}

func TestReadValidatorDelegatedStakeActiveUsesSlot9(t *testing.T) {
	t.Parallel()

	tx := newFetcherTestTransition(t)
	validator := types.StringToAddress("0x1001")

	slot6 := storageMappingSlot(validator, 6)
	slot9 := storageMappingSlot(validator, validatorDelegatedActiveSlot)

	tx.Txn().SetState(staking.AddrStakingContract, slot6, types.BytesToHash(big.NewInt(123).Bytes()))
	tx.Txn().SetState(staking.AddrStakingContract, slot9, types.BytesToHash(big.NewInt(999).Bytes()))

	require.Equal(t, big.NewInt(999), readValidatorDelegatedStakeActive(tx, validator))
}

func TestReadValidatorStakersUsesSlot6(t *testing.T) {
	t.Parallel()

	tx := newFetcherTestTransition(t)
	validator := types.StringToAddress("0x1002")
	delegator := types.StringToAddress("0x2002")

	base := storageMappingSlotBytes(validator, stakersByValidatorSlot)
	baseHash := types.BytesToHash(base)
	dataBase := keccak.Keccak256(nil, common.PadLeftOrTrim(base, 32))

	tx.Txn().SetState(staking.AddrStakingContract, baseHash, types.BytesToHash(big.NewInt(1).Bytes()))
	tx.Txn().SetState(staking.AddrStakingContract, types.BytesToHash(dataBase), types.BytesToHash(delegator.Bytes()))

	require.Equal(t, []types.Address{delegator}, readValidatorStakers(tx, validator))
}

func storageMappingSlot(addr types.Address, slot int64) types.Hash {
	return types.BytesToHash(storageMappingSlotBytes(addr, slot))
}

func storageMappingSlotBytes(addr types.Address, slot int64) []byte {
	return keccak.Keccak256(nil,
		append(
			common.PadLeftOrTrim(addr.Bytes(), 32),
			common.PadLeftOrTrim(big.NewInt(slot).Bytes(), 32)...,
		),
	)
}

func newFetcherTestTransition(t *testing.T) *state.Transition {
	t.Helper()

	st := itrie.NewState(itrie.NewMemoryStorage())
	ex := state.NewExecutor(&chain.Params{
		Forks: chain.AllForksEnabled,
		BurnContract: map[uint64]types.Address{
			0: types.ZeroAddress,
		},
	}, st, hclog.NewNullLogger())

	root, err := ex.WriteGenesis(nil, types.Hash{})
	require.NoError(t, err)

	ex.GetHash = func(h *types.Header) state.GetHashByNumber {
		return func(uint64) types.Hash {
			return root
		}
	}

	tx, err := ex.BeginTxn(
		root,
		&types.Header{},
		types.ZeroAddress,
	)
	require.NoError(t, err)

	return tx
}
