package evm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/jsonrpc"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

// Historic checkpoint proofs depend on canonical logs/receipts, not state
// snapshots. Do not substitute an unverified off-chain indexer or a latest
// root for the root at the signed source block.
var errILNMerkleMissingPrefix = errors.New("ILN hook history begins after insertion zero")

const ilnMerkleDepth = 32

type ilnMerkleAccumulator struct {
	count uint64
	branch [ilnMerkleDepth]types.Hash
}

func ilnMerkleParent(left, right types.Hash) types.Hash {
	pair := make([]byte, 64)
	copy(pair[:32], left.Bytes())
	copy(pair[32:], right.Bytes())
	return crypto.Keccak256Hash(pair)
}

func (t *ilnMerkleAccumulator) insert(index uint32, messageID types.Hash) error {
	if messageID == types.ZeroHash || t.count >= uint64(1)<<32 || uint64(index) != t.count {
		return fmt.Errorf("noncanonical ILN insertion index %d (expected %d)", index, t.count)
	}
	node := messageID
	n := t.count
	for level := 0; level < ilnMerkleDepth; level++ {
		if n&1 == 0 {
			t.branch[level] = node
			t.count++
			return nil
		}
		node = ilnMerkleParent(t.branch[level], node)
		n >>= 1
	}
	return fmt.Errorf("ILN Merkle tree capacity exceeded")
}

func (t *ilnMerkleAccumulator) root() types.Hash {
	root := types.ZeroHash
	zero := types.ZeroHash
	for level := 0; level < ilnMerkleDepth; level++ {
		if (t.count>>level)&1 == 1 {
			root = ilnMerkleParent(t.branch[level], root)
		} else {
			root = ilnMerkleParent(root, zero)
		}
		zero = ilnMerkleParent(zero, zero)
	}
	return root
}

// Hyperlane InsertedIntoTree(bytes32,uint32) exists in both non-indexed and
// indexed ABI layouts. Never infer missing data or accept truncated words.
func parseILNInsertionLog(log *ethgo.Log) (types.Hash, uint32, error) {
	if log == nil || log.Removed {
		return types.ZeroHash, 0, fmt.Errorf("removed or missing ILN insertion")
	}
	topic := ethgo.Hash(crypto.Keccak256Hash([]byte("InsertedIntoTree(bytes32,uint32)")))
	if len(log.Topics) == 0 || log.Topics[0] != topic {
		return types.ZeroHash, 0, fmt.Errorf("invalid ILN insertion event signature")
	}
	var id types.Hash
	var word []byte
	switch len(log.Topics) {
	case 1:
		if len(log.Data) != 64 {
			return types.ZeroHash, 0, fmt.Errorf("invalid unindexed ILN insertion event length")
		}
		id = types.BytesToHash(log.Data[:32])
		word = log.Data[32:64]
	case 2:
		if len(log.Data) != 32 {
			return types.ZeroHash, 0, fmt.Errorf("invalid indexed ILN insertion event length")
		}
		id = types.Hash(log.Topics[1])
		word = log.Data
	case 3:
		if len(log.Data) != 0 {
			return types.ZeroHash, 0, fmt.Errorf("invalid fully-indexed ILN insertion event length")
		}
		id = types.Hash(log.Topics[1])
		word = log.Topics[2][:]
	default:
		return types.ZeroHash, 0, fmt.Errorf("invalid ILN insertion topic count")
	}
	index := new(big.Int).SetBytes(word)
	if !index.IsUint64() || index.Uint64() > uint64(^uint32(0)) || id == types.ZeroHash {
		return types.ZeroHash, 0, fmt.Errorf("invalid ILN insertion index or message")
	}
	return id, uint32(index.Uint64()), nil
}

func reconstructILNMerkleFromLogs(
	client *jsonrpc.Client, hook types.Address, from, to uint64,
) (*ilnMerkleAccumulator, error) {
	return replayILNMerkleLogs(client, hook, from, to, &ilnMerkleAccumulator{}, nil)
}

