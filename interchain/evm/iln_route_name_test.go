package evm

import (
    "strings"
    "testing"

    "github.com/stretchr/testify/require"
)

func TestParseILNRouteNameCanonical(t *testing.T) {
    id := strings.Repeat("f", 64)
    source, destination, routeID, err := ParseILNRouteName("iln_42161_1643_" + id)
    require.NoError(t, err)
    require.Equal(t, uint32(42161), source)
    require.Equal(t, uint32(1643), destination)
    require.Equal(t, "0x"+id, routeID.String())

    for _, input := range []string{
        "iln_042161_1643_" + id,
        "iln_42161_01643_" + id,
        "iln_0_1643_" + id,
        "iln_42161_0_" + id,
        "iln_4294967296_1643_" + id,
        "iln_42161_1643_" + strings.Repeat("0", 64),
        "iln_42161_1643_" + strings.Repeat("G", 64),
        "ILN_42161_1643_" + id,
        "iln_42161_1643_" + id + "/../../",
        "unverified_user_route",
    } {
        _, _, _, err := ParseILNRouteName(input)
        require.Error(t, err, input)
    }
}
