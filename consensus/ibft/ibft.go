package ibft

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"sync"
	"time"

	ibftProtoMessages "github.com/0xPolygon/go-ibft/messages/proto"
	"github.com/armon/go-metrics"
	"github.com/hashicorp/go-hclog"
	"github.com/xgr-network/xgr-node/blockchain"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/consensus"
	"github.com/xgr-network/xgr-node/consensus/ibft/fork"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	"github.com/xgr-network/xgr-node/consensus/ibft/proto"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	xcrypto "github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/helper/progress"
	"github.com/xgr-network/xgr-node/network"
	"github.com/xgr-network/xgr-node/secrets"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/syncer"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
	"google.golang.org/grpc"
)

const (
	DefaultEpochSize = 100000
	IbftKeyName      = "validator.key"
	KeyEpochSize     = "epochSize"

	ibftProto = "/ibft/0.2"

	// consensusMetrics is a prefix used for consensus-related metrics
	consensusMetrics = "consensus"
)

var (
	ErrInvalidHookParam           = errors.New("invalid IBFT hook param passed in")
	ErrProposerSealByNonValidator = errors.New("proposer seal by non-validator")
	ErrInvalidMixHash             = errors.New("invalid mixhash")
	ErrInvalidSha3Uncles          = errors.New("invalid sha3 uncles")
	ErrWrongDifficulty            = errors.New("wrong difficulty")
)

type effectivePowerSnapshot struct {
	hash             types.Hash
	powers           map[string]string
	totalVotingPower string
	quorumThreshold  string
}

type txPoolInterface interface {
	Prepare()
	Length() uint64
	Peek() *types.Transaction
	Pop(tx *types.Transaction)
	Drop(tx *types.Transaction)
	Demote(tx *types.Transaction)
	ResetWithHeaders(headers ...*types.Header)
	SetSealing(bool)
}

type forkManagerInterface interface {
	Initialize() error
	Close() error
	GetSigner(uint64) (signer.Signer, error)
	GetValidatorStore(uint64) (fork.ValidatorStore, error)
	GetValidators(uint64) (validators.Validators, error)
	GetValidatorStakeSnapshot(uint64, validators.Validators) (map[types.Address]*big.Int, error)
	GetHooks(uint64) fork.HooksInterface
	IsPosActive(uint64) bool
}

// backendIBFT represents the IBFT consensus mechanism object
type backendIBFT struct {
	consensus *IBFTConsensus

	// Static References
	logger         hclog.Logger           // Reference to the logging
	blockchain     *blockchain.Blockchain // Reference to the blockchain layer
	network        *network.Server        // Reference to the networking layer
	executor       *state.Executor        // Reference to the state executor
	txpool         txPoolInterface        // Reference to the transaction pool
	syncer         syncer.Syncer          // Reference to the sync protocol
	secretsManager secrets.SecretsManager // Reference to the secret manager
	Grpc           *grpc.Server           // Reference to the gRPC manager
	operator       *operator              // Reference to the gRPC service of IBFT
	transport      transport              // Reference to the transport protocol

	// Dynamic References
	forkManager       forkManagerInterface  // Manager to hold IBFT Forks
	currentSigner     signer.Signer         // Signer at current sequence
	currentValidators validators.Validators // signer at current sequence
	currentHooks      fork.HooksInterface   // Hooks at current sequence
	currentModulesMu  sync.RWMutex

	// Configurations
	config             *consensus.Config // Consensus configuration
	epochSize          uint64
	quorumSizeBlockNum uint64
	blockTime          time.Duration // Minimum block generation time in seconds
	uptimeCfg          pos.UptimeConfig

	// Channels
	closeCh chan struct{} // Channel for closing
}

