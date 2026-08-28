package server

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	itrie "github.com/xgr-network/xgr-node/state/immutable-trie"
	"github.com/xgr-network/xgr-node/types"
)

type trieSweeperRuntime struct {
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	sweeper         *itrie.TrieSweeper
	trackingHeadNum uint64
}

var trieSweeperRegistry = struct {
	sync.Mutex
	runtimes map[*Server]*trieSweeperRuntime
}{
	runtimes: make(map[*Server]*trieSweeperRuntime),
}

// StartTrieSweeper starts the optional online immutable-trie garbage collector.
// The server flag is disabled by default. Write tracking is enabled before the
// synchronization head is captured. Before the first GC cycle, the worker waits
// for one canonical head advance so that any state transition which was already
// in flight before tracking became active is either canonical (and is therefore
// selected as a retained root) or obsolete. One sweep then starts, and following
// cycles start after TrieSweeperInterval has elapsed since the previous cycle
// completed.
func (s *Server) StartTrieSweeper() error {
	if s == nil || s.config == nil || !s.config.TrieSweeperEnabled {
		return nil
	}
	if s.config.TrieSweeperRetainBlocks == 0 {
		return errors.New("trie sweeper retain blocks must be greater than zero")
	}
	if s.config.TrieSweeperInterval <= 0 {
		return errors.New("trie sweeper interval must be greater than zero")
	}
	if s.config.DataDir == "" {
		return errors.New("trie sweeper requires a persistent data directory")
	}
	if s.blockchain == nil || s.blockchain.Header() == nil {
		return errors.New("trie sweeper requires an initialized canonical head")
	}

	storage, ok := s.stateStorage.(*itrie.KVStorage)
	if !ok {
		return fmt.Errorf("trie sweeper requires LevelDB trie storage, got %T", s.stateStorage)
	}

	workDir := filepath.Join(s.config.DataDir, "trie-gc")
	sweeper, err := itrie.NewTrieSweeper(storage, workDir)
	if err != nil {
		return fmt.Errorf("initialize trie sweeper: %w", err)
	}

	// Capture the head only after write tracking is active. A proposal whose trie
	// commit happened before tracking activation can only target the next
	// canonical height in IBFT. Waiting for one head advance therefore closes the
	// startup race without blocking normal trie writes.
	trackingHeadNum := s.blockchain.Header().Number

	ctx, cancel := context.WithCancel(context.Background())
	runtime := &trieSweeperRuntime{
		cancel:          cancel,
		sweeper:         sweeper,
		trackingHeadNum: trackingHeadNum,
	}

	trieSweeperRegistry.Lock()
	if _, exists := trieSweeperRegistry.runtimes[s]; exists {
		trieSweeperRegistry.Unlock()
		cancel()
		_ = sweeper.Close()
		return errors.New("trie sweeper already started")
	}
	trieSweeperRegistry.runtimes[s] = runtime
	trieSweeperRegistry.Unlock()

	runtime.wg.Add(1)
	go s.runTrieSweeper(ctx, runtime)

	s.logger.Info(
		"Trie sweeper enabled",
		"retainBlocks", s.config.TrieSweeperRetainBlocks,
		"interval", s.config.TrieSweeperInterval,
		"trackingFromBlock", trackingHeadNum,
		"workDir", workDir,
	)

	return nil
}

// StopTrieSweeper stops the background collector before the trie LevelDB is
// closed by Server.Close.
func (s *Server) StopTrieSweeper() {
	if s == nil {
		return
	}

	trieSweeperRegistry.Lock()
	runtime := trieSweeperRegistry.runtimes[s]
	delete(trieSweeperRegistry.runtimes, s)
	trieSweeperRegistry.Unlock()

	if runtime == nil {
		return
	}

	runtime.cancel()
	runtime.wg.Wait()
	if err := runtime.sweeper.Close(); err != nil {
		s.logger.Error("failed to close trie sweeper", "err", err)
	}
}

