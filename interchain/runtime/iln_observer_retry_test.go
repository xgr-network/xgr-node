package runtime

import (
 "errors"
 "math/big"
 "os"
 "path/filepath"
 "testing"
 "time"

 "github.com/stretchr/testify/require"
 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

func retryFixture(t *testing.T)(*Worker,*evm.Destination,observerRoute,evm.ILNOperation){
 t.Helper()
 source:=&evm.Destination{Name:"base",ChainID:8453,Domain:8453,ILNRegistryActivationBlock:100}
 route:=observerRoute{Route:&evm.CheckpointRoute{Name:"route",RouteID:types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")},
  Gateway:types.StringToAddress("0x1111111111111111111111111111111111111111"),DestinationDomain:1643}
 op:=evm.ILNOperation{RouteID:route.Route.RouteID,
  MessageID:types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
  TransactionHash:types.StringToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
  BlockNumber:101,DestinationDomain:1643,ValidatorFeeWei:big.NewInt(1),LogIndex:7}
 return &Worker{dataDir:t.TempDir()},source,route,op
}

func TestObserverRetryDurableDedupConflictAndReverification(t *testing.T){
 w,source,route,op:=retryFixture(t)
 require.NoError(t,w.queueObserverRetry(source,route,op))
 require.NoError(t,w.queueObserverRetry(source,route,op))
 count,err:=w.observerRetryCount(source.Name,route.Route.Name)
 require.NoError(t,err);require.Equal(t,1,count)
 changed:=op;changed.LogIndex++
 require.ErrorContains(t,w.queueObserverRetry(source,route,changed),"conflicting immutable")
 dir,err:=w.observerRetryDir(source.Name,route.Route.Name)
 require.NoError(t,err)
 path:=filepath.Join(dir,"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.json")
 stamp:=time.Now().Add(-time.Minute)
 require.NoError(t,os.Chtimes(path,stamp,stamp))
 old:=observerSignMessage
 defer func(){observerSignMessage=old}()
 attempts:=0
 observerSignMessage=func(_ *Worker,r *evm.CheckpointRoute,id types.Hash,block uint64)error{
  attempts++;require.Equal(t,route.Route.Name,r.Name)
  require.Equal(t,op.MessageID,id);require.Equal(t,op.BlockNumber,block)
  if attempts==1{return errors.New("temporary RPC quorum failure")}
  return nil
 }
 require.NoError(t,w.retryObserverSource(source,[]observerRoute{route}))
 require.Equal(t,1,attempts)
 count,err=w.observerRetryCount(source.Name,route.Route.Name)
 require.NoError(t,err);require.Equal(t,1,count)
 require.NoError(t,os.Chtimes(path,stamp,stamp))
 require.NoError(t,w.retryObserverSource(source,[]observerRoute{route}))
 require.Equal(t,2,attempts)
 count,err=w.observerRetryCount(source.Name,route.Route.Name)
 require.NoError(t,err);require.Zero(t,count)
}

func TestObserverRetryRejectsCorruptJournalWithoutSigning(t *testing.T){
 w,source,route,op:=retryFixture(t)
 require.NoError(t,w.queueObserverRetry(source,route,op))
 dir,err:=w.observerRetryDir(source.Name,route.Route.Name)
 require.NoError(t,err)
 path:=filepath.Join(dir,"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.json")
 require.NoError(t,os.WriteFile(path,[]byte("{}"),0o660))
 stamp:=time.Now().Add(-time.Minute)
 require.NoError(t,os.Chtimes(path,stamp,stamp))
 original:=observerSignMessage
 defer func(){observerSignMessage=original}()
 observerSignMessage=func(_ *Worker,_ *evm.CheckpointRoute,_ types.Hash,_ uint64)error{
  t.Fatal("unverified/invalid retry must never be signed")
  return nil
 }
 require.ErrorContains(t,w.retryObserverSource(source,[]observerRoute{route}),"identity mismatch")
}
