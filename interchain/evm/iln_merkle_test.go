package evm

import (
	"errors"
	"os"
	"path/filepath"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

func TestILNMerkleAccumulatorMatchesIncrementalTree(t *testing.T) {
	leaves := []types.Hash{
		types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		types.StringToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
	}
	tree := &ilnMerkleAccumulator{}
	for i, leaf := range leaves {
		require.NoError(t, tree.insert(uint32(i), leaf))
	}
	require.Equal(t, uint64(3), tree.count)
	// Independently arrange the three leaves in the fixed-depth sparse tree.
	expected := ilnMerkleParent(
		ilnMerkleParent(leaves[0], leaves[1]),
		ilnMerkleParent(leaves[2], types.ZeroHash),
	)
	zero := ilnMerkleParent(types.ZeroHash, types.ZeroHash)
	zero = ilnMerkleParent(zero, zero)
	for level := 2; level < ilnMerkleDepth; level++ {
		expected = ilnMerkleParent(expected, zero)
		zero = ilnMerkleParent(zero, zero)
	}
	require.Equal(t, expected, tree.root())
	require.Error(t, tree.insert(3, types.ZeroHash), "zero message ID must fail closed")
	require.Error(t, tree.insert(2, leaves[0]), "duplicate index must fail closed")
	require.Error(t, tree.insert(5, leaves[0]), "missing insertion must fail closed")
}

func TestParseILNInsertionLogFailClosed(t *testing.T) {
	msg := types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	topic := ethgo.Hash(crypto.Keccak256Hash([]byte("InsertedIntoTree(bytes32,uint32)")))
	data := append(append([]byte(nil), msg.Bytes()...), make([]byte, 32)...)
	data[63] = 5
	id, index, err := parseILNInsertionLog(&ethgo.Log{Topics: []ethgo.Hash{topic}, Data: data})
	require.NoError(t, err)
	require.Equal(t, msg, id)
	require.Equal(t, uint32(5), index)
	id, index, err = parseILNInsertionLog(&ethgo.Log{Topics: []ethgo.Hash{topic, ethgo.Hash(msg)}, Data: data[32:]})
	require.NoError(t, err)
	require.Equal(t, msg, id)
	require.Equal(t, uint32(5), index)
	_, _, err = parseILNInsertionLog(&ethgo.Log{Topics: []ethgo.Hash{topic}, Data: data[:32]})
	require.Error(t, err)
	data[32] = 1 // Noncanonical uint32 value.
	_, _, err = parseILNInsertionLog(&ethgo.Log{Topics: []ethgo.Hash{topic}, Data: data})
	require.Error(t, err)
}

func testILNInsertedLog(hook types.Address, id types.Hash, index uint32, block uint64) map[string]interface{} {
	word := make([]byte, 32)
	word[28] = byte(index >> 24)
	word[29] = byte(index >> 16)
	word[30] = byte(index >> 8)
	word[31] = byte(index)
	topic := crypto.Keccak256Hash([]byte("InsertedIntoTree(bytes32,uint32)"))
	txHash := crypto.Keccak256Hash(append(id.Bytes(), byte(index)))
	return map[string]interface{}{
		"address": hook.String(),
		"topics": []string{topic.String()},
		"data": "0x" + hex.EncodeToString(append(append([]byte(nil), id.Bytes()...), word...)),
		"blockNumber": fmt.Sprintf("0x%x", block),
		"logIndex": "0x0",
		"transactionIndex": "0x0",
		"transactionHash": txHash.String(),
		"removed": false,
	}
}

func TestILNCheckpointUsesConfirmedHookStateWithoutHistoricalLogs(t *testing.T) {
 hook:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 registry:=types.StringToAddress("0x2222222222222222222222222222222222222222")
 mailbox:=types.StringToAddress("0x3333333333333333333333333333333333333333")
 message:=types.StringToHash("0xf735484813a91eda5c60e959f4038b97ea518de5a9e92de440bd5612c1f387a5")
 tree:=&ilnMerkleAccumulator{}
 require.NoError(t,tree.insert(0,message))
 selector:=func(name string)string{return crypto.Keccak256Hash([]byte(name)).String()[:10]}
 var logs,badCalls atomic.Int32
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  var req struct{ID json.RawMessage `json:"id"`;Method string `json:"method"`;Params []json.RawMessage `json:"params"`}
  _=json.NewDecoder(r.Body).Decode(&req)
  var output interface{}
  switch req.Method {
  case "eth_chainId":output="0x2105"
  case "eth_blockNumber":output="0x8e" // 142 - 12 confirmations = 130; epoch checkpoint = 128.
  case "eth_getLogs":
   logs.Add(1)
   _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"error":{"code":-32005,"message":"historical logs unavailable"}}`,req.ID)
   return
  case "eth_call":
   if len(req.Params)!=2 || string(req.Params[1])!=`"latest"` {
    badCalls.Add(1)
    _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"error":{"code":-32603,"message":"unavailable historical state"}}`,req.ID)
    return
   }
   var call struct{Data string `json:"data"`}
   _=json.Unmarshal(req.Params[0],&call)
   if len(call.Data)<10 {t.Errorf("short eth_call");return}
   switch strings.ToLower(call.Data[:10]) {
   case selector("mailbox()"):output=fmt.Sprintf("0x%064x",mailbox.Bytes())
   case selector("count()"):output=fmt.Sprintf("0x%064x",1)
   case selector("root()"):output=tree.root().String()
   default:t.Errorf("unexpected getter: %s",call.Data);return
   }
  default:t.Errorf("unexpected rpc: %s",req.Method);return
  }
  encoded,_:=json.Marshal(output)
  w.Header().Set("Content-Type","application/json")
  _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":%s}`,req.ID,encoded)
 }))
 defer srv.Close()
 source:=&Destination{Name:"base",ChainID:8453,Domain:8453,RPCURL:srv.URL,ILNRegistryAddress:registry.String(),Confirmations:12}
 snapshot:=&ILNRouteSnapshot{Registry:registry,Route:protocol.ILNRoute{Mailbox:mailbox,MerkleTreeHook:hook},BlockNumber:110}
 got,err:=GetConfirmedILNCheckpoint(source,snapshot)
 require.NoError(t,err)
 require.Equal(t,tree.root(),got.Root)
 require.Equal(t,uint32(0),got.Index)
 require.Equal(t,uint64(142),got.BlockNumber)
 require.Zero(t,logs.Load())
 require.Zero(t,badCalls.Load())
 snapshot.BlockNumber=131
 _,err=GetConfirmedILNCheckpoint(source,snapshot)
 require.ErrorContains(t,err,"not sufficiently confirmed")
}

