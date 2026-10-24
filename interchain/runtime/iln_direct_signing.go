package runtime

import (
    "fmt"
    "math/big"
    "time"

    protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
    "github.com/xgr-network/xgr-node/crypto"
    evm "github.com/xgr-network/xgr-node/interchain/evm"
    "github.com/xgr-network/xgr-node/types"
)

// Only a successful, independently confirmed source receipt containing one
// immutable Gateway fee allocation and one canonical Mailbox Dispatch can
// authorize an Interchain BLS signature. No root, leaf or Merkle state exists.
type directILNEvidence struct {
    route *evm.CheckpointRoute
    source *evm.Destination
    destination *evm.Destination
    snapshot *evm.ILNRouteSnapshot
    operation *evm.ILNOperation
    set *evm.ValidatorSet
    destinationISM types.Address
}

func (w *Worker) verifyDirectILNEvidence(
    route *evm.CheckpointRoute, messageID types.Hash, sourceBlock uint64,
) (*directILNEvidence,error){
    if route==nil||messageID==types.ZeroHash||sourceBlock==0 {
        return nil,fmt.Errorf("invalid direct Interchain message")
    }
    source,destination:=w.destinations[route.SourceNetwork],w.destinations[route.Destination]
    if source==nil||destination==nil{return nil,fmt.Errorf("unknown source/destination network")}
    var operation *evm.ILNOperation
    var snapshot *evm.ILNRouteSnapshot
    var err error
    if source.ChainID==1643 {
        local,ok:=w.state.(evm.NativeXGRReader)
        if !ok{return nil,fmt.Errorf("native XGR source reader unavailable")}
        operation,_,snapshot,err=evm.VerifyNativeXGROperation(local,source,route,destination.Domain,messageID,sourceBlock)
    } else {
        operation,_,snapshot,err=evm.GetQuorumVerifiedDirectILNOperation(source,route,destination.Domain,messageID,sourceBlock)
    }
    if err!=nil{return nil,err}
    if snapshot.Route.Key.RouteID!=route.RouteID ||
        snapshot.Route.Key.SourceChainID!=source.ChainID ||
        snapshot.Route.Key.SourceDomain!=source.Domain ||
        snapshot.Route.Key.DestinationDomain!=destination.Domain ||
        snapshot.Registry!=types.StringToAddress(source.ILNRegistryAddress) ||
        operation.ValidatorFeeWei==nil||operation.ValidatorFeeWei.Sign()<=0 ||
        operation.BlockNumber!=sourceBlock||operation.MessageID!=messageID {
        return nil,fmt.Errorf("source route or Gateway operation identity mismatch")
    }
    ism,err:=evm.GetDirectILNDestinationISM(destination,snapshot.Route.DestinationRouter)
    if err!=nil{return nil,err}
    set,err:=getILNValidatorSet(destination)
    if err!=nil{return nil,err}
    if set==nil||set.SetID==0||len(set.Validators)==0||
        len(set.Validators)!=len(set.BLSPublicKeys) {
        return nil,fmt.Errorf("current destination BLS validator set unavailable")
    }
    delivered,err:=getILNMessageDelivered(destination,snapshot.Route.DestinationRouter,messageID)
    if err!=nil{return nil,fmt.Errorf("destination delivery-state verification: %w",err)}
    if delivered{return nil,fmt.Errorf("Interchain message already delivered")}
    return &directILNEvidence{
        route:route,source:source,destination:destination,
        snapshot:snapshot,operation:operation,set:set,destinationISM:ism,
    },nil
}

func (w *Worker) directILNPayload(c *directILNEvidence) protocol.XITADirectMessage {
    return protocol.XITADirectMessage{
        SourceChainID:c.source.ChainID,
        SourceDomain:c.source.Domain,
        DestinationDomain:c.destination.Domain,
        RouteID:c.route.RouteID,
        SetID:c.set.SetID,
        SourceBlockNumber:c.operation.BlockNumber,
        SourceRegistry:c.snapshot.Registry,
        Gateway:c.snapshot.Route.Gateway,
        SourceRouter:c.snapshot.Route.SourceRouter,
        SourceMailbox:c.snapshot.Route.Mailbox,
        DestinationRouter:c.snapshot.Route.DestinationRouter,
        ValidatorFeeWei:new(big.Int).Set(c.operation.ValidatorFeeWei),
        MessageID:c.operation.MessageID,
        DestinationISM:c.destinationISM,
        DestinationRegistry:types.StringToAddress(c.destination.RegistryAddress),
    }
}

