package interchain

import (
 "testing"
 "github.com/stretchr/testify/require"
)

func TestOptimismV343PredeployMustRemainInactive(t *testing.T){
 p:=ProposedOptimismPolicy()
 require.Equal(t,uint64(10),p.ChainID)
 require.Equal(t,uint32(10),p.Domain)
 require.Equal(t,FinalitySafe,p.Finality)
 require.Equal(t,"eip2537",p.VerifierFormat)
 require.Len(t,p.RPCURLs,4)
 require.Len(t,p.IndependentOperatorIDs,4)
 require.Equal(t,ZeroRegistryAddress,p.Registry)
 require.Equal(t,ZeroRegistryAddress,p.SourceRegistry)
 require.Zero(t,p.SourceActivationBlock)
 require.ErrorContains(t,validatePolicy(p),"unsupported chain 10")
 _,err:=Policy("optimism")
 require.Error(t,err,"undocumented OP Mainnet MUST NOT enter the active policy")
 _,err=PolicyForChainID(10)
 require.Error(t,err)
 active,err:=ActivePolicies()
 require.NoError(t,err)
 require.Len(t,active,4,"the fifth chain must only activate after receipt-pinned v3.4.3")
 for _,entry:=range active{require.NotEqual(t,uint64(10),entry.ChainID)}
}
