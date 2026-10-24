package evm

import (
    "encoding/binary"
    "fmt"
    "math/big"
    "testing"

    "github.com/stretchr/testify/require"
    "github.com/umbracle/ethgo"

    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

// Positive fixture: one committed XGR receipt with a canonical Router ->
// Mailbox Dispatch and a matching fee-qualified Gateway operation. The
// contract reader stands in for ONE pinned local IBFT EVM state snapshot.
// No network/RPC stubs, external chain or archive state are involved.
func nativeXGRPositiveFixture(t *testing.T, allocated bool) (
    nativeXGRFixture, *Destination, *CheckpointRoute, types.Hash, []byte,
) {
    t.Helper()
    addr := func(s string) types.Address {return types.StringToAddress(s)}
    registry := addr("0x1111111111111111111111111111111111111111")
    gateway := addr("0x2222222222222222222222222222222222222222")
    mailbox := addr("0x3333333333333333333333333333333333333333")
    srcRouter := addr("0x4444444444444444444444444444444444444444")
    dstRouter := addr("0x5555555555555555555555555555555555555555")
    hook := addr("0x6666666666666666666666666666666666666666")
    vault := addr("0x7777777777777777777777777777777777777777")
    gov := addr("0x8888888888888888888888888888888888888888")
    factory := addr("0x9999999999999999999999999999999999999999")
    routeID := types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
    txHash := types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
    blockHash := types.StringToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
    fee := big.NewInt(1234)
    word := func(n int64) []byte {return new(big.Int).SetInt64(n).FillBytes(make([]byte,32))}
    addressWord := func(address types.Address) []byte {
        out:=make([]byte,32)
        copy(out[12:],address.Bytes())
        return out
    }
    selector := func(name string) string {
        digest:=crypto.Keccak256([]byte(name))
        return fmt.Sprintf("%x",digest[:4])
    }
    routeABI:=ilnRegistryABI.Methods["getRoute"]
    require.NotNil(t,routeABI)
    routeRecord,err:=routeABI.Outputs.Encode(map[string]interface{}{
        "sourceChainId":uint64(1643),
        "sourceDomain":uint32(1643),
        "gateway":ethgo.Address(gateway),
        "sourceRouter":ethgo.Address(srcRouter),
        "mailbox":ethgo.Address(mailbox),
        "dispatchHook":ethgo.Address(hook),
        "destinationRouter":ethgo.Address(dstRouter),
        "validatorFeeWei":fee,
        "enabled":true,
    })
    require.NoError(t,err)
    view:=func(to types.Address, data []byte)([]byte,error){
        if len(data)<4{return nil,fmt.Errorf("no contract selector")}
        method:=fmt.Sprintf("%x",data[:4])
        switch {
        case to==registry&&method==selector("factory()"):
            return addressWord(factory),nil
        case to==registry&&method==fmt.Sprintf("%x",routeABI.ID()):
            require.Len(t,data,4+64)
            require.Equal(t,word(8453),data[4:36])
            require.Equal(t,routeID.Bytes(),data[36:68])
            return routeRecord,nil
        case to==registry&&method==selector("governanceRegistry()"):
            return addressWord(gov),nil
        case to==gateway&&method==selector("ilnRegistry()"):
            return addressWord(registry),nil
        case to==gateway&&method==selector("warpRouter()"):
            return addressWord(srcRouter),nil
        case to==gateway&&method==selector("mailbox()"):
            return addressWord(mailbox),nil
        case to==gateway&&method==selector("dispatchHook()"):
            return addressWord(hook),nil
        case to==gateway&&method==selector("feeVault()"):
            return addressWord(vault),nil
        case to==vault&&method==selector("gateway()"):
            return addressWord(gateway),nil
        case to==vault&&method==selector("routeId()"):
            return routeID.Bytes(),nil
        case to==vault&&method==selector("destinationDomain()"):
            return word(8453),nil
        case to==vault&&method==selector("validatorRegistry()"):
            return addressWord(gov),nil
        case to==vault&&method==selector("allocatedOperation(bytes32)"):
            if len(data)!=36{return nil,fmt.Errorf("invalid allocation message id")}
            if !allocated{return word(0),nil}
            return word(1),nil
        case to==vault&&method==selector("allocatedSetId(bytes32)"):
            if len(data)!=36{return nil,fmt.Errorf("invalid epoch message id")}
            return word(3),nil
        default:
            return nil,fmt.Errorf("unrecognized native contract view to=%s selector=%s",to,method)
        }
    }

    message:=make([]byte,90)
    message[0]=3
    binary.BigEndian.PutUint32(message[1:5],2)
    binary.BigEndian.PutUint32(message[5:9],1643)
    copy(message[21:41],srcRouter.Bytes())
    binary.BigEndian.PutUint32(message[41:45],8453)
    copy(message[57:77],dstRouter.Bytes())
    copy(message[77:],[]byte("hello XITA"))
    messageID:=crypto.Keccak256Hash(message)
    topicsAddress:=func(a types.Address)types.Hash{return types.BytesToHash(addressWord(a))}
    logMailbox:=&types.Log{
        Address:mailbox,
        Topics:[]types.Hash{
            crypto.Keccak256Hash([]byte("Dispatch(address,uint32,bytes32,bytes)")),
            topicsAddress(srcRouter),
            types.BytesToHash(word(8453)),
            topicsAddress(dstRouter),
        },
        Data:append(append(append([]byte{},word(32)...),word(int64(len(message)))...),message...),
    }
    for len(logMailbox.Data)%32!=0 {logMailbox.Data=append(logMailbox.Data,0)}
    logGateway:=&types.Log{
        Address:gateway,
        Topics:[]types.Hash{
            crypto.Keccak256Hash([]byte(ilnOperationEventSignature)),
            routeID,messageID,types.BytesToHash(word(8453)),
        },
        Data:word(fee.Int64()),
    }
    success:=types.ReceiptSuccess
    block:=&NativeXGRBlock{
        Number:77,Hash:blockHash,
        Transactions:[]*types.Transaction{{Hash:txHash}},
        Receipts:[]*types.Receipt{{Status:&success,Logs:[]*types.Log{logMailbox,logGateway}}},
        View:view,
    }
    source:=&Destination{ChainID:1643,Domain:1643,ILNRegistryAddress:registry.String()}
    route:=&CheckpointRoute{
        Name:"iln_1643_8453_test",SourceNetwork:"xgr",
        Destination:"base",RouteID:routeID,
    }
    return nativeXGRFixture{head:80,block:block},source,route,messageID,message
}

func TestNativeXGRPositiveGatewayMailboxFeeVaultEvidence(t *testing.T){
    reader,source,route,messageID,originalMessage:=nativeXGRPositiveFixture(t,true)
    operation,message,snapshot,err:=VerifyNativeXGROperation(reader,source,route,8453,messageID,77)
    require.NoError(t,err)
    require.Equal(t,originalMessage,message)
    require.NotNil(t,operation)
    require.Equal(t,messageID,operation.MessageID)
    require.Equal(t,reader.block.Hash,operation.CanonicalBlockHash)
    require.Equal(t,uint64(77),operation.BlockNumber)
    require.Equal(t,uint64(1),operation.LogIndex)
    require.NotNil(t,snapshot)
    require.Equal(t,route.RouteID,snapshot.Route.Key.RouteID)
    require.Equal(t,uint64(1643),snapshot.Route.Key.SourceChainID)
    require.Equal(t,uint32(8453),snapshot.Route.Key.DestinationDomain)
}

func TestNativeXGRRejectsUnallocatedMessageDespiteValidReceipt(t *testing.T){
    reader,source,route,messageID,_:=nativeXGRPositiveFixture(t,false)
    _,_,_,err:=VerifyNativeXGROperation(reader,source,route,8453,messageID,77)
    require.ErrorIs(t,err,ErrILNFeeNotAllocated)
}

func TestNativeXGRRejectsMissingMailboxDispatch(t *testing.T){
    reader,source,route,messageID,_:=nativeXGRPositiveFixture(t,true)
    reader.block.Receipts[0].Logs=reader.block.Receipts[0].Logs[1:]
    _,_,_,err:=VerifyNativeXGROperation(reader,source,route,8453,messageID,77)
    require.ErrorContains(t,err,"one bound Mailbox Dispatch")
}
