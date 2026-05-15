package state

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/state/runtime"
	"github.com/xgr-network/xgr-node/types"
)

func TestOverride(t *testing.T) {
	t.Parallel()

	state := newStateWithPreState(map[types.Address]*PreState{
		{0x0}: {
			Nonce:   1,
			Balance: 1,
			State: map[types.Hash]types.Hash{
				types.ZeroHash: {0x1},
			},
		},
		{0x1}: {
			State: map[types.Hash]types.Hash{
				types.ZeroHash: {0x1},
			},
		},
	})

	nonce := uint64(2)
	balance := big.NewInt(2)
	code := []byte{0x1}

	tt := NewTransition(chain.ForksInTime{}, state, newTxn(state))

	require.Empty(t, tt.state.GetCode(types.ZeroAddress))

	err := tt.WithStateOverride(types.StateOverride{
		{0x0}: types.OverrideAccount{
			Nonce:   &nonce,
			Balance: balance,
			Code:    code,
			StateDiff: map[types.Hash]types.Hash{
				types.ZeroHash: {0x2},
			},
		},
		{0x1}: types.OverrideAccount{
			State: map[types.Hash]types.Hash{
				{0x1}: {0x1},
			},
		},
	})
	require.NoError(t, err)

	require.Equal(t, nonce, tt.state.GetNonce(types.ZeroAddress))
	require.Equal(t, balance, tt.state.GetBalance(types.ZeroAddress))
	require.Equal(t, code, tt.state.GetCode(types.ZeroAddress))
	require.Equal(t, types.Hash{0x2}, tt.state.GetState(types.ZeroAddress, types.ZeroHash))

	// state is fully replaced
	require.Equal(t, types.Hash{0x0}, tt.state.GetState(types.Address{0x1}, types.ZeroHash))
	require.Equal(t, types.Hash{0x1}, tt.state.GetState(types.Address{0x1}, types.Hash{0x1}))
}

func Test_Transition_checkDynamicFees(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseFee *big.Int
		tx      *types.Transaction
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name:    "happy path",
			baseFee: big.NewInt(100),
			tx: &types.Transaction{
				Type:      types.DynamicFeeTx,
				GasFeeCap: big.NewInt(100),
				GasTipCap: big.NewInt(100),
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				assert.NoError(t, err, i)

				return false
			},
		},
		{
			name:    "happy path with empty values",
			baseFee: big.NewInt(0),
			tx: &types.Transaction{
				Type:      types.DynamicFeeTx,
				GasFeeCap: big.NewInt(0),
				GasTipCap: big.NewInt(0),
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				assert.NoError(t, err, i)

				return false
			},
		},
		{
			name:    "gas fee cap less than base fee",
			baseFee: big.NewInt(20),
			tx: &types.Transaction{
				Type:      types.DynamicFeeTx,
				GasFeeCap: big.NewInt(10),
				GasTipCap: big.NewInt(0),
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				expectedError := fmt.Sprintf("max fee per gas less than block base fee: "+
					"address %s, GasFeeCap/GasPrice: 10, BaseFee: 20", types.ZeroAddress)
				assert.EqualError(t, err, expectedError, i)

				return true
			},
		},
		{
			name:    "gas fee cap less than tip cap",
			baseFee: big.NewInt(5),
			tx: &types.Transaction{
				Type:      types.DynamicFeeTx,
				GasFeeCap: big.NewInt(10),
				GasTipCap: big.NewInt(15),
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				expectedError := fmt.Sprintf("max priority fee per gas higher than max fee per gas: "+
					"address %s, GasTipCap: 15, GasFeeCap: 10", types.ZeroAddress)
				assert.EqualError(t, err, expectedError, i)

				return true
			},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tr := &Transition{
				ctx: runtime.TxContext{
					BaseFee: tt.baseFee,
				},
				config: chain.ForksInTime{
					London: true,
				},
			}

			err := tr.checkDynamicFees(tt.tx)
			tt.wantErr(t, err, fmt.Sprintf("checkDynamicFees(%v)", tt.tx))
		})
	}
}

