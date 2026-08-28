package itrie

import "fmt"

// GetNodeStrict resolves a hash-referenced trie node and treats a missing key as
// corruption instead of an ordinary lookup miss. Hash references are structural
// links inside an already materialized trie; once such a reference exists, the
// referenced node must exist as well.
func GetNodeStrict(root []byte, storage Storage) (Node, error) {
	node, ok, err := GetNode(root, storage)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("missing referenced trie node %x", root)
	}

	return node, nil
}
