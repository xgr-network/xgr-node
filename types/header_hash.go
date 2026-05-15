package types

import (
	"sync"

	"github.com/umbracle/fastrlp"
	"github.com/xgr-network/xgr-node/helper/keccak"
)

var (
	HeaderHash   func(h *Header) Hash
	headerHashMu sync.RWMutex
)

// This is the default header hash for the block.
// In IBFT, this header hash method is substituted
// for Istanbul Header Hash calculation
func init() {
	SetHeaderHash(defHeaderHash)
}

var marshalArenaPool fastrlp.ArenaPool

func defHeaderHash(h *Header) (hash Hash) {
	// default header hashing
	ar := marshalArenaPool.Get()
	hasher := keccak.DefaultKeccakPool.Get()

	v := h.MarshalRLPWith(ar)
	hasher.WriteRlp(hash[:0], v)

	marshalArenaPool.Put(ar)
	keccak.DefaultKeccakPool.Put(hasher)

	return
}

// ComputeHash computes the hash of the header
func (h *Header) ComputeHash() *Header {
	h.Hash = GetHeaderHash()(h)

	return h
}

// SetHeaderHash updates the active header hash function.
func SetHeaderHash(hashFn func(h *Header) Hash) {
	headerHashMu.Lock()
	HeaderHash = hashFn
	headerHashMu.Unlock()
}

// GetHeaderHash returns the active header hash function.
func GetHeaderHash() func(h *Header) Hash {
	headerHashMu.RLock()
	hashFn := HeaderHash
	headerHashMu.RUnlock()

	return hashFn
}
