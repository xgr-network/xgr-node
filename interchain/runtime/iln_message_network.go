package runtime

import (
    "encoding/hex"
    "fmt"
    "sort"
    "strings"
    "time"

    "github.com/libp2p/go-libp2p/core/peer"

    protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
    evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
    "github.com/xgr-network/xgr-node/types"
)

type ilnMessageRequestWire struct {
    SourceChainID uint64 `json:"sourceChainId"`
    SourceDomain uint32 `json:"sourceDomain"`
    DestinationDomain uint32 `json:"destinationDomain"`
    RouteID types.Hash `json:"routeId"`
    MessageID types.Hash `json:"messageId"`
    Recipient string `json:"recipient"`
}
type ilnMessageResponseWire struct {
    ilnMessageRequestWire
    Target string `json:"target"`
    Message string `json:"message"`
}
func (w *Worker) findILNMessageRoute(req ilnMessageRequestWire)*evmInterchain.CheckpointRoute{
    w.mu.Lock()
    defer w.mu.Unlock()
    for _,r:=range w.routes{
        if r==nil||r.RouteID!=req.RouteID{continue}
        src,dst:=w.destinations[r.SourceNetwork],w.destinations[r.Destination]
        if src!=nil&&dst!=nil&&src.ChainID==req.SourceChainID &&
            src.Domain==req.SourceDomain&&dst.Domain==req.DestinationDomain{
            return r
        }
    }
    return nil
}
func ilnMessagePendingKey(req ilnMessageRequestWire,remote peer.ID)string{
    return fmt.Sprintf("%d/%d/%s/%s/%s",req.SourceChainID,req.DestinationDomain,
        req.RouteID,req.MessageID,remote)
}
func (w *Worker) replicateILNMissingMessages(){
    if w.network==nil||w.topic==nil{return}
    // Work with a copy: disk and BLS callbacks may mutate local votes while
    // the snapshot is being read.
    w.mu.Lock()
    votes:=make([]ilnCheckpointVote,0,len(w.ilnLocalVotes))
    for _,state:=range w.ilnLocalVotes {
        if state!=nil{votes=append(votes,state.vote)}
    }
    w.mu.Unlock()
    sort.Slice(votes,func(i,j int)bool{return votes[i].Route<votes[j].Route})
    cached:=make(map[string]*ilnVerifiedReplicaContext)
    peers:=w.network.Peers()
    sent:=0
    for _,vote:=range votes{
        if sent>=12{break}
        var payload protocol.ILNCheckpointPayload
        if payload.UnmarshalBinary(vote.Payload)!=nil {continue}
        w.mu.Lock()
        route:=w.routes[vote.Route]
        w.mu.Unlock()
        if route==nil{continue}
        ctx:=cached[route.Name]
        if ctx==nil{
            resolved,err:=w.verifiedILNReplicaRoute(route)
            if err!=nil{continue}
            ctx=resolved
            cached[route.Name]=ctx
        }
        if w.hasVerifiedILNMessage(ctx,payload.AuthorizedMessageID){continue}
        // The current Merkle prefix must contain this signed message. An
        // older root is not an authority to import bytes for another route.
        exists:=false
        for _,leaf:=range ctx.replica.Leaves{
            if leaf==payload.AuthorizedMessageID{exists=true;break}
        }
        if !exists{continue}
        req:=ilnMessageRequestWire{
            SourceChainID:ctx.source.ChainID,SourceDomain:ctx.source.Domain,
            DestinationDomain:ctx.destination.Domain,RouteID:route.RouteID,
            MessageID:payload.AuthorizedMessageID,
        }
        for _,connection:=range peers{
            if sent>=12{break}
            if connection==nil{continue}
            id:=connection.Info.ID
            if id==w.network.AddrInfo().ID || !w.network.IsConnected(id){continue}
            key:=ilnMessagePendingKey(req,id)
            w.mu.Lock()
            if w.ilnMessageRequests==nil{w.ilnMessageRequests=make(map[string]time.Time)}
            last:=w.ilnMessageRequests[key]
            if time.Since(last)<15*time.Second{w.mu.Unlock();continue}
            w.ilnMessageRequests[key]=time.Now()
            w.mu.Unlock()
            request:=req
            request.Recipient=id.String()
            if err:=w.publish(wireEnvelope{
                Type:"iln_message_request",ILNMessageRequest:&request,
            });err!=nil{
                w.logger.Debug("request serialized ILN message from peer failed",
                    "peer",id,"err",err)
            }
            sent++
        }
    }
}

