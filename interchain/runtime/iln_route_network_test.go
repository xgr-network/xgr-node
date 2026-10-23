package runtime

import (
    "fmt"
    "math/big"
    "testing"
    "time"

    "github.com/hashicorp/go-hclog"
    "github.com/libp2p/go-libp2p/core/peer"
    "github.com/stretchr/testify/require"
    "google.golang.org/protobuf/types/known/wrapperspb"

    protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
    "github.com/xgr-network/xgr-node/interchain/evm"
    "github.com/xgr-network/xgr-node/network"
    "github.com/xgr-network/xgr-node/types"
)

func TestILNRoutePeerDiscoveryThreeValidatorsWithoutHistoricalLogs(t *testing.T){
    if testing.Short(){t.Skip("real three-validator route synchronization test")}
    validators:=make([]*network.Server,0,3)
    for i:=0;i<3;i++{
        server,err:=network.CreateServer(&network.CreateServerParams{
            ConfigCallback:func(c *network.Config){
                c.NoDiscover=true;c.DataDir=t.TempDir()
                c.MaxInboundPeers=6;c.MaxOutboundPeers=6
            },Logger:hclog.NewNullLogger(),
        })
        require.NoError(t,err)
        validators=append(validators,server)
    }
    t.Cleanup(func(){for _,v:=range validators{require.NoError(t,v.Close())}})
    for _,err:=range network.MeshJoin(validators...){require.NoError(t,err)}

    registry:=types.StringToAddress("0x5555555555555555555555555555555555555555")
    gateway:=types.StringToAddress("0x2222222222222222222222222222222222222222")
    routeID:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
    unknown:=types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
    src:=&evm.Destination{Name:"base",ChainID:8453,Domain:8453,
        ILNRegistryAddress:registry.String()}
    dst:=&evm.Destination{Name:"xgr",ChainID:1643,Domain:1643}
    canonical:=protocol.ILNRoute{
        Key:protocol.ILNRouteKey{
            SourceChainID:8453,SourceDomain:8453,DestinationDomain:1643,RouteID:routeID,
        },
        Gateway:gateway,
        SourceRouter:types.StringToAddress("0x7777777777777777777777777777777777777777"),
        Mailbox:types.StringToAddress("0x3333333333333333333333333333333333333333"),
        DispatchHook:types.StringToAddress("0x4444444444444444444444444444444444444444"),
        DestinationRouter:types.StringToAddress("0x6666666666666666666666666666666666666666"),
        ValidatorFeeWei:big.NewInt(1000),Enabled:true,
    }
    oldHead,oldRoute,oldRouteAdds:=getConfirmedILNHead,getILNRouteAtBlock,getConfirmedILNRouteAdds
    defer func(){
        getConfirmedILNHead=oldHead
        getILNRouteAtBlock=oldRoute
        getConfirmedILNRouteAdds=oldRouteAdds
    }()
    getConfirmedILNHead=func(*evm.Destination)(uint64,error){return 128,nil}
    getILNRouteAtBlock=func(_ *evm.Destination,domain uint32,id types.Hash,at uint64)(*evm.ILNRouteSnapshot,error){
        if at!=128||domain!=1643||id!=routeID{return nil,fmt.Errorf("unverified canonical route")}
        return &evm.ILNRouteSnapshot{
            Registry:registry,Route:canonical,BlockNumber:at,
        },nil
    }
    // This test disallows genesis RouteAdded event scans completely.
    getConfirmedILNRouteAdds=func(*evm.Destination,uint64,uint64)([]evm.ILNRouteAdded,error){
        t.Error("historical RouteAdded should not be needed for P2P synchronization")
        return nil,fmt.Errorf("unverified canonical route")
    }

    workers:=make([]*Worker,0,3)
    for i,server:=range validators {
        routes:=make(map[string]*evm.CheckpointRoute)
        if i==0 {
            routes["base_to_xgr"]=&evm.CheckpointRoute{
                Name:"base_to_xgr",SourceNetwork:"base",Destination:"xgr",RouteID:routeID,
            }
        }
        worker:=&Worker{
            dataDir:t.TempDir(),logger:hclog.NewNullLogger(),network:server,
            destinations:map[string]*evm.Destination{"base":src,"xgr":dst},
            routes:routes,
        }
        topic,err:=server.NewTopic(TopicID,&wrapperspb.BytesValue{})
        require.NoError(t,err)
        worker.topic=topic
        subscribedWorker:=worker
        require.NoError(t,topic.Subscribe(func(obj interface{},from peer.ID){
            value,ok:=obj.(*wrapperspb.BytesValue)
            if !ok||value==nil{return}
            _=subscribedWorker.handleWireFrom(value.Value,from)
        }))
        workers=append(workers,worker)
    }
    time.Sleep(1300*time.Millisecond)
    workers[0].publishVerifiedILNRoutes()

    deadline:=time.Now().Add(15*time.Second)
    for time.Now().Before(deadline) {
        workers[1].mu.Lock();gotOne:=len(workers[1].routes)
        workers[1].mu.Unlock()
        workers[2].mu.Lock();gotTwo:=len(workers[2].routes)
        workers[2].mu.Unlock()
        if gotOne==1&&gotTwo==1{break}
        time.Sleep(150*time.Millisecond)
    }
    for _,i:=range []int{1,2} {
        w:=workers[i]
        w.mu.Lock()
        require.Len(t,w.routes,1,"late validator must learn from verified libp2p peers")
        w.mu.Unlock()
        entries:=w.ilnRouteCatalogDir(src)
        require.DirExists(t,entries)
        // Cold restart: confirmed route catalog restored from the usual
        // data/interchain directory, without archived source log replay.
        restarted:=&Worker{
            dataDir:w.dataDir,destinations:w.destinations,
            routes:make(map[string]*evm.CheckpointRoute),
        }
        require.NoError(t,restarted.restoreILNDiscoveredRoutes(src,128))
        require.Len(t,restarted.routes,1)
    }
    // A malicious peer cannot fabricate a route or impersonate another
    // gateway even with a valid authenticated libp2p sender identity.
    time.Sleep(300*time.Millisecond)
    malicious:=ilnRouteNoticeWire{
        SourceNetwork:"base",SourceDomain:8453,DestinationDomain:1643,
        RouteID:unknown,Gateway:gateway,
    }
    require.Error(t,workers[1].acceptVerifiedILNRouteNotice(
        validators[0].AddrInfo().ID,malicious))
    require.Len(t,workers[1].routes,1)
    time.Sleep(300*time.Millisecond)
    malicious.RouteID=routeID
    malicious.Gateway=types.StringToAddress("0x9999999999999999999999999999999999999999")
    require.ErrorContains(t,workers[1].acceptVerifiedILNRouteNotice(
        validators[0].AddrInfo().ID,malicious),"not authorized")
    require.Len(t,workers[1].routes,1)
}
