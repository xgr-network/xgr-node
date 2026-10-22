package runtime

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "strconv"
    "strings"
    "time"

    "github.com/libp2p/go-libp2p/core/peer"

    evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
    "github.com/xgr-network/xgr-node/types"
)

// ILN Merkle leaves are public Hyperlane message hashes. This protocol moves
// UNTRUSTED bytes between authenticated libp2p peers, never signing keys.
// Each assembled prefix must independently equal a confirmed source Hook root.
const ilnReplicaWireLeaves = 256
const ilnReplicaMaxPeers = 3
const ilnReplicaMaxFileBytes = 40 << 20

type ilnReplicaRequestWire struct {
    ChainID uint64 `json:"chainId"`
    Hook types.Address `json:"hook"`
    Root types.Hash `json:"root"`
    CheckpointBlock uint64 `json:"checkpointBlock"`
    Count uint64 `json:"count"`
    Offset uint64 `json:"offset"`
    Recipient string `json:"recipient"`
}
type ilnReplicaChunkWire struct {
    ilnReplicaRequestWire
    Target string `json:"target"`
    Leaves []types.Hash `json:"leaves"`
}
type ilnReplicaPeerAttempt struct {
    leaves []types.Hash
    lastRequested time.Time
    requestedOffset uint64
    blockedUntil time.Time
}
type ilnReplicaSyncState struct {
    canonical evmInterchain.ILNReplicaCheckpoint
    prefix evmInterchain.ILNReplica
    attempts map[peer.ID]*ilnReplicaPeerAttempt
    lastRecentTry time.Time
    nextPeer uint64
}
func sameILNReplicaContent(a,b evmInterchain.ILNReplicaCheckpoint)bool{
    return a.ChainID==b.ChainID&&a.Hook==b.Hook&&a.Root==b.Root&&a.Count==b.Count
}
func ilnReplicaKey(c evmInterchain.ILNReplicaCheckpoint) string {
    return strconv.FormatUint(c.ChainID,10)+"-"+strings.ToLower(c.Hook.String())
}
func (w *Worker) ilnReplicaPath(c evmInterchain.ILNReplicaCheckpoint) string {
    return filepath.Join(w.ilnDir(),"replicas",strconv.FormatUint(c.ChainID,10),
        strings.ToLower(c.Hook.String())+".json")
}
func (w *Worker) ilnReplicaCandidate(c evmInterchain.ILNReplicaCheckpoint) evmInterchain.ILNReplica {
    path:=w.ilnReplicaPath(c)
    raw,err:=os.ReadFile(path)
    if err!=nil || len(raw)>ilnReplicaMaxFileBytes{return evmInterchain.ILNReplica{}}
    var out evmInterchain.ILNReplica
    if json.Unmarshal(raw,&out)!=nil || out.ChainID!=c.ChainID ||
        out.Hook!=c.Hook || out.CheckpointBlock>c.BlockNumber ||
        uint64(len(out.Leaves))>c.Count {
        return evmInterchain.ILNReplica{}
    }
    return out
}

// refreshILNReplicas is called by the independent interchain worker. It
// never participates in IBFT consensus, cannot mint, and fails closed on
// missing canonical source reads. A restarted validator can cold-join from a
// peer even if both the origin RPC and its own local node are non-archival.
func (w *Worker) refreshILNReplicas() {
    if w.network==nil{return}
    w.mu.Lock()
    routes:=make([]*evmInterchain.CheckpointRoute,0,len(w.routes))
    for _,route:=range w.routes {routes=append(routes,route)}
    w.mu.Unlock()
    for _,route:=range routes {
        if err:=w.refreshILNReplicaRoute(route);err!=nil {
            w.recordILNReplicaFailure(route,err)
            w.logger.Debug("ILN replica waiting for canonical source or peer",
                "route",route.Name,"err",err)
        }
    }
}

