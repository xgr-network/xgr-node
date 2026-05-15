package poolconfig

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/jsonrpc"
	"github.com/xgr-network/xgr-node/contracts/abis"
	"github.com/xgr-network/xgr-node/types"
)

type mockRelayer struct {
	tx     *ethgo.Transaction
	callFn func(from ethgo.Address, to ethgo.Address, input []byte) (string, error)
}

type mockKey struct {
	addr ethgo.Address
}

func (m mockKey) Address() ethgo.Address {
	return m.addr
}

func (m mockKey) Sign(_ []byte) ([]byte, error) {
	return nil, nil
}

func (m *mockRelayer) Call(from ethgo.Address, to ethgo.Address, input []byte) (string, error) {
	if m.callFn != nil {
		return m.callFn(from, to, input)
	}
	return "0x", nil
}

func (m *mockRelayer) SendTransaction(txn *ethgo.Transaction, key ethgo.Key) (*ethgo.Receipt, error) {
	m.tx = txn
	return &ethgo.Receipt{Status: uint64(types.ReceiptSuccess)}, nil
}

func (m *mockRelayer) SendTransactionLocal(txn *ethgo.Transaction) (*ethgo.Receipt, error) {
	m.tx = txn
	return &ethgo.Receipt{Status: uint64(types.ReceiptSuccess)}, nil
}

func (m *mockRelayer) Client() *jsonrpc.Client { return nil }

func TestSendPoolConfigTx_UsesStakingABI(t *testing.T) {
	relayer := &mockRelayer{}
	max := new(big.Int).Mul(big.NewInt(123), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	key := mockKey{addr: ethgo.Address(types.StringToAddress("0x9999999999999999999999999999999999999999"))}

	require.NoError(t, sendPoolConfigTx(relayer, key, true, max, big.NewInt(77), 250, "http://127.0.0.1:8545"))
	require.NotNil(t, relayer.tx)

	method := abis.StakingABI.Methods["setValidatorPoolConfig"]
	require.NotNil(t, method)
	require.GreaterOrEqual(t, len(relayer.tx.Input), 4)
	require.Equal(t, method.ID(), relayer.tx.Input[:4])

	decoded, err := method.Inputs.Decode(relayer.tx.Input[4:])
	require.NoError(t, err)
	args, ok := decoded.(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, true, args["delegationEnabled"].(bool))
	require.Equal(t, 0, args["maxTotalDelegatedStake"].(*big.Int).Cmp(max))
	require.Equal(t, 0, args["minDelegatorStake"].(*big.Int).Cmp(big.NewInt(77)))
	require.Equal(t, uint16(250), args["commissionBps"].(uint16))
	require.Equal(t, key.Address(), relayer.tx.From)
}

func TestSendPoolConfigTx_RejectsTooHighCommission(t *testing.T) {
	err := sendPoolConfigTx(&mockRelayer{}, mockKey{}, true, big.NewInt(1), big.NewInt(0), 10001, "http://127.0.0.1:8545")
	require.Error(t, err)
}

func TestGetCommand_DefaultsForPoolConfig(t *testing.T) {
	cmd := GetCommand()
	require.Equal(t, "0", cmd.Flags().Lookup("max-total-delegated").DefValue)
	require.Equal(t, "0", cmd.Flags().Lookup("commission-bps").DefValue)
	require.Equal(t, "0", cmd.Flags().Lookup("min-delegator-stake").DefValue)
	require.Equal(t, "true", cmd.Flags().Lookup("enabled").DefValue)
}

func TestResult_IncludesMinDelegatorWei(t *testing.T) {
	r := &Result{Validator: "0x1", Enabled: true, MaxTotalDelegatedWei: "2", MinDelegatorWei: "3", CommissionBps: 4}
	out := r.GetOutput()
	require.Contains(t, out, "Max total delegated (wei)")
	require.Contains(t, out, "Min delegator stake (wei)")
	require.Contains(t, out, "3")
}
