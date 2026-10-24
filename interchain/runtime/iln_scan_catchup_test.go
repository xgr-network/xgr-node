package runtime

import (
 "errors"
 "fmt"
 "testing"
 "time"

 "github.com/stretchr/testify/require"
 evm "github.com/xgr-network/xgr-node/interchain/evm"
)

func TestDirectILNAdaptiveWindowByLag(t *testing.T){
 require.Equal(t,uint64(256),directILNWindow(80,false))
 require.Equal(t,uint64(512),directILNWindow(100000,false))
 require.Equal(t,uint64(1000),directILNWindow(100000,true))
 require.Equal(t,uint64(256),directILNWindow(80,true))
}

func TestDirectILNCatchupAdvancesOnlyAfterDurableSuccess(t *testing.T){
 src:=&evm.Destination{Name:"arbitrum",ChainID:42161,Domain:42161,ILNRegistryActivationBlock:100}
 var scanned,committed []uint64
 reader:=func(cursor uint64,exists bool,window uint64)(uint64,uint64,uint64,[]evm.ILNOperation,error){
  require.True(t,exists)
  require.LessOrEqual(t,window,uint64(512))
  from:=cursor+1
  if from>3100{return 3100,0,0,nil,nil}
  to:=from+window-1
  if to>3100{to=3100}
  scanned=append(scanned,to)
  return 3100,from,to,nil,nil
 }
 head,cursor,err:=runDirectILNCatchup(src,0,false,reader,func([]evm.ILNOperation)error{return nil},
   func(to uint64)error{committed=append(committed,to);return nil})
 require.NoError(t,err)
 require.Equal(t,uint64(3100),head)
 require.Greater(t,cursor,uint64(179))
 require.LessOrEqual(t,len(scanned),directILNMaxWindowsPerPoll)
 require.Equal(t,scanned,committed)
 require.Equal(t,committed[len(committed)-1],cursor)
}

func TestDirectILNCatchupNeverSkipsFailedWindow(t *testing.T){
 src:=&evm.Destination{Name:"base",ChainID:8453,Domain:8453,ILNRegistryActivationBlock:10}
 count:=0
 reader:=func(cursor uint64,_ bool,window uint64)(uint64,uint64,uint64,[]evm.ILNOperation,error){
  count++
  if count==2{return 1000,0,0,nil,errors.New("429 rate limited")}
  return 1000,cursor+1,cursor+window,nil,nil
 }
 committed:=uint64(9)
 head,cursor,err:=runDirectILNCatchup(src,0,false,reader,func([]evm.ILNOperation)error{return nil},
 func(n uint64)error{committed=n;return nil})
 require.ErrorContains(t,err,"429")
 require.Equal(t,uint64(1000),head)
 require.Equal(t,committed,cursor)
 require.Equal(t,uint64(265),cursor)
}

func TestDirectILNFailedProcessingDoesNotAdvanceCursor(t *testing.T){
 src:=&evm.Destination{Name:"xgr",ChainID:1643,Domain:1643,ILNRegistryActivationBlock:100}
 calls:=0
 head,cursor,err:=runDirectILNCatchup(src,101,true,
 func(cursor uint64,_ bool,window uint64)(uint64,uint64,uint64,[]evm.ILNOperation,error){
  calls++;return 200,cursor+1,cursor+2,nil,nil
 },
 func([]evm.ILNOperation)error{return fmt.Errorf("source proof failed")},
 func(uint64)error{t.Fatal("must never commit an unverified scan window");return nil})
 require.ErrorContains(t,err,"source proof failed")
 require.Equal(t,uint64(200),head)
 require.Equal(t,uint64(101),cursor)
 require.Equal(t,1,calls)
}

func TestDirectILNRemoteBackoffIsBounded(t *testing.T){
 require.Equal(t,30*time.Second,directILNRetryDelay(1))
 require.Equal(t,60*time.Second,directILNRetryDelay(2))
 require.Equal(t,4*time.Minute,directILNRetryDelay(4))
 require.Equal(t,4*time.Minute,directILNRetryDelay(10))
 require.True(t,directILNTransportFailure(errors.New("HTTP 429 too many requests")))
 require.False(t,directILNTransportFailure(errors.New("invalid route identity")))
}

func TestDirectILNCatchupRejectsInvalidRanges(t *testing.T){
 src:=&evm.Destination{Name:"xgr",ChainID:1643,Domain:1643,ILNRegistryActivationBlock:77}
 _,cursor,err:=runDirectILNCatchup(src,78,true,
 func(cursor uint64,_ bool,window uint64)(uint64,uint64,uint64,[]evm.ILNOperation,error){
  return 100,cursor+2,cursor+window+1,nil,nil
 },
 func([]evm.ILNOperation)error{return nil},
 func(uint64)error{t.Fatal("invalid window cannot commit");return nil})
 require.ErrorContains(t,err,"invalid Direct-BLS scan range")
 require.Equal(t,uint64(78),cursor)
}
