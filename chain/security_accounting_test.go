package chain

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSecurityAccountingExplicitActivation(t *testing.T) {
	f := Forks{}
	require.False(t, f.At(^uint64(0)).SecurityAccounting)
	f.SetFork(SecurityAccounting, NewFork(100))
	require.False(t, f.At(99).SecurityAccounting)
	require.True(t, f.At(100).SecurityAccounting)
	require.True(t, f.At(101).SecurityAccounting)
	// Existing genesis generation must not activate an unapproved mainnet fork.
	require.False(t, AllForksEnabled.At(0).SecurityAccounting)
}
