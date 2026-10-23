package interchain

import (
    "math/big"
    "encoding/hex"
    "testing"

    "github.com/stretchr/testify/require"
    "github.com/xgr-network/xgr-node/types"
)

func sampleXITADirectMessage() XITADirectMessage {
    return XITADirectMessage{
        SourceChainID:42161,SourceDomain:42161,DestinationDomain:1643,
        RouteID:types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
        SetID:9,SourceBlockNumber:517578687,
        SourceRegistry:types.StringToAddress("0x1111111111111111111111111111111111111111"),
        Gateway:types.StringToAddress("0x2222222222222222222222222222222222222222"),
        SourceRouter:types.StringToAddress("0x3333333333333333333333333333333333333333"),
        SourceMailbox:types.StringToAddress("0x4444444444444444444444444444444444444444"),
        DestinationRouter:types.StringToAddress("0x5555555555555555555555555555555555555555"),
        ValidatorFeeWei:big.NewInt(1000),
        MessageID:types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
        DestinationISM:types.StringToAddress("0x6666666666666666666666666666666666666666"),
        DestinationRegistry:types.StringToAddress("0x7777777777777777777777777777777777777777"),
    }
}

func TestXITADirectMessageWireAndSignatureDomain(t *testing.T) {
    original:=sampleXITADirectMessage()
    raw,err:=original.MarshalBinary()
    require.NoError(t,err)
    require.Equal(t, len(XITADirectMessageDomain)+8+4+4+32+8+8+5*20+32+32+20+20,len(raw))
    require.Equal(t, XITADirectMessageDomain,string(raw[:len(XITADirectMessageDomain)]))
    expected,err:=hex.DecodeString("584954415f4449524543545f4d4553534147455f5631000000000000a4b10000a4b10000066baaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0000000000000009000000001ed99fbf1111111111111111111111111111111111111111222222222222222222222222222222222222222233333333333333333333333333333333333333334444444444444444444444444444444444444444555555555555555555555555555555555555555500000000000000000000000000000000000000000000000000000000000003e8bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb66666666666666666666666666666666666666667777777777777777777777777777777777777777")
    require.NoError(t,err)
    require.Equal(t,expected,raw,"must match Solidity XITADirectProtocol.encode exactly")
    var decoded XITADirectMessage
    require.NoError(t,decoded.UnmarshalBinary(raw))
    again,err:=decoded.MarshalBinary()
    require.NoError(t,err)
    require.Equal(t,raw,again)
    require.Error(t,decoded.UnmarshalBinary(raw[:len(raw)-1]))
    require.Error(t,decoded.UnmarshalBinary(append(append([]byte(nil),raw...),0x00)))
    originalHash,err:=original.Hash()
    require.NoError(t,err)
    mutations:=[]func(*XITADirectMessage){
        func(x *XITADirectMessage){x.SourceChainID=8453},
        func(x *XITADirectMessage){x.SourceDomain=8453},
        func(x *XITADirectMessage){x.DestinationDomain=8453},
        func(x *XITADirectMessage){x.RouteID=types.StringToHash("0x01")},
        func(x *XITADirectMessage){x.SetID=10},
        func(x *XITADirectMessage){x.SourceBlockNumber=517578688},
        func(x *XITADirectMessage){x.SourceRegistry=types.StringToAddress("0x01")},
        func(x *XITADirectMessage){x.Gateway=types.StringToAddress("0x01")},
        func(x *XITADirectMessage){x.SourceRouter=types.StringToAddress("0x01")},
        func(x *XITADirectMessage){x.SourceMailbox=types.StringToAddress("0x01")},
        func(x *XITADirectMessage){x.DestinationRouter=types.StringToAddress("0x01")},
        func(x *XITADirectMessage){x.ValidatorFeeWei=big.NewInt(1001)},
        func(x *XITADirectMessage){x.MessageID=types.StringToHash("0x01")},
        func(x *XITADirectMessage){x.DestinationISM=types.StringToAddress("0x01")},
        func(x *XITADirectMessage){x.DestinationRegistry=types.StringToAddress("0x01")},
    }
    for _,change:=range mutations{
        altered:=original
        change(&altered)
        actual,err:=altered.Hash()
        require.NoError(t,err)
        require.NotEqual(t,originalHash,actual)
    }
}

func TestXITADirectMessageRejectsEmptyQuorumAndFee(t *testing.T) {
    a:=sampleXITADirectMessage()
    a.SetID=0
    _,err:=a.MarshalBinary()
    require.Error(t,err)
    a=sampleXITADirectMessage()
    a.ValidatorFeeWei=big.NewInt(0)
    _,err=a.MarshalBinary()
    require.Error(t,err)
    a=sampleXITADirectMessage()
    a.DestinationISM=types.ZeroAddress
    _,err=a.MarshalBinary()
    require.Error(t,err)
}
