package evm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

func TestLoadCheckpointRoutesFallsBackToLocalDestinationRoutes(t *testing.T) {
	destinations := []*Destination{{Name: "base", ChainID: 8453, Domain: 8453}}
	routes, err := LoadCheckpointRoutes(destinations)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Equal(t, "base", routes[0].Name)
	require.Equal(t, "base", routes[0].Destination)
	require.True(t, routes[0].IsLocal())
}

func TestLoadCheckpointRoutesExternalEVM(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_DESTINATION", "xgr")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_TYPE", "evm")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_CHAIN_ID", "8453")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_DOMAIN", "8453")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_RPC", "https://base.example.invalid")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_MAILBOX_ADDR", "0x1111111111111111111111111111111111111111")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_MERKLE_TREE_HOOK_ADDR", "0x2222222222222222222222222222222222222222")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_CONFIRMATIONS", "12")

	routes, err := LoadCheckpointRoutes(nil)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	route := routes[0]
	require.Equal(t, "base_to_xgr", route.Name)
	require.Equal(t, "xgr", route.Destination)
	require.Equal(t, CheckpointSourceEVM, route.SourceType)
	require.NotNil(t, route.Source)
	require.Equal(t, uint64(8453), route.Source.ChainID)
	require.Equal(t, uint32(8453), route.Source.Domain)
	require.Equal(t, uint64(12), route.Source.Confirmations)
}

func TestLoadCheckpointRoutesAllowsMultipleOriginsToSameDestination(t *testing.T) {
	for _, name := range []string{"BASE_TO_XGR", "ARBITRUM_TO_XGR"} {
		prefix := "XGR_INTERCHAIN_ROUTE_" + name + "_"
		t.Setenv(prefix+"DESTINATION", "xgr")
		t.Setenv(prefix+"SOURCE_TYPE", "evm")
		t.Setenv(prefix+"SOURCE_CHAIN_ID", map[string]string{"BASE_TO_XGR": "8453", "ARBITRUM_TO_XGR": "42161"}[name])
		t.Setenv(prefix+"SOURCE_DOMAIN", map[string]string{"BASE_TO_XGR": "8453", "ARBITRUM_TO_XGR": "42161"}[name])
		t.Setenv(prefix+"SOURCE_RPC", "https://example.invalid")
		t.Setenv(prefix+"SOURCE_MAILBOX_ADDR", "0x1111111111111111111111111111111111111111")
		t.Setenv(prefix+"SOURCE_MERKLE_TREE_HOOK_ADDR", "0x2222222222222222222222222222222222222222")
		t.Setenv(prefix+"SOURCE_CONFIRMATIONS", "12")
	}
	routes, err := LoadCheckpointRoutes(nil)
	require.NoError(t, err)
	require.Len(t, routes, 2)
	require.Equal(t, "xgr", routes[0].Destination)
	require.Equal(t, "xgr", routes[1].Destination)
	require.NotEqual(t, routes[0].Name, routes[1].Name)
}

func TestLoadCheckpointRoutesRejectsPartialExternalSource(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_DESTINATION", "xgr")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_TYPE", "evm")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_CHAIN_ID", "8453")

	_, err := LoadCheckpointRoutes(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires CHAIN_ID")
}

func TestLoadCheckpointRoutesRejectsExternalFieldsForLocalRoute(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_ROUTE_XGR_TO_BASE_DESTINATION", "base")
	t.Setenv("XGR_INTERCHAIN_ROUTE_XGR_TO_BASE_SOURCE_TYPE", "local")
	t.Setenv("XGR_INTERCHAIN_ROUTE_XGR_TO_BASE_SOURCE_CHAIN_ID", "1643")

	_, err := LoadCheckpointRoutes(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "must not define external")
}

func TestGetConfirmedCheckpointReadsExplicitConfirmedBlock(t *testing.T) {
	mailbox := "0x1111111111111111111111111111111111111111"
	hook := "0x2222222222222222222222222222222222222222"
	root := "0x3333333333333333333333333333333333333333333333333333333333333333"

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
			require.Len(t, req.Params, 2)
			var call struct {
				Data string `json:"data"`
			}
			require.NoError(t, json.Unmarshal(req.Params[0], &call))
			var blockTag string
			require.NoError(t, json.Unmarshal(req.Params[1], &blockTag))
			require.Equal(t, "0x61", blockTag)

			var result string
			switch strings.ToLower(call.Data) {
			case fmt.Sprintf("0x%x", checkpointSourceSelector("mailbox()")):
				result = "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(mailbox, "0x")
			case fmt.Sprintf("0x%x", checkpointSourceSelector("count()")):
				result = "0x" + strings.Repeat("0", 63) + "4"
			case fmt.Sprintf("0x%x", checkpointSourceSelector("root()")):
				result = root
			default:
				t.Fatalf("unexpected eth_call data %s", call.Data)
			}
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"%s"}`, req.ID, result)
		default:
			t.Fatalf("unexpected JSON-RPC method %s", req.Method)
		}
	}))
	defer server.Close()

	source := &CheckpointSource{
		ChainID:        8453,
		Domain:         8453,
		RPCURL:         server.URL,
		Mailbox:        types.StringToAddress(mailbox),
		MerkleTreeHook: types.StringToAddress(hook),
		Confirmations:  3,
	}

	checkpoint, err := GetConfirmedCheckpoint(source)
	require.NoError(t, err)
	require.Equal(t, uint64(97), checkpoint.BlockNumber)
	require.Equal(t, uint32(3), checkpoint.Index)
	require.Equal(t, types.StringToHash(root), checkpoint.Root)
}

func checkpointSourceSelector(signature string) []byte {
	hash := crypto.Keccak256([]byte(signature))
	return hash[:4]
}
