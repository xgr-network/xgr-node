package e2e

import (
	"encoding/hex"
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
	confirmations uint64
	sourceDomain      uint32
	destinationDomain uint32
	routeID           string
	registry          string
	gateway           string
	sourceRouter      string
	mailbox           string
	hook              string
	destinationRouter string
	feeWei            uint64
	root              string
	count             uint64
	insertions        []map[string]interface{}
	invalidHookRoot bool
	getLogsCalls int
}

func newILNRPCFixture(
	t *testing.T,
	chainID uint64,
	head uint64,
	confirmations uint64,
	sourceDomain uint32,
	destinationDomain uint32,
	routeID string,
	registry string,
	gateway string,
	sourceRouter string,
	mailbox string,
	hook string,
	destinationRouter string,
	feeWei uint64,
	seed string,
	count uint64,
) (*ilnRPCFixture, *evmInterchain.Destination) {
	t.Helper()

	fixture := &ilnRPCFixture{
		chainID:           chainID,
		head:              head,
		confirmations: confirmations,
		sourceDomain:      sourceDomain,
		destinationDomain: destinationDomain,
		routeID:           routeID,
		registry:          registry,
		gateway:           gateway,
		sourceRouter:      sourceRouter,
		mailbox:           mailbox,
		hook:              hook,
		destinationRouter: destinationRouter,
		feeWei:            feeWei,
		count:             count,
	}


	// These are canonical, ordered Merkle insertions rather than arbitrary
	// root()/count() mock values. Each route has an independent history.
	seedHash := types.StringToHash(seed)
	leaves := make([]types.Hash, 0, count)
	fixture.insertions = make([]map[string]interface{}, 0, count)
	insertedTopic := crypto.Keccak256Hash([]byte("InsertedIntoTree(bytes32,uint32)"))
	for i := uint64(0); i < count; i++ {
		leaf := crypto.Keccak256Hash(append(append([]byte(nil), seedHash.Bytes()...), byte(i)))
		leaves = append(leaves, leaf)
		fixture.insertions = append(fixture.insertions, map[string]interface{}{
			"address": fixture.hook,
			"topics": []string{insertedTopic.String()},
			"data": "0x" + hex.EncodeToString(leaf.Bytes()) + fmt.Sprintf("%064x", i),
			"blockNumber": fmt.Sprintf("0x%x", uint64(2)+i),
			"logIndex": "0x0",
			"transactionIndex": "0x0",
			"transactionHash": leaf.String(),
			"removed": false,
		})
	}
	fixture.root = independentILNFixtureRoot(leaves).String()

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
            expectedTag := "latest"
            if strings.EqualFold(call.To, fixture.hook) {
                checkpoint := (fixture.head - fixture.confirmations) / 32 * 32
                expectedTag = fmt.Sprintf("0x%x", checkpoint)
            }
            require.Equal(t, expectedTag, blockTag,
                "only recent confirmed hook state may use a numeric block; all route reads use latest")

			to := strings.ToLower(call.To)
			data := strings.ToLower(call.Data)
			var result string

			switch to {
			case strings.ToLower(fixture.registry):
				switch {
				case data == selectorHex("factory()"):
					// V315 exposes a non-zero immutable factory. The source
					// implementation must never infer append-only status from
					// a historical EVM-state snapshot.
					result = encodeAddressResult("0x1717171717171717171717171717171717171717")
				case data == selectorHex("activationBlock()"):
					result = "0x" + fmt.Sprintf("%064x", uint64(1))
				case strings.HasPrefix(data, selectorHex("getRoute(uint32,bytes32)")):
					require.Contains(t, data, strings.TrimPrefix(strings.ToLower(fixture.routeID), "0x"))
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
				default:
					t.Errorf("unexpected registry eth_call data %s", call.Data)
					http.Error(w, "unsupported registry selector", http.StatusBadRequest)
					return
				}
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
                case selectorHex("mailbox()"):
                    result = encodeAddressResult(fixture.mailbox)
                case selectorHex("count()"):
                    result = "0x" + fmt.Sprintf("%064x", fixture.count)
                case selectorHex("root()"):
                    fixture.mu.Lock()
                    invalid := fixture.invalidHookRoot
                    fixture.mu.Unlock()
                    if invalid {
                        result = "0x" + strings.Repeat("0", 64)
                    } else {
                        result = fixture.root
                    }
                default:
                    t.Errorf("unexpected hook selector %s", call.Data)
                    http.Error(w, "unsupported hook selector", http.StatusBadRequest)
                    return
                }
			default:
				t.Fatalf("unexpected eth_call target %s", call.To)
			}

			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"%s"}`, req.ID, result)
		case "eth_getLogs":
            fixture.mu.Lock()
            fixture.getLogsCalls++
            fixture.mu.Unlock()
            _, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32005,"message":"historical logs unavailable"}}`, req.ID)
		default:
			t.Errorf("unexpected JSON-RPC method %s", req.Method)
			http.Error(w, "unsupported RPC method", http.StatusBadRequest)
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


