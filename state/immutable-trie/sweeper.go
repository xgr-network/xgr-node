package itrie

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
	xcrypto "github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
)

const (
	gcLivePrefix         byte = 'L'
	gcVisitAccountPrefix byte = 'A'
	gcVisitStoragePrefix byte = 'S'
	gcVisitCodePrefix    byte = 'C'

	defaultGCDeleteBatch = 256
	defaultGCScanBatch   = 4096
	defaultGCMarkBatch   = 2048
)

var defaultGCPause = 10 * time.Millisecond

// TrieSweepOptions controls one online mark-and-sweep pass.
// Small batches and pauses keep block processing ahead of GC work.
type TrieSweepOptions struct {
	DeleteBatch int
	ScanBatch   int
	MarkBatch   int
	Pause       time.Duration
	Compact     bool
}

// TrieSweepStats summarizes one completed sweep generation.
type TrieSweepStats struct {
	Generation uint64
	Roots      uint64
	Marked     uint64
	Scanned    uint64
	Deleted    uint64
	Retained   uint64
	Skipped    uint64
	Duration   time.Duration
}

// TrieSweeper performs online mark-and-sweep GC on the immutable trie LevelDB.
//
// Every normal trie write is generation-marked before it reaches the trie DB.
// A sweep keeps the current and immediately previous write generation in
// addition to everything reachable from the retained canonical state roots.
// The one-generation grace protects state written just before a GC snapshot but
// canonicalized just after it.
type TrieSweeper struct {
	storage *KVStorage
	marker  *leveldb.DB
	runMu   sync.Mutex
}

// NewTrieSweeper enables write tracking and creates a temporary marker DB.
// The marker DB is disposable metadata and is rebuilt on every node start.
func NewTrieSweeper(storage *KVStorage, workDir string) (*TrieSweeper, error) {
	if storage == nil || storage.db == nil {
		return nil, errors.New("nil trie storage")
	}
	if workDir == "" {
		return nil, errors.New("empty trie GC work directory")
	}

	if err := os.RemoveAll(workDir); err != nil {
		return nil, fmt.Errorf("reset trie GC work directory: %w", err)
	}
	if err := os.MkdirAll(workDir, 0o770); err != nil {
		return nil, fmt.Errorf("create trie GC work directory: %w", err)
	}

	markerPath := filepath.Join(workDir, "marks")
	marker, err := leveldb.OpenFile(markerPath, nil)
	if err != nil {
		return nil, fmt.Errorf("open trie GC marker DB: %w", err)
	}

	storage.gcBarrier.Lock()
	if storage.gcMarker != nil {
		storage.gcBarrier.Unlock()
		_ = marker.Close()
		return nil, errors.New("trie GC tracking already enabled")
	}
	storage.gcMarker = marker
	storage.gcGeneration = 1
	storage.gcBarrier.Unlock()

	return &TrieSweeper{
		storage: storage,
		marker:  marker,
	}, nil
}

// Close disables write tracking and closes the temporary marker DB.
func (s *TrieSweeper) Close() error {
	if s == nil {
		return nil
	}

	s.runMu.Lock()
	defer s.runMu.Unlock()

	s.storage.gcBarrier.Lock()
	if s.storage.gcMarker == s.marker {
		s.storage.gcMarker = nil
		s.storage.gcGeneration = 0
	}
	s.storage.gcBarrier.Unlock()

	if s.marker == nil {
		return nil
	}

	err := s.marker.Close()
	s.marker = nil
	return err
}

