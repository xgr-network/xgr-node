package staking

import (
	"errors"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/umbracle/ethgo"
	"github.com/xgr-network/xgr-node/contracts/abis"
	"github.com/xgr-network/xgr-node/state/runtime"
	"github.com/xgr-network/xgr-node/types"
)

var (
	addr1 = types.StringToAddress("1")
	addr2 = types.StringToAddress("2")
)

func leftPad(buf []byte, n int) []byte {
	l := len(buf)
	if l > n {
		return buf
	}

	tmp := make([]byte, n)
	copy(tmp[n-l:], buf)

	return tmp
}

func appendAll(bytesArrays ...[]byte) []byte {
	var res []byte

	for idx := range bytesArrays {
		res = append(res, bytesArrays[idx]...)
	}

	return res
}

type TxMock struct {
	hashToRes  map[types.Hash]*runtime.ExecutionResult
	nonce      map[types.Address]uint64
	nonPayable bool
}

func (m *TxMock) Apply(tx *types.Transaction) (*runtime.ExecutionResult, error) {
	if m.hashToRes == nil {
		return nil, nil
	}

	tx.ComputeHash(1)

	res, ok := m.hashToRes[tx.Hash]
	if ok {
		return res, nil
	}

	return nil, errors.New("not found")
}

func (m *TxMock) Call(tx *types.Transaction) (*runtime.ExecutionResult, error) {
	return m.Apply(tx)
}

func (m *TxMock) GetNonce(addr types.Address) uint64 {
	if m.nonce != nil {
		return m.nonce[addr]
	}

	return 0
}

func (m *TxMock) SetNonPayable(nonPayable bool) {
	m.nonPayable = nonPayable
}