// Factory implements the base consensus Factory method
func Factory(params *consensus.Params) (consensus.Consensus, error) {
	if err := alignFeePoolSplitWithPoSFork(params.Config.Params, params.Config.Config); err != nil {
		return nil, err
	}

	// defaults for user set fields in genesis
	var (
		quorumSizeBlockNum = uint64(0)
	)

	epochSize, uptimeCfg, err := resolveEpochSizeAndUptimeConfig(params.Config.Config)
	if err != nil {
		return nil, err
	}
	uptimeCfg.ChainID = params.Config.Params.ChainID

	if rawBlockNum, ok := params.Config.Config["quorumSizeBlockNum"]; ok {
		// Block number specified for quorum size switch
		readBlockNum, ok := rawBlockNum.(float64)
		if !ok {
			return nil, errors.New("invalid type assertion")
		}

		quorumSizeBlockNum = uint64(readBlockNum)
	}

	logger := params.Logger.Named("ibft")

	forkManager, err := fork.NewForkManager(
		logger,
		params.Blockchain,
		params.Executor,
		params.SecretsManager,
		params.Config.Path,
		epochSize,
		params.Config.Config,
	)

	if err != nil {
		return nil, err
	}

	p := &backendIBFT{
		// References
		logger:     logger,
		blockchain: params.Blockchain,
		network:    params.Network,
		executor:   params.Executor,
		txpool:     params.TxPool,
		syncer: syncer.NewSyncer(
			params.Logger,
			params.Network,
			params.Blockchain,
			time.Duration(params.BlockTime)*3*time.Second,
		),
		secretsManager: params.SecretsManager,
		Grpc:           params.Grpc,
		forkManager:    forkManager,

		// Configurations
		config:             params.Config,
		epochSize:          epochSize,
		quorumSizeBlockNum: quorumSizeBlockNum,
		blockTime:          time.Duration(params.BlockTime) * time.Second,
		uptimeCfg:          uptimeCfg,

		// Channels
		closeCh: make(chan struct{}),
	}
	// Istanbul requires a different header hash function
	p.SetHeaderHash()

	return p, nil
}

// alignFeePoolSplitWithPoSFork ensures FeePoolSplit activates exactly when the first PoS IBFT fork starts.
// This removes double-gating at genesis level (PoS-from + feePoolSplit block).
func alignFeePoolSplitWithPoSFork(chainParams *chain.Params, ibftConfig map[string]interface{}) error {
	if chainParams == nil {
		return nil
	}
	if ibftConfig == nil {
		return nil
	}

	ibftForks, err := fork.GetIBFTForks(ibftConfig)
	if err != nil {
		return err
	}

	firstPoSFrom := uint64(0)
	foundPoS := false

	for _, ibftFork := range ibftForks {
		if ibftFork.Type != fork.PoS {
			continue
		}

		if !foundPoS || ibftFork.From.Value < firstPoSFrom {
			firstPoSFrom = ibftFork.From.Value
			foundPoS = true
		}
	}

	if !foundPoS {
		return nil
	}

	if chainParams.Forks == nil {
		forks := make(chain.Forks)
		chainParams.Forks = &forks
	}

	if feePoolSplitFork, exists := (*chainParams.Forks)[chain.FeePoolSplit]; exists {
		if feePoolSplitFork.Block != firstPoSFrom {
			return fmt.Errorf("feePoolSplit fork must match first PoS fork: feePoolSplit=%d firstPoSFrom=%d", feePoolSplitFork.Block, firstPoSFrom)
		}

		return nil
	}

	chainParams.Forks.SetFork(chain.FeePoolSplit, chain.NewFork(firstPoSFrom))

	return nil
}

func (i *backendIBFT) Initialize() error {
	// register the grpc operator
	if i.Grpc != nil {
		i.operator = &operator{ibft: i}
		proto.RegisterIbftOperatorServer(i.Grpc, i.operator)
	}

	// start the transport protocol
	if err := i.setupTransport(); err != nil {
		return err
	}

	// initialize fork manager
	if err := i.forkManager.Initialize(); err != nil {
		return err
	}

	if err := i.updateCurrentModules(i.blockchain.Header().Number + 1); err != nil {
		return err
	}

	i.logger.Info("validator key", "addr", i.getCurrentSigner().Address().String())

	i.consensus = newIBFT(
		i.logger.Named("consensus"),
		i,
		i,
	)

	// Ensure consensus takes into account user configured block production time
	i.consensus.ExtendRoundTimeout(i.blockTime)

	return nil
}

// sync runs the syncer in the background to receive blocks from advanced peers
func (i *backendIBFT) startSyncing() {
	callInsertBlockHook := func(fullBlock *types.FullBlock) bool {
		hooks := i.forkManager.GetHooks(fullBlock.Block.Number())

		if err := hooks.PostInsertBlock(fullBlock.Block); err != nil {
			i.logger.Error("failed to call PostInsertBlock", "height", fullBlock.Block.Header.Number, "error", err)
		}

		if err := i.updateCurrentModules(fullBlock.Block.Number() + 1); err != nil {
			i.logger.Error("failed to update sub modules", "height", fullBlock.Block.Number()+1, "err", err)
			i.txpool.SetSealing(false)
		}

		i.txpool.ResetWithHeaders(fullBlock.Block.Header)

		return false
	}

	if err := i.syncer.Sync(
		callInsertBlockHook,
	); err != nil {
		i.logger.Error("watch sync failed", "err", err)
	}
}

