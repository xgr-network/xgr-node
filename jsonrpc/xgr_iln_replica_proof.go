package jsonrpc

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "strconv"
    "strings"

    evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
    "github.com/xgr-network/xgr-node/types"
)

// ilnReplicaProofRPC is a read-only, relayer-independent Hyperlane Merkle
// inclusion proof. A client still supplies original message bytes to the
// destination Mailbox.process() and must verify the included BLS attestation
// and current destination registry set before submitting any transaction.
type ilnReplicaProofRPC struct {
    Route string `json:"route"`
    MessageID string `json:"messageId"`
    OriginChainID uint64 `json:"originChainId"`
    Hook string `json:"merkleTreeHook"`
    Root string `json:"root"`
    CheckpointBlock uint64 `json:"checkpointBlock"`
    CheckpointIndex uint32 `json:"checkpointIndex"`
    MessageIndex uint32 `json:"messageIndex"`
    MerkleProof [32]string `json:"merkleProof"`
    Quorum *interchainAttestationRPC `json:"quorum"`
}

// GetILNReplicaProof serves standard Merkle siblings directly from validator
// data/interchain/ after recomputing the COMPLETE root. No external service,
// private key, historic eth_getLogs or custom transaction execution is used.
// The current destination ISM remains the final proof verifier.
func (x *xgrEndpoint) GetILNReplicaProof(route,messageID string)(*ilnReplicaProofRPC,error) {
    route,err:=normalizeInterchainRPCChain(route);if err!=nil{return nil,err}
    messageID,err=normalizedILNMessageID(messageID);if err!=nil{return nil,err}
    if x==nil||x.dataDir==""{return nil,fmt.Errorf("ILN proof storage unavailable")}
    attestation,err:=x.GetILNInterchainAttestation(route,messageID)
    if err!=nil{return nil,err}
    if attestation.AuthorizedMessageID!=messageID ||
        attestation.Index==^uint32(0) || attestation.OriginChainID==0 ||
        !strings.EqualFold(attestation.RouteID,strings.TrimSpace(attestation.RouteID)) ||
        !isILNProofHash(attestation.Root) ||
        !isILNProofAddress(attestation.MerkleTreeHook) {
        return nil,fmt.Errorf("invalid ILN quorum/replica identity")
    }
    hook:=types.StringToAddress(attestation.MerkleTreeHook)
    root:=types.StringToHash(attestation.Root)
    c:=evmInterchain.ILNReplicaCheckpoint{
        ChainID:attestation.OriginChainID,Hook:hook,Root:root,
        Count:uint64(attestation.Index)+1,
    }
    path:=filepath.Join(x.dataDir,"interchain","iln","replicas",
        strconv.FormatUint(c.ChainID,10),strings.ToLower(hook.String())+".json")
    info,err:=os.Stat(path)
    if err!=nil{return nil,fmt.Errorf("ILN verified replica not yet available: %w",err)}
    if !info.Mode().IsRegular() || info.Size()>40<<20 || info.Size()<=0 {
        return nil,fmt.Errorf("invalid bounded ILN replica file")
    }
    raw,err:=os.ReadFile(path);if err!=nil{return nil,err}
    var stored evmInterchain.ILNReplica
    if err:=json.Unmarshal(raw,&stored);err!=nil{return nil,fmt.Errorf("corrupt ILN replica: %w",err)}
    if stored.CheckpointBlock==0{return nil,fmt.Errorf("ILN replica has no confirmed source block")}
    c.BlockNumber=stored.CheckpointBlock
    replica,err:=evmInterchain.LoadVerifiedILNReplica(path,c)
    if err!=nil{return nil,fmt.Errorf("ILN replica does not match signed checkpoint: %w",err)}
    target:=types.StringToHash(messageID)
    index:=-1
    for i,id:=range replica.Leaves {
        if id==target {
            if index>=0{return nil,fmt.Errorf("ambiguous Merkle message insertion")}
            index=i
        }
    }
    if index<0{return nil,fmt.Errorf("message ID not in verified Hook prefix")}
    proof,err:=evmInterchain.BuildILNReplicaProof(replica,c,uint32(index))
    if err!=nil{return nil,err}
    result:=&ilnReplicaProofRPC{
        Route:route,MessageID:messageID,OriginChainID:c.ChainID,
        Hook:hook.String(),Root:root.String(),
        CheckpointBlock:c.BlockNumber,
        CheckpointIndex:attestation.Index,MessageIndex:uint32(index),
        Quorum:attestation,
    }
    for i,sibling:=range proof {result.MerkleProof[i]=sibling.String()}
    return result,nil
}
func isILNProofHash(s string)bool {
    if len(s)!=66||!strings.HasPrefix(s,"0x"){return false}
    for _,ch:=range s[2:] {if (ch<'0'||ch>'9')&&(ch<'a'||ch>'f')&&(ch<'A'||ch>'F'){return false}}
    return !strings.EqualFold(s,"0x"+strings.Repeat("0",64))
}
func isILNProofAddress(s string)bool {
    if len(s)!=42||!strings.HasPrefix(s,"0x"){return false}
    for _,ch:=range s[2:] {if (ch<'0'||ch>'9')&&(ch<'a'||ch>'f')&&(ch<'A'||ch>'F'){return false}}
    return !strings.EqualFold(s,"0x"+strings.Repeat("0",40))
}
