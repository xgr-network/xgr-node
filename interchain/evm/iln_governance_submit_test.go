package evm

import (
 "math/big"
 "testing"
 "bytes"

 "github.com/stretchr/testify/require"
 protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

func TestILNGovernanceSourceABIEncodesRouteAdd(t *testing.T) {
 proposal:=protocol.ILNGovernanceProposal{
  Type:protocol.ILNProposalRouteAdd,
  Registry:types.StringToAddress("0x5555555555555555555555555555555555555555"),
  SetID:2,Nonce:1,ValidUntil:1800000000,
  Route:protocol.ILNRoute{
   Key:protocol.ILNRouteKey{SourceChainID:8453,SourceDomain:8453,DestinationDomain:1643,RouteID:types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")},
   Gateway:types.StringToAddress("0x1111111111111111111111111111111111111111"),
   SourceRouter:types.StringToAddress("0x2222222222222222222222222222222222222222"),
   Mailbox:types.StringToAddress("0x3333333333333333333333333333333333333333"),
   MerkleTreeHook:types.StringToAddress("0x4444444444444444444444444444444444444444"),
   DestinationRouter:types.StringToAddress("0x6666666666666666666666666666666666666666"),
   ValidatorFeeWei:big.NewInt(12345),Enabled:true,
  },
 }
 raw,err:=proposal.MarshalBinary()
 require.NoError(t,err)
 key,err:=crypto.GenerateBLSKey()
 require.NoError(t,err)
 signature,err:=crypto.SignByBLS(key,raw)
 require.NoError(t,err)
 method:=ilnApplyGovernanceABI.Methods["applyGovernance"]
 require.NotNil(t,method)
 for _,format:=range []string{VerifierFormatEIP2537,VerifierFormatCompressed} {
  calldata,err:=EncodeILNGovernanceCalldata(proposal,big.NewInt(1),signature,format)
  require.NoError(t,err)
  require.True(t,bytes.Equal(method.ID(),calldata[:4]))
  require.Greater(t,len(calldata),4)
  decoded,err:=method.Inputs.Decode(calldata[4:])
  require.NoError(t,err)
  require.NotNil(t,decoded)
 }
}

func TestILNGovernanceSourceABIRejectsMissingQuorum(t *testing.T) {
 _,err:=EncodeILNGovernanceCalldata(protocol.ILNGovernanceProposal{},big.NewInt(0),nil,VerifierFormatEIP2537)
 require.Error(t,err)
}