// Start starts the IBFT consensus
func (i *backendIBFT) Start() error {
	// Start the syncer
	if err := i.syncer.Start(); err != nil {
		return err
	}

	// Start syncing blocks from other peers
	go i.startSyncing()

	// Start the actual consensus protocol
	go i.startConsensus()

	return nil
}

// GetSyncProgression gets the latest sync progression, if any
func (i *backendIBFT) GetSyncProgression() *progress.Progression {
	return i.syncer.GetSyncProgression()
}

func (i *backendIBFT) startConsensus() {
	var (
		newBlockSub   = i.blockchain.SubscribeEvents()
		syncerBlockCh = make(chan struct{})
	)

	// Receive a notification every time syncer manages
	// to insert a valid block. Used for cancelling active consensus
	// rounds for a specific height
	go func() {
		eventCh := newBlockSub.GetEventCh()

		for {
			if ev := <-eventCh; ev.Source == "syncer" {
				if ev.NewChain[0].Number < i.blockchain.Header().Number {
					// The blockchain notification system can eventually deliver
					// stale block notifications. These should be ignored
					continue
				}

				syncerBlockCh <- struct{}{}
			}
		}
	}()

	defer i.blockchain.UnsubscribeEvents(newBlockSub)

	var (
		sequenceCh  = make(<-chan struct{})
		isValidator bool
	)

	for {
		var (
			latest  = i.blockchain.Header().Number
			pending = latest + 1
		)

		if err := i.updateCurrentModules(pending); err != nil {
			i.logger.Error(
				"failed to update submodules",
				"height", pending,
				"err", err,
			)

			i.txpool.SetSealing(false)

			retryTimer := time.NewTimer(i.blockTime)

			select {
			case <-syncerBlockCh:
				retryTimer.Stop()
				goto nextHeight
			case <-retryTimer.C:
				goto nextHeight
			case <-i.closeCh:
				retryTimer.Stop()

				return
			}
		}

		// Update the No.of validator metric
		metrics.SetGauge([]string{consensusMetrics, "validators"}, float32(i.getCurrentValidators().Len()))

		isValidator = i.isActiveValidator()

		i.txpool.SetSealing(isValidator)
		sequenceCh = make(<-chan struct{})

		if isValidator {
			sequenceCh = i.consensus.runSequence(pending)
		}

		for {
			select {
			case <-syncerBlockCh:
				if isValidator {
					i.consensus.stopSequence()
					i.logger.Info("canceled sequence", "sequence", pending)
				}
				goto nextHeight
			case <-sequenceCh:
				goto nextHeight
			case <-i.closeCh:
				if isValidator {
					i.consensus.stopSequence()
				}

				return
			}
		}
	nextHeight:
	}
}

// isActiveValidator returns whether my signer belongs to current validators
func (i *backendIBFT) isActiveValidator() bool {
	currentValidators := i.getCurrentValidators()
	currentSigner := i.getCurrentSigner()

	return currentValidators.Includes(currentSigner.Address())
}

// RoundStarts notifies the backend that IBFT is about to start a new round.
func (i *backendIBFT) RoundStarts(view *ibftProtoMessages.View) error {
	_ = i
	_ = view
	return nil
}

// SequenceCancelled notifies the backend that the active sequence was cancelled.
func (i *backendIBFT) SequenceCancelled(*ibftProtoMessages.View) error {
	return nil
}

// updateMetrics will update various metrics based on the given block
// currently we capture No.of Txs and block interval metrics using this function
func (i *backendIBFT) updateMetrics(block *types.Block) {
	// get previous header
	prvHeader, _ := i.blockchain.GetHeaderByNumber(block.Number() - 1)
	parentTime := time.Unix(int64(prvHeader.Timestamp), 0)
	headerTime := time.Unix(int64(block.Header.Timestamp), 0)

	// Update the block interval metric
	if block.Number() > 1 {
		metrics.SetGauge([]string{consensusMetrics, "block_interval"}, float32(headerTime.Sub(parentTime).Seconds()))
	}

	// Update the Number of transactions in the block metric
	metrics.SetGauge([]string{consensusMetrics, "num_txs"}, float32(len(block.Body().Transactions)))

	// Update the base fee metric
	metrics.SetGauge([]string{consensusMetrics, "base_fee"}, float32(block.Header.BaseFee))
}

