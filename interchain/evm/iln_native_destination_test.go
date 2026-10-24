package evm

import (
 "errors"
 "math/big"
 "testing"

 "github.com/stretchr/testify/require"
 "github.com/umbracle/ethgo"
 "github.com/xgr-network/xgr-node/types"
)

type nativeDestinationFixture struct{
 reads int
 submissions int
 response []byte
 readErr error
}
func (f *nativeDestinationFixture) NativeXGRHead()(uint64,error){return 200,nil}
func (f *nativeDestinationFixture) NativeXGRBlock(uint64)(*NativeXGRBlock,error){return nil,errors.New("not provided")}
func (f *nativeDestinationFixture) NativeXGRRead(_ types.Address,_ []byte)([]byte,error){
 f.reads++
 return append([]byte(nil),f.response...),f.readErr
}
func (f *nativeDestinationFixture) NativeXGRBalance(types.Address)(*big.Int,error){return big.NewInt(100),nil}
func (f *nativeDestinationFixture) NativeXGREstimateCost(types.Address,types.Address,[]byte,*big.Int)(*big.Int,error){return big.NewInt(3),nil}
func (f *nativeDestinationFixture) NativeXGRSubmit(ethgo.Key,types.Address,[]byte,*big.Int)(*ethgo.Receipt,error){
 f.submissions++
 return nil,errors.New("intentional no submission")
}
func (f *nativeDestinationFixture) NativeXGRVerifyReceipt(*ethgo.Receipt)error{return errors.New("not committed")}

func TestNativeDestinationReadsWithoutRPC(t *testing.T){
 f:=&nativeDestinationFixture{response:[]byte{4,5,6}}
 dest:=&Destination{Name:"xgr",ChainID:1643,Domain:1643,NativeXGR:f}
 raw,err:=DestinationCall(dest,types.StringToAddress("0x1111111111111111111111111111111111111111"),[]byte{1,2,3,4})
 require.NoError(t,err)
 require.Equal(t,[]byte{4,5,6},raw)
 require.Equal(t,1,f.reads)
 require.Zero(t,f.submissions)
}
func TestNativeDestinationFailsClosedWithoutReader(t *testing.T){
 dest:=&Destination{Name:"xgr",ChainID:1643,Domain:1643}
 _,err:=DestinationCall(dest,types.StringToAddress("0x1111111111111111111111111111111111111111"),[]byte{1,2,3,4})
 require.ErrorContains(t,err,"backend unavailable")
}
