package runtime

import (
    "fmt"
    "sort"
    "strings"
    "time"

    "github.com/libp2p/go-libp2p/core/peer"
    "github.com/xgr-network/xgr-node/interchain/evm"
    "github.com/xgr-network/xgr-node/types"
)

// Route notices are discovery hints, NEVER authority. A connected peer can
// announce any proposed route; the receiver has to read its own confirmed
// source XGRILNRegistry and independently verify gateway/route bindings.
type ilnRouteNoticeWire struct {
    SourceNetwork string `json:"sourceNetwork"`
    SourceDomain uint32 `json:"sourceDomain"`
    DestinationDomain uint32 `json:"destinationDomain"`
    RouteID types.Hash `json:"routeId"`
    Gateway types.Address `json:"gateway"`
}

const ilnRouteNoticeFreshness = 15 * time.Minute

type verifiedILNNotice struct {
    snapshot *evm.ILNRouteSnapshot
    checkedAt time.Time
}

// The cached result is only used for discovery gossip. Transfer verification
// and signing always read independent source witnesses through their own path.
func (w *Worker) routeForILNNotice(source *evm.Destination, domain uint32, routeID types.Hash) (*evm.ILNRouteSnapshot, error) {
    key := fmt.Sprintf("%s/%s/%d/%s", source.Name, source.ILNRegistryAddress, domain, routeID.String())
    value, err, _ := w.ilnRouteNoticeChecks.Do(key, func() (interface{}, error) {
        w.mu.Lock()
        cached := w.ilnRouteNoticeCache[key]
        w.mu.Unlock()
        if cached.snapshot != nil && time.Since(cached.checkedAt) < ilnRouteNoticeFreshness {
            return cached.snapshot, nil
        }
        snapshot, err := w.sourceILNRoute(source, domain, routeID)
        if err != nil {
            return nil, err
        }
        if !validILNSourceRouteNotice(source, domain, routeID, snapshot) {
            return nil, fmt.Errorf("route not authorized by canonical source registry")
        }
        w.mu.Lock()
        if w.ilnRouteNoticeCache == nil {
            w.ilnRouteNoticeCache = make(map[string]verifiedILNNotice)
        }
        w.ilnRouteNoticeCache[key] = verifiedILNNotice{snapshot: snapshot, checkedAt: time.Now()}
        w.mu.Unlock()
        return snapshot, nil
    })
    if err != nil {
        return nil, err
    }
    return value.(*evm.ILNRouteSnapshot), nil
}

