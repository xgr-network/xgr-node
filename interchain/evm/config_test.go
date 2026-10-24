package evm
import (
 "testing"
 "github.com/stretchr/testify/require"
)
func TestV332CompiledDestinationsHaveReceiptBindings(t *testing.T){
 cfg,err:=Load("base")
 require.NoError(t,err)
 require.Equal(t,uint64(8453),cfg.ChainID)
 require.Equal(t,uint32(8453),cfg.Domain)
 require.Equal(t,"0x3dc4cc1912792749d320789a67c2c14b7ee4ee33",cfg.RegistryAddress)
 require.Equal(t,"0xf243093350b1b4ef8a9e3b0c13be3495297461b0",cfg.ILNRegistryAddress)
 require.Equal(t,uint64(53026521),cfg.ILNRegistryActivationBlock)
 require.NoError(t,cfg.ValidateSubmission())
 all,err:=LoadAll()
 require.NoError(t,err)
 require.Len(t,all,3)
 _,err=Load("polygon")
 require.Error(t,err)
}
func TestInterchainEnvironmentCannotOverrideProtocol(t *testing.T){
 t.Setenv("XGR_INTERCHAIN_BASE_CHAIN_ID","1")
 t.Setenv("XGR_INTERCHAIN_BASE_DOMAIN","1")
 t.Setenv("XGR_INTERCHAIN_BASE_CONFIRMATIONS","1")
 t.Setenv("XGR_INTERCHAIN_BASE_REGISTRY_ADDR","0x1111111111111111111111111111111111111111")
 t.Setenv("XGR_INTERCHAIN_BASE_RPC","http://bad.example.invalid")
 t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_DESTINATION","xgr")
 got,err:=Load("base")
 require.NoError(t,err)
 require.Equal(t,uint64(8453),got.ChainID)
 require.Equal(t,"0x3dc4cc1912792749d320789a67c2c14b7ee4ee33",got.RegistryAddress)
 require.Equal(t,"https://mainnet.base.org",got.RPCURL)
 all,err:=LoadAll()
 require.NoError(t,err)
 require.Empty(t,all)
}
