package interchain

import (
 "testing"
 "github.com/stretchr/testify/require"
)

func TestV343OptimismReceiptPinnedAndSafeOnly(t *testing.T) {
 p,err:=Policy("optimism")
 require.NoError(t,err)
 require.Equal(t,uint64(10),p.ChainID)
 require.Equal(t,uint32(10),p.Domain)
 require.Equal(t,FinalitySafe,p.Finality)
 require.Equal(t,"eip2537",p.VerifierFormat)
 require.Equal(t,"0x70f5752326735b31641f21d174ba035e904db93c",p.Registry)
 require.Equal(t,"0x14d5d33a65a39f96665ed631b04fecf357bcb576",p.SourceRegistry)
 require.Equal(t,uint64(158929283),p.SourceActivationBlock)
 require.Len(t,p.RPCURLs,4)
 require.Len(t,p.IndependentOperatorIDs,4)
 operators:=make(map[string]bool)
 for _,operator:=range p.IndependentOperatorIDs { operators[operator]=true }
 require.Len(t,operators,4)
 bsc,err:=Policy("bsc")
 require.NoError(t,err)
 // Both EVM chains may independently deploy the same deterministic address.
 require.Equal(t,bsc.Registry,p.Registry)
 require.Equal(t,bsc.SourceRegistry,p.SourceRegistry)
 require.NotEqual(t,bsc.ChainID,p.ChainID)
 require.NotEqual(t,bsc.SourceActivationBlock,p.SourceActivationBlock)
 byID,err:=PolicyForChainID(10)
 require.NoError(t,err)
 require.Equal(t,p,byID)
 active,err:=ActivePolicies()
 require.NoError(t,err)
 require.Len(t,active,5)
 require.Equal(t,"optimism",active[4].Name)
}

func TestV343OptimismFailClosedOnUnreviewedIdentityFinalityOrRegistry(t *testing.T) {
 p,err:=Policy("optimism")
 require.NoError(t,err)
 p.Finality=FinalityFinalized
 require.ErrorContains(t,validatePolicy(p),"finality")
 p,err=Policy("optimism")
 require.NoError(t,err)
 p.VerifierFormat="compressed"
 require.ErrorContains(t,validatePolicy(p),"finality")
 p,err=Policy("optimism")
 require.NoError(t,err)
 p.Registry=ZeroRegistryAddress
 require.ErrorContains(t,validatePolicy(p),"placeholder")
 p,err=Policy("optimism")
 require.NoError(t,err)
 p.SourceRegistry=ZeroRegistryAddress
 require.ErrorContains(t,validatePolicy(p),"placeholder")
 p,err=Policy("optimism")
 require.NoError(t,err)
 p.SourceActivationBlock=0
 require.ErrorContains(t,validatePolicy(p),"height")
 p,err=Policy("optimism")
 require.NoError(t,err)
 p.ChainID=56
 require.ErrorContains(t,validatePolicy(p),"finality")
 p,err=Policy("optimism")
 require.NoError(t,err)
 p.RPCURLs=append([]string(nil),p.RPCURLs...)
 p.RPCURLs[0]="http://untrusted.example.invalid"
 require.ErrorContains(t,validatePolicy(p),"RPC")
 p,err=Policy("optimism")
 require.NoError(t,err)
 p.IndependentOperatorIDs=[]string{"one","one","one","one"}
 require.ErrorContains(t,validatePolicy(p),"independent")
 _,err=Policy("polygon")
 require.Error(t,err)
}
