package jsonrpc

import (
 "encoding/json"
 "fmt"
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


func TestILNRegisteredQuorumRequestSurvivesHintExpiry(t *testing.T) {
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
 require.Equal(t,"PENDING",status.Status)
 // Re-admission cannot evict the existing durable request.
 _,err=ep.RequestILNQuorum("base_to_xgr",quorumTestMessage,11)
 require.ErrorContains(t,err,"different source block")
}


func TestILNQuorumProgressSurvivesRequestCleanup(t *testing.T) {
 dir:=t.TempDir()
 route:="iln_8453_1643_"+strings.Repeat("b",64)
 path:=filepath.Join(dir,"interchain","iln","quorum-status",route,quorumTestMessage+".json")
 require.NoError(t,os.MkdirAll(filepath.Dir(path),0o770))
 progress:=map[string]interface{}{
  "version":1,"route":route,"messageId":quorumTestMessage,
  "sourceBlockNumber":uint64(52801389),"status":"PARTIAL_QUORUM",
  "votes":1,"quorumThreshold":2,"updatedUnix":time.Now().Unix(),
 }
 raw,err:=json.Marshal(progress);require.NoError(t,err)
 require.NoError(t,os.WriteFile(path,raw,0o660))
 ep:=newXGREndpoint(nil,dir)
 status,err:=ep.GetILNQuorum(route,quorumTestMessage)
 require.NoError(t,err)
 require.Equal(t,"PARTIAL_QUORUM",status.Status)
 require.Equal(t,1,status.Votes)
 require.Equal(t,2,status.QuorumThreshold)
 require.Equal(t,uint64(52801389),status.SourceBlockNumber)
 // Temporary validation errors must remain actionable after original
 // admission file was deleted.
 progress["status"]="RETRYING_VALIDATION"
 progress["failureCategory"]="SOURCE_RECEIPT_NOT_VERIFIED"
 raw,err=json.Marshal(progress);require.NoError(t,err)
 require.NoError(t,os.WriteFile(path,raw,0o660))
 status,err=ep.GetILNQuorum(route,quorumTestMessage)
 require.NoError(t,err)
 require.Equal(t,"EXPIRED_RETRY",status.Status)
 require.Equal(t,"SOURCE_RECEIPT_NOT_VERIFIED",status.FailureCategory)
 // Registered work remains live after the public discovery hint TTL.
 cursor:=filepath.Join(dir,"interchain","iln","cursors",route+".cursor")
 require.NoError(t,os.MkdirAll(filepath.Dir(cursor),0o770))
 require.NoError(t,os.WriteFile(cursor,[]byte("1"),0o660))
 requestPath:=filepath.Join(dir,"interchain","iln","quorum-requests",route,quorumTestMessage+".json")
 require.NoError(t,os.MkdirAll(filepath.Dir(requestPath),0o770))
 request,err:=json.Marshal(ilnQuorumRequestRPC{Route:route,MessageID:quorumTestMessage,SourceBlockNumber:52801389})
 require.NoError(t,err)
 require.NoError(t,os.WriteFile(requestPath,request,0o660))
 past:=time.Now().Add(-30*time.Minute)
 require.NoError(t,os.Chtimes(requestPath,past,past))
 status,err=ep.GetILNQuorum(route,quorumTestMessage)
 require.NoError(t,err)
 require.Equal(t,"RETRYING_VALIDATION",status.Status)
 // If no queued work remains, expose the state that prompts re-admission.
 require.NoError(t,os.Remove(requestPath))
 status,err=ep.GetILNQuorum(route,quorumTestMessage)
 require.NoError(t,err)
 require.Equal(t,"EXPIRED_RETRY",status.Status)
 // Stale/unknown state must not create a false signed quorum.
 progress["updatedUnix"]=time.Now().Add(-48*time.Hour).Unix()
 raw,err=json.Marshal(progress);require.NoError(t,err)
 require.NoError(t,os.WriteFile(path,raw,0o660))
 status,err=ep.GetILNQuorum(route,quorumTestMessage)
 require.NoError(t,err)
 require.Equal(t,"NOT_FOUND",status.Status)
}

func TestILNUnknownCanonicalRouteCreatesHintNotSigningAdmission(t *testing.T) {
 dir:=t.TempDir()
 heartbeat:=filepath.Join(dir,"interchain","worker.heartbeat")
 require.NoError(t,os.MkdirAll(filepath.Dir(heartbeat),0o770))
 require.NoError(t,os.WriteFile(heartbeat,[]byte("worker"),0o660))
 route:="iln_42161_1643_"+strings.Repeat("f",64)
 ep:=newXGREndpoint(nil,dir)
 status,err:=ep.RequestILNQuorum(route,quorumTestMessage,517578687)
 require.NoError(t,err)
 require.Equal(t,"PENDING",status.Status)
 hintPath:=filepath.Join(dir,"interchain","iln","route-hints",route+".json")
 raw,err:=os.ReadFile(hintPath)
 require.NoError(t,err)
 require.Contains(t,string(raw),`"sourceDomain":42161`)
 require.Contains(t,string(raw),`"destinationDomain":1643`)
 require.Contains(t,string(raw),strings.Repeat("f",64))
 _,err=os.Stat(filepath.Join(dir,"interchain","iln","registered-routes",route+".route"))
 require.True(t,os.IsNotExist(err),"unverified hints must not register a route")
 _,err=ep.RequestILNQuorum(route,quorumTestMessage,517578687)
 require.NoError(t,err,"same hint is idempotent")
 _,err=ep.RequestILNQuorum("iln_042161_1643_"+strings.Repeat("f",64),quorumTestMessage,123)
 require.Error(t,err)
}

func TestF32UnknownRouteDirectoryCapacityPreservesExistingRequests(t *testing.T){
 dir:=t.TempDir()
 base:=filepath.Join(dir,"interchain","iln","quorum-requests")
 for i:=0;i<maxUnverifiedILNRouteDirs;i++{
  require.NoError(t,os.MkdirAll(filepath.Join(base,fmt.Sprintf("unverified-%03d",i)),0o770))
 }
 require.NoError(t,checkUnverifiedILNRequestCapacity(dir,"unverified-000"))
 require.ErrorContains(t,checkUnverifiedILNRequestCapacity(dir,"new-unknown-route"),"capacity")
}

func TestReceiptPinnedQuorumHintIdempotentAndFailClosed(t *testing.T){
 dir:=t.TempDir()
 heartbeat:=filepath.Join(dir,"interchain","worker.heartbeat")
 cursor:=filepath.Join(dir,"interchain","iln","cursors","base_to_xgr.cursor")
 require.NoError(t,os.MkdirAll(filepath.Dir(cursor),0o770))
 require.NoError(t,os.WriteFile(heartbeat,[]byte("1"),0o660))
 require.NoError(t,os.WriteFile(cursor,[]byte("1"),0o660))
 ep:=newXGREndpoint(nil,dir)
 tx:="0x"+strings.Repeat("b",64)
 _,err:=ep.RequestILNQuorumWithSourceTx("base_to_xgr",quorumTestMessage,12345,"0xdead")
 require.Error(t,err)
 _,err=ep.RequestILNQuorumWithSourceTx("base_to_xgr",quorumTestMessage,12345,"0x"+strings.Repeat("0",64))
 require.Error(t,err)
 status,err:=ep.RequestILNQuorumWithSourceTx("base_to_xgr",quorumTestMessage,12345,tx)
 require.NoError(t,err);require.Equal(t,"PENDING",status.Status)
 _,err=ep.RequestILNQuorumWithSourceTx("base_to_xgr",quorumTestMessage,12345,tx)
 require.NoError(t,err,"identical hints are idempotent")
 _,err=ep.RequestILNQuorumWithSourceTx("base_to_xgr",quorumTestMessage,12345,"0x"+strings.Repeat("c",64))
 require.Error(t,err,"a different transaction may not replace an active hint")
 _,err=ep.RequestILNQuorum("base_to_xgr",quorumTestMessage,12345)
 require.Error(t,err,"a legacy request may not silently erase the bound source transaction")
 raw,err:=os.ReadFile(filepath.Join(dir,"interchain","iln","quorum-requests","base_to_xgr",quorumTestMessage+".json"))
 require.NoError(t,err)
 var record ilnQuorumRequestRPC
 require.NoError(t,json.Unmarshal(raw,&record))
 require.Equal(t,tx,record.SourceTxHash)
 require.Equal(t,uint64(12345),record.SourceBlockNumber)
}
