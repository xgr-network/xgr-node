package runtime

import (
 "errors"
 "math/big"
 "testing"

 "github.com/stretchr/testify/require"
 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

func TestSharedILNObserverReducesExternalRequestsAndPreservesDurableCursors(t *testing.T){
 idA:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 idB:=types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
 tx:=types.StringToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
 mid:=types.StringToHash("0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
 addrA:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 addrB:=types.StringToAddress("0x2222222222222222222222222222222222222222")
 src:=&evm.Destination{Name:"base",ChainID:8453,Domain:8453,ILNRegistryActivationBlock:100,
  ILNRegistryAddress:"0x3333333333333333333333333333333333333333"}
 dst:=&evm.Destination{Name:"xgr",ChainID:1643,Domain:1643}
 routeA:=&evm.CheckpointRoute{Name:"a",RouteID:idA,SourceNetwork:"base",Destination:"xgr"}
 routeB:=&evm.CheckpointRoute{Name:"b",RouteID:idB,SourceNetwork:"base",Destination:"xgr"}
 w:=&Worker{dataDir:t.TempDir(),destinations:map[string]*evm.Destination{"base":src,"xgr":dst},
  routes:map[string]*evm.CheckpointRoute{"a":routeA,"b":routeB}}
 require.NoError(t,w.rememberILNRouteAdded(src,evm.ILNRouteAdded{RouteID:idA,Gateway:addrA,DestinationDomain:1643,BlockNumber:100}))
 require.NoError(t,w.rememberILNRouteAdded(src,evm.ILNRouteAdded{RouteID:idB,Gateway:addrB,DestinationDomain:1643,BlockNumber:100}))
 a,err:=w.knownObserverRoute(src,routeA);require.NoError(t,err)
 b,err:=w.knownObserverRoute(src,routeB);require.NoError(t,err)
 originalRemote,originalSigner:=openObserverWindow,observerSignMessage
 defer func(){openObserverWindow=originalRemote;observerSignMessage=originalSigner}()
 reads,signed:=0,0
 openObserverWindow=func(_ *evm.Destination)(observerWindowReader,func()){
  return func(gateways []types.Address,cursor,window uint64)(uint64,uint64,uint64,[]evm.ILNObservedGatewayOperation,error){
  reads++
  require.Len(t,gateways,2)
  require.Equal(t,uint64(99),cursor)
  require.Equal(t,uint64(256),window)
  op:=evm.ILNOperation{RouteID:idA,MessageID:mid,DestinationDomain:1643,
   ValidatorFeeWei:big.NewInt(1),BlockNumber:100,TransactionHash:tx,LogIndex:0}
  return 100,100,100,[]evm.ILNObservedGatewayOperation{{Gateway:addrA,Operation:op}},nil
  },func(){}
 }
 observerSignMessage=func(_ *Worker,r *evm.CheckpointRoute,_ types.Hash,_ uint64,tx types.Hash)error{
  require.Equal(t,"a",r.Name);signed++;return nil
 }
 require.NoError(t,w.scanSharedILNSource(src,[]observerRoute{a,b}))
 require.Equal(t,1,reads,"two routes must not double the source RPC query")
 require.Equal(t,1,signed)
 for _,route:=range []*evm.CheckpointRoute{routeA,routeB}{
  cur,exists,e:=w.readILNCursor("_direct_"+route.Name)
  require.NoError(t,e);require.True(t,exists);require.Equal(t,uint64(100),cur)
 }
}

func TestSharedILNObserverRejectsUnverifiedWorkWithoutAdvancingCursor(t *testing.T){
 id:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 msg:=types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
 tx:=types.StringToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
 gateway:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 source:=&evm.Destination{Name:"base",ChainID:8453,Domain:8453,ILNRegistryActivationBlock:10}
 route:=&evm.CheckpointRoute{Name:"a",RouteID:id,SourceNetwork:"base",Destination:"xgr"}
 w:=&Worker{dataDir:t.TempDir()}
 originalRemote,originalSigner:=openObserverWindow,observerSignMessage
 defer func(){openObserverWindow=originalRemote;observerSignMessage=originalSigner}()
 openObserverWindow=func(_ *evm.Destination)(observerWindowReader,func()){
  return func(_ []types.Address,cursor,window uint64)(uint64,uint64,uint64,[]evm.ILNObservedGatewayOperation,error){
  return 20,10,20,[]evm.ILNObservedGatewayOperation{{Gateway:gateway,
   Operation:evm.ILNOperation{RouteID:id,MessageID:msg,DestinationDomain:1643,
    BlockNumber:12,TransactionHash:tx,ValidatorFeeWei:big.NewInt(1)}}},nil
  },func(){}
 }
 observerSignMessage=func(_ *Worker,_ *evm.CheckpointRoute,_ types.Hash,_ uint64,tx types.Hash)error{
  return errors.New("independent source witness mismatch")
 }
 err:=w.scanSharedILNSource(source,[]observerRoute{{Route:route,Gateway:gateway,DestinationDomain:1643}})
 require.NoError(t,err)
 cursor,exists,e:=w.readILNCursor("_direct_a")
 require.NoError(t,e)
 require.True(t,exists,"a durable retry permits advancing the window")
 require.Equal(t,uint64(20),cursor)
 count,e:=w.observerRetryCount("base","a")
 require.NoError(t,e);require.Equal(t,1,count,"a poisoned message must remain queued and unsigned")
}

func TestSharedILNObserverLateRouteBackfillsWithoutRepeatingExistingVotes(t *testing.T){
 idOld:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 idNew:=types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
 tx:=types.StringToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
 msg:=types.StringToHash("0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
 gwOld:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 gwNew:=types.StringToAddress("0x2222222222222222222222222222222222222222")
 src:=&evm.Destination{Name:"base",ChainID:8453,Domain:8453,ILNRegistryActivationBlock:100}
 oldRoute:=&evm.CheckpointRoute{Name:"old",RouteID:idOld}
 newRoute:=&evm.CheckpointRoute{Name:"new",RouteID:idNew}
 w:=&Worker{dataDir:t.TempDir()}
 require.NoError(t,w.writeILNCursor("_direct_old",200))
 originalReader,originalSigner:=openObserverWindow,observerSignMessage
 defer func(){openObserverWindow=originalReader;observerSignMessage=originalSigner}()
 calls,signs:=0,0
 openObserverWindow=func(_ *evm.Destination)(observerWindowReader,func()){
  return func(_ []types.Address,cursor,window uint64)(uint64,uint64,uint64,[]evm.ILNObservedGatewayOperation,error){
  calls++
  require.Equal(t,uint64(99),cursor)
  require.Equal(t,uint64(256),window)
  oldOp:=evm.ILNOperation{RouteID:idOld,MessageID:msg,DestinationDomain:1643,
   TransactionHash:tx,BlockNumber:150,ValidatorFeeWei:big.NewInt(1),LogIndex:0}
  newOp:=oldOp;newOp.RouteID=idNew;newOp.LogIndex=1
  return 200,100,200,[]evm.ILNObservedGatewayOperation{
   {Gateway:gwOld,Operation:oldOp},{Gateway:gwNew,Operation:newOp},
  },nil
  },func(){}
 }
 observerSignMessage=func(_ *Worker,r *evm.CheckpointRoute,_ types.Hash,_ uint64,tx types.Hash)error{
  signs++;require.Equal(t,"new",r.Name);return nil
 }
 require.NoError(t,w.scanSharedILNSource(src,[]observerRoute{
  {Route:oldRoute,Gateway:gwOld,DestinationDomain:1643},
  {Route:newRoute,Gateway:gwNew,DestinationDomain:1643},
 }))
 require.Equal(t,1,calls)
 require.Equal(t,1,signs,"late route backfill may not re-sign already scanned events")
 for _,name:=range []string{"old","new"}{
  cursor,exists,err:=w.readILNCursor("_direct_"+name)
  require.NoError(t,err);require.True(t,exists);require.Equal(t,uint64(200),cursor)
 }
}

func TestObserverUncatalogedRouteDoesNotStartAnyGatewayScan(t *testing.T){
 src:=&evm.Destination{Name:"base",ChainID:8453,Domain:8453,ILNRegistryActivationBlock:100}
 dst:=&evm.Destination{Name:"xgr",ChainID:1643,Domain:1643}
 route:=&evm.CheckpointRoute{Name:"uncataloged",SourceNetwork:"base",Destination:"xgr",
  RouteID:types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}
 w:=&Worker{dataDir:t.TempDir(),destinations:map[string]*evm.Destination{"base":src,"xgr":dst}}
 _,err:=w.ensureObserverCatalogRoute(src,route,0)
 require.ErrorContains(t,err,"confirmed source head unavailable")
 _,exists,err:=w.readILNCursor("_direct_"+route.Name)
 require.NoError(t,err)
 require.False(t,exists)
}

func TestSharedObserverContinuesSigningHealthySiblingButNeverAdvancesFailedWindow(t *testing.T){
 idA:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 idB:=types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
 tx:=types.StringToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
 bad:=types.StringToHash("0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
 good:=types.StringToHash("0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
 addrA:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 addrB:=types.StringToAddress("0x2222222222222222222222222222222222222222")
 src:=&evm.Destination{Name:"base",ChainID:8453,Domain:8453,ILNRegistryActivationBlock:100}
 routeA:=&evm.CheckpointRoute{Name:"a",RouteID:idA}
 routeB:=&evm.CheckpointRoute{Name:"b",RouteID:idB}
 w:=&Worker{dataDir:t.TempDir()}
 originalReader,originalSigner:=openObserverWindow,observerSignMessage
 defer func(){openObserverWindow=originalReader;observerSignMessage=originalSigner}()
 openObserverWindow=func(_ *evm.Destination)(observerWindowReader,func()){
  return func(_ []types.Address,cursor,window uint64)(uint64,uint64,uint64,[]evm.ILNObservedGatewayOperation,error){
  require.Equal(t,uint64(99),cursor)
  a:=evm.ILNOperation{RouteID:idA,MessageID:bad,DestinationDomain:1643,
   BlockNumber:100,TransactionHash:tx,ValidatorFeeWei:big.NewInt(1),LogIndex:0}
  b:=evm.ILNOperation{RouteID:idB,MessageID:good,DestinationDomain:1643,
   BlockNumber:100,TransactionHash:tx,ValidatorFeeWei:big.NewInt(1),LogIndex:1}
  return 100,100,100,[]evm.ILNObservedGatewayOperation{
   {Gateway:addrA,Operation:a},{Gateway:addrB,Operation:b},
  },nil
  },func(){}
 }
 attempts:=map[string]int{}
 observerSignMessage=func(_ *Worker,r *evm.CheckpointRoute,_ types.Hash,_ uint64,tx types.Hash)error{
  attempts[r.Name]++
  if r.Name=="a"{return errors.New("independent source witness mismatch")}
  return nil
 }
 err:=w.scanSharedILNSource(src,[]observerRoute{
  {Route:routeA,Gateway:addrA,DestinationDomain:1643},
  {Route:routeB,Gateway:addrB,DestinationDomain:1643},
 })
 require.NoError(t,err)
 require.Equal(t,1,attempts["a"])
 require.Equal(t,1,attempts["b"],"healthy sibling must be independently signed")
 for _,name:=range []string{"a","b"}{
  cursor,exists,e:=w.readILNCursor("_direct_"+name)
  require.NoError(t,e);require.True(t,exists)
  require.Equal(t,uint64(100),cursor)
 }
 count,e:=w.observerRetryCount("base","a")
 require.NoError(t,e);require.Equal(t,1,count)
 count,e=w.observerRetryCount("base","b")
 require.NoError(t,e);require.Zero(t,count)
}

func TestSharedObserverCatchupThroughputAndPartialProgress(t *testing.T) {
	originalReader := openObserverWindow
	t.Cleanup(func() { openObserverWindow = originalReader })

	for _, test := range []struct {
		name         string
		chainID      uint64
		sourceName   string
		head         uint64
		failAt       int
		wantWindows  []uint64
		wantCursor   uint64
	}{
		{"caught-up source", 8453, "base", 100, 0, []uint64{256}, 100},
		{"Base last 3000 blocks fully caught up", 8453, "base", 3000, 0, []uint64{256, 1000, 1000, 744}, 3000},
		{"Base last 10000 blocks continues eight windows", 8453, "base", 10000, 0, []uint64{256, 1000, 1000, 1000, 1000, 1000, 1000, 1000}, 7356},
		{"Arbitrum catch-up uses eight windows", 42161, "arbitrum", 10000, 0, []uint64{256, 1000, 1000, 1000, 1000, 1000, 1000, 1000}, 7356},
		{"BSC catch-up uses eight windows", 56, "bsc", 10000, 0, []uint64{256, 1000, 1000, 1000, 1000, 1000, 1000, 1000}, 7356},
		{"Base partial progress on rate-limit", 8453, "base", 10000, 2, []uint64{256, 1000}, 356},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := &evm.Destination{Name: test.sourceName, ChainID: test.chainID, ILNRegistryActivationBlock: 101}
			route := &evm.CheckpointRoute{Name: "catchup", SourceNetwork: source.Name,
				RouteID: types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}
			gateway := types.StringToAddress("0x1111111111111111111111111111111111111111")
			worker := &Worker{dataDir: t.TempDir()}
			var windows []uint64
			openObserverWindow = func(*evm.Destination) (observerWindowReader, func()) {
				return func(_ []types.Address, cursor, window uint64) (uint64, uint64, uint64, []evm.ILNObservedGatewayOperation, error) {
					windows = append(windows, window)
					if len(windows) == test.failAt {
						return 0, 0, 0, nil, errors.New("rpc rate limit")
					}
					if cursor == test.head {
						return test.head, 0, 0, nil, nil
					}
					return test.head, cursor + 1, min(test.head, cursor + window), nil, nil
				}, func() {}
			}
			err := worker.scanSharedILNSource(source, []observerRoute{{Route: route, Gateway: gateway, DestinationDomain: 1643}})
			if test.failAt > 0 {
				require.ErrorContains(t, err, "rate limit")
				worker.recordILNDirectScanFailure(route, err)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, test.wantWindows, windows)
			health := worker.ilnRouteHealthByName[route.Name]
			require.Equal(t, test.wantCursor, health.ScanCursor)
			require.Equal(t, test.head-test.wantCursor, health.BlockLag)
			if test.wantCursor > 100 {
				cursor, exists, err := worker.readILNCursor("_direct_" + route.Name)
				require.NoError(t, err)
				require.True(t, exists)
				require.Equal(t, test.wantCursor, cursor)
			}
		})
	}
}