// replayILNMerkleLogs accepts only a previously canonical, independently
// reconstructed accumulator. Each processed range can be checkpointed atomically
// after all logs pass contiguous-index and canonical-order checks.
func replayILNMerkleLogs(
	client *jsonrpc.Client, hook types.Address, from, to uint64,
	tree *ilnMerkleAccumulator, onRange func(uint64, *ilnMerkleAccumulator) error,
) (*ilnMerkleAccumulator, error) {
	if client == nil || tree == nil || hook == types.ZeroAddress || from == 0 || to < from {
		return nil, fmt.Errorf("invalid ILN Merkle replay bounds")
	}
	topic := ethgo.Hash(crypto.Keccak256Hash([]byte("InsertedIntoTree(bytes32,uint32)")))
	var lastBlock, lastIndex uint64
	var haveLast bool
	for begin := from; begin <= to; {
		end := to
		if end-begin+1 > maxILNLogRange {
			end = begin + maxILNLogRange - 1
		}
		filter := &ethgo.LogFilter{
			Address: []ethgo.Address{ethgo.Address(hook)},
			Topics: [][]*ethgo.Hash{{&topic}},
		}
		filter.SetFromUint64(begin)
		filter.SetToUint64(end)
		logs, err := client.Eth().GetLogs(filter)
		if err != nil {
			return nil, fmt.Errorf("get ILN insertion logs at %d-%d: %w", begin, end, err)
		}
		sort.SliceStable(logs, func(i, j int) bool {
			if logs[i].BlockNumber != logs[j].BlockNumber {
				return logs[i].BlockNumber < logs[j].BlockNumber
			}
			return logs[i].LogIndex < logs[j].LogIndex
		})
		for _, log := range logs {
			if log == nil || log.BlockNumber < begin || log.BlockNumber > end ||
				types.Address(log.Address) != hook {
				return nil, fmt.Errorf("untrusted ILN insertion log outside requested scope")
			}
			if haveLast && (log.BlockNumber < lastBlock ||
				(log.BlockNumber == lastBlock && log.LogIndex <= lastIndex)) {
				return nil, fmt.Errorf("duplicate or out-of-order ILN insertion logs")
			}
			messageID, index, err := parseILNInsertionLog(log)
			if err != nil {
				return nil, err
			}
			if tree.count == 0 && index > 0 && from > 1 {
				return nil, errILNMerkleMissingPrefix
			}
			if err := tree.insert(index, messageID); err != nil {
				return nil, err
			}
			lastBlock, lastIndex, haveLast = log.BlockNumber, log.LogIndex, true
		}
		if onRange != nil {
			if err := onRange(end, tree); err != nil { return nil, err }
		}
		if end == to {
			break
		}
		begin = end + 1
	}
	return tree, nil
}

// ILN Merkle cache v1 is a local performance accelerator, NEVER a source of
// validator authority. Every cached accumulator was produced by strict canonical
// log replay and must remain anchored to the canonical source block hash.
// In particular, unknown/corrupt/pruned checkpoints are not trusted.
type ilnMerkleDiskCheckpoint struct {
 Version int `json:"version"`
 ChainID uint64 `json:"chainId"`
 Hook string `json:"hook"`
 StartBlock uint64 `json:"startBlock"`
 ThroughBlock uint64 `json:"throughBlock"`
 BlockHash types.Hash `json:"blockHash"`
 Count uint64 `json:"count"`
 Branch []types.Hash `json:"branch"`
 Digest string `json:"digest"`
}
var ilnMerkleReplayLocks sync.Map

func (record ilnMerkleDiskCheckpoint) checksum() (string,error) {
 record.Digest=""
 raw,err:=json.Marshal(record)
 if err!=nil{return "",err}
 sum:=sha256.Sum256(raw)
 return hex.EncodeToString(sum[:]),nil
}

func validateILNMerkleCheckpoint(record ilnMerkleDiskCheckpoint,chainID uint64,hook types.Address,from,to uint64) bool {
 if record.Version!=1||record.ChainID!=chainID||
    record.Hook!=hook.String()||record.StartBlock!=from||
    record.ThroughBlock<from||record.ThroughBlock>to||
    record.BlockHash==types.ZeroHash||record.Count>=uint64(1)<<32||
    len(record.Branch)!=ilnMerkleDepth||len(record.Digest)!=64 {return false}
 digest,err:=record.checksum()
 return err==nil&&digest==record.Digest
}

func ilnMerkleCachePath(dir string,chainID uint64,hook types.Address,from uint64) string {
 return filepath.Join(dir,fmt.Sprintf("v1-%d-%s-%d.json",chainID,hook.String(),from))
}

func readILNMerkleDiskCheckpoint(path string) (*ilnMerkleDiskCheckpoint,error) {
 raw,err:=os.ReadFile(path)
 if err!=nil{return nil,err}
 if len(raw)>16384{return nil,fmt.Errorf("ILN Merkle cache checkpoint oversized")}
 var record ilnMerkleDiskCheckpoint
 if err:=json.Unmarshal(raw,&record);err!=nil{return nil,err}
 return &record,nil
}

func saveILNMerkleDiskCheckpoint(path string,record ilnMerkleDiskCheckpoint) error {
 digest,err:=record.checksum()
 if err!=nil{return err}
 record.Digest=digest
 raw,err:=json.Marshal(record)
 if err!=nil{return err}
 if err:=os.MkdirAll(filepath.Dir(path),0o770);err!=nil{return err}
 f,err:=os.CreateTemp(filepath.Dir(path),".iln-merkle-*")
 if err!=nil{return err}
 defer os.Remove(f.Name())
 if err=f.Chmod(0o660);err!=nil{f.Close();return err}
 if _,err=f.Write(raw);err!=nil{f.Close();return err}
 if err=f.Sync();err!=nil{f.Close();return err}
 if err=f.Close();err!=nil{return err}
 return os.Rename(f.Name(),path)
}

