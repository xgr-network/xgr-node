package runtime

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xgr-network/xgr-node/types"
)

// The source-tx hash is part of the retry identity, NOT evidence or
// authorization. An untrusted incorrect hash must never throttle a correct
// independently observed transaction for the same message ID.
const (
	ilnEvidenceRetryInitial = 2 * time.Minute
	ilnEvidenceRetryMaximum = 30 * time.Minute
	ilnEvidenceRetryVersion = 1
)
var errILNEvidenceDeferred = errors.New("Direct-BLS source evidence retry deferred")

type ilnEvidenceRetryRecord struct {
	Version int `json:"version"`
	Route string `json:"route"`
	MessageID string `json:"messageId"`
	SourceBlock uint64 `json:"sourceBlock"`
	SourceTxHash string `json:"sourceTxHash,omitempty"`
	Failures uint32 `json:"failures"`
	NextAttemptUnixNano int64 `json:"nextAttemptUnixNano"`
}

// Per-validator deterministic jitter makes independent validators unlikely to
// retry the same external RPC providers on an identical wall-clock boundary.
func ilnEvidenceRetryDelay(failures uint32,seed string)time.Duration{
	delay:=ilnEvidenceRetryInitial
	for i:=uint32(1);i<failures&&delay<ilnEvidenceRetryMaximum;i++{
		delay*=2
		if delay>ilnEvidenceRetryMaximum{delay=ilnEvidenceRetryMaximum}
	}
	sum:=sha256.Sum256([]byte(seed))
	jitter:=time.Duration(binary.BigEndian.Uint16(sum[:2])%16)*delay/100
	if delay+jitter>ilnEvidenceRetryMaximum{return ilnEvidenceRetryMaximum}
	return delay+jitter
}

func (w *Worker) ilnEvidenceRetryPath(route string,id,tx types.Hash)(string,error){
	if w==nil||w.dataDir==""||route==""||len(route)>150||
		strings.ContainsAny(route,"/\\")||route=="."||route==".."||
		id==types.ZeroHash{
		return "",fmt.Errorf("invalid Direct-BLS retry identity")
	}
	token:="legacy"
	if tx!=types.ZeroHash{token=strings.TrimPrefix(tx.String(),"0x")}
	return filepath.Join(w.ilnDir(),"evidence-retry",route,
		strings.TrimPrefix(id.String(),"0x")+"-"+token+".json"),nil
}
func readILNEvidenceRetry(path string,expected ilnEvidenceRetryRecord)(ilnEvidenceRetryRecord,error){
	raw,err:=os.ReadFile(path)
	if os.IsNotExist(err){return expected,nil}
	if err!=nil{return ilnEvidenceRetryRecord{},err}
	if len(raw)==0||len(raw)>1024{return ilnEvidenceRetryRecord{},fmt.Errorf("invalid durable Direct-BLS retry record size")}
	var record ilnEvidenceRetryRecord
	if err=json.Unmarshal(raw,&record);err!=nil{return record,err}
	if record.Version!=expected.Version||record.Route!=expected.Route||
		record.MessageID!=expected.MessageID||record.SourceBlock!=expected.SourceBlock||
		record.SourceTxHash!=expected.SourceTxHash||record.NextAttemptUnixNano<=0{
		return record,fmt.Errorf("conflicting durable Direct-BLS retry identity")
	}
	return record,nil
}
func persistILNEvidenceRetry(path string,record ilnEvidenceRetryRecord)error{
	dir:=filepath.Dir(path)
	if err:=os.MkdirAll(dir,0o770);err!=nil{return err}
	tmp,err:=os.CreateTemp(dir,".retry-*")
	if err!=nil{return err}
	defer os.Remove(tmp.Name())
	if err=tmp.Chmod(0o660);err!=nil{tmp.Close();return err}
	raw,err:=json.Marshal(record)
	if err!=nil{tmp.Close();return err}
	if len(raw)>1024{tmp.Close();return fmt.Errorf("Direct-BLS retry record too large")}
	if _,err=tmp.Write(raw);err!=nil{tmp.Close();return err}
	if err=tmp.Sync();err!=nil{tmp.Close();return err}
	if err=tmp.Close();err!=nil{return err}
	if err=os.Rename(tmp.Name(),path);err!=nil{return err}
	return syncILNRetryDir(dir)
}
func syncILNRetryDir(dir string)error{
	d,err:=os.Open(dir)
	if err!=nil{return err}
	e:=d.Sync()
	closeErr:=d.Close()
	if e!=nil{return e}
	return closeErr
}

// beginILNEvidenceRetry serializes *all* signing paths (observer, durable
// observer replay, public quorum request and authenticated peer request) for
// the same source tx. It persists a crash-safe in-flight lease BEFORE any
// external source RPC read. Deferred work keeps existing queues and journals.
func (w *Worker) beginILNEvidenceRetry(
	route string,id types.Hash,block uint64,tx types.Hash,
)(func(error)error,error){
	if block==0{return nil,fmt.Errorf("invalid source block for retry")}
	path,err:=w.ilnEvidenceRetryPath(route,id,tx)
	if err!=nil{return nil,err}
	key:=path
	w.ilnRetryMu.Lock()
	defer w.ilnRetryMu.Unlock()
	if w.ilnRetryInFlight==nil{w.ilnRetryInFlight=make(map[string]bool)}
	if w.ilnRetryInFlight[key]{
		return nil,fmt.Errorf("%w: verification already active",errILNEvidenceDeferred)
	}
	expected:=ilnEvidenceRetryRecord{
		Version:ilnEvidenceRetryVersion,Route:route,MessageID:id.String(),
		SourceBlock:block,
	}
	if tx!=types.ZeroHash{expected.SourceTxHash=tx.String()}
	record,err:=readILNEvidenceRetry(path,expected)
	if err!=nil{return nil,fmt.Errorf("read Direct-BLS source retry: %w",err)}
	now:=time.Now()
	if record.NextAttemptUnixNano>now.UnixNano(){
		return nil,fmt.Errorf("%w until %s",errILNEvidenceDeferred,
			time.Unix(0,record.NextAttemptUnixNano).UTC().Format(time.RFC3339))
	}
	// If the process crashes during RPC verification, restart cannot bypass
	// the initial cooldown and stampede the same public providers.
	lease:=record
	lease.NextAttemptUnixNano=now.Add(ilnEvidenceRetryInitial).UnixNano()
	if err=persistILNEvidenceRetry(path,lease);err!=nil{
		return nil,fmt.Errorf("persist Direct-BLS source retry lease: %w",err)
	}
	w.ilnRetryInFlight[key]=true
	return func(result error)error{
		w.ilnRetryMu.Lock()
		defer w.ilnRetryMu.Unlock()
		delete(w.ilnRetryInFlight,key)
		if result==nil||strings.Contains(strings.ToLower(result.Error()),"already delivered"){
			if err:=os.Remove(path);err!=nil&&!os.IsNotExist(err){return err}
			return syncILNRetryDir(filepath.Dir(path))
		}
		record.Failures++
		record.NextAttemptUnixNano=time.Now().Add(ilnEvidenceRetryDelay(
			record.Failures,fmt.Sprintf("%s/%d/%s",key,record.Failures,w.localAddr.String()),
		)).UnixNano()
		if err:=persistILNEvidenceRetry(path,record);err!=nil{
			return fmt.Errorf("persist Direct-BLS source retry backoff: %w",err)
		}
		return nil
	},nil
}