func Test_decodeValidators(t *testing.T) {
	tests := []struct {
		name     string
		value    []byte
		succeed  bool
		expected []types.Address
	}{
		{
			name: "should fail to parse",
			value: appendAll(
				leftPad([]byte{0x20}, 32), // Offset of the beginning of array
				leftPad([]byte{0x01}, 32), // Number of addresses
			),
			succeed: false,
		},
		{
			name: "should succeed",
			value: appendAll(
				leftPad([]byte{0x20}, 32), // Offset of the beginning of array
				leftPad([]byte{0x02}, 32), // Number of addresses
				leftPad(addr1[:], 32),     // Address 1
				leftPad(addr2[:], 32),     // Address 2
			),
			succeed: true,
			expected: []types.Address{
				addr1,
				addr2,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method := abis.StakingABI.Methods["validators"]
			assert.NotNil(t, method)

			res, err := DecodeValidators(method, tt.value)
			if tt.succeed {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
			assert.Equal(t, tt.expected, res)
		})
	}
}

func TestQueryValidators(t *testing.T) {
	method := abis.StakingABI.Methods["validators"]
	if method == nil {
		t.Fail()
	}

	type MockArgs struct {
		addr types.Address
		tx   *types.Transaction
	}

	type MockReturns struct {
		nonce uint64
		res   *runtime.ExecutionResult
		err   error
	}

	tests := []struct {
		name        string
		from        types.Address
		mockArgs    *MockArgs
		mockReturns *MockReturns
		succeed     bool
		expected    []types.Address
		err         error
	}{
		{
			name: "should failed",
			from: addr1,
			mockArgs: &MockArgs{
				addr: addr1,
				tx: &types.Transaction{
					From:     addr1,
					To:       &AddrStakingContract,
					Value:    big.NewInt(0),
					Input:    method.ID(),
					GasPrice: big.NewInt(0),
					Gas:      100000000,
					Nonce:    10,
				},
			},
			mockReturns: &MockReturns{
				nonce: 10,
				res: &runtime.ExecutionResult{
					Err: runtime.ErrExecutionReverted,
				},
				err: nil,
			},
			succeed:  false,
			expected: nil,
			err:      runtime.ErrExecutionReverted,
		},
		{
			name: "should succeed",
			from: addr1,
			mockArgs: &MockArgs{
				addr: addr1,
				tx: &types.Transaction{
					From:     addr1,
					To:       &AddrStakingContract,
					Value:    big.NewInt(0),
					Input:    method.ID(),
					GasPrice: big.NewInt(0),
					Gas:      queryGasLimit,
					Nonce:    10,
				},
			},
			mockReturns: &MockReturns{
				nonce: 10,
				res: &runtime.ExecutionResult{
					ReturnValue: appendAll(
						leftPad([]byte{0x20}, 32), // Offset of the beginning of array
						leftPad([]byte{0x00}, 32), // Number of addresses
					),
				},
				err: nil,
			},
			succeed:  true,
			expected: []types.Address{},
			err:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method := abis.StakingABI.Methods["validators"]
			assert.NotNil(t, method)

			mock := &TxMock{
				hashToRes: map[types.Hash]*runtime.ExecutionResult{
					tt.mockArgs.tx.ComputeHash(1).Hash: tt.mockReturns.res,
				},
				nonce: map[types.Address]uint64{
					tt.mockArgs.addr: tt.mockReturns.nonce,
				},
			}

			res, err := QueryValidators(mock, tt.from)
			if tt.succeed {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
			assert.Equal(t, tt.expected, res)
		})
	}
}

func TestQueryAccountStake(t *testing.T) {
	method := abis.StakingABI.Methods["accountStake"]
	if method == nil {
		t.Fail()
	}

	input, err := method.Inputs.Encode(map[string]interface{}{"account": ethgo.Address(addr2)})
	assert.NoError(t, err)

	tx := &types.Transaction{
		From:     addr1,
		To:       &AddrStakingContract,
		Value:    big.NewInt(0),
		Input:    append(method.ID(), input...),
		GasPrice: big.NewInt(0),
		Gas:      queryGasLimit,
		Nonce:    7,
	}

	expectedStake := big.NewInt(12345)
	encodedOut, err := method.Outputs.Encode(map[string]interface{}{"0": expectedStake})
	assert.NoError(t, err)

	mock := &TxMock{
		hashToRes: map[types.Hash]*runtime.ExecutionResult{
			tx.ComputeHash(1).Hash: {ReturnValue: encodedOut},
		},
		nonce: map[types.Address]uint64{addr1: 7},
	}

	stake, err := QueryAccountStake(mock, addr1, addr2)
	assert.NoError(t, err)
	assert.Equal(t, 0, expectedStake.Cmp(stake))
}

func TestDecodeBLSPublicKeys(t *testing.T) {
	method := abis.StakingABI.Methods["validatorBLSPublicKeys"]
	if method == nil {
		t.FailNow()
	}

	want := [][]byte{{0x01, 0x02}, {0x03, 0x04}}

	encoded, err := method.Outputs.Encode(map[string]interface{}{"keys": want})
	assert.NoError(t, err)

	res, err := decodeBLSPublicKeys(method, encoded)
	assert.NoError(t, err)
	assert.Equal(t, want, res)
}

func TestDecodeWeb3ArrayOfBytes_FixedArrayItems(t *testing.T) {
	method := abis.StakingABI.Methods["validatorBLSPublicKeys"]
	if method == nil {
		t.FailNow()
	}

	res, err := decodeWeb3ArrayOfBytes(method, map[string]interface{}{"keys": [][2]byte{{0x01, 0x02}, {0x03, 0x04}}})
	assert.NoError(t, err)
	assert.Equal(t, [][]byte{{0x01, 0x02}, {0x03, 0x04}}, res)
}

func TestStakingABI_SetValidatorPoolConfigSignature(t *testing.T) {
	m := abis.StakingABI.Methods["setValidatorPoolConfig"]
	assert.NotNil(t, m)
	assert.Equal(t, 4, len(m.Inputs.TupleElems()))
	assert.Equal(t, "delegationEnabled", m.Inputs.TupleElems()[0].Name)
	assert.Equal(t, "maxTotalDelegatedStake", m.Inputs.TupleElems()[1].Name)
	assert.Equal(t, "minDelegatorStake", m.Inputs.TupleElems()[2].Name)
	assert.Equal(t, "commissionBps", m.Inputs.TupleElems()[3].Name)
}

func TestStakingABI_ValidatorPoolTupleNames(t *testing.T) {
	m := abis.StakingABI.Methods["validatorPool"]
	assert.NotNil(t, m)
	out := m.Outputs.TupleElems()[0]
	elems := out.Elem.TupleElems()
	assert.Equal(t, "exists", elems[0].Name)
	assert.Equal(t, "active", elems[1].Name)
	assert.Equal(t, "delegationEnabled", elems[2].Name)
	assert.Equal(t, "maxTotalDelegatedStake", elems[3].Name)
	assert.Equal(t, "minDelegatorStake", elems[4].Name)
	assert.Equal(t, "commissionBps", elems[5].Name)
	assert.Equal(t, "blsPubKey", elems[6].Name)
}
