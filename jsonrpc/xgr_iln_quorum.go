package jsonrpc

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strings"
 "time"
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
  return nil,fmt.Errorf("route is not configured or discovered locally")
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
