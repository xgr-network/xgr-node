package ibft

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/0xPolygon/go-ibft/messages"
	"github.com/0xPolygon/go-ibft/messages/proto"
	"github.com/xgr-network/xgr-node/consensus"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/helper/hex"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
)

func (i *backendIBFT) BuildProposal(view *proto.View) []byte {

	if view == nil {
		if i.logger != nil {
			i.logger.Error("build proposal requested with nil view")
		}

		return nil
	}

	var (
		latestHeader      = i.blockchain.Header()
		latestBlockNumber = latestHeader.Number
	)
	if latestBlockNumber+1 != view.Height {
		i.logger.Error(
			"unable to build block, due to lack of parent block",
			"num",
			latestBlockNumber,
		)

		return nil
	}

	block, err := i.buildBlock(latestHeader)
	if err != nil {
		i.logger.Error("cannot build block", "num", view.Height, "err", err)

		return nil
	}

	return block.MarshalRLP()
}

// InsertProposal inserts a proposal of which the consensus has been got
func (i *backendIBFT) InsertProposal(
	proposal *proto.Proposal,
	committedSeals []*messages.CommittedSeal,
) {
	newBlock := &types.Block{}
	if err := newBlock.UnmarshalRLP(proposal.RawProposal); err != nil {
		i.logger.Error("cannot unmarshal proposal", "err", err)

		return
	}

	committedSealsMap := make(map[types.Address][]byte, len(committedSeals))

	for _, cm := range committedSeals {
		committedSealsMap[types.BytesToAddress(cm.Signer)] = cm.Signature
	}

	// Copy extra data for debugging purposes
	extraDataOriginal := newBlock.Header.ExtraData
	extraDataBackup := make([]byte, len(extraDataOriginal))
	copy(extraDataBackup, extraDataOriginal)

	// Push the committed seals to the header
	currentSigner := i.getCurrentSigner()
	header, err := currentSigner.WriteCommittedSeals(newBlock.Header, proposal.Round, committedSealsMap)
	if err != nil {
		i.logger.Error("cannot write committed seals", "err", err)

		return
	}

	// WriteCommittedSeals alters the extra data before writing the block
	// It doesn't handle errors while pushing changes which can result in
	// corrupted extra data.
	// We don't know exact circumstance of the unmarshalRLP error
	// This is a safety net to help us narrow down and also recover before
	// writing the block
	if err := i.ValidateExtraDataFormat(newBlock.Header); err != nil {
		//Format committed seals to make them more readable
		committedSealsStr := make([]string, len(committedSealsMap))
		for i, seal := range committedSeals {
			committedSealsStr[i] = fmt.Sprintf("{signer=%v signature=%v}",
				hex.EncodeToHex(seal.Signer),
				hex.EncodeToHex(seal.Signature))
		}

		i.logger.Error("cannot write block: corrupted extra data",
			"err", err,
			"before", hex.EncodeToHex(extraDataBackup),
			"after", hex.EncodeToHex(header.ExtraData),
			"committedSeals", committedSealsStr)

		return
	}

	newBlock.Header = header

	// Save the block locally
	if err := i.blockchain.WriteBlock(newBlock, "consensus"); err != nil {
		i.logger.Error("cannot write block", "err", err)

		return
	}
	i.updateMetrics(newBlock)

	//	i.logger.Info(
	//		"block committed",
	//		"number", newBlock.Number(),
	//		"hash", newBlock.Hash(),
	//		"validation_type", i.currentSigner.Type(),
	//		"validators", i.currentValidators.Len(),
	//		"committed", len(committedSeals),
	//	)

	if err := i.getCurrentHooks().PostInsertBlock(newBlock); err != nil {
		i.logger.Error(
			"failed to call PostInsertBlock hook",
			"height", newBlock.Number(),
			"hash", newBlock.Hash(),
			"err", err,
		)

		return
	}

	// after the block has been written we reset the txpool so that
	// the old transactions are removed
	i.txpool.ResetWithHeaders(newBlock.Header)
}

func (i *backendIBFT) ID() []byte {
	return i.getCurrentSigner().Address().Bytes()
}

func (i *backendIBFT) MaximumFaultyNodes() uint64 {
	return uint64(CalcMaxFaultyNodes(i.getCurrentValidators()))
}

