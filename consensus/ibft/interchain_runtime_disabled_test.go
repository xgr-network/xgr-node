package ibft

import (
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/consensus"
)

func TestInterchainRuntimeDisabledBeforeWorkerConstruction(t *testing.T) {
	backend := &backendIBFT{logger: hclog.NewNullLogger(), config: &consensus.Config{Config: map[string]interface{}{"interchainRuntimeDisabled": true}}}
	// No executor, Params, network, keys or chain RPCs are available. Returning
	// cleanly proves the explicit disable path precedes worker construction.
	backend.startInterchainRuntime()
	require.Nil(t, backend.interchainClose)
}
