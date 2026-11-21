package runtime

import (
 "encoding/json"
 "errors"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"

 "github.com/hashicorp/go-hclog"
 "github.com/stretchr/testify/require"
 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

func TestILNEvidenceRetryDurableLeaseBackoffAndRestart(t *testing.T){
 dir:=t.TempDir()
 id:=types.StringToHash("0x"+strings.Repeat("a",64))
 tx:=types.StringToHash("0x"+strings.Repeat("b",64))
 first:=&Worker{dataDir:dir}
 finish,err:=first.beginILNEvidenceRetry("iln_56_1643_test",id,123,tx)
 require.NoError(t,err)
 require.NotNil(t,finish)
 _,err=first.beginILNEvidenceRetry("iln_56_1643_test",id,123,tx)
 require.ErrorIs(t,err,errILNEvidenceDeferred,"active attempt must not overlap")
 require.NoError(t,finish(errors.New("RPC source unavailable")))
 // Retrying after a process restart must not evade the durable cooldown.
 restarted:=&Worker{dataDir:dir}
 _,err=restarted.beginILNEvidenceRetry("iln_56_1643_test",id,123,tx)
 require.ErrorIs(t,err,errILNEvidenceDeferred)
 path,err:=restarted.ilnEvidenceRetryPath("iln_56_1643_test",id,tx)
 require.NoError(t,err)
 record,err:=readILNEvidenceRetry(path,ilnEvidenceRetryRecord{
  Version:ilnEvidenceRetryVersion,Route:"iln_56_1643_test",
  MessageID:id.String(),SourceBlock:123,SourceTxHash:tx.String()})
 require.NoError(t,err)
 require.Equal(t,uint32(1),record.Failures)
 require.Greater(t,record.NextAttemptUnixNano,time.Now().UnixNano())
 // Explicitly advance the *test fixture*, never a production retry journal.
 record.NextAttemptUnixNano=time.Now().Add(-time.Second).UnixNano()
 require.NoError(t,persistILNEvidenceRetry(path,record))
 finish,err=restarted.beginILNEvidenceRetry("iln_56_1643_test",id,123,tx)
 require.NoError(t,err)
 require.NoError(t,finish(nil))
 _,err=os.Stat(path)
 require.True(t,os.IsNotExist(err),"success must remove only the backoff sidecar")
}

func TestILNEvidenceRetryBackoffBoundedAndDeterministicallyJittered(t *testing.T){
 require.Equal(t,2*time.Minute,ilnEvidenceRetryInitial)
 require.Equal(t,30*time.Minute,ilnEvidenceRetryMaximum)
 d1:=ilnEvidenceRetryDelay(1,"a")
 d2:=ilnEvidenceRetryDelay(2,"a")
 d3:=ilnEvidenceRetryDelay(3,"a")
 require.GreaterOrEqual(t,d1,2*time.Minute)
 require.LessOrEqual(t,d1,3*time.Minute)
 require.Greater(t,d2,d1)
 require.Greater(t,d3,d2)
 require.Equal(t,d1,ilnEvidenceRetryDelay(1,"a"))
 require.Equal(t,30*time.Minute,ilnEvidenceRetryDelay(100,"a"))
}

func TestILNEvidenceRetryInvalidJournalFailsClosed(t *testing.T){
 w:=&Worker{dataDir:t.TempDir()}
 id:=types.StringToHash("0x"+strings.Repeat("c",64))
 tx:=types.StringToHash("0x"+strings.Repeat("d",64))
 path,err:=w.ilnEvidenceRetryPath("iln_56_1643_test",id,tx)
 require.NoError(t,err)
 require.NoError(t,os.MkdirAll(filepath.Dir(path),0o770))
 require.NoError(t,os.WriteFile(path,[]byte("{bad"),0o660))
 _,err=w.beginILNEvidenceRetry("iln_56_1643_test",id,123,tx)
 require.Error(t,err)
 raw,err:=os.ReadFile(path)
 require.NoError(t,err)
 require.Equal(t,"{bad",string(raw),"corrupt journal is not silently discarded")
}

func TestILNEvidenceRetryInvalidTxCannotOverwriteOtherIdentity(t *testing.T){
 w:=&Worker{dataDir:t.TempDir()}
 id:=types.StringToHash("0x"+strings.Repeat("e",64))
 goodTx:=types.StringToHash("0x"+strings.Repeat("1",64))
 badTx:=types.StringToHash("0x"+strings.Repeat("2",64))
 first,err:=w.beginILNEvidenceRetry("iln_56_1643_test",id,123,goodTx)
 require.NoError(t,err)
 other,err:=w.beginILNEvidenceRetry("iln_56_1643_test",id,123,badTx)
 require.NoError(t,err,"untrusted bogus tx hints must never starve independently known correct transaction")
 require.NoError(t,first(errors.New("RPC unavailable")))
 require.NoError(t,other(errors.New("transaction hash mismatch")))
}

func TestILNQueuedQuorumSurvivesBackoffAndRetries(t *testing.T) {
	const route = "iln_56_1643_test"
	id := types.StringToHash("0x" + strings.Repeat("a", 64))
	tx := types.StringToHash("0x" + strings.Repeat("b", 64))
	w := &Worker{
		dataDir: t.TempDir(), logger: hclog.NewNullLogger(),
		routes: map[string]*evm.CheckpointRoute{route: {Name: route, SourceNetwork: "bsc"}},
		destinations: map[string]*evm.Destination{"bsc": {ChainID: 56}},
	}
	request := filepath.Join(w.ilnDir(), "quorum-requests", route, id.String()+".json")
	require.NoError(t, os.MkdirAll(filepath.Dir(request), 0o770))
	raw, err := json.Marshal(requestedILNQuorum{Route: route, MessageID: id.String(), SourceBlockNumber: 123, SourceTxHash: tx.String()})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(request, raw, 0o660))
	queuedAt := time.Now().Add(-31 * time.Minute)
	require.NoError(t, os.Chtimes(request, queuedAt, queuedAt))

	path, err := w.ilnEvidenceRetryPath(route, id, tx)
	require.NoError(t, err)
	retry := ilnEvidenceRetryRecord{
		Version: ilnEvidenceRetryVersion, Route: route, MessageID: id.String(), SourceBlock: 123,
		SourceTxHash: tx.String(), Failures: 8, NextAttemptUnixNano: time.Now().Add(time.Minute).UnixNano(),
	}
	require.NoError(t, persistILNEvidenceRetry(path, retry))
	w.processRequestedILNQuorums()
	require.FileExists(t, request)
	require.NoFileExists(t, w.ilnQuorumProgressPath(route, id.String()))

	retry.NextAttemptUnixNano = time.Now().Add(-time.Second).UnixNano()
	require.NoError(t, persistILNEvidenceRetry(path, retry))
	// The unsynchronized worker fails before external RPC and records a new retry.
	w.processRequestedILNQuorums()
	require.FileExists(t, request)
	require.FileExists(t, w.ilnQuorumProgressPath(route, id.String()))
	after, err := readILNEvidenceRetry(path, retry)
	require.NoError(t, err)
	require.Equal(t, uint32(9), after.Failures)
	require.Greater(t, after.NextAttemptUnixNano, time.Now().UnixNano())
}
