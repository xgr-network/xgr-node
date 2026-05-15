package setactive

import (
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/xgr-network/xgr-node/contracts/abis"

	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/jsonrpc"
	"github.com/xgr-network/xgr-node/types"
)

type mockRelayer struct {
	callResp    string
	callErr     error
	sendReceipt *ethgo.Receipt
	sendErr     error
}

func (m *mockRelayer) Call(ethgo.Address, ethgo.Address, []byte) (string, error) {
	return m.callResp, m.callErr
}
func (m *mockRelayer) SendTransaction(*ethgo.Transaction, ethgo.Key) (*ethgo.Receipt, error) {
	return m.sendReceipt, m.sendErr
}
func (m *mockRelayer) SendTransactionLocal(*ethgo.Transaction) (*ethgo.Receipt, error) {
	return nil, nil
}
func (m *mockRelayer) Client() *jsonrpc.Client { return nil }

type mockKey struct {
	addr ethgo.Address
}

func (m mockKey) Address() ethgo.Address { return m.addr }
func (m mockKey) Sign([]byte) ([]byte, error) {
	return nil, nil
}

func TestSendSetActive_AlreadyInRequestedState_IsNoop(t *testing.T) {
	err := sendSetActive(&mockRelayer{sendErr: errors.New("execution reverted data:0xa291f7f5")}, mockKey{}, false)
	if err != nil {
		t.Fatalf("expected noop nil error, got %v", err)
	}
}

func TestSendSetActive_ValidatorNotFound(t *testing.T) {
	key := mockKey{addr: ethgo.HexToAddress("0x1111111111111111111111111111111111111111")}
	err := sendSetActive(&mockRelayer{sendErr: errors.New("execution reverted data:0x580e542f")}, key, false)
	if err == nil {
		t.Fatal("expected error")
	}

	if !strings.Contains(err.Error(), "not found in staking set on this chain") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), key.addr.String()) {
		t.Fatalf("expected error to contain validator address, got %v", err)
	}
}

func TestSendSetActive_RevertedReceipt(t *testing.T) {
	err := sendSetActive(&mockRelayer{sendReceipt: &ethgo.Receipt{Status: uint64(types.ReceiptFailed)}}, mockKey{}, true)
	if err == nil || err.Error() != "setActive reverted" {
		t.Fatalf("expected setActive reverted, got %v", err)
	}
}

func TestQueryValidatorStatus_DecodeByIndex(t *testing.T) {
	method := abis.StakingABI.Methods["validatorInfo"]
	enc, err := method.Outputs.Encode(map[string]interface{}{
		"exists":             false,
		"active":             true,
		"stakedAmount":       big.NewInt(0),
		"deactivatedAtBlock": big.NewInt(7),
		"blsPubKey":          []byte{},
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	status, err := queryValidatorStatus(&mockRelayer{callResp: "0x" + hex.EncodeToString(enc)}, types.ZeroAddress)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !status.Active {
		t.Fatal("expected active=true")
	}
	if status.DeactivatedAtBlock != 7 {
		t.Fatalf("expected deactivatedAtBlock=7, got %d", status.DeactivatedAtBlock)
	}
}

func TestQueryValidatorStatus_CallError(t *testing.T) {
	_, err := queryValidatorStatus(&mockRelayer{callErr: errors.New("rpc down")}, types.ZeroAddress)
	if err == nil || !strings.Contains(err.Error(), "rpc down") {
		t.Fatalf("expected rpc error, got %v", err)
	}
}

func TestQueryEpochSize(t *testing.T) {
	method := abis.StakingABI.Methods["epochSize"]
	enc, err := method.Outputs.Encode(map[string]interface{}{
		"0": big.NewInt(100),
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	epochSize, err := queryEpochSize(&mockRelayer{callResp: "0x" + hex.EncodeToString(enc)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if epochSize != 100 {
		t.Fatalf("expected epoch size 100, got %d", epochSize)
	}
}
