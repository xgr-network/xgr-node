package evm

import (
 "errors"
 "testing"

 "github.com/stretchr/testify/require"
 "github.com/xgr-network/xgr-node/types"
)

type nativeXGRFixture struct{
 head uint64
 block *NativeXGRBlock
 err error
}
func (n nativeXGRFixture) NativeXGRHead()(uint64,error){if n.err!=nil{return 0,n.err};return n.head,nil}
func (n nativeXGRFixture) NativeXGRBlock(uint64)(*NativeXGRBlock,error){return n.block,n.err}

func TestNativeXGRFailsClosedWithoutLocalState(t *testing.T){
 source:=&Destination{Name:"xgr",ChainID:1643,Domain:1643,
  ILNRegistryAddress:"0xea887a7f2f519f291732aaadf578baf3ff374df6"}
 route:=&CheckpointRoute{Name:"xgr_to_base",SourceNetwork:"xgr",Destination:"base",
  RouteID:types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}
 message:=types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
 _,_,_,err:=VerifyNativeXGROperation(nil,source,route,8453,message,4)
 require.Error(t,err)
 _,_,_,err=VerifyNativeXGROperation(nativeXGRFixture{err:errors.New("not synchronized")},source,route,8453,message,4)
 require.ErrorContains(t,err,"not synchronized")
 _,_,_,err=VerifyNativeXGROperation(nativeXGRFixture{head:3},source,route,8453,message,4)
 require.ErrorContains(t,err,"not IBFT-committed")
 _,_,_,err=VerifyNativeXGROperation(nativeXGRFixture{head:5,block:&NativeXGRBlock{Number:4}},source,route,8453,message,4)
 require.ErrorContains(t,err,"unavailable or inconsistent")
 source.ChainID=8453
 _,_,_,err=VerifyNativeXGROperation(nativeXGRFixture{head:5},source,route,8453,message,4)
 require.ErrorContains(t,err,"invalid native XGR")
}
