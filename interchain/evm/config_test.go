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
 require.Len(t,all,4)
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
 require.Len(t,all,4)
}

func TestV341BSCReceiptPinnedWithFinalizedOnly(t *testing.T) {
 d,err:=Load("bsc")
 require.NoError(t,err)
 require.Equal(t,uint64(56),d.ChainID)
 require.Equal(t,uint32(56),d.Domain)
 require.Equal(t,VerifierFormatEIP2537,d.VerifierFormat)
 require.Equal(t,"ethereum-finalized",string(d.FinalityRule))
 require.Equal(t,"0x70f5752326735b31641f21d174ba035e904db93c",d.RegistryAddress)
 require.Equal(t,"0x14d5d33a65a39f96665ed631b04fecf357bcb576",d.ILNRegistryAddress)
 require.Equal(t,uint64(130525341),d.ILNRegistryActivationBlock)
 require.NotEmpty(t,d.RPCURL)
 require.NoError(t,d.ValidateMembershipRead())
 require.NoError(t,d.ValidateSubmission())
 require.NoError(t,d.ValidateILNRead())
 active,err:=LoadAll()
 require.NoError(t,err)
 require.Len(t,active,4)
 require.Equal(t,uint64(56),active[3].ChainID)
}