func (i *backendIBFT) GetVotingPowers(height uint64) (map[string]*big.Int, error) {
	if height == 0 {
		return nil, fmt.Errorf("invalid height 0")
	}

	validators, err := i.forkManager.GetValidators(height)
	if err != nil {
		return nil, err
	}
	parentHeader, ok := i.blockchain.GetHeaderByNumber(height - 1)
	if !ok {
		return nil, fmt.Errorf("parent header not found for height %d", height)
	}

	result, snapshot, err := i.effectiveVotingPowerSnapshot(height, validators, parentHeader)
	if err != nil {
		return nil, err
	}
	if i.logger != nil && i.forkManager.IsPosActive(height) {
		i.logger.Info(
			"ibft voting powers",
			"height", height,
			"parentNumber", parentHeader.Number,
			"parentHash", parentHeader.Hash,
			"parentStateRoot", parentHeader.StateRoot,
			"stakeWeightedActive", i.forkManager.IsPosActive(parentHeader.Number),
			"totalVotingPower", snapshot.totalVotingPower,
			"quorumThreshold", snapshot.quorumThreshold,
			"votingPowers", snapshot.powers,
		)
	}

	return result, nil
}

// buildBlock builds the block, based on the passed in snapshot and parent header.
func (i *backendIBFT) buildBlock(parent *types.Header) (*types.Block, error) {
	header := &types.Header{
		ParentHash: parent.Hash,
		Number:     parent.Number + 1,
		Miner:      types.ZeroAddress.Bytes(),
		Nonce:      types.Nonce{},
		MixHash:    signer.IstanbulDigest,
		// this is required because blockchain needs difficulty to organize blocks and forks
		Difficulty: parent.Number + 1,
		StateRoot:  types.EmptyRootHash, // this avoids needing state for now
		Sha3Uncles: types.EmptyUncleHash,
		GasLimit:   parent.GasLimit, // Inherit from parent for now, will need to adjust dynamically later.
	}

	// calculate gas limit based on parent header
	gasLimit, err := i.blockchain.CalculateGasLimit(header.Number)
	if err != nil {
		return nil, err
	}

	// calculate base fee
	header.GasLimit = gasLimit
	baseFee := i.blockchain.CalculateBaseFee(parent)

	header.BaseFee = baseFee
	currentHooks := i.getCurrentHooks()
	currentSigner := i.getCurrentSigner()
	currentValidators := i.getCurrentValidators()

	if err := currentHooks.ModifyHeader(header, currentSigner.Address()); err != nil {
		return nil, err
	}

	// Set the header timestamp.
	// PoS mode may still use round-bound logical timestamps in the header,
	// but runtime transaction writing / pacing should stay aligned with normal
	// IBFT wall-clock scheduling.
	var potentialTimestamp time.Time
	runtimeDeadline := i.calcHeaderTimestamp(parent.Timestamp, time.Now().UTC())
	potentialTimestamp = runtimeDeadline
	header.Timestamp = uint64(potentialTimestamp.Unix())

	parentCommittedSeals, err := i.extractParentCommittedSeals(parent)
	if err != nil {
		return nil, err
	}

	currentSigner.InitIBFTExtra(header, currentValidators, parentCommittedSeals)

	transition, err := i.executor.BeginTxn(parent.StateRoot, header, currentSigner.Address())
	if err != nil {
		return nil, err
	}
	// Get the block transactions
	writeCtx, cancelFn := context.WithDeadline(context.Background(), runtimeDeadline)
	defer cancelFn()

	txs := i.writeTransactions(
		writeCtx,
		gasLimit,
		header.Number,
		transition,
	)
	// Provide the in-progress block body to PreCommitState. PoS epoch boundary
	// finalization writes a deterministic internal system receipt; when that
	// happens, append the matching system transaction to the otherwise user-tx-free
	// block so receipt count, receipt root, and tx root remain deterministic.
	preCommitBlock := &types.Block{Header: header, Transactions: txs}
	if err := i.PreCommitState(preCommitBlock, transition); err != nil {
		return nil, fmt.Errorf("pre-commit state failed at block %d: %w", header.Number, err)
	}
	if len(transition.Receipts()) == len(txs)+1 {
		finalizationTx := pos.EpochFinalizationSystemTx(header.Number)
		if transition.Receipts()[len(transition.Receipts())-1].TxHash == finalizationTx.Hash {
			txs = append(txs, finalizationTx)
		}
	}
	_, root, err := transition.Commit()
	if err != nil {
		return nil, fmt.Errorf("failed to commit the state changes: %w", err)
	}

	header.StateRoot = root
	header.GasUsed = transition.TotalGas()

	// build the block
	block := consensus.BuildBlock(consensus.BuildBlockParams{
		Header:   header,
		Txns:     txs,
		Receipts: transition.Receipts(),
	})

	// write the seal of the block after all the fields are completed
	header, err = currentSigner.WriteProposerSeal(header)
	if err != nil {
		return nil, err
	}

	block.Header = header

	// compute the hash, this is only a provisional hash since the final one
	// is sealed after all the committed seals
	block.Header.ComputeHash()

	i.logger.Info("build block", "number", header.Number, "txs", len(txs))
	return block, nil
}

