package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xgr-network/xgr-node/crypto"
	evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
	"github.com/xgr-network/xgr-node/types"
)

type checkpointRPCFixture struct {
	server        *httptest.Server
	mu            sync.Mutex
	lastBlockTags []string
}

func newCheckpointRPCFixture(
	t *testing.T,
	chainID uint64,
	head uint64,
	mailbox string,
	root string,
	count uint64,
) *checkpointRPCFixture {
	t.Helper()

	fixture := &checkpointRPCFixture{}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		w.Header().Set("Content-Type", "application/json")

		switch req.Method {
		case "eth_chainId":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"0x%x"}`, req.ID, chainID)
		case "eth_blockNumber":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"0x%x"}`, req.ID, head)
		case "eth_call":
			require.Len(t, req.Params, 2)
			var call struct {
				Data string `json:"data"`
			}
			require.NoError(t, json.Unmarshal(req.Params[0], &call))
			var blockTag string
			require.NoError(t, json.Unmarshal(req.Params[1], &blockTag))

			fixture.mu.Lock()
			fixture.lastBlockTags = append(fixture.lastBlockTags, blockTag)
			fixture.mu.Unlock()

			var result string
			switch strings.ToLower(call.Data) {
			case fmt.Sprintf("0x%x", checkpointSelector("mailbox()")):
				result = "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(mailbox, "0x")
			case fmt.Sprintf("0x%x", checkpointSelector("count()")):
				result = fmt.Sprintf("0x%064x", count)
			case fmt.Sprintf("0x%x", checkpointSelector("root()")):
				result = root
			default:
				t.Fatalf("unexpected eth_call data %s", call.Data)
			}
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"%s"}`, req.ID, result)
		default:
			t.Fatalf("unexpected JSON-RPC method %s", req.Method)
		}
	}))

	t.Cleanup(fixture.server.Close)
	return fixture
}

func (f *checkpointRPCFixture) URL() string {
	return f.server.URL
}

func (f *checkpointRPCFixture) blockTags() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lastBlockTags...)
}

func checkpointSelector(signature string) []byte {
	hash := crypto.Keccak256([]byte(signature))
	return hash[:4]
}

func TestInterchainExternalCheckpointRoutesE2E(t *testing.T) {
	const (
		baseMailbox     = "0x1111111111111111111111111111111111111111"
		arbitrumMailbox = "0x2222222222222222222222222222222222222222"
		baseHook        = "0x3333333333333333333333333333333333333333"
		arbitrumHook    = "0x4444444444444444444444444444444444444444"
		baseRoot        = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		arbitrumRoot    = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)

	baseRPC := newCheckpointRPCFixture(t, 8453, 100, baseMailbox, baseRoot, 4)
	arbitrumRPC := newCheckpointRPCFixture(t, 42161, 200, arbitrumMailbox, arbitrumRoot, 9)

	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_DESTINATION", "xgr")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_TYPE", "evm")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_CHAIN_ID", "8453")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_DOMAIN", "8453")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_RPC", baseRPC.URL())
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_MAILBOX_ADDR", baseMailbox)
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_MERKLE_TREE_HOOK_ADDR", baseHook)
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_CONFIRMATIONS", "3")

	t.Setenv("XGR_INTERCHAIN_ROUTE_ARBITRUM_TO_XGR_DESTINATION", "xgr")
	t.Setenv("XGR_INTERCHAIN_ROUTE_ARBITRUM_TO_XGR_SOURCE_TYPE", "evm")
	t.Setenv("XGR_INTERCHAIN_ROUTE_ARBITRUM_TO_XGR_SOURCE_CHAIN_ID", "42161")
	t.Setenv("XGR_INTERCHAIN_ROUTE_ARBITRUM_TO_XGR_SOURCE_DOMAIN", "42161")
	t.Setenv("XGR_INTERCHAIN_ROUTE_ARBITRUM_TO_XGR_SOURCE_RPC", arbitrumRPC.URL())
	t.Setenv("XGR_INTERCHAIN_ROUTE_ARBITRUM_TO_XGR_SOURCE_MAILBOX_ADDR", arbitrumMailbox)
	t.Setenv("XGR_INTERCHAIN_ROUTE_ARBITRUM_TO_XGR_SOURCE_MERKLE_TREE_HOOK_ADDR", arbitrumHook)
	t.Setenv("XGR_INTERCHAIN_ROUTE_ARBITRUM_TO_XGR_SOURCE_CONFIRMATIONS", "20")

	routes, err := evmInterchain.LoadCheckpointRoutes(nil)
	require.NoError(t, err)
	require.Len(t, routes, 2)

	byName := make(map[string]*evmInterchain.CheckpointRoute, len(routes))
	for _, route := range routes {
		byName[route.Name] = route
		require.Equal(t, "xgr", route.Destination)
		require.Equal(t, evmInterchain.CheckpointSourceEVM, route.SourceType)
	}

	baseRoute := byName["base_to_xgr"]
	require.NotNil(t, baseRoute)
	require.Equal(t, uint64(8453), baseRoute.Source.ChainID)
	require.Equal(t, uint32(8453), baseRoute.Source.Domain)

	baseCheckpoint, err := evmInterchain.GetConfirmedCheckpoint(baseRoute.Source)
	require.NoError(t, err)
	require.Equal(t, uint64(97), baseCheckpoint.BlockNumber)
	require.Equal(t, uint32(3), baseCheckpoint.Index)
	require.Equal(t, types.StringToHash(baseRoot), baseCheckpoint.Root)
	for _, tag := range baseRPC.blockTags() {
		require.Equal(t, "0x61", tag)
	}

	arbitrumRoute := byName["arbitrum_to_xgr"]
	require.NotNil(t, arbitrumRoute)
	require.Equal(t, uint64(42161), arbitrumRoute.Source.ChainID)
	require.Equal(t, uint32(42161), arbitrumRoute.Source.Domain)

	arbitrumCheckpoint, err := evmInterchain.GetConfirmedCheckpoint(arbitrumRoute.Source)
	require.NoError(t, err)
	require.Equal(t, uint64(180), arbitrumCheckpoint.BlockNumber)
	require.Equal(t, uint32(8), arbitrumCheckpoint.Index)
	require.Equal(t, types.StringToHash(arbitrumRoot), arbitrumCheckpoint.Root)
	for _, tag := range arbitrumRPC.blockTags() {
		require.Equal(t, "0xb4", tag)
	}

	require.NotEqual(t, baseCheckpoint.Root, arbitrumCheckpoint.Root)
	require.NotEqual(t, baseRoute.Name, arbitrumRoute.Name)
}