func TestILNHookAllowsAdvancingHeadButRejectsChangedRoot(t *testing.T) {
 hook:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 mailbox:=types.StringToAddress("0x3333333333333333333333333333333333333333")
 leaf:=crypto.Keccak256Hash([]byte("immutable insertion"))
 tree:=&ilnMerkleAccumulator{}
 require.NoError(t,tree.insert(0,leaf))
 selector:=func(name string)string{return crypto.Keccak256Hash([]byte(name)).String()[:10]}
 var height atomic.Int32
 var rootReads atomic.Int32
 var poison atomic.Bool
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
   var req struct{ID json.RawMessage `json:"id"`;Method string `json:"method"`;Params []json.RawMessage `json:"params"`}
   require.NoError(t,json.NewDecoder(r.Body).Decode(&req))
   var out interface{}
   switch req.Method {
   case "eth_chainId":out="0x2105"
   case "eth_blockNumber":
      out=fmt.Sprintf("0x%x",200+height.Add(1))
   case "eth_call":
      require.Len(t,req.Params,2)
      require.Equal(t,`"latest"`,string(req.Params[1]))
      var call struct{Data string `json:"data"`}
      require.NoError(t,json.Unmarshal(req.Params[0],&call))
      switch strings.ToLower(call.Data[:10]){
      case selector("mailbox()"):out=fmt.Sprintf("0x%064x",mailbox.Bytes())
      case selector("count()"):out=fmt.Sprintf("0x%064x",1)
      case selector("root()"):
          if poison.Load() && rootReads.Add(1)%2==0{out=crypto.Keccak256Hash([]byte("poison")).String()}else{out=tree.root().String()}
      default:t.Errorf("unexpected getter: %s",call.Data);return
      }
   default:t.Errorf("unexpected JSON-RPC %s",req.Method);return
   }
   raw,_:=json.Marshal(out)
   _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":%s}`,req.ID,raw)
 }))
 defer srv.Close()
 source:=&Destination{Name:"base",ChainID:8453,Domain:8453,RPCURL:srv.URL,ILNRegistryAddress:"0x2222222222222222222222222222222222222222",Confirmations:1}
 snapshot:=&ILNRouteSnapshot{Route:protocol.ILNRoute{Mailbox:mailbox,MerkleTreeHook:hook},BlockNumber:10}
 got,err:=GetConfirmedILNCheckpoint(source,snapshot)
 require.NoError(t,err)
 require.Equal(t,tree.root(),got.Root)
 require.EqualValues(t,203,got.BlockNumber)
 // On the next read, the second root differs at the same count; reject it.
 poison.Store(true)
 _,err=GetConfirmedILNCheckpoint(source,snapshot)
 require.Error(t,err)
}

