package jsonrpc

import (
 "encoding/json"
 "os"
 "path/filepath"
 "strconv"
 "testing"
 "time"

 "github.com/stretchr/testify/require"
)

func TestDedicatedILNIndexedCoverageRequiresIndependentCompleteHistory(t *testing.T){
 t.Setenv("XGR_ILN_READ_INDEX","1")
 dir:=t.TempDir()
 endpoint:=newXGREndpoint(nil,dir)
 health:=filepath.Join(dir,"interchain","iln","indexer-health.json")
 cursor:=filepath.Join(dir,"interchain","iln","cursors","_index_bsc.cursor")
 require.NoError(t,os.MkdirAll(filepath.Dir(health),0o700))
 require.NoError(t,os.MkdirAll(filepath.Dir(cursor),0o700))
 writeStatus:=func(now time.Time,lag bool){
  state:="ok"
  if lag{state="unavailable"}
  v:=ilnIndexerStatusRPC{Version:2,UpdatedUnix:now.Unix(),WorkerAlive:true,
   Routes:[]ilnIndexerRouteStatusRPC{{Source:"bsc",Route:"bsc_to_xgr",
    SourceHeadKnown:true,SourceHead:100,ScanCursorKnown:true,ScanCursor:100,
    LastSuccessfulScanUnix:now.Unix(),State:state}}}
  raw,e:=json.Marshal(v);require.NoError(t,e)
  require.NoError(t,os.WriteFile(health,raw,0o600))
 }
 writeStatus(time.Now(),false)
 initial,err:=endpoint.GetILNIndexedCoverage("bsc")
 require.NoError(t,err)
 require.Equal(t,"backfilling",initial.State)
 require.False(t,initial.Complete)
 require.False(t,initial.IndexedKnown)
 require.NoError(t,os.WriteFile(cursor,[]byte("80\n"),0o600))
 behind,err:=endpoint.GetILNIndexedCoverage("bsc")
 require.NoError(t,err);require.Equal(t,"backfilling",behind.State)
 require.Equal(t,uint64(80),behind.IndexedTo)
 require.NoError(t,os.WriteFile(cursor,[]byte(strconv.Itoa(100)),0o600))
 ready,err:=endpoint.GetILNIndexedCoverage("bsc")
 require.NoError(t,err);require.True(t,ready.Complete)
 require.Equal(t,"complete",ready.State)
 writeStatus(time.Now(),true)
 degraded,err:=endpoint.GetILNIndexedCoverage("bsc")
 require.NoError(t,err);require.False(t,degraded.Complete)
 t.Setenv("XGR_ILN_READ_INDEX","")
 _,err=endpoint.GetILNIndexedCoverage("bsc")
 require.ErrorContains(t,err,"not enabled")
}
