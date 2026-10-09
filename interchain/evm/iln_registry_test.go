package evm

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/jsonrpc"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

func TestValidateILNReadRequiresRegistry(t *testing.T) {
	cfg := &Destination{
		Name: "base", ChainID: 8453, Domain: 8453,
		RPCURL: "https://base.example.invalid", Confirmations: 1,
	}
	require.Error(t, cfg.ValidateILNRead())
	cfg.ILNRegistryAddress = "0x5555555555555555555555555555555555555555"
	require.NoError(t, cfg.ValidateILNRead())
}



func TestGetConfirmedILNGovernanceNonce(t *testing.T) {
	registry := types.StringToAddress("0x5555555555555555555555555555555555555555")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		w.Header().Set("Content-Type", "application/json")

		switch req.Method {
		case "eth_chainId":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"0x2105"}`, req.ID)
		case "eth_blockNumber":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"0x64"}`, req.ID)
		case "eth_call":
			var call struct {
				To   string `json:"to"`
				Data string `json:"data"`
			}
			require.NoError(t, json.Unmarshal(req.Params[0], &call))
			require.Equal(t, strings.ToLower(registry.String()), strings.ToLower(call.To))
			require.True(t, strings.HasPrefix(strings.ToLower(call.Data), selectorHex("sourceFeeNonce()")))
			result := "0x" + strings.Repeat("0", 63) + "7"
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"%s"}`, req.ID, result)
		default:
			t.Fatalf("unexpected rpc method %s", req.Method)
		}
	}))
	defer server.Close()

	source := &Destination{
		Name:               "base",
		ChainID:            8453,
		Domain:             8453,
		RPCURL:             server.URL,
		Confirmations:      1,
		ILNRegistryAddress: registry.String(),
	}
	got, err := GetConfirmedILNGovernanceNonce(source)
	require.NoError(t, err)
	require.Equal(t, uint64(7), got)
}

func TestVerifyILNGatewayBindingRequiresCanonicalWarpRouter(t *testing.T) {
	registry := types.StringToAddress("0x5555555555555555555555555555555555555555")
	gateway := types.StringToAddress("0x1111111111111111111111111111111111111111")
	sourceRouter := types.StringToAddress("0x6666666666666666666666666666666666666666")
	mailbox := types.StringToAddress("0x2222222222222222222222222222222222222222")
	hook := types.StringToAddress("0x3333333333333333333333333333333333333333")
	destinationRouter := types.StringToAddress("0x4444444444444444444444444444444444444444")

	reportedRouter := sourceRouter
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "eth_call", req.Method)

		var call struct {
			To   string `json:"to"`
			Data string `json:"data"`
		}
		require.NoError(t, json.Unmarshal(req.Params[0], &call))
		require.Equal(t, strings.ToLower(gateway.String()), strings.ToLower(call.To))

		var value types.Address
		switch strings.ToLower(call.Data) {
		case selectorHex("ilnRegistry()"):
			value = registry
		case selectorHex("warpRouter()"):
			value = reportedRouter
		case selectorHex("mailbox()"):
			value = mailbox
		case selectorHex("merkleTreeHook()"):
			value = hook
		default:
			t.Fatalf("unexpected eth_call data=%s", call.Data)
		}
		result := "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(value.String(), "0x")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"%s"}`, req.ID, result)
	}))
	defer server.Close()

	client, err := jsonrpc.NewClient(server.URL)
	require.NoError(t, err)
	route := protocolTestRoute(gateway, sourceRouter, mailbox, hook, destinationRouter)

	require.NoError(t, verifyILNGatewayBinding(client, registry, route, 100))

	reportedRouter = types.StringToAddress("0x9999999999999999999999999999999999999999")
	err = verifyILNGatewayBinding(client, registry, route, 100)
	require.Error(t, err)
	require.Contains(t, err.Error(), "source router mismatch")
}

func TestParseILNOperationLog(t *testing.T) {
	routeID := types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	messageID := types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	event := ethgo.Hash(crypto.Keccak256Hash([]byte(ilnOperationEventSignature)))
	routeTopic := ethgo.Hash(routeID)
	messageTopic := ethgo.Hash(messageID)

	var destinationTopic ethgo.Hash
	new(big.Int).SetUint64(1643).FillBytes(destinationTopic[:])
	feeData := make([]byte, 32)
	big.NewInt(12345).FillBytes(feeData)

	log := &ethgo.Log{
		BlockNumber: 77,
		Topics: []ethgo.Hash{event, routeTopic, messageTopic, destinationTopic},
		Data: feeData,
	}
	got, err := parseILNOperationLog(log)
	require.NoError(t, err)
	require.Equal(t, routeID, got.RouteID)
	require.Equal(t, messageID, got.MessageID)
	require.Equal(t, uint32(1643), got.DestinationDomain)
	require.Zero(t, big.NewInt(12345).Cmp(got.ValidatorFeeWei))
	require.Equal(t, uint64(77), got.BlockNumber)
}

func TestParseILNOperationLogRejectsZeroFee(t *testing.T) {
	routeID := types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	messageID := types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	event := ethgo.Hash(crypto.Keccak256Hash([]byte(ilnOperationEventSignature)))
	routeTopic := ethgo.Hash(routeID)
	messageTopic := ethgo.Hash(messageID)
	var destinationTopic ethgo.Hash
	new(big.Int).SetUint64(1643).FillBytes(destinationTopic[:])

	_, err := parseILNOperationLog(&ethgo.Log{
		BlockNumber: 77,
		Topics: []ethgo.Hash{event, routeTopic, messageTopic, destinationTopic},
		Data: make([]byte, 32),
	})
	require.Error(t, err)
}

func protocolTestRoute(
	gateway types.Address,
	sourceRouter types.Address,
	mailbox types.Address,
	hook types.Address,
	destinationRouter types.Address,
) protocol.ILNRoute {
	return protocol.ILNRoute{
		Key: protocol.ILNRouteKey{
			SourceChainID: 8453, SourceDomain: 8453, DestinationDomain: 1643,
			RouteID: types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		},
		Gateway: gateway,
		SourceRouter: sourceRouter,
		Mailbox: mailbox,
		MerkleTreeHook: hook,
		DestinationRouter: destinationRouter,
		ValidatorFeeWei: big.NewInt(1),
		Enabled: true,
	}
}

func selectorHex(signature string) string {
	hash := crypto.Keccak256([]byte(signature))
	return fmt.Sprintf("0x%x", hash[:4])
}
