package evm

import (
	"errors"
	"fmt"
	"math/big"
	"sort"

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
	if client == nil || hook == types.ZeroAddress || from == 0 || to < from {
		return nil, fmt.Errorf("invalid ILN Merkle replay bounds")
	}
	topic := ethgo.Hash(crypto.Keccak256Hash([]byte("InsertedIntoTree(bytes32,uint32)")))
	tree := &ilnMerkleAccumulator{}
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
		if end == to {
			break
		}
		begin = end + 1
	}
	return tree, nil
}