// calcHeaderTimestamp calculates the new block timestamp, based
// on the block time and parent timestamp
func (i *backendIBFT) calcHeaderTimestamp(parentUnix uint64, currentTime time.Time) time.Time {
	var (
		parentTimestamp    = time.Unix(int64(parentUnix), 0)
		potentialTimestamp = parentTimestamp.Add(i.blockTime)
	)

	if potentialTimestamp.Before(currentTime) {
		// The deadline for creating this next block
		// has passed, round it to the nearest
		// multiple of block time
		// t........t+blockT...x (t+blockT.x; now).....t+blockT (potential)
		potentialTimestamp = roundUpTime(currentTime, i.blockTime)
	}

	return potentialTimestamp
}

// roundUpTime rounds up the specified time to the
// nearest higher multiple
func roundUpTime(t time.Time, roundOn time.Duration) time.Time {
	return t.Add(roundOn / 2).Round(roundOn)
}

type status uint8

const (
	success status = iota
	fail
	skip
)

type txExeResult struct {
	tx     *types.Transaction
	status status
}

type transitionInterface interface {
	Write(txn *types.Transaction) error
}

func (i *backendIBFT) writeTransactions(
	writeCtx context.Context,
	gasLimit,
	blockNumber uint64,
	transition transitionInterface,
) (executed []*types.Transaction) {
	executed = make([]*types.Transaction, 0)

	if !i.getCurrentHooks().ShouldWriteTransactions(blockNumber) {
		return
	}

	var (
		successful = 0
		failed     = 0
		skipped    = 0
	)

	defer func() {
		i.logger.Info(
			"executed txs",
			"successful", successful,
			"failed", failed,
			"skipped", skipped,
			"remaining", i.txpool.Length(),
		)
	}()

	i.txpool.Prepare()

write:
	for {
		select {
		case <-writeCtx.Done():
			return
		default:
			// execute transactions one by one
			result, ok := i.writeTransaction(
				i.txpool.Peek(),
				transition,
				gasLimit,
			)

			if !ok {
				break write
			}

			tx := result.tx

			switch result.status {
			case success:
				executed = append(executed, tx)
				successful++
			case fail:
				failed++
			case skip:
				skipped++
			}
		}
	}

	// wait for the timer to expire so the built block is aligned with the
	// configured block time schedule
	<-writeCtx.Done()

	return
}

func (i *backendIBFT) writeTransaction(
	tx *types.Transaction,
	transition transitionInterface,
	gasLimit uint64,
) (*txExeResult, bool) {
	if tx == nil {
		return nil, false
	}

	if tx.Gas > gasLimit {
		i.txpool.Drop(tx)

		// continue processing
		return &txExeResult{tx, fail}, true
	}

	if err := transition.Write(tx); err != nil {
		if _, ok := err.(*state.GasLimitReachedTransitionApplicationError); ok { //nolint:errorlint
			// stop processing
			return nil, false
		} else if appErr, ok := err.(*state.TransitionApplicationError); ok && appErr.IsRecoverable { //nolint:errorlint
			i.txpool.Demote(tx)

			return &txExeResult{tx, skip}, true
		} else {
			i.txpool.Drop(tx)

			return &txExeResult{tx, fail}, true
		}
	}

	i.txpool.Pop(tx)

	return &txExeResult{tx, success}, true
}

// extractCommittedSeals extracts CommittedSeals from header
func (i *backendIBFT) extractCommittedSeals(
	header *types.Header,
) (signer.Seals, error) {
	signer, err := i.forkManager.GetSigner(header.Number)
	if err != nil {
		return nil, err
	}

	extra, err := signer.GetIBFTExtra(header)
	if err != nil {
		return nil, err
	}

	return extra.CommittedSeals, nil
}

// extractParentCommittedSeals extracts ParentCommittedSeals from header
func (i *backendIBFT) extractParentCommittedSeals(
	header *types.Header,
) (signer.Seals, error) {
	if header.Number == 0 {
		return nil, nil
	}

	return i.extractCommittedSeals(header)
}
