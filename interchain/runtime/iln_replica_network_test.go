package runtime

import (
    "encoding/json"
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/hashicorp/go-hclog"
    "github.com/libp2p/go-libp2p/core/peer"
    "github.com/stretchr/testify/require"
    "google.golang.org/protobuf/types/known/wrapperspb"

    evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
    "github.com/xgr-network/xgr-node/network"
    "github.com/xgr-network/xgr-node/types"
    "github.com/xgr-network/xgr-node/crypto"
)

func TestILNReplicaThreeAuthenticatedLibp2pValidatorsE2E(t *testing.T) {
    if testing.Short(){t.Skip("real three-peer libp2p transport test")}
    servers:=make([]*network.Server,0,3)
    for i:=0;i<3;i++ {
        srv,err:=network.CreateServer(&network.CreateServerParams{
            ConfigCallback:func(c *network.Config){
                c.NoDiscover=true
                c.DataDir=t.TempDir()
                c.MaxInboundPeers=6
                c.MaxOutboundPeers=6
            },
            Logger:hclog.NewNullLogger(),
        })
        require.NoError(t,err)
        servers=append(servers,srv)
    }
    t.Cleanup(func(){
        for _,s:=range servers{require.NoError(t,s.Close())}
    })
    for _,err:=range network.MeshJoin(servers...){require.NoError(t,err)}

    logger:=hclog.NewNullLogger()
    workers:=make([]*Worker,3)
    for i,srv:=range servers {
        worker:=&Worker{
            logger:logger,network:srv,dataDir:t.TempDir(),
            ilnReplicaSync:make(map[string]*ilnReplicaSyncState),
            ilnReplicaServed:make(map[peer.ID]time.Time),
        }
        topic,err:=srv.NewTopic(TopicID,&wrapperspb.BytesValue{})
        require.NoError(t,err)
        worker.topic=topic
        workers[i]=worker
        workerLocal:=worker
        require.NoError(t,topic.Subscribe(func(obj interface{},from peer.ID){
            value,ok:=obj.(*wrapperspb.BytesValue)
            if !ok{return}
            _=workerLocal.handleWireFrom(value.Value,from)
        }))
    }

    hook:=types.StringToAddress("0x1111111111111111111111111111111111111111")
    leaves:=make([]types.Hash,768)
    // Build independent test Merkle prefix and root, not by trusting
    // the live replica assembler's own candidate.
    for i:=range leaves {
        leaves[i]=crypto.Keccak256Hash([]byte{byte(i>>8),byte(i)})
    }
    computeRoot:=func(hashes []types.Hash)types.Hash {
        // Standard Hyperlane append-only 32-depth Merkle tree, using
        // independent pair hashing. Tests the network, not Go consensus.
        nodes:=make([]types.Hash,len(hashes))
        copy(nodes,hashes)
        zero:=types.ZeroHash
        for level:=0;level<32;level++{
            next:=make([]types.Hash,0,(len(nodes)+1)/2)
            if len(nodes)%2!=0{nodes=append(nodes,zero)}
            if len(nodes)==0 {nodes=append(nodes,zero,zero)}
            for i:=0;i<len(nodes);i+=2{
                pair:=append(append([]byte(nil),nodes[i].Bytes()...),nodes[i+1].Bytes()...)
                next=append(next,crypto.Keccak256Hash(pair))
            }
            nodes=next
            z:=append(append([]byte(nil),zero.Bytes()...),zero.Bytes()...)
            zero=crypto.Keccak256Hash(z)
        }
        return nodes[0]
    }
    canonical:=evmInterchain.ILNReplicaCheckpoint{
        ChainID:8453,Hook:hook,Root:computeRoot(leaves),
        Count:uint64(len(leaves)),BlockNumber:128,
    }

    // Peer 0 is malicious: it is a valid libp2p identity but serves one
    // tampered leaf, so it must never overwrite local live state.
    // Peer 1 is honest and holds a verified data-dir snapshot.
    honest:=evmInterchain.ILNReplica{
        ChainID:canonical.ChainID,Hook:canonical.Hook,
        CheckpointBlock:canonical.BlockNumber,Root:canonical.Root,Leaves:leaves,
    }
    require.NoError(t,evmInterchain.StoreVerifiedILNReplica(workers[1].ilnReplicaPath(canonical),honest,canonical))
    key:=ilnReplicaKey(canonical)
    workers[2].ilnReplicaSync[key]=&ilnReplicaSyncState{
        canonical:canonical,attempts:make(map[peer.ID]*ilnReplicaPeerAttempt),
    }

    // Advertise the exact canonical checkpoint to both peers. Each
    // response is authenticated by pubsub's libp2p sender ID; an attacker
    // cannot claim another responder's identity.
    time.Sleep(1300*time.Millisecond) // GossipSub mesh formation
    badID:=servers[0].AddrInfo().ID
    goodID:=servers[1].AddrInfo().ID
    client:=workers[2]
    requestBad,ok:=client.nextILNReplicaPeerRequest(key,badID)
    require.True(t,ok)
    require.NoError(t,client.publish(wireEnvelope{
        Type:"iln_replica_request",ILNReplicaRequest:&requestBad,
    }))
    // Authenticated but corrupt reply crafted by peer 0.
    tampered:=append([]types.Hash(nil),leaves[:ilnReplicaWireLeaves]...)
    tampered[7]=crypto.Keccak256Hash([]byte("poison"))
    responseBad:=ilnReplicaChunkWire{
        ilnReplicaRequestWire:requestBad,
        Target:servers[2].AddrInfo().ID.String(),Leaves:tampered,
    }
    require.NoError(t,workers[0].publish(wireEnvelope{
        Type:"iln_replica_chunk",ILNReplicaChunk:&responseBad,
    }))
    // Honest peer 1 serves chunks over the SAME real libp2p topic.
    deadline:=time.Now().Add(30*time.Second)
    for time.Now().Before(deadline){
        if restored,err:=evmInterchain.LoadVerifiedILNReplica(client.ilnReplicaPath(canonical),canonical);err==nil {
            require.Equal(t,leaves,restored.Leaves)
            // A new validator restarts from the data-dir snapshot and does
            // not require ANY archived source-transaction logs.
            _,err=evmInterchain.BuildILNReplicaProof(restored,canonical,431)
            require.NoError(t,err)
            raw,err:=os.ReadFile(client.ilnReplicaPath(canonical))
            require.NoError(t,err)
            var file evmInterchain.ILNReplica
            require.NoError(t,json.Unmarshal(raw,&file))
            file.Leaves[0]=crypto.Keccak256Hash([]byte("disk-corrupted"))
            poisoned,_:=json.Marshal(file)
            require.NoError(t,os.WriteFile(client.ilnReplicaPath(canonical),poisoned,0o660))
            _,err=evmInterchain.LoadVerifiedILNReplica(client.ilnReplicaPath(canonical),canonical)
            require.Error(t,err,"corrupted persistent replica must fail closed")
            return
        }
        if request,ok:=client.nextILNReplicaPeerRequest(key,goodID);ok {
            require.NoError(t,client.publish(wireEnvelope{
                Type:"iln_replica_request",ILNReplicaRequest:&request,
            }))
        }
        time.Sleep(150*time.Millisecond)
    }
    _,err:=os.Stat(filepath.Join(client.ilnDir(),"replicas","8453",hook.String()+".json"))
    require.NoError(t,err,"honest peer should complete an authenticated cold join despite poisoned replica peer")
}