func (w *Worker) respondILNMessageRequest(from peer.ID,req ilnMessageRequestWire)error{
    if w.network==nil||from==""||!w.network.IsConnected(from)||
        req.Recipient!=w.network.AddrInfo().ID.String()||
        req.SourceChainID==0||req.SourceDomain==0||
        req.DestinationDomain==0||req.RouteID==types.ZeroHash||
        req.MessageID==types.ZeroHash{
        return fmt.Errorf("invalid authenticated ILN serialized message request")
    }
    w.mu.Lock()
    if w.ilnMessageLastServed==nil{w.ilnMessageLastServed=make(map[peer.ID]time.Time)}
    if time.Since(w.ilnMessageLastServed[from])<250*time.Millisecond {
        w.mu.Unlock()
        return fmt.Errorf("rate limited serialized ILN message request")
    }
    w.ilnMessageLastServed[from]=time.Now()
    w.mu.Unlock()
    route:=w.findILNMessageRoute(req)
    if route==nil{return fmt.Errorf("unknown ILN message source route")}
    ctx,err:=w.verifiedILNReplicaRoute(route)
    if err!=nil{return err}
    if err:=verifyILNFeeAllocation(ctx.source,ctx.snapshot,req.MessageID,ctx.canonical.BlockNumber);err!=nil{
        return fmt.Errorf("source validator fee allocation missing from message replication: %w",err)
    }
    message,err:=w.readVerifiedILNMessage(ctx,req.MessageID)
    if err!=nil{return err}
    response:=ilnMessageResponseWire{
        ilnMessageRequestWire:req,Target:from.String(),
        Message:"0x"+hex.EncodeToString(message),
    }
    return w.publish(wireEnvelope{Type:"iln_message_response",ILNMessageResponse:&response})
}
func (w *Worker) acceptILNMessageResponse(from peer.ID,msg ilnMessageResponseWire)error{
    if w.network==nil||from==""||!w.network.IsConnected(from)||
        msg.Target!=w.network.AddrInfo().ID.String()||
        msg.Recipient!=from.String() ||
        len(msg.Message)<2 ||len(msg.Message)>2+2*evmInterchain.MaxILNSerializedMessageBytes||
        !strings.HasPrefix(msg.Message,"0x") {
        return fmt.Errorf("invalid authenticated ILN serialized message response")
    }
    key:=ilnMessagePendingKey(msg.ilnMessageRequestWire,from)
    w.mu.Lock()
    at:=w.ilnMessageRequests[key]
    w.mu.Unlock()
    if at.IsZero()||time.Since(at)>30*time.Second {
        return fmt.Errorf("unsolicited, replayed or expired ILN message response")
    }
    route:=w.findILNMessageRoute(msg.ilnMessageRequestWire)
    if route==nil{return fmt.Errorf("unknown ILN message response route")}
    ctx,err:=w.verifiedILNReplicaRoute(route)
    if err!=nil{return err}
    if err:=verifyILNFeeAllocation(ctx.source,ctx.snapshot,msg.MessageID,ctx.canonical.BlockNumber);err!=nil{
        return fmt.Errorf("ILN message response lacks mandatory validator fee: %w",err)
    }
    decoded,err:=hex.DecodeString(msg.Message[2:])
    if err!=nil{return err}
    if err:=evmInterchain.StoreVerifiedILNMessage(
        w.ilnMessagePath(ctx.canonical,msg.MessageID),
        ctx.replica,ctx.canonical,decoded,msg.MessageID,
        ctx.source.Domain,ctx.snapshot.Route.SourceRouter,
        ctx.destination.Domain,ctx.snapshot.Route.DestinationRouter,
    );err!=nil {
        return fmt.Errorf("peer provided corrupted or foreign ILN message: %w",err)
    }
    w.mu.Lock()
    delete(w.ilnMessageRequests,key)
    w.mu.Unlock()
    return nil
}
