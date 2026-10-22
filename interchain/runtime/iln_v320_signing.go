package runtime

import (
    "errors"
    "fmt"
    "math/big"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "time"

    protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
    "github.com/xgr-network/xgr-node/crypto"
    evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
    "github.com/xgr-network/xgr-node/types"
)

const maxILNReplicaLeavesPerTick = 16

var verifyILNFeeAllocation = evmInterchain.VerifyConfirmedILNFeeAllocation

type ilnVerifiedReplicaContext struct {
    route *evmInterchain.CheckpointRoute
    source *evmInterchain.Destination
    destination *evmInterchain.Destination
    snapshot *evmInterchain.ILNRouteSnapshot
    checkpoint *evmInterchain.ConfirmedCheckpoint
    canonical evmInterchain.ILNReplicaCheckpoint
    replica evmInterchain.ILNReplica
}

// Every public request, local sign, and received vote uses the same
// validator-owned prefix, whose complete root must match the confirmed
// source MerkleTreeHook. A peer's claim, local cursor or relayer is never
// sufficient to mint or authorize a BLS vote.
func (w *Worker) verifiedILNReplicaRoute(route *evmInterchain.CheckpointRoute)(*ilnVerifiedReplicaContext,error){
    if route==nil{return nil,fmt.Errorf("missing ILN route")}
    source,destination:=w.destinations[route.SourceNetwork],w.destinations[route.Destination]
    if source==nil||destination==nil{return nil,fmt.Errorf("missing ILN source/destination")}
    head,err:=getConfirmedILNHead(source)
    if err!=nil{return nil,err}
    if head==0{return nil,fmt.Errorf("confirmed source head unavailable")}
    snapshot,err:=getILNRouteAtBlock(source,destination.Domain,route.RouteID,head)
    if err!=nil{return nil,err}
    checkpoint,err:=getConfirmedILNCheckpoint(source,snapshot)
    if err!=nil{return nil,err}
    if checkpoint.Root==types.ZeroHash || checkpoint.Index==^uint32(0) ||
        snapshot.Route.Key.SourceChainID!=source.ChainID ||
        snapshot.Route.Key.SourceDomain!=source.Domain ||
        snapshot.Route.Key.RouteID!=route.RouteID ||
        snapshot.Route.Key.DestinationDomain!=destination.Domain ||
        snapshot.Route.ValidatorFeeWei==nil ||
        snapshot.Route.ValidatorFeeWei.Sign()<=0 {
        return nil,fmt.Errorf("invalid confirmed ILN route root or fee policy")
    }
    c:=evmInterchain.ILNReplicaCheckpoint{
        ChainID:source.ChainID,Hook:snapshot.Route.MerkleTreeHook,
        Root:checkpoint.Root,Count:uint64(checkpoint.Index)+1,
        BlockNumber:checkpoint.BlockNumber,
    }
    replica,err:=evmInterchain.LoadVerifiedILNReplica(w.ilnReplicaPath(c),c)
    if err!=nil{return nil,fmt.Errorf("verified validator-owned ILN Merkle replica not available: %w",err)}
    return &ilnVerifiedReplicaContext{
        route:route,source:source,destination:destination,snapshot:snapshot,
        checkpoint:checkpoint,canonical:c,replica:replica,
    },nil
}

func (w *Worker) ilnLeafCursorPath(route string,setID uint64)string{
    return filepath.Join(w.ilnDir(),"leaf-cursors",
        route+"-set-"+strconv.FormatUint(setID,10)+".index")
}
func (w *Worker) readILNLeafCursor(route string,setID uint64)(uint64,error){
    if setID==0||route==""||strings.ContainsAny(route,"/\\") {
        return 0,fmt.Errorf("invalid ILN leaf cursor identity")
    }
    raw,err:=os.ReadFile(w.ilnLeafCursorPath(route,setID))
    if os.IsNotExist(err){return 0,nil}
    if err!=nil{return 0,err}
    index,err:=strconv.ParseUint(strings.TrimSpace(string(raw)),10,64)
    if err!=nil{return 0,fmt.Errorf("corrupt ILN leaf cursor: %w",err)}
    return index,nil
}
func (w *Worker) writeILNLeafCursor(route string,setID,index uint64)error{
    if setID==0||route==""||strings.ContainsAny(route,"/\\") {
        return fmt.Errorf("invalid ILN leaf cursor identity")
    }
    path:=w.ilnLeafCursorPath(route,setID)
    if err:=os.MkdirAll(filepath.Dir(path),0o770);err!=nil{return err}
    tmp,err:=os.CreateTemp(filepath.Dir(path),".iln-cursor-*")
    if err!=nil{return err}
    defer os.Remove(tmp.Name())
    if err:=tmp.Chmod(0o660);err!=nil{tmp.Close();return err}
    if _,err:=tmp.WriteString(strconv.FormatUint(index,10)+"\n");err!=nil{tmp.Close();return err}
    if err:=tmp.Sync();err!=nil{tmp.Close();return err}
    if err:=tmp.Close();err!=nil{return err}
    return os.Rename(tmp.Name(),path)
}