func (w *Worker) refreshILNReplicaRoute(route *evmInterchain.CheckpointRoute) error {
    if route==nil{return fmt.Errorf("missing ILN route")}
    source,dest:=w.destinations[route.SourceNetwork],w.destinations[route.Destination]
    if source==nil||dest==nil{return fmt.Errorf("unconfigured ILN source/destination")}
    head,err:=getConfirmedILNHead(source)
    if err!=nil{return err}
    if head==0{return fmt.Errorf("confirmed source head unavailable")}
    snapshot,err:=getILNRouteAtBlock(source,dest.Domain,route.RouteID,head)
    if err!=nil{return err}
    checkpoint,err:=getConfirmedILNCheckpoint(source,snapshot)
    if err!=nil{return err}
    if checkpoint.Index==^uint32(0) || checkpoint.Root==types.ZeroHash {
        return fmt.Errorf("empty or invalid ILN Hook checkpoint")
    }
    canonical:=evmInterchain.ILNReplicaCheckpoint{
        ChainID:source.ChainID,Hook:snapshot.Route.MerkleTreeHook,
        Root:checkpoint.Root,Count:uint64(checkpoint.Index)+1,
        BlockNumber:checkpoint.BlockNumber,
    }
    if canonical.Count>1<<20{return fmt.Errorf("ILN replica over bounded snapshot capacity")}
    if _,err:=evmInterchain.LoadVerifiedILNReplica(w.ilnReplicaPath(canonical),canonical);err==nil {
        w.mu.Lock();delete(w.ilnReplicaSync,ilnReplicaKey(canonical));w.mu.Unlock()
        w.recordILNReplicaVerified(route,canonical,head)
        return nil
    }

    key:=ilnReplicaKey(canonical)
    w.mu.Lock()
    if w.ilnReplicaSync==nil {w.ilnReplicaSync=make(map[string]*ilnReplicaSyncState)}
    state:=w.ilnReplicaSync[key]
    if state==nil || !sameILNReplicaContent(state.canonical,canonical) {
        state=&ilnReplicaSyncState{
            canonical:canonical,
            prefix:w.ilnReplicaCandidate(canonical),
            attempts:make(map[peer.ID]*ilnReplicaPeerAttempt),
        }
        w.ilnReplicaSync[key]=state
    }
    state.canonical=canonical
    recent:=!state.lastRecentTry.After(time.Now().Add(-30*time.Second))
    if recent {state.lastRecentTry=time.Now()}
    prefix:=state.prefix
    w.mu.Unlock()

    if recent && len(prefix.Leaves)>0 {
        extended,updateErr:=evmInterchain.CatchUpILNReplicaFromRecentHook(source,prefix,canonical)
        if updateErr==nil {
            if err:=evmInterchain.StoreVerifiedILNReplica(w.ilnReplicaPath(canonical),extended,canonical);err==nil {
                w.mu.Lock();delete(w.ilnReplicaSync,key);w.mu.Unlock()
                w.recordILNReplicaVerified(route,canonical,head)
                return nil
            }
        }
    }

    peers:=w.network.Peers()
    sort.Slice(peers,func(i,j int)bool{return peers[i].Info.ID.String()<peers[j].Info.ID.String()})
    w.mu.Lock()
    start:=int(state.nextPeer%uint64(max(1,len(peers))))
    state.nextPeer+=ilnReplicaMaxPeers
    w.mu.Unlock()
    sent:=0
    for i:=0;i<len(peers);i++ {
        if sent>=ilnReplicaMaxPeers {break}
        p:=peers[(start+i)%len(peers)]
        if p==nil || p.Info.ID==w.network.AddrInfo().ID || !w.network.IsConnected(p.Info.ID) {continue}
        request,send:=w.nextILNReplicaPeerRequest(key,p.Info.ID)
        if !send {continue}
        if err:=w.publish(wireEnvelope{Type:"iln_replica_request",ILNReplicaRequest:&request});err!=nil {
            w.logger.Debug("ILN Merkle peer request publish failed", "peer",p.Info.ID,"err",err)
        }
        sent++
    }
    return fmt.Errorf("waiting for independently verified Merkle leaves: chain=%d hook=%s count=%d",
        canonical.ChainID,canonical.Hook,canonical.Count)
}

func (w *Worker) nextILNReplicaPeerRequest(key string, id peer.ID)(ilnReplicaRequestWire,bool){
    w.mu.Lock()
    defer w.mu.Unlock()
    state:=w.ilnReplicaSync[key]
    if state==nil{return ilnReplicaRequestWire{},false}
    attempt:=state.attempts[id]
    if attempt==nil {
        if len(state.attempts)>=ilnReplicaMaxPeers {
            for p,a:=range state.attempts {if time.Since(a.lastRequested)>15*time.Second {delete(state.attempts,p);break}}
            if len(state.attempts)>=ilnReplicaMaxPeers{return ilnReplicaRequestWire{},false}
        }
        attempt=&ilnReplicaPeerAttempt{leaves:append([]types.Hash(nil),state.prefix.Leaves...)}
        state.attempts[id]=attempt
    }
    now:=time.Now()
    if now.Before(attempt.blockedUntil)||now.Sub(attempt.lastRequested)<2*time.Second ||
        uint64(len(attempt.leaves))>=state.canonical.Count {return ilnReplicaRequestWire{},false}
    attempt.lastRequested=now
    attempt.requestedOffset=uint64(len(attempt.leaves))
    c:=state.canonical
    return ilnReplicaRequestWire{
        ChainID:c.ChainID,Hook:c.Hook,Root:c.Root,Count:c.Count,
        CheckpointBlock:c.BlockNumber,Offset:attempt.requestedOffset,
        Recipient:id.String(),
    },true
}

