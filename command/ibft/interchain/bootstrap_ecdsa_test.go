package interchain

import (
 "encoding/hex"
 "strings"
 "testing"

 "github.com/stretchr/testify/require"
 "github.com/xgr-network/xgr-node/crypto"
 ecdsaV5 "github.com/xgr-network/xgr-node/interchain/ecdsa"
 "github.com/xgr-network/xgr-node/secrets"
 secretsHelper "github.com/xgr-network/xgr-node/secrets/helper"
 "github.com/xgr-network/xgr-node/types"
)

func TestECDSAV5BootstrapProofUsesOnlyDedicatedSecret(t *testing.T) {
 dir:=t.TempDir()
 sm,err:=secretsHelper.SetupLocalSecretsManager(dir)
 require.NoError(t,err)
 posAddr,err:=secretsHelper.InitECDSAValidatorKey(sm)
 require.NoError(t,err)
 keyAddr,err:=secretsHelper.InitInterchainECDSAValidatorKey(sm)
 require.NoError(t,err)
 require.NotEqual(t,posAddr,keyAddr)
 require.False(t,sm.HasSecret(secrets.ValidatorBLSKey))
 p:=&bootstrapProofParams{dataDir:dir,originChainID:1643,destinationDomain:50,scheme:"ecdsa"}
 proof,err:=createBootstrapProof(p)
 require.NoError(t,err)
 require.Equal(t,"ecdsa",proof.SignatureScheme)
 require.Equal(t,posAddr.String(),proof.Validator)
 require.Equal(t,keyAddr.String(),proof.ECDSASigner)
 require.Empty(t,proof.BLSPublicKey)
 require.Empty(t,proof.BLSPublicKeyEIP2537)
 require.Contains(t,proof.GetOutput(),"Destination ECDSA signer")
 raw,err:=hex.DecodeString(strings.TrimPrefix(proof.Payload,"0x"))
 require.NoError(t,err)
 expected,err:=ecdsaV5.BootstrapPayloadV5(1643,50,posAddr,keyAddr)
 require.NoError(t,err)
 require.Equal(t,expected,raw)
 sig,err:=hex.DecodeString(strings.TrimPrefix(proof.PossessionProof,"0x"))
 require.NoError(t,err)
 require.Len(t,sig,65)
 require.Contains(t,[]byte{27,28},sig[64])
 sig[64]-=27
 digest:=crypto.Keccak256Hash(raw)
 pub,err:=crypto.RecoverPubkey(sig,digest.Bytes())
 require.NoError(t,err)
 require.Equal(t,keyAddr,crypto.PubKeyToAddress(pub))
 wrong,err:=ecdsaV5.BootstrapPayloadV5(1643,51,posAddr,keyAddr)
 require.NoError(t,err)
 require.NotEqual(t,digest,crypto.Keccak256Hash(wrong))
}

func TestECDSAV5BootstrapFailsClosedWithoutSeparateKey(t *testing.T) {
 dir:=t.TempDir()
 sm,err:=secretsHelper.SetupLocalSecretsManager(dir)
 require.NoError(t,err)
 _,err=secretsHelper.InitECDSAValidatorKey(sm)
 require.NoError(t,err)
 _,err=createBootstrapProof(&bootstrapProofParams{dataDir:dir,originChainID:1643,destinationDomain:50,scheme:"ecdsa"})
 require.Error(t,err)
 require.False(t,sm.HasSecret(secrets.ValidatorInterchainECDSAKey))
 _,err=createBootstrapProof(&bootstrapProofParams{dataDir:dir,originChainID:1643,destinationDomain:51,scheme:"ecdsa"})
 require.Error(t,err)
 // The default BLS path is still separate and requires the BLS key.
 _,err=createBootstrapProof(&bootstrapProofParams{dataDir:dir,originChainID:1643,destinationDomain:50,scheme:"bls"})
 require.Error(t,err)
 require.NotEqual(t,types.ZeroAddress,types.StringToAddress("0x0000000000000000000000000000000000000aaa"))
}