func TestV322ReplicaPeerSlotsRotateWithoutRelaxingCanonicalRoot(t *testing.T) {
    canonical:=evmInterchain.ILNReplicaCheckpoint{
        ChainID:42161,Hook:types.StringToAddress("0x1111111111111111111111111111111111111111"),
        Root:crypto.Keccak256Hash([]byte("canonical")),Count:1,BlockNumber:128,
    }
    w:=&Worker{ilnReplicaSync:map[string]*ilnReplicaSyncState{}}
    key:=ilnReplicaKey(canonical)
    w.ilnReplicaSync[key]=&ilnReplicaSyncState{
        canonical:canonical,attempts:make(map[peer.ID]*ilnReplicaPeerAttempt),
    }
    ids:=[]peer.ID{"peer-a","peer-b","peer-c","peer-honest"}
    for _,id:=range ids[:3]{
        _,ok:=w.nextILNReplicaPeerRequest(key,id)
        require.True(t,ok)
    }
    _,ok:=w.nextILNReplicaPeerRequest(key,ids[3])
    require.False(t,ok,"peer set stays bounded during the in-flight window")
    for _,id:=range ids[:3]{
        w.ilnReplicaSync[key].attempts[id].lastRequested=time.Now().Add(-31*time.Second)
    }
    request,ok:=w.nextILNReplicaPeerRequest(key,ids[3])
    require.True(t,ok,"honest peer cannot remain permanently starved")
    require.Equal(t,canonical.Root,request.Root)
    require.Equal(t,canonical.Count,request.Count)
    require.Equal(t,canonical.BlockNumber,request.CheckpointBlock)
    require.Len(t,w.ilnReplicaSync[key].attempts,ilnReplicaMaxPeers)
}

func TestV322ReplicaContentIdentitySurvivesIndependentEpochAdvancement(t *testing.T) {
    root:=crypto.Keccak256Hash([]byte("source-root"))
    original:=evmInterchain.ILNReplicaCheckpoint{
        ChainID:42161,
        Hook:types.StringToAddress("0x1111111111111111111111111111111111111111"),
        Root:root,Count:257,BlockNumber:128,
    }
    newer:=original
    newer.BlockNumber=16384
    require.True(t,sameILNReplicaContent(original,newer))
    newer.Root=crypto.Keccak256Hash([]byte("different-root"))
    require.False(t,sameILNReplicaContent(original,newer))
    newer=original
    newer.Count++
    require.False(t,sameILNReplicaContent(original,newer))
    newer=original
    newer.ChainID=8453
    require.False(t,sameILNReplicaContent(original,newer))
    newer=original
    newer.Hook=types.StringToAddress("0x2222222222222222222222222222222222222222")
    require.False(t,sameILNReplicaContent(original,newer))
}

func TestV322ReplicaPeerSelectionTraversesEntirePeerSet(t *testing.T) {
    canonical:=evmInterchain.ILNReplicaCheckpoint{
        ChainID:42161,Hook:types.StringToAddress("0x1111111111111111111111111111111111111111"),
        Root:crypto.Keccak256Hash([]byte("root")),Count:1,BlockNumber:128,
    }
    w:=&Worker{ilnReplicaSync:map[string]*ilnReplicaSyncState{}}
    key:=ilnReplicaKey(canonical)
    w.ilnReplicaSync[key]=&ilnReplicaSyncState{canonical:canonical}
    visited:=map[int]bool{}
    for i:=0;i<10;i++{
        start:=w.nextILNReplicaPeerStart(key,5)
        for j:=0;j<ilnReplicaMaxPeers;j++{visited[(start+j)%5]=true}
    }
    require.Len(t,visited,5,"all connected peers must be traversed")
}
