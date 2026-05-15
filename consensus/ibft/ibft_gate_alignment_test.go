package ibft

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/chain"
)

func Test_alignFeePoolSplitWithPoSFork(t *testing.T) {
	t.Run("sets FeePoolSplit to first PoS from", func(t *testing.T) {
		forks := chain.Forks{}
		params := &chain.Params{Forks: &forks}
		ibftConfig := map[string]interface{}{
			"types": []interface{}{
				map[string]interface{}{"type": "PoA", "from": 0.0, "to": 99.0},
				map[string]interface{}{"type": "PoS", "from": 100.0},
			},
		}

		require.NoError(t, alignFeePoolSplitWithPoSFork(params, ibftConfig))
		require.True(t, params.Forks.IsActive(chain.FeePoolSplit, 100))
		require.False(t, params.Forks.IsActive(chain.FeePoolSplit, 99))
	})

	t.Run("accepts explicit FeePoolSplit matching first PoS from", func(t *testing.T) {
		forks := chain.Forks{chain.FeePoolSplit: chain.NewFork(200)}
		params := &chain.Params{Forks: &forks}
		ibftConfig := map[string]interface{}{
			"types": []interface{}{
				map[string]interface{}{"type": "PoA", "from": 0.0, "to": 199.0},
				map[string]interface{}{"type": "PoS", "from": 200.0},
			},
		}

		require.NoError(t, alignFeePoolSplitWithPoSFork(params, ibftConfig))
		require.True(t, params.Forks.IsActive(chain.FeePoolSplit, 200))
		require.False(t, params.Forks.IsActive(chain.FeePoolSplit, 199))
	})

	t.Run("returns error when explicit FeePoolSplit does not match first PoS from", func(t *testing.T) {
		forks := chain.Forks{chain.FeePoolSplit: chain.NewFork(500)}
		params := &chain.Params{Forks: &forks}
		ibftConfig := map[string]interface{}{
			"types": []interface{}{
				map[string]interface{}{"type": "PoA", "from": 0.0, "to": 199.0},
				map[string]interface{}{"type": "PoS", "from": 200.0},
			},
		}

		require.EqualError(t, alignFeePoolSplitWithPoSFork(params, ibftConfig), "feePoolSplit fork must match first PoS fork: feePoolSplit=500 firstPoSFrom=200")
	})

	t.Run("does nothing when PoS is not configured", func(t *testing.T) {
		forks := chain.Forks{chain.FeePoolSplit: chain.NewFork(42)}
		params := &chain.Params{Forks: &forks}
		ibftConfig := map[string]interface{}{"type": "PoA"}

		require.NoError(t, alignFeePoolSplitWithPoSFork(params, ibftConfig))
		require.True(t, params.Forks.IsActive(chain.FeePoolSplit, 42))
		require.False(t, params.Forks.IsActive(chain.FeePoolSplit, 41))
	})

	t.Run("does nothing for legacy config without types", func(t *testing.T) {
		forks := chain.Forks{}
		params := &chain.Params{Forks: &forks}
		ibftConfig := map[string]interface{}{"type": "PoA"}

		require.NoError(t, alignFeePoolSplitWithPoSFork(params, ibftConfig))
		require.False(t, params.Forks.IsActive(chain.FeePoolSplit, 1))
	})
}
