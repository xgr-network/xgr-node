package ibft

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConsensusWrapper_DoesNotUseReflectOrUnsafe(t *testing.T) {
	data, err := os.ReadFile("consensus.go")
	require.NoError(t, err)

	source := string(data)
	require.NotContains(t, source, "\"reflect\"")
	require.NotContains(t, source, "\"unsafe\"")
	require.False(t, strings.Contains(source, "Unsafe"))
}
