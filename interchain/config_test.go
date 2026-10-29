package interchain

import (
 "testing"
 "github.com/stretchr/testify/require"
)

func TestV332HasReviewedSignerBindings(t *testing.T) {
 cases := []struct{
  name string
  id uint64
  rule FinalityRule
  format string
  registry string
  source string
  activation uint64
 }{
  {"xgr",1643,FinalityIBFT,"compressed","0x9b6b92ef1745611f402341965f7fc649cbe0da8c","0xea887a7f2f519f291732aaadf578baf3ff374df6",12053797},
  {"base",8453,FinalitySafe,"eip2537","0x3dc4cc1912792749d320789a67c2c14b7ee4ee33","0xf243093350b1b4ef8a9e3b0c13be3495297461b0",53026521},
  {"arbitrum",42161,FinalitySafe,"eip2537","0x1a6610cc7dd0d0f42e22c3608e025d891065814c","0x51e3b684ed61fc88083497e475825a6cecaf21f5",517836903},
 }
 for _,tc:=range cases {
  p,err:=Policy(tc.name)
  require.NoError(t,err)
  require.Equal(t,tc.id,p.ChainID)
  require.Equal(t,uint32(tc.id),p.Domain)
  require.Equal(t,tc.rule,p.Finality)
  require.Equal(t,tc.format,p.VerifierFormat)
  require.Equal(t,tc.registry,p.Registry)
  require.Equal(t,tc.source,p.SourceRegistry)
  require.Equal(t,tc.activation,p.SourceActivationBlock)
  if tc.id==1643 {require.Empty(t,p.RPCURLs);require.Empty(t,p.IndependentOperatorIDs)} else {require.NotEmpty(t,p.RPCURLs)}
 }
 active,err:=ActivePolicies()
 require.NoError(t,err)
 require.Len(t,active,3)
 _,err=Policy("polygon")
 require.Error(t,err)
}

func TestInvalidChainPoliciesFailClosed(t *testing.T){
 p,_:=Policy("base")
 p.Finality=FinalityIBFT
 require.Error(t,validatePolicy(p))
 p,_=Policy("base")
 p.Registry=""
 require.Error(t,validatePolicy(p))
 p,_=Policy("base")
 p.SourceRegistry=""
 require.Error(t,validatePolicy(p))
 p,_=Policy("base")
 p.SourceActivationBlock=0
 require.Error(t,validatePolicy(p))
 p,_=Policy("base")
 p.RPCURLs=[]string{"http://untrusted.example.invalid"}
 require.Error(t,validatePolicy(p))
 p,_=Policy("base")
 p.RPCURLs=[]string{"https://one.example.invalid","https://one.example.invalid"}
 require.Error(t,validatePolicy(p))
}

func TestXGRFirstPartyRPCsAreNotIndependentWitnesses(t *testing.T) {
 p,err:=Policy("xgr")
 require.NoError(t,err)
 require.Empty(t,p.RPCURLs)
 operators:=map[string]struct{}{}
 for i,endpoint:=range p.RPCURLs {
  require.NotContains(t,endpoint,"thirdweb")
  operators[p.IndependentOperatorIDs[i]]=struct{}{}
 }
 require.Empty(t,operators)
}

func TestV341BSCKnownWithExplicitZeroAddressPlaceholdersButInactive(t *testing.T) {
 p,err:=Policy("bsc")
 require.NoError(t,err)
 require.Equal(t,uint64(56),p.ChainID)
 require.Equal(t,uint32(56),p.Domain)
 require.Equal(t,FinalityFinalized,p.Finality)
 require.Equal(t,"eip2537",p.VerifierFormat)
 require.Equal(t,ZeroRegistryAddress,p.Registry)
 require.Equal(t,ZeroRegistryAddress,p.SourceRegistry)
 require.Zero(t,p.SourceActivationBlock)
 require.Len(t,p.RPCURLs,3)
 require.Len(t,p.IndependentOperatorIDs,3)
 operators:=map[string]bool{}
 for _,operator:=range p.IndependentOperatorIDs {operators[operator]=true}
 require.GreaterOrEqual(t,len(operators),2)
 active,err:=ActivePolicies()
 require.NoError(t,err)
 require.Len(t,active,3)
 for _,entry:=range active {require.NotEqual(t,uint64(56),entry.ChainID)}
}

func TestV341BSCBindingAndFinalityPolicyFailClosed(t *testing.T) {
 p,err:=Policy("bsc")
 require.NoError(t,err)
 p.Finality=FinalitySafe
 require.ErrorContains(t,validatePolicy(p),"finality")
 p,err=Policy("bsc")
 require.NoError(t,err)
 p.Registry="0x1111111111111111111111111111111111111111"
 require.ErrorContains(t,validatePolicy(p),"incomplete")
 p.SourceRegistry="0x2222222222222222222222222222222222222222"
 require.ErrorContains(t,validatePolicy(p),"height")
 p.SourceActivationBlock=100
 require.NoError(t,validatePolicy(p),"fully bound reviewed policy structure")
 p,err=Policy("bsc")
 require.NoError(t,err)
 p.SourceActivationBlock=100
 require.ErrorContains(t,validatePolicy(p),"unbound")
 p,err=Policy("bsc")
 require.NoError(t,err)
 p.RPCURLs=append([]string(nil),p.RPCURLs...)
 p.RPCURLs[0]="http://untrusted.example.invalid"
 require.Error(t,validatePolicy(p))
 base,err:=Policy("base")
 require.NoError(t,err)
 base.Registry=ZeroRegistryAddress
 base.SourceRegistry=ZeroRegistryAddress
 base.SourceActivationBlock=0
 require.ErrorContains(t,validatePolicy(base),"placeholder")
}