// independentILNFixtureRoot calculates the complete fixed-depth binary Merkle
// tree from leaf history. The E2E oracle must not reuse the runtime's
// incremental accumulator (which is itself the code under test).
func independentILNFixtureRoot(leaves []types.Hash) types.Hash {
	level := append([]types.Hash(nil), leaves...)
	zero := types.ZeroHash
	for depth := 0; depth < 32; depth++ {
		if len(level)%2 != 0 {
			level = append(level, zero)
		}
		next := make([]types.Hash, 0, len(level)/2)
		for i := 0; i < len(level); i += 2 {
			pair := append(append([]byte(nil), level[i].Bytes()...), level[i+1].Bytes()...)
			next = append(next, crypto.Keccak256Hash(pair))
		}
		level = next
		zero = crypto.Keccak256Hash(append(zero.Bytes(), zero.Bytes()...))
	}
	return level[0]
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
		baseSeed              = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

		arbitrumRegistry          = "0x2020202020202020202020202020202020202020"
		arbitrumGateway           = "0x2121212121212121212121212121212121212121"
		arbitrumSourceRouter      = "0x2222222222222222222222222222222222222222"
		arbitrumMailbox           = "0x2323232323232323232323232323232323232323"
		arbitrumHook              = "0x2424242424242424242424242424242424242424"
		arbitrumDestinationRouter = "0x2525252525252525252525252525252525252525"
		arbitrumSeed              = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)

	baseRPC, base := newILNRPCFixture(
		t, 8453, 100, 3, 8453, xgrDomain, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		baseRegistry, baseGateway, baseSourceRouter, baseMailbox, baseHook, baseDestinationRouter,
		1000, baseSeed, 4,
	)
	base.Name = "base"

	arbitrumRPC, arbitrum := newILNRPCFixture(
		t, 42161, 200, 20, 42161, xgrDomain, "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		arbitrumRegistry, arbitrumGateway, arbitrumSourceRouter, arbitrumMailbox, arbitrumHook, arbitrumDestinationRouter,
		2000, arbitrumSeed, 9,
	)
	arbitrum.Name = "arbitrum"

	xgr := &evmInterchain.Destination{
		Name:   "xgr",
		ChainID: 1643,
		Domain:  xgrDomain,
	}

	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_NETWORK", "base")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_DESTINATION", "xgr")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_ROUTE_ID", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	t.Setenv("XGR_INTERCHAIN_ROUTE_ARBITRUM_TO_XGR_SOURCE_NETWORK", "arbitrum")
	t.Setenv("XGR_INTERCHAIN_ROUTE_ARBITRUM_TO_XGR_DESTINATION", "xgr")
	t.Setenv("XGR_INTERCHAIN_ROUTE_ARBITRUM_TO_XGR_ROUTE_ID", "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

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

	baseSnapshot, err := evmInterchain.GetConfirmedILNRoute(base, xgrDomain, baseRoute.RouteID)
	require.NoError(t, err)
	require.Equal(t, uint64(97), baseSnapshot.BlockNumber)
	require.Equal(t, uint64(8453), baseSnapshot.Route.Key.SourceChainID)
	require.Equal(t, uint32(8453), baseSnapshot.Route.Key.SourceDomain)
	require.Equal(t, xgrDomain, baseSnapshot.Route.Key.DestinationDomain)
	require.Equal(t, types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), baseSnapshot.Route.Key.RouteID)
	require.Equal(t, types.StringToAddress(baseGateway), baseSnapshot.Route.Gateway)
	require.Equal(t, types.StringToAddress(baseSourceRouter), baseSnapshot.Route.SourceRouter)
	require.Equal(t, types.StringToAddress(baseDestinationRouter), baseSnapshot.Route.DestinationRouter)
	require.Equal(t, int64(1000), baseSnapshot.Route.ValidatorFeeWei.Int64())

	baseCheckpoint, err := evmInterchain.GetConfirmedILNCheckpoint(base, baseSnapshot)
	require.NoError(t, err)
	require.Equal(t, uint64(96), baseCheckpoint.BlockNumber)
	require.Equal(t, uint32(3), baseCheckpoint.Index)
	require.Equal(t, types.StringToHash(baseRPC.root), baseCheckpoint.Root)
	require.Contains(t, baseRPC.blockTags(), "0x60")
    require.Zero(t, baseRPC.getLogsCalls)

	arbitrumRoute := byName["arbitrum_to_xgr"]
	require.NotNil(t, arbitrumRoute)
	require.Equal(t, "arbitrum", arbitrumRoute.SourceNetwork)

	arbitrumSnapshot, err := evmInterchain.GetConfirmedILNRoute(arbitrum, xgrDomain, arbitrumRoute.RouteID)
	require.NoError(t, err)
	require.Equal(t, uint64(180), arbitrumSnapshot.BlockNumber)
	require.Equal(t, uint64(42161), arbitrumSnapshot.Route.Key.SourceChainID)
	require.Equal(t, uint32(42161), arbitrumSnapshot.Route.Key.SourceDomain)
	require.Equal(t, xgrDomain, arbitrumSnapshot.Route.Key.DestinationDomain)
	require.Equal(t, types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), arbitrumSnapshot.Route.Key.RouteID)
	require.Equal(t, types.StringToAddress(arbitrumGateway), arbitrumSnapshot.Route.Gateway)
	require.Equal(t, types.StringToAddress(arbitrumSourceRouter), arbitrumSnapshot.Route.SourceRouter)
	require.Equal(t, types.StringToAddress(arbitrumDestinationRouter), arbitrumSnapshot.Route.DestinationRouter)
	require.Equal(t, int64(2000), arbitrumSnapshot.Route.ValidatorFeeWei.Int64())

	arbitrumCheckpoint, err := evmInterchain.GetConfirmedILNCheckpoint(arbitrum, arbitrumSnapshot)
	require.NoError(t, err)
	require.Equal(t, uint64(160), arbitrumCheckpoint.BlockNumber)
	require.Equal(t, uint32(8), arbitrumCheckpoint.Index)
	require.Equal(t, types.StringToHash(arbitrumRPC.root), arbitrumCheckpoint.Root)
	require.Contains(t, arbitrumRPC.blockTags(), "0xa0")
    require.Zero(t, arbitrumRPC.getLogsCalls)

	require.NotEqual(t, baseCheckpoint.Root, arbitrumCheckpoint.Root)
	require.NotEqual(t, baseRoute.Name, arbitrumRoute.Name)

	// A corrupt or empty canonical hook root is rejected, even when its
    // count matches. No historical eth_getLogs fallback is permissible.
    baseRPC.mu.Lock()
    baseRPC.invalidHookRoot = true
    baseRPC.mu.Unlock()
    _, err = evmInterchain.GetConfirmedILNCheckpoint(base, baseSnapshot)
    require.ErrorContains(t, err, "noncanonical ILN hook root")
    require.Zero(t, baseRPC.getLogsCalls)
}
