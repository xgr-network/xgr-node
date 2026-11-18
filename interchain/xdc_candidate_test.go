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
 require.Equal(t,"0x70f5752326735b31641f21d174ba035e904db93c",p.Registry)
 require.Equal(t,"0x14d5d33a65a39f96665ed631b04fecf357bcb576",p.SourceRegistry)
 require.Equal(t,uint64(109507718),p.SourceActivationBlock)
 require.Equal(t,FinalityFinalized,p.Finality)
 require.Len(t,p.RPCURLs,3)
 require.Len(t,p.IndependentOperatorIDs,3)
 require.Error(t,validatePolicy(p))
 _,err:=Policy("xdc")
 require.Error(t,err)
}
func TestXDCDeploymentReceiptsPinnedAndNotActivated(t *testing.T) {
 contracts:=XDCMainnetContracts()
 require.Len(t,contracts,13)
 require.Equal(t,"0x202c10bdecf3b796ea4b4025c81952c4f2dd9f93",contracts["ecdsaVerifier"].Address)
 require.Equal(t,"0x70f5752326735b31641f21d174ba035e904db93c",contracts["validatorRegistry"].Address)
 require.Equal(t,"0x3d2add3a7dacb82c11338b6731b22d2afed4e1cc",contracts["ism"].Address)
 require.Equal(t,"0x14d5d33a65a39f96665ed631b04fecf357bcb576",contracts["sourceRegistry"].Address)
 require.Equal(t,XDCMainnetDeploymentSourceCommit,"010e16c33a726d893ac8cdd8dc3d28d8caa538cb")
 for name,entry:=range contracts {
  require.NotEmpty(t,name)
  require.Len(t,entry.Address,42)
  require.Len(t,entry.RuntimeCodeKeccak256,66)
  require.Len(t,entry.TransactionHash,66)
  require.Contains(t,entry.ReceiptPath,"deployments/receipts/xdc/")
 }
 require.Equal(t,contracts["dispatchHook"],contracts["requiredHook"])
 contracts["ism"]=XDCDeploymentBinding{}
 require.NotEmpty(t,XDCMainnetContracts()["ism"].Address)
 active,err:=ActivePolicies()
 require.NoError(t,err)
 require.Len(t,active,5)
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
