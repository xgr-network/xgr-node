package itrie

import (
	"errors"
	"fmt"

	"github.com/umbracle/fastrlp"
	"github.com/xgr-network/xgr-node/types"
)

// HashCheckerStrict recalculates a state root while requiring every hash-linked
// trie node to be present. It is intended as an integrity check after online GC:
// a missing referenced node is corruption and must never be interpreted as an
// empty subtree.
func HashCheckerStrict(stateRoot []byte, storage Storage) (types.Hash, error) {
	root := types.BytesToHash(stateRoot)
	if root == types.EmptyRootHash {
		return types.EmptyRootHash, nil
	}

	node, err := GetNodeStrict(stateRoot, storage)
	if err != nil {
		return types.Hash{}, err
	}

	h, ok := hasherPool.Get().(*hasher)
	if !ok {
		return types.Hash{}, errors.New("can't get hasher")
	}
	defer hasherPool.Put(h)

	arena, _ := h.AcquireArena()
	defer h.ReleaseArenas(0)

	val, err := hashCheckerStrict(node, h, arena, 0, storage)
	if err != nil {
		return types.Hash{}, err
	}
	if val == nil {
		return emptyStateHash, nil
	}

	return types.BytesToHash(val.Raw()), nil
}

func hashCheckerStrict(node Node, h *hasher, a *fastrlp.Arena, d int, storage Storage) (*fastrlp.Value, error) {
	var (
		val *fastrlp.Value
		aa  *fastrlp.Arena
		idx int
	)

	switch n := node.(type) {
	case nil:
		return nil, nil
	case *ValueNode:
		if n.hash {
			nd, err := GetNodeStrict(n.buf, storage)
			if err != nil {
				return nil, err
			}

			return hashCheckerStrict(nd, h, a, d, storage)
		}

		return a.NewCopyBytes(n.buf), nil

	case *ShortNode:
		child, err := hashCheckerStrict(n.child, h, a, d+1, storage)
		if err != nil {
			return nil, err
		}

		val = a.NewArray()
		val.Set(a.NewBytes(encodeCompact(n.key)))
		val.Set(child)

	case *FullNode:
		val = a.NewArray()
		aa, idx = h.AcquireArena()

		for _, child := range n.children {
			if child == nil {
				val.Set(a.NewNull())
				continue
			}

			v, err := hashCheckerStrict(child, h, aa, d+1, storage)
			if err != nil {
				return nil, err
			}
			val.Set(v)
		}

		if n.value == nil {
			val.Set(a.NewNull())
		} else {
			v, err := hashCheckerStrict(n.value, h, a, d+1, storage)
			if err != nil {
				return nil, err
			}
			val.Set(v)
		}

	default:
		return nil, fmt.Errorf("unknown node type %T", node)
	}

	if val.Len() < 32 {
		return val, nil
	}

	h.buf = val.MarshalTo(h.buf[:0])
	if aa != nil {
		h.ReleaseArenas(idx)
	}

	tmp := h.Hash(h.buf)
	hh := node.SetHash(tmp)

	return a.NewCopyBytes(hh), nil
}
