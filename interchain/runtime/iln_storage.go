package runtime

import (
 "encoding/json"
 "encoding/hex"

 protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
 evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
 "os"
 "path/filepath"
 "strings"
 "time"

 "github.com/xgr-network/xgr-node/types"
)

const ilnSettledRetention = 7 * 24 * time.Hour
const ilnPendingQuorumCacheRetention = 30 * 24 * time.Hour
const ilnCleanupInterval = 5 * time.Minute
const ilnCleanupBudget = 64

// pruneDeliveredILNQuorums is *not* TTL-based transfer deletion.
// Only the destination Mailbox's affirmative delivered(messageId) can justify
// deleting a completed attestation and its historical set-specific snapshots.
// Pending, unreachable, invalid and unknown-route data always survive.
func (w *Worker) pruneILNQuorumCaches() {
 routesDir:=w.attestationDir()
 routeDirs,err:=os.ReadDir(routesDir);if err!=nil{return}
 budget:=ilnCleanupBudget
 for _,routeEntry:=range routeDirs {
  if budget<=0{return}
  if !routeEntry.IsDir(){continue}
  name:=routeEntry.Name()
  w.mu.Lock()
  route:=w.routes[name]
  w.mu.Unlock()
  if route==nil {continue}
  source:=w.destinations[route.SourceNetwork]
  destination:=w.destinations[route.Destination]
  if source==nil||destination==nil {continue}
  dir:=filepath.Join(routesDir,name)
  entries,err:=os.ReadDir(dir);if err!=nil{continue}
  if len(entries)==0{continue}
  // Rotate the bounded candidate window hourly. Otherwise a small number
  // of old unsettled transfers could permanently starve later archives.
  start:=int(((time.Now().Unix()/3600)*int64(ilnCleanupBudget))%int64(len(entries)))
  for offset:=0;offset<len(entries);offset++ {
   if budget<=0{return}
   entry:=entries[(start+offset)%len(entries)]
   if !entry.IsDir(){continue}
   messageID:=entry.Name()
   if len(messageID)!=66||!strings.HasPrefix(messageID,"0x"){continue}
   hash:=types.StringToHash(messageID)
   if hash==types.ZeroHash||hash.String()!=messageID{continue}
   info,err:=entry.Info()
   if err!=nil {continue}
   aliasPath:=filepath.Join(dir,messageID+".json")
   aliasInfo,err:=os.Stat(aliasPath)
   if err!=nil{continue}
   newest:=info.ModTime()
   if aliasInfo.ModTime().After(newest){newest=aliasInfo.ModTime()}
   age:=time.Since(newest)
   if age<ilnSettledRetention{continue}
   budget--
   // The immutable source registry route tuple includes the canonical
   // destination router. Never trust a local filename or arbitrary caller.
   snapshot,err:=getConfirmedILNRoute(source,destination.Domain,route.RouteID)
   if err!=nil{
    // Disabled routes still have pending historical messages: use the
    // historical source block from a persisted signer-approved attestation.
    raw,readErr:=os.ReadFile(filepath.Join(dir,messageID+".json"))
    if readErr!=nil{continue}
    var a ilnCheckpointAttestation
    if json.Unmarshal(raw,&a)!=nil||!strings.EqualFold(a.AuthorizedMessageID,messageID)||a.SourceBlockNumber==0{continue}
    snapshot,err=getILNRouteAtBlock(source,destination.Domain,route.RouteID,a.SourceBlockNumber)
    if err!=nil{continue}
   }
   delivered,err:=getILNMessageDelivered(destination,snapshot.Route.DestinationRouter,hash)
   if err!=nil{continue}
   if !delivered {
    if age<ilnPendingQuorumCacheRetention||!w.canRegenerateILNQuorum(route,aliasPath,hash){continue}
   }
   // No bridge custody data is needed locally after successful on-chain
   // delivery; chain logs and receipts remain independently auditable.
   if err:=os.RemoveAll(filepath.Join(dir,messageID));err!=nil{continue}
   _=os.Remove(filepath.Join(dir,messageID+".json"))
   latestPath:=filepath.Join(dir,"latest.json")
   if raw,err:=os.ReadFile(latestPath);err==nil{
    var latest ilnCheckpointAttestation
    if json.Unmarshal(raw,&latest)==nil&&strings.EqualFold(latest.AuthorizedMessageID,messageID){
     _=os.Remove(latestPath)
    }
   }
  }
 }
}


