package evm

import (
    "encoding/json"
    "fmt"
    "net/http"
    "net/http/httptest"
    "testing"
    "errors"

    "github.com/stretchr/testify/require"
    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

func TestILNReplicaExtensionRejectsByzantineTailAndPreservesExistingData(t *testing.T){
    hook:=types.StringToAddress("0x1111111111111111111111111111111111111111")
    hashes:=[]types.Hash{
        crypto.Keccak256Hash([]byte("m0")),
        crypto.Keccak256Hash([]byte("m1")),
        crypto.Keccak256Hash([]byte("m2")),
        crypto.Keccak256Hash([]byte("m3")),
    }
    oldTree:=&ilnMerkleAccumulator{}
    for i:=0;i<2;i++{require.NoError(t,oldTree.insert(uint32(i),hashes[i]))}
    old:=ILNReplica{ChainID:8453,Hook:hook,CheckpointBlock:32,Root:oldTree.root(),Leaves:hashes[:2]}
    oldCanonical:=ILNReplicaCheckpoint{ChainID:8453,Hook:hook,BlockNumber:32,Root:old.Root,Count:2}
    require.NoError(t,VerifyILNReplica(old,oldCanonical))
    newTree:=&ilnMerkleAccumulator{}
    for i,id:=range hashes{require.NoError(t,newTree.insert(uint32(i),id))}
    next:=ILNReplicaCheckpoint{ChainID:8453,Hook:hook,BlockNumber:64,Root:newTree.root(),Count:4}
    valid:=[]ILNReplicaChunk{{ChainID:8453,Hook:hook,Offset:2,Leaves:hashes[2:]}}
    extension,err:=ExtendILNReplica(next,old,valid)
    require.NoError(t,err)
    require.Equal(t,hashes,extension.Leaves)
    require.NoError(t,VerifyILNReplica(extension,next))
    tampered:=append([]ILNReplicaChunk(nil),valid...)
    tampered[0].Leaves=append([]types.Hash(nil),hashes[2:]...)
    tampered[0].Leaves[0]=crypto.Keccak256Hash([]byte("attacker"))
    _,err=ExtendILNReplica(next,old,tampered)
    require.ErrorIs(t,err,ErrILNReplicaUnverified)
    require.Equal(t,hashes[:2],old.Leaves)
    _,err=ExtendILNReplica(next,old,[]ILNReplicaChunk{{ChainID:8453,Hook:hook,Offset:3,Leaves:hashes[2:]}})
    require.ErrorIs(t,err,ErrILNReplicaUnverified)
    _,err=ExtendILNReplica(next,old,nil)
    require.ErrorIs(t,err,ErrILNReplicaUnverified)
    // A live root that has not changed can be re-anchored without RPC logs.
    same:=oldCanonical
    same.BlockNumber=1024
    refreshed,err:=ExtendILNReplica(same,old,nil)
    require.NoError(t,err)
    require.Equal(t,uint64(1024),refreshed.CheckpointBlock)
}

func TestILNReplicaLiveCatchUpUsesOnlyRecentHookEvents(t *testing.T){
    hook:=types.StringToAddress("0x1111111111111111111111111111111111111111")
    registry:=types.StringToAddress("0x2222222222222222222222222222222222222222")
    leaves:=[]types.Hash{
        crypto.Keccak256Hash([]byte("old")),crypto.Keccak256Hash([]byte("new")),
    }
    oldTree:=&ilnMerkleAccumulator{}
    require.NoError(t,oldTree.insert(0,leaves[0]))
    old:=ILNReplica{ChainID:8453,Hook:hook,CheckpointBlock:32,Root:oldTree.root(),Leaves:leaves[:1]}
    tree:=&ilnMerkleAccumulator{}
    require.NoError(t,tree.insert(0,leaves[0]))
    require.NoError(t,tree.insert(1,leaves[1]))
    canonical:=ILNReplicaCheckpoint{ChainID:8453,Hook:hook,Root:tree.root(),Count:2,BlockNumber:64}
    log:=testILNInsertedLog(hook,leaves[1],1,54)
    var requestedLogs int
    failLogs:=false
    server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
        var req struct{
            ID json.RawMessage `json:"id"`
            Method string `json:"method"`
            Params []json.RawMessage `json:"params"`
        }
        require.NoError(t,json.NewDecoder(r.Body).Decode(&req))
        w.Header().Set("Content-Type","application/json")
        var result interface{}
        switch req.Method {
        case "eth_chainId":result="0x2105"
        case "eth_blockNumber":result="0x50" // 80 - 12 => confirmed 68
        case "eth_getLogs":
            requestedLogs++
            var f struct{From string `json:"fromBlock"`;To string `json:"toBlock"`}
            require.NoError(t,json.Unmarshal(req.Params[0],&f))
            require.Equal(t,"0x21",f.From)
            require.Equal(t,"0x40",f.To)
            if failLogs{
                _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"error":{"code":-32005,"message":"RPC pruned"}}`,req.ID)
                return
            }
            result=[]interface{}{log}
        default:t.Errorf("unexpected RPC method %s",req.Method)
        }
        data,err:=json.Marshal(result);require.NoError(t,err)
        _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":%s}`,req.ID,data)
    }))
    defer server.Close()
    source:=&Destination{Name:"base",ChainID:8453,Domain:8453,
        ILNRegistryAddress:registry.String(),RPCURL:server.URL,Confirmations:12}
    result,err:=CatchUpILNReplicaFromRecentHook(source,old,canonical)
    require.NoError(t,err)
    require.Equal(t,leaves,result.Leaves)
    require.Equal(t,1,requestedLogs)
    failLogs=true
    _,err=CatchUpILNReplicaFromRecentHook(source,old,canonical)
    require.True(t,errors.Is(err,ErrILNReplicaPeerRequired))
    old.CheckpointBlock=1
    canonical.BlockNumber=200 // 199 blocks behind => do not query historical logs
    _,err=CatchUpILNReplicaFromRecentHook(source,old,canonical)
    require.ErrorIs(t,err,ErrILNReplicaPeerRequired)
    require.Equal(t,2,requestedLogs)
}

