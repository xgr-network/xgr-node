package evm

import (
 "fmt"
 "math/big"
 "testing"

 "github.com/stretchr/testify/require"
 "github.com/xgr-network/xgr-node/types"
)
func TestTwoIndependentOperatorsWithFailover(t *testing.T){
 h:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 d:=&Destination{Name:"base",ChainID:8453,Domain:8453, RPCURLs:[]string{"a1","a2","b1","c1"},IndependentOperatorIDs:[]string{"a","a","b","c"}}
 calls:=[]string{}
 reader:=func(src *Destination,_ *CheckpointRoute,_ uint32,_ types.Hash,_ uint64)(*ILNOperation,[]byte,*ILNRouteSnapshot,error){
  calls=append(calls,src.RPCURL)
  if src.RPCURL=="a1" {return nil,nil,nil,fmt.Errorf("timeout")}
  op:=&ILNOperation{CanonicalBlockHash:h,TransactionHash:h,MessageID:h,RouteID:h,BlockNumber:9,DestinationDomain:1643,ValidatorFeeWei:big.NewInt(1)}
  return op,[]byte("message"),&ILNRouteSnapshot{Registry:types.StringToAddress("0x1111111111111111111111111111111111111111"),BlockNumber:9},nil
 }
 op,_,_,err:=quorumVerifiedDirectILNOperation(d,nil,1643,h,9,reader)
 require.NoError(t,err)
 require.NotNil(t,op)
 require.Equal(t,[]string{"a1","a2","b1"},calls)
}
func TestOneOperatorAndConflictingProvidersFailClosed(t *testing.T){
 h:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 d:=&Destination{Name:"base",RPCURLs:[]string{"a1","a2","b1"},IndependentOperatorIDs:[]string{"a","a","b"}}
 reader:=func(src *Destination,_ *CheckpointRoute,_ uint32,_ types.Hash,_ uint64)(*ILNOperation,[]byte,*ILNRouteSnapshot,error){
  op:=&ILNOperation{CanonicalBlockHash:h,TransactionHash:h,MessageID:h,RouteID:h,BlockNumber:9,DestinationDomain:1643,ValidatorFeeWei:big.NewInt(1)}
  if src.RPCURL=="b1"{op.BlockNumber=10}
  return op,[]byte("message"),&ILNRouteSnapshot{BlockNumber:9},nil
 }
 _,_,_,err:=quorumVerifiedDirectILNOperation(d,nil,1643,h,9,reader)
 require.ErrorContains(t,err,"quorum not reached")
 d.RPCURLs=[]string{"a1","a2"}
 d.IndependentOperatorIDs=[]string{"a","a"}
 _,_,_,err=quorumVerifiedDirectILNOperation(d,nil,1643,h,9,reader)
 require.ErrorContains(t,err,"quorum not reached")
}
