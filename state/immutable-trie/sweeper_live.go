package itrie

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/syndtr/goleveldb/leveldb/util"
	"github.com/xgr-network/xgr-node/types"
)

// SweepLive performs the production online GC path without holding a long-lived
// LevelDB snapshot. Trie and code entries are content-addressed and immutable,
// and no deletion is performed during the mark phase, so retained roots can be
// traversed safely from the live database. Writes concurrent with the cycle are
// protected by the current/previous GC generations.
//
// The sweep phase uses short-lived scan iterators in bounded chunks. This is
// important operationally: a LevelDB snapshot or iterator pins the SSTables it
// can see. Holding one across a multi-hour full-database sweep can therefore
// temporarily retain many gigabytes of obsolete SSTables. Here every iterator
// is released before its candidate keys are deleted, and each first-byte range
// is compacted only after its scan has completed and only if GC deleted data in
// that range.
func (s *TrieSweeper) SweepLive(
	ctx context.Context,
	roots []types.Hash,
	options TrieSweepOptions,
) (*TrieSweepStats, error) {
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

	generation, err := s.beginLiveGeneration()
	if err != nil {
		return nil, err
	}

	stats := &TrieSweepStats{
		Generation: generation,
		Roots:      uint64(len(roots)),
	}

	marker := newGCMarkerWriter(s.marker, generation, options.MarkBatch)

	// Mark against the live immutable trie. Existing hash-addressed nodes are not
	// modified, and this sweeper cannot delete anything until marking completes.
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if root == types.ZeroHash || root == types.EmptyRootHash {
			continue
		}
		if err := markTrieHash(ctx, root.Bytes(), s.storage, marker, nil, false, stats, options); err != nil {
			return nil, fmt.Errorf("mark retained state root %s: %w", root, err)
		}
	}
	if err := marker.Flush(); err != nil {
		return nil, fmt.Errorf("flush trie GC marks: %w", err)
	}

	keepGeneration := generation - 1

	// Raw trie hashes are uniformly distributed across all first-byte ranges;
	// code entries live in the 'c' range. Scanning range-by-range lets us release
	// iterators frequently and compact reclaimed space without pinning the whole
	// LevelDB for the duration of the cycle.
	for prefix := 0; prefix < 256; prefix++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		start := []byte{byte(prefix)}
		var limit []byte
		if prefix < 255 {
			limit = []byte{byte(prefix + 1)}
		}

		deletedBefore := stats.Deleted
		if err := s.sweepLiveRange(ctx, start, limit, keepGeneration, options, stats); err != nil {
			return nil, fmt.Errorf("sweep trie range %d: %w", prefix, err)
		}

		if options.Compact && stats.Deleted > deletedBefore {
			if err := s.storage.db.CompactRange(util.Range{Start: start, Limit: limit}); err != nil {
				return nil, fmt.Errorf("compact trie LevelDB range %d: %w", prefix, err)
			}
			if err := pauseGC(ctx, options.Pause); err != nil {
				return nil, err
			}
		}
	}

	stats.Duration = time.Since(started)
	return stats, nil
}

func (s *TrieSweeper) beginLiveGeneration() (uint64, error) {
	s.storage.gcBarrier.Lock()
	defer s.storage.gcBarrier.Unlock()

	if s.storage.gcMarker != s.marker {
		return 0, errors.New("trie GC write tracking is not active")
	}

	s.storage.gcGeneration++
	return s.storage.gcGeneration, nil
}

func (s *TrieSweeper) sweepLiveRange(
	ctx context.Context,
	start []byte,
	limit []byte,
	keepGeneration uint64,
	options TrieSweepOptions,
	stats *TrieSweepStats,
) error {
	cursor := append([]byte(nil), start...)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		iter := s.storage.db.NewIterator(&util.Range{Start: cursor, Limit: limit}, nil)
		candidates := make([][]byte, 0, options.ScanBatch)
		var lastKey []byte
		scannedChunk := 0
		exhausted := true

		for iter.Next() {
			key := append([]byte(nil), iter.Key()...)
			lastKey = key
			scannedChunk++
			stats.Scanned++

			if !isSweepableTrieKey(key) {
				stats.Skipped++
			} else {
				markedGeneration, ok, err := readGCGeneration(s.marker, gcMarkerKey(gcLivePrefix, key))
				if err != nil {
					iter.Release()
					return fmt.Errorf("read trie GC mark: %w", err)
				}
				if ok && markedGeneration >= keepGeneration {
					stats.Retained++
				} else {
					candidates = append(candidates, key)
				}
			}

			if scannedChunk >= options.ScanBatch {
				exhausted = false
				break
			}
		}

		if err := iter.Error(); err != nil {
			iter.Release()
			return fmt.Errorf("iterate trie database: %w", err)
		}
		iter.Release()

		// The iterator is intentionally released before deletion. This prevents
		// deleted SSTables from being pinned by the scan itself. deleteCandidates
		// rechecks every key under the exclusive write barrier, so a concurrent
		// resurrection between scan and delete is retained safely.
		for offset := 0; offset < len(candidates); offset += options.DeleteBatch {
			end := offset + options.DeleteBatch
			if end > len(candidates) {
				end = len(candidates)
			}

			deleted, retained, err := s.deleteCandidates(candidates[offset:end], keepGeneration)
			if err != nil {
				return err
			}
			stats.Deleted += deleted
			stats.Retained += retained

			if err := pauseGC(ctx, options.Pause); err != nil {
				return err
			}
		}

		if scannedChunk == 0 || exhausted {
			return nil
		}

		// Continue strictly after the final key from the previous iterator.
		// Appending 0x00 is the lexicographic successor boundary after the exact
		// key while still allowing keys for which lastKey is a prefix.
		cursor = make([]byte, len(lastKey)+1)
		copy(cursor, lastKey)
		cursor[len(lastKey)] = 0

		if err := pauseGC(ctx, options.Pause); err != nil {
			return err
		}
	}
}
