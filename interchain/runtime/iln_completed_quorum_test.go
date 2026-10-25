package runtime

import (
 "encoding/hex"
 "math/big"
 "os"
 "path/filepath"
 "testing"

 "github.com/stretchr/testify/require"
 protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
 "github.com/xgr-network/xgr-node/crypto"
 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

func TestILNCompletedQuorumVerificationRejectsForgedAndStale(t *testing.T) {
 keys:=make([][]byte,3)
 signatures:=make([]protocol.Vote,0,2)
 p:=protocol.XITADirectMessage{
  SourceChainID:8453,SourceDomain:8453,DestinationDomain:1643,
  RouteID:types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
  SetID:7,SourceBlockNumber:100,
  SourceRegistry:types.StringToAddress("0x1111111111111111111111111111111111111111"),
  Gateway:types.StringToAddress("0x2222222222222222222222222222222222222222"),
  SourceRouter:types.StringToAddress("0x3333333333333333333333333333333333333333"),
  SourceMailbox:types.StringToAddress("0x4444444444444444444444444444444444444444"),
  DestinationRouter:types.StringToAddress("0x5555555555555555555555555555555555555555"),
  ValidatorFeeWei:big.NewInt(1000),
  MessageID:types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
  DestinationISM:types.StringToAddress("0x6666666666666666666666666666666666666666"),
  DestinationRegistry:types.StringToAddress("0x7777777777777777777777777777777777777777"),
 }
 raw,err:=p.MarshalBinary();require.NoError(t,err)
 set:=&evm.ValidatorSet{SetID:7}
 for i:=0;i<3;i++{
  key,e:=crypto.GenerateBLSKey();require.NoError(t,e)
  pub,e:=crypto.BLSSecretKeyToPubkeyBytes(key);require.NoError(t,e)
  keys[i]=pub
  set.Validators=append(set.Validators,types.BytesToAddress([]byte{byte(i+1)}))
  if i<2 {
   sig,e:=crypto.SignByBLS(key,raw);require.NoError(t,e)
   signatures=append(signatures,protocol.Vote{ValidatorIndex:i,Signature:sig})
  }
 }
 set.BLSPublicKeys=keys
 bitmap,sig,err:=protocol.AggregateVotes(keys,signatures,raw)
 require.NoError(t,err)
 eip,err:=crypto.BLSSignatureToEIP2537(sig);require.NoError(t,err)
 source:=&evm.Destination{Name:"base",ChainID:8453,Domain:8453,
  ILNRegistryAddress:p.SourceRegistry.String()}
 dest:=&evm.Destination{Name:"xgr",ChainID:1643,Domain:1643,
  RegistryAddress:p.DestinationRegistry.String()}
 route:=&evm.CheckpointRoute{Name:"base_to_xgr",SourceNetwork:"base",
  Destination:"xgr",RouteID:p.RouteID}
 a:=ilnCheckpointAttestation{
  Version:protocol.XITADirectMessageDomain,Chain:route.Name,Destination:dest.Name,
  OriginChainID:p.SourceChainID,OriginDomain:p.SourceDomain,DestinationDomain:p.DestinationDomain,
  RouteID:p.RouteID.String(),SetID:p.SetID,SourceBlockNumber:p.SourceBlockNumber,
  Registry:p.SourceRegistry.String(),Gateway:p.Gateway.String(),
  SourceRouter:p.SourceRouter.String(),Mailbox:p.SourceMailbox.String(),
  DestinationRouter:p.DestinationRouter.String(),ValidatorFeeWei:p.ValidatorFeeWei.String(),
  AuthorizedMessageID:p.MessageID.String(),DestinationISM:p.DestinationISM.String(),
  DestinationRegistry:p.DestinationRegistry.String(),
  Payload:"0x"+hex.EncodeToString(raw),SignerBitmap:"0x"+hex.EncodeToString(bitmap.Bytes()),
  AggregateSignature:"0x"+hex.EncodeToString(eip),
  AggregateSignatureCompressed:"0x"+hex.EncodeToString(sig),
 }
 _,err=verifyImportedILNQuorum(a,route,source,dest,set,p.DestinationISM)
 require.NoError(t,err)
 stale:=*set;stale.SetID=8
 _,err=verifyImportedILNQuorum(a,route,source,dest,&stale,p.DestinationISM)
 require.Error(t,err,"never accept retired validator set")
 _,err=verifyImportedILNQuorum(a,route,source,dest,set,types.StringToAddress("0x9999999999999999999999999999999999999999"))
 require.Error(t,err,"never accept altered effective ISM")
 changed:=a;changed.SourceBlockNumber++
 _,err=verifyImportedILNQuorum(changed,route,source,dest,set,p.DestinationISM)
 require.Error(t,err,"cannot alter unsigned wrapper metadata")
 changed=a;changed.AggregateSignatureCompressed="0x"+hex.EncodeToString(make([]byte,96))
 _,err=verifyImportedILNQuorum(changed,route,source,dest,set,p.DestinationISM)
 require.Error(t,err,"malformed signature rejected")
 changed=a;changed.SignerBitmap="0x01"
 _,err=verifyImportedILNQuorum(changed,route,source,dest,set,p.DestinationISM)
 require.Error(t,err,"1/3 cannot substitute for 2/3")
 changed=a;changed.RouteID="0x"+hex.EncodeToString(make([]byte,32))
 _,err=verifyImportedILNQuorum(changed,route,source,dest,set,p.DestinationISM)
 require.Error(t,err,"no route replay")
}

func TestILNCompletedCandidateDiscoveryBounded(t *testing.T){
 dir:=t.TempDir()
 require.NoError(t,os.WriteFile(filepath.Join(dir,"0x"+stringRepeat("a",64)+".json"),[]byte("{}"),0600))
 require.NoError(t,os.WriteFile(filepath.Join(dir,"latest.json"),[]byte("{}"),0600))
 require.NoError(t,os.WriteFile(filepath.Join(dir,"0x"+stringRepeat("b",64)+".json"),make([]byte,ilnCompleteMaxArchiveBytes+1),0600))
 got:=ilnCompletedCandidates(dir)
 require.Len(t,got,1)
 require.Contains(t,got[0].path,stringRepeat("a",64))
}
func stringRepeat(s string,n int)string{
 out:=""
 for i:=0;i<n;i++{out+=s}
 return out
}
