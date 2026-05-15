package pos

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func TestDeriveLastProposer(t *testing.T) {
	v1 := validators.NewECDSAValidator(types.StringToAddress("0x1001"))
	v2 := validators.NewECDSAValidator(types.StringToAddress("0x1002"))
	v3 := validators.NewECDSAValidator(types.StringToAddress("0x1003"))
	v4 := validators.NewECDSAValidator(types.StringToAddress("0x1004"))
	set := validators.NewECDSAValidatorSet(v1, v2, v3, v4)

	t.Run("round zero derives previous validator", func(t *testing.T) {
		last := deriveLastProposer(set, 0, v3.Addr())
		require.Equal(t, v2.Addr(), last)
	})

	t.Run("non-zero round derives parent proposer", func(t *testing.T) {
		last := deriveLastProposer(set, 2, v1.Addr())
		require.Equal(t, v2.Addr(), last)

		expected := calcProposer(set, 2, last)
		require.Equal(t, v1.Addr(), expected)
	})

	t.Run("wrap-around works", func(t *testing.T) {
		last := deriveLastProposer(set, 1, v1.Addr())
		require.Equal(t, v3.Addr(), last)

		expected := calcProposer(set, 1, last)
		require.Equal(t, v1.Addr(), expected)
	})

	t.Run("unknown proposer returns zero", func(t *testing.T) {
		last := deriveLastProposer(set, 0, types.StringToAddress("0x9999"))
		require.Equal(t, types.ZeroAddress, last)
	})
}