// Sweep marks all trie nodes and contract code reachable from roots, then scans
// the LevelDB snapshot and deletes unreachable trie/code keys in small batches.
func (s *TrieSweeper) Sweep(ctx context.Context, roots []types.Hash, options TrieSweepOptions) (*TrieSweepStats, error) {
	if s == nil || s.storage == nil || s.marker == nil {
		return nil, errors.New("trie sweeper is not initialized")
	}
	if len(roots) == 0 {
		return nil, errors.New("no retained state roots supplied")
	}

	s.runMu.Lock()
	defer s.runMu.Unlock()

	options = normalizeSweepOptions(options)
	started := time.Now()

	snapshot, generation, err := s.beginSnapshot()
	if err != nil {
		return nil, err
	}
	released := false
	defer func() {
		if !released {
			snapshot.Release()
		}
	}()

	stats := &TrieSweepStats{
		Generation: generation,
		Roots:      uint64(len(roots)),
	}

	marker := newGCMarkerWriter(s.marker, generation, options.MarkBatch)
	reader := &levelDBSnapshotStorage{snapshot: snapshot}

	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if root == types.ZeroHash || root == types.EmptyRootHash {
			continue
		}
		if err := markTrieHash(ctx, root.Bytes(), reader, marker, nil, false, stats, options); err != nil {
			return nil, fmt.Errorf("mark retained state root %s: %w", root, err)
		}
	}
	if err := marker.Flush(); err != nil {
		return nil, fmt.Errorf("flush trie GC marks: %w", err)
	}

	keepGeneration := generation - 1
	iter := snapshot.NewIterator(nil, nil)
	candidates := make([][]byte, 0, options.DeleteBatch)

	flushCandidates := func() error {
		if len(candidates) == 0 {
			return nil
		}
		deleted, retained, err := s.deleteCandidates(candidates, keepGeneration)
		if err != nil {
			return err
		}
		stats.Deleted += deleted
		stats.Retained += retained
		candidates = candidates[:0]
		return pauseGC(ctx, options.Pause)
	}

	for iter.Next() {
		if err := ctx.Err(); err != nil {
			iter.Release()
			return nil, err
		}

		stats.Scanned++
		key := append([]byte(nil), iter.Key()...)
		if !isSweepableTrieKey(key) {
			stats.Skipped++
			continue
		}

		markedGeneration, ok, err := readGCGeneration(s.marker, gcMarkerKey(gcLivePrefix, key))
		if err != nil {
			iter.Release()
			return nil, fmt.Errorf("read trie GC mark: %w", err)
		}
		if ok && markedGeneration >= keepGeneration {
			stats.Retained++
			continue
		}

		candidates = append(candidates, key)
		if len(candidates) >= options.DeleteBatch {
			if err := flushCandidates(); err != nil {
				iter.Release()
				return nil, err
			}
		}

		if options.ScanBatch > 0 && stats.Scanned%uint64(options.ScanBatch) == 0 {
			if err := pauseGC(ctx, options.Pause); err != nil {
				iter.Release()
				return nil, err
			}
		}
	}
	if err := iter.Error(); err != nil {
		iter.Release()
		return nil, fmt.Errorf("iterate trie snapshot: %w", err)
	}
	iter.Release()

	if err := flushCandidates(); err != nil {
		return nil, err
	}

	// Release the snapshot before compaction so LevelDB can physically reclaim
	// obsolete tables instead of retaining them for snapshot visibility.
	snapshot.Release()
	released = true

	if options.Compact && stats.Deleted > 0 {
		if err := s.compact(ctx, options.Pause); err != nil {
			return nil, err
		}
	}

	stats.Duration = time.Since(started)
	return stats, nil
}

func normalizeSweepOptions(options TrieSweepOptions) TrieSweepOptions {
	if options.DeleteBatch <= 0 {
		options.DeleteBatch = defaultGCDeleteBatch
	}
	if options.ScanBatch <= 0 {
		options.ScanBatch = defaultGCScanBatch
	}
	if options.MarkBatch <= 0 {
		options.MarkBatch = defaultGCMarkBatch
	}
	if options.Pause < 0 {
		options.Pause = 0
	}
	if options.Pause == 0 {
		options.Pause = defaultGCPause
	}
	return options
}

func (s *TrieSweeper) beginSnapshot() (*leveldb.Snapshot, uint64, error) {
	s.storage.gcBarrier.Lock()
	defer s.storage.gcBarrier.Unlock()

	if s.storage.gcMarker != s.marker {
		return nil, 0, errors.New("trie GC write tracking is not active")
	}

	s.storage.gcGeneration++
	generation := s.storage.gcGeneration
	snapshot, err := s.storage.db.GetSnapshot()
	if err != nil {
		return nil, 0, fmt.Errorf("create trie LevelDB snapshot: %w", err)
	}

	return snapshot, generation, nil
}

