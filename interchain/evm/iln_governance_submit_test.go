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

func TestILNSourceFeeABIEncodesV315(t *testing.T) {
 proposal:=protocol.ILNSourceFeeProposal{
  SourceChainID:8453, SourceDomain:8453,
  Registry:types.StringToAddress("0x5555555555555555555555555555555555555555"),
  SetID:2,Nonce:1,ValidUntil:1800000000,
  ValidatorFeeWei:big.NewInt(12345),
 }
 raw,err:=proposal.MarshalBinary()
 require.NoError(t,err)
 key,err:=crypto.GenerateBLSKey()
 require.NoError(t,err)
 signature,err:=crypto.SignByBLS(key,raw)
 require.NoError(t,err)
 method:=ilnApplyGovernanceABI.Methods["applySourceFee"]
 require.NotNil(t,method)
 for _,format:=range []string{VerifierFormatEIP2537,VerifierFormatCompressed} {
  calldata,err:=EncodeILNGovernanceCalldata(proposal,big.NewInt(1),signature,format)
  require.NoError(t,err)
  require.True(t,bytes.Equal(method.ID(),calldata[:4]))
  decoded,err:=method.Inputs.Decode(calldata[4:])
  require.NoError(t,err)
  require.NotNil(t,decoded)
 }
}

func TestILNSourceFeeABIRejectsMissingQuorum(t *testing.T) {
 _,err:=EncodeILNGovernanceCalldata(protocol.ILNSourceFeeProposal{},big.NewInt(0),nil,VerifierFormatEIP2537)
 require.Error(t,err)
}
