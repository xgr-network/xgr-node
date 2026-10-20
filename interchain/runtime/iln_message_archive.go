package runtime

import (
    "fmt"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "time"

    "github.com/xgr-network/xgr-node/crypto"
    evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
    "github.com/xgr-network/xgr-node/types"
)

const ilnMessageRecentScanLimit uint64 = 128

func (w *Worker) ilnMessagePath(c evmInterchain.ILNReplicaCheckpoint,id types.Hash)string{
    return filepath.Join(w.ilnDir(),"messages",strconv.FormatUint(c.ChainID,10),
        strings.ToLower(c.Hook.String()),strings.ToLower(id.String())+".json")
}
func (w *Worker) captureRecentILNMessages() {
    w.mu.Lock()
    routes:=make([]*evmInterchain.CheckpointRoute,0,len(w.routes))
    for _,r:=range w.routes{routes=append(routes,r)}
    if w.ilnMessageLastScan==nil{w.ilnMessageLastScan=make(map[string]time.Time)}
    w.mu.Unlock()
    for _,route:=range routes {
        if route==nil{continue}
        w.mu.Lock()
        if time.Since(w.ilnMessageLastScan[route.Name])<20*time.Second {
            w.mu.Unlock();continue
        }
        w.ilnMessageLastScan[route.Name]=time.Now()
        w.mu.Unlock()
        if err:=w.captureRecentILNMessagesForRoute(route);err!=nil{
            w.logger.Debug("ILN source Mailbox message archive not caught up",
                "route",route.Name,"err",err)
        }
    }
}

// A recent successful Dispatch is confirmed on the actual Mailbox, then
// independently checked for exactly one Hook leaf and a mandatory FeeVault
// allocation. No archived source receipt, private relayer or company fee is
// required. Missing old bytes may instead be imported by a wallet user or
// recovered from an authenticated validator peer.
func (w *Worker) captureRecentILNMessagesForRoute(route *evmInterchain.CheckpointRoute)error{
    ctx,err:=w.verifiedILNReplicaRoute(route)
    if err!=nil{return err}
    cursorKey:="_dispatch_"+route.Name
    cursor,exists,err:=w.readILNCursor(cursorKey)
    if err!=nil{return err}
    to:=ctx.canonical.BlockNumber
    if !exists {
        // Only seed from the recent RPC retention window. A private
        // historical Gateway/indexer is NEVER an implicit requirement.
        if to>=ilnMessageRecentScanLimit {cursor=to-ilnMessageRecentScanLimit}
        else {cursor=0}
    }
    if cursor>=to{return nil}
    from:=cursor+1
    if to-from>=ilnMessageRecentScanLimit {
        from=to-ilnMessageRecentScanLimit+1
    }
    if from==0{return fmt.Errorf("invalid ILN recent Dispatch block range")}
    messages,err:=evmInterchain.GetRecentConfirmedILNDispatches(
        ctx.source,ctx.snapshot.Route.Mailbox,from,to)
    if err!=nil{return err}
    for _,message:=range messages {
        if message.SourceRouter!=ctx.snapshot.Route.SourceRouter ||
            message.DestinationDomain!=ctx.destination.Domain ||
            message.DestinationRouter!=ctx.snapshot.Route.DestinationRouter {
            continue
        }
        if crypto.Keccak256Hash(message.Message)!=message.MessageID {
            return fmt.Errorf("source Dispatch hash mismatch")
        }
        matched:=0
        for _,leaf:=range ctx.replica.Leaves{
            if leaf==message.MessageID{matched++}
        }
        if matched==0{continue} // source Mailbox dispatch used another Hook
        if matched!=1{return fmt.Errorf("duplicate source Hook insertion")}
        if err:=verifyILNFeeAllocation(ctx.source,ctx.snapshot,
            message.MessageID,ctx.canonical.BlockNumber);err!=nil{
            if err==evmInterchain.ErrILNFeeNotAllocated{continue}
            return err
        }
        if err:=evmInterchain.StoreVerifiedILNMessage(
            w.ilnMessagePath(ctx.canonical,message.MessageID),
            ctx.replica,ctx.canonical,message.Message,message.MessageID,
            ctx.source.Domain,ctx.snapshot.Route.SourceRouter,
            ctx.destination.Domain,ctx.snapshot.Route.DestinationRouter,
        );err!=nil{return err}
    }
    return w.writeILNCursor(cursorKey,to)
}

// Load bytes from on-disk node backup or independently validated peers.
// This is a separate message-availability check from BLS authorization.
func (w *Worker) readVerifiedILNMessage(ctx *ilnVerifiedReplicaContext,id types.Hash)([]byte,error){
    if ctx==nil{return nil,fmt.Errorf("ILN route context unavailable")}
    return evmInterchain.LoadVerifiedILNMessage(
        w.ilnMessagePath(ctx.canonical,id),
        ctx.replica,ctx.canonical,id,
        ctx.source.Domain,ctx.snapshot.Route.SourceRouter,
        ctx.destination.Domain,ctx.snapshot.Route.DestinationRouter)
}

// A user can bootstrap missing historic message bytes using a public source
// transaction receipt, without claiming that the Merkle root is reversible.
// Validation fails closed on any mismatch of route, fee or confirmed Hook root.
func (w *Worker) importILNMessageFromRawRoute(route *evmInterchain.CheckpointRoute,raw []byte)error{
    if len(raw)<77||len(raw)>evmInterchain.MaxILNSerializedMessageBytes {
        return fmt.Errorf("invalid bounded message import")
    }
    ctx,err:=w.verifiedILNReplicaRoute(route)
    if err!=nil{return err}
    id:=crypto.Keccak256Hash(raw)
    if err:=verifyILNFeeAllocation(ctx.source,ctx.snapshot,id,ctx.canonical.BlockNumber);err!=nil {
        return fmt.Errorf("message import not eligible for source validator fee: %w",err)
    }
    return evmInterchain.StoreVerifiedILNMessage(
        w.ilnMessagePath(ctx.canonical,id),ctx.replica,ctx.canonical,raw,id,
        ctx.source.Domain,ctx.snapshot.Route.SourceRouter,
        ctx.destination.Domain,ctx.snapshot.Route.DestinationRouter)
}

func (w *Worker) hasVerifiedILNMessage(ctx *ilnVerifiedReplicaContext,id types.Hash)bool{
    if ctx==nil{return false}
    path:=w.ilnMessagePath(ctx.canonical,id)
    info,err:=os.Stat(path)
    if err!=nil||info.Size()>2*evmInterchain.MaxILNSerializedMessageBytes+1024{return false}
    _,err=w.readVerifiedILNMessage(ctx,id)
    return err==nil
}