// verifyHeaderImpl verifies fields including Extra
// for the past or being proposed header
func (i *backendIBFT) verifyHeaderImpl(
	parent, header *types.Header,
	headerSigner signer.Signer,
	validators validators.Validators,
	hooks fork.HooksInterface,
	shouldVerifyParentCommittedSeals bool,
) error {
	if header.MixHash != signer.IstanbulDigest {
		return ErrInvalidMixHash
	}

	if header.Sha3Uncles != types.EmptyUncleHash {
		return ErrInvalidSha3Uncles
	}

	// difficulty has to match number
	if header.Difficulty != header.Number {
		return ErrWrongDifficulty
	}

	// ensure the extra data is correctly formatted
	if _, err := headerSigner.GetIBFTExtra(header); err != nil {
		return err
	}
	// verify the ProposerSeal
	if err := verifyProposerSeal(
		header,
		headerSigner,
		validators,
	); err != nil {
		return err
	}

	// verify the ParentCommittedSeals
	if err := i.verifyParentCommittedSeals(
		parent, header,
		shouldVerifyParentCommittedSeals,
	); err != nil {
		return err
	}
	// Additional header verification
	if err := hooks.VerifyHeader(header); err != nil {
		return err
	}

	return nil
}

// VerifyHeader wrapper for verifying headers
func (i *backendIBFT) VerifyHeader(header *types.Header) error {
	parent, ok := i.blockchain.GetHeaderByHash(header.ParentHash)
	if !ok {
		return fmt.Errorf(
			"unable to get parent header %s for block number %d",
			header.ParentHash,
			header.Number,
		)
	}
	if parent.Number+1 != header.Number {
		return fmt.Errorf(
			"invalid parent header number %d for block number %d",
			parent.Number,
			header.Number,
		)
	}

	headerSigner, validators, hooks, err := getModulesFromForkManager(
		i.forkManager,
		header.Number,
	)
	if err != nil {
		return err
	}

	// verify all the header fields
	if err := i.verifyHeaderImpl(
		parent,
		header,
		headerSigner,
		validators,
		hooks,
		false,
	); err != nil {
		return err
	}

	extra, err := headerSigner.GetIBFTExtra(header)
	if err != nil {
		return err
	}

	hashForCommittedSeal, err := i.calculateProposalHash(
		header,
		extra.RoundNumber,
	)
	if err != nil {
		return err
	}

	// verify the Committed Seals
	// CommittedSeals exists only in the finalized header
	committedSealQuorum := i.quorumSize(header.Number)(validators)
	if i.forkManager.IsPosActive(header.Number) {
		// In PoS weighted mode, signer-level verification is used only for
		// structural/cryptographic validation. Weighted voting power check below
		// is the sole quorum acceptance rule.
		committedSealQuorum = 0
	}

	if err := headerSigner.VerifyCommittedSeals(
		hashForCommittedSeal,
		extra.CommittedSeals,
		validators,
		committedSealQuorum,
	); err != nil {
		return err
	}

	if err := i.verifyWeightedCommittedPower(header.Number, hashForCommittedSeal, extra.CommittedSeals, validators, header, parent, extra.RoundNumber); err != nil {
		return err
	}

	return nil
}

// quorumSize returns a callback that when executed on a Validators computes
// number of votes required to reach quorum based on the size of the set.
// The blockNumber argument indicates which formula was used to calculate the result (see PRs #513, #549)
func (i *backendIBFT) quorumSize(blockNumber uint64) QuorumImplementation {
	if blockNumber < i.quorumSizeBlockNum {
		return LegacyQuorumSize
	}

	return OptimalQuorumSize
}

// ProcessHeaders updates the snapshot based on previously verified headers
func (i *backendIBFT) ProcessHeaders(headers []*types.Header) error {
	for _, header := range headers {
		hooks := i.forkManager.GetHooks(header.Number)

		if err := hooks.ProcessHeader(header); err != nil {
			return err
		}
	}

	return nil
}

// GetBlockCreator retrieves the block signer from the extra data field
func (i *backendIBFT) GetBlockCreator(header *types.Header) (types.Address, error) {
	signer, err := i.forkManager.GetSigner(header.Number)
	if err != nil {
		return types.ZeroAddress, err
	}

	return signer.EcrecoverFromHeader(header)
}

