package ecdsa

import (
 stdECDSA "crypto/ecdsa"
 "testing"
 "time"

 "github.com/stretchr/testify/require"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

func TestV5BootstrapUsesExistingValidatorECDSAIdentity(t *testing.T) {
 key,err:=crypto.GenerateECDSAKey()
 require.NoError(t,err)
 validator:=crypto.PubKeyToAddress(&key.PublicKey)
 signer,raw,sig,err:=SignBootstrapV5(key,1643,50)
 require.NoError(t,err)
 require.Equal(t,validator,signer)
 require.True(t,verifyOne(raw,validator,sig))
 require.Len(t,raw,len(BootstrapDomainV2)+8+4+20)
 expected,err:=BootstrapPayloadV5(1643,50,validator)
 require.NoError(t,err)
 require.Equal(t,expected,raw)
 _,_,_,err=SignBootstrapV5(nil,1643,50)
 require.Error(t,err)
 _,err=BootstrapPayloadV5(1643,50,types.ZeroAddress)
 require.Error(t,err)
 other,err:=BootstrapPayloadV5(1643,51,validator)
 require.NoError(t,err)
 require.False(t,verifyOne(other,validator,sig))
}

func TestV5PossessionProofTiedToRegistryAndValidatorKey(t *testing.T) {
 key,err:=crypto.GenerateECDSAKey()
 require.NoError(t,err)
 validator:=crypto.PubKeyToAddress(&key.PublicKey)
 registry:=types.StringToAddress("0x0000000000000000000000000000000000000bbb")
 signer,raw,sig,err:=SignPossessionV5(key,1643,50,registry)
 require.NoError(t,err)
 require.Equal(t,validator,signer)
 require.True(t,verifyOne(raw,signer,sig))
 require.Len(t,raw,len(PossessionDomainV2)+8+4+20+20)
 other,err:=PossessionPayloadV5(1643,50,types.StringToAddress("0x0000000000000000000000000000000000000bbc"),validator)
 require.NoError(t,err)
 require.False(t,verifyOne(other,signer,sig))
}

func TestV5IndividualMembershipQuorum(t *testing.T) {
 keys:=make([]*stdECDSA.PrivateKey,3)
 validators:=make([]types.Address,3)
 for i:=range keys {
  k,err:=crypto.GenerateECDSAKey()
  require.NoError(t,err)
  keys[i]=k
  validators[i]=crypto.PubKeyToAddress(&k.PublicKey)
 }
 now:=time.Unix(1700000000,0)
 m:=MembershipV5{
  OriginChainID:1643,DestinationDomain:50,
  Registry:types.StringToAddress("0x0000000000000000000000000000000000000bbb"),
  ExpectedSetID:1,ValidUntil:uint64(now.Unix()+300),
  Action:MemberAdd,Validator:types.StringToAddress("0x0000000000000000000000000000000000000aaa"),
 }
 raw,sig0,err:=SignMembershipV5(keys[0],m,now)
 require.NoError(t,err)
 _,sig1,err:=SignMembershipV5(keys[1],m,now)
 require.NoError(t,err)
 require.Len(t,raw,len(MembershipDomainV2)+8+4+20+8+8+1+20)
 require.True(t,verifyOne(raw,validators[0],sig0))
 bitmap,packed,err:=AssembleMembershipQuorumV5(m,validators,
  []VoteV5{{ValidatorIndex:1,Signature:sig1},{ValidatorIndex:0,Signature:sig0}})
 require.NoError(t,err)
 require.Equal(t,[]byte{0x03},bitmap)
 require.True(t,VerifyMembershipQuorumV5(m,validators,bitmap,packed))
 modified:=m
 modified.Validator=validators[2]
 require.False(t,VerifyMembershipQuorumV5(modified,validators,bitmap,packed))
 modified=m;modified.Action=MemberRemove
 require.False(t,VerifyMembershipQuorumV5(modified,validators,bitmap,packed))
 modified=m;modified.Registry=validators[2]
 require.False(t,VerifyMembershipQuorumV5(modified,validators,bitmap,packed))
 modified=m;modified.ExpectedSetID++
 require.False(t,VerifyMembershipQuorumV5(modified,validators,bitmap,packed))
 _,_,err=AssembleMembershipQuorumV5(m,validators,
  []VoteV5{{ValidatorIndex:0,Signature:sig0},{ValidatorIndex:0,Signature:sig0}})
 require.Error(t,err)
 require.False(t,VerifyMembershipQuorumV5(m,validators,[]byte{0x83},packed))
 require.False(t,VerifyMembershipQuorumV5(m,validators,[]byte{0x01},sig0))
 _,_,err=SignMembershipV5(keys[0],m,now.Add(301*time.Second))
 require.Error(t,err)
 require.NoError(t,AssertV5PayloadSeparation(m))
}
