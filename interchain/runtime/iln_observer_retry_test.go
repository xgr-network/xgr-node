package runtime

import (
 "errors"
 "math/big"
 "os"
 "path/filepath"
 "strings"
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
 require.NoError(t,w.retryObserverSource(source,[]observerRoute{route}))
 _,err=os.Stat(path);require.NoError(t,err,"invalid evidence must not be discarded")
}

func TestObserverRetryRouteRotationReachesLateRoutes(t *testing.T){
 const routes=13
 seen:=map[int]bool{}
 first:=time.Unix(0,0)
 for tick:=0;tick<routes;tick++{
  start:=observerRetryRouteStart(routes,first.Add(time.Duration(tick)*observerRetryMinInterval))
  require.GreaterOrEqual(t,start,0)
  require.Less(t,start,routes)
  seen[start]=true
 }
 require.Len(t,seen,routes,"all routes must have a first-served poll")
 require.Equal(t,0,observerRetryRouteStart(0,first))
}

func TestF04CorruptRouteDoesNotStarveHealthySibling(t *testing.T){
 w,source,bad,op:=retryFixture(t)
 bad.Route.Name="broken"
 require.NoError(t,w.queueObserverRetry(source,bad,op))
 badDir,err:=w.observerRetryDir(source.Name,bad.Route.Name)
 require.NoError(t,err)
 badPath:=filepath.Join(badDir,strings.TrimPrefix(op.MessageID.String(),"0x")+".json")
 require.NoError(t,os.WriteFile(badPath,[]byte("{}"),0o660))
 healthy:=bad
 healthy.Route=&evm.CheckpointRoute{Name:"healthy",RouteID:bad.Route.RouteID}
 require.NoError(t,w.queueObserverRetry(source,healthy,op))
 healthyDir,err:=w.observerRetryDir(source.Name,healthy.Route.Name)
 require.NoError(t,err)
 healthyPath:=filepath.Join(healthyDir,strings.TrimPrefix(op.MessageID.String(),"0x")+".json")
 old:=time.Now().Add(-time.Minute)
 require.NoError(t,os.Chtimes(badPath,old,old))
 require.NoError(t,os.Chtimes(healthyPath,old,old))
 original:=observerSignMessage
 defer func(){observerSignMessage=original}()
 attempts:=0
 observerSignMessage=func(_ *Worker,r *evm.CheckpointRoute,_ types.Hash,_ uint64)error{
  require.Equal(t,"healthy",r.Name)
  attempts++
  return nil
 }
 require.NoError(t,w.retryObserverSource(source,[]observerRoute{bad,healthy}))
 require.Equal(t,1,attempts)
 _,err=os.Stat(badPath);require.NoError(t,err)
 _,err=os.Stat(healthyPath);require.True(t,os.IsNotExist(err))
}