// PreCommitState a hook to be called before finalizing state transition on inserting block
func (i *backendIBFT) PreCommitState(block *types.Block, txn *state.Transition) error {
	if block == nil || block.Header == nil {
		return fmt.Errorf("missing block header in PreCommitState")
	}
	// Deterministic uptime accounting MUST use the parent header (already sealed/final).
	// Counting on the current header breaks determinism during buildBlock because proposer seal/hash
	// may not be present yet.
	parentHeader, ok := i.blockchain.GetHeaderByHash(block.Header.ParentHash)
	if !ok {
		return fmt.Errorf("parent header %s not found for block %d", block.Header.ParentHash, block.Number())
	}

	parentPosActive := parentHeader.Number > 0 && i.forkManager.IsPosActive(parentHeader.Number)
	requiresValidators := parentPosActive
	var parentValidators validators.Validators
	if requiresValidators {
		vLoaded, vErr := i.forkManager.GetValidators(parentHeader.Number)
		if vErr != nil {
			if parentPosActive {
				return fmt.Errorf("failed to get validators for uptime at height %d: %w", parentHeader.Number, vErr)
			}

			return fmt.Errorf("failed to get validators at height %d: %w", parentHeader.Number, vErr)
		}
		parentValidators = vLoaded
	}

	if parentPosActive {
		parentSigner, err := i.forkManager.GetSigner(parentHeader.Number)
		if err != nil {
			return fmt.Errorf("failed to get signer for uptime at height %d: %w", parentHeader.Number, err)
		}
		if parentSigner == nil {
			return fmt.Errorf("missing signer for uptime at height %d", parentHeader.Number)
		}
		if err := pos.RecordBlockUptime(parentHeader, i.epochSize, parentValidators, parentSigner, i.uptimeCfg, txn); err != nil {
			return fmt.Errorf("failed to record parent uptime for block %d: %w", block.Number(), err)
		}
	}
	hooks := i.forkManager.GetHooks(block.Number())
	if err := hooks.PreCommitState(block.Header, txn); err != nil {
		return err
	}
	return nil
}

// GetEpoch returns the current epoch
func (i *backendIBFT) GetEpoch(number uint64) uint64 {
	if number%i.epochSize == 0 {
		return number / i.epochSize
	}

	return number/i.epochSize + 1
}

// IsLastOfEpoch checks if the block number is the last of the epoch
func (i *backendIBFT) IsLastOfEpoch(number uint64) bool {
	return number > 0 && number%i.epochSize == 0
}

// Close closes the IBFT consensus mechanism, and does write back to disk
func (i *backendIBFT) Close() error {
	close(i.closeCh)

	if i.syncer != nil {
		if err := i.syncer.Close(); err != nil {
			return err
		}
	}

	if i.forkManager != nil {
		if err := i.forkManager.Close(); err != nil {
			return err
		}
	}

	return nil
}

// SetHeaderHash updates hash calculation function for IBFT
func (i *backendIBFT) SetHeaderHash() {
	types.SetHeaderHash(func(h *types.Header) types.Hash {
		signer, err := i.forkManager.GetSigner(h.Number)
		if err != nil {
			return types.ZeroHash
		}

		hash, err := signer.CalculateHeaderHash(h)
		if err != nil {
			return types.ZeroHash
		}

		return hash
	})
}

// GetBridgeProvider returns an instance of BridgeDataProvider
func (i *backendIBFT) GetBridgeProvider() consensus.BridgeDataProvider {
	return nil
}

// FilterExtra is the implementation of Consensus interface
func (i *backendIBFT) FilterExtra(extra []byte) ([]byte, error) {
	return extra, nil
}

// updateCurrentModules updates Signer, Hooks, and Validators
// that are used at specified height
// by fetching from ForkManager
func (i *backendIBFT) updateCurrentModules(height uint64) error {
	signer, validators, hooks, err := getModulesFromForkManager(i.forkManager, height)
	if err != nil {
		return err
	}

	i.currentModulesMu.Lock()
	lastSigner := i.currentSigner
	i.currentSigner = signer
	i.currentValidators = validators
	i.currentHooks = hooks
	i.currentModulesMu.Unlock()

	i.logFork(lastSigner, signer)

	return nil
}

func (i *backendIBFT) getCurrentHooks() fork.HooksInterface {
	i.currentModulesMu.RLock()
	defer i.currentModulesMu.RUnlock()

	return i.currentHooks
}

func (i *backendIBFT) getCurrentSigner() signer.Signer {
	i.currentModulesMu.RLock()
	defer i.currentModulesMu.RUnlock()

	return i.currentSigner
}

