package evm

import (
    "bytes"
    "encoding/binary"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"

    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

// The complete serialized Hyperlane message is required by Mailbox.process.
// A message hash + Merkle proof alone can never reconstruct the message body.
const MaxILNSerializedMessageBytes = 16 << 10

type ILNStoredMessage struct {
    ChainID uint64 `json:"chainId"`
    Hook types.Address `json:"hook"`
    MessageID types.Hash `json:"messageId"`
    Message string `json:"message"`
}

func VerifyILNMessageBytes(
    message []byte,
    expected types.Hash,
    sourceDomain uint32,
    sourceRouter types.Address,
    destinationDomain uint32,
    destinationRouter types.Address,
) error {
    if len(message)<77 || len(message)>MaxILNSerializedMessageBytes ||
        expected==types.ZeroHash || sourceDomain==0 || destinationDomain==0 ||
        sourceRouter==types.ZeroAddress || destinationRouter==types.ZeroAddress {
        return fmt.Errorf("invalid bounded ILN Hyperlane message")
    }
    if crypto.Keccak256Hash(message)!=expected {
        return fmt.Errorf("ILN message body does not match canonical Merkle leaf hash")
    }
    if binary.BigEndian.Uint32(message[5:9])!=sourceDomain ||
        binary.BigEndian.Uint32(message[41:45])!=destinationDomain ||
        !bytes.Equal(message[9:21],make([]byte,12)) ||
        !bytes.Equal(message[45:57],make([]byte,12)) ||
        !bytes.Equal(message[21:41],sourceRouter.Bytes()) ||
        !bytes.Equal(message[57:77],destinationRouter.Bytes()){
        return fmt.Errorf("ILN Hyperlane message origin/sender/destination/recipient mismatches route")
    }
    return nil
}

func VerifyILNMessageInReplica(
    replica ILNReplica,
    canonical ILNReplicaCheckpoint,
    message []byte,
    messageID types.Hash,
    sourceDomain uint32,
    sourceRouter types.Address,
    destinationDomain uint32,
    destinationRouter types.Address,
) error {
    if err:=VerifyILNMessageBytes(message,messageID,sourceDomain,sourceRouter,
        destinationDomain,destinationRouter);err!=nil{return err}
    if err:=VerifyILNReplica(replica,canonical);err!=nil{return err}
    found:=0
    for _,leaf:=range replica.Leaves{
        if leaf==messageID{found++}
    }
    if found!=1{return fmt.Errorf("serialized ILN message is not uniquely included in verified source root")}
    return nil
}

// This is part of the normal validator data dir and uses the same atomic
// rename/fsync policy as Merkle snapshots. It stores no secret keys.
func StoreVerifiedILNMessage(
    path string,replica ILNReplica,canonical ILNReplicaCheckpoint,
    message []byte,messageID types.Hash,
    sourceDomain uint32,sourceRouter types.Address,
    destinationDomain uint32,destinationRouter types.Address,
)error{
    if err:=VerifyILNMessageInReplica(replica,canonical,message,messageID,
        sourceDomain,sourceRouter,destinationDomain,destinationRouter);err!=nil{return err}
    if path==""{return fmt.Errorf("empty ILN message archive path")}
    record:=ILNStoredMessage{ChainID:canonical.ChainID,Hook:canonical.Hook,
        MessageID:messageID,Message:"0x"+hex.EncodeToString(message)}
    raw,err:=json.Marshal(record);if err!=nil{return err}
    if err:=os.MkdirAll(filepath.Dir(path),0o770);err!=nil{return err}
    f,err:=os.CreateTemp(filepath.Dir(path),".iln-message-*");if err!=nil{return err}
    defer os.Remove(f.Name())
    if err=f.Chmod(0o660);err!=nil{f.Close();return err}
    if _,err=f.Write(raw);err!=nil{f.Close();return err}
    if err=f.Sync();err!=nil{f.Close();return err}
    if err=f.Close();err!=nil{return err}
    return os.Rename(f.Name(),path)
}
func LoadVerifiedILNMessage(
    path string,replica ILNReplica,canonical ILNReplicaCheckpoint,
    messageID types.Hash,
    sourceDomain uint32,sourceRouter types.Address,
    destinationDomain uint32,destinationRouter types.Address,
)([]byte,error){
    raw,err:=os.ReadFile(path);if err!=nil{return nil,err}
    if len(raw)==0||len(raw)>2*MaxILNSerializedMessageBytes+1024{
        return nil,fmt.Errorf("ILN serialized message file exceeds size limit")
    }
    var record ILNStoredMessage
    if err:=json.Unmarshal(raw,&record);err!=nil{return nil,err}
    if record.ChainID!=canonical.ChainID||record.Hook!=canonical.Hook||
        record.MessageID!=messageID||len(record.Message)<2||
        record.Message[:2]!="0x" {
        return nil,fmt.Errorf("ILN message archive identity mismatch")
    }
    bytes,err:=hex.DecodeString(record.Message[2:]);if err!=nil{return nil,err}
    if err:=VerifyILNMessageInReplica(replica,canonical,bytes,messageID,
        sourceDomain,sourceRouter,destinationDomain,destinationRouter);err!=nil{
        return nil,fmt.Errorf("ILN serialized message was not independently verified: %w",err)
    }
    return bytes,nil
}
