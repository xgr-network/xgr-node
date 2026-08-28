package itrie

import (
	"context"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
	ldbstorage "github.com/syndtr/goleveldb/leveldb/storage"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
)

func newSweeperTestStorage(t *testing.T) *KVStorage {
	t.Helper()

	mem := ldbstorage.NewMemStorage()
	db, err := leveldb.Open(mem, nil)
	if err != nil {
		t.Fatal(err)
	}

	storage := NewKV(db)
	t.Cleanup(func() {
		_ = storage.Close()
	})

	return storage
}

func commitSweeperTestRoot(t *testing.T, storage *KVStorage) types.Hash {
	t.Helper()

	st := NewState(storage)
	snapshot := st.NewSnapshot()
	account := &state.Object{
		Address:  types.BytesToAddress([]byte{0x01}),
		Balance:  big.NewInt(100),
		Root:     types.EmptyRootHash,
		CodeHash: types.EmptyCodeHash,
	}
	_, rootBytes, err := snapshot.Commit([]*state.Object{account})
	if err != nil {
		t.Fatal(err)
	}

	return types.BytesToHash(rootBytes)
}

func sweepTestOptions() TrieSweepOptions {
	return TrieSweepOptions{
		DeleteBatch: 1,
		ScanBatch:   1,
		MarkBatch:   1,
		Pause:       time.Nanosecond,
		Compact:     false,
	}
}

func TestTrieSweeperDeletesUnreachableAndKeepsRetainedRoot(t *testing.T) {
	storage := newSweeperTestStorage(t)
	root := commitSweeperTestRoot(t, storage)

	garbageKey := types.BytesToHash([]byte("unreachable-garbage-key-000000001"))
	if err := storage.Put(garbageKey.Bytes(), []byte{0xc0}); err != nil {
		t.Fatal(err)
	}

	sweeper, err := NewTrieSweeper(storage, filepath.Join(t.TempDir(), "gc"))
	if err != nil {
		t.Fatal(err)
	}
	defer sweeper.Close()

	stats, err := sweeper.SweepLive(context.Background(), []types.Hash{root}, sweepTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Deleted == 0 {
		t.Fatal("expected at least one unreachable trie key to be deleted")
	}

	if _, ok, err := storage.Get(garbageKey.Bytes()); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("unreachable garbage key still exists after sweep")
	}

	checked, err := HashChecker(root.Bytes(), storage)
	if err != nil {
		t.Fatal(err)
	}
	if checked != root {
		t.Fatalf("retained root mismatch: expected %s, got %s", root, checked)
	}
}

func TestTrieSweeperProtectsWritesAfterGenerationBoundary(t *testing.T) {
	storage := newSweeperTestStorage(t)

	sweeper, err := NewTrieSweeper(storage, filepath.Join(t.TempDir(), "gc"))
	if err != nil {
		t.Fatal(err)
	}
	defer sweeper.Close()

	generation, err := sweeper.beginLiveGeneration()
	if err != nil {
		t.Fatal(err)
	}

	newKey := types.BytesToHash([]byte("written-after-boundary-00000000000001"))
	if err := storage.Put(newKey.Bytes(), []byte{0xc0}); err != nil {
		t.Fatal(err)
	}

	deleted, retained, err := sweeper.deleteCandidates([][]byte{newKey.Bytes()}, generation-1)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 || retained != 1 {
		t.Fatalf("new write not protected: deleted=%d retained=%d", deleted, retained)
	}

	if _, ok, err := storage.Get(newKey.Bytes()); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatal("write created after generation boundary was deleted")
	}
}

func TestTrieSweeperProtectsResurrectedHash(t *testing.T) {
	storage := newSweeperTestStorage(t)

	key := types.BytesToHash([]byte("resurrected-content-addressed-key-01"))
	if err := storage.db.Put(key.Bytes(), []byte{0xc0}, nil); err != nil {
		t.Fatal(err)
	}

	sweeper, err := NewTrieSweeper(storage, filepath.Join(t.TempDir(), "gc"))
	if err != nil {
		t.Fatal(err)
	}
	defer sweeper.Close()

	generation, err := sweeper.beginLiveGeneration()
	if err != nil {
		t.Fatal(err)
	}

	// Rewriting the same content-addressed key after the generation boundary
	// must protect it from a candidate list derived before that rewrite.
	if err := storage.Put(key.Bytes(), []byte{0xc0}); err != nil {
		t.Fatal(err)
	}

	deleted, retained, err := sweeper.deleteCandidates([][]byte{key.Bytes()}, generation-1)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 || retained != 1 {
		t.Fatalf("resurrected hash not protected: deleted=%d retained=%d", deleted, retained)
	}
}

func TestTrieSweeperNeverDeletesUnknownKeys(t *testing.T) {
	storage := newSweeperTestStorage(t)
	root := commitSweeperTestRoot(t, storage)

	unknownKey := []byte("xgr-trie-metadata")
	unknownValue := []byte("keep-me")
	if err := storage.db.Put(unknownKey, unknownValue, nil); err != nil {
		t.Fatal(err)
	}

	sweeper, err := NewTrieSweeper(storage, filepath.Join(t.TempDir(), "gc"))
	if err != nil {
		t.Fatal(err)
	}
	defer sweeper.Close()

	if _, err := sweeper.SweepLive(context.Background(), []types.Hash{root}, sweepTestOptions()); err != nil {
		t.Fatal(err)
	}

	value, err := storage.db.Get(unknownKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != string(unknownValue) {
		t.Fatalf("unknown key changed: expected %q, got %q", unknownValue, value)
	}
}
