package blockchain

import (
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"math/big"
	"testing"
)

func TestSecurityBaseFeeCheckedUnavailableParent(t *testing.T) {
	old := chain.EngineRegistryAddress
	chain.EngineRegistryAddress = types.Address{19: 1}
	defer func() { chain.EngineRegistryAddress = old }()
	b := NewTestBlockchain(t, nil)
	_, err := b.CalculateBaseFeeChecked(&types.Header{StateRoot: types.Hash{1}, GasLimit: 10000000, BaseFee: chain.MinBaseFee})
	require.ErrorContains(t, err, "parent state")
	// Legacy continues its documented static fallback for historical compatibility.
	require.Equal(t, chain.MinBaseFee, b.CalculateBaseFee(&types.Header{StateRoot: types.Hash{1}, GasLimit: 10000000, BaseFee: chain.MinBaseFee}))
}
func TestSecurityBaseFeeOverflow(t *testing.T) {
	b := NewTestBlockchain(t, nil)
	max := ^uint64(0)
	parent := &types.Header{GasLimit: max, GasUsed: max, BaseFee: max}
	require.Equal(t, max, b.calculateBaseFeeWithMin(parent, chain.MinBaseFee, true))
	parent.BaseFee = chain.MinBaseFee
	parent.GasUsed = 0
	require.Equal(t, chain.MinBaseFee, b.calculateBaseFeeWithMin(parent, chain.MinBaseFee, true))
}

func TestSecurityBaseFeeRegistryValues(t *testing.T) {
	old := chain.EngineRegistryAddress
	reg := types.Address{19: 9}
	chain.EngineRegistryAddress = reg
	defer func() { chain.EngineRegistryAddress = old }()
	for _, value := range []*big.Int{new(big.Int), big.NewInt(12345), new(big.Int).Lsh(big.NewInt(1), 80)} {
		b := NewTestBlockchain(t, nil)
		ex := b.executor.(*state.Executor)
		root, err := ex.WriteGenesis(map[types.Address]*chain.GenesisAccount{reg: {Balance: new(big.Int), Nonce: 1, Code: []byte{0}, Storage: map[types.Hash]types.Hash{chain.EngineRegistrySlotKeyMinBaseFee(): types.BytesToHash(value.Bytes())}}}, types.ZeroHash)
		require.NoError(t, err)
		fee, err := b.CalculateBaseFeeChecked(&types.Header{StateRoot: root, GasLimit: 10000000, BaseFee: chain.MinBaseFee})
		require.NoError(t, err)
		expected := chain.MinBaseFee
		if value.BitLen() <= 64 {
			expected = value.Uint64()
		}
		require.Equal(t, expected, fee)
	}
}