func (i *backendIBFT) getCurrentValidators() validators.Validators {
	i.currentModulesMu.RLock()
	defer i.currentModulesMu.RUnlock()

	return i.currentValidators
}

// logFork logs validation type switch
func (i *backendIBFT) logFork(
	lastSigner, signer signer.Signer,
) {
	if lastSigner != nil && signer != nil && lastSigner.Type() != signer.Type() {
		i.logger.Info("IBFT validation type switched", "old", lastSigner.Type(), "new", signer.Type())
	}
}

func (i *backendIBFT) verifyParentCommittedSeals(
	parent, header *types.Header,
	shouldVerifyParentCommittedSeals bool,
) error {
	if parent.IsGenesis() {
		return nil
	}

	parentSigner, parentValidators, _, err := getModulesFromForkManager(
		i.forkManager,
		parent.Number,
	)
	if err != nil {
		return err
	}

	parentHeader, ok := i.blockchain.GetHeaderByHash(parent.Hash)
	if !ok {
		return fmt.Errorf("header %s not found", parent.Hash)
	}

	parentExtra, err := parentSigner.GetIBFTExtra(parentHeader)
	if err != nil {
		return err
	}

	parentHash, err := i.calculateProposalHash(
		parentHeader,
		parentExtra.RoundNumber,
	)
	if err != nil {
		return err
	}

	// if shouldVerifyParentCommittedSeals is false, skip the verification
	// when header doesn't have Parent Committed Seals (Backward Compatibility)
	parentCommittedSealQuorum := i.quorumSize(parent.Number)(parentValidators)
	if i.forkManager.IsPosActive(parent.Number) {
		// In PoS weighted mode, signer-level verification remains a
		// structural/cryptographic guard only. Weighted parent committed power
		// verification below is the sole quorum acceptance rule.
		parentCommittedSealQuorum = 0
	}

	if err := parentSigner.VerifyParentCommittedSeals(
		parentHash,
		header,
		parentValidators,
		parentCommittedSealQuorum,
		shouldVerifyParentCommittedSeals,
	); err != nil {
		return err
	}

	if !i.forkManager.IsPosActive(parent.Number) {
		return nil
	}

	parentCommittedSealsGetter, ok := parentSigner.(interface {
		GetParentCommittedSeals(*types.Header) (signer.Seals, error)
	})
	if !ok {
		return errors.New("parent signer does not support extracting parent committed seals")
	}

	parentCommittedSeals, err := parentCommittedSealsGetter.GetParentCommittedSeals(header)
	if err != nil {
		return err
	}
	if parentCommittedSeals == nil || parentCommittedSeals.Num() == 0 {
		return fmt.Errorf("missing parent committed seals in PoS weighted mode at height %d", parent.Number)
	}

	parentParentHeader, ok := i.blockchain.GetHeaderByHash(parentHeader.ParentHash)
	if !ok {
		return fmt.Errorf("header %s not found", parentHeader.ParentHash)
	}

	// ParentCommittedSeals stored in child header are signatures for the parent proposal.
	// Verify weighted quorum in the same consensus context:
	// state at parent height + evidence transition from grandparent -> parent.
	return i.verifyWeightedCommittedPower(
		parent.Number,
		parentHash,
		parentCommittedSeals,
		parentValidators,
		parentHeader,
		parentParentHeader,
		parentExtra.RoundNumber,
	)
}

func resolveEpochSizeAndUptimeConfig(ibftConfig map[string]interface{}) (uint64, pos.UptimeConfig, error) {
	uptimeCfg := pos.ParseUptimeConfig(ibftConfig)
	isPoS := false
	if forks, err := fork.GetIBFTForks(ibftConfig); err == nil {
		for _, f := range forks {
			if f.Type == fork.PoS {
				isPoS = true
				break
			}
		}
	}

	if isPoS {
		if _, ok := ibftConfig[KeyEpochSize]; ok {
			return 0, pos.UptimeConfig{}, errors.New("epochSize must not be set; macro epoch size is derived from microEpochSize * macroEpochMicroFactor")
		}
		if uptimeCfg.MicroEpochSize == 0 {
			return 0, pos.UptimeConfig{}, errors.New("microEpochSize is required for PoS")
		}
		if uptimeCfg.MacroEpochMicroFactor == 0 {
			return 0, pos.UptimeConfig{}, errors.New("macroEpochMicroFactor is required for PoS")
		}
		if uptimeCfg.MicroEpochSize > math.MaxUint64/uptimeCfg.MacroEpochMicroFactor {
			return 0, pos.UptimeConfig{}, errors.New("invalid PoS uptime config: microEpochSize * macroEpochMicroFactor overflows uint64")
		}
		epochSize := uptimeCfg.MicroEpochSize * uptimeCfg.MacroEpochMicroFactor
		if err := validateEpochSize(epochSize); err != nil {
			return 0, pos.UptimeConfig{}, err
		}
		if err := validateUptimeConfig(uptimeCfg); err != nil {
			return 0, pos.UptimeConfig{}, err
		}
		return epochSize, uptimeCfg, nil
	}

	epochSize := uint64(DefaultEpochSize)
	if definedEpochSize, ok := ibftConfig[KeyEpochSize]; ok {
		readSize, ok := definedEpochSize.(float64)
		if !ok {
			return 0, pos.UptimeConfig{}, errors.New("invalid type assertion")
		}
		epochSize = uint64(readSize)
	}
	if err := validateEpochSize(epochSize); err != nil {
		return 0, pos.UptimeConfig{}, err
	}
	return epochSize, uptimeCfg, nil
}

