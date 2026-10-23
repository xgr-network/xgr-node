package jsonrpc

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strings"
 "time"

 evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
)

const ilnQuorumHintLease = 15*time.Minute

// ILN quorum requests are hints only. They contain NO caller-provided payload
// or signatures. The validator worker reconstructs and verifies all evidence.
// The source block is mandatory to prevent unbounded historical log scans.
type ilnQuorumRequestRPC struct {
 Route string `json:"route"`
 MessageID string `json:"messageId"`
 SourceBlockNumber uint64 `json:"sourceBlockNumber"`
}

type ilnQuorumStatusRPC struct {
 Route string `json:"route"`
 MessageID string `json:"messageId"`
 SourceBlockNumber uint64 `json:"sourceBlockNumber"`
 Status string `json:"status"`
 Votes int `json:"votes,omitempty"`
 QuorumThreshold int `json:"quorumThreshold,omitempty"`
 FailureCategory string `json:"failureCategory,omitempty"`
 UpdatedUnix int64 `json:"updatedUnix,omitempty"`
}

func (x *xgrEndpoint) RequestILNQuorum(route, messageID string, sourceBlockNumber uint64) (*ilnQuorumStatusRPC, error) {
 normalized, err := normalizeInterchainRPCChain(route)
 if err != nil { return nil, err }
 normalizedID, err := normalizedILNMessageID(messageID)
 if err != nil { return nil, err }
 if sourceBlockNumber == 0 { return nil, fmt.Errorf("source block is required for bounded ILN quorum lookup") }
 if x == nil || strings.TrimSpace(x.dataDir) == "" { return nil, fmt.Errorf("ILN storage unavailable") }
 // Worker heartbeat is a low-cost admission check. This RPC never signs.
 heartbeat, err := os.Stat(filepath.Join(x.dataDir,"interchain","worker.heartbeat"))
 if err != nil || time.Since(heartbeat.ModTime()) > 10*time.Second {
  return nil, fmt.Errorf("ILN worker not available")
 }
 // Do not let random public callers create arbitrary route directories.
 cursor, cursorErr := os.Stat(filepath.Join(x.dataDir,"interchain","iln","cursors",normalized+".cursor"))
 registered, routeErr := os.Stat(filepath.Join(x.dataDir,"interchain","iln","registered-routes",normalized+".route"))
 if (cursorErr!=nil||!cursor.Mode().IsRegular()) && (routeErr!=nil||!registered.Mode().IsRegular()) {
  // An unknown deterministic route is only a discovery hint. Admission to
  // the quorum queue is NOT permission to sign: the validator worker must
  // independently verify the canonical source registry before processing it.
  sourceDomain,destinationDomain,routeID,parseErr:=evmInterchain.ParseILNRouteName(normalized)
  if parseErr!=nil{return nil,fmt.Errorf("route is not configured or discovered locally")}
  hintDir:=filepath.Join(x.dataDir,"interchain","iln","route-hints")
  if err:=os.MkdirAll(hintDir,0o770);err!=nil{return nil,err}
  hintPath:=filepath.Join(hintDir,normalized+".json")
  hint:=struct{
   SourceDomain uint32 `json:"sourceDomain"`
   DestinationDomain uint32 `json:"destinationDomain"`
   RouteID string `json:"routeId"`
  }{sourceDomain,destinationDomain,routeID.String()}
  raw,err:=json.Marshal(hint);if err!=nil{return nil,err}
  // Bound unknown route hints. Repeated requests for the same identity are
  // idempotent; bogus identities can never create validator attestations.
  if _,err:=os.Stat(hintPath);os.IsNotExist(err){
   entries,err:=os.ReadDir(hintDir);if err!=nil{return nil,err}
   if len(entries)>=128{return nil,fmt.Errorf("ILN discovery hint queue full")}
   f,err:=os.OpenFile(hintPath,os.O_WRONLY|os.O_CREATE|os.O_EXCL,0o660)
   if err!=nil&&!os.IsExist(err){return nil,err}
   if err==nil{
    if _,err=f.Write(raw);err!=nil{f.Close();os.Remove(hintPath);return nil,err}
    if err=f.Close();err!=nil{os.Remove(hintPath);return nil,err}
   }
  }else if err!=nil{return nil,err}
 }
 status := &ilnQuorumStatusRPC{Route:normalized, MessageID:normalizedID,SourceBlockNumber:sourceBlockNumber,Status:"PENDING"}
 dir:=filepath.Join(x.dataDir,"interchain","iln","quorum-requests",normalized)
 if err:=os.MkdirAll(dir,0o770);err!=nil{return nil,err}
 target:=filepath.Join(dir,normalizedID+".json")
 // Check existing request BEFORE enforcing the queue limit. Otherwise a full
 // queue makes even an idempotent status retry fail.
 if raw,readErr:=os.ReadFile(target); readErr==nil {
  if info,statErr:=os.Stat(target);statErr==nil && time.Since(info.ModTime())>ilnQuorumHintLease {
   _=os.Remove(target)
  } else {
   var existing ilnQuorumRequestRPC
   if json.Unmarshal(raw,&existing)!=nil || existing.Route!=normalized || existing.MessageID!=normalizedID || existing.SourceBlockNumber!=sourceBlockNumber {
    return nil,fmt.Errorf("ILN request exists with a different source block or invalid content")
   }
   return status,nil
  }
 } else if !os.IsNotExist(readErr){return nil,readErr}
 entries,err:=os.ReadDir(dir)
 if err!=nil{return nil,err}
 live:=0
 for _,entry:=range entries {
  if entry.IsDir()||!strings.HasSuffix(entry.Name(),".json"){continue}
  info,statErr:=entry.Info()
  if statErr==nil && time.Since(info.ModTime())>ilnQuorumHintLease {
   _=os.Remove(filepath.Join(dir,entry.Name()))
  }else {live++}
 }
 if live>=128 {return nil,fmt.Errorf("ILN request queue full; retry later")}
 // O_EXCL guarantees idempotent enqueue under parallel RPC calls, and bounds
 // disk consumption to one request per route/message rather than caller/IP.
 file,err:=os.OpenFile(target,os.O_WRONLY|os.O_CREATE|os.O_EXCL,0o660)
 if os.IsExist(err) {
  raw,readErr:=os.ReadFile(target)
  if readErr!=nil{return nil,readErr}
  var existing ilnQuorumRequestRPC
  if json.Unmarshal(raw,&existing)!=nil || existing.Route!=normalized || existing.MessageID!=normalizedID || existing.SourceBlockNumber!=sourceBlockNumber {
   return nil,fmt.Errorf("ILN request exists with a different source block or invalid content")
  }
  return status,nil
 }
 if err!=nil{return nil,err}
 encoded,err:=json.Marshal(ilnQuorumRequestRPC{Route:normalized,MessageID:normalizedID,SourceBlockNumber:sourceBlockNumber})
 if err!=nil{file.Close();os.Remove(target);return nil,err}
 _,err=file.Write(encoded)
 closeErr:=file.Close()
 if err!=nil||closeErr!=nil{os.Remove(target);return nil,fmt.Errorf("persist ILN quorum request: %v / %v",err,closeErr)}
 return status,nil
}

