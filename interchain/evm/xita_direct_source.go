package evm

import (
    "fmt"
    "math/big"

    "github.com/umbracle/ethgo"
    "github.com/umbracle/ethgo/jsonrpc"
    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

// XITADirectSourceEvidence proves the original fee-paid Mailbox.Dispatch
// from a confirmed, successful, uniquely identified Gateway transaction.
// No Hook insertion, Merkle root or historical storage call is involved.
type XITADirectSourceEvidence struct {
    Operation ILNOperation
    Message []byte
    Snapshot *ILNRouteSnapshot
}

// verifyXITADirectReceipt authenticates the exact Gateway operation and
// exactly one matching canonical Mailbox Dispatch in the SAME receipt.
// A direct WarpRouter call cannot spoof Gateway fees or unlock another asset.
func verifyXITADirectReceipt(
    client *jsonrpc.Client, op ILNOperation, gateway, mailbox,
    sourceRouter, destinationRouter types.Address, sourceDomain uint32,
) ([]byte,error) {
    if client==nil || op.TransactionHash==types.ZeroHash ||
        op.MessageID==types.ZeroHash || op.BlockNumber==0 ||
        op.DestinationDomain==0 || sourceDomain==0 ||
        gateway==types.ZeroAddress || mailbox==types.ZeroAddress ||
        sourceRouter==types.ZeroAddress || destinationRouter==types.ZeroAddress {
        return nil,fmt.Errorf("invalid direct source receipt identity")
    }
    receipt,err:=client.Eth().GetTransactionReceipt(ethgo.Hash(op.TransactionHash))
    if err!=nil || receipt==nil{return nil,fmt.Errorf("direct source receipt unavailable: %v",err)}
    if receipt.Status!=1 || receipt.BlockNumber!=op.BlockNumber ||
        types.Hash(receipt.TransactionHash)!=op.TransactionHash {
        return nil,fmt.Errorf("direct source receipt was not successfully confirmed")
    }

    gatewayTopic:=ethgo.Hash(crypto.Keccak256Hash([]byte(ilnOperationEventSignature)))
    dispatchTopic:=ethgo.Hash(crypto.Keccak256Hash([]byte("Dispatch(address,uint32,bytes32,bytes)")))
    gatewayCount,dispatchCount:=0,0
    var message []byte
    for _,log:=range receipt.Logs{
        if log==nil||log.Removed||types.Hash(log.TransactionHash)!=op.TransactionHash||
            log.BlockNumber!=op.BlockNumber{continue}
        if types.Address(log.Address)==gateway && len(log.Topics)==4 &&
            log.Topics[0]==gatewayTopic{
            parsed,parseErr:=parseILNOperationLog(log)
            if parseErr!=nil{return nil,fmt.Errorf("malformed direct Gateway event: %w",parseErr)}
            if parsed.RouteID==op.RouteID && parsed.MessageID==op.MessageID &&
                parsed.DestinationDomain==op.DestinationDomain &&
                parsed.LogIndex==op.LogIndex && parsed.ValidatorFeeWei!=nil &&
                op.ValidatorFeeWei!=nil &&
                parsed.ValidatorFeeWei.Cmp(op.ValidatorFeeWei)==0 {
                gatewayCount++
            }
        }
        if types.Address(log.Address)!=mailbox||len(log.Topics)!=4||
            log.Topics[0]!=dispatchTopic{continue}
        if types.BytesToAddress(log.Topics[1][12:])!=sourceRouter ||
            new(big.Int).SetBytes(log.Topics[2][:]).Cmp(
                new(big.Int).SetUint64(uint64(op.DestinationDomain)))!=0 ||
            types.BytesToAddress(log.Topics[3][12:])!=destinationRouter {
            continue
        }
        if len(log.Data)<64||new(big.Int).SetBytes(log.Data[:32]).Cmp(big.NewInt(32))!=0{
            return nil,fmt.Errorf("invalid direct Mailbox.Dispatch encoding")
        }
        size:=new(big.Int).SetBytes(log.Data[32:64])
        if !size.IsUint64()||size.Uint64()>uint64(len(log.Data)-64)||
            size.Uint64()>MaxILNSerializedMessageBytes {
            return nil,fmt.Errorf("invalid direct Mailbox message length")
        }
        actual:=log.Data[64:64+int(size.Uint64())]
        if crypto.Keccak256Hash(actual)!=op.MessageID{continue}
        if err:=VerifyILNMessageBytes(actual,op.MessageID,sourceDomain,
            sourceRouter,op.DestinationDomain,destinationRouter);err!=nil {
            return nil,fmt.Errorf("invalid canonical direct message: %w",err)
        }
        dispatchCount++
        message=append([]byte(nil),actual...)
    }
    if gatewayCount!=1||dispatchCount!=1 {
        return nil,fmt.Errorf("direct source receipt lacks unique fee-qualified Gateway and Mailbox events")
    }
    return message,nil
}

// VerifyXITADirectSourceMessage is the only source-authorization boundary
// used before a validator BLS vote. The block number is a candidate hint,
// never authority; the canonical transaction, route and FeeVault decide.
func VerifyXITADirectSourceMessage(
    source *Destination, destinationDomain uint32,
    routeID,messageID types.Hash,sourceBlock uint64,
) (*XITADirectSourceEvidence,error) {
    if source==nil||destinationDomain==0||routeID==types.ZeroHash||
        messageID==types.ZeroHash||sourceBlock==0 {
        return nil,fmt.Errorf("invalid direct source lookup")
    }
    confirmed,err:=GetConfirmedILNHead(source)
    if err!=nil{return nil,err}
    if sourceBlock>confirmed{return nil,fmt.Errorf("source transaction awaits finality")}
    snapshot,err:=GetILNRouteAtBlock(source,destinationDomain,routeID,sourceBlock)
    if err!=nil{return nil,err}
    operations,err:=GetConfirmedILNOperations(source,snapshot.Route.Gateway,
        routeID,destinationDomain,sourceBlock,sourceBlock)
    if err!=nil{return nil,err}
    var op *ILNOperation
    for i:=range operations{
        if operations[i].MessageID!=messageID{continue}
        if op!=nil{return nil,fmt.Errorf("duplicate direct source Gateway operation")}
        op=&operations[i]
    }
    if op==nil{return nil,fmt.Errorf("fee-qualified direct source message not found in confirmed block")}
    client,err:=ilnSourceClient(source)
    if err!=nil{return nil,err}
    message,err:=verifyXITADirectReceipt(client,*op,snapshot.Route.Gateway,
        snapshot.Route.Mailbox,snapshot.Route.SourceRouter,
        snapshot.Route.DestinationRouter,source.Domain)
    if err!=nil{return nil,err}
    if err:=VerifyConfirmedILNFeeAllocation(source,snapshot,messageID,sourceBlock);err!=nil {
        return nil,fmt.Errorf("direct source FeeVault allocation failed: %w",err)
    }
    return &XITADirectSourceEvidence{Operation:*op,Message:message,Snapshot:snapshot},nil
}
