package ibft

import (
	"fmt"
	"path/filepath"
	"time"

	stakingcontract "github.com/xgr-network/xgr-node/contracts/staking"
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
	routes, err := evmInterchain.LoadCheckpointRoutes(destinations)
	if err != nil {
		i.logger.Warn("interchain disabled: invalid checkpoint route configuration", "err", err)
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
		destinations,
		routes,
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
	i.logger.Info("interchain worker started", "destinations", len(destinations), "routes", len(routes))
}
