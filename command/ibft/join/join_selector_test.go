package join

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnsureMethodSelector_Ok(t *testing.T) {
	err := ensureMethodSelector("stake", "stake()", methodSelector("stake()"))
	require.NoError(t, err)
}

func TestEnsureMethodSelector_Mismatch(t *testing.T) {
	err := ensureMethodSelector("stake", "stake()", methodSelector("stake(uint256)"))
	require.Error(t, err)
	require.ErrorContains(t, err, "selector mismatch")
}