// The automatic scanner uses only recent, confirmed Gateway events. It never
// assumes that a Merkle leaf's insertion block can be inferred from the
// current Hook epoch, and never requests historical contract state. Older
// messages remain recoverable through the public quorum request's source
// operation-block hint, which is independently checked against the receipt.
func (w *Worker) signVerifiedILNReplicaBatch(route *evmInterchain.CheckpointRoute) error {
    ctx,err:=w.verifiedILNReplicaRoute(route)
    if err!=nil{return err}
    set,err:=getILNValidatorSet(ctx.destination)
    if err!=nil{return err}
    eligible,err:=w.interchainSignerEligible(set,w.localAddr)
    if err!=nil{return err}
    if !eligible{return nil}
    // Scan a bounded window of confirmed Gateway events; this is discovery
    // only. Each candidate is separately authorized by its successful
    // receipt, full Merkle inclusion, FeeVault, and destination set.
    to:=ctx.canonical.BlockNumber
    from:=uint64(1)
    if to>127{from=to-127}
    ops,err:=getConfirmedILNOperations(ctx.source,ctx.snapshot.Route.Gateway,
        ctx.snapshot.Route.Key.RouteID,ctx.destination.Domain,from,to)
    if err!=nil{return fmt.Errorf("recent source operation discovery unavailable: %w",err)}
    for _,op:=range ops {
        if op.MessageID==types.ZeroHash||op.BlockNumber<from||op.BlockNumber>to{
            return fmt.Errorf("invalid confirmed source operation locator")
        }
        index:=-1
        for i,id:=range ctx.replica.Leaves{
            if id==op.MessageID{
                if index>=0{return fmt.Errorf("ambiguous source Merkle insertion")}
                index=i
            }
        }
        if index<0{continue} // not included in the confirmed Hook root
        if err:=w.signVerifiedILNReplicaLeaf(ctx,set,op.MessageID,
            uint32(index),true,op.BlockNumber);err!=nil{return err}
    }
    return nil
}

func (w *Worker) signVerifiedILNReplicaMessage(
    route *evmInterchain.CheckpointRoute,messageID types.Hash,sourceBlock uint64,
) error {
    if sourceBlock==0{return fmt.Errorf("missing original confirmed ILN operation block")}
    ctx,err:=w.verifiedILNReplicaRoute(route)
    if err!=nil{return err}
    set,err:=getILNValidatorSet(ctx.destination)
    if err!=nil{return err}
    index:=-1
    for i,id:=range ctx.replica.Leaves{
        if id==messageID{
            if index>=0{return fmt.Errorf("ambiguous ILN message insertion")}
            index=i
        }
    }
    if index<0{return fmt.Errorf("ILN message is not in confirmed Hook prefix")}
    return w.signVerifiedILNReplicaLeaf(ctx,set,messageID,uint32(index),false,sourceBlock)
}

