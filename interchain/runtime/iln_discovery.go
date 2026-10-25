package runtime

import (
 "encoding/hex"
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "sort"
 "strings"
 "time"

 evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

// ILN route discovery is unprivileged: only the quorum-governed on-chain
// source registry grants authorization. The local catalog stores hints so a
// node restart or a newly configured destination cannot skip old RouteAdded logs.
type ilnDiscoveredRoute struct {
 SourceNetwork string `json:"sourceNetwork"`
 SourceDomain uint32 `json:"sourceDomain"`
 DestinationDomain uint32 `json:"destinationDomain"`
 RouteID string `json:"routeId"`
 Gateway string `json:"gateway"`
}

func (w *Worker) registerILNRPCRoute(route *evmInterchain.CheckpointRoute) error {
 if route==nil {return fmt.Errorf("missing ILN RPC route")}
 if err:=route.Validate();err!=nil{return err}
 dir:=filepath.Join(w.ilnDir(),"registered-routes")
 if err:=os.MkdirAll(dir,0o770);err!=nil{return err}
 path:=filepath.Join(dir,route.Name+".route")
 tmp:=path+".tmp"
 if err:=os.WriteFile(tmp,[]byte(route.RouteID.String()+"\n"),0o660);err!=nil{return err}
 return os.Rename(tmp,path)
}

// Bounded discovery scans use registry events only for bootstrap; gossip and
// confirmed on-chain getters independently validate routes for late joiners.
const ilnRouteDiscoveryScanChunk uint64 = 1000

var getConfirmedILNRouteAdds = evmInterchain.GetConfirmedILNRouteAdds
var getILNRegistryActivationBlock = evmInterchain.GetILNRegistryActivationBlock

// A new source registry is a new contract generation. Never read a former
// registry's routes when the chain name remains unchanged after a hard reset.
func (w *Worker) ilnRouteCatalogDir(source *evmInterchain.Destination) string {
 return filepath.Join(w.ilnDir(),"route-catalog",source.Name,
  strings.ToLower(source.ILNRegistryAddress))
}

// The discovery cursor is RPC-native AND bound to the exact source Registry.
// This avoids accidentally skipping new RouteAdded events after redeployment.
func ilnDiscoveryCursorName(source *evmInterchain.Destination) string {
 return "_discovery_"+source.Name+"_"+strings.TrimPrefix(strings.ToLower(source.ILNRegistryAddress),"0x")
}

func (w *Worker) ensureILNRouteCatalogVersion(source *evmInterchain.Destination) error {
 dir:=w.ilnRouteCatalogDir(source)
 if err:=os.MkdirAll(dir,0o770);err!=nil{return err}
 marker:=filepath.Join(dir,".v314")
 if _,err:=os.Stat(marker);err==nil{return nil}else if !os.IsNotExist(err){return err}
 // Older v3.1.4 builds advanced the discovery cursor without storing the
 // route list, and silently skipped unknown destinations. Backfill ONCE from
 // the on-chain registry deployment rather than trusting that old cursor.
 cursor:=w.ilnCursorPath(ilnDiscoveryCursorName(source))
 if err:=os.Remove(cursor);err!=nil&&!os.IsNotExist(err){return err}
 return os.WriteFile(marker,[]byte("route-event-catalog-v1\n"),0o660)
}

// A previous version may have persisted Solidity's L1 block.number as an
// L2 RPC cursor. Its numerical value can be below the L2 head, so comparing
// cursor <= head cannot detect the mismatch. Migrate exactly once while
// retaining and independently revalidating every durable route catalog entry.
func (w *Worker) ensureILNRPCDiscoveryCursorVersion(source *evmInterchain.Destination) error {
 dir:=w.ilnRouteCatalogDir(source)
 if err:=os.MkdirAll(dir,0o770);err!=nil{return err}
 marker:=filepath.Join(dir,".rpc-native-v324")
 if _,err:=os.Stat(marker);err==nil{return nil}else if !os.IsNotExist(err){return err}
 if err:=os.Remove(w.ilnCursorPath(ilnDiscoveryCursorName(source)));err!=nil&&!os.IsNotExist(err){return err}
 return os.WriteFile(marker,[]byte("source-rpc-height-v1\n"),0o660)
}

// On previously unknown chains, read a recent bounded window immediately.
// Earlier routes are learned via registry-verified hints, peer announcements
// and the durable catalog, never by waiting for a genesis-to-head replay.
func ilnRecentDiscoveryCursor(head uint64) uint64 {
 if head>ilnRouteDiscoveryScanChunk{return head-ilnRouteDiscoveryScanChunk}
 return 0
}

func (w *Worker) rememberILNRouteAdded(source *evmInterchain.Destination,event evmInterchain.ILNRouteAdded) error {
 if event.RouteID==types.ZeroHash||event.Gateway==types.ZeroAddress||event.DestinationDomain==0{return fmt.Errorf("invalid ILN discovery event")}
 record:=ilnDiscoveredRoute{SourceNetwork:source.Name,SourceDomain:source.Domain,DestinationDomain:event.DestinationDomain,RouteID:event.RouteID.String(),Gateway:event.Gateway.String()}
 raw,err:=json.Marshal(record);if err!=nil{return err}
 dir:=w.ilnRouteCatalogDir(source)
 if err:=os.MkdirAll(dir,0o770);err!=nil{return err}
 path:=filepath.Join(dir,fmt.Sprintf("%d-%s.json",event.DestinationDomain,strings.TrimPrefix(event.RouteID.String(),"0x")))
 if existing,err:=os.ReadFile(path);err==nil {
  var previous ilnDiscoveredRoute
  if json.Unmarshal(existing,&previous)!=nil||previous!=record{return fmt.Errorf("ILN route catalog conflicts with on-chain event for %s",record.RouteID)}
  return nil
 }else if !os.IsNotExist(err){return err}
 tmp:=path+".tmp"
 if err:=os.WriteFile(tmp,raw,0o660);err!=nil{return err}
 return os.Rename(tmp,path)
}

func (w *Worker) attachILNDiscoveredRoute(source *evmInterchain.Destination,record ilnDiscoveredRoute,confirmedBlock uint64) error {
 if record.SourceNetwork!=source.Name||record.SourceDomain!=source.Domain||record.DestinationDomain==0||len(record.RouteID)!=66||!strings.HasPrefix(record.RouteID,"0x"){
  return fmt.Errorf("invalid stored ILN route identity")
 }
 decoded,err:=hex.DecodeString(strings.TrimPrefix(record.RouteID,"0x"))
 if err!=nil||len(decoded)!=32{return fmt.Errorf("invalid stored ILN route id")}
 routeID:=types.BytesToHash(decoded)
 if routeID==types.ZeroHash{return fmt.Errorf("stored ILN route ID is zero")}
 var destination *evmInterchain.Destination
 for _,candidate:=range w.destinations{
  if candidate!=nil&&candidate.Domain==record.DestinationDomain {destination=candidate;break}
 }
 if destination==nil{return nil} // Persisted for a later chain onboarding.
 snapshot,err:=w.sourceILNRouteAtBlock(source,destination.Domain,routeID,confirmedBlock)
 if err!=nil{return fmt.Errorf("validate discovered ILN route %s: %w",record.RouteID,err)}
 if !strings.EqualFold(snapshot.Route.Gateway.String(),record.Gateway) {
  return fmt.Errorf("ILN discovered route gateway mismatch for %s",record.RouteID)
 }
 routeName:=fmt.Sprintf("iln_%d_%d_%s",source.Domain,destination.Domain,strings.TrimPrefix(record.RouteID,"0x"))
 identity:=source.Name+"|"+destination.Name+"|"+routeID.String()
 w.mu.Lock()
 var matching *evmInterchain.CheckpointRoute
 for _,existing:=range w.routes {
  if existing!=nil&&existing.SourceNetwork+"|"+existing.Destination+"|"+existing.RouteID.String()==identity{matching=existing;break}
 }
 if matching==nil {
  matching=&evmInterchain.CheckpointRoute{Name:routeName,SourceNetwork:source.Name,Destination:destination.Name,RouteID:routeID}
  w.routes[routeName]=matching
 }
 w.mu.Unlock()
 return w.registerILNRPCRoute(matching)
}

func (w *Worker) restoreILNDiscoveredRoutes(source *evmInterchain.Destination,head uint64) error {
 if w.ilnRestoredSources!=nil&&w.ilnRestoredSources[source.Name]{return nil}
 dir:=w.ilnRouteCatalogDir(source)
 entries,err:=os.ReadDir(dir)
 if err!=nil{return err}
 for _,entry:=range entries {
  if entry.IsDir()||!strings.HasSuffix(entry.Name(),".json"){continue}
  raw,err:=os.ReadFile(filepath.Join(dir,entry.Name()))
  if err!=nil{return err}
  var record ilnDiscoveredRoute
  if err:=json.Unmarshal(raw,&record);err!=nil{return fmt.Errorf("decode ILN route catalog %s: %w",entry.Name(),err)}
  if err:=w.attachILNDiscoveredRoute(source,record,head);err!=nil{return err}
 }
 if w.ilnRestoredSources==nil{w.ilnRestoredSources=make(map[string]bool)}
 w.ilnRestoredSources[source.Name]=true
 return nil
}

// An unknown permissionless route hint is untrusted discovery input, NOT a
// signing authorization. A fixed lifetime bounds unusable hints even when
// their retry mtime is rotated for fair queue processing.
const ilnRouteHintLifetime = 15*time.Minute
const ilnRouteHintsPerTick = 8
const ilnRouteHintMaxBytes = 512

type ilnRouteHintLease struct{
 IssuedUnix int64 `json:"issuedUnix"`
}

// Persist the first-seen timestamp for old pre-lease hints before rotating
// the file's mtime. Otherwise a repeated transient RPC failure could renew
// its lease forever. A torn write never unlocks route authorization.
func readILNRouteHintLease(path string,originalModTime time.Time)(int64,error){
 raw,err:=os.ReadFile(path)
 if err!=nil{return 0,err}
 if len(raw)==0||len(raw)>ilnRouteHintMaxBytes{return 0,fmt.Errorf("invalid route hint size")}
 var lease ilnRouteHintLease
 if err=json.Unmarshal(raw,&lease);err!=nil{return 0,fmt.Errorf("invalid route hint encoding: %w",err)}
 if lease.IssuedUnix!=0{return lease.IssuedUnix,nil}
 lease.IssuedUnix=originalModTime.Unix()
 // The deterministic route filename, not user-provided JSON, is the
 // discovery locator. Old metadata fields are intentionally discarded.
 encoded,err:=json.Marshal(lease)
 if err!=nil{return 0,err}
 dir:=filepath.Dir(path)
 file,err:=os.CreateTemp(dir,".route-hint-lease-*")
 if err!=nil{return 0,err}
 defer os.Remove(file.Name())
 if err=file.Chmod(0o660);err!=nil{file.Close();return 0,err}
 if _,err=file.Write(encoded);err!=nil{file.Close();return 0,err}
 if err=file.Sync();err!=nil{file.Close();return 0,err}
 if err=file.Close();err!=nil{return 0,err}
 if err=os.Rename(file.Name(),path);err!=nil{return 0,err}
 d,err:=os.Open(dir)
 if err!=nil{return 0,err}
 syncErr:=d.Sync()
 closeErr:=d.Close()
 if syncErr!=nil{return 0,syncErr}
 if closeErr!=nil{return 0,closeErr}
 return lease.IssuedUnix,nil
}

// Resolve bounded user hints against the independently confirmed source
// registry. No route file or signing cursor is created for an invalid hint.
// In particular, this path never interprets Solidity block.number as an RPC
// block number, and never scans from an L1 height on an L2.
func (w *Worker) discoverILNHintedRoutes() {
 dir:=filepath.Join(w.ilnDir(),"route-hints")
 entries,err:=os.ReadDir(dir)
 if os.IsNotExist(err){return}
 if err!=nil{w.logger.Debug("ILN route hint directory unavailable","err",err);return}
 // Rotate failed files to the back of this bounded worklist, instead of
 // repeatedly choosing the same eight alphabetic names forever.
 type queuedHint struct{name string;modTime time.Time}
 candidates:=make([]queuedHint,0,len(entries))
 for _,entry:=range entries{
  if entry.IsDir()||!strings.HasSuffix(entry.Name(),".json"){continue}
  info,e:=entry.Info()
  if e!=nil{continue}
  candidates=append(candidates,queuedHint{name:entry.Name(),modTime:info.ModTime()})
 }
 sort.Slice(candidates,func(i,j int)bool{
  if candidates[i].modTime.Equal(candidates[j].modTime){return candidates[i].name<candidates[j].name}
  return candidates[i].modTime.Before(candidates[j].modTime)
 })
 processed:=0
 now:=time.Now()
 for _,candidate:=range candidates{
  if processed>=ilnRouteHintsPerTick{break}
  processed++
  path:=filepath.Join(dir,candidate.name)
  issued,e:=readILNRouteHintLease(path,candidate.modTime)
  if e!=nil{
   // Corrupt hints are not recoverable source evidence.
   _=os.Remove(path);continue
  }
  if issued>now.Add(time.Minute).Unix()||
   now.Sub(time.Unix(issued,0))>=ilnRouteHintLifetime{
   _=os.Remove(path);continue
  }
  retry:=func(){_ = os.Chtimes(path,now,now)}
  routeName:=strings.TrimSuffix(candidate.name,".json")
  sourceDomain,destinationDomain,routeID,e:=evmInterchain.ParseILNRouteName(routeName)
  if e!=nil{_ = os.Remove(path);continue}
  var source *evmInterchain.Destination
  for _,v:=range w.destinations{
   if v!=nil&&v.Domain==sourceDomain&&strings.TrimSpace(v.ILNRegistryAddress)!=""{source=v;break}
  }
  // Unknown source/destination pairs are not configured here. Remove
  // permanently invalid hints rather than filling the public queue.
  var destinationConfigured bool
  for _,v:=range w.destinations{
   if v!=nil&&v.Domain==destinationDomain{destinationConfigured=true;break}
  }
  if source==nil||!destinationConfigured{_ = os.Remove(path);continue}
  head,e:=w.sourceILNHead(source)
  if e!=nil{retry();continue}
  snapshot,e:=w.sourceILNRouteAtBlock(source,destinationDomain,routeID,head)
  if e!=nil{retry();continue} // outage is not proof of a bogus route
  if !validILNSourceRouteNotice(source,destinationDomain,routeID,snapshot){
   _=os.Remove(path);continue // conclusive noncanonical route
  }
  record:=ilnDiscoveredRoute{SourceNetwork:source.Name,SourceDomain:sourceDomain,
   DestinationDomain:destinationDomain,RouteID:routeID.String(),Gateway:snapshot.Route.Gateway.String()}
  if e=w.rememberILNRouteAdded(source,evmInterchain.ILNRouteAdded{
   DestinationDomain:destinationDomain,RouteID:routeID,Gateway:snapshot.Route.Gateway,BlockNumber:head,
  });e!=nil{retry();continue}
  if e=w.attachILNDiscoveredRoute(source,record,head);e!=nil{retry();continue}
  _=os.Remove(path)
 }
}

func (w *Worker) discoverILNRoutes() {
 w.discoverILNHintedRoutes()
 for _,source:=range w.destinations {
  if source==nil||strings.TrimSpace(source.ILNRegistryAddress)==""{continue}
  if err:=w.discoverILNRoutesFromSource(source);err!=nil {
   w.logger.Debug("ILN registry route discovery pending","chain",source.Name,"err",err)
  }
 }
}

func (w *Worker) discoverILNRoutesFromSource(source *evmInterchain.Destination) error {
 head,err:=w.sourceILNHead(source)
 if err!=nil{return err}
 if err:=w.ensureILNRouteCatalogVersion(source);err!=nil{return err}
 if err:=w.ensureILNRPCDiscoveryCursorVersion(source);err!=nil{return err}
 if err:=w.restoreILNDiscoveredRoutes(source,head);err!=nil{return err}
 cursorName:=ilnDiscoveryCursorName(source)
 cursor,exists,err:=w.readILNCursor(cursorName)
 if err!=nil{return err}
 if !exists {
  if source.ILNRegistryActivationBlock!=0 {
   // Explicit operator configuration is an RPC-native deployment height.
   activation,err:=w.sourceILNActivation(source)
   if err!=nil{return err}
   if activation==0||activation>head{return fmt.Errorf("invalid RPC-native ILN registry deployment block")}
   cursor=activation-1
  } else {
   cursor=ilnRecentDiscoveryCursor(head)
  }
 }
 if cursor>head{
  // A restored cursor above the confirmed RPC head is not a valid scan.
  cursor=ilnRecentDiscoveryCursor(head)
 }
 if cursor==head{return nil}
 from,to:=cursor+1,head
 if to-from+1>ilnRouteDiscoveryScanChunk{to=from+ilnRouteDiscoveryScanChunk-1}
 events,err:=w.sourceILNRouteAdds(source,from,to)
 if err!=nil{return err}
 for _,event:=range events {
  // Durable before the discovery cursor advances. Unknown destinations are
  // retained and independently validated when their chain gets configured.
  if err:=w.rememberILNRouteAdded(source,event);err!=nil{return err}
  record:=ilnDiscoveredRoute{SourceNetwork:source.Name,SourceDomain:source.Domain,DestinationDomain:event.DestinationDomain,RouteID:event.RouteID.String(),Gateway:event.Gateway.String()}
  if err:=w.attachILNDiscoveredRoute(source,record,to);err!=nil{return err}
 }
 return w.writeILNCursor(cursorName,to)
}
