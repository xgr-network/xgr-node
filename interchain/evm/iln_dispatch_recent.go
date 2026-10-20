package evm

import (
    "fmt"
    "math/big"
    "sort"

    "github.com/umbracle/ethgo"
    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

type ILNConfirmedDispatch struct {
    Block uint64
    TransactionHash types.Hash
    MessageID types.Hash
    SourceRouter types.Address
    DestinationDomain uint32
    DestinationRouter types.Address
    Message []byte
}

// GetRecentConfirmedILNDispatches reads ONLY a bounded recent block window,
// never requiring archival receipt replay. Each Dispatch event is from the
// confirmed canonical Mailbox address, but final eligibility additionally
// requires verified Hook inclusion + immutable source FeeVault allocation.
func GetRecentConfirmedILNDispatches(
    source *Destination,mailbox types.Address,from,to uint64,
)([]ILNConfirmedDispatch,error){
    if source==nil||mailbox==types.ZeroAddress||from==0||from>to||
        to-from>=maxILNReplicaRecentBlocks{
        return nil,fmt.Errorf("invalid bounded ILN recent dispatch range")
    }
    client,err:=ilnSourceClient(source)
    if err!=nil{return nil,err}
    confirmed,err:=confirmedILNHead(client,source)
    if err!=nil{return nil,err}
    if to>confirmed||to>uint64(1<<63-1){
        return nil,fmt.Errorf("unconfirmed ILN Dispatch logs")
    }
    topic:=ethgo.Hash(crypto.Keccak256Hash([]byte("Dispatch(address,uint32,bytes32,bytes)")))
    filter:=&ethgo.LogFilter{
        Address:[]ethgo.Address{ethgo.Address(mailbox)},
        Topics:[][]*ethgo.Hash{{&topic}},
    }
    filter.SetFromUint64(from)
    filter.SetToUint64(to)
    logs,err:=client.Eth().GetLogs(filter)
    if err!=nil{return nil,fmt.Errorf("recent canonical Mailbox Dispatch RPC unavailable: %w",err)}
    sort.SliceStable(logs,func(i,j int)bool{
        if logs[i]==nil{return false}
        if logs[j]==nil{return true}
        if logs[i].BlockNumber!=logs[j].BlockNumber {
            return logs[i].BlockNumber<logs[j].BlockNumber
        }
        return logs[i].LogIndex<logs[j].LogIndex
    })
    out:=make([]ILNConfirmedDispatch,0,len(logs))
    for _,log:=range logs {
        if log==nil||log.Removed||log.BlockNumber<from||log.BlockNumber>to||
            types.Address(log.Address)!=mailbox||len(log.Topics)!=4||
            log.Topics[0]!=topic||
            new(big.Int).SetBytes(log.Topics[2][:]).BitLen()>32 {
            return nil,fmt.Errorf("malformed or noncanonical confirmed Mailbox Dispatch log")
        }
        domain:=new(big.Int).SetBytes(log.Topics[2][:]).Uint64()
        if domain==0{return nil,fmt.Errorf("zero destination in Mailbox dispatch")}
        sender:=types.BytesToAddress(log.Topics[1][12:])
        recipient:=types.BytesToAddress(log.Topics[3][12:])
        if len(log.Data)<64{
            return nil,fmt.Errorf("truncated Mailbox Dispatch data")
        }
        if new(big.Int).SetBytes(log.Data[:32]).Cmp(big.NewInt(32))!=0 {
            return nil,fmt.Errorf("invalid ABI dynamic offset for Mailbox Dispatch")
        }
        n:=new(big.Int).SetBytes(log.Data[32:64])
        if !n.IsUint64() || n.Uint64()>MaxILNSerializedMessageBytes ||
            n.Uint64()>uint64(len(log.Data)-64) {
            return nil,fmt.Errorf("oversized or truncated Mailbox Dispatch payload")
        }
        message:=append([]byte(nil),log.Data[64:64+int(n.Uint64())]...)
        id:=crypto.Keccak256Hash(message)
        if err:=VerifyILNMessageBytes(message,id,source.Domain,sender,uint32(domain),recipient);err!=nil {
            return nil,fmt.Errorf("Mailbox.Dispatch header malformed: %w",err)
        }
        out=append(out,ILNConfirmedDispatch{
            Block:log.BlockNumber,TransactionHash:types.Hash(log.TransactionHash),
            MessageID:id,SourceRouter:sender,
            DestinationDomain:uint32(domain),
            DestinationRouter:recipient,Message:message,
        })
    }
    return out,nil
}
