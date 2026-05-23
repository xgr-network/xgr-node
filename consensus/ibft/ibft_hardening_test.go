package ibft

import (
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/blockchain"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/consensus/ibft/fork"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	xcrypto "github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/helper/progress"
	"github.com/xgr-network/xgr-node/state"
	itrie "github.com/xgr-network/xgr-node/state/immutable-trie"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

type hardeningTxPool struct {
	mu      sync.Mutex
	sealing []bool
	callsCh chan bool
}

func newHardeningTxPool() *hardeningTxPool {
	return &hardeningTxPool{callsCh: make(chan bool, 16)}
}

func (m *hardeningTxPool) Prepare()                                  {}
func (m *hardeningTxPool) Length() uint64                            { return 0 }
func (m *hardeningTxPool) Peek() *types.Transaction                  { return nil }
func (m *hardeningTxPool) Pop(tx *types.Transaction)                 {}
func (m *hardeningTxPool) Drop(tx *types.Transaction)                {}
func (m *hardeningTxPool) Demote(tx *types.Transaction)              {}
func (m *hardeningTxPool) ResetWithHeaders(headers ...*types.Header) {}
func (m *hardeningTxPool) SetSealing(v bool) {
	m.mu.Lock()
	m.sealing = append(m.sealing, v)
	m.mu.Unlock()

	select {
	case m.callsCh <- v:
	default:
	}
}

func (m *hardeningTxPool) seenTrue() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, v := range m.sealing {
		if v {
			return true
		}
	}

	return false
}

type hardeningHooks struct {
	mu                sync.Mutex
	postInsertHeights []uint64
	preCommitHeights  []uint64
}

func (h *hardeningHooks) ShouldWriteTransactions(uint64) bool { return true }
func (h *hardeningHooks) ModifyHeader(*types.Header, types.Address) error {
	return nil
}
func (h *hardeningHooks) VerifyHeader(*types.Header) error { return nil }
func (h *hardeningHooks) VerifyBlock(*types.Block) error   { return nil }
func (h *hardeningHooks) ProcessHeader(*types.Header) error {
	return nil
}
func (h *hardeningHooks) PreCommitState(header *types.Header, _ *state.Transition) error {
	h.mu.Lock()
	h.preCommitHeights = append(h.preCommitHeights, header.Number)
	h.mu.Unlock()

	return nil
}
func (h *hardeningHooks) PostInsertBlock(block *types.Block) error {
	h.mu.Lock()
	h.postInsertHeights = append(h.postInsertHeights, block.Number())
	h.mu.Unlock()

	return nil
}

func (h *hardeningHooks) heights() []uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	cloned := make([]uint64, len(h.postInsertHeights))
	copy(cloned, h.postInsertHeights)

	return cloned
}

func (h *hardeningHooks) preCommitCalls() []uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	cloned := make([]uint64, len(h.preCommitHeights))
	copy(cloned, h.preCommitHeights)

	return cloned
}

type hardeningForkManager struct {
	signer         signer.Signer
	validators     validators.Validators
	validatorsByH  map[uint64]validators.Validators
	getValidators  func(uint64) (validators.Validators, error)
	hooksByHeight  map[uint64]fork.HooksInterface
	getSignerError map[uint64]error
	isPosActiveFn  func(uint64) bool
}