func (s *Server) runTrieSweeper(ctx context.Context, runtime *trieSweeperRuntime) {
	defer runtime.wg.Done()

	if err := s.waitForTrackedTrieHead(ctx, runtime.trackingHeadNum); err != nil {
		return
	}

	for {
		if err := ctx.Err(); err != nil {
			return
		}

		if err := s.runTrieSweepCycle(ctx, runtime.sweeper); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			s.logger.Error("Trie sweeper cycle failed", "err", err)
		}

		timer := time.NewTimer(s.config.TrieSweeperInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func (s *Server) waitForTrackedTrieHead(ctx context.Context, trackingHeadNum uint64) error {
	const pollInterval = 250 * time.Millisecond

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		head := s.blockchain.Header()
		if head != nil && head.Number > trackingHeadNum {
			s.logger.Info(
				"Trie sweeper write tracking synchronized",
				"trackingFromBlock", trackingHeadNum,
				"currentBlock", head.Number,
			)
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Server) runTrieSweepCycle(ctx context.Context, sweeper *itrie.TrieSweeper) error {
	roots, fromBlock, toBlock, err := s.trieSweeperRoots()
	if err != nil {
		return err
	}

	s.logger.Info(
		"Trie sweeper cycle started",
		"fromBlock", fromBlock,
		"toBlock", toBlock,
		"roots", len(roots),
	)

	stats, err := sweeper.SweepLive(ctx, roots, itrie.TrieSweepOptions{
		Compact: true,
	})
	if err != nil {
		return err
	}

	// Validate a freshly captured canonical head after deletion and compaction.
	// Concurrent state writes are generation-protected, so this root must remain
	// fully traversable. The strict checker treats a missing hash-linked node as
	// corruption rather than silently interpreting it as an empty subtree.
	head := s.blockchain.Header()
	if head == nil {
		return errors.New("canonical head disappeared after trie sweep")
	}
	checkedRoot, err := itrie.HashCheckerStrict(head.StateRoot.Bytes(), s.stateStorage)
	if err != nil {
		return fmt.Errorf("post-sweep trie integrity check at block %d: %w", head.Number, err)
	}
	if checkedRoot != head.StateRoot {
		return fmt.Errorf(
			"post-sweep trie integrity mismatch at block %d: have %s want %s",
			head.Number,
			checkedRoot,
			head.StateRoot,
		)
	}

	s.logger.Info(
		"Trie sweeper cycle completed",
		"generation", stats.Generation,
		"fromBlock", fromBlock,
		"toBlock", toBlock,
		"roots", stats.Roots,
		"marked", stats.Marked,
		"scanned", stats.Scanned,
		"deleted", stats.Deleted,
		"retained", stats.Retained,
		"skipped", stats.Skipped,
		"duration", stats.Duration,
		"verifiedHead", head.Number,
		"verifiedStateRoot", checkedRoot,
	)

	return nil
}

func (s *Server) trieSweeperRoots() ([]types.Hash, uint64, uint64, error) {
	if s.blockchain == nil {
		return nil, 0, 0, errors.New("blockchain is not initialized")
	}

	head := s.blockchain.Header()
	if head == nil {
		return nil, 0, 0, errors.New("canonical head is not initialized")
	}

	toBlock := head.Number
	fromBlock := uint64(0)
	if toBlock+1 > s.config.TrieSweeperRetainBlocks {
		fromBlock = toBlock + 1 - s.config.TrieSweeperRetainBlocks
	}

	capacity := s.config.TrieSweeperRetainBlocks
	if toBlock+1 < capacity {
		capacity = toBlock + 1
	}
	roots := make([]types.Hash, 0, int(capacity))
	seen := make(map[types.Hash]struct{})

	for number := fromBlock; ; number++ {
		header, ok := s.blockchain.GetHeaderByNumber(number)
		if !ok {
			return nil, 0, 0, fmt.Errorf("read canonical header %d for trie sweep", number)
		}
		if _, exists := seen[header.StateRoot]; !exists {
			seen[header.StateRoot] = struct{}{}
			roots = append(roots, header.StateRoot)
		}

		if number == toBlock {
			break
		}
	}

	if len(roots) == 0 {
		return nil, 0, 0, errors.New("no canonical state roots selected for trie sweep")
	}

	return roots, fromBlock, toBlock, nil
}
