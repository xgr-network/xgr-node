package runtime

import (
 "fmt"
 "testing"
 "os"
 "path/filepath"

 "github.com/stretchr/testify/require"
 protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
 "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

func TestILNDiscoverOnlyGovernedCanonicalRoutesAndPersistCursor(t *testing.T) {
 routeID:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 gateway:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 source:=&evm.Destination{Name:"base",ChainID:8453,Domain:8453,ILNRegistryAddress:"0x5555555555555555555555555555555555555555"}
 destination:=&evm.Destination{Name:"xgr",ChainID:1643,Domain:1643}
 w:=&Worker{dataDir:t.TempDir(),destinations:map[string]*evm.Destination{"base":source,"xgr":destination},routes:make(map[string]*evm.CheckpointRoute)}
 oldHead,oldAdds,oldRoute,oldActivation:=getConfirmedILNHead,getConfirmedILNRouteAdds,getILNRouteAtBlock,getILNRegistryActivationBlock
 defer func(){getConfirmedILNHead=oldHead;getConfirmedILNRouteAdds=oldAdds;getILNRouteAtBlock=oldRoute;getILNRegistryActivationBlock=oldActivation}()
 getILNRegistryActivationBlock=func(*evm.Destination)(uint64,error){return 1,nil}
 getConfirmedILNHead=func(*evm.Destination)(uint64,error){return 100,nil}
 reads:=0
 getConfirmedILNRouteAdds=func(*evm.Destination,uint64,uint64)([]evm.ILNRouteAdded,error){
  reads++
  return []evm.ILNRouteAdded{{DestinationDomain:1643,RouteID:routeID,Gateway:gateway,BlockNumber:50}},nil
 }
 getILNRouteAtBlock=func(*evm.Destination,uint32,types.Hash,uint64)(*evm.ILNRouteSnapshot,error){
  return &evm.ILNRouteSnapshot{Route:protocol.ILNRoute{Gateway:gateway}},nil
 }
 require.NoError(t,w.discoverILNRoutesFromSource(source))
 require.Len(t,w.routes,1)
 for name,route:=range w.routes {
  require.Equal(t,"iln_8453_1643_"+routeID.String()[2:],name)
  require.Equal(t,routeID,route.RouteID)
  require.Equal(t,"base",route.SourceNetwork)
  indexed,err:=os.ReadFile(filepath.Join(w.ilnDir(),"registered-routes",name+".route"))
  require.NoError(t,err)
  require.Equal(t,routeID.String()+"\n",string(indexed))
 }
 require.Equal(t,1,reads)
 require.NoError(t,w.discoverILNRoutesFromSource(source))
 require.Equal(t,1,reads,"restarting the discovery loop must not rescan confirmed blocks")
}

func TestILNDiscoveryRejectsMismatchedGatewayAndDoesNotAdvance(t *testing.T) {
 routeID:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 source:=&evm.Destination{Name:"base",ChainID:8453,Domain:8453,ILNRegistryAddress:"0x5555555555555555555555555555555555555555"}
 w:=&Worker{dataDir:t.TempDir(),destinations:map[string]*evm.Destination{"base":source,"xgr":{Name:"xgr",Domain:1643}},routes:make(map[string]*evm.CheckpointRoute)}
 oldHead,oldAdds,oldRoute,oldActivation:=getConfirmedILNHead,getConfirmedILNRouteAdds,getILNRouteAtBlock,getILNRegistryActivationBlock
 defer func(){getConfirmedILNHead=oldHead;getConfirmedILNRouteAdds=oldAdds;getILNRouteAtBlock=oldRoute;getILNRegistryActivationBlock=oldActivation}()
 getILNRegistryActivationBlock=func(*evm.Destination)(uint64,error){return 1,nil}
 getConfirmedILNHead=func(*evm.Destination)(uint64,error){return 100,nil}
 getConfirmedILNRouteAdds=func(*evm.Destination,uint64,uint64)([]evm.ILNRouteAdded,error){return []evm.ILNRouteAdded{{DestinationDomain:1643,RouteID:routeID,Gateway:types.StringToAddress("0x1111111111111111111111111111111111111111"),BlockNumber:50}},nil}
 getILNRouteAtBlock=func(*evm.Destination,uint32,types.Hash,uint64)(*evm.ILNRouteSnapshot,error){return &evm.ILNRouteSnapshot{Route:protocol.ILNRoute{Gateway:types.StringToAddress("0x2222222222222222222222222222222222222222")}},nil}
 require.ErrorContains(t,w.discoverILNRoutesFromSource(source),"gateway mismatch")
 require.Empty(t,w.routes)
 _,exists,err:=w.readILNCursor("_discovery_base")
 require.NoError(t,err)
 require.False(t,exists)
}

func TestILNDiscoveryRetryOnTransientRegistryFailure(t *testing.T) {
 routeID:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 gateway:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 source:=&evm.Destination{Name:"base",ChainID:8453,Domain:8453,ILNRegistryAddress:"0x5555555555555555555555555555555555555555"}
 destination:=&evm.Destination{Name:"xgr",ChainID:1643,Domain:1643}
 w:=&Worker{dataDir:t.TempDir(),destinations:map[string]*evm.Destination{"base":source,"xgr":destination},routes:make(map[string]*evm.CheckpointRoute)}
 oldHead,oldAdds,oldRoute,oldActivation:=getConfirmedILNHead,getConfirmedILNRouteAdds,getILNRouteAtBlock,getILNRegistryActivationBlock
 defer func(){getConfirmedILNHead=oldHead;getConfirmedILNRouteAdds=oldAdds;getILNRouteAtBlock=oldRoute;getILNRegistryActivationBlock=oldActivation}()
 getConfirmedILNHead=func(*evm.Destination)(uint64,error){return 100,nil}
 getILNRegistryActivationBlock=func(*evm.Destination)(uint64,error){return 99,nil}
 calls:=0
 getConfirmedILNRouteAdds=func(_ *evm.Destination,from,to uint64)([]evm.ILNRouteAdded,error){
  require.Equal(t,uint64(99),from)
  require.Equal(t,uint64(100),to)
  calls++
  return []evm.ILNRouteAdded{{DestinationDomain:1643,RouteID:routeID,Gateway:gateway,BlockNumber:100}},nil
 }
 fail:=true
 getILNRouteAtBlock=func(*evm.Destination,uint32,types.Hash,uint64)(*evm.ILNRouteSnapshot,error){
  if fail{return nil,fmt.Errorf("temporary source RPC error")}
  return &evm.ILNRouteSnapshot{Route:protocol.ILNRoute{Gateway:gateway}},nil
 }
 require.ErrorContains(t,w.discoverILNRoutesFromSource(source),"temporary source RPC")
 _,exists,err:=w.readILNCursor("_discovery_base")
 require.NoError(t,err)
 require.False(t,exists,"must not skip confirmed events during transient RPC faults")
 require.Empty(t,w.routes)
 fail=false
 require.NoError(t,w.discoverILNRoutesFromSource(source))
 require.Equal(t,2,calls)
 require.Len(t,w.routes,1)
 cursor,exists,err:=w.readILNCursor("_discovery_base")
 require.NoError(t,err)
 require.True(t,exists)
 require.Equal(t,uint64(100),cursor)
}


func TestILNDiscoveryRestoresRouteAfterWorkerRestart(t *testing.T) {
 routeID:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 gateway:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 source:=&evm.Destination{Name:"base",ChainID:8453,Domain:8453,ILNRegistryAddress:"0x5555555555555555555555555555555555555555"}
 dest:=&evm.Destination{Name:"xgr",ChainID:1643,Domain:1643}
 dir:=t.TempDir()
 oldHead,oldAdds,oldRoute,oldActivation:=getConfirmedILNHead,getConfirmedILNRouteAdds,getILNRouteAtBlock,getILNRegistryActivationBlock
 defer func(){getConfirmedILNHead=oldHead;getConfirmedILNRouteAdds=oldAdds;getILNRouteAtBlock=oldRoute;getILNRegistryActivationBlock=oldActivation}()
 getConfirmedILNHead=func(*evm.Destination)(uint64,error){return 100,nil}
 getILNRegistryActivationBlock=func(*evm.Destination)(uint64,error){return 1,nil}
 scans:=0
 getConfirmedILNRouteAdds=func(*evm.Destination,uint64,uint64)([]evm.ILNRouteAdded,error){scans++;return []evm.ILNRouteAdded{{DestinationDomain:1643,RouteID:routeID,Gateway:gateway,BlockNumber:50}},nil}
 getILNRouteAtBlock=func(*evm.Destination,uint32,types.Hash,uint64)(*evm.ILNRouteSnapshot,error){
  return &evm.ILNRouteSnapshot{Route:protocol.ILNRoute{Gateway:gateway}},nil
 }
 first:=&Worker{dataDir:dir,destinations:map[string]*evm.Destination{"base":source,"xgr":dest},routes:make(map[string]*evm.CheckpointRoute)}
 require.NoError(t,first.discoverILNRoutesFromSource(source))
 require.Equal(t,1,scans)
 restarted:=&Worker{dataDir:dir,destinations:map[string]*evm.Destination{"base":source,"xgr":dest},routes:make(map[string]*evm.CheckpointRoute)}
 require.NoError(t,restarted.discoverILNRoutesFromSource(source))
 require.Len(t,restarted.routes,1)
 require.Equal(t,1,scans,"no full rescanning is needed after a clean restart")
}

func TestILNDiscoveryRemembersUnknownDestinationUntilChainConfigured(t *testing.T) {
 routeID:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 gateway:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 source:=&evm.Destination{Name:"base",ChainID:8453,Domain:8453,ILNRegistryAddress:"0x5555555555555555555555555555555555555555"}
 dir:=t.TempDir()
 oldHead,oldAdds,oldRoute,oldActivation:=getConfirmedILNHead,getConfirmedILNRouteAdds,getILNRouteAtBlock,getILNRegistryActivationBlock
 defer func(){getConfirmedILNHead=oldHead;getConfirmedILNRouteAdds=oldAdds;getILNRouteAtBlock=oldRoute;getILNRegistryActivationBlock=oldActivation}()
 getConfirmedILNHead=func(*evm.Destination)(uint64,error){return 100,nil}
 getILNRegistryActivationBlock=func(*evm.Destination)(uint64,error){return 1,nil}
 scans:=0
 getConfirmedILNRouteAdds=func(*evm.Destination,uint64,uint64)([]evm.ILNRouteAdded,error){scans++;return []evm.ILNRouteAdded{{DestinationDomain:137,RouteID:routeID,Gateway:gateway,BlockNumber:50}},nil}
 getILNRouteAtBlock=func(*evm.Destination,uint32,types.Hash,uint64)(*evm.ILNRouteSnapshot,error){
  return &evm.ILNRouteSnapshot{Route:protocol.ILNRoute{Gateway:gateway}},nil
 }
 first:=&Worker{dataDir:dir,destinations:map[string]*evm.Destination{"base":source},routes:make(map[string]*evm.CheckpointRoute)}
 require.NoError(t,first.discoverILNRoutesFromSource(source))
 require.Empty(t,first.routes)
 later:=&Worker{dataDir:dir,destinations:map[string]*evm.Destination{"base":source,"polygon":{Name:"polygon",Domain:137}},routes:make(map[string]*evm.CheckpointRoute)}
 require.NoError(t,later.discoverILNRoutesFromSource(source))
 require.Len(t,later.routes,1)
 require.Equal(t,1,scans,"must restore from catalog even after the cursor advanced")
}
