package itrie

import (
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"math/big"
	"testing"
)

func TestSecurityCheckedReads(t *testing.T) {
	st := NewState(NewMemoryStorage())
	s := st.NewSnapshot()
	tx := state.NewTxn(s)
	a := types.Address{1}
	tx.SetNonce(a, 1)
	for n := int64(1); n <= 100; n++ {
		tx.SetState(a, types.BytesToHash(big.NewInt(n).Bytes()), types.BytesToHash(big.NewInt(n*2).Bytes()))
	}
	objs, err := tx.Commit(false)
	require.NoError(t, err)
	committed, _, err := s.Commit(objs)
	require.NoError(t, err)
	snap := committed.(*Snapshot)
	account, err := snap.GetAccountChecked(a)
	require.NoError(t, err)
	require.NotNil(t, account)
	for n := int64(0); n <= 101; n++ {
		key := types.BytesToHash(big.NewInt(n).Bytes())
		v, err := snap.GetStorageChecked(a, account.Root, key)
		require.NoError(t, err)
		require.Equal(t, snap.GetStorage(a, account.Root, key), v)
	}
	_, err = snap.GetStorageChecked(a, types.Hash{1}, types.ZeroHash)
	require.Error(t, err)
	// A hashed child that cannot be loaded is an error, never proof of absence.
	tr := &Trie{root: &ValueNode{hash: true, buf: make([]byte, 32)}}
	_, _, err = tr.checkedLookup(make([]byte, 32), st.storage)
	require.Error(t, err)
}
