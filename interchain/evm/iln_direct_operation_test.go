package evm

import (
    "encoding/binary"
    "math/big"
    "testing"

    "github.com/stretchr/testify/require"
    "github.com/umbracle/ethgo"
    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

func directSourceReceiptFixture() (ILNOperation,*ethgo.Receipt,types.Address,types.Address,types.Address,types.Address){
    gateway:=types.StringToAddress("0x1111111111111111111111111111111111111111")
    mailbox:=types.StringToAddress("0x2222222222222222222222222222222222222222")
    router:=types.StringToAddress("0x3333333333333333333333333333333333333333")
    destination:=types.StringToAddress("0x4444444444444444444444444444444444444444")
    var message [90]byte
    message[0]=3
    binary.BigEndian.PutUint32(message[1:5],2)
    binary.BigEndian.PutUint32(message[5:9],8453)
    copy(message[21:41],router.Bytes())
    binary.BigEndian.PutUint32(message[41:45],1643)
    copy(message[57:77],destination.Bytes())
    copy(message[77:],[]byte("hello XITA"))
    id:=crypto.Keccak256Hash(message[:])
    routeID:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
    tx:=types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
    fee:=big.NewInt(1234)
    op:=ILNOperation{RouteID:routeID,MessageID:id,DestinationDomain:1643,
       ValidatorFeeWei:fee,TransactionHash:tx,BlockNumber:77,LogIndex:1}
    word:=func(n *big.Int)[]byte{return n.FillBytes(make([]byte,32))}
    addressTopic:=func(x types.Address)ethgo.Hash{
       var b [32]byte;copy(b[12:],x.Bytes());return ethgo.Hash(b)
    }
    feeEvent:=&ethgo.Log{
      Address:ethgo.Address(gateway),
      Topics:[]ethgo.Hash{
        ethgo.Hash(crypto.Keccak256Hash([]byte(ilnOperationEventSignature))),
        ethgo.Hash(routeID),ethgo.Hash(id),ethgo.Hash(types.BytesToHash(word(big.NewInt(1643)))),
      },
      Data:word(fee),TransactionHash:ethgo.Hash(tx),BlockNumber:77,LogIndex:1,
    }
    abiMessage:=append(word(big.NewInt(32)),word(big.NewInt(int64(len(message))))...)
    abiMessage=append(abiMessage,message[:]...)
    for len(abiMessage)%32!=0 {abiMessage=append(abiMessage,0)}
    msgEvent:=&ethgo.Log{
      Address:ethgo.Address(mailbox),
      Topics:[]ethgo.Hash{
        ethgo.Hash(crypto.Keccak256Hash([]byte("Dispatch(address,uint32,bytes32,bytes)"))),
        addressTopic(router),ethgo.Hash(types.BytesToHash(word(big.NewInt(1643)))),
        addressTopic(destination),
      },
      Data:abiMessage,TransactionHash:ethgo.Hash(tx),BlockNumber:77,LogIndex:0,
    }
    receipt:=&ethgo.Receipt{Status:uint64(types.ReceiptSuccess),BlockNumber:77,
       TransactionHash:ethgo.Hash(tx),Logs:[]*ethgo.Log{msgEvent,feeEvent}}
    return op,receipt,gateway,mailbox,router,destination
}

func TestDirectGatewayReceiptBindsOneActualSourceTransaction(t *testing.T){
    op,receipt,gateway,mailbox,router,destination:=directSourceReceiptFixture()
    raw,err:=VerifyDirectILNReceipt(op,receipt,8453,gateway,mailbox,router,destination)
    require.NoError(t,err)
    require.Equal(t,op.MessageID,crypto.Keccak256Hash(raw))
}
func TestDirectReceiptFailsOnMissingOrDuplicateGatewayOperation(t *testing.T){
    op,receipt,gateway,mailbox,router,destination:=directSourceReceiptFixture()
    receipt.Logs=receipt.Logs[:1]
    _,err:=VerifyDirectILNReceipt(op,receipt,8453,gateway,mailbox,router,destination)
    require.Error(t,err)
    _,good,gateway,mailbox,router,destination:=directSourceReceiptFixture()
    good.Logs=append(good.Logs,good.Logs[1])
    _,err=VerifyDirectILNReceipt(op,good,8453,gateway,mailbox,router,destination)
    require.Error(t,err)
}
func TestDirectReceiptRejectsWrongAssetOrDestinationAndSourceForgery(t *testing.T){
    op,receipt,gateway,mailbox,router,destination:=directSourceReceiptFixture()
    _,err:=VerifyDirectILNReceipt(op,receipt,8453,gateway,mailbox,router,
        types.StringToAddress("0x9999999999999999999999999999999999999999"))
    require.Error(t,err)
    _,err=VerifyDirectILNReceipt(op,receipt,42161,gateway,mailbox,router,destination)
    require.Error(t,err)
    op.ValidatorFeeWei=big.NewInt(9999)
    _,err=VerifyDirectILNReceipt(op,receipt,8453,gateway,mailbox,router,destination)
    require.Error(t,err)
}
func TestDirectReceiptRejectsRevertOrWrongTransaction(t *testing.T){
    op,receipt,gateway,mailbox,router,destination:=directSourceReceiptFixture()
    receipt.Status=0
    _,err:=VerifyDirectILNReceipt(op,receipt,8453,gateway,mailbox,router,destination)
    require.Error(t,err)
    receipt.Status=uint64(types.ReceiptSuccess)
    receipt.Logs[0].TransactionHash=ethgo.Hash{}
    _,err=VerifyDirectILNReceipt(op,receipt,8453,gateway,mailbox,router,destination)
    require.Error(t,err)
}

func TestDirectReceiptRejectsDuplicatedGatewayOrMailboxEvents(t *testing.T){
    for _,duplicateGateway:=range []bool{true,false}{
        op,receipt,gateway,mailbox,router,destination:=directSourceReceiptFixture()
        i:=0
        if duplicateGateway{i=1}
        receipt.Logs=append(receipt.Logs,receipt.Logs[i])
        _,err:=VerifyDirectILNReceipt(op,receipt,8453,gateway,mailbox,router,destination)
        require.Error(t,err,"a duplicate canonical event must not authorize a second BLS vote")
    }
}
func TestDirectReceiptRejectsUnrelatedOrMalformedGatewayAndMailbox(t *testing.T){
    for _,invalid:=range []string{"gateway-address","mailbox-address","wrong-log-tx","wrong-domain","missing-fee","wrong-operation-index"}{
        op,receipt,gateway,mailbox,router,destination:=directSourceReceiptFixture()
        switch invalid{
        case "gateway-address":
            receipt.Logs[1].Address=ethgo.Address(types.StringToAddress("0x9999999999999999999999999999999999999999"))
        case "mailbox-address":
            receipt.Logs[0].Address=ethgo.Address(types.StringToAddress("0x9999999999999999999999999999999999999999"))
        case "wrong-log-tx":
            receipt.Logs[0].TransactionHash=ethgo.Hash{}
        case "wrong-domain":
            op.DestinationDomain=42161
        case "missing-fee":
            op.ValidatorFeeWei=big.NewInt(0)
        case "wrong-operation-index":
            op.LogIndex=42
        }
        _,err:=VerifyDirectILNReceipt(op,receipt,8453,gateway,mailbox,router,destination)
        require.Error(t,err,invalid)
    }
}
