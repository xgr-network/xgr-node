package jsonrpc

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"

	interchainEVM "github.com/xgr-network/xgr-node/interchain/evm"
	"github.com/xgr-network/xgr-node/types"
)

func TestXGRGetInterchainValidatorsFormatsCanonicalRegistryOrder(t *testing.T) {
	first := types.StringToAddress("0x1111111111111111111111111111111111111111")
	second := types.StringToAddress("0x2222222222222222222222222222222222222222")
	keyA, keyB := bytes.Repeat([]byte{0x11}, 48), bytes.Repeat([]byte{0x22}, 48)
	destination := &interchainEVM.Destination{
		Name: "base", ChainID: 8453, Domain: 8453,
		RegistryAddress: "0x9999999999999999999999999999999999999999",
	}
	got, err := formatInterchainValidators(destination, &interchainEVM.ValidatorSet{
		Validators: []types.Address{second, first}, BLSPublicKeys: [][]byte{keyB, keyA}, SetID: 7,
	})
	require.NoError(t, err)
	require.Equal(t, "base", got.Destination)
	require.Equal(t, uint64(8453), got.ChainID)
	require.Equal(t, uint32(8453), got.Domain)
	require.Equal(t, uint64(7), got.SetID)
	require.Equal(t, 2, got.Count)
	require.Equal(t, 2, got.QuorumThreshold)
	require.Equal(t, second.String(), got.Validators[0].Address)
	require.Equal(t, first.String(), got.Validators[1].Address)
	require.Equal(t, "0x"+hex.EncodeToString(keyA), got.Validators[1].BLSPublicKey)
}

func TestXGRGetInterchainValidatorsRejectsInvalidCanonicalSet(t *testing.T) {
	address := types.StringToAddress("0x1111111111111111111111111111111111111111")
	dest := &interchainEVM.Destination{Name: "base", ChainID: 8453, Domain: 8453}
	for _, tc := range []struct {
		name string
		set  *interchainEVM.ValidatorSet
	}{
		{"nil", nil},
		{"empty", &interchainEVM.ValidatorSet{SetID: 1}},
		{"zero set id", &interchainEVM.ValidatorSet{SetID: 0, Validators: []types.Address{address}, BLSPublicKeys: [][]byte{bytes.Repeat([]byte{0x11}, 48)}}},
		{"mismatched arrays", &interchainEVM.ValidatorSet{SetID: 1, Validators: []types.Address{address}}},
		{"zero address", &interchainEVM.ValidatorSet{SetID: 1, Validators: []types.Address{types.ZeroAddress}, BLSPublicKeys: [][]byte{bytes.Repeat([]byte{0x11}, 48)}}},
		{"wrong key size", &interchainEVM.ValidatorSet{SetID: 1, Validators: []types.Address{address}, BLSPublicKeys: [][]byte{{0x01}}}},
		{"duplicate address", &interchainEVM.ValidatorSet{SetID: 1, Validators: []types.Address{address, address}, BLSPublicKeys: [][]byte{bytes.Repeat([]byte{0x11}, 48), bytes.Repeat([]byte{0x22}, 48)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := formatInterchainValidators(dest, tc.set)
			require.Error(t, err)
		})
	}
}

func TestXGRGetInterchainValidatorsRequiresConfiguredDestination(t *testing.T) {
	// No user-supplied destination URL/address is ever passed through.
	ep := newXGREndpoint(nil, t.TempDir())
	_, err := ep.GetInterchainValidators("")
	require.ErrorContains(t, err, "name is required")
	_, err = ep.GetInterchainValidators("__unknown_interchain_destination__")
	require.ErrorContains(t, err, "not configured")
}

func TestXGRGetInterchainValidatorsReadsDestinationRegistryOverRPC(t *testing.T) {
	const registry = "0x9999999999999999999999999999999999999999"
	address := ethgo.Address(types.StringToAddress("0x1111111111111111111111111111111111111111"))
	key := bytes.Repeat([]byte{0x43}, 48)
	abiDef := abi.MustNewABI(`[
		{"inputs":[],"name":"getValidatorSet","outputs":[
		{"name":"validators","type":"address[]"},
		{"name":"blsPublicKeys","type":"bytes[]"},
		{"name":"currentSetId","type":"uint64"}
		],"stateMutability":"view","type":"function"}
	]`)
	method := abiDef.Methods["getValidatorSet"]
	require.NotNil(t, method)
	response, err := method.Outputs.Encode(map[string]interface{}{
		"validators":    []ethgo.Address{address},
		"blsPublicKeys": [][]byte{key},
		"currentSetId":  big.NewInt(11),
	})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode JSON-RPC request: %v", err)
			return
		}
		result := ""
		switch req.Method {
		case "eth_chainId":
			result = "0x2105"
		case "eth_call":
			require.NotEmpty(t, req.Params)
			var call struct {
				To   string `json:"to"`
				Data string `json:"data"`
			}
			require.NoError(t, json.Unmarshal(req.Params[0], &call))
			require.Equal(t, strings.ToLower(registry), strings.ToLower(call.To))
			require.Equal(t, "0x"+hex.EncodeToString(method.ID()), strings.ToLower(call.Data))
			result = "0x" + hex.EncodeToString(response)
		default:
			t.Errorf("unexpected EVM RPC method: %s", req.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%q}`, req.ID, result)
	}))
	defer server.Close()
	t.Setenv("XGR_INTERCHAIN_BASE_CHAIN_ID", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_DOMAIN", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_REGISTRY_ADDR", registry)
	t.Setenv("XGR_INTERCHAIN_BASE_RPC", server.URL)
	t.Setenv("XGR_INTERCHAIN_BASE_CONFIRMATIONS", "1")

	ep := newXGREndpoint(nil, t.TempDir())
	got, err := ep.GetInterchainValidators("BASE")
	require.NoError(t, err)
	require.Equal(t, "base", got.Destination)
	require.Equal(t, uint64(11), got.SetID)
	require.Len(t, got.Validators, 1)
	require.Equal(t, 1, got.QuorumThreshold)
	require.Equal(t, "0x1111111111111111111111111111111111111111", strings.ToLower(got.Validators[0].Address))
	// Registering methods uses the same reflection dispatcher as all xgr_* RPCs.
	d := &Dispatcher{logger: hclog.NewNullLogger()}
	require.NoError(t, d.registerService("xgr", ep))
	_, _, rpcError := d.getFnHandler(Request{Method: "xgr_getInterchainValidators"})
	require.Nil(t, rpcError)
}