func (w *Worker) signVerifiedILNReplicaLeaf(
    ctx *ilnVerifiedReplicaContext,set *evmInterchain.ValidatorSet,
    messageID types.Hash,messageIndex uint32,scan bool,sourceBlock uint64,
)error{
    if ctx==nil||set==nil||uint64(messageIndex)>=uint64(len(ctx.replica.Leaves)) ||
        ctx.replica.Leaves[messageIndex]!=messageID{return fmt.Errorf("invalid message inclusion identity")}
    // An unauthenticated candidate can never be accepted via the index
    // alone. Reconstruct and check all 32 Hyperlane proof siblings.
    _,err:=evmInterchain.BuildILNReplicaProof(ctx.replica,ctx.canonical,messageIndex)
    if err!=nil{return fmt.Errorf("refuse BLS over unverified Merkle inclusion: %w",err)}
    err=verifyILNFeeAllocation(ctx.source,ctx.snapshot,messageID,ctx.canonical.BlockNumber)
    if scan&&errors.Is(err,evmInterchain.ErrILNFeeNotAllocated){return nil}
    if err!=nil{return err}
    // On-chain operation and same-transaction Gateway/Dispatch/Hook receipt
    // resolve the original insertion position without historical eth_call.
    operation,err:=getILNOperationAtBlock(ctx.source,ctx.snapshot.Route.Gateway,
        ctx.snapshot.Route.Key.RouteID,ctx.destination.Domain,messageID,sourceBlock)
    if err!=nil{return fmt.Errorf("confirmed source receipt required for ILN BLS: %w",err)}
    if operation==nil||operation.BlockNumber!=sourceBlock||
        operation.MessageID!=messageID||operation.InsertionIndex!=messageIndex{
        return fmt.Errorf("ILN source receipt insertion index/block mismatch")
    }
    _,fixed,err:=evmInterchain.VerifiedILNMessagePrefix(
        ctx.replica,ctx.canonical,messageIndex,operation.BlockNumber)
    if err!=nil{return fmt.Errorf("unverified immutable source insertion root: %w",err)}
    delivered,err:=getILNMessageDelivered(ctx.destination,ctx.snapshot.Route.DestinationRouter,messageID)
    if err!=nil{return fmt.Errorf("destination state unavailable: %w",err)}
    if delivered {
        if scan{return nil}
        return fmt.Errorf("ILN message already delivered")
    }

    // This is the CURRENT confirmed source fee policy. The original amount
    // was atomically charged by Gateway/FeeVault at dispatch; V315 does NOT
    // preserve a per-message historical fee amount. Never misrepresent this
    // signed nonzero value as the original charge.
    payload:=protocol.ILNCheckpointPayload{
        SourceChainID:ctx.snapshot.Route.Key.SourceChainID,
        SourceDomain:ctx.snapshot.Route.Key.SourceDomain,
        DestinationDomain:ctx.snapshot.Route.Key.DestinationDomain,
        RouteID:ctx.snapshot.Route.Key.RouteID,
        SetID:set.SetID,
        SourceBlockNumber:fixed.BlockNumber,
        Registry:ctx.snapshot.Registry,
        Gateway:ctx.snapshot.Route.Gateway,
        SourceRouter:ctx.snapshot.Route.SourceRouter,
        Mailbox:ctx.snapshot.Route.Mailbox,
        MerkleTreeHook:ctx.snapshot.Route.MerkleTreeHook,
        DestinationRouter:ctx.snapshot.Route.DestinationRouter,
        ValidatorFeeWei:new(big.Int).Set(ctx.snapshot.Route.ValidatorFeeWei),
        AuthorizedMessageID:messageID,
        Root:fixed.Root,Index:messageIndex,
    }
    raw,err:=payload.MarshalBinary()
    if err!=nil{return err}
    hash:=crypto.Keccak256Hash(raw)
    w.mu.Lock();_,already:=w.ilnLocalVotes[hash];w.mu.Unlock()
    if already{return nil}
    current,err:=getILNValidatorSet(ctx.destination)
    if err!=nil{return fmt.Errorf("BLS set recheck: %w",err)}
    if current.SetID!=set.SetID{return fmt.Errorf("ILN validator set changed during verification")}
    eligible,err:=w.interchainSignerEligible(current,w.localAddr)
    if err!=nil{return err}
    if !eligible{return nil} // permitted read-only non-validator can relay hints
    delivered,err=getILNMessageDelivered(ctx.destination,ctx.snapshot.Route.DestinationRouter,messageID)
    if err!=nil{return err}
    if delivered{return nil}
    key,err:=crypto.BytesToBLSSecretKey(w.blsRaw)
    if err!=nil{return err}
    sig,err:=crypto.SignByBLS(key,raw)
    if err!=nil{return err}
    vote:=ilnCheckpointVote{Route:ctx.route.Name,Payload:raw,Signer:w.localAddr,Signature:sig}
    if err:=w.persistILNLocalVote(hash,vote);err!=nil{return err}
    w.mu.Lock()
    if w.ilnLocalVotes==nil{w.ilnLocalVotes=make(map[types.Hash]*ilnLocalVoteState)}
    w.ilnLocalVotes[hash]=&ilnLocalVoteState{vote:vote,lastBroadcast:time.Now()}
    w.mu.Unlock()
    if err:=w.acceptILNCheckpointVote(vote);err!=nil{return err}
    return w.publish(wireEnvelope{Type:"iln_checkpoint_vote",ILNCheckpointVote:&vote})
}
