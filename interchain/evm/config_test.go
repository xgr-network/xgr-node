package evm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadDestination(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_BASE_CHAIN_ID", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_DOMAIN", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_REGISTRY_ADDR", "0x1000000000000000000000000000000000000001")
	t.Setenv("XGR_INTERCHAIN_BASE_RPC", "https://base.example.invalid")
	t.Setenv("XGR_INTERCHAIN_BASE_DEACTIVATION_RESERVE_WEI", "12345")

	cfg, err := Load("base")
	require.NoError(t, err)
	require.Equal(t, "base", cfg.Name)
	require.Equal(t, uint64(8453), cfg.ChainID)
	require.Equal(t, uint32(8453), cfg.Domain)
	require.Equal(t, "0x1000000000000000000000000000000000000001", cfg.RegistryAddress)
	require.Equal(t, "12345", cfg.DeactivationReserveWei.String())
	require.Equal(t, uint64(1), cfg.Confirmations)
	require.Equal(t, uint64(300), cfg.MembershipValiditySeconds)
	require.Equal(t, uint64(10), cfg.ExecutorStepDelaySeconds)
	require.NoError(t, cfg.ValidateMembershipRead())
	require.NoError(t, cfg.ValidateSubmission())
}

func TestLoadAllowsDestinationIdentityBeforeRegistryDeployment(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_BASE_CHAIN_ID", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_DOMAIN", "8453")

	cfg, err := Load("base")
	require.NoError(t, err)
	require.Equal(t, uint64(8453), cfg.ChainID)
	require.Equal(t, uint32(8453), cfg.Domain)
	require.Error(t, cfg.ValidateMembershipRead())
}

func TestLoadNormalizesDestinationName(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_BASE_SEPOLIA_CHAIN_ID", "84532")
	t.Setenv("XGR_INTERCHAIN_BASE_SEPOLIA_DOMAIN", "84532")

	cfg, err := Load("base-sepolia")
	require.NoError(t, err)
	require.Equal(t, uint64(84532), cfg.ChainID)
	require.Equal(t, uint32(84532), cfg.Domain)
}

func TestLoadRejectsMissingChainID(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_BASE_DOMAIN", "8453")

	_, err := Load("base")
	require.Error(t, err)
	require.Contains(t, err.Error(), "CHAIN_ID")
}

func TestLoadRejectsMissingDomain(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_BASE_CHAIN_ID", "8453")

	_, err := Load("base")
	require.Error(t, err)
	require.Contains(t, err.Error(), "DOMAIN")
}

func TestLoadRejectsInvalidAddress(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_BASE_CHAIN_ID", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_DOMAIN", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_REGISTRY_ADDR", "not-an-address")

	_, err := Load("base")
	require.Error(t, err)
}

func TestLoadRejectsInvalidName(t *testing.T) {
	_, err := Load("base/mainnet")
	require.Error(t, err)
}

func TestLoadAllDiscoversDestinationConfigs(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_BASE_CHAIN_ID", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_DOMAIN", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_REGISTRY_ADDR", "0x1000000000000000000000000000000000000001")
	t.Setenv("XGR_INTERCHAIN_BASE_RPC", "https://base.example.invalid")

	cfgs, err := LoadAll()
	require.NoError(t, err)

	var found *Destination
	for _, cfg := range cfgs {
		if cfg.Name == "base" {
			found = cfg
			break
		}
	}
	require.NotNil(t, found)
	require.Equal(t, uint64(8453), found.ChainID)
}


func TestLoadDestinationConfirmations(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_BASE_CHAIN_ID", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_DOMAIN", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_CONFIRMATIONS", "12")

	cfg, err := Load("base")
	require.NoError(t, err)
	require.Equal(t, uint64(12), cfg.Confirmations)
}


func TestLoadAllRejectsDuplicateDomains(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_BASE_CHAIN_ID", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_DOMAIN", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_REGISTRY_ADDR", "0x1000000000000000000000000000000000000001")
	t.Setenv("XGR_INTERCHAIN_BASE_RPC", "https://base.example.invalid")

	t.Setenv("XGR_INTERCHAIN_BASE_ALT_CHAIN_ID", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_ALT_DOMAIN", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_ALT_REGISTRY_ADDR", "0x2000000000000000000000000000000000000002")
	t.Setenv("XGR_INTERCHAIN_BASE_ALT_RPC", "https://base-alt.example.invalid")

	_, err := LoadAll()
	require.Error(t, err)
	require.Contains(t, err.Error(), "configured more than once")
}


func TestLoadDestinationTimingPolicy(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_BASE_CHAIN_ID", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_DOMAIN", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_MEMBERSHIP_VALIDITY_SECONDS", "540")
	t.Setenv("XGR_INTERCHAIN_BASE_EXECUTOR_STEP_DELAY_SECONDS", "45")

	cfg, err := Load("base")
	require.NoError(t, err)
	require.Equal(t, uint64(540), cfg.MembershipValiditySeconds)
	require.Equal(t, uint64(45), cfg.ExecutorStepDelaySeconds)
}

func TestLoadRejectsMembershipValidityAboveProtocolMaximum(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_BASE_CHAIN_ID", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_DOMAIN", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_MEMBERSHIP_VALIDITY_SECONDS", "601")

	_, err := Load("base")
	require.Error(t, err)
	require.Contains(t, err.Error(), "between 1 and 600")
}

func TestLoadRejectsExecutorDelayOutsideValidityWindow(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_BASE_CHAIN_ID", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_DOMAIN", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_MEMBERSHIP_VALIDITY_SECONDS", "60")
	t.Setenv("XGR_INTERCHAIN_BASE_EXECUTOR_STEP_DELAY_SECONDS", "60")

	_, err := Load("base")
	require.Error(t, err)
	require.Contains(t, err.Error(), "smaller than membership validity")
}


func TestLoadOriginContracts(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_ORIGIN_MAILBOX_ADDR", "0x1111111111111111111111111111111111111111")
	t.Setenv("XGR_INTERCHAIN_ORIGIN_MERKLE_TREE_HOOK_ADDR", "0x2222222222222222222222222222222222222222")

	cfg, err := LoadOriginContracts()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "0x1111111111111111111111111111111111111111", cfg.Mailbox.String())
	require.Equal(t, "0x2222222222222222222222222222222222222222", cfg.MerkleTreeHook.String())
}

func TestLoadOriginContractsRejectsPartialConfig(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_ORIGIN_MAILBOX_ADDR", "0x1111111111111111111111111111111111111111")

	_, err := LoadOriginContracts()
	require.Error(t, err)
	require.Contains(t, err.Error(), "both XGR_INTERCHAIN_ORIGIN")
}

func TestLoadOriginContractsRejectsZeroAddress(t *testing.T) {
	t.Setenv("XGR_INTERCHAIN_ORIGIN_MAILBOX_ADDR", "0x0000000000000000000000000000000000000000")
	t.Setenv("XGR_INTERCHAIN_ORIGIN_MERKLE_TREE_HOOK_ADDR", "0x2222222222222222222222222222222222222222")

	_, err := LoadOriginContracts()
	require.Error(t, err)
	require.Contains(t, err.Error(), "non-zero")
}