func validateUptimeConfig(cfg pos.UptimeConfig) error {
	if cfg.MicroEpochSize == 0 {
		return nil
	}

	if cfg.MacroEpochMicroFactor == 0 {
		return errors.New("macroEpochMicroFactor must be greater than 0 when microEpochSize is enabled")
	}
	if cfg.MicroEpochNominalWeight == 0 {
		return errors.New("microEpochNominalWeightUnits must be greater than 0 when microEpochSize is enabled")
	}
	if cfg.MicroEpochInactivityDecayBps == 0 {
		return errors.New("microEpochInactivityDecayBps must be greater than 0 when microEpochSize is enabled")
	}
	if cfg.MicroEpochInactivityDecayBps > 10000 {
		return errors.New("microEpochInactivityDecayBps must be less than or equal to 10000")
	}

	return nil
}

func validateEpochSize(epochSize uint64) error {
	if epochSize < 2 {
		return errors.New("epochSize must be greater than or equal to 2")
	}

	return nil
}

func (i *backendIBFT) verifyWeightedCommittedPower(
	height uint64,
	hash types.Hash,
	committedSeals signer.Seals,
	validatorSet validators.Validators,
	proposalHeader *types.Header,
	parentHeader *types.Header,
	roundNumber *uint64,
) error {
	if !i.forkManager.IsPosActive(height) {
		return nil
	}

	votingPowers, err := i.getVotingPowersWithProposal(height, validatorSet, proposalHeader, parentHeader, roundNumber)
	if err != nil {
		return err
	}

	signers, err := committedSealSigners(hash, committedSeals, validatorSet)
	if err != nil {
		return err
	}

	collected := new(big.Int)
	total := new(big.Int)
	powerByValidator := make(map[string]string, validatorSet.Len())
	for idx := 0; idx < validatorSet.Len(); idx++ {
		addr := validatorSet.At(uint64(idx)).Addr()
		p, ok := votingPowers[types.AddressToString(addr)]
		if !ok {
			p = big.NewInt(0)
		}
		total.Add(total, p)
		powerByValidator[addr.String()] = p.String()
		if _, ok := signers[addr]; ok {
			collected.Add(collected, p)
		}
	}

	if !hasWeightedQuorum(collected, total) {
		signerAddrs := make([]string, 0, len(signers))
		for addr := range signers {
			signerAddrs = append(signerAddrs, addr.String())
		}
		sort.Strings(signerAddrs)
		quorumThreshold := weightedQuorumThreshold(total)
		proposalNumber := uint64(0)
		proposalHash := types.ZeroHash.String()
		proposalTimestamp := uint64(0)
		if proposalHeader != nil {
			proposalNumber = proposalHeader.Number
			proposalHash = proposalHeader.Hash.String()
			proposalTimestamp = proposalHeader.Timestamp
		}
		parentNumber := uint64(0)
		parentHash := types.ZeroHash.String()
		parentTimestamp := uint64(0)
		if parentHeader != nil {
			parentNumber = parentHeader.Number
			parentHash = parentHeader.Hash.String()
			parentTimestamp = parentHeader.Timestamp
		}
		roundDisplay := "nil"
		if roundNumber != nil {
			roundDisplay = strconv.FormatUint(*roundNumber, 10)
		}
		committedSealsCount := 0
		if committedSeals != nil {
			committedSealsCount = committedSeals.Num()
		}

		return fmt.Errorf(
			"not enough weighted committed power: height=%d round=%s committedSeals=%d collected=%s quorum=%s total=%s proposalNumber=%d proposalHash=%s proposalTimestamp=%d parentNumber=%d parentHash=%s parentTimestamp=%d signerAddresses=%v votingPowers=%v: %w",
			height,
			roundDisplay,
			committedSealsCount,
			collected.String(),
			quorumThreshold.String(),
			total.String(),
			proposalNumber,
			proposalHash,
			proposalTimestamp,
			parentNumber,
			parentHash,
			parentTimestamp,
			signerAddrs,
			powerByValidator,
			signer.ErrNotEnoughCommittedSeals,
		)
	}

	return nil
}

