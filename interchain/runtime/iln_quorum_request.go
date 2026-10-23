package runtime

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strings"
 "encoding/hex"
 "time"

 evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

type requestedILNQuorum struct {
 Route string `json:"route"`
 MessageID string `json:"messageId"`
 SourceBlockNumber uint64 `json:"sourceBlockNumber"`
 RouteID string `json:"routeId,omitempty"`
 SourceDomain uint32 `json:"sourceDomain,omitempty"`
 DestinationDomain uint32 `json:"destinationDomain,omitempty"`
}


// Validator-local diagnostic state, not authority to sign a message. The
// caller's request is never itself a vote or a custody operation.
type ilnQuorumProgress struct {
 Version int `json:"version"`
 Route string `json:"route"`
 MessageID string `json:"messageId"`
 SourceBlockNumber uint64 `json:"sourceBlockNumber"`
 Status string `json:"status"`
 Votes int `json:"votes"`
 QuorumThreshold int `json:"quorumThreshold"`
 FailureCategory string `json:"failureCategory,omitempty"`
 UpdatedUnix int64 `json:"updatedUnix"`
}
func ilnQuorumErrorCategory(err error) string {
 if err==nil{return ""}
 s:=strings.ToLower(err.Error())
 switch {
 case strings.Contains(s,"fee vault")||strings.Contains(s,"fee allocation"):
  return "SOURCE_FEE_NOT_VERIFIED"
 case strings.Contains(s,"source receipt")||strings.Contains(s,"dispatch")||strings.Contains(s,"receipt"):
  return "SOURCE_RECEIPT_NOT_VERIFIED"
 case strings.Contains(s,"rpc")||strings.Contains(s,"eth_getlogs")||
      strings.Contains(s,"get canonical source receipt")||strings.Contains(s,"chain id mismatch"):
  return "CHAIN_RPC_UNAVAILABLE"
 case strings.Contains(s,"signer")||strings.Contains(s,"bls")||
      strings.Contains(s,"validator set"):
  return "SIGNER_OR_VALIDATOR_SET"
 case strings.Contains(s,"route")||strings.Contains(s,"gateway")||
      strings.Contains(s,"operation")||strings.Contains(s,"dispatch"):
  return "SOURCE_ROUTE_OR_DISPATCH"
 case strings.Contains(s,"delivered")||strings.Contains(s,"destination"):
  return "DESTINATION_DELIVERY_CHECK"
 default:return "ILN_VALIDATION_PENDING"
 }
}
func (w *Worker) ilnQuorumProgressPath(route,messageID string) string {
 return filepath.Join(w.ilnDir(),"quorum-status",route,messageID+".json")
}
func (w *Worker) recordILNQuorumProgress(progress ilnQuorumProgress) error {
 if w==nil||w.dataDir==""||progress.SourceBlockNumber==0||
    len(progress.MessageID)!=66||!strings.HasPrefix(progress.MessageID,"0x")||
    strings.ContainsAny(progress.Route,"/\\")||progress.Route==""{
  return fmt.Errorf("invalid ILN quorum progress identity")
 }
 for _,c:=range progress.MessageID[2:]{
  if (c<'0'||c>'9')&&(c<'a'||c>'f'){return fmt.Errorf("invalid ILN quorum progress message")}
 }
 progress.Version=1
 progress.UpdatedUnix=time.Now().Unix()
 path:=w.ilnQuorumProgressPath(progress.Route,progress.MessageID)
 dir:=filepath.Dir(path)
 if err:=os.MkdirAll(dir,0o770);err!=nil{return err}
 f,err:=os.CreateTemp(dir,".quorum-status-*")
 if err!=nil{return err}
 defer os.Remove(f.Name())
 if err=f.Chmod(0o660);err!=nil{f.Close();return err}
 raw,err:=json.Marshal(progress)
 if err!=nil{f.Close();return err}
 if _,err=f.Write(raw);err!=nil{f.Close();return err}
 if err=f.Sync();err!=nil{f.Close();return err}
 if err=f.Close();err!=nil{return err}
 return os.Rename(f.Name(),path)
}
func (w *Worker) trackILNQuorumRetry(route,message string,block uint64,err error) {
 if block==0{return}
 progress:=ilnQuorumProgress{
  Route:route,MessageID:message,SourceBlockNumber:block,
  Status:"RETRYING_VALIDATION",FailureCategory:ilnQuorumErrorCategory(err),
 }
 // A valid locally held vote must not be overwritten by a transient source
 // error on another goroutine. Keep existing nonzero signature counts.
 if raw,readErr:=os.ReadFile(w.ilnQuorumProgressPath(route,message));readErr==nil{
  var previous ilnQuorumProgress
  if json.Unmarshal(raw,&previous)==nil&&previous.MessageID==message&&
     previous.SourceBlockNumber==block {
   if previous.Votes>0{
    progress.Votes=previous.Votes
    progress.QuorumThreshold=previous.QuorumThreshold
   }
  }
 }
 if saveErr:=w.recordILNQuorumProgress(progress);saveErr!=nil{
  w.logger.Warn("ILN quorum retry status persistence failed","route",route,"err",saveErr)
 }
}
const maxILNRequestPerTick = 8
// Public hints contain no funds or custody evidence. After expiry the client
// can safely request a replacement for the ORIGINAL message.
const maxILNQuorumHintAge = 15 * time.Minute

