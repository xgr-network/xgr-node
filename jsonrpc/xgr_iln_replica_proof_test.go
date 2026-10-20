package jsonrpc

import (
    "encoding/json"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "testing"

    "github.com/stretchr/testify/require"
    "github.com/xgr-network/xgr-node/crypto"
    evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
    "github.com/xgr-network/xgr-node/types"
)

func testRPCOneLeafRoot(leaf types.Hash)types.Hash{
    zero:=types.ZeroHash
    for l:=0;l<32;l++{
        leaf=crypto.Keccak256Hash(append(append([]byte(nil),leaf.Bytes()...),zero.Bytes()...))
        zero=crypto.Keccak256Hash(append(append([]byte(nil),zero.Bytes()...),zero.Bytes()...))
    }
    return leaf
}

func TestGetILNReplicaProofIsPublicReadOnlyAndSourceRootBound(t *testing.T){
    dir:=t.TempDir()
    route:="base_to_xgr"
    hook:=types.StringToAddress("0x4444444444444444444444444444444444444444")
    message:=crypto.Keccak256Hash([]byte("public Hyperlane message bytes"))
    root:=testRPCOneLeafRoot(message)
    c:=evmInterchain.ILNReplicaCheckpoint{
        ChainID:8453,Hook:hook,Root:root,Count:1,BlockNumber:128,
    }
    replica:=evmInterchain.ILNReplica{
        ChainID:c.ChainID,Hook:hook,Root:root,
        CheckpointBlock:128,Leaves:[]types.Hash{message},
    }
    path:=filepath.Join(dir,"interchain","iln","replicas",
        strconv.FormatUint(c.ChainID,10),strings.ToLower(hook.String())+".json")
    require.NoError(t,evmInterchain.StoreVerifiedILNReplica(path,replica,c))
    attest:=interchainAttestationRPC{
        Version:"XGR_ILN_CHECKPOINT_V2",Chain:route,
        OriginChainID:c.ChainID,OriginDomain:8453,DestinationDomain:1643,
        SetID:7,SourceBlockNumber:128,
        RouteID:"0x"+strings.Repeat("a",64),
        SourceRouter:"0x"+strings.Repeat("7",40),
        MerkleTreeHook:hook.String(),
        AuthorizedMessageID:message.String(),Root:root.String(),Index:0,
        ValidatorFeeWei:"100",Payload:"0xabcdef",AggregateSignature:"0x1234",
    }
    raw,err:=json.Marshal(attest);require.NoError(t,err)
    attPath:=filepath.Join(dir,"interchain","attestations",route,message.String()+".json")
    require.NoError(t,os.MkdirAll(filepath.Dir(attPath),0o770))
    require.NoError(t,os.WriteFile(attPath,raw,0o660))
    endpoint:=newXGREndpoint(nil,dir)
    got,err:=endpoint.GetILNReplicaProof(route,message.String())
    require.NoError(t,err)
    require.Equal(t,uint32(0),got.MessageIndex)
    require.Equal(t,c.Root.String(),got.Root)
    require.Equal(t,c.BlockNumber,got.CheckpointBlock)
    require.Len(t,got.MerkleProof,32)
    require.NotNil(t,got.Quorum)
    require.Equal(t,attest.SetID,got.Quorum.SetID)
    _,err=endpoint.GetILNReplicaProof("../base_to_xgr",message.String())
    require.Error(t,err)
    _,err=endpoint.GetILNReplicaProof(route,"0x"+strings.Repeat("b",64))
    require.Error(t,err)
    // On-disk tampering cannot change a proof or produce a forged result.
    replica.Leaves[0]=crypto.Keccak256Hash([]byte("corrupt"))
    tampered,err:=json.Marshal(replica);require.NoError(t,err)
    require.NoError(t,os.WriteFile(path,tampered,0o660))
    _,err=endpoint.GetILNReplicaProof(route,message.String())
    require.ErrorContains(t,err,"does not match signed checkpoint")
}