func (s *TrieSweeper) deleteCandidates(keys [][]byte, keepGeneration uint64) (uint64, uint64, error) {
	s.storage.gcBarrier.Lock()
	defer s.storage.gcBarrier.Unlock()

	deleteBatch := new(leveldb.Batch)
	markerCleanup := new(leveldb.Batch)
	var deleted uint64
	var retained uint64

	for _, key := range keys {
		generation, ok, err := readGCGeneration(s.marker, gcMarkerKey(gcLivePrefix, key))
		if err != nil {
			return 0, 0, fmt.Errorf("recheck trie GC mark: %w", err)
		}
		if ok && generation >= keepGeneration {
			retained++
			continue
		}

		deleteBatch.Delete(key)
		markerCleanup.Delete(gcMarkerKey(gcLivePrefix, key))
		markerCleanup.Delete(gcMarkerKey(gcVisitAccountPrefix, key))
		markerCleanup.Delete(gcMarkerKey(gcVisitStoragePrefix, key))
		markerCleanup.Delete(gcMarkerKey(gcVisitCodePrefix, key))
		deleted++
	}

	if deleted == 0 {
		return 0, retained, nil
	}
	if err := s.storage.db.Write(deleteBatch, nil); err != nil {
		return 0, retained, fmt.Errorf("delete unreachable trie nodes: %w", err)
	}
	if err := s.marker.Write(markerCleanup, nil); err != nil {
		// The trie deletion is already durable. Stale marker metadata is safe: it
		// can only retain extra garbage in a later cycle, never delete live state.
		return deleted, retained, fmt.Errorf("clean trie GC marker metadata: %w", err)
	}

	return deleted, retained, nil
}

func (s *TrieSweeper) compact(ctx context.Context, pause time.Duration) error {
	for i := 0; i < 256; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		start := []byte{byte(i)}
		var limit []byte
		if i < 255 {
			limit = []byte{byte(i + 1)}
		}
		if err := s.storage.db.CompactRange(util.Range{Start: start, Limit: limit}); err != nil {
			return fmt.Errorf("compact trie LevelDB range %d: %w", i, err)
		}
		if err := pauseGC(ctx, pause); err != nil {
			return err
		}
	}

	return nil
}

func isSweepableTrieKey(key []byte) bool {
	if len(key) == types.HashLength {
		return true
	}
	return len(key) == len(codePrefix)+types.HashLength && bytes.Equal(key[:len(codePrefix)], codePrefix)
}

func gcMarkerKey(prefix byte, key []byte) []byte {
	out := make([]byte, 1+len(key))
	out[0] = prefix
	copy(out[1:], key)
	return out
}

func encodeGCGeneration(generation uint64) []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, generation)
	return out
}