// processRequestedILNQuorums uses the EXACT same canonical operation signing
// path as automatic scans. The RPC request itself never supplies signer payload.
func (w *Worker) processRequestedILNQuorums() {
 processed:=0
 w.mu.Lock()
 routes:=make(map[string]*evmInterchain.CheckpointRoute,len(w.routes))
 for name,route:=range w.routes{routes[name]=route}
 w.mu.Unlock()
 for routeName,route:=range routes {
  if processed>=maxILNRequestPerTick {break}
  if route==nil {continue}
  dir:=filepath.Join(w.ilnDir(),"quorum-requests",routeName)
  entries,err:=os.ReadDir(dir)
  if os.IsNotExist(err){continue}
  if err!=nil{w.logger.Warn("read ILN quorum request dir", "err",err);continue}
  for _,entry:=range entries {
   if processed>=maxILNRequestPerTick {break}
   if entry.IsDir() || !strings.HasSuffix(entry.Name(),".json"){continue}
   path:=filepath.Join(dir,entry.Name())
   info,statErr:=entry.Info()
   if statErr==nil && time.Since(info.ModTime())>maxILNQuorumHintAge {
    _=os.Remove(path)
    continue
   }
   raw,err:=os.ReadFile(path)
   if err!=nil{continue}
   if len(raw)>2048{ _=os.Remove(path);continue }
   var request requestedILNQuorum
   if json.Unmarshal(raw,&request)!=nil||request.Route!=routeName||request.SourceBlockNumber==0 {
    w.logger.Warn("invalid ILN quorum request", "route",routeName)
    _=os.Remove(path)
    continue
   }
   messageID:=strings.TrimSuffix(entry.Name(),".json")
   if len(messageID)!=66||request.MessageID!=messageID { _=os.Remove(path);continue }
   invalid:=false
   for _,ch:=range messageID[2:] {if (ch<'0'||ch>'9')&&(ch<'a'||ch>'f'){invalid=true;break}}
   if invalid { _=os.Remove(path);continue }
   processed++
   if err:=w.processRequestedILNQuorum(route,types.StringToHash(messageID),request.SourceBlockNumber);err!=nil {
    w.logger.Debug("ILN quorum request pending validation", "route",routeName,"message",messageID,"err",err)
    w.trackILNQuorumRetry(routeName,messageID,request.SourceBlockNumber,err)
    msg:=strings.ToLower(err.Error())
    // Only conclusive source-evidence failures are terminal. RPC failures
    // remain queued so that an unavailable chain cannot strand valid funds.
    if strings.Contains(msg,"not found at source block") ||
       strings.Contains(msg,"lacks unique matching gateway") ||
       strings.Contains(msg,"already delivered") {
      _=os.Remove(path)
    }
    continue // temporary failures remain retryable.
   }
   // Propagate only after full local authorization. Do not discard a hint
   // until gossip accepts it; every peer re-verifies all source evidence.
   gossip:=request
   source:=w.destinations[route.SourceNetwork]
   destination:=w.destinations[route.Destination]
   if source==nil||destination==nil {
    w.trackILNQuorumRetry(routeName,messageID,request.SourceBlockNumber,
     fmt.Errorf("route source or destination unavailable"))
    continue
   }
   gossip.RouteID=route.RouteID.String()
   gossip.SourceDomain=source.Domain
   gossip.DestinationDomain=destination.Domain
   if err:=w.publish(wireEnvelope{Type:"iln_quorum_request",ILNQuorumRequest:&gossip});err!=nil {
    w.logger.Warn("ILN verified quorum request gossip retry","route",routeName,"message",messageID,"err",err)
    w.trackILNQuorumRetry(routeName,messageID,request.SourceBlockNumber,err)
    continue
   }
   // A locally signed vote survives in durable storage until quorum. Gossip
   // acceptance removes only this redundant request admission hint.
   if err:=os.Remove(path);err!=nil&&!os.IsNotExist(err){
    w.logger.Warn("ILN processed quorum hint cleanup failed","route",routeName,"err",err)
   }
  }
 }
}