// cachedILNMerkleReplay serializes requests for the same independent
// source-chain/hook/start-boundary. It can handle restarts and reorgs without
// trusting old roots: a cached height is reused ONLY if its canonical block
// hash still matches the source chain. A mismatch forces full log replay.
// The readBlock and replay callbacks are injectable for deterministic tests.
func cachedILNMerkleReplay(
 dir string,chainID uint64,hook types.Address,from,to uint64,
 readBlock func(uint64)(types.Hash,error),
 replay func(uint64,*ilnMerkleAccumulator,func(uint64,*ilnMerkleAccumulator) error)(*ilnMerkleAccumulator,error),
) (*ilnMerkleAccumulator,error) {
 if dir==""||chainID==0||hook==types.ZeroAddress||from==0||to<from||
    readBlock==nil||replay==nil{return nil,fmt.Errorf("invalid ILN Merkle cache configuration")}
 path:=ilnMerkleCachePath(dir,chainID,hook,from)
 value,_:=ilnMerkleReplayLocks.LoadOrStore(path,&sync.Mutex{})
 lock:=value.(*sync.Mutex)
 lock.Lock()
 defer lock.Unlock()

 seed:=&ilnMerkleAccumulator{}
 begin:=from
 var anchor *ilnMerkleDiskCheckpoint
 if record,err:=readILNMerkleDiskCheckpoint(path);err==nil &&
    validateILNMerkleCheckpoint(*record,chainID,hook,from,to) {
  if canonical,err:=readBlock(record.ThroughBlock);err==nil && canonical==record.BlockHash {
   seed.count=record.Count
   copy(seed.branch[:],record.Branch)
   begin=record.ThroughBlock+1
   anchor=record
  }
 }
 if anchor!=nil&&anchor.ThroughBlock==to{return seed,nil}
 saveRange:=func(height uint64,current *ilnMerkleAccumulator) error {
  canonical,err:=readBlock(height)
  if err!=nil||canonical==types.ZeroHash{
   // Do not persist checkpoints which cannot be anchored on-chain.
   // Replayed logs still remain usable for this single verification.
   return nil
  }
  record:=ilnMerkleDiskCheckpoint{
   Version:1,ChainID:chainID,Hook:hook.String(),
   StartBlock:from,ThroughBlock:height,BlockHash:canonical,
   Count:current.count,Branch:append([]types.Hash(nil),current.branch[:]...),
  }
  // Disk space errors may degrade performance but never change the trusted
  // in-memory result of the already validated log replay.
  _=saveILNMerkleDiskCheckpoint(path,record)
  return nil
 }
 result,err:=replay(begin,seed,saveRange)
 if err!=nil{return nil,err}
 // Recheck the reused prefix after the replay, so a reorg during scanning
 // cannot combine a stale prefix with current-chain suffix logs.
 if anchor!=nil{
  canonical,err:=readBlock(anchor.ThroughBlock)
  if err!=nil||canonical!=anchor.BlockHash {
   _=os.Remove(path)
   return replay(from,&ilnMerkleAccumulator{},saveRange)
  }
 }
 return result,nil
}

func reconstructILNMerkleFromLogsCached(
 client *jsonrpc.Client,source *Destination,hook types.Address,from,to uint64,
) (*ilnMerkleAccumulator,error) {
 if source==nil||source.ILNMerkleCacheDir=="" {
  return reconstructILNMerkleFromLogs(client,hook,from,to)
 }
 readBlock:=func(height uint64)(types.Hash,error){
  if height==0{return types.ZeroHash,fmt.Errorf("invalid canonical ILN checkpoint block")}
  block,err:=client.Eth().GetBlockByNumber(ethgo.BlockNumber(height),false)
  if err!=nil{return types.ZeroHash,err}
  if block==nil||block.Hash==(ethgo.Hash{}) {
   return types.ZeroHash,fmt.Errorf("canonical ILN checkpoint block unavailable")
  }
  return types.Hash(block.Hash),nil
 }
 return cachedILNMerkleReplay(source.ILNMerkleCacheDir,source.ChainID,hook,from,to,
  readBlock,func(begin uint64,seed *ilnMerkleAccumulator,
    save func(uint64,*ilnMerkleAccumulator)error)(*ilnMerkleAccumulator,error){
   return replayILNMerkleLogs(client,hook,begin,to,seed,save)
  })
}
