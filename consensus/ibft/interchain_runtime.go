package ibft

import (
	"fmt"
	"math/big"
	"path/filepath"
	"time"

	stakingcontract "github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
	interchainRuntime "github.com/xgr-network/xgr-node/interchain/runtime"
	"github.com/xgr-network/xgr-node/types"
)

type ibftInterchainState struct {
	ibft *backendIBFT
}

func (s *ibftInterchainState) OriginChainID() uint64 {
	if s == nil || s.ibft == nil || s.ibft.config == nil || s.ibft.config.Params == nil {
		return 0
	}
	if s.ibft.config.Params.ChainID <= 0 {
		return 0
	}
	return uint64(s.ibft.config.Params.ChainID)
}

func (s *ibftInterchainState) IsSynchronized() bool {
	if s == nil || s.ibft == nil || s.ibft.syncer == nil || s.ibft.blockchain == nil || s.ibft.network == nil {
		return false
	}
	if s.ibft.syncer.GetSyncProgression() != nil || s.ibft.syncer.HasSyncPeer() {
		return false
	}
	// An isolated node cannot prove that its local head is globally fresh.
	if len(s.ibft.network.Peers()) == 0 {
		return false
	}
	header := s.ibft.blockchain.Header()
	if header == nil || header.Timestamp == 0 {
		return false
	}

	now := time.Now()
	if header.Timestamp > uint64(now.Add(15*time.Second).Unix()) {
		return false
	}
	freshness := 5 * s.ibft.blockTime
	if freshness < 30*time.Second {
		freshness = 30 * time.Second
	}
	if now.Sub(time.Unix(int64(header.Timestamp), 0)) > freshness {
		return false
	}

	return true
}

func callInterchainView(tx stakingcontract.TxQueryHandler, addr types.Address, input []byte) ([]byte, error) {
	tx.SetNonPayable(true)
	call := &types.Transaction{
		From:     types.ZeroAddress,
		To:       &addr,
		Input:    append([]byte(nil), input...),
		Nonce:    tx.GetNonce(types.ZeroAddress),
		Gas:      1_000_000,
		Value:    big.NewInt(0),
		GasPrice: big.NewInt(0),
	}
	res, err := tx.Apply(call)
	if err != nil {
		return nil, err
	}
	if res.Failed() {
		return nil, res.Err
	}
	return append([]byte(nil), res.ReturnValue...), nil
}

func interchainMethodSelector(signature string) []byte {
	hash := crypto.Keccak256([]byte(signature))
	return append([]byte(nil), hash[:4]...)
}

