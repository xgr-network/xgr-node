package interchain

import (
 "testing"
 "github.com/stretchr/testify/require"
)

func TestExistingMainnetPoliciesRemainBLSOnly(t *testing.T) {
 active,err:=ActivePolicies()
 require.NoError(t,err)
 require.Len(t,active,5)
 for _,p:=range active {
  require.Equal(t,"bls",p.SignatureScheme)
  require.Contains(t,[]string{"compressed","eip2537"},p.VerifierFormat)
  require.NotEqual(t,uint64(50),p.ChainID)
 }
}
func TestXDCMainnetCandidateIsNotDeployedOrActive(t *testing.T) {
 p:=XDCMainnetCandidate()
 require.Equal(t,uint64(50),p.ChainID)
 require.Equal(t,uint32(50),p.Domain)
 require.Equal(t,"ecdsa",p.SignatureScheme)
 require.Equal(t,"ecdsa-secp256k1",p.VerifierFormat)
 require.Empty(t,p.Registry)
 require.Empty(t,p.SourceRegistry)
 require.Error(t,validatePolicy(p))
 _,err:=Policy("xdc")
 require.Error(t,err)
}
func TestBLSSchemeCannotAccidentallyBecomeECDSA(t *testing.T) {
 p,err:=Policy("base")
 require.NoError(t,err)
 p.SignatureScheme="ecdsa"
 require.Error(t,validatePolicy(p))
 p,err=Policy("xgr")
 require.NoError(t,err)
 p.SignatureScheme=""
 require.Error(t,validatePolicy(p))
}
