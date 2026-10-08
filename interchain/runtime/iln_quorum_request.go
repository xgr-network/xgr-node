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
   // A successful authorization leaves either a pending signed vote or
   // a completed quorum. Request is no longer needed for admission.
   _=os.Remove(path)
   // Propagate only after local canonical source and destination checks pass.
   // Every receiving validator independently verifies the same source event.
   gossip:=request
   source:=w.destinations[route.SourceNetwork]
   destination:=w.destinations[route.Destination]
   if source!=nil&&destination!=nil {
    gossip.RouteID=route.RouteID.String()
    gossip.SourceDomain=source.Domain
    gossip.DestinationDomain=destination.Domain
    _=w.publish(wireEnvelope{Type:"iln_quorum_request",ILNQuorumRequest:&gossip})
   }
  }
 }
}

func (w *Worker) processRequestedILNQuorum(route *evmInterchain.CheckpointRoute,messageID types.Hash,sourceBlock uint64) error {
 source:=w.destinations[route.SourceNetwork]
 destination:=w.destinations[route.Destination]
 if source==nil||destination==nil{return fmt.Errorf("route network unavailable")}
 snapshot,err:=getILNRouteAtBlock(source,destination.Domain,route.RouteID,sourceBlock)
 if err!=nil{return err}
 // Never infer eligibility from a caller's request; read the exact fee-paid
 // Gateway event from its confirmed block.
 operation,err:=getILNOperationAtBlock(source,snapshot.Route.Gateway,route.RouteID,destination.Domain,messageID,sourceBlock)
 if err!=nil{return err}
 currentSet,err:=getILNValidatorSet(destination)
 if err!=nil{return err}
 eligible,err:=w.interchainSignerEligible(currentSet,w.localAddr)
 if err!=nil{return err}
 if !eligible{
  // Public RPC may run on a non-validator. It can still forward an authentic
  // request, but MUST prove source fee, route, and undelivered destination.
  if operation.ValidatorFeeWei==nil||operation.ValidatorFeeWei.Sign()<=0||snapshot.Route.ValidatorFeeWei==nil{return fmt.Errorf("invalid canonical ILN fee")}
  delivered,checkErr:=getILNMessageDelivered(destination,snapshot.Route.DestinationRouter,messageID)
  if checkErr!=nil{return checkErr}
  if delivered{return fmt.Errorf("ILN message already delivered")}
  return nil
 }
 return w.signILNOperation(route,source,destination,currentSet,*operation)
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