func readGCGeneration(db *leveldb.DB, key []byte) (uint64, bool, error) {
	value, err := db.Get(key, nil)
	if errors.Is(err, leveldb.ErrNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if len(value) != 8 {
		return 0, false, fmt.Errorf("invalid GC generation value length %d", len(value))
	}
	return binary.BigEndian.Uint64(value), true, nil
}

func pauseGC(ctx context.Context, pause time.Duration) error {
	if pause <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(pause)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type gcMarkerWriter struct {
	db         *leveldb.DB
	generation uint64
	value      []byte
	batch      *leveldb.Batch
	pending    map[string]struct{}
	maxBatch   int
	count      int
}

func newGCMarkerWriter(db *leveldb.DB, generation uint64, maxBatch int) *gcMarkerWriter {
	return &gcMarkerWriter{
		db:         db,
		generation: generation,
		value:      encodeGCGeneration(generation),
		batch:      new(leveldb.Batch),
		pending:    make(map[string]struct{}),
		maxBatch:   maxBatch,
	}
}

func (w *gcMarkerWriter) ensureVisited(prefix byte, key []byte) (bool, error) {
	markerKey := gcMarkerKey(prefix, key)
	if _, ok := w.pending[string(markerKey)]; ok {
		return false, nil
	}
	generation, ok, err := readGCGeneration(w.db, markerKey)
	if err != nil {
		return false, err
	}
	if ok && generation == w.generation {
		return false, nil
	}

	w.batch.Put(markerKey, w.value)
	w.pending[string(markerKey)] = struct{}{}
	w.count++
	if w.count >= w.maxBatch {
		if err := w.Flush(); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (w *gcMarkerWriter) markLive(key []byte) error {
	w.batch.Put(gcMarkerKey(gcLivePrefix, key), w.value)
	w.count++
	if w.count >= w.maxBatch {
		return w.Flush()
	}
	return nil
}

func (w *gcMarkerWriter) Flush() error {
	if w.count == 0 {
		return nil
	}
	if err := w.db.Write(w.batch, nil); err != nil {
		return err
	}
	w.batch.Reset()
	w.pending = make(map[string]struct{})
	w.count = 0
	return nil
}

type levelDBSnapshotStorage struct {
	snapshot *leveldb.Snapshot
}

func (s *levelDBSnapshotStorage) Put([]byte, []byte) error {
	return errors.New("read-only trie snapshot")
}

func (s *levelDBSnapshotStorage) Get(key []byte) ([]byte, bool, error) {
	value, err := s.snapshot.Get(key, nil)
	if errors.Is(err, leveldb.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (s *levelDBSnapshotStorage) Batch() Batch {
	return readOnlyBatch{}
}

func (s *levelDBSnapshotStorage) SetCode(types.Hash, []byte) error {
	return errors.New("read-only trie snapshot")
}

func (s *levelDBSnapshotStorage) GetCode(hash types.Hash) ([]byte, bool) {
	value, ok, err := s.Get(GetCodeKey(hash))
	return value, ok && err == nil
}

func (s *levelDBSnapshotStorage) Close() error {
	return nil
}

type readOnlyBatch struct{}

func (readOnlyBatch) Put([]byte, []byte) {}
func (readOnlyBatch) Write() error      { return errors.New("read-only trie snapshot") }

func markTrieHash(
	ctx context.Context,
	nodeHash []byte,
	storage Storage,
	marker *gcMarkerWriter,
	agg []byte,
	isStorage bool,
	stats *TrieSweepStats,
	options TrieSweepOptions,
) error {
	if len(nodeHash) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	visitPrefix := gcVisitAccountPrefix
	if isStorage {
		visitPrefix = gcVisitStoragePrefix
	}
	fresh, err := marker.ensureVisited(visitPrefix, nodeHash)
	if err != nil {
		return err
	}
	if !fresh {
		return nil
	}

	node, data, err := getCustomNode(nodeHash, storage)
	if err != nil {
		return err
	}
	if node == nil || len(data) == 0 {
		return fmt.Errorf("missing trie node %x", nodeHash)
	}
	if err := marker.markLive(nodeHash); err != nil {
		return err
	}
	stats.Marked++
	if options.MarkBatch > 0 && stats.Marked%uint64(options.MarkBatch) == 0 {
		if err := pauseGC(ctx, options.Pause); err != nil {
			return err
		}
	}

	return markTrieNode(ctx, node, storage, marker, agg, isStorage, stats, options)
}

func markTrieNode(
	ctx context.Context,
	node Node,
	storage Storage,
	marker *gcMarkerWriter,
	agg []byte,
	isStorage bool,
	stats *TrieSweepStats,
	options TrieSweepOptions,
) error {
	switch n := node.(type) {
	case nil:
		return nil
	case *FullNode:
		if len(n.hash) > 0 {
			return markTrieHash(ctx, n.hash, storage, marker, agg, isStorage, stats, options)
		}
		for i, child := range n.children {
			if child == nil {
				continue
			}
			if err := markTrieNode(ctx, child, storage, marker, append(agg, uint8(i)), isStorage, stats, options); err != nil {
				return err
			}
		}
		return markTrieNode(ctx, n.value, storage, marker, agg, isStorage, stats, options)

	case *ShortNode:
		if len(n.hash) > 0 {
			return markTrieHash(ctx, n.hash, storage, marker, agg, isStorage, stats, options)
		}
		return markTrieNode(ctx, n.child, storage, marker, append(agg, n.key...), isStorage, stats, options)

	case *ValueNode:
		if n.hash {
			return markTrieHash(ctx, n.buf, storage, marker, agg, isStorage, stats, options)
		}
		if isStorage {
			return nil
		}

		var account state.Account
		if err := account.UnmarshalRlp(n.buf); err != nil {
			return fmt.Errorf("parse account %s: %w", hex.EncodeToString(encodeCompact(agg)), err)
		}

		if account.CodeHash != nil && !bytes.Equal(account.CodeHash, emptyCodeHash) {
			codeHash := types.BytesToHash(account.CodeHash)
			codeKey := GetCodeKey(codeHash)
			fresh, err := marker.ensureVisited(gcVisitCodePrefix, codeKey)
			if err != nil {
				return err
			}
			if fresh {
				code, found := storage.GetCode(codeHash)
				if !found {
					return fmt.Errorf("can't find code %s", hex.EncodeToString(account.CodeHash))
				}
				if actual := xcrypto.Keccak256(code); !bytes.Equal(actual, account.CodeHash) {
					return fmt.Errorf(
						"contract code hash mismatch: expected %s, got %s",
						hex.EncodeToString(account.CodeHash),
						hex.EncodeToString(actual),
					)
				}
				if err := marker.markLive(codeKey); err != nil {
					return err
				}
				stats.Marked++
			}
		}

		if account.Root != types.EmptyRootHash {
			return markTrieHash(ctx, account.Root.Bytes(), storage, marker, nil, true, stats, options)
		}
		return nil

	default:
		return fmt.Errorf("unknown trie node type %T", node)
	}
}
