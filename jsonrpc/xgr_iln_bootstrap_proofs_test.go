package jsonrpc

import (
 "encoding/hex"
 "encoding/json"
 "os"
 "path/filepath"
 "strings"
 "testing"

 "github.com/stretchr/testify/require"
 protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

func TestGetILNBootstrapProofsReadsOnlyCryptographicallyVerifiedPublicFiles(t *testing.T){
 directory:=t.TempDir()
 endpoint:=newXGREndpoint(nil,directory)
 missing,err:=endpoint.GetILNBootstrapProofs(42161)
 require.NoError(t,err)
 require.Empty(t,missing.Proofs)
 require.Equal(t,uint32(42161),missing.DestinationDomain)
 _,err=endpoint.GetILNBootstrapProofs(0)
 require.Error(t,err)

 secret,err:=crypto.GenerateBLSKey()
 require.NoError(t,err)
 pub,err:=crypto.BLSSecretKeyToPubkeyBytes(secret)
 require.NoError(t,err)
 eipPub,err:=crypto.BLSPublicKeyToEIP2537(pub)
 require.NoError(t,err)
 address:=types.StringToAddress("0x98F8bC086454B8386788244eee9A43d5D0b4E63E")
 payload,err:=protocol.MarshalBootstrapPayload(1643,42161,address,pub,eipPub)
 require.NoError(t,err)
 sig,err:=crypto.SignByBLS(secret,payload)
 require.NoError(t,err)
 eipSig,err:=crypto.BLSSignatureToEIP2537(sig)
 require.NoError(t,err)
 h:=func(b []byte)string{return "0x"+hex.EncodeToString(b)}
 p:=ilnBootstrapPublicProofRPC{
  Validator:address.String(),OriginChainID:1643,DestinationDomain:42161,
  BLSPublicKey:h(pub),BLSPublicKeyEIP2537:h(eipPub),
  Payload:h(payload),CompressedProof:h(sig),PossessionProof:h(eipSig),
 }
 path:=filepath.Join(directory,"interchain","bootstrap-public","42161",strings.ToLower(p.Validator)+".json")
 require.NoError(t,os.MkdirAll(filepath.Dir(path),0o770))
 raw,err:=json.Marshal(p)
 require.NoError(t,err)
 require.NoError(t,os.WriteFile(path,raw,0o660))
 result,err:=endpoint.GetILNBootstrapProofs(42161)
 require.NoError(t,err)
 require.Len(t,result.Proofs,1)
 require.Equal(t,p.Validator,result.Proofs[0].Validator)
 require.Equal(t,p.PossessionProof,result.Proofs[0].PossessionProof)
 require.Equal(t,p.CompressedProof,result.Proofs[0].CompressedProof)

 // Poisoned local evidence must never become an executable registry proof.
 p.Payload="0x00"
 raw,err=json.Marshal(p)
 require.NoError(t,err)
 require.NoError(t,os.WriteFile(path,raw,0o660))
 _,err=endpoint.GetILNBootstrapProofs(42161)
 require.Error(t,err)
}