func (w *Worker) canRegenerateILNQuorum(route *evmInterchain.CheckpointRoute,aliasPath string,messageID types.Hash)bool{
 source:=w.destinations[route.SourceNetwork]
 destination:=w.destinations[route.Destination]
 if source==nil||destination==nil{return false}
 raw,err:=os.ReadFile(aliasPath)
 if err!=nil||len(raw)>16384{return false}
 var record ilnCheckpointAttestation
 if json.Unmarshal(raw,&record)!=nil||record.Chain!=route.Name||
   record.AuthorizedMessageID!=messageID.String()||record.SourceBlockNumber==0||record.Payload==""{return false}
 packed,err:=hex.DecodeString(strings.TrimPrefix(record.Payload,"0x"))
 if err!=nil{return false}
 var payload protocol.ILNCheckpointPayload
 if payload.UnmarshalBinary(packed)!=nil||payload.RouteID!=route.RouteID||
   payload.AuthorizedMessageID!=messageID||payload.SourceBlockNumber!=record.SourceBlockNumber||
   payload.DestinationDomain!=destination.Domain{return false}
 snapshot,err:=getILNRouteAtBlock(source,destination.Domain,route.RouteID,payload.SourceBlockNumber)
 if err!=nil||snapshot==nil||snapshot.Route.Gateway!=payload.Gateway||
   snapshot.Route.DestinationRouter!=payload.DestinationRouter||snapshot.Registry!=payload.Registry{return false}
 operation,err:=getILNOperationAtBlock(source,snapshot.Route.Gateway,route.RouteID,destination.Domain,messageID,payload.SourceBlockNumber)
 if err!=nil||operation==nil||operation.ValidatorFeeWei==nil||payload.ValidatorFeeWei==nil||
   operation.ValidatorFeeWei.Cmp(payload.ValidatorFeeWei)!=0{return false}
 checkpoint,err:=getConfirmedILNCheckpoint(source,snapshot)
 if err!=nil||checkpoint==nil||checkpoint.Root!=payload.Root||checkpoint.Index!=payload.Index{return false}
 set,err:=getILNValidatorSet(destination)
 return err==nil&&set!=nil&&set.SetID>0&&len(set.Validators)>0&&len(set.BLSPublicKeys)==len(set.Validators)
}

// These are temporary governance control-plane artifacts, not pending asset
// custody proofs. Only an explicitly expired proposal + grace period is purged.
func (w *Worker) pruneExpiredILNGovernance() {
 entries,err:=os.ReadDir(w.governanceProposalDir());if err!=nil{return}
 cutoff:=uint64(time.Now().Add(-ilnSettledRetention).Unix())
 for _,entry:=range entries {
  if entry.IsDir()||!strings.HasSuffix(entry.Name(),".json"){continue}
  path:=filepath.Join(w.governanceProposalDir(),entry.Name())
  _,proposal,_,err:=readStoredGovernanceProposal(path)
  if err!=nil||proposal.ValidUntil>=cutoff{continue}
  id:=strings.TrimSuffix(entry.Name(),".json")
  _=os.Remove(path)
  _=os.Remove(filepath.Join(w.governanceQuorumDir(),id+".json"))
 }
}

func (w *Worker) storageMaintenanceLoop() {
 defer w.wg.Done()
 ticker:=time.NewTicker(ilnCleanupInterval)
 defer ticker.Stop()
 for {
  select {
  case <-w.closeCh: return
  case <-ticker.C:
   w.pruneILNQuorumCaches()
   w.pruneExpiredILNGovernance()
   w.pruneCompletedILNControlResults()
  }
 }
}

// Completed CLI results are delivery receipts for local control requests,
// not asset-custody data. Wait seven days so slow CLI readers can collect
// them, then reclaim their small JSON files. Never delete pending requests.
func (w *Worker) pruneCompletedILNControlResults() {
 for _,dir:=range []string{w.governanceResultDir(),w.resultDir()} {
  entries,err:=os.ReadDir(dir)
  if err!=nil{continue}
  removed:=0
  for _,entry:=range entries {
   if removed>=128 {break}
   if entry.IsDir()||!strings.HasSuffix(entry.Name(),".json"){continue}
   info,err:=entry.Info()
   if err!=nil||time.Since(info.ModTime())<ilnSettledRetention{continue}
   if os.Remove(filepath.Join(dir,entry.Name()))==nil{removed++}
  }
 }
}
