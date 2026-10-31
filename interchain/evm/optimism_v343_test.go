package evm

import (
 "testing"
 "github.com/stretchr/testify/require"
)

func TestV343OptimismLoadAllPinsChain10AndSafeRule(t *testing.T) {
 d,err:=Load("optimism")
 require.NoError(t,err)
 require.Equal(t,uint64(10),d.ChainID)
 require.Equal(t,uint32(10),d.Domain)
 require.Equal(t,VerifierFormatEIP2537,d.VerifierFormat)
 require.Equal(t,"ethereum-safe",string(d.FinalityRule))
 require.Equal(t,"0x70f5752326735b31641f21d174ba035e904db93c",d.RegistryAddress)
 require.Equal(t,"0x14d5d33a65a39f96665ed631b04fecf357bcb576",d.ILNRegistryAddress)
 require.Equal(t,uint64(158929283),d.ILNRegistryActivationBlock)
 require.Len(t,d.RPCURLs,4)
 require.Len(t,d.IndependentOperatorIDs,4)
 require.NoError(t,d.ValidateMembershipRead())
 require.NoError(t,d.ValidateSubmission())
 require.NoError(t,d.ValidateILNRead())
 all,err:=LoadAll()
 require.NoError(t,err)
 require.Len(t,all,5)
 require.Equal(t,uint64(10),all[4].ChainID)
 _,err=Load("polygon")
 require.Error(t,err)
}