func normalizedILNMessageID(value string)(string,error){
 value=strings.ToLower(strings.TrimSpace(value))
 if len(value)!=66 || !strings.HasPrefix(value,"0x"){return "",fmt.Errorf("invalid message ID")}
 for _,ch:=range value[2:] {
  if (ch<'0'||ch>'9')&&(ch<'a'||ch>'f'){return "",fmt.Errorf("invalid message ID")}
 }
 if value=="0x"+strings.Repeat("0",64){return "",fmt.Errorf("zero message ID")}
 return value,nil
}

func (x *xgrEndpoint) GetILNQuorum(route,messageID string)(*ilnQuorumStatusRPC,error){
 route,err:=normalizeInterchainRPCChain(route);if err!=nil{return nil,err}
 messageID,err=normalizedILNMessageID(messageID);if err!=nil{return nil,err}
 if x==nil||x.dataDir==""{return nil,fmt.Errorf("ILN storage unavailable")}
 dir:=filepath.Join(x.dataDir,"interchain","attestations",route)
 if _,err:=x.readInterchainAttestation(filepath.Join(dir,messageID+".json"));err==nil{
  // Current destination-set status is validated only by the executor and
  // validator worker. The raw archival quorum may be stale after rotation.
  return &ilnQuorumStatusRPC{Route:route,MessageID:messageID,Status:"AVAILABLE_UNVERIFIED"},nil
 }
 // The worker may already have signed and removed the admission hint
 // without collecting 2/3 signatures. Persistent progress is therefore
 // authoritative for *diagnostics*, never for creating an attestation.
 statusPath:=filepath.Join(x.dataDir,"interchain","iln","quorum-status",route,messageID+".json")
 if progressRaw,progressErr:=os.ReadFile(statusPath);progressErr==nil&&len(progressRaw)<=4096 {
  var progress struct{
   Version int `json:"version"`
   Route string `json:"route"`
   MessageID string `json:"messageId"`
   SourceBlockNumber uint64 `json:"sourceBlockNumber"`
   Status string `json:"status"`
   Votes int `json:"votes"`
   QuorumThreshold int `json:"quorumThreshold"`
   FailureCategory string `json:"failureCategory"`
   UpdatedUnix int64 `json:"updatedUnix"`
  }
  if json.Unmarshal(progressRaw,&progress)==nil&&progress.Version==1&&
    progress.Route==route&&progress.MessageID==messageID&&
    progress.SourceBlockNumber>0&&progress.UpdatedUnix>0&&
    progress.UpdatedUnix<=time.Now().Unix()+60&&
    time.Since(time.Unix(progress.UpdatedUnix,0))<24*time.Hour&&
    (progress.Status=="PARTIAL_QUORUM"||
     progress.Status=="RETRYING_VALIDATION"||
     progress.Status=="AVAILABLE_UNVERIFIED"){
   return &ilnQuorumStatusRPC{
    Route:route,MessageID:messageID,SourceBlockNumber:progress.SourceBlockNumber,
    Status:progress.Status,Votes:progress.Votes,
    QuorumThreshold:progress.QuorumThreshold,
    FailureCategory:progress.FailureCategory,UpdatedUnix:progress.UpdatedUnix,
   },nil
  }
 }
 raw,err:=os.ReadFile(filepath.Join(x.dataDir,"interchain","iln","quorum-requests",route,messageID+".json"))
 if err!=nil{return &ilnQuorumStatusRPC{Route:route,MessageID:messageID,Status:"NOT_FOUND"},nil}
 if info,statErr:=os.Stat(filepath.Join(x.dataDir,"interchain","iln","quorum-requests",route,messageID+".json"));statErr==nil && time.Since(info.ModTime())>ilnQuorumHintLease {
  return &ilnQuorumStatusRPC{Route:route,MessageID:messageID,Status:"EXPIRED_RETRY"},nil
 }
 var req ilnQuorumRequestRPC
 if err=json.Unmarshal(raw,&req);err!=nil{return nil,fmt.Errorf("invalid persisted ILN request")}
 if req.Route!=route||req.MessageID!=messageID {return nil,fmt.Errorf("invalid persisted ILN request identity")}
 return &ilnQuorumStatusRPC{Route:route,MessageID:messageID,SourceBlockNumber:req.SourceBlockNumber,Status:"PENDING"},nil
}

 
// GetILNQuorumAttestation retrieves the complete, immutable quorum for an
// explicitly requested historical/current destination validator set.
// Callers must compare setId with the live destination registry before process.
func (x *xgrEndpoint) GetILNQuorumAttestation(route, messageID string, setID uint64) (*interchainAttestationRPC, error) {
 normalized,err:=normalizeInterchainRPCChain(route)
 if err!=nil{return nil,err}
 id,err:=normalizedILNMessageID(messageID)
 if err!=nil{return nil,err}
 if setID==0{return nil,fmt.Errorf("ILN destination set ID required")}
 if x==nil||strings.TrimSpace(x.dataDir)==""{return nil,fmt.Errorf("ILN attestation storage unavailable")}
 path:=filepath.Join(x.dataDir,"interchain","attestations",normalized,id,fmt.Sprintf("%d.json",setID))
 out,err:=x.readInterchainAttestation(path)
 if err!=nil{return nil,err}
 if out.SetID!=setID||strings.ToLower(out.AuthorizedMessageID)!=id||strings.ToLower(out.Chain)!=normalized{
  return nil,fmt.Errorf("ILN quorum identity/set mismatch")
 }
 return out,nil
}
