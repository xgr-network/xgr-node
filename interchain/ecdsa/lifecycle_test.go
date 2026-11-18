package ecdsa

import (
 "bytes"
 stdECDSA "crypto/ecdsa"
 "testing"
 "time"

 "github.com/stretchr/testify/require"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

// PoS validator identity and dedicated interchain ECDSA signer MUST be distinct.
// Test exact Solidity abi.encodePacked layout and proof ownership.
func TestV5SeparateValidatorAndSignerBootstrapProof(t *testing.T) {
 key,err:=crypto.GenerateECDSAKey()
 require.NoError(t,err)
 validator:=types.StringToAddress("0x0000000000000000000000000000000000000aaa")
 signer,raw,proof,err:=SignBootstrapV5(key,1643,50,validator)
 require.NoError(t,err)
 require.Equal(t,crypto.PubKeyToAddress(&key.PublicKey),signer)
 require.NotEqual(t,validator,signer)
 require.Len(t,proof,SignatureSize)
 require.True(t,verifyOne(raw,signer,proof))
 expected:=append([]byte(BootstrapDomainV2),
  []byte{0,0,0,0,0,0,6,107,0,0,0,50}...)
 expected=append(expected,validator.Bytes()...)
 expected=append(expected,signer.Bytes()...)
 require.Equal(t,expected,raw)
 require.Len(t,raw,len(BootstrapDomainV2)+8+4+20+20)
 _,_,_,err=SignBootstrapV5(nil,1643,50,validator)
 require.Error(t,err)
 _,_,_,err=SignBootstrapV5(key,1643,50,types.ZeroAddress)
 require.Error(t,err)
 otherValidator:=types.StringToAddress("0x0000000000000000000000000000000000000aab")
 otherRaw,err:=BootstrapPayloadV5(1643,50,otherValidator,signer)
 require.NoError(t,err)
 require.False(t,verifyOne(otherRaw,signer,proof))
}

func TestV5RegistryBoundPossessionProof(t *testing.T) {
 key,err:=crypto.GenerateECDSAKey()
 require.NoError(t,err)
 validator:=types.StringToAddress("0x0000000000000000000000000000000000000aaa")
 registry:=types.StringToAddress("0x0000000000000000000000000000000000000bbb")
 signer,payload,sig,err:=SignPossessionV5(key,1643,50,registry,validator)
 require.NoError(t,err)
 require.Equal(t,crypto.PubKeyToAddress(&key.PublicKey),signer)
 require.True(t,verifyOne(payload,signer,sig))
 require.Len(t,payload,len(PossessionDomainV2)+8+4+20+20+20)
 foreignRegistry:=types.StringToAddress("0x0000000000000000000000000000000000000bbc")
 other,err:=PossessionPayloadV5(1643,50,foreignRegistry,validator,signer)
 require.NoError(t,err)
 require.False(t,verifyOne(other,signer,sig))
 wrongSigner:=types.StringToAddress("0x0000000000000000000000000000000000000ccc")
 other,err=PossessionPayloadV5(1643,50,registry,validator,wrongSigner)
 require.NoError(t,err)
 require.False(t,verifyOne(other,signer,sig))
 _,err=PossessionPayloadV5(1643,50,types.ZeroAddress,validator,signer)
 require.Error(t,err)
}

func TestV5IndividualMembershipQuorumAndReplay(t *testing.T) {
 keys:=make([]*stdECDSA.PrivateKey,3)
 signers:=make([]types.Address,3)
 for i:=range keys {
  k,err:=crypto.GenerateECDSAKey()
  require.NoError(t,err)
  keys[i]=k
  signers[i]=crypto.PubKeyToAddress(&k.PublicKey)
 }
 now:=time.Unix(1700000000,0)
 m:=MembershipV5{
  OriginChainID:1643,DestinationDomain:50,
  Registry:types.StringToAddress("0x0000000000000000000000000000000000000bbb"),
  ExpectedSetID:1,ValidUntil:uint64(now.Unix()+300),
  Action:MemberAdd,
  Validator:types.StringToAddress("0x0000000000000000000000000000000000000aaa"),
  ECDSASigner:types.StringToAddress("0x0000000000000000000000000000000000000ccc"),
 }
 payload,sig0,err:=SignMembershipV5(keys[0],m,now)
 require.NoError(t,err)
 _,sig1,err:=SignMembershipV5(keys[1],m,now)
 require.NoError(t,err)
 require.Len(t,payload,len(MembershipDomainV2)+8+4+20+8+8+1+20+20)
 require.True(t,bytes.HasSuffix(payload,m.ECDSASigner.Bytes()))
 require.True(t,verifyOne(payload,signers[0],sig0))
 // Reverse vote order: final signatures must be canonical signer-index order.
 votes:=[]VoteV5{{ValidatorIndex:1,Signature:sig1},{ValidatorIndex:0,Signature:sig0}}
 bitmap,signatures,err:=AssembleMembershipQuorumV5(m,signers,votes)
 require.NoError(t,err)
 require.Equal(t,[]byte{0x03},bitmap)
 require.Equal(t,append(append([]byte{},sig0...),sig1...),signatures)
 require.True(t,VerifyMembershipQuorumV5(m,signers,bitmap,signatures))
 altered:=m
 altered.Registry=types.StringToAddress("0x0000000000000000000000000000000000000bbc")
 require.False(t,VerifyMembershipQuorumV5(altered,signers,bitmap,signatures))
 altered=m
 altered.ECDSASigner=signers[2]
 require.False(t,VerifyMembershipQuorumV5(altered,signers,bitmap,signatures))
 altered=m
 altered.Validator=signers[2]
 require.False(t,VerifyMembershipQuorumV5(altered,signers,bitmap,signatures))
 altered=m
 altered.Action=MemberRemove
 require.False(t,VerifyMembershipQuorumV5(altered,signers,bitmap,signatures))
 altered=m
 altered.ExpectedSetID++
 require.False(t,VerifyMembershipQuorumV5(altered,signers,bitmap,signatures))
 altered=m
 altered.DestinationDomain=8453
 require.False(t,VerifyMembershipQuorumV5(altered,signers,bitmap,signatures))
 altered=m
 altered.ValidUntil++
 require.False(t,VerifyMembershipQuorumV5(altered,signers,bitmap,signatures))
 _,_,err=AssembleMembershipQuorumV5(m,signers,[]VoteV5{{ValidatorIndex:0,Signature:sig0},{ValidatorIndex:0,Signature:sig0}})
 require.Error(t,err)
 _,_,err=AssembleMembershipQuorumV5(m,signers,[]VoteV5{{ValidatorIndex:0,Signature:sig1},{ValidatorIndex:1,Signature:sig0}})
 require.Error(t,err)
 _,_,err=AssembleMembershipQuorumV5(m,signers,[]VoteV5{{ValidatorIndex:-1,Signature:sig0},{ValidatorIndex:1,Signature:sig1}})
 require.Error(t,err)
 require.False(t,VerifyMembershipQuorumV5(m,signers,[]byte{0x83},signatures))
 require.False(t,VerifyMembershipQuorumV5(m,signers,[]byte{0x01},sig0))
 require.False(t,VerifyMembershipQuorumV5(m,signers,bitmap,signatures[:65]))
 require.False(t,VerifyMembershipQuorumV5(m,[]types.Address{signers[0],signers[0],signers[2]},bitmap,signatures))
 require.NoError(t,AssertV5PayloadSeparation(m))
}

func TestV5MembershipDeadlineAndInvalidIdentityFailClosed(t *testing.T) {
 key,err:=crypto.GenerateECDSAKey()
 require.NoError(t,err)
 now:=time.Unix(1700000000,0)
 m:=MembershipV5{
  OriginChainID:1643,DestinationDomain:50,
  Registry:types.StringToAddress("0x0000000000000000000000000000000000000bbb"),
  ExpectedSetID:7,ValidUntil:uint64(now.Unix()+300),Action:MemberRemove,
  Validator:types.StringToAddress("0x0000000000000000000000000000000000000aaa"),
  ECDSASigner:types.StringToAddress("0x0000000000000000000000000000000000000ccc"),
 }
 _,_,err=SignMembershipV5(key,m,now)
 require.NoError(t,err)
 _,_,err=SignMembershipV5(key,m,now.Add(301*time.Second))
 require.Error(t,err)
 m.ValidUntil=uint64(now.Unix()+601)
 _,_,err=SignMembershipV5(key,m,now)
 require.Error(t,err)
 m.ValidUntil=uint64(now.Unix()+300)
 m.ECDSASigner=types.ZeroAddress
 _,err=MembershipPayloadV5(m)
 require.Error(t,err)
 m.ECDSASigner=types.StringToAddress("0x0000000000000000000000000000000000000ccc")
 m.Action=3
 _,err=MembershipPayloadV5(m)
 require.Error(t,err)
}
