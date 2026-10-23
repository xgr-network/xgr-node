package evm

import (
    "fmt"
    "sort"

    "github.com/umbracle/ethgo"
    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

// maxILNReplicaRecentBlocks limits incremental log reads to a RECENT confirmed
// Hook epoch. This is never a genesis-to-present Gateway/event replay and
// never needs an archive node. A validator that missed too many blocks must
// catch up from another validator's verified Merkle replica or a backup.
const maxILNReplicaRecentBlocks = uint64(128)

var ErrILNReplicaPeerRequired = fmt.Errorf("ILN historical prefix unavailable locally; request verified peer snapshot or backup")

// ExtendILNReplica proves that an untrusted existing prefix and strictly
// ordered new leaves reconstruct exactly the independently confirmed Hook root.
// It does not trust the previous file's checksum, prior alleged root or a peer.
func ExtendILNReplica(
    canonical ILNReplicaCheckpoint,
    previous ILNReplica,
    suffix []ILNReplicaChunk,
) (ILNReplica,error) {
    if canonical.Count==0 || canonical.Count>1<<20 ||
        canonical.Hook==types.ZeroAddress || canonical.Root==types.ZeroHash ||
        canonical.ChainID==0 || canonical.BlockNumber==0 ||
        previous.ChainID!=canonical.ChainID ||
        previous.Hook!=canonical.Hook ||
        previous.CheckpointBlock>canonical.BlockNumber ||
        uint64(len(previous.Leaves))>canonical.Count {
        return ILNReplica{},ErrILNReplicaUnverified
    }
    joined:=ILNReplica{
        ChainID:canonical.ChainID,Hook:canonical.Hook,
        CheckpointBlock:canonical.BlockNumber,Root:canonical.Root,
        Leaves:append([]types.Hash(nil),previous.Leaves...),
    }
    for _,part:=range suffix {
        if part.ChainID!=canonical.ChainID || part.Hook!=canonical.Hook ||
            part.Offset!=uint64(len(joined.Leaves)) ||
            len(part.Leaves)==0 || len(part.Leaves)>1024 ||
            uint64(len(joined.Leaves)+len(part.Leaves))>canonical.Count {
            return ILNReplica{},ErrILNReplicaUnverified
        }
        joined.Leaves=append(joined.Leaves,part.Leaves...)
    }
    if err:=VerifyILNReplica(joined,canonical);err!=nil{return ILNReplica{},err}
    return joined,nil
}

// CatchUpILNReplicaFromRecentHook obtains ONLY the short, missing confirmed
// Hook range. It is an optimization for continuously running validators;
// recovery from pruned history or a cold join uses authenticated peer transfer.
// Network fees, ILNOperation and route-history logs are NOT consulted.
func CatchUpILNReplicaFromRecentHook(
    source *Destination,
    previous ILNReplica,
    canonical ILNReplicaCheckpoint,
) (ILNReplica,error) {
    if source==nil || canonical.ChainID!=source.ChainID ||
        canonical.Hook==types.ZeroAddress ||
        previous.ChainID!=source.ChainID || previous.Hook!=canonical.Hook ||
        previous.CheckpointBlock==0 || previous.CheckpointBlock>canonical.BlockNumber {
        return ILNReplica{},ErrILNReplicaUnverified
    }
    // A canonical unchanged root AND count needs no logs, even if the old
    // validator missed a very long interval. Verify all leaves against root.
    if uint64(len(previous.Leaves))==canonical.Count {
        return ExtendILNReplica(canonical,previous,nil)
    }
    if previous.CheckpointBlock==canonical.BlockNumber {
        return ILNReplica{},ErrILNReplicaPeerRequired
    }
    // Long idle periods do not imply missing leaves: search only the
    // bounded recent *confirmed* range, and verify a complete suffix.
    fromBlock:=previous.CheckpointBlock+1
    client,err:=ilnSourceClient(source)
    if err!=nil{return ILNReplica{},err}
    confirmed,err:=confirmedILNHead(client,source)
    if err!=nil{return ILNReplica{},err}
    // The canonical Hook root/count was observed at 'latest' and anchored at
    // its observation head. That timestamp is NOT an already finalized head.
    // Use only independently confirmed log blocks to reconstruct new leaves;
    // the existing per-prefix finality gate separately matures the validated
    // result before any BLS vote can be issued.
    toBlock:=canonical.BlockNumber
    if toBlock>confirmed {toBlock=confirmed}
    if toBlock<=previous.CheckpointBlock {
        return ILNReplica{},ErrILNReplicaPeerRequired
    }
    if toBlock-fromBlock+1>maxILNReplicaRecentBlocks {
        fromBlock=toBlock-maxILNReplicaRecentBlocks+1
    }
    topic:=ethgo.Hash(crypto.Keccak256Hash([]byte("InsertedIntoTree(bytes32,uint32)")))
    filter:=&ethgo.LogFilter{
        Address:[]ethgo.Address{ethgo.Address(canonical.Hook)},
        Topics:[][]*ethgo.Hash{{&topic}},
    }
    filter.SetFromUint64(fromBlock)
    filter.SetToUint64(toBlock)
    logs,err:=client.Eth().GetLogs(filter)
    if err!=nil {return ILNReplica{},fmt.Errorf("%w: recent Hook logs not available: %v",ErrILNReplicaPeerRequired,err)}
    sort.SliceStable(logs,func(i,j int)bool{
        if logs[i].BlockNumber!=logs[j].BlockNumber{return logs[i].BlockNumber<logs[j].BlockNumber}
        return logs[i].LogIndex<logs[j].LogIndex
    })
    next:=uint64(len(previous.Leaves))
    chunks:=make([]ILNReplicaChunk,0)
    var lastBlock,lastLogIndex uint64
    for i,log:=range logs {
        if log==nil || log.Removed || log.BlockNumber<fromBlock ||
            log.BlockNumber>toBlock || types.Address(log.Address)!=canonical.Hook ||
            (i>0 && (log.BlockNumber<lastBlock ||
              (log.BlockNumber==lastBlock && log.LogIndex<=lastLogIndex))) {
            return ILNReplica{},ErrILNReplicaUnverified
        }
        leaf,index,parseErr:=parseILNInsertionLog(log)
        if parseErr!=nil || uint64(index)!=next || next>=canonical.Count {
            return ILNReplica{},ErrILNReplicaUnverified
        }
        if len(chunks)==0 || len(chunks[len(chunks)-1].Leaves)==1024 {
            chunks=append(chunks,ILNReplicaChunk{
                ChainID:source.ChainID,Hook:canonical.Hook,Offset:next,
            })
        }
        chunks[len(chunks)-1].Leaves=append(chunks[len(chunks)-1].Leaves,leaf)
        next++
        lastBlock,lastLogIndex=log.BlockNumber,log.LogIndex
    }
    // If the latest Hook includes a still-unconfirmed insertion, the
    // confirmed log window will be incomplete. Never promote partial data.
    // Retry after finality or recover from a peer with the complete root.
    if next!=canonical.Count {return ILNReplica{},ErrILNReplicaPeerRequired}
    return ExtendILNReplica(canonical,previous,chunks)
}