func (w *Worker) respondILNReplicaRequest(from peer.ID, req ilnReplicaRequestWire) error {
    if w.network==nil || from=="" || req.Recipient!=w.network.AddrInfo().ID.String() ||
        req.ChainID==0 || req.Hook==types.ZeroAddress || req.Root==types.ZeroHash ||
        req.CheckpointBlock==0 || req.Count==0 || req.Count>1<<20 ||
        req.Offset>=req.Count {return fmt.Errorf("invalid authenticated replica chunk request")}
    w.mu.Lock()
    if w.ilnReplicaServed==nil{w.ilnReplicaServed=make(map[peer.ID]time.Time)}
    if time.Since(w.ilnReplicaServed[from])<500*time.Millisecond {
        w.mu.Unlock()
        return fmt.Errorf("rate limited ILN replica request")
    }
    w.ilnReplicaServed[from]=time.Now()
    w.mu.Unlock()
    c:=evmInterchain.ILNReplicaCheckpoint{
        ChainID:req.ChainID,Hook:req.Hook,Root:req.Root,Count:req.Count,
        BlockNumber:req.CheckpointBlock,
    }
    // The sender may supply a poisoned target root. It will NEVER cause a
    // reply from an unverified local file, nor can it promote stored data.
    // The provider's verified local root/count may have been observed at
    // a different confirmed height. Check the stored content independently;
    // a P2P response cannot establish its recipient's source state.
    stored:=w.ilnReplicaCandidate(c)
    if stored.CheckpointBlock==0{return evmInterchain.ErrILNReplicaUnverified}
    own:=evmInterchain.ILNReplicaCheckpoint{
        ChainID:stored.ChainID,Hook:stored.Hook,Root:stored.Root,
        Count:uint64(len(stored.Leaves)),BlockNumber:stored.CheckpointBlock,
    }
    if err:=evmInterchain.VerifyILNReplica(stored,own);err!=nil{return err}
    if !sameILNReplicaContent(own,c){return evmInterchain.ErrILNReplicaUnverified}
    to:=req.Offset+ilnReplicaWireLeaves
    if to>req.Count {to=req.Count}
    response:=ilnReplicaChunkWire{
        ilnReplicaRequestWire:req,Target:from.String(),
        Leaves:append([]types.Hash(nil),stored.Leaves[int(req.Offset):int(to)]...),
    }
    return w.publish(wireEnvelope{Type:"iln_replica_chunk",ILNReplicaChunk:&response})
}

func (w *Worker) acceptILNReplicaChunk(from peer.ID, chunk ilnReplicaChunkWire) error {
    if w.network==nil || from=="" || chunk.Target!=w.network.AddrInfo().ID.String() ||
        len(chunk.Leaves)==0 || len(chunk.Leaves)>ilnReplicaWireLeaves ||
        chunk.Recipient!=from.String() {
        return fmt.Errorf("invalid authenticated ILN replica peer response")
    }
    c:=evmInterchain.ILNReplicaCheckpoint{
        ChainID:chunk.ChainID,Hook:chunk.Hook,Root:chunk.Root,
        Count:chunk.Count,BlockNumber:chunk.CheckpointBlock,
    }
    for _,leaf:=range chunk.Leaves {if leaf==types.ZeroHash{return fmt.Errorf("zero Merkle leaf from peer")}}
    key:=ilnReplicaKey(c)
    w.mu.Lock()
    state:=w.ilnReplicaSync[key]
    if state==nil || !sameILNReplicaContent(state.canonical,c) {
        w.mu.Unlock()
        return fmt.Errorf("unsolicited or stale ILN replica data")
    }
    attempt:=state.attempts[from]
    if attempt==nil || time.Now().Before(attempt.blockedUntil) ||
        attempt.requestedOffset!=chunk.Offset ||
        uint64(len(attempt.leaves))!=chunk.Offset ||
        chunk.Offset+uint64(len(chunk.Leaves))>c.Count ||
        time.Since(attempt.lastRequested)>15*time.Second {
        w.mu.Unlock()
        return fmt.Errorf("unsolicited or out-of-order peer Merkle chunk")
    }
    attempt.leaves=append(attempt.leaves,chunk.Leaves...)
    // Consume the request only once; a replayed response cannot be appended.
    attempt.lastRequested=time.Time{}
    if uint64(len(attempt.leaves))!=c.Count {
        w.mu.Unlock()
        return nil
    }
    anchor:=state.canonical
    replica:=evmInterchain.ILNReplica{
        ChainID:anchor.ChainID,Hook:anchor.Hook,Root:anchor.Root,
        CheckpointBlock:anchor.BlockNumber,
        Leaves:append([]types.Hash(nil),attempt.leaves...),
    }
    w.mu.Unlock()
    if err:=evmInterchain.StoreVerifiedILNReplica(w.ilnReplicaPath(anchor),replica,anchor);err!=nil {
        w.mu.Lock()
        if current:=w.ilnReplicaSync[key];current!=nil {
            if attempt:=current.attempts[from];attempt!=nil {
                attempt.blockedUntil=time.Now().Add(30*time.Second)
                attempt.leaves=append([]types.Hash(nil),current.prefix.Leaves...)
            }
        }
        w.mu.Unlock()
        return fmt.Errorf("malicious or inconsistent ILN replica from peer %s: %w",from,err)
    }
    w.mu.Lock()
    if current:=w.ilnReplicaSync[key];current!=nil&&sameILNReplicaContent(current.canonical,anchor) {
        delete(w.ilnReplicaSync,key)
    }
    w.mu.Unlock()
    return nil
}
