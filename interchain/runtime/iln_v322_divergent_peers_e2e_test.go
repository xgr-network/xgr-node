package runtime

import (
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	evm "github.com/xgr-network/xgr-node/interchain/evm"
	"github.com/xgr-network/xgr-node/network"
	"github.com/xgr-network/xgr-node/types"
)

// TestV322ThreeValidatorDivergentHeadMerkleP2PE2E deliberately uses the real
// authenticated GossipSub network while each validator observes a DIFFERENT
// source head. It is an ILN transport/reconstruction test, not a claim that
// mocked source RPC responses prove Ethereum finality or BLS quorum.
func TestV322ThreeValidatorDivergentHeadMerkleP2PE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("real three-peer libp2p integration")
	}

	// Source RPC views differ by two full epochs in BOTH directions. This
	// reproduces the old epoch-exact P2P request/response rejection.
	heights := map[string]uint64{
		"provider": 160,
		"behind":   128,
		"ahead":    192,
	}
	message := crypto.Keccak256Hash([]byte("v3.2.2 independent source leaf"))
	hook := types.StringToAddress("0x4444444444444444444444444444444444444444")
	root := ilnTestCanonicalRoot(message)
	registry := types.StringToAddress("0x5555555555555555555555555555555555555555")
	gateway := types.StringToAddress("0x2222222222222222222222222222222222222222")
	mailbox := types.StringToAddress("0x3333333333333333333333333333333333333333")
	router := types.StringToAddress("0x7777777777777777777777777777777777777777")
	destRouter := types.StringToAddress("0x6666666666666666666666666666666666666666")
	route := &evm.CheckpointRoute{
		Name: "arbitrary_source_to_xgr", SourceNetwork: "source",
		Destination: "xgr", RouteID: testCheckpointRouteID,
	}
	sourceRoute := protocol.ILNRoute{
		Key: protocol.ILNRouteKey{
			SourceChainID: 42161, SourceDomain: 42161,
			DestinationDomain: 1643, RouteID: testCheckpointRouteID,
		},
		Gateway: gateway, SourceRouter: router, Mailbox: mailbox,
		MerkleTreeHook: hook, DestinationRouter: destRouter,
		ValidatorFeeWei: big.NewInt(10), Enabled: true,
	}
	oldHead := getConfirmedILNHead
	oldRoute := getILNRouteAtBlock
	oldCheckpoint := getConfirmedILNCheckpoint
	t.Cleanup(func() {
		getConfirmedILNHead = oldHead
		getILNRouteAtBlock = oldRoute
		getConfirmedILNCheckpoint = oldCheckpoint
	})
	getConfirmedILNHead = func(source *evm.Destination) (uint64, error) {
		height, ok := heights[source.Name]
		if !ok {
			return 0, fmt.Errorf("unregistered source view: %s", source.Name)
		}
		return height, nil
	}
	getILNRouteAtBlock = func(source *evm.Destination, destination uint32, routeID types.Hash, block uint64) (*evm.ILNRouteSnapshot, error) {
		if source.ChainID != 42161 || destination != 1643 ||
			routeID != testCheckpointRouteID || block != heights[source.Name] {
			return nil, fmt.Errorf("unexpected source checkpoint request")
		}
		return &evm.ILNRouteSnapshot{Registry: registry, Route: sourceRoute, BlockNumber: block}, nil
	}
	getConfirmedILNCheckpoint = func(_ *evm.Destination, snapshot *evm.ILNRouteSnapshot) (*evm.ConfirmedCheckpoint, error) {
		return &evm.ConfirmedCheckpoint{
			Root: root, Index: 0, BlockNumber: snapshot.BlockNumber,
		}, nil
	}

	names := []string{"provider", "behind", "ahead"}
	servers := make([]*network.Server, 0, len(names))
	for range names {
		s, err := network.CreateServer(&network.CreateServerParams{
			ConfigCallback: func(c *network.Config) {
				c.NoDiscover = true
				c.DataDir = t.TempDir()
				c.MaxInboundPeers = 6
				c.MaxOutboundPeers = 6
			},
			Logger: hclog.NewNullLogger(),
		})
		require.NoError(t, err)
		servers = append(servers, s)
	}
	t.Cleanup(func() {
		for _, s := range servers {
			require.NoError(t, s.Close())
		}
	})
	for _, err := range network.MeshJoin(servers...) {
		require.NoError(t, err)
	}

	workers := make([]*Worker, len(names))
	for i, name := range names {
		w := &Worker{
			logger: hclog.NewNullLogger(),
			network: servers[i],
			dataDir: t.TempDir(),
			destinations: map[string]*evm.Destination{
				"source": {
					Name: name, ChainID: 42161, Domain: 42161,
					ILNRegistryAddress: registry.String(),
				},
				"xgr": {Name: "xgr", ChainID: 1643, Domain: 1643},
			},
			routes: map[string]*evm.CheckpointRoute{route.Name: route},
			ilnReplicaSync: make(map[string]*ilnReplicaSyncState),
			ilnReplicaServed: make(map[peer.ID]time.Time),
		}
		topic, err := servers[i].NewTopic(TopicID, &wrapperspb.BytesValue{})
		require.NoError(t, err)
		w.topic = topic
		workers[i] = w
		localWorker := w
		require.NoError(t, topic.Subscribe(func(value interface{}, from peer.ID) {
			if b, ok := value.(*wrapperspb.BytesValue); ok {
				_ = localWorker.handleWireFrom(b.Value, from)
			}
		}))
	}

	// Only validator 1 has an independently verified persisted replica. The
	// other two start with zero leaves and no manual bootstrap/copy operation.
	provider := evm.ILNReplicaCheckpoint{
		ChainID: 42161, Hook: hook, Root: root, Count: 1,
		BlockNumber: heights["provider"],
	}
	require.NoError(t, evm.StoreVerifiedILNReplica(
		workers[0].ilnReplicaPath(provider),
		evm.ILNReplica{
			ChainID: provider.ChainID, Hook: hook, Root: root,
			CheckpointBlock: provider.BlockNumber, Leaves: []types.Hash{message},
		}, provider,
	))

	time.Sleep(1300 * time.Millisecond) // establish real GossipSub mesh
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ready := 0
		for i := 1; i < len(names); i++ {
			expected := provider
			expected.BlockNumber = heights[names[i]]
			if replica, err := evm.LoadVerifiedILNReplica(workers[i].ilnReplicaPath(expected), expected); err == nil {
				require.Equal(t, []types.Hash{message}, replica.Leaves)
				_, err = evm.BuildILNReplicaProof(replica, expected, 0)
				require.NoError(t, err)
				ready++
				continue
			}
			// A missing replica is expected until P2P catch-up completes;
			// any unexpected failure in source verification is not.
			if err := workers[i].refreshILNReplicaRoute(route); err != nil &&
				!strings.Contains(err.Error(), "waiting for independently verified Merkle leaves") {
				t.Fatalf("validator %d source/P2P failure: %v", i+1, err)
			}
		}
		if ready == len(names)-1 {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatal("both cold validators must independently assemble the canonical Merkle root across divergent head epochs")
}
