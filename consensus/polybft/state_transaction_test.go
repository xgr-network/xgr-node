package polybft

import (
	"encoding/hex"
	"math/big"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo/abi"
	"github.com/xgr-network/xgr-node/consensus/polybft/contractsapi"
	"github.com/xgr-network/xgr-node/types"
)

func TestStateTransaction_Signature(t *testing.T) {
	t.Parallel()

	cases := []struct {
		m   *abi.Method
		sig string
	}{
		{
			contractsapi.ValidatorSet.Abi.GetMethod("commitEpoch"),
			"0f50287c",
		},
	}
	for _, c := range cases {
		sig := hex.EncodeToString(c.m.ID())
		require.Equal(t, c.sig, sig)
	}
}

func TestStateTransaction_Encoding(t *testing.T) {
	t.Parallel()

	cases := []contractsapi.StateTransactionInput{
		&contractsapi.CommitEpochValidatorSetFn{
			ID: big.NewInt(1),
			Epoch: &contractsapi.Epoch{
				StartBlock: big.NewInt(1),
				EndBlock:   big.NewInt(10),
				EpochRoot:  types.Hash{},
			},
		},
	}

	for _, c := range cases {
		res, err := c.EncodeAbi()

		require.NoError(t, err)

		// use reflection to create another type and decode
		val := reflect.New(reflect.TypeOf(c).Elem()).Interface()
		obj, ok := val.(contractsapi.StateTransactionInput)
		assert.True(t, ok)

		err = obj.DecodeAbi(res)
		require.NoError(t, err)

		require.Equal(t, obj, c)
	}
}
