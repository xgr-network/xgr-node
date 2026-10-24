package runtime

import (
 "fmt"
 "math/big"
 "testing"
 "os"
 "path/filepath"

 "github.com/stretchr/testify/require"
 protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
 "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

func TestILNDiscoverOnlyCanonicalPermissionlessRoutesAndPersistCursor(t *testing.T) {
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
 _,exists,err:=w.readILNCursor(ilnDiscoveryCursorName(source))
 require.NoError(t,err)
 require.False(t,exists)
}

func TestILNDiscoveryRetryOnTransientRegistryFailure(t *testing.T) {
 routeID:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 gateway:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 source:=&evm.Destination{Name:"base",ChainID:8453,Domain:8453,ILNRegistryAddress:"0x5555555555555555555555555555555555555555",ILNRegistryActivationBlock:99}
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
 _,exists,err:=w.readILNCursor(ilnDiscoveryCursorName(source))
 require.NoError(t,err)
 require.False(t,exists,"must not skip confirmed events during transient RPC faults")
 require.Empty(t,w.routes)
 fail=false
 require.NoError(t,w.discoverILNRoutesFromSource(source))
 require.Equal(t,2,calls)
 require.Len(t,w.routes,1)
 cursor,exists,err:=w.readILNCursor(ilnDiscoveryCursorName(source))
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

func TestILNDiscoveryRecoversPersistedL1HeightOnL2(t *testing.T) {
 source:=&evm.Destination{Name:"rollup",ChainID:42161,Domain:42161,ILNRegistryAddress:"0x5555555555555555555555555555555555555555"}
 w:=&Worker{dataDir:t.TempDir(),destinations:map[string]*evm.Destination{"rollup":source},routes:make(map[string]*evm.CheckpointRoute)}
 oldHead,oldAdds,oldActivation:=getConfirmedILNHead,getConfirmedILNRouteAdds,getILNRegistryActivationBlock
 defer func(){getConfirmedILNHead=oldHead;getConfirmedILNRouteAdds=oldAdds;getILNRegistryActivationBlock=oldActivation}()
 getConfirmedILNHead=func(*evm.Destination)(uint64,error){return 517578687,nil}
 getILNRegistryActivationBlock=func(*evm.Destination)(uint64,error){return 24000000,nil}
 require.NoError(t,w.ensureILNRouteCatalogVersion(source))
 require.NoError(t,w.writeILNCursor(ilnDiscoveryCursorName(source),517578688))
 calls:=0
 getConfirmedILNRouteAdds=func(_ *evm.Destination,from,to uint64)([]evm.ILNRouteAdded,error){
  calls++
  require.Equal(t,uint64(517577688),from,"cross-domain cursor must not be trusted")
  require.Equal(t,uint64(517578687),to)
  return nil,nil
 }
 require.NoError(t,w.discoverILNRoutesFromSource(source))
 require.Equal(t,1,calls)
 cursor,exists,err:=w.readILNCursor(ilnDiscoveryCursorName(source))
 require.NoError(t,err)
 require.True(t,exists)
 require.Equal(t,uint64(517578687),cursor)
}

// Re-deploying a source registry must not restore the retired generation's
// route catalog or reuse its cursor, even on the same EVM chain.
func TestILNDiscoveryIsolatesNewSourceRegistryGeneration(t *testing.T) {
 oldRegistry:="0x5555555555555555555555555555555555555555"
 newRegistry:="0x7777777777777777777777777777777777777777"
 oldRoute:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 newRoute:=types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
 oldGateway:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 newGateway:=types.StringToAddress("0x2222222222222222222222222222222222222222")
 source:=&evm.Destination{Name:"base",ChainID:8453,Domain:8453,ILNRegistryAddress:oldRegistry}
 dst:=&evm.Destination{Name:"xgr",ChainID:1643,Domain:1643}
 dir:=t.TempDir()
 oldHead,oldAdds,oldRouteLookup:=getConfirmedILNHead,getConfirmedILNRouteAdds,getILNRouteAtBlock
 defer func(){getConfirmedILNHead=oldHead;getConfirmedILNRouteAdds=oldAdds;getILNRouteAtBlock=oldRouteLookup}()
 getConfirmedILNHead=func(*evm.Destination)(uint64,error){return 100,nil}
 scans:=0
 getConfirmedILNRouteAdds=func(_ *evm.Destination,_,_ uint64)([]evm.ILNRouteAdded,error){
  scans++
  if source.ILNRegistryAddress==oldRegistry {
   return []evm.ILNRouteAdded{{DestinationDomain:1643,RouteID:oldRoute,Gateway:oldGateway,BlockNumber:90}},nil
  }
  return []evm.ILNRouteAdded{{DestinationDomain:1643,RouteID:newRoute,Gateway:newGateway,BlockNumber:95}},nil
 }
 getILNRouteAtBlock=func(_ *evm.Destination,_ uint32,id types.Hash,_ uint64)(*evm.ILNRouteSnapshot,error){
  if source.ILNRegistryAddress==newRegistry&&id==oldRoute {
   return nil,fmt.Errorf("retired registry route was read")
  }
  gateway:=oldGateway
  if id==newRoute{gateway=newGateway}
  return &evm.ILNRouteSnapshot{Route:protocol.ILNRoute{Gateway:gateway}},nil
 }
 before:=&Worker{dataDir:dir,destinations:map[string]*evm.Destination{"base":source,"xgr":dst},routes:make(map[string]*evm.CheckpointRoute)}
 require.NoError(t,before.discoverILNRoutesFromSource(source))
 oldCursorName:=ilnDiscoveryCursorName(source)
 require.FileExists(t,filepath.Join(before.ilnRouteCatalogDir(source),"1643-"+oldRoute.String()[2:]+".json"))
 require.Len(t,before.routes,1)
 source.ILNRegistryAddress=newRegistry
 after:=&Worker{dataDir:dir,destinations:map[string]*evm.Destination{"base":source,"xgr":dst},routes:make(map[string]*evm.CheckpointRoute)}
 require.NotEqual(t,oldCursorName,ilnDiscoveryCursorName(source))
 require.NoError(t,after.discoverILNRoutesFromSource(source))
 require.Equal(t,2,scans,"new Registry must bootstrap its own discovery cursor")
 require.Len(t,after.routes,1)
 for _,route:=range after.routes{require.Equal(t,newRoute,route.RouteID)}
 require.FileExists(t,filepath.Join(after.ilnRouteCatalogDir(source),"1643-"+newRoute.String()[2:]+".json"))
 require.NoError(t,after.discoverILNRoutesFromSource(source))
 require.Equal(t,2,scans,"new generation cursor must persist over restart")
}

func TestILNHintDiscoveryRequiresCanonicalRegistryProof(t *testing.T) {
 routeID:=types.StringToHash("0x"+fmt.Sprintf("%064x",98765))
 gateway:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 registry:=types.StringToAddress("0x5555555555555555555555555555555555555555")
 source:=&evm.Destination{Name:"rollup",ChainID:42161,Domain:42161,ILNRegistryAddress:registry.String()}
 dst:=&evm.Destination{Name:"xgr",ChainID:1643,Domain:1643}
 w:=&Worker{dataDir:t.TempDir(),destinations:map[string]*evm.Destination{"rollup":source,"xgr":dst},routes:make(map[string]*evm.CheckpointRoute)}
 name:=fmt.Sprintf("iln_42161_1643_%s",routeID.String()[2:])
 hintDir:=filepath.Join(w.ilnDir(),"route-hints")
 require.NoError(t,os.MkdirAll(hintDir,0o770))
 require.NoError(t,os.WriteFile(filepath.Join(hintDir,name+".json"),[]byte("{}"),0o660))
 oldHead,oldRoute:=getConfirmedILNHead,getILNRouteAtBlock
 defer func(){getConfirmedILNHead=oldHead;getILNRouteAtBlock=oldRoute}()
 getConfirmedILNHead=func(*evm.Destination)(uint64,error){return 517578687,nil}
 approved:=false
 getILNRouteAtBlock=func(_ *evm.Destination,d uint32,id types.Hash,head uint64)(*evm.ILNRouteSnapshot,error){
  require.Equal(t,uint32(1643),d)
  require.Equal(t,routeID,id)
  require.Equal(t,uint64(517578687),head)
  if !approved{return nil,fmt.Errorf("source route is not canonical")}
  return &evm.ILNRouteSnapshot{Registry:registry,BlockNumber:head,Route:protocol.ILNRoute{
   Key:protocol.ILNRouteKey{SourceChainID:42161,SourceDomain:42161,DestinationDomain:1643,RouteID:routeID},
   Gateway:gateway,SourceRouter:types.StringToAddress("0x2222222222222222222222222222222222222222"),
   Mailbox:types.StringToAddress("0x3333333333333333333333333333333333333333"),
   DispatchHook:types.StringToAddress("0x4444444444444444444444444444444444444444"),
   DestinationRouter:types.StringToAddress("0x6666666666666666666666666666666666666666"),
   ValidatorFeeWei:big.NewInt(1),Enabled:true,
  }},nil
 }
 w.discoverILNHintedRoutes()
 require.Empty(t,w.routes)
 _,err:=os.Stat(filepath.Join(w.ilnDir(),"registered-routes",name+".route"))
 require.True(t,os.IsNotExist(err))
 approved=true
 w.discoverILNHintedRoutes()
 require.Contains(t,w.routes,name)
 require.FileExists(t,filepath.Join(w.ilnDir(),"registered-routes",name+".route"))
 _,err=os.Stat(filepath.Join(hintDir,name+".json"))
 require.True(t,os.IsNotExist(err))
}

func TestILNRPCDiscoveryCursorMigrationRejectsNumericallyPlausibleL1Height(t *testing.T) {
 source:=&evm.Destination{Name:"arbitrum",ChainID:42161,Domain:42161,ILNRegistryAddress:"0x5555555555555555555555555555555555555555"}
 w:=&Worker{dataDir:t.TempDir(),destinations:map[string]*evm.Destination{"arbitrum":source},routes:make(map[string]*evm.CheckpointRoute)}
 oldHead,oldAdds:=getConfirmedILNHead,getConfirmedILNRouteAdds
 defer func(){getConfirmedILNHead=oldHead;getConfirmedILNRouteAdds=oldAdds}()
 getConfirmedILNHead=func(*evm.Destination)(uint64,error){return 517578687,nil}
 require.NoError(t,w.ensureILNRouteCatalogVersion(source))
 require.NoError(t,w.writeILNCursor(ilnDiscoveryCursorName(source),24000000))
 scans:=0
 getConfirmedILNRouteAdds=func(_ *evm.Destination,from,to uint64)([]evm.ILNRouteAdded,error){
  scans++
  require.Equal(t,uint64(517577688),from)
  require.Equal(t,uint64(517578687),to)
  return nil,nil
 }
 require.NoError(t,w.discoverILNRoutesFromSource(source))
 require.Equal(t,1,scans)
 require.NoError(t,w.discoverILNRoutesFromSource(source))
 require.Equal(t,1,scans,"migrated cursor must not repeatedly rescan")
}