func (s *ibftInterchainState) OriginCheckpoint(mailbox, hook types.Address) (types.Hash, uint32, error) {
	if s == nil || s.ibft == nil || s.ibft.blockchain == nil || s.ibft.executor == nil {
		return types.ZeroHash, 0, fmt.Errorf("interchain XGR state reader is unavailable")
	}
	header := s.ibft.blockchain.Header()
	if header == nil {
		return types.ZeroHash, 0, fmt.Errorf("XGR head is unavailable")
	}
	tx, err := s.ibft.executor.BeginTxn(header.StateRoot, header, types.ZeroAddress)
	if err != nil {
		return types.ZeroHash, 0, fmt.Errorf("open XGR state at head %d: %w", header.Number, err)
	}

	mailboxRaw, err := callInterchainView(tx, hook, interchainMethodSelector("mailbox()"))
	if err != nil {
		return types.ZeroHash, 0, fmt.Errorf("read MerkleTreeHook mailbox at head %d: %w", header.Number, err)
	}
	if len(mailboxRaw) != 32 {
		return types.ZeroHash, 0, fmt.Errorf("unexpected MerkleTreeHook mailbox response length %d", len(mailboxRaw))
	}
	if got := types.BytesToAddress(mailboxRaw[12:]); got != mailbox {
		return types.ZeroHash, 0, fmt.Errorf("configured MerkleTreeHook belongs to mailbox %s, expected %s", got, mailbox)
	}

	countRaw, err := callInterchainView(tx, hook, interchainMethodSelector("count()"))
	if err != nil {
		return types.ZeroHash, 0, fmt.Errorf("read MerkleTreeHook count at head %d: %w", header.Number, err)
	}
	if len(countRaw) != 32 {
		return types.ZeroHash, 0, fmt.Errorf("unexpected MerkleTreeHook count response length %d", len(countRaw))
	}
	count := new(big.Int).SetBytes(countRaw)
	if count.Sign() == 0 {
		return types.ZeroHash, 0, nil
	}
	if count.BitLen() > 32 {
		return types.ZeroHash, 0, fmt.Errorf("MerkleTreeHook count exceeds uint32")
	}

	rootRaw, err := callInterchainView(tx, hook, interchainMethodSelector("root()"))
	if err != nil {
		return types.ZeroHash, 0, fmt.Errorf("read MerkleTreeHook root at head %d: %w", header.Number, err)
	}
	if len(rootRaw) != 32 {
		return types.ZeroHash, 0, fmt.Errorf("unexpected MerkleTreeHook root response length %d", len(rootRaw))
	}
	root := types.BytesToHash(rootRaw)
	if root == types.ZeroHash {
		return types.ZeroHash, 0, fmt.Errorf("MerkleTreeHook returned zero root with non-zero count")
	}
	return root, uint32(count.Uint64() - 1), nil
}

func (s *ibftInterchainState) ValidatorInfo(addr types.Address) (*stakingcontract.ValidatorInfo, error) {
	if s == nil || s.ibft == nil {
		return nil, fmt.Errorf("interchain XGR state reader is unavailable")
	}
	header := s.ibft.blockchain.Header()
	if header == nil {
		return nil, fmt.Errorf("XGR head is unavailable")
	}
	tx, err := s.ibft.executor.BeginTxn(header.StateRoot, header, types.ZeroAddress)
	if err != nil {
		return nil, fmt.Errorf("open XGR state at head %d: %w", header.Number, err)
	}
	info, err := stakingcontract.QueryValidatorInfo(tx, types.ZeroAddress, addr)
	if err != nil {
		return nil, fmt.Errorf("read XGR validator %s at head %d: %w", addr, header.Number, err)
	}
	return info, nil
}

// startInterchainRuntime is deliberately outside every consensus-critical hook.
// Destination failures disable only interchain processing; they must never stop
// XGR block production, validation, finalization, or syncing.
func (i *backendIBFT) startInterchainRuntime() {
	destinations, err := evmInterchain.LoadAll()
	if err != nil {
		i.logger.Warn("interchain disabled: invalid destination configuration", "err", err)
		return
	}
	if len(destinations) == 0 {
		return
	}
	originContracts, err := evmInterchain.LoadOriginContracts()
	if err != nil {
		i.logger.Warn("interchain disabled: invalid origin Hyperlane configuration", "err", err)
		return
	}
	if originContracts == nil {
		i.logger.Warn("interchain disabled: origin Hyperlane contracts are not configured")
		return
	}

	stateReader := &ibftInterchainState{ibft: i}
	if stateReader.OriginChainID() == 0 {
		i.logger.Warn("interchain disabled: invalid XGR origin chain ID")
		return
	}

	dataDir := filepath.Dir(i.config.Path)
	worker, err := interchainRuntime.New(
		i.logger,
		stateReader,
		i.network,
		i.secretsManager,
		dataDir,
		originContracts,
		destinations,
	)
	if err != nil {
		i.logger.Warn("interchain disabled: worker initialization failed", "err", err)
		return
	}
	if err := worker.Start(); err != nil {
		worker.Close()
		i.logger.Warn("interchain disabled: worker start failed", "err", err)
		return
	}

	i.interchainClose = worker.Close
	i.logger.Info("interchain worker started", "destinations", len(destinations))
}