func TestTransitionWriteEmitsXGRFeeAccounting(t *testing.T) {
	t.Parallel()

	feeLogDataWord := func(t *testing.T, data []byte, index int) *big.Int {
		t.Helper()
		require.GreaterOrEqual(t, len(data), (index+1)*32)

		return new(big.Int).SetBytes(data[index*32 : (index+1)*32])
	}

	getLog := func(receipt *types.Receipt, topic types.Hash) *types.Log {
		for _, log := range receipt.Logs {
			if len(log.Topics) > 0 && log.Topics[0] == topic {
				return log
			}
		}

		return nil
	}

	findLog := func(t *testing.T, receipt *types.Receipt, topic types.Hash) *types.Log {
		t.Helper()

		log := getLog(receipt, topic)
		require.NotNilf(t, log, "missing log topic %s", topic)

		return log
	}

	newFeeAccountingTransition := func(t *testing.T, feePoolSplit bool) (*Transition, *types.Transaction) {
		t.Helper()

		snap := newStateWithPreState(map[types.Address]*PreState{
			addr1: {Balance: 1_000_000_000_000_000_000},
			addr2: {},
		})
		txn := newTxn(snap)
		transition := NewTransition(chain.ForksInTime{FeePoolSplit: feePoolSplit}, snap, txn)
		transition.ctx = runtime.TxContext{
			BaseFee:  big.NewInt(0),
			Coinbase: types.StringToAddress("0x0000000000000000000000000000000000000abc"),
			Number:   7,
		}
		transition.gasPool = 100_000

		return transition, &types.Transaction{
			From:     addr1,
			To:       &addr2,
			Gas:      50_000,
			GasPrice: big.NewInt(1_000_000_000),
			Value:    big.NewInt(0),
		}
	}

	t.Run("pre-PoS fee split does not emit accounting log", func(t *testing.T) {
		t.Parallel()

		transition, txn := newFeeAccountingTransition(t, false)
		require.NoError(t, transition.Write(txn))
		require.Len(t, transition.Receipts(), 1)

		receipt := transition.Receipts()[0]
		feeSplit := findLog(t, receipt, xgrFeeSplitTopic)
		require.Nil(t, getLog(receipt, xgrFeeAccountingTopic))

		require.Len(t, feeSplit.Data, 96)

		donation := feeLogDataWord(t, feeSplit.Data, 0)
		validator := feeLogDataWord(t, feeSplit.Data, 1)
		burned := feeLogDataWord(t, feeSplit.Data, 2)

		accounted := new(big.Int).Add(donation, validator)
		accounted.Add(accounted, burned)
		expectedTotal := new(big.Int).Mul(new(big.Int).SetUint64(receipt.GasUsed), txn.GasPrice)
		require.Equal(t, 0, expectedTotal.Cmp(accounted))
	})

	t.Run("PoS fee pool split accounting includes immediate and pooled validator shares", func(t *testing.T) {
		t.Parallel()

		transition, txn := newFeeAccountingTransition(t, true)
		require.NoError(t, transition.Write(txn))
		require.Len(t, transition.Receipts(), 1)

		receipt := transition.Receipts()[0]
		feeSplit := findLog(t, receipt, xgrFeeSplitTopic)
		feeAccounting := findLog(t, receipt, xgrFeeAccountingTopic)

		require.Len(t, feeSplit.Data, 96)
		require.Len(t, feeAccounting.Data, 128)

		donation := feeLogDataWord(t, feeAccounting.Data, 0)
		validatorImmediate := feeLogDataWord(t, feeAccounting.Data, 1)
		validatorPooled := feeLogDataWord(t, feeAccounting.Data, 2)
		burned := feeLogDataWord(t, feeAccounting.Data, 3)
		validatorAggregate := feeLogDataWord(t, feeSplit.Data, 1)

		require.Positive(t, validatorImmediate.Sign())
		require.Positive(t, validatorPooled.Sign())

		validatorTotal := new(big.Int).Add(validatorImmediate, validatorPooled)
		require.Equal(t, 0, validatorAggregate.Cmp(validatorTotal))

		accounted := new(big.Int).Add(donation, validatorImmediate)
		accounted.Add(accounted, validatorPooled)
		accounted.Add(accounted, burned)
		expectedTotal := new(big.Int).Mul(new(big.Int).SetUint64(receipt.GasUsed), txn.GasPrice)
		require.Equal(t, 0, expectedTotal.Cmp(accounted))
	})
}
