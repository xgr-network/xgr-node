package ecdsa

import (
 "bytes"
 "crypto/ecdsa"
 "math/big"
 "testing"

 protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
 "github.com/stretchr/testify/require"
)

func exampleMessage() protocol.XITADirectMessage {
 return protocol.XITADirectMessage{
  SourceChainID:1643,SourceDomain:1643,DestinationDomain:50,
  RouteID:types.StringToHash("0x1234"),SetID:7,SourceBlockNumber:555,
  SourceRegistry:types.StringToAddress("0x1111"),Gateway:types.StringToAddress("0x3333"),
  SourceRouter:types.StringToAddress("0x2222"),SourceMailbox:types.StringToAddress("0x4444"),
  DestinationRouter:types.StringToAddress("0x5555"),
  ValidatorFeeWei:big1000(),MessageID:types.StringToHash("0x7777"),
  DestinationISM:types.StringToAddress("0x8888"),DestinationRegistry:types.StringToAddress("0x9999"),
 }
}

func big1000() *big.Int {return big.NewInt(1000)}

func TestECDSADomainSeparationAnd2Of3(t *testing.T) {
 keys:=make([]*ecdsa.PrivateKey,3)
 members:=make([]types.Address,3)
 for i:=range keys{
  k,err:=crypto.GenerateECDSAKey()
  require.NoError(t,err)
  keys[i]=k
  members[i]=crypto.PubKeyToAddress(&k.PublicKey)
 }
 message:=exampleMessage()
 raw,err:=EncodeDirect(message)
 require.NoError(t,err)
 require.Equal(t,len(Domain)+268,len(raw))
 require.NotEqual(t,[]byte(protocol.XITADirectMessageDomain),raw[:len(protocol.XITADirectMessageDomain)])
 digest,err:=Digest(raw)
 require.NoError(t,err)
 signed:=make([]byte,0,130)
 for _,key:=range keys[:2] {
  s,err:=crypto.Sign(key,digest.Bytes())
  require.NoError(t,err)
  s[64]+=27
  signed=append(signed,s...)
 }
 require.True(t,VerifyQuorum(raw,members,[]byte{3},signed))
 require.False(t,VerifyQuorum(raw,members,[]byte{1},signed[:65]))
 require.False(t,VerifyQuorum(raw,members,[]byte{0x83},signed))
 require.False(t,VerifyQuorum(raw,members,[]byte{3},signed[:65]))
 tampered:=append([]byte(nil),raw...)
 tampered[len(tampered)-1]^=1
 require.False(t,VerifyQuorum(tampered,members,[]byte{3},signed))
 prefix:=append([]byte(nil),raw...)
 copy(prefix[:len(Domain)],bytes.Repeat([]byte{'Z'},len(Domain)))
 require.False(t,VerifyQuorum(prefix,members,[]byte{3},signed))
 invalid:=append([]byte(nil),signed...)
 invalid[64]=0
 require.False(t,VerifyQuorum(raw,members,[]byte{3},invalid))
 message.SetID++
 rotated,err:=EncodeDirect(message)
 require.NoError(t,err)
 require.False(t,VerifyQuorum(rotated,members,[]byte{3},signed))
}
