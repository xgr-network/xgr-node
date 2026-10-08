package jsonrpc

import (
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"

 "github.com/stretchr/testify/require"
)

var quorumTestMessage = "0x" + strings.Repeat("a", 64)

func TestILNRequestRequiresCanonicalIdentityAndLiveWorker(t *testing.T) {
 ep:=newXGREndpoint(nil,t.TempDir())
 _,err:=ep.RequestILNQuorum("../bad",quorumTestMessage,10)
 require.Error(t,err)
 _,err=ep.RequestILNQuorum("base_to_xgr","0xdead",10)
 require.Error(t,err)
 _,err=ep.RequestILNQuorum("base_to_xgr",quorumTestMessage,0)
 require.Error(t,err)
 _,err=ep.RequestILNQuorum("base_to_xgr",quorumTestMessage,10)
 require.ErrorContains(t,err,"worker not available")
}

func TestILNQuorumRequestPersistsAndDeduplicates(t *testing.T) {
 dir:=t.TempDir()
 heartbeat:=filepath.Join(dir,"interchain","worker.heartbeat")
 cursor:=filepath.Join(dir,"interchain","iln","cursors","base_to_xgr.cursor")
 require.NoError(t,os.MkdirAll(filepath.Dir(cursor),0o770))
 require.NoError(t,os.WriteFile(heartbeat,[]byte("1"),0o660))
 require.NoError(t,os.Chtimes(heartbeat,time.Now(),time.Now()))
 require.NoError(t,os.WriteFile(cursor,[]byte("1"),0o660))
 ep:=newXGREndpoint(nil,dir)
 id:="0x"+strings.Repeat("a",64)
 status,err:=ep.RequestILNQuorum("BASE_TO_XGR",id,12345)
 require.NoError(t,err)
 require.Equal(t,"PENDING",status.Status)
 status,err=ep.RequestILNQuorum("base_to_xgr",id,12345)
 require.NoError(t,err)
 require.Equal(t,"PENDING",status.Status)
 _,err=ep.RequestILNQuorum("base_to_xgr",id,12346)
 require.ErrorContains(t,err,"different source block")
 status,err=ep.GetILNQuorum("base_to_xgr",id)
 require.NoError(t,err)
 require.Equal(t,"PENDING",status.Status)
 require.Equal(t,uint64(12345),status.SourceBlockNumber)
 entries,err:=os.ReadDir(filepath.Join(dir,"interchain","iln","quorum-requests","base_to_xgr"))
 require.NoError(t,err)
 require.Len(t,entries,1)
}

func TestILNQuorumRequestFromNonSignerWithDiscoveredRoute(t *testing.T) {
 dir:=t.TempDir()
 heartbeat:=filepath.Join(dir,"interchain","worker.heartbeat")
 routeIndex:=filepath.Join(dir,"interchain","iln","registered-routes","iln_8453_1643_example.route")
 require.NoError(t,os.MkdirAll(filepath.Dir(routeIndex),0o770))
 require.NoError(t,os.WriteFile(routeIndex,[]byte("0x"+strings.Repeat("b",64)),0o660))
 require.NoError(t,os.WriteFile(heartbeat,[]byte("worker"),0o660))
 require.NoError(t,os.Chtimes(heartbeat,time.Now(),time.Now()))
 ep:=newXGREndpoint(nil,dir)
 got,err:=ep.RequestILNQuorum("iln_8453_1643_example",quorumTestMessage,12345)
 require.NoError(t,err)
 require.Equal(t,"PENDING",got.Status)
 require.Equal(t,quorumTestMessage,got.MessageID)
 // The route was admitted through confirmed discovery, even though the
 // non-validator has never created a signer cursor.
 _,err=os.Stat(filepath.Join(dir,"interchain","iln","cursors","iln_8453_1643_example.cursor"))
 require.True(t,os.IsNotExist(err))
 _,err=ep.RequestILNQuorum("unverified_user_route",quorumTestMessage,12345)
 require.ErrorContains(t,err,"not configured or discovered locally")
}


func TestILNQuorumHintLeaseUnblocksStaleSourceBlock(t *testing.T) {
 dir:=t.TempDir()
 hb:=filepath.Join(dir,"interchain","worker.heartbeat")
 cursor:=filepath.Join(dir,"interchain","iln","cursors","base_to_xgr.cursor")
 require.NoError(t,os.MkdirAll(filepath.Dir(cursor),0o770))
 require.NoError(t,os.WriteFile(hb,[]byte("1"),0o660))
 require.NoError(t,os.WriteFile(cursor,[]byte("1"),0o660))
 ep:=newXGREndpoint(nil,dir)
 _,err:=ep.RequestILNQuorum("base_to_xgr",quorumTestMessage,10)
 require.NoError(t,err)
 filename:=filepath.Join(dir,"interchain","iln","quorum-requests","base_to_xgr",quorumTestMessage+".json")
 past:=time.Now().Add(-2*ilnQuorumHintLease)
 require.NoError(t,os.Chtimes(filename,past,past))
 status,err:=ep.GetILNQuorum("base_to_xgr",quorumTestMessage)
 require.NoError(t,err)
 require.Equal(t,"EXPIRED_RETRY",status.Status)
 // Replacing an unverified hint never re-locks or re-pays a transfer.
 status,err=ep.RequestILNQuorum("base_to_xgr",quorumTestMessage,11)
 require.NoError(t,err)
 require.Equal(t,uint64(11),status.SourceBlockNumber)
 require.Equal(t,"PENDING",status.Status)
}
