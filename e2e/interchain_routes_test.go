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

type ilnRPCFixture struct {
	server *httptest.Server

	mu            sync.Mutex
	lastBlockTags []string

	chainID           uint64
	head              uint64
	sourceDomain      uint32
	destinationDomain uint32
	registry          string
	gateway           string
	sourceRouter      string
	mailbox           string
	hook              string
	destinationRouter string
	feeWei            uint64
	root              string
	count             uint64
}

func newILNRPCFixture(
	t *testing.T,
	chainID uint64,
	head uint64,
	confirmations uint64,
	sourceDomain uint32,
	destinationDomain uint32,
	registry string,
	gateway string,
	sourceRouter string,
	mailbox string,
	hook string,
	destinationRouter string,
	feeWei uint64,
	root string,
	count uint64,
) (*ilnRPCFixture, *evmInterchain.Destination) {
	t.Helper()

	fixture := &ilnRPCFixture{
		chainID:           chainID,
		head:              head,
		sourceDomain:      sourceDomain,
		destinationDomain: destinationDomain,
		registry:          registry,
		gateway:           gateway,
		sourceRouter:      sourceRouter,
		mailbox:           mailbox,
		hook:              hook,
		destinationRouter: destinationRouter,
		feeWei:            feeWei,
		root:              root,
		count:             count,
	}

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
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"0x%x"}`, req.ID, fixture.chainID)
		case "eth_blockNumber":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"0x%x"}`, req.ID, fixture.head)
		case "eth_call":
			require.Len(t, req.Params, 2)
			var call struct {
				To   string `json:"to"`
				Data string `json:"data"`
			}
			require.NoError(t, json.Unmarshal(req.Params[0], &call))
			var blockTag string
			require.NoError(t, json.Unmarshal(req.Params[1], &blockTag))

			fixture.mu.Lock()
			fixture.lastBlockTags = append(fixture.lastBlockTags, blockTag)
			fixture.mu.Unlock()

			to := strings.ToLower(call.To)
			data := strings.ToLower(call.Data)
			var result string

			switch to {
			case strings.ToLower(fixture.registry):
				require.True(t, strings.HasPrefix(data, selectorHex("getRoute(uint32)")))
				result = encodeILNRouteResult(
					fixture.chainID,
					fixture.sourceDomain,
					fixture.gateway,
					fixture.sourceRouter,
					fixture.mailbox,
					fixture.hook,
					fixture.destinationRouter,
					fixture.feeWei,
				)
			case strings.ToLower(fixture.gateway):
				switch data {
				case selectorHex("ilnRegistry()"):
					result = encodeAddressResult(fixture.registry)
				case selectorHex("warpRouter()"):
					result = encodeAddressResult(fixture.sourceRouter)
				case selectorHex("mailbox()"):
					result = encodeAddressResult(fixture.mailbox)
				case selectorHex("merkleTreeHook()"):
					result = encodeAddressResult(fixture.hook)
				case selectorHex("activationBlock()"):
					result = "0x" + fmt.Sprintf("%064x", uint64(1))
				default:
					t.Fatalf("unexpected gateway eth_call data %s", call.Data)
				}
			case strings.ToLower(fixture.hook):
				switch data {
				case selectorHex("count()"):
					result = "0x" + fmt.Sprintf("%064x", fixture.count)
				case selectorHex("root()"):
					result = fixture.root
				default:
					t.Fatalf("unexpected hook eth_call data %s", call.Data)
				}
			default:
				t.Fatalf("unexpected eth_call target %s", call.To)
			}

			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"%s"}`, req.ID, result)
		default:
			t.Fatalf("unexpected JSON-RPC method %s", req.Method)
		}
	}))

	t.Cleanup(fixture.server.Close)

	network := &evmInterchain.Destination{
		ChainID:            chainID,
		Domain:             sourceDomain,
		ILNRegistryAddress: registry,
		RPCURL:             fixture.server.URL,
		Confirmations:      confirmations,
	}
	return fixture, network
}

func (f *ilnRPCFixture) blockTags() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lastBlockTags...)
}

func selectorHex(signature string) string {
	hash := crypto.Keccak256([]byte(signature))
	return fmt.Sprintf("0x%x", hash[:4])
}

func encodeAddressResult(value string) string {
	return "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(strings.ToLower(value), "0x")
}

func encodeILNRouteResult(
	sourceChainID uint64,
	sourceDomain uint32,
	gateway string,
	sourceRouter string,
	mailbox string,
	hook string,
	destinationRouter string,
	feeWei uint64,
) string {
	wordUint := func(value uint64) string {
		return fmt.Sprintf("%064x", value)
	}
	wordAddress := func(value string) string {
		return strings.Repeat("0", 24) + strings.TrimPrefix(strings.ToLower(value), "0x")
	}

	return "0x" +
		wordUint(sourceChainID) +
		wordUint(uint64(sourceDomain)) +
		wordAddress(gateway) +
		wordAddress(sourceRouter) +
		wordAddress(mailbox) +
		wordAddress(hook) +
		wordAddress(destinationRouter) +
		wordUint(feeWei) +
		wordUint(1)
}

func TestInterchainILNRoutesE2E(t *testing.T) {
	const (
		xgrDomain = uint32(1643)

		baseRegistry          = "0x1010101010101010101010101010101010101010"
		baseGateway           = "0x1111111111111111111111111111111111111111"
		baseSourceRouter      = "0x1212121212121212121212121212121212121212"
		baseMailbox           = "0x1313131313131313131313131313131313131313"
		baseHook              = "0x1414141414141414141414141414141414141414"
		baseDestinationRouter = "0x1515151515151515151515151515151515151515"
		baseRoot              = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

		arbitrumRegistry          = "0x2020202020202020202020202020202020202020"
		arbitrumGateway           = "0x2121212121212121212121212121212121212121"
		arbitrumSourceRouter      = "0x2222222222222222222222222222222222222222"
		arbitrumMailbox           = "0x2323232323232323232323232323232323232323"
		arbitrumHook              = "0x2424242424242424242424242424242424242424"
		arbitrumDestinationRouter = "0x2525252525252525252525252525252525252525"
		arbitrumRoot              = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)

	baseRPC, base := newILNRPCFixture(
		t, 8453, 100, 3, 8453, xgrDomain,
		baseRegistry, baseGateway, baseSourceRouter, baseMailbox, baseHook, baseDestinationRouter,
		1000, baseRoot, 4,
	)
	base.Name = "base"

	arbitrumRPC, arbitrum := newILNRPCFixture(
		t, 42161, 200, 20, 42161, xgrDomain,
		arbitrumRegistry, arbitrumGateway, arbitrumSourceRouter, arbitrumMailbox, arbitrumHook, arbitrumDestinationRouter,
		2000, arbitrumRoot, 9,
	)
	arbitrum.Name = "arbitrum"

	xgr := &evmInterchain.Destination{
		Name:   "xgr",
		ChainID: 1643,
		Domain:  xgrDomain,
	}

	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_NETWORK", "base")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_DESTINATION", "xgr")
	t.Setenv("XGR_INTERCHAIN_ROUTE_ARBITRUM_TO_XGR_SOURCE_NETWORK", "arbitrum")
	t.Setenv("XGR_INTERCHAIN_ROUTE_ARBITRUM_TO_XGR_DESTINATION", "xgr")

	routes, err := evmInterchain.LoadCheckpointRoutes([]*evmInterchain.Destination{base, arbitrum, xgr})
	require.NoError(t, err)
	require.Len(t, routes, 2)

	byName := make(map[string]*evmInterchain.CheckpointRoute, len(routes))
	for _, route := range routes {
		byName[route.Name] = route
		require.Equal(t, "xgr", route.Destination)
	}

	baseRoute := byName["base_to_xgr"]
	require.NotNil(t, baseRoute)
	require.Equal(t, "base", baseRoute.SourceNetwork)

	baseSnapshot, err := evmInterchain.GetConfirmedILNRoute(base, xgrDomain)
	require.NoError(t, err)
	require.Equal(t, uint64(97), baseSnapshot.BlockNumber)
	require.Equal(t, uint64(8453), baseSnapshot.Route.Key.SourceChainID)
	require.Equal(t, uint32(8453), baseSnapshot.Route.Key.SourceDomain)
	require.Equal(t, xgrDomain, baseSnapshot.Route.Key.DestinationDomain)
	require.Equal(t, types.StringToAddress(baseGateway), baseSnapshot.Route.Gateway)
	require.Equal(t, types.StringToAddress(baseSourceRouter), baseSnapshot.Route.SourceRouter)
	require.Equal(t, types.StringToAddress(baseDestinationRouter), baseSnapshot.Route.DestinationRouter)
	require.Equal(t, int64(1000), baseSnapshot.Route.ValidatorFeeWei.Int64())

	baseCheckpoint, err := evmInterchain.GetConfirmedILNCheckpoint(base, baseSnapshot)
	require.NoError(t, err)
	require.Equal(t, uint64(97), baseCheckpoint.BlockNumber)
	require.Equal(t, uint32(3), baseCheckpoint.Index)
	require.Equal(t, types.StringToHash(baseRoot), baseCheckpoint.Root)
	for _, tag := range baseRPC.blockTags() {
		require.Equal(t, "0x61", tag)
	}

	arbitrumRoute := byName["arbitrum_to_xgr"]
	require.NotNil(t, arbitrumRoute)
	require.Equal(t, "arbitrum", arbitrumRoute.SourceNetwork)

	arbitrumSnapshot, err := evmInterchain.GetConfirmedILNRoute(arbitrum, xgrDomain)
	require.NoError(t, err)
	require.Equal(t, uint64(180), arbitrumSnapshot.BlockNumber)
	require.Equal(t, uint64(42161), arbitrumSnapshot.Route.Key.SourceChainID)
	require.Equal(t, uint32(42161), arbitrumSnapshot.Route.Key.SourceDomain)
	require.Equal(t, xgrDomain, arbitrumSnapshot.Route.Key.DestinationDomain)
	require.Equal(t, types.StringToAddress(arbitrumGateway), arbitrumSnapshot.Route.Gateway)
	require.Equal(t, types.StringToAddress(arbitrumSourceRouter), arbitrumSnapshot.Route.SourceRouter)
	require.Equal(t, types.StringToAddress(arbitrumDestinationRouter), arbitrumSnapshot.Route.DestinationRouter)
	require.Equal(t, int64(2000), arbitrumSnapshot.Route.ValidatorFeeWei.Int64())

	arbitrumCheckpoint, err := evmInterchain.GetConfirmedILNCheckpoint(arbitrum, arbitrumSnapshot)
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