// Only advertise routes that this validator can itself read from the source
// registry. Do not broadcast unverified route-catalog disk files and never
// advertise a permissioned admission vote. Each route is retried periodically
// to help late-joining nodes even if historical RouteAdded logs are pruned.
func (w *Worker) publishVerifiedILNRoutes() {
    if w.network==nil||w.topic==nil{return}
    w.mu.Lock()
    routes:=make([]*evm.CheckpointRoute,0,len(w.routes))
    for _,route:=range w.routes{routes=append(routes,route)}
    w.mu.Unlock()
    sort.Slice(routes,func(i,j int)bool{return routes[i].Name<routes[j].Name})
    const maxNoticesPerTick=16
    published:=0
    for _,route:=range routes {
        if published>=maxNoticesPerTick{break}
        if route==nil {continue}
        source:=w.destinations[route.SourceNetwork]
        dest:=w.destinations[route.Destination]
        if source==nil||dest==nil||source.ChainID==0||source.Domain==0||
            source.ILNRegistryAddress==""{continue}
        key:=fmt.Sprintf("%d/%d/%s",source.Domain,dest.Domain,route.RouteID.String())
        w.mu.Lock()
        last:=w.ilnRouteLastPublished[key]
        w.mu.Unlock()
        if !last.IsZero()&&time.Since(last)<90*time.Second{continue}
        snapshot,err:=w.routeForILNNotice(source,dest.Domain,route.RouteID)
        if err!=nil{continue}
        notice:=ilnRouteNoticeWire{
            SourceNetwork:source.Name,SourceDomain:source.Domain,
            DestinationDomain:dest.Domain,RouteID:route.RouteID,
            Gateway:snapshot.Route.Gateway,
        }
        if err:=w.publish(wireEnvelope{Type:"iln_route_notice",ILNRouteNotice:&notice});err!=nil{
            continue
        }
        w.mu.Lock()
        if w.ilnRouteLastPublished==nil{w.ilnRouteLastPublished=make(map[string]time.Time)}
        w.ilnRouteLastPublished[key]=time.Now()
        w.mu.Unlock()
        published++
    }
}
func validILNSourceRouteNotice(source *evm.Destination,domain uint32,id types.Hash,snapshot *evm.ILNRouteSnapshot)bool{
    if source==nil||snapshot==nil||id==types.ZeroHash||domain==0||
        snapshot.Registry!=types.StringToAddress(source.ILNRegistryAddress) ||
        !snapshot.Route.Enabled ||
        snapshot.Route.Key.SourceChainID!=source.ChainID ||
        snapshot.Route.Key.SourceDomain!=source.Domain ||
        snapshot.Route.Key.DestinationDomain!=domain ||
        snapshot.Route.Key.RouteID!=id ||
        snapshot.Route.Gateway==types.ZeroAddress ||
        snapshot.Route.SourceRouter==types.ZeroAddress ||
        snapshot.Route.Mailbox==types.ZeroAddress ||
        snapshot.Route.DispatchHook==types.ZeroAddress ||
        snapshot.Route.DestinationRouter==types.ZeroAddress ||
        snapshot.Route.ValidatorFeeWei==nil||
        snapshot.Route.ValidatorFeeWei.Sign()<=0 {
        return false
    }
    return true
}
func (w *Worker) acceptVerifiedILNRouteNotice(from peer.ID,notice ilnRouteNoticeWire)error{
    if from==""||w.network==nil||!w.network.IsConnected(from) {
        return fmt.Errorf("ILN route notice must originate from a connected authenticated peer")
    }
    if len(notice.SourceNetwork)==0||len(notice.SourceNetwork)>64||
        strings.ContainsAny(notice.SourceNetwork,"/\\") ||
        notice.SourceDomain==0||notice.DestinationDomain==0||
        notice.RouteID==types.ZeroHash||notice.Gateway==types.ZeroAddress {
        return fmt.Errorf("invalid ILN route notice identity")
    }
    source:=w.destinations[notice.SourceNetwork]
    if source==nil||source.Domain!=notice.SourceDomain||
        source.ILNRegistryAddress==""{
        return fmt.Errorf("unknown source registry for peer route notice")
    }
    // Rate limit all failed and successful notices BEFORE external RPC reads.
    // Untrusted peers cannot force unlimited expensive Base registry lookups.
    w.mu.Lock()
    if w.ilnRouteLastSeen==nil{w.ilnRouteLastSeen=make(map[peer.ID]time.Time)}
    if time.Since(w.ilnRouteLastSeen[from])<250*time.Millisecond{
        w.mu.Unlock()
        return fmt.Errorf("rate limited ILN peer route announcement")
    }
    w.ilnRouteLastSeen[from]=time.Now()
    w.mu.Unlock()
    snapshot,err:=w.routeForILNNotice(source,notice.DestinationDomain,notice.RouteID)
    if err!=nil{return fmt.Errorf("verify announced route with source registry: %w",err)}
    if notice.Gateway!=snapshot.Route.Gateway{
        return fmt.Errorf("peer route announcement not authorized by canonical source registry")
    }
    // Receiving an authentic listed route is permissionless and independent
    // of the XITA GmbH UI, an admission vote, or a relayer's own index.
    event:=evm.ILNRouteAdded{
        DestinationDomain:notice.DestinationDomain,
        RouteID:notice.RouteID,
        Gateway:notice.Gateway,
        BlockNumber:snapshot.BlockNumber,
    }
    if err:=w.rememberILNRouteAdded(source,event);err!=nil{return err}
    destination := w.ilnDestinationByDomain(notice.DestinationDomain)
    return w.attachVerifiedILNDiscoveredRoute(source, destination, notice.RouteID)
}
