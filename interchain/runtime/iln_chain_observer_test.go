package runtime

import (
 "math/big"
 "testing"
 "github.com/stretchr/testify/require"
 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)
func TestObserverDispatchRoutesByGatewayDomainAndID(t *testing.T){
 addr:=func(c string)types.Address{return types.StringToAddress("0x"+c)}
 a,b:=addr("1111111111111111111111111111111111111111"),addr("2222222222222222222222222222222222222222")
 id:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 tx:=types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
 msg:=types.StringToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
 routes:=[]observerRoute{
  {Route:&evm.CheckpointRoute{Name:"base-one",RouteID:id},Gateway:a,DestinationDomain:1643},
  {Route:&evm.CheckpointRoute{Name:"base-two",RouteID:id},Gateway:b,DestinationDomain:1643},
  {Route:&evm.CheckpointRoute{Name:"base-three",RouteID:id},Gateway:a,DestinationDomain:42161},
 }
 op:=evm.ILNOperation{RouteID:id,MessageID:msg,DestinationDomain:1643,
  ValidatorFeeWei:big.NewInt(1),BlockNumber:77,TransactionHash:tx,LogIndex:2}
 opOther:=op;opOther.LogIndex=1
 out,err:=dispatchObservedILNOperations(routes,
  []evm.ILNOperation{op,op,opOther,op},
  []types.Address{a,a,b,b})
 require.NoError(t,err)
 require.Len(t,out,3,"same event is idempotent; different Gateway remains distinct")
 require.Equal(t,"base-two",out[0].Route.Name)
 require.Equal(t,"base-one",out[1].Route.Name)
 require.Equal(t,"base-two",out[2].Route.Name)
}
func TestObserverDispatchFailsClosedOnInvalidEvidenceHint(t *testing.T){
 route:=&evm.CheckpointRoute{Name:"route",RouteID:types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}
 gateway:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 _,err:=dispatchObservedILNOperations([]observerRoute{{Route:route,Gateway:gateway,DestinationDomain:8453}},
  []evm.ILNOperation{{RouteID:route.RouteID,DestinationDomain:8453,ValidatorFeeWei:big.NewInt(1)}},
  []types.Address{gateway})
 require.ErrorContains(t,err,"invalid observed")
}
func TestObserverRejectsAmbiguousSameGatewayRoute(t *testing.T){
 id:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 gateway:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 _,err:=dispatchObservedILNOperations([]observerRoute{
  {Route:&evm.CheckpointRoute{Name:"first",RouteID:id},Gateway:gateway,DestinationDomain:8453},
  {Route:&evm.CheckpointRoute{Name:"second",RouteID:id},Gateway:gateway,DestinationDomain:8453},
 },nil,nil)
 require.ErrorContains(t,err,"ambiguous")
}