func TestILNReplicaSparseChainRecoverAfterThousandsOfIdleBlocks(t *testing.T){
    hook:=types.StringToAddress("0x1111111111111111111111111111111111111111")
    registry:=types.StringToAddress("0x2222222222222222222222222222222222222222")
    leaves:=[]types.Hash{
        crypto.Keccak256Hash([]byte("old")),crypto.Keccak256Hash([]byte("new")),
    }
    oldTree:=&ilnMerkleAccumulator{}
    require.NoError(t,oldTree.insert(0,leaves[0]))
    old:=ILNReplica{ChainID:8453,Hook:hook,CheckpointBlock:32,Root:oldTree.root(),Leaves:leaves[:1]}
    tree:=&ilnMerkleAccumulator{}
    require.NoError(t,tree.insert(0,leaves[0]))
    require.NoError(t,tree.insert(1,leaves[1]))
    canonical:=ILNReplicaCheckpoint{ChainID:8453,Hook:hook,Root:tree.root(),Count:2,BlockNumber:7000}
    log:=testILNInsertedLog(hook,leaves[1],1,6992)
    var requestedLogs int
    failLogs:=false
    server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
        var req struct{
            ID json.RawMessage `json:"id"`
            Method string `json:"method"`
            Params []json.RawMessage `json:"params"`
        }
        require.NoError(t,json.NewDecoder(r.Body).Decode(&req))
        w.Header().Set("Content-Type","application/json")
        var result interface{}
        switch req.Method {
        case "eth_chainId":result="0x2105"
        case "eth_blockNumber":result="0x1b65" // 7013 - 12 => confirmed 7001
        case "eth_getLogs":
            requestedLogs++
            var f struct{From string `json:"fromBlock"`;To string `json:"toBlock"`}
            require.NoError(t,json.Unmarshal(req.Params[0],&f))
            require.Equal(t,"0x1ad9",f.From)
            require.Equal(t,"0x1b58",f.To)
            if failLogs{
                _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"error":{"code":-32005,"message":"RPC pruned"}}`,req.ID)
                return
            }
            result=[]interface{}{log}
        default:t.Errorf("unexpected RPC method %s",req.Method)
        }
        data,err:=json.Marshal(result);require.NoError(t,err)
        _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":%s}`,req.ID,data)
    }))
    defer server.Close()
    source:=&Destination{Name:"base",ChainID:8453,Domain:8453,
        ILNRegistryAddress:registry.String(),RPCURL:server.URL,Confirmations:12}
    result,err:=CatchUpILNReplicaFromRecentHook(source,old,canonical)
    require.NoError(t,err)
    require.Equal(t,leaves,result.Leaves)
    require.Equal(t,1,requestedLogs)
    require.Equal(t,1,requestedLogs)
}