// This is the ONE signing path used by automatic Gateway scanning, public
// xgr_requestILNQuorum and peer authorization hints. Local signed votes are
// persisted before gossip and stay valid until destination set rotation.
func (w *Worker) signDirectILNMessage(
    route *evm.CheckpointRoute,messageID types.Hash,sourceBlock uint64,
)error{
    // M-03: recheck synchronization in the signing entry point, not only
    // in the periodic worker tick. RPC requests and gossip can arrive anytime.
    if w.state==nil||!w.state.IsSynchronized(){
        return fmt.Errorf("Direct-BLS signing paused: XGR validator not synchronized")
    }
    ctx,err:=w.verifyDirectILNEvidence(route,messageID,sourceBlock)
    if err!=nil{return err}
    eligible,err:=w.interchainSignerEligible(ctx.set,w.localAddr)
    if err!=nil{return err}
    if !eligible{return nil}
    payload:=w.directILNPayload(ctx)
    raw,err:=payload.MarshalBinary()
    if err!=nil{return err}
    hash:=crypto.Keccak256Hash(raw)
    w.mu.Lock();_,exists:=w.ilnLocalVotes[hash];w.mu.Unlock()
    if exists{return nil}
    // Do not sign for a retired set or a message settled between the
    // independent source proof and this wallet's BLS signature.
    current,err:=getILNValidatorSet(ctx.destination)
    if err!=nil{return err}
    if current.SetID!=payload.SetID{return fmt.Errorf("destination BLS set rotated before signing")}
    delivered,err:=getILNMessageDelivered(ctx.destination,payload.DestinationRouter,messageID)
    if err!=nil{return err}
    if delivered{return fmt.Errorf("Interchain message already delivered")}
    key,err:=crypto.BytesToBLSSecretKey(w.blsRaw)
    if err!=nil{return err}
    if w.state==nil||!w.state.IsSynchronized(){
        return fmt.Errorf("Direct-BLS signature rejected: node lost synchronization during verification")
    }
    signature,err:=crypto.SignByBLS(key,raw)
    if err!=nil{return err}
    vote:=ilnCheckpointVote{Route:route.Name,Payload:raw,Signer:w.localAddr,Signature:signature}
    if err:=w.persistILNLocalVote(hash,vote);err!=nil{return err}
    w.mu.Lock()
    w.ilnLocalVotes[hash]=&ilnLocalVoteState{vote:vote,lastBroadcast:time.Now()}
    w.mu.Unlock()
    if err:=w.acceptILNCheckpointVote(vote);err!=nil{return err}
    return w.publish(wireEnvelope{Type:"iln_checkpoint_vote",ILNCheckpointVote:&vote})
}

// One bounded eth_getLogs scan of at most 80 blocks avoids 8 successive
// head/dial/log checks per active route every 30 seconds.
const directILNScanBatchBlocks = uint64(80)

// Independent background discovery of fee-qualified Gateway operations.
// The cursor is a SOURCE RPC block locator only, never a Merkle progress
// measure. RPCs are bounded, 30-second paced, and an error never advances it.
func (w *Worker) scanDirectILNOperations(route *evm.CheckpointRoute) error {
    if route==nil{return fmt.Errorf("missing Direct-BLS route")}
    source,destination:=w.destinations[route.SourceNetwork],w.destinations[route.Destination]
    if source==nil||destination==nil{return fmt.Errorf("unconfigured Direct-BLS chain")}
    cursorName:="_direct_"+route.Name
    cursor,exists,err:=w.readILNCursor(cursorName)
    if err!=nil{return err}
    var head,from,to uint64
    var operations []evm.ILNOperation
    if source.ChainID==1643 {
        native,e:=w.nativeReader(source)
        if e!=nil{return e}
        head,from,to,operations,err=evm.NativeXGRScanWindow(native,source,
            destination.Domain,route.RouteID,cursor,exists,directILNScanBatchBlocks)
    } else {
        head,from,to,operations,err=evm.ScanDirectILNOperationWindow(
            source,destination.Domain,route.RouteID,cursor,exists,directILNScanBatchBlocks)
    }
    if err!=nil{return err}
    if from!=0 {
        for _,op:=range operations {
            err:=w.signDirectILNMessage(route,op.MessageID,op.BlockNumber)
            if err!=nil{
                if err.Error()=="Interchain message already delivered"{continue}
                return err
            }
        }
        if err:=w.writeILNCursor(cursorName,to);err!=nil{return err}
        cursor=to
    } else if !exists {
        cursor=head
    }
    w.recordILNDirectScan(route,head,cursor)
    return nil
}
