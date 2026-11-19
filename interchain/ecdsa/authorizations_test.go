package ecdsa

import (
 "math/big"
 "testing"

 "github.com/stretchr/testify/require"
 protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

func TestV5DirectECDSASignerOrderedQuorumAndTamperProtection(t *testing.T) {
 k1,err:=crypto.GenerateECDSAKey()
 require.NoError(t,err)
 k2,err:=crypto.GenerateECDSAKey()
 require.NoError(t,err)
 k3,err:=crypto.GenerateECDSAKey()
 require.NoError(t,err)
 members:=[]types.Address{
  crypto.PubKeyToAddress(&k1.PublicKey),
  crypto.PubKeyToAddress(&k2.PublicKey),
  crypto.PubKeyToAddress(&k3.PublicKey),
 }
 m:=exampleMessage()
 m.DestinationDomain=50
 raw1,sig1,err:=SignDirectV5(k1,m)
 require.NoError(t,err)
 raw2,sig2,err:=SignDirectV5(k2,m)
 require.NoError(t,err)
 require.Equal(t,raw1,raw2)
 bitmap,packed,err:=AssembleDirectQuorumV5(m,members,[]VoteV5{
  {ValidatorIndex:1,Signature:sig2},{ValidatorIndex:0,Signature:sig1},
 })
 require.NoError(t,err)
 require.Equal(t,[]byte{3},bitmap)
 require.Equal(t,append(append([]byte(nil),sig1...),sig2...),packed)
 require.True(t,VerifyDirectQuorumV5(m,members,bitmap,packed))
 altered:=m
 altered.SetID++
 require.False(t,VerifyDirectQuorumV5(altered,members,bitmap,packed))
 altered=m
 altered.DestinationRegistry=members[2]
 require.False(t,VerifyDirectQuorumV5(altered,members,bitmap,packed))
 altered=m
 altered.MessageID=types.StringToHash("0x55aa")
 require.False(t,VerifyDirectQuorumV5(altered,members,bitmap,packed))
 require.False(t,VerifyDirectQuorumV5(m,members,[]byte{0x83},packed))
 _,_,err=AssembleDirectQuorumV5(m,members,[]VoteV5{{ValidatorIndex:0,Signature:sig2},{ValidatorIndex:1,Signature:sig1}})
 require.Error(t,err)
 _,_,err=SignDirectV5(nil,m)
 require.Error(t,err)
}

func TestV5SourceFeeECDSASigningAndNonceProtection(t *testing.T) {
 k1,err:=crypto.GenerateECDSAKey()
 require.NoError(t,err)
 k2,err:=crypto.GenerateECDSAKey()
 require.NoError(t,err)
 k3,err:=crypto.GenerateECDSAKey()
 require.NoError(t,err)
 members:=[]types.Address{
  crypto.PubKeyToAddress(&k1.PublicKey),
  crypto.PubKeyToAddress(&k2.PublicKey),
  crypto.PubKeyToAddress(&k3.PublicKey),
 }
 p:=protocol.ILNSourceFeeProposal{
  SourceChainID:50,SourceDomain:50,
  Registry:types.StringToAddress("0x0000000000000000000000000000000000000bbb"),
  SetID:1,Nonce:1,ValidUntil:1790000000,ValidatorFeeWei:big.NewInt(100000000000000),
 }
 raw1,sig1,err:=SignSourceFeeV5(k1,p)
 require.NoError(t,err)
 raw2,sig2,err:=SignSourceFeeV5(k2,p)
 require.NoError(t,err)
 require.Equal(t,raw1,raw2)
 bitmap,packed,err:=AssembleSourceFeeQuorumV5(p,members,[]VoteV5{{ValidatorIndex:1,Signature:sig2},{ValidatorIndex:0,Signature:sig1}})
 require.NoError(t,err)
 require.True(t,VerifySourceFeeQuorumV5(p,members,bitmap,packed))
 newNonce:=p;newNonce.Nonce++
 require.False(t,VerifySourceFeeQuorumV5(newNonce,members,bitmap,packed))
 otherRegistry:=p;otherRegistry.Registry=members[2]
 require.False(t,VerifySourceFeeQuorumV5(otherRegistry,members,bitmap,packed))
 increasedFee:=p;increasedFee.ValidatorFeeWei=big.NewInt(200000000000000)
 require.False(t,VerifySourceFeeQuorumV5(increasedFee,members,bitmap,packed))
 p.ValidatorFeeWei=big.NewInt(0)
 _,_,err=SignSourceFeeV5(k1,p)
 require.Error(t,err)
 _,_,err=AssembleSourceFeeQuorumV5(p,members,nil)
 require.Error(t,err)
}
