package runtime

import (
 "encoding/hex"
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strings"

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

var getConfirmedILNRouteAdds = evmInterchain.GetConfirmedILNRouteAdds
var getILNRegistryActivationBlock = evmInterchain.GetILNRegistryActivationBlock

func (w *Worker) ilnRouteCatalogDir(source *evmInterchain.Destination) string {
 return filepath.Join(w.ilnDir(),"route-catalog",source.Name)
}

func (w *Worker) ensureILNRouteCatalogVersion(source *evmInterchain.Destination) error {
 dir:=w.ilnRouteCatalogDir(source)
 if err:=os.MkdirAll(dir,0o770);err!=nil{return err}
 marker:=filepath.Join(dir,".v314")
 if _,err:=os.Stat(marker);err==nil{return nil}else if !os.IsNotExist(err){return err}
 // Older v3.1.4 builds advanced the discovery cursor without storing the
 // route list, and silently skipped unknown destinations. Backfill ONCE from
 // the on-chain registry deployment rather than trusting that old cursor.
 cursor:=w.ilnCursorPath("_discovery_"+source.Name)
 if err:=os.Remove(cursor);err!=nil&&!os.IsNotExist(err){return err}
 return os.WriteFile(marker,[]byte("route-event-catalog-v1\n"),0o660)
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
 snapshot,err:=getILNRouteAtBlock(source,destination.Domain,routeID,confirmedBlock)
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

func (w *Worker) discoverILNRoutes() {
 for _,source:=range w.destinations {
  if source==nil||strings.TrimSpace(source.ILNRegistryAddress)==""{continue}
  if err:=w.discoverILNRoutesFromSource(source);err!=nil {
   w.logger.Debug("ILN registry route discovery pending","chain",source.Name,"err",err)
  }
 }
}

func (w *Worker) discoverILNRoutesFromSource(source *evmInterchain.Destination) error {
 head,err:=getConfirmedILNHead(source)
 if err!=nil{return err}
 if err:=w.ensureILNRouteCatalogVersion(source);err!=nil{return err}
 if err:=w.restoreILNDiscoveredRoutes(source,head);err!=nil{return err}
 cursorName:="_discovery_"+source.Name
 cursor,exists,err:=w.readILNCursor(cursorName)
 if err!=nil{return err}
 if !exists {
  activation,err:=getILNRegistryActivationBlock(source)
  if err!=nil{return err}
  if activation==0{return fmt.Errorf("missing ILN registry deployment block")}
  cursor=activation-1
 }
 if cursor>=head{return nil}
 from,to:=cursor+1,head
 if to-from+1>ilnOperationScanChunk{to=from+ilnOperationScanChunk-1}
 events,err:=getConfirmedILNRouteAdds(source,from,to)
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
