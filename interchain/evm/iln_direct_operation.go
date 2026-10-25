package evm

import (
    "fmt"
    "math/big"

    "github.com/umbracle/ethgo"
    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

// VerifyDirectILNReceipt authenticates ONE actual successful Gateway lock/burn
// and exactly ONE matching canonical Mailbox Dispatch in the SAME source receipt.
// No Merkle tree, root, leaf, insertion index, or historical contract state.
func VerifyDirectILNReceipt(
    operation ILNOperation, receipt *ethgo.Receipt,
    sourceDomain uint32, gateway, mailbox, sourceRouter, destinationRouter types.Address,
)([]byte,error){
    if receipt==nil||operation.TransactionHash==types.ZeroHash||
       operation.MessageID==types.ZeroHash||operation.BlockNumber==0||
       sourceDomain==0||gateway==types.ZeroAddress||mailbox==types.ZeroAddress||
       sourceRouter==types.ZeroAddress||destinationRouter==types.ZeroAddress||
       operation.DestinationDomain==0||operation.RouteID==types.ZeroHash||
       operation.ValidatorFeeWei==nil||operation.ValidatorFeeWei.Sign()<=0 {
        return nil,fmt.Errorf("invalid direct ILN source operation")
    }
    if receipt.Status!=uint64(types.ReceiptSuccess)||
       receipt.BlockNumber!=operation.BlockNumber||
       types.Hash(receipt.TransactionHash)!=operation.TransactionHash {
        return nil,fmt.Errorf("direct source transaction not confirmed or wrong receipt")
    }
    gatewayTopic:=ethgo.Hash(crypto.Keccak256Hash([]byte(ilnOperationEventSignature)))
    dispatchTopic:=ethgo.Hash(crypto.Keccak256Hash([]byte("Dispatch(address,uint32,bytes32,bytes)")))
    events,dispatches:=0,0
    var message []byte
    for _,log:=range receipt.Logs {
        if log==nil||log.Removed {return nil,fmt.Errorf("removed source receipt log")}
        if types.Hash(log.TransactionHash)!=operation.TransactionHash ||
           log.BlockNumber!=operation.BlockNumber {
            return nil,fmt.Errorf("source receipt log does not belong to operation")
        }
        if types.Address(log.Address)==gateway&&len(log.Topics)>0&&log.Topics[0]==gatewayTopic {
            record,err:=parseILNOperationLog(log)
            if err!=nil{return nil,err}
            if record.RouteID==operation.RouteID&&record.MessageID==operation.MessageID&&
               record.DestinationDomain==operation.DestinationDomain&&
               record.ValidatorFeeWei.Cmp(operation.ValidatorFeeWei)==0&&
               record.LogIndex==operation.LogIndex {
                events++
            }
        }
        if types.Address(log.Address)!=mailbox||len(log.Topics)==0||log.Topics[0]!=dispatchTopic {continue}
        if len(log.Topics)!=4 {return nil,fmt.Errorf("malformed canonical Mailbox Dispatch topics")}
        if len(log.Data)<64||new(big.Int).SetBytes(log.Data[:32]).Cmp(big.NewInt(32))!=0 {
            return nil,fmt.Errorf("invalid canonical Mailbox Dispatch ABI")
        }
        n:=new(big.Int).SetBytes(log.Data[32:64])
        if !n.IsUint64()||n.Uint64()>MaxILNSerializedMessageBytes||
           n.Uint64()>uint64(len(log.Data)-64) {
            return nil,fmt.Errorf("invalid bounded Hyperlane message length")
        }
        candidate:=append([]byte(nil),log.Data[64:64+int(n.Uint64())]...)
        // One successful transaction can legitimately dispatch other messages.
        // Ignore unrelated canonical dispatches, but require exactly ONE
        // independently authenticated dispatch for this fee-qualified message.
        if crypto.Keccak256Hash(candidate)!=operation.MessageID {continue}
        if new(big.Int).SetBytes(log.Topics[1][:12]).Sign()!=0||
           new(big.Int).SetBytes(log.Topics[3][:12]).Sign()!=0||
           types.BytesToAddress(log.Topics[1][12:])!=sourceRouter||
           types.BytesToAddress(log.Topics[3][12:])!=destinationRouter||
           new(big.Int).SetBytes(log.Topics[2][:]).Cmp(
             new(big.Int).SetUint64(uint64(operation.DestinationDomain)))!=0 {
            return nil,fmt.Errorf("wrong source router or destination in bound Mailbox Dispatch")
        }
        if err:=VerifyILNMessageBytes(candidate,operation.MessageID,sourceDomain,
            sourceRouter,operation.DestinationDomain,destinationRouter);err!=nil {
            return nil,fmt.Errorf("canonical Mailbox Dispatch message mismatch: %w",err)
        }
        message=candidate
        dispatches++
    }
    if events!=1||dispatches!=1 {
        return nil,fmt.Errorf("source receipt must contain exactly one matching Gateway operation and one message-bound Mailbox Dispatch")
    }
    return message,nil
}

// GetDirectILNOperation resolves an operation at its actual, source-RPC-native
// block, and requires the original Gateway and Mailbox events plus FeeVault
// allocation. All RPC queries are to current state or the single source
// receipt/block; no historical eth_call and no Merkle history.
func GetDirectILNOperation(
    source *Destination, route *CheckpointRoute, destinationDomain uint32,
    messageID types.Hash, sourceBlock uint64,
)(*ILNOperation,[]byte,*ILNRouteSnapshot,error){
    if source==nil||route==nil||messageID==types.ZeroHash||sourceBlock==0 {
        return nil,nil,nil,fmt.Errorf("invalid direct source message identity")
    }
    client,head,err:=newVerifiedRPCClientWithHead(source)
    if err!=nil{return nil,nil,nil,err}
    defer client.Close()
    if sourceBlock>head{return nil,nil,nil,fmt.Errorf("source block beyond safe head")}
    snapshot,err:=getILNRouteAtBlock(client,source,destinationDomain,route.RouteID,sourceBlock,true)
    if err!=nil{recordInterchainRPCReadError(source,source.RPCURL,err);return nil,nil,nil,err}
    ops,err:=confirmedILNOperationsOnClient(client,source,snapshot.Route.Gateway,
        route.RouteID,destinationDomain,sourceBlock,sourceBlock,head)
    if err!=nil{recordInterchainRPCReadError(source,source.RPCURL,err);return nil,nil,nil,err}
    var operation *ILNOperation
    for i:=range ops {
        if ops[i].MessageID!=messageID{continue}
        if operation!=nil{return nil,nil,nil,fmt.Errorf("duplicate Gateway operation for direct message")}
        operation=&ops[i]
    }
    if operation==nil{return nil,nil,nil,fmt.Errorf("direct ILN message not found at source block")}
    receipt,err:=ilnRPCReceipt(client,ethgo.Hash(operation.TransactionHash))
    if err!=nil{
        recordInterchainRPCReadError(source,source.RPCURL,err)
        return nil,nil,nil,fmt.Errorf("canonical source transaction receipt unavailable: %w",err)
    }
    if receipt==nil||receipt.TransactionHash!=ethgo.Hash(operation.TransactionHash){
        return nil,nil,nil,fmt.Errorf("canonical source receipt transaction hash mismatch")
    }
    if err:=verifyFinalizedReceiptWithHead(client,receipt,head);err!=nil{
        return nil,nil,nil,fmt.Errorf("source receipt is not canonical and final: %w",err)
    }
    operation.CanonicalBlockHash=types.Hash(receipt.BlockHash)
    raw,err:=VerifyDirectILNReceipt(*operation,receipt,source.Domain,
        snapshot.Route.Gateway,snapshot.Route.Mailbox,snapshot.Route.SourceRouter,
        snapshot.Route.DestinationRouter)
    if err!=nil{return nil,nil,nil,err}
    if err:=verifyConfirmedILNFeeAllocationOnClient(client,source,snapshot,messageID,sourceBlock,head);err!=nil{
        return nil,nil,nil,fmt.Errorf("immutable Gateway FeeVault allocation not proven: %w",err)
    }
    return operation,raw,snapshot,nil
}

// GetDirectILNDestinationISM resolves the exact currently effective recipient
// ISM and checks its immutable validator registry and domain. Signature
// domain separation uses this address, never a user-provided contract.
func GetDirectILNDestinationISM(destination *Destination, recipient types.Address) (types.Address,error){
    if destination==nil||recipient==types.ZeroAddress {
        return types.ZeroAddress,fmt.Errorf("invalid direct destination identity")
    }
    if err:=destination.ValidateMembershipRead();err!=nil{return types.ZeroAddress,err}
    view:=func(to types.Address,call []byte)([]byte,error){
        return DestinationCall(destination,to,call)
    }
    mailbox,err:=viewILNAddress(view,recipient,"mailbox()")
    if err!=nil{return types.ZeroAddress,err}
    ism,err:=viewILNAddress(view,mailbox,"defaultIsm()")
    if err!=nil{return types.ZeroAddress,err}
    // Reject recipients that deliberately install a different effective ISM:
    // Mailbox.process() must exercise the very same module we sign for.
    if raw,viewErr:=viewILNSignature(view,recipient,"interchainSecurityModule()");viewErr==nil {
        if len(raw)!=32{return types.ZeroAddress,fmt.Errorf("malformed recipient ISM")}
        configured:=types.BytesToAddress(raw[12:])
        if configured!=types.ZeroAddress&&configured!=ism {
            return types.ZeroAddress,fmt.Errorf("recipient overrides canonical Direct-BLS ISM")
        }
    }
    registry,err:=viewILNAddress(view,ism,"registry()")
    if err!=nil{return types.ZeroAddress,err}
    if registry!=types.StringToAddress(destination.RegistryAddress) {
        return types.ZeroAddress,fmt.Errorf("destination ISM registry mismatch")
    }
    domain,err:=viewILNSignature(view,ism,"destinationDomain()")
    if err!=nil||len(domain)!=32||
       new(big.Int).SetBytes(domain).Cmp(new(big.Int).SetUint64(uint64(destination.Domain)))!=0 {
        return types.ZeroAddress,fmt.Errorf("destination ISM domain mismatch")
    }
    return ism,nil
}
