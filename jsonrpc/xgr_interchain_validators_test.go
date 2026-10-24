package jsonrpc

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	interchainEVM "github.com/xgr-network/xgr-node/interchain/evm"
	"github.com/xgr-network/xgr-node/types"
)

func TestXGRGetInterchainValidatorsFormatsCanonicalRegistryOrder(t *testing.T) {
	first := types.StringToAddress("0x1111111111111111111111111111111111111111")
	second := types.StringToAddress("0x2222222222222222222222222222222222222222")
	keyA, keyB := bytes.Repeat([]byte{0x11}, 48), bytes.Repeat([]byte{0x22}, 48)
	destination := &interchainEVM.Destination{
		Name: "base", ChainID: 8453, Domain: 8453,
		RegistryAddress: "0x9999999999999999999999999999999999999999",
	}
	got, err := formatInterchainValidators(destination, &interchainEVM.ValidatorSet{
		Validators: []types.Address{second, first}, BLSPublicKeys: [][]byte{keyB, keyA}, SetID: 7,
	})
	require.NoError(t, err)
	require.Equal(t, "base", got.Destination)
	require.Equal(t, uint64(8453), got.ChainID)
	require.Equal(t, uint32(8453), got.Domain)
	require.Equal(t, uint64(7), got.SetID)
	require.Equal(t, 2, got.Count)
	require.Equal(t, 2, got.QuorumThreshold)
	require.Equal(t, second.String(), got.Validators[0].Address)
	require.Equal(t, first.String(), got.Validators[1].Address)
	require.Equal(t, "0x"+hex.EncodeToString(keyA), got.Validators[1].BLSPublicKey)
}

func TestXGRGetInterchainValidatorsRejectsInvalidCanonicalSet(t *testing.T) {
	address := types.StringToAddress("0x1111111111111111111111111111111111111111")
	dest := &interchainEVM.Destination{Name: "base", ChainID: 8453, Domain: 8453}
	for _, tc := range []struct {
		name string
		set  *interchainEVM.ValidatorSet
	}{
		{"nil", nil},
		{"empty", &interchainEVM.ValidatorSet{SetID: 1}},
		{"zero set id", &interchainEVM.ValidatorSet{SetID: 0, Validators: []types.Address{address}, BLSPublicKeys: [][]byte{bytes.Repeat([]byte{0x11}, 48)}}},
		{"mismatched arrays", &interchainEVM.ValidatorSet{SetID: 1, Validators: []types.Address{address}}},
		{"zero address", &interchainEVM.ValidatorSet{SetID: 1, Validators: []types.Address{types.ZeroAddress}, BLSPublicKeys: [][]byte{bytes.Repeat([]byte{0x11}, 48)}}},
		{"wrong key size", &interchainEVM.ValidatorSet{SetID: 1, Validators: []types.Address{address}, BLSPublicKeys: [][]byte{{0x01}}}},
		{"duplicate address", &interchainEVM.ValidatorSet{SetID: 1, Validators: []types.Address{address, address}, BLSPublicKeys: [][]byte{bytes.Repeat([]byte{0x11}, 48), bytes.Repeat([]byte{0x22}, 48)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := formatInterchainValidators(dest, tc.set)
			require.Error(t, err)
		})
	}
}

func TestXGRGetInterchainValidatorsRequiresConfiguredDestination(t *testing.T) {
	// No user-supplied destination URL/address is ever passed through.
	ep := newXGREndpoint(nil, t.TempDir())
	_, err := ep.GetInterchainValidators("")
	require.ErrorContains(t, err, "name is required")
	_, err = ep.GetInterchainValidators("__unknown_interchain_destination__")
	require.ErrorContains(t, err, "not configured")
}

func TestXGRGetInterchainValidatorsCannotBeOverriddenByEnv(t *testing.T) {
 t.Setenv("XGR_INTERCHAIN_BASE_CHAIN_ID","1")
 t.Setenv("XGR_INTERCHAIN_BASE_DOMAIN","1")
 t.Setenv("XGR_INTERCHAIN_BASE_REGISTRY_ADDR","0x9999999999999999999999999999999999999999")
 t.Setenv("XGR_INTERCHAIN_BASE_RPC","http://127.0.0.1:9999")
 cfg,err:=interchainEVM.Load("base")
 require.NoError(t,err)
 require.Equal(t,uint64(8453),cfg.ChainID)
 require.Equal(t,uint32(8453),cfg.Domain)
 require.Equal(t,"0x3dc4cc1912792749d320789a67c2c14b7ee4ee33",cfg.RegistryAddress)
 require.Equal(t,"https://mainnet.base.org",cfg.RPCURL)
 ep:=newXGREndpoint(nil,t.TempDir())
 _,err=ep.GetInterchainValidators("__unknown_interchain_destination__")
 require.ErrorContains(t,err,"not configured")
}
