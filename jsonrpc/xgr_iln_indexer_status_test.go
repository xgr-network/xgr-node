package jsonrpc

import (
 "encoding/json"
 "os"
 "path/filepath"
 "testing"
 "time"

 "github.com/stretchr/testify/require"
)

func TestILNIndexerStatusFreshStalledAfterRestartAndOutage(t *testing.T){
 dir:=t.TempDir()
 endpoint:=newXGREndpoint(nil,dir)
 _,err:=endpoint.GetILNIndexerStatus()
 require.Error(t,err)
 path:=filepath.Join(dir,"interchain","iln","indexer-health.json")
 require.NoError(t,os.MkdirAll(filepath.Dir(path),0o770))
 mk:=func(age time.Duration,alive bool){
  item:=ilnIndexerStatusRPC{Version:2,UpdatedUnix:time.Now().Add(-age).Unix(),
   WorkerAlive:alive,Routes:[]ilnIndexerRouteStatusRPC{{
    Route:"base_to_xgr",Source:"base",Destination:"xgr",State:"ok",
    SourceHead:100,ScanCursor:100,BlockLag:0,
   }}}
  raw,e:=json.Marshal(item);require.NoError(t,e)
  require.NoError(t,os.WriteFile(path,raw,0o660))
 }
 mk(0,true)
 fresh,err:=endpoint.GetILNIndexerStatus()
 require.NoError(t,err);require.Equal(t,"ok",fresh.State)
 mk(5*time.Minute,true)
 stalled,err:=endpoint.GetILNIndexerStatus()
 require.NoError(t,err);require.Equal(t,"stalled",stalled.State)
 require.False(t,stalled.WorkerAlive)
 mk(0,false)
 offline,err:=endpoint.GetILNIndexerStatus()
 require.NoError(t,err);require.Equal(t,"stalled",offline.State)
 mk(0,true)
 // A restarted worker can replace stale snapshots atomically without
 // changing chain/contract configuration.
 recovered,err:=endpoint.GetILNIndexerStatus()
 require.NoError(t,err);require.Equal(t,"ok",recovered.State)
}
