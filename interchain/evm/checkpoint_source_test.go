package evm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadCheckpointRoutesHasNoImplicitFallback(t *testing.T) {
	networks := []*Destination{
		{Name: "base", ChainID: 8453, Domain: 8453, ILNRegistryAddress: "0x3333333333333333333333333333333333333333"},
		{Name: "xgr", ChainID: 1643, Domain: 1643, ILNRegistryAddress: "0x4444444444444444444444444444444444444444"},
	}
	routes, err := LoadCheckpointRoutes(networks)
	require.NoError(t, err)
	require.Empty(t, routes)
}

func TestLoadCheckpointRoutesILN(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_NETWORK", "base")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_DESTINATION", "xgr")

	networks := []*Destination{
		{Name: "base", ChainID: 8453, Domain: 8453, ILNRegistryAddress: "0x3333333333333333333333333333333333333333"},
		{Name: "xgr", ChainID: 1643, Domain: 1643, ILNRegistryAddress: "0x4444444444444444444444444444444444444444"},
	}
	routes, err := LoadCheckpointRoutes(networks)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Equal(t, "base_to_xgr", routes[0].Name)
	require.Equal(t, "base", routes[0].SourceNetwork)
	require.Equal(t, "xgr", routes[0].Destination)
}

func TestLoadCheckpointRoutesAllowsMultipleSources(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_NETWORK", "base")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_DESTINATION", "xgr")
	t.Setenv("XGR_INTERCHAIN_ROUTE_XDC_TO_XGR_SOURCE_NETWORK", "xdc")
	t.Setenv("XGR_INTERCHAIN_ROUTE_XDC_TO_XGR_DESTINATION", "xgr")

	networks := []*Destination{
		{Name: "base", ChainID: 8453, Domain: 8453, ILNRegistryAddress: "0x3333333333333333333333333333333333333333"},
		{Name: "xdc", ChainID: 50, Domain: 50, ILNRegistryAddress: "0x5555555555555555555555555555555555555555"},
		{Name: "xgr", ChainID: 1643, Domain: 1643, ILNRegistryAddress: "0x4444444444444444444444444444444444444444"},
	}
	routes, err := LoadCheckpointRoutes(networks)
	require.NoError(t, err)
	require.Len(t, routes, 2)
	require.Equal(t, "xgr", routes[0].Destination)
	require.Equal(t, "xgr", routes[1].Destination)
	require.NotEqual(t, routes[0].SourceNetwork, routes[1].SourceNetwork)
}

func TestLoadCheckpointRoutesRejectsMissingSourceNetwork(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_DESTINATION", "xgr")
	networks := []*Destination{
		{Name: "base", ChainID: 8453, Domain: 8453, ILNRegistryAddress: "0x3333333333333333333333333333333333333333"},
		{Name: "xgr", ChainID: 1643, Domain: 1643, ILNRegistryAddress: "0x4444444444444444444444444444444444444444"},
	}
	_, err := LoadCheckpointRoutes(networks)
	require.Error(t, err)
	require.Contains(t, err.Error(), "SOURCE_NETWORK")
}

func TestLoadCheckpointRoutesRejectsSourceWithoutILNRegistry(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_NETWORK", "base")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_DESTINATION", "xgr")
	networks := []*Destination{
		{Name: "base", ChainID: 8453, Domain: 8453},
		{Name: "xgr", ChainID: 1643, Domain: 1643, ILNRegistryAddress: "0x4444444444444444444444444444444444444444"},
	}
	_, err := LoadCheckpointRoutes(networks)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no ILN_REGISTRY_ADDR")
}

func TestLoadCheckpointRoutesRejectsLegacySourceFields(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_NETWORK", "base")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_DESTINATION", "xgr")
	t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_SOURCE_TYPE", "evm")
	networks := []*Destination{
		{Name: "base", ChainID: 8453, Domain: 8453, ILNRegistryAddress: "0x3333333333333333333333333333333333333333"},
		{Name: "xgr", ChainID: 1643, Domain: 1643, ILNRegistryAddress: "0x4444444444444444444444444444444444444444"},
	}
	_, err := LoadCheckpointRoutes(networks)
	require.Error(t, err)
	require.Contains(t, err.Error(), "legacy v3.1.1")
}
