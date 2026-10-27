package itrie

import (
	"bytes"
	"fmt"
	"github.com/umbracle/fastrlp"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
)

// checkedLookup distinguishes proven absence from an unavailable/corrupt trie
// node. It does not alter the legacy reader or cache partially decoded nodes.
func (t *Trie) checkedLookup(key []byte, storage Storage) ([]byte, bool, error) {
	search := bytesToHexNibbles(key)
	node := t.root
	for {
		switch n := node.(type) {
		case nil:
			return nil, false, nil
		case *ValueNode:
			if n.hash {
				child, err := GetNodeStrict(n.buf, storage)
				if err != nil {
					return nil, false, err
				}
				node = child
				continue
			}
			if len(search) == 0 {
				return n.buf, true, nil
			}
			return nil, false, nil
		case *ShortNode:
			if len(search) < len(n.key) || !bytes.Equal(search[:len(n.key)], n.key) {
				return nil, false, nil
			}
			search = search[len(n.key):]
			node = n.child
		case *FullNode:
			if len(search) == 0 {
				node = n.value
			} else {
				node = n.getEdge(search[0])
				search = search[1:]
			}
		default:
			return nil, false, fmt.Errorf("invalid trie node %T", node)
		}
	}
}

func (s *Snapshot) GetAccountChecked(addr types.Address) (*state.Account, error) {
	data, ok, err := s.trie.checkedLookup(crypto.Keccak256(addr.Bytes()), s.state.storage)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	var account state.Account
	if err := account.UnmarshalRlp(data); err != nil {
		return nil, err
	}
	return &account, nil
}
func (s *Snapshot) GetStorageChecked(addr types.Address, root, key types.Hash) (types.Hash, error) {
	trie, err := s.state.newTrieAt(root)
	if err != nil {
		return types.ZeroHash, err
	}
	data, ok, err := trie.checkedLookup(crypto.Keccak256(key.Bytes()), s.state.storage)
	if err != nil {
		return types.ZeroHash, err
	}
	if !ok {
		return types.ZeroHash, nil
	}
	p := &fastrlp.Parser{}
	v, err := p.Parse(data)
	if err != nil {
		return types.ZeroHash, err
	}
	raw, err := v.GetBytes(nil)
	if err != nil {
		return types.ZeroHash, err
	}
	if len(raw) > 32 {
		return types.ZeroHash, fmt.Errorf("storage value exceeds 32 bytes")
	}
	return types.BytesToHash(raw), nil
}