func (m *hardeningForkManager) Initialize() error { return nil }
func (m *hardeningForkManager) Close() error      { return nil }
func (m *hardeningForkManager) GetSigner(height uint64) (signer.Signer, error) {
	if err := m.getSignerError[height]; err != nil {
		return nil, err
	}

	return m.signer, nil
}
func (m *hardeningForkManager) GetValidatorStore(uint64) (fork.ValidatorStore, error) {
	return nil, nil
}
func (m *hardeningForkManager) GetValidators(height uint64) (validators.Validators, error) {
	if m.getValidators != nil {
		return m.getValidators(height)
	}

	if vals, ok := m.validatorsByH[height]; ok {
		return vals, nil
	}

	return m.validators, nil
}
func (m *hardeningForkManager) GetValidatorStakeSnapshot(uint64, validators.Validators) (map[types.Address]*big.Int, error) {
	return nil, nil
}
func (m *hardeningForkManager) GetHooks(height uint64) fork.HooksInterface {
	if hooks, ok := m.hooksByHeight[height]; ok {
		return hooks
	}

	return &hardeningHooks{}
}
func (m *hardeningForkManager) IsPosActive(height uint64) bool {
	if m.isPosActiveFn != nil {
		return m.isPosActiveFn(height)
	}

	return false
}

type hardeningSyncer struct {
	syncFn func(func(*types.FullBlock) bool) error
}

func (s *hardeningSyncer) Start() error                                { return nil }
func (s *hardeningSyncer) Close() error                                { return nil }
func (s *hardeningSyncer) GetSyncProgression() *progress.Progression   { return nil }
func (s *hardeningSyncer) HasSyncPeer() bool                           { return false }
func (s *hardeningSyncer) Sync(hook func(*types.FullBlock) bool) error { return s.syncFn(hook) }

func newHardeningSigner(t *testing.T) signer.Signer {
	t.Helper()

	key, err := xcrypto.GenerateECDSAKey()
	require.NoError(t, err)
	keyManager := signer.NewECDSAKeyManagerFromKey(key)

	return signer.NewSigner(keyManager, keyManager)
}

func TestStartConsensus_UpdateCurrentModulesError_DisablesSealing(t *testing.T) {
	t.Parallel()

	genesis := &types.Header{Number: 0, Difficulty: 1}
	genesis.ComputeHash()
	h1 := &types.Header{Number: 1, ParentHash: genesis.Hash, Difficulty: 1}
	h1.ComputeHash()
	bc := blockchain.NewTestBlockchain(t, []*types.Header{genesis, h1})

	testSigner := newHardeningSigner(t)
	staleValidators := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(testSigner.Address()))

	txpool := newHardeningTxPool()

	backend := &backendIBFT{
		logger:            hclog.NewNullLogger(),
		blockchain:        bc,
		txpool:            txpool,
		forkManager:       &hardeningForkManager{getSignerError: map[uint64]error{2: errors.New("boom")}},
		currentSigner:     testSigner,
		currentValidators: staleValidators,
		blockTime:         5 * time.Millisecond,
		closeCh:           make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		backend.startConsensus()
		close(done)
	}()

	select {
	case sealing := <-txpool.callsCh:
		require.False(t, sealing)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for SetSealing call")
	}

	require.False(t, txpool.seenTrue(), "sealing must not be enabled when updateCurrentModules fails")

	close(backend.closeCh)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for startConsensus shutdown")
	}
}

func TestStartSyncing_PostInsertBlock_UsesImportedBlockHooks(t *testing.T) {
	t.Parallel()

	staleHooks := &hardeningHooks{}
	importedHooks := &hardeningHooks{}
	nextHeightHooks := &hardeningHooks{}

	testSigner := newHardeningSigner(t)
	valSet := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(testSigner.Address()))
	forkManager := &hardeningForkManager{
		signer:     testSigner,
		validators: valSet,
		hooksByHeight: map[uint64]fork.HooksInterface{
			7: importedHooks,
			8: nextHeightHooks,
		},
	}

	txpool := newHardeningTxPool()
	block := &types.Block{Header: &types.Header{Number: 7}}

	backend := &backendIBFT{
		logger:       hclog.NewNullLogger(),
		txpool:       txpool,
		forkManager:  forkManager,
		currentHooks: staleHooks,
		syncer: &hardeningSyncer{syncFn: func(hook func(*types.FullBlock) bool) error {
			hook(&types.FullBlock{Block: block})
			return nil
		}},
	}

	backend.startSyncing()

	require.Empty(t, staleHooks.heights(), "stale currentHooks must not be used for PostInsertBlock")
	require.Equal(t, []uint64{7}, importedHooks.heights(), "PostInsertBlock must run on hooks resolved for imported height")
}

