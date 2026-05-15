package genesis

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xgr-network/xgr-node/command/helper"
	"github.com/xgr-network/xgr-node/types"
)

func Test_parsePremineInfo(t *testing.T) {
	t.Parallel()

	p := &genesisParams{
		premine: []string{types.StringToAddress("1").String() + ":1000"},
	}

	err := p.parsePremineInfo()
	require.NoError(t, err)
	require.Len(t, p.premineInfos, 1)
	require.Equal(t, &helper.PremineInfo{
		Address: types.StringToAddress("1"),
		Amount:  big.NewInt(1000),
	}, p.premineInfos[0])
}