func weightedQuorumThreshold(total *big.Int) *big.Int {
	if total == nil || total.Sign() <= 0 {
		return big.NewInt(0)
	}

	// Equivalent to ceil((2*total)/3), integer-safe.
	// Keep this aligned with the classic PoA quorum behavior for equal weights.
	quorum := new(big.Int).Mul(total, big.NewInt(2))
	quorum.Add(quorum, big.NewInt(2))
	quorum.Div(quorum, big.NewInt(3))

	return quorum
}

func hasWeightedQuorum(collected, total *big.Int) bool {
	if total == nil || total.Sign() <= 0 {
		return false
	}
	if collected == nil || collected.Sign() <= 0 {
		return false
	}

	return collected.Cmp(weightedQuorumThreshold(total)) >= 0
}

func (i *backendIBFT) getVotingPowersWithProposal(
	height uint64,
	validatorSet validators.Validators,
	proposalHeader *types.Header,
	parentHeader *types.Header,
	_ *uint64,
) (map[string]*big.Int, error) {
	_ = proposalHeader
	result, _, err := i.effectiveVotingPowerSnapshot(height, validatorSet, parentHeader)
	return result, err
}

func committedSealSigners(
	hash types.Hash,
	committedSeals signer.Seals,
	validatorSet validators.Validators,
) (map[types.Address]struct{}, error) {
	out := map[types.Address]struct{}{}
	if committedSeals == nil {
		return out, nil
	}

	switch seals := committedSeals.(type) {
	case *signer.AggregatedSeal:
		if seals.Bitmap == nil {
			return out, nil
		}
		for idx := 0; idx < validatorSet.Len(); idx++ {
			if seals.Bitmap.Bit(idx) == 0 {
				continue
			}
			out[validatorSet.At(uint64(idx)).Addr()] = struct{}{}
		}
		return out, nil
	case *signer.SerializedSeal:
		digest := signer.LegacyCommitDigest(hash.Bytes())
		for _, raw := range *seals {
			pub, err := xcrypto.RecoverPubkey(raw, digest)
			if err != nil {
				return nil, err
			}
			addr := xcrypto.PubKeyToAddress(pub)
			if !validatorSet.Includes(addr) {
				return nil, signer.ErrNonValidatorCommittedSeal
			}
			out[addr] = struct{}{}
		}
		return out, nil
	default:
		return nil, signer.ErrInvalidCommittedSealType
	}
}

// getModulesFromForkManager is a helper function to get all modules from ForkManager
func getModulesFromForkManager(forkManager forkManagerInterface, height uint64) (
	signer.Signer,
	validators.Validators,
	fork.HooksInterface,
	error,
) {
	signer, err := forkManager.GetSigner(height)
	if err != nil {
		return nil, nil, nil, err
	}

	validators, err := forkManager.GetValidators(height)
	if err != nil {
		return nil, nil, nil, err
	}

	hooks := forkManager.GetHooks(height)

	return signer, validators, hooks, nil
}

// verifyProposerSeal verifies ProposerSeal in IBFT Extra of header
// and make sure signer belongs to validators
func verifyProposerSeal(
	header *types.Header,
	signer signer.Signer,
	validators validators.Validators,
) error {
	proposer, err := signer.EcrecoverFromHeader(header)
	if err != nil {
		return err
	}

	if !validators.Includes(proposer) {
		return ErrProposerSealByNonValidator
	}

	return nil
}

// ValidateExtraDataFormat Verifies that extra data can be unmarshaled
func (i *backendIBFT) ValidateExtraDataFormat(header *types.Header) error {
	blockSigner, _, _, err := getModulesFromForkManager(
		i.forkManager,
		header.Number,
	)

	if err != nil {
		return err
	}

	_, err = blockSigner.GetIBFTExtra(header)

	return err
}
