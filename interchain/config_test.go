package interchain
import (
 "testing"
 "github.com/stretchr/testify/require"
)
func TestV332HasNoSignerBindings(t *testing.T){
 for _,tc:=range []struct{name string;id uint64;rule FinalityRule;format string}{
  {"xgr",1643,FinalityIBFT,"compressed"},{"base",8453,FinalitySafe,"eip2537"},
  {"arbitrum",42161,FinalitySafe,"eip2537"},
 } {
  p,err:=Policy(tc.name)
  require.NoError(t,err)
  require.Equal(t,tc.id,p.ChainID)
  require.Equal(t,uint32(tc.id),p.Domain)
  require.Equal(t,tc.rule,p.Finality)
  require.Equal(t,tc.format,p.VerifierFormat)
  require.Empty(t,p.Registry)
  require.Empty(t,p.SourceRegistry)
 }
 active,err:=ActivePolicies()
 require.NoError(t,err)
 require.Empty(t,active)
 _,err=Policy("polygon")
 require.Error(t,err)
}
func TestInvalidChainPoliciesFailClosed(t *testing.T){
 p,_:=Policy("base")
 p.Finality=FinalityIBFT
 require.Error(t,validatePolicy(p))
 p,_=Policy("base")
 p.Registry="0x1111111111111111111111111111111111111111"
 require.Error(t,validatePolicy(p))
 p.SourceRegistry="0x2222222222222222222222222222222222222222"
 require.Error(t,validatePolicy(p))
 p.SourceActivationBlock=1234
 require.NoError(t,validatePolicy(p))
}