func (w *Worker) processRequestedILNQuorum(route *evmInterchain.CheckpointRoute,messageID types.Hash,sourceBlock uint64) error {
    // A public RPC hint is neither proof nor an order to sign. The supplied
    // transaction block is only a SOURCE RPC locator. Independent
    // canonical Gateway/Dispatch/fee receipts, current destination ISM and
    // current BLS validator set authorize the vote, never the public hint.
    if route==nil||sourceBlock==0||messageID==types.ZeroHash{
        return fmt.Errorf("invalid public ILN quorum request")
    }
    source:=w.destinations[route.SourceNetwork]
    if source==nil{return fmt.Errorf("ILN source unavailable")}
    confirmed,err:=getConfirmedILNHead(source)
    if err!=nil{return err}
    if sourceBlock>confirmed{return fmt.Errorf("hint source transaction is not confirmed")}
    return w.signDirectILNMessage(route,messageID,sourceBlock)
}

func (w *Worker) acceptRequestedILNQuorum(request requestedILNQuorum) error {
 w.mu.Lock()
 route:=w.routes[request.Route]
 if route==nil&&request.RouteID!=""&&request.SourceDomain!=0&&request.DestinationDomain!=0{
  for _,candidate:=range w.routes {
   source:=w.destinations[candidate.SourceNetwork]
   dest:=w.destinations[candidate.Destination]
   if source!=nil&&dest!=nil&&candidate.RouteID.String()==request.RouteID&&
      source.Domain==request.SourceDomain&&dest.Domain==request.DestinationDomain{
     route=candidate
     break
   }
  }
 }
 w.mu.Unlock()
 if route==nil||request.SourceBlockNumber==0||len(request.MessageID)!=66||!strings.HasPrefix(request.MessageID,"0x"){
  return fmt.Errorf("invalid peer ILN quorum request")
 }
 if request.RouteID!=""&&(route.RouteID.String()!=request.RouteID){
  return fmt.Errorf("peer ILN quorum route identity mismatch")
 }
 bytes,err:=hex.DecodeString(request.MessageID[2:])
 if err!=nil||len(bytes)!=32 {return fmt.Errorf("invalid peer ILN message ID")}
 messageID:=types.BytesToHash(bytes)
 if messageID==types.ZeroHash{return fmt.Errorf("empty peer ILN message ID")}
 // A peer's request never authorizes arbitrary signing. It is resolved through
 // exactly the same source event, fee, set and delivered guards as local RPC.
 return w.processRequestedILNQuorum(route,messageID,request.SourceBlockNumber)
}

func (w *Worker) quorumHintLoop() {
 defer w.wg.Done()
 ticker:=time.NewTicker(5*time.Second)
 defer ticker.Stop()
 for {
  select {
  case <-w.closeCh:return
  case <-ticker.C:w.processRequestedILNQuorums()
  }
 }
}