func TestILNRouteSnapshotUsesAppendOnlyCurrentBindings(t *testing.T) {
	registry := types.StringToAddress("0x1111111111111111111111111111111111111111")
	gateway := types.StringToAddress("0x2222222222222222222222222222222222222222")
	router := types.StringToAddress("0x3333333333333333333333333333333333333333")
	mailbox := types.StringToAddress("0x4444444444444444444444444444444444444444")
	hook := types.StringToAddress("0x5555555555555555555555555555555555555555")
	destinationRouter := types.StringToAddress("0x6666666666666666666666666666666666666666")
	factory := types.StringToAddress("0x7777777777777777777777777777777777777777")
	routeID := types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	sel := func(signature string) string {
		return crypto.Keccak256Hash([]byte(signature)).String()[:10]
	}
	addressWord := func(addr types.Address) string {
		return fmt.Sprintf("%064x", addr.Bytes())
	}
	routeWord := fmt.Sprintf("%064x%064x%s%s%s%s%s%064x%064x",
		8453, 8453, addressWord(gateway), addressWord(router),
		addressWord(mailbox), addressWord(hook), addressWord(destinationRouter),
		200, 1)
	var historicalCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{
			ID json.RawMessage `json:"id"`
			Method string `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		var output interface{}
		switch req.Method {
		case "eth_chainId": output = "0x2105"
		case "eth_blockNumber": output = "0x200"
		case "eth_call":
			if len(req.Params) != 2 || string(req.Params[1]) != `"latest"` {
				historicalCalls.Add(1)
				_,_ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32603,"message":"state not found"}}`,req.ID)
				return
			}
			var call struct{
				To string `json:"to"`
				Data string `json:"data"`
			}
			_ = json.Unmarshal(req.Params[0], &call)
			switch strings.ToLower(call.Data[:10]) {
			case sel("factory()"):
				output = "0x" + addressWord(factory)
			case sel("getRoute(uint32,bytes32)"):
				output = "0x" + routeWord
			case sel("ilnRegistry()"):
				output = "0x" + addressWord(registry)
			case sel("warpRouter()"):
				output = "0x" + addressWord(router)
			case sel("mailbox()"):
				output = "0x" + addressWord(mailbox)
			case sel("merkleTreeHook()"):
				output = "0x" + addressWord(hook)
			default:
				_,_ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"unknown getter"}}`,req.ID)
				return
			}
		default:
			_,_ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"unknown method"}}`,req.ID)
			return
		}
		encoded,_:=json.Marshal(output)
		_,_ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`,req.ID,encoded)
	}))
	defer srv.Close()
	source := &Destination{
		Name: "base", ChainID: 8453, Domain: 8453,
		RPCURL: srv.URL, ILNRegistryAddress: registry.String(), Confirmations: 1,
	}
	snapshot, err := GetILNRouteAtBlock(source, 1643, routeID, 110)
	require.NoError(t, err)
	require.Equal(t, uint64(110), snapshot.BlockNumber)
	require.Equal(t, registry, snapshot.Registry)
	require.Equal(t, gateway, snapshot.Route.Gateway)
	require.Equal(t, router, snapshot.Route.SourceRouter)
	require.Equal(t, mailbox, snapshot.Route.Mailbox)
	require.Equal(t, hook, snapshot.Route.MerkleTreeHook)
	require.Equal(t, destinationRouter, snapshot.Route.DestinationRouter)
	// The present-day global fee can differ from the fee in the original receipt.
	require.Equal(t, int64(200), snapshot.Route.ValidatorFeeWei.Int64())
	require.EqualValues(t, 0, historicalCalls.Load())
}


func TestILNMerkleDurableReplayIncrementalReorgAndCorruption(t *testing.T) {
 dir:=t.TempDir()
 hook:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 a:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 b:=types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
 c:=types.StringToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
 replacement:=types.StringToHash("0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
 hashes:=map[uint64]types.Hash{
  20:a, 22:b, 24:c,
 }
 read:=func(n uint64)(types.Hash,error){
  if id,ok:=hashes[n];ok{return id,nil}
  return types.ZeroHash,errors.New("unavailable block hash")
 }
 events:=map[uint64]types.Hash{10:a,19:b,21:c}
 var starts []uint64
 replay:=func(start uint64,tree *ilnMerkleAccumulator,
  save func(uint64,*ilnMerkleAccumulator)error)(*ilnMerkleAccumulator,error){
  starts=append(starts,start)
  var to uint64=20
  if len(starts)>1{to=22}
  if len(starts)>2{to=24}
  for block:=start;block<=to;block++ {
   if id,ok:=events[block];ok{
    require.NoError(t,tree.insert(uint32(tree.count),id))
   }
  }
  require.NoError(t,save(to,tree))
  return tree,nil
 }
 result,err:=cachedILNMerkleReplay(dir,8453,hook,10,20,read,replay)
 require.NoError(t,err)
 require.Equal(t,uint64(2),result.count)
 require.Equal(t,[]uint64{10},starts)
 require.FileExists(t,ilnMerkleCachePath(dir,8453,hook,10))
 // Saved state reuses the verified block-20 anchor across requests/restarts.
 result,err=cachedILNMerkleReplay(dir,8453,hook,10,20,read,replay)
 require.NoError(t,err)
 require.Equal(t,uint64(2),result.count)
 require.Equal(t,[]uint64{10},starts)
 result,err=cachedILNMerkleReplay(dir,8453,hook,10,22,read,replay)
 require.NoError(t,err)
 require.Equal(t,uint64(3),result.count)
 require.Equal(t,[]uint64{10,21},starts)
 // A different canonical hash must invalidate and replay the entire prefix.
 hashes[22]=replacement
 events[21]=replacement
 result,err=cachedILNMerkleReplay(dir,8453,hook,10,24,read,replay)
 require.NoError(t,err)
 require.Equal(t,uint64(3),result.count)
 require.Equal(t,[]uint64{10,21,10},starts)
 expected:=&ilnMerkleAccumulator{}
 require.NoError(t,expected.insert(0,a))
 require.NoError(t,expected.insert(1,b))
 require.NoError(t,expected.insert(2,replacement))
 require.Equal(t,expected.root(),result.root())
 // An attacker/corruption cannot silently replace the on-disk branch.
 path:=ilnMerkleCachePath(dir,8453,hook,10)
 require.NoError(t,os.WriteFile(path,[]byte("{corrupt"),0o660))
 result,err=cachedILNMerkleReplay(dir,8453,hook,10,24,read,func(start uint64,tree *ilnMerkleAccumulator,
  save func(uint64,*ilnMerkleAccumulator)error)(*ilnMerkleAccumulator,error){
  require.Equal(t,uint64(10),start)
  for _,id:=range []types.Hash{a,b,replacement}{
   require.NoError(t,tree.insert(uint32(tree.count),id))
  }
  require.NoError(t,save(24,tree))
  return tree,nil
 })
 require.NoError(t,err)
 require.Equal(t,expected.root(),result.root())
 // A cache for Base can never authorize the same hook on another chain.
 require.NotEqual(t,path,ilnMerkleCachePath(dir,1643,hook,10))
 require.NotEqual(t,path,ilnMerkleCachePath(filepath.Join(dir,"other"),8453,hook,10))
}

func TestILNMerkleDurableReplayFailsClosedWhenLogsInvalid(t *testing.T) {
 hook:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 hash:=types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
 fail:=errors.New("noncanonical insertion")
 result,err:=cachedILNMerkleReplay(t.TempDir(),8453,hook,1,10,
  func(uint64)(types.Hash,error){return hash,nil},
  func(uint64,*ilnMerkleAccumulator,func(uint64,*ilnMerkleAccumulator)error)(*ilnMerkleAccumulator,error){
   return nil,fail
  })
 require.Nil(t,result)
 require.ErrorIs(t,err,fail)
}
