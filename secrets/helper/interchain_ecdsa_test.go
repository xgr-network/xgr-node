package helper_test

import (
 "bytes"
 "os"
 "path/filepath"
 "testing"

 "github.com/stretchr/testify/require"
 "github.com/xgr-network/xgr-node/secrets"
 helper "github.com/xgr-network/xgr-node/secrets/helper"
 "github.com/xgr-network/xgr-node/types"
)

func TestDedicatedInterchainECDSAKeyNeverOverwritesPoSOrBLS(t *testing.T) {
 dir:=t.TempDir()
 sm,err:=helper.SetupLocalSecretsManager(dir)
 require.NoError(t,err)
 _,err=helper.InitInterchainECDSAValidatorKey(sm)
 require.Error(t,err,"PoS validator identity must already exist")
 validator,err:=helper.InitECDSAValidatorKey(sm)
 require.NoError(t,err)
 signer,err:=helper.InitInterchainECDSAValidatorKey(sm)
 require.NoError(t,err)
 require.NotEqual(t,validator,signer)
 storedSigner,err:=helper.LoadInterchainECDSASignerAddress(sm)
 require.NoError(t,err)
 require.Equal(t,signer,storedSigner)
 storedValidator,err:=helper.LoadValidatorAddress(sm)
 require.NoError(t,err)
 require.Equal(t,validator,storedValidator)
 validatorRaw,err:=sm.GetSecret(secrets.ValidatorKey)
 require.NoError(t,err)
 signerRaw,err:=sm.GetSecret(secrets.ValidatorInterchainECDSAKey)
 require.NoError(t,err)
 require.False(t,bytes.Equal(validatorRaw,signerRaw))
 path:=filepath.Join(dir,"consensus",secrets.ValidatorInterchainECDSAKeyLocal)
 stat,err:=os.Stat(path)
 require.NoError(t,err)
 require.Equal(t,os.FileMode(0440),stat.Mode().Perm())
 _,err=helper.InitInterchainECDSAValidatorKey(sm)
 require.Error(t,err,"must never rotate this key by overwriting the existing secret")
 after,err:=sm.GetSecret(secrets.ValidatorInterchainECDSAKey)
 require.NoError(t,err)
 require.True(t,bytes.Equal(signerRaw,after))
 require.False(t,sm.HasSecret(secrets.ValidatorBLSKey))
 _,err=helper.InitBLSValidatorKey(sm)
 require.NoError(t,err)
 require.True(t,sm.HasSecret(secrets.ValidatorBLSKey))
 kept,err:=sm.GetSecret(secrets.ValidatorInterchainECDSAKey)
 require.NoError(t,err)
 require.Equal(t,signerRaw,kept)
}

func TestMissingDedicatedKeyNeverFallsBackToPoS(t *testing.T) {
 sm,err:=helper.SetupLocalSecretsManager(t.TempDir())
 require.NoError(t,err)
 _,err=helper.InitECDSAValidatorKey(sm)
 require.NoError(t,err)
 signer,err:=helper.LoadInterchainECDSASignerAddress(sm)
 require.NoError(t,err)
 require.Equal(t,types.ZeroAddress,signer)
 require.False(t,sm.HasSecret(secrets.ValidatorInterchainECDSAKey))
}