func TestPreCommitState_DirectPoSBlock1SkipsGenesisParentUptimeButRunsHooks(t *testing.T) {

	genesis := &types.Header{Number: 0, Difficulty: 1}
	genesis.ComputeHash()
	block1 := &types.Header{Number: 1, ParentHash: genesis.Hash, Difficulty: 1}
	block1.ComputeHash()
	bc := blockchain.NewTestBlockchain(t, []*types.Header{genesis, block1})

	hooks := &hardeningHooks{}
	getValidatorsCalls := make([]uint64, 0, 1)
	fm := &hardeningForkManager{
		getValidators: func(height uint64) (validators.Validators, error) {
			getValidatorsCalls = append(getValidatorsCalls, height)
			return nil, errors.New("height must be greater than 0")
		},
		hooksByHeight: map[uint64]fork.HooksInterface{
			1: hooks,
		},
		isPosActiveFn: func(uint64) bool { return true },
	}

	backend := &backendIBFT{
		blockchain:  bc,
		forkManager: fm,
		epochSize:   10,
	}

	err := backend.PreCommitState(&types.Block{Header: block1}, nil)
	require.NoError(t, err)
	require.Empty(t, getValidatorsCalls)
	require.Equal(t, []uint64{1}, hooks.preCommitCalls())
}

func TestStartConsensus_ValidatorToNonValidator_DoesNotReuseClosedSequenceChannel(t *testing.T) {
	t.Parallel()

	genesis := &types.Header{Number: 0, Difficulty: 1}
	genesis.ComputeHash()
	h1 := &types.Header{Number: 1, ParentHash: genesis.Hash, Difficulty: 1}
	h1.ComputeHash()
	bc := blockchain.NewTestBlockchain(t, []*types.Header{genesis, h1})

	testSigner := newHardeningSigner(t)
	otherSigner := newHardeningSigner(t)

	validatorSetWithSelf := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(testSigner.Address()))
	validatorSetWithoutSelf := validators.NewECDSAValidatorSet(validators.NewECDSAValidator(otherSigner.Address()))

	txpool := newHardeningTxPool()
	st := itrie.NewState(itrie.NewMemoryStorage())
	executor := state.NewExecutor(&chain.Params{Forks: chain.AllForksEnabled}, st, hclog.NewNullLogger())

	getValidatorsCalls := 0
	backend := &backendIBFT{
		logger:     hclog.NewNullLogger(),
		blockchain: bc,
		executor:   executor,
		txpool:     txpool,
		forkManager: &hardeningForkManager{
			signer: testSigner,
			getValidators: func(uint64) (validators.Validators, error) {
				if getValidatorsCalls == 0 {
					getValidatorsCalls++
					return validatorSetWithSelf, nil
				}

				return validatorSetWithoutSelf, nil
			},
		},
		blockTime: 5 * time.Millisecond,
		closeCh:   make(chan struct{}),
	}
	backend.consensus = newIBFT(hclog.NewNullLogger(), backend, backend)

	done := make(chan struct{})
	go func() {
		backend.startConsensus()
		close(done)
	}()

	select {
	case sealing := <-txpool.callsCh:
		require.True(t, sealing)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for validator SetSealing(true) call")
	}

	backend.consensus.stopSequence()

	select {
	case sealing := <-txpool.callsCh:
		require.False(t, sealing)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for non-validator SetSealing(false) call")
	}

	select {
	case sealing := <-txpool.callsCh:
		t.Fatalf("unexpected extra SetSealing(%v) call, non-validator path should wait instead of spinning", sealing)
	case <-time.After(100 * time.Millisecond):
	}

	close(backend.closeCh)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for startConsensus shutdown")
	}
}
