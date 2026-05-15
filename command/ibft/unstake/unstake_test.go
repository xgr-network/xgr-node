package unstake

import (
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/jsonrpc"
	"github.com/xgr-network/xgr-node/contracts/abis"
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

func TestQueryEpochSize(t *testing.T) {
	method := abis.StakingABI.Methods["epochSize"]
	enc, err := method.Outputs.Encode(map[string]interface{}{"0": big.NewInt(500)})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	epochSize, err := queryEpochSize(&mockRelayer{callResp: "0x" + hex.EncodeToString(enc)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if epochSize != 500 {
		t.Fatalf("expected epoch size 500, got %d", epochSize)
	}
}

func TestSendUnstake_Panic12Message(t *testing.T) {
	err := sendUnstake(&mockRelayer{sendErr: errors.New("execution reverted data:0x4e487b710000000000000000000000000000000000000000000000000000000000000012")}, nil)
	if err == nil {
		t.Fatal("expected error")
	}

	if !strings.Contains(err.Error(), "panic 0x12") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMapUnstakeError_PanicSelectorGeneric(t *testing.T) {
	err := mapUnstakeError(errors.New("execution reverted: 0x4e487b71"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "panic selector 0x4e487b71") {
		t.Fatalf("unexpected error: %v", err)
	}
}
