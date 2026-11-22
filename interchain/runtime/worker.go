package runtime

import (
	"bytes"
	"context"
	stdECDSA "crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/umbracle/ethgo"
	ethgowallet "github.com/umbracle/ethgo/wallet"
	"golang.org/x/sync/singleflight"
	"google.golang.org/protobuf/types/known/wrapperspb"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	stakingcontract "github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/interchain/authorization"
	evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
	"github.com/xgr-network/xgr-node/network"
	"github.com/xgr-network/xgr-node/secrets"
	"github.com/xgr-network/xgr-node/types"
)

const (
	TopicID             = "/xgr/interchain/direct-bls/1.0.0"
	defaultPollInterval = 2 * time.Second // local state / gossip, no periodic external RPC
	// XGR v3.2.1: external RPC traffic must not follow the fast 2s loop.
	// The XITA finalized indexer submits source-tx-pinned untrusted hints via
	// xgr_requestILNQuorumWithSourceTx. Keep independent RPC-native polling as
	// a slow censorship/outage fallback, never a signing trust assumption.
	ilnExternalCheckpointInterval = 10 * time.Minute
	ilnExternalCatchupInterval    = 60 * time.Second
	ilnExternalDiscoveryInterval  = 5 * time.Minute
	ilnExternalMembershipInterval = 60 * time.Second
	ilnExternalRetryInterval      = 30 * time.Second
	ilnLocalHealthInterval        = 10 * time.Second
	maximumMembershipLifetime     = 10 * time.Minute
	requestRebroadcastInterval    = 10 * time.Second
)

type StateReader interface {
	OriginChainID() uint64
	ValidatorInfo(types.Address) (*stakingcontract.ValidatorInfo, error)
	IsSynchronized() bool
}

type localRequest struct {
	Chain  string `json:"chain"`
	Active bool   `json:"active"`
}

type localResult struct {
	Done      bool   `json:"done"`
	Error     string `json:"error,omitempty"`
	Validator string `json:"validator,omitempty"`
	Active    bool   `json:"active"`
	Changed   bool   `json:"changed"`
	TxHash    string `json:"txHash,omitempty"`
	SetID     uint64 `json:"setId,omitempty"`
}

type wireEnvelope struct {
	Type               string                    `json:"type"`
	Req                *wireRequest              `json:"request,omitempty"`
	Vote               *wireVote                 `json:"vote,omitempty"`
	ILNCheckpointVote  *ilnCheckpointVote        `json:"ilnCheckpointVote,omitempty"`
	ILNCompletedQuorum *ilnCheckpointAttestation `json:"ilnCompletedQuorum,omitempty"`
	ILNRouteNotice     *ilnRouteNoticeWire       `json:"ilnRouteNotice,omitempty"`
	ILNQuorumRequest   *requestedILNQuorum       `json:"ilnQuorumRequest,omitempty"`
	GovernanceProposal *governanceProposalWire   `json:"governanceProposal,omitempty"`
	GovernanceVote     *governanceVote           `json:"governanceVote,omitempty"`
}

type wireRequest struct {
	Chain              string `json:"chain"`
	Payload            []byte `json:"payload"`
	CandidateSignature []byte `json:"candidateSignature,omitempty"`
	Forced             bool   `json:"forced,omitempty"`
}

type wireVote struct {
	Chain     string        `json:"chain"`
	Payload   []byte        `json:"payload"`
	Signer    types.Address `json:"signer"`
	Signature []byte        `json:"signature"`
}

type requestState struct {
	request         wireRequest
	payload         authorization.Membership
	firstSeen       time.Time
	lastBroadcast   time.Time
	localRequestIDs map[string]struct{}
	votes           map[types.Address][]byte
}

type Worker struct {
	logger             hclog.Logger
	state              StateReader
	network            *network.Server
	secrets            secrets.SecretsManager
	dataDir            string
	destinations       map[string]*evmInterchain.Destination
	routes             map[string]*evmInterchain.CheckpointRoute
	ilnRestoredSources map[string]bool
	// Last independently verified per-route observations for local-only health.
	ilnRouteHealthByName map[string]ilnRouteHealth
	// Accessed only by the interchain worker loop, never from gossip handlers.
	ilnLastCheckpointPoll time.Time
	// One-minute catch-up applies only to external sources with known backlog.
	ilnLastCatchupPoll    time.Time
	ilnDirectScanRetries  map[string]directILNScanRetry
	ilnLastDiscoveryPoll  time.Time
	ilnLastMembershipPoll time.Time
	ilnLastRetryPoll      time.Time
	ilnLastHealthWrite    time.Time
	ilnRouteLastPublished map[string]time.Time
	ilnRouteNoticeChecks  singleflight.Group
	ilnRouteFilesMu       sync.Mutex
	ilnRouteNoticeCache   map[string]verifiedILNNotice
	ilnRouteLastSeen      map[peer.ID]time.Time

	ecdsaKey  *stdECDSA.PrivateKey
	txKey     ethgo.Key
	localAddr types.Address
	blsRaw    []byte
	blsPubKey []byte

	topic   *network.Topic
	closeCh chan struct{}
	wg      sync.WaitGroup

	rpcCancel context.CancelFunc
	// Message-scoped source verification backpressure, shared by every ILN
	// signing entry point. Never protect this with w.mu: verification performs
	// independently locked vote and network operations.
	ilnRetryMu                sync.Mutex
	ilnRetryInFlight          map[string]bool
	mu                        sync.Mutex
	requests                  map[types.Hash]*requestState
	reserveWarned             map[string]bool
	ilnLocalVotes             map[types.Hash]*ilnLocalVoteState
	ilnCompletedPublished     map[string]time.Time
	ilnCompletedInbound       map[peer.ID]ilnCompleteInboundBucket
	ilnCheckpoints            map[types.Hash]*ilnCheckpointState
	governance                map[types.Hash]*governanceState
	governanceExecuteCh       chan governanceExecuteRequest
	governanceExecuteInFlight map[string]struct{}
}

func New(
	logger hclog.Logger,
	state StateReader,
	networkServer *network.Server,
	secretsManager secrets.SecretsManager,
	dataDir string,
	destinations []*evmInterchain.Destination,
	routes []*evmInterchain.CheckpointRoute,
) (*Worker, error) {
	if logger == nil {
		logger = hclog.NewNullLogger()
	}
	if state == nil || networkServer == nil || secretsManager == nil {
		return nil, fmt.Errorf("interchain worker requires state, network, and secrets manager")
	}
	if stringsTrim(dataDir) == "" {
		return nil, fmt.Errorf("interchain worker data dir is required")
	}

	ecdsaRaw, err := secretsManager.GetSecret(secrets.ValidatorKey)
	if err != nil {
		return nil, fmt.Errorf("load validator ECDSA key: %w", err)
	}
	ecdsaBytes, err := hex.DecodeString(string(ecdsaRaw))
	if err != nil {
		return nil, fmt.Errorf("decode validator ECDSA key: %w", err)
	}
	txKey, err := ethgowallet.NewWalletFromPrivKey(ecdsaBytes)
	if err != nil {
		return nil, fmt.Errorf("parse validator ECDSA key: %w", err)
	}

	ecdsaKey, err := crypto.BytesToECDSAPrivateKey(ecdsaRaw)
	if err != nil {
		return nil, fmt.Errorf("parse validator signing key: %w", err)
	}
	var blsRaw, blsPubKey []byte
	for _, d := range destinations {
		if d == nil {
			continue
		}
		adapter, err := d.AuthorizationAdapter()
		if err != nil {
			return nil, err
		}
		if adapter.Scheme() != authorization.SchemeBLS {
			continue
		}
		blsRaw, err = secretsManager.GetSecret(secrets.ValidatorBLSKey)
		if err != nil {
			return nil, fmt.Errorf("load validator BLS key: %w", err)
		}
		blsSecret, err := crypto.BytesToBLSSecretKey(blsRaw)
		if err != nil {
			return nil, fmt.Errorf("parse validator BLS key: %w", err)
		}
		blsPubKey, err = crypto.BLSSecretKeyToPubkeyBytes(blsSecret)
		if err != nil {
			return nil, fmt.Errorf("derive validator BLS public key: %w", err)
		}
		break
	}

	byName := make(map[string]*evmInterchain.Destination, len(destinations))
	for _, d := range destinations {
		if d == nil {
			continue
		}
		// One persistent source-chain/hook cache per validator node. All routes
		// remain permissionless; no per-route configuration is introduced.
		if err := d.ValidateMembershipRead(); err != nil {
			return nil, fmt.Errorf("destination %q: %w", d.Name, err)
		}
		byName[d.Name] = d
	}

	routesByName := make(map[string]*evmInterchain.CheckpointRoute, len(routes))
	routeIdentities := make(map[string]string, len(routes))
	for _, route := range routes {
		if route == nil {
			continue
		}
		if err := route.Validate(); err != nil {
			return nil, fmt.Errorf("checkpoint route %q: %w", route.Name, err)
		}
		if byName[route.SourceNetwork] == nil {
			return nil, fmt.Errorf("checkpoint route %q references unknown source network %q", route.Name, route.SourceNetwork)
		}
		if byName[route.Destination] == nil {
			return nil, fmt.Errorf("checkpoint route %q references unknown destination %q", route.Name, route.Destination)
		}
		if _, exists := routesByName[route.Name]; exists {
			return nil, fmt.Errorf("checkpoint route %q is configured more than once", route.Name)
		}
		identity := route.SourceNetwork + "|" + route.Destination + "|" + route.RouteID.String()
		if previous, exists := routeIdentities[identity]; exists {
			return nil, fmt.Errorf(
				"checkpoint routes %q and %q resolve to the same source, destination, and route id",
				previous, route.Name,
			)
		}
		routeIdentities[identity] = route.Name
		routesByName[route.Name] = route
	}
	return &Worker{
		logger:                    logger.Named("interchain"),
		state:                     state,
		network:                   networkServer,
		secrets:                   secretsManager,
		dataDir:                   dataDir,
		destinations:              byName,
		routes:                    routesByName,
		ecdsaKey:                  ecdsaKey,
		txKey:                     txKey,
		localAddr:                 types.Address(txKey.Address()),
		blsRaw:                    blsRaw,
		blsPubKey:                 blsPubKey,
		closeCh:                   make(chan struct{}),
		requests:                  make(map[types.Hash]*requestState),
		reserveWarned:             make(map[string]bool),
		ilnLocalVotes:             make(map[types.Hash]*ilnLocalVoteState),
		ilnCompletedPublished:     make(map[string]time.Time),
		ilnCompletedInbound:       make(map[peer.ID]ilnCompleteInboundBucket),
		ilnCheckpoints:            make(map[types.Hash]*ilnCheckpointState),
		ilnRouteLastPublished:     make(map[string]time.Time),
		ilnRouteLastSeen:          make(map[peer.ID]time.Time),
		governance:                make(map[types.Hash]*governanceState),
		governanceExecuteCh:       make(chan governanceExecuteRequest, 1),
		governanceExecuteInFlight: make(map[string]struct{}),
	}, nil
}

func (w *Worker) Start() error {
	if len(w.destinations) == 0 {
		return nil
	}
	topic, err := w.network.NewTopic(TopicID, &wrapperspb.BytesValue{})
	if err != nil {
		return err
	}
	if err := topic.Subscribe(func(obj interface{}, from peer.ID) {
		defer func() {
			if recovered := recover(); recovered != nil {
				w.logger.Error("interchain gossip panic isolated", "panic", recovered)
			}
		}()
		value, ok := obj.(*wrapperspb.BytesValue)
		if !ok || value == nil {
			return
		}
		if err := w.handleWireFrom(value.Value, from); err != nil {
			w.logger.Warn("interchain gossip rejected", "err", err)
		}
	}); err != nil {
		topic.Close()
		return err
	}
	w.topic = topic

	if err := os.MkdirAll(w.requestDir(), 0o770); err != nil {
		return err
	}
	if err := os.MkdirAll(w.resultDir(), 0o770); err != nil {
		return err
	}
	if err := os.MkdirAll(w.pendingDir(), 0o770); err != nil {
		return err
	}
	if err := os.MkdirAll(w.attestationDir(), 0o770); err != nil {
		return err
	}
	if err := os.MkdirAll(w.ilnCursorDir(), 0o770); err != nil {
		return err
	}
	// Static route aliases must also be available for public RPC requests,
	// including on a node that is currently not an eligible ILN signer.
	for _, route := range w.routes {
		if err := w.registerILNRPCRoute(route); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(w.ilnVoteDir(), 0o770); err != nil {
		return err
	}
	if err := w.recoverILNLocalVotes(); err != nil {
		return err
	}
	if err := os.MkdirAll(w.governanceProposalDir(), 0o770); err != nil {
		return err
	}
	if err := os.MkdirAll(w.governanceRequestDir(), 0o770); err != nil {
		return err
	}
	if err := os.MkdirAll(w.governanceResultDir(), 0o770); err != nil {
		return err
	}
	if err := os.MkdirAll(w.governanceQuorumDir(), 0o770); err != nil {
		return err
	}
	if err := w.touchHeartbeat(); err != nil {
		return err
	}

	rpcContext, rpcCancel := context.WithCancel(context.Background())
	w.rpcCancel = rpcCancel
	for _, destination := range w.destinations {
		destination.SetILNRPCScope(rpcContext)
	}
	w.wg.Add(5)
	go w.heartbeatLoop()
	go w.quorumHintLoop()
	go w.storageMaintenanceLoop()
	go w.governanceExecuteLoop()
	go w.loop()
	return nil
}

func (w *Worker) Close() {
	select {
	case <-w.closeCh:
	default:
		close(w.closeCh)
	}
	if w.rpcCancel != nil {
		w.rpcCancel()
	}
	w.wg.Wait()
	_ = os.Remove(w.heartbeatPath())
	if w.topic != nil {
		w.topic.Close()
	}
}

func (w *Worker) loop() {
	defer w.wg.Done()

	// Governance recovery can require destination-chain RPC reads. Keep it
	// strictly inside the worker goroutine so an unavailable external chain
	// can never delay or block IBFT startup.
	if err := w.recoverGovernanceProposals(); err != nil {
		w.logger.Warn("recover persisted ILN governance proposals failed", "err", err)
	}

	ticker := time.NewTicker(defaultPollInterval)
	defer ticker.Stop()

	w.tick()
	for {
		select {
		case <-ticker.C:
			w.tick()
		case <-w.closeCh:
			return
		}
	}
}

// dueILNPeriodicPolling updates a loop-local schedule before performing a
// potentially failing RPC operation. A 429 must never cause a 2-second retry
// storm. On-demand quorum and membership requests bypass this background gate.
func dueILNPeriodicPolling(now time.Time, last *time.Time, interval time.Duration) bool {
	if !last.IsZero() && now.Sub(*last) < interval {
		return false
	}
	*last = now
	return true
}

func (w *Worker) tick() {
	defer func() {
		if recovered := recover(); recovered != nil {
			w.logger.Error("interchain worker panic isolated", "panic", recovered)
		}
	}()
	if !w.state.IsSynchronized() {
		w.logger.Debug("interchain signing paused while XGR node is synchronizing")
		return
	}
	// Fast, local/event-driven queue processing remains responsive at 2s.
	if err := w.processLocalRequests(); err != nil {
		w.logger.Warn("process local interchain requests failed", "err", err)
	}
	if err := w.recoverLocalPending(); err != nil {
		w.logger.Warn("recover local interchain requests failed", "err", err)
	}
	if err := w.processLocalGovernanceRequests(); err != nil {
		w.logger.Warn("process local ILN governance requests failed", "err", err)
	}
	now := time.Now()
	if dueILNPeriodicPolling(now, &w.ilnLastMembershipPoll, ilnExternalMembershipInterval) {
		for _, destination := range w.destinations {
			poll, err := evmInterchain.PollMembershipRegistry(destination, w.localAddr)
			if err != nil {
				w.logger.Warn("interchain membership poll failed", "chain", destination.Name, "err", err)
				continue
			}
			setErr := poll.SetError
			if setErr == nil {
				setErr = w.detectForcedRemovals(destination, poll.Set)
			}
			if setErr != nil {
				w.logger.Warn("forced removal scan failed", "chain", destination.Name, "err", setErr)
			}
			if poll.ReserveError != nil {
				w.logger.Warn("interchain reserve check failed", "chain", destination.Name, "err", poll.ReserveError)
			} else {
				w.checkLocalReserve(destination, poll.Local, poll.Minimum)
			}
		}
	}
	if dueILNPeriodicPolling(now, &w.ilnLastDiscoveryPoll, ilnExternalDiscoveryInterval) {
		w.discoverILNRoutes()
		w.publishVerifiedILNRoutes()
	}
	// The full independent fallback scanner remains on its ten-minute cadence.
	// During catch-up, poll only external sources with a locally observed lag.
	fullScan := dueILNPeriodicPolling(now, &w.ilnLastCheckpointPoll, ilnExternalCheckpointInterval)
	catchupScan := false
	if fullScan {
		w.ilnLastCatchupPoll = now
	} else if w.hasILNExternalObserverBacklog() {
		catchupScan = dueILNPeriodicPolling(now, &w.ilnLastCatchupPoll, ilnExternalCatchupInterval)
	}
	if fullScan || catchupScan {
		w.mu.Lock()
		activeRoutes := make([]*evmInterchain.CheckpointRoute, 0, len(w.routes))
		for _, route := range w.routes {
			activeRoutes = append(activeRoutes, route)
		}
		w.mu.Unlock()
		ready := make([]*evmInterchain.CheckpointRoute, 0, len(activeRoutes))
		for _, route := range activeRoutes {
			if !fullScan && !w.ilnSourceHasExternalObserverBacklog(route.SourceNetwork) {
				continue
			}
			if w.directILNScanMayRun(route, now) {
				ready = append(ready, route)
			}
		}
		if len(ready) > 0 {
			failures := w.scanILNObserverRoutes(ready)
			w.finishILNObserverScanBackoff(ready, failures, time.Now())
			for _, route := range ready {
				if err := failures[route.Name]; err != nil {
					w.recordILNDirectScanFailure(route, err)
					w.logger.Debug("Direct BLS source observation pending",
						"route", route.Name, "err", err)
				}
			}
		}
	}
	if dueILNPeriodicPolling(now, &w.ilnLastHealthWrite, ilnLocalHealthInterval) {
		// v3.2.1: purely local persisted observations; never query Base/XGR.
		w.writeILNIndexerHealth()
	}
	w.rebroadcastPendingRequests()
	if dueILNPeriodicPolling(now, &w.ilnLastRetryPoll, ilnExternalRetryInterval) {
		// Pending votes/message repair may require remote reads. They MUST NOT
		// retry every 2s when the remote chain rate-limits a validator.
		w.rebroadcastILNCheckpointVotes()
		w.rebroadcastILNCompletedQuorums()
		w.trySubmissions()
	}
	w.rebroadcastGovernance()
}

func (w *Worker) requestDir() string           { return filepath.Join(w.dataDir, "interchain", "requests") }
func (w *Worker) resultDir() string            { return filepath.Join(w.dataDir, "interchain", "results") }
func (w *Worker) pendingDir() string           { return filepath.Join(w.dataDir, "interchain", "pending") }
func (w *Worker) pendingPath(id string) string { return filepath.Join(w.pendingDir(), id+".json") }
func (w *Worker) attestationDir() string {
	return filepath.Join(w.dataDir, "interchain", "attestations")
}
func (w *Worker) heartbeatPath() string {
	return filepath.Join(w.dataDir, "interchain", "worker.heartbeat")
}

func (w *Worker) persistLocalPending(id string, req localRequest) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	tmp := w.pendingPath(id) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o660); err != nil {
		return err
	}
	return os.Rename(tmp, w.pendingPath(id))
}

func (w *Worker) recoverLocalPending() error {
	entries, err := os.ReadDir(w.pendingDir())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-len(".json")]
		if w.hasLocalRequestID(id) {
			continue
		}
		raw, err := os.ReadFile(w.pendingPath(id))
		if err != nil {
			continue
		}
		var req localRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			_ = w.completeLocalResult(id, localResult{Done: true, Error: err.Error()})
			continue
		}
		if err := w.startLocalRequest(id, req); err != nil {
			_ = w.completeLocalResult(id, localResult{Done: true, Error: err.Error()})
		}
	}
	return nil
}

func (w *Worker) hasLocalRequestID(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, state := range w.requests {
		if _, ok := state.localRequestIDs[id]; ok {
			return true
		}
	}
	return false
}

func (w *Worker) completeLocalResult(id string, result localResult) error {
	if err := w.writeLocalResult(id, result); err != nil {
		return err
	}
	if err := os.Remove(w.pendingPath(id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (w *Worker) attachLocalToPending(id, chain string, setID uint64, action protocol.Action, validator types.Address, blsKey []byte) bool {
	now := uint64(time.Now().Unix())
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, state := range w.requests {
		if state.payload.ValidUntil < now {
			continue
		}
		if state.request.Chain == chain &&
			state.payload.ExpectedSetID == setID &&
			state.payload.Action == action &&
			state.payload.Validator == validator &&
			bytes.Equal(state.payload.BLSPublicKey, blsKey) {
			if state.localRequestIDs == nil {
				state.localRequestIDs = make(map[string]struct{})
			}
			state.localRequestIDs[id] = struct{}{}
			return true
		}
	}
	return false
}

func (w *Worker) hasPendingMembership(chain string, setID uint64, action protocol.Action, validator types.Address) bool {
	now := uint64(time.Now().Unix())
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, state := range w.requests {
		if state.payload.ValidUntil < now {
			continue
		}
		if state.request.Chain == chain &&
			state.payload.ExpectedSetID == setID &&
			state.payload.Action == action &&
			state.payload.Validator == validator {
			return true
		}
	}
	return false
}

// The worker heartbeat must not depend on slow external EVM RPCs in tick().
func (w *Worker) heartbeatLoop() {
	defer w.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-w.closeCh:
			return
		case <-ticker.C:
			if err := w.touchHeartbeat(); err != nil {
				w.logger.Warn("update interchain worker heartbeat failed", "err", err)
			}
		}
	}
}

func (w *Worker) touchHeartbeat() error {
	now := []byte(fmt.Sprintf("%d\n", time.Now().UnixNano()))
	tmp := w.heartbeatPath() + ".tmp"
	if err := os.WriteFile(tmp, now, 0o660); err != nil {
		return err
	}
	return os.Rename(tmp, w.heartbeatPath())
}

func (w *Worker) processLocalRequests() error {
	entries, err := os.ReadDir(w.requestDir())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-len(".json")]
		raw, err := os.ReadFile(filepath.Join(w.requestDir(), entry.Name()))
		if err != nil {
			continue
		}
		var req localRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			w.writeLocalResult(id, localResult{Done: true, Error: err.Error()})
			_ = os.Remove(filepath.Join(w.requestDir(), entry.Name()))
			continue
		}
		if err := w.persistLocalPending(id, req); err != nil {
			_ = w.writeLocalResult(id, localResult{Done: true, Error: err.Error()})
			_ = os.Remove(filepath.Join(w.requestDir(), entry.Name()))
			continue
		}
		if err := w.startLocalRequest(id, req); err != nil {
			_ = w.completeLocalResult(id, localResult{Done: true, Error: err.Error()})
		}
		_ = os.Remove(filepath.Join(w.requestDir(), entry.Name()))
	}
	return nil
}

func (w *Worker) startLocalRequest(id string, req localRequest) error {
	destination := w.destinations[req.Chain]
	if destination == nil {
		return fmt.Errorf("destination %q is not configured on the validator node", req.Chain)
	}
	adapter, err := destination.AuthorizationAdapter()
	if err != nil {
		return err
	}
	if w.state == nil || !w.state.IsSynchronized() {
		return fmt.Errorf("interchain membership signing is paused while XGR node is synchronizing")
	}
	info, err := w.state.ValidatorInfo(w.localAddr)
	if err != nil {
		return err
	}
	if info == nil || !info.Exists {
		return fmt.Errorf("local validator is not registered in XGR PoS")
	}
	if req.Active && !info.Active {
		return fmt.Errorf("local validator is not active in XGR PoS")
	}
	localKey := w.localVerificationKey(adapter)
	if !validatorIdentityMatches(adapter, w.localAddr, info, localKey) {
		return fmt.Errorf("local signing key does not match XGR staking identity")
	}
	set, err := getMembershipValidatorSet(destination)
	if err != nil {
		return err
	}
	if _, err := validateAuthorizationSet(destination, set); err != nil {
		return err
	}
	currentIndex := indexOf(set.Validators, w.localAddr)
	currentActive := currentIndex >= 0
	currentIdentityMatches := currentActive && bytes.Equal(set.AuthorizationSet().PublicKeys[currentIndex], localKey)
	if (!req.Active && !currentActive) || (req.Active && currentIdentityMatches) {
		return w.completeLocalResult(id, localResult{Done: true, Validator: w.localAddr.String(), Active: req.Active, Changed: false, SetID: set.SetID})
	}
	if req.Active && currentActive && !currentIdentityMatches {
		return fmt.Errorf("destination still contains an old validator signing identity; wait for forced removal before re-activation")
	}
	action := protocol.ActionRemoveValidator
	if req.Active {
		action = protocol.ActionAddValidator
	}
	pendingKey := localKey
	if adapter.Scheme() == authorization.SchemeECDSA {
		pendingKey = nil
	}
	if w.attachLocalToPending(id, destination.Name, set.SetID, action, w.localAddr, pendingKey) {
		return nil
	}
	payload, err := w.membershipPayload(destination, adapter, set.SetID, action, w.localAddr, localKey)
	if err != nil {
		return err
	}
	if req.Active {
		proof, err := adapter.SignPossession(w.authorizationKeys(), payload)
		if err != nil {
			return fmt.Errorf("sign candidate possession: %w", err)
		}
		if err := evmInterchain.EnsureValidatorPossessionAuthorization(destination, w.txKey, payload, proof); err != nil {
			return fmt.Errorf("register candidate possession: %w", err)
		}
		latestSet, err := getMembershipValidatorSet(destination)
		if err != nil {
			return fmt.Errorf("recheck validator set after possession: %w", err)
		}
		if latestSet.SetID != payload.ExpectedSetID {
			return fmt.Errorf("validator set changed during possession registration: retry set-active")
		}
		payload.ValidUntil = uint64(time.Now().Add(time.Duration(destination.MembershipValiditySeconds) * time.Second).Unix())
	}
	rawPayload, err := adapter.EncodeMembership(payload)
	if err != nil {
		return err
	}
	if !w.state.IsSynchronized() {
		return fmt.Errorf("interchain membership signing lost XGR synchronization")
	}
	reqWire := wireRequest{Chain: destination.Name, Payload: rawPayload}
	if action == protocol.ActionRemoveValidator && !info.Active {
		reqWire.Forced = true
	} else {
		reqWire.CandidateSignature, err = adapter.SignMembership(w.authorizationKeys(), payload, time.Now())
		if err != nil {
			return err
		}
	}
	if err := w.acceptRequest(reqWire); err != nil {
		return err
	}
	hash := crypto.Keccak256Hash(reqWire.Payload)
	w.mu.Lock()
	if state := w.requests[hash]; state != nil {
		if state.localRequestIDs == nil {
			state.localRequestIDs = make(map[string]struct{})
		}
		state.localRequestIDs[id] = struct{}{}
	}
	w.mu.Unlock()
	if err := w.vote(reqWire); err != nil {
		return err
	}
	return w.publish(wireEnvelope{Type: "request", Req: &reqWire})
}

func (w *Worker) checkLocalReserve(destination *evmInterchain.Destination, details *evmInterchain.ValidatorDetails, minimum *big.Int) {
	if !details.Active {
		w.reserveWarned[destination.Name] = false
		return
	}
	if details.ReserveWei.Cmp(minimum) >= 0 {
		w.reserveWarned[destination.Name] = false
		return
	}
	if !w.reserveWarned[destination.Name] {
		w.logger.Warn(
			"interchain deactivation reserve below on-chain minimum",
			"chain", destination.Name,
			"validator", w.localAddr.String(),
			"reserveWei", details.ReserveWei.String(),
			"targetWei", minimum.String(),
		)
		w.reserveWarned[destination.Name] = true
	}
}

func (w *Worker) detectForcedRemovals(destination *evmInterchain.Destination, set *evmInterchain.ValidatorSet) error {
	adapter, err := validateAuthorizationSet(destination, set)
	if err != nil {
		return err
	}

	for idx, validator := range set.Validators {
		info, err := w.state.ValidatorInfo(validator)
		if err != nil {
			return err
		}
		key := set.AuthorizationSet().PublicKeys[idx]
		if info != nil && info.Active && validatorIdentityMatches(adapter, validator, info, key) {
			continue
		}
		if w.hasPendingMembership(destination.Name, set.SetID, protocol.ActionRemoveValidator, validator) {
			continue
		}
		payload, err := w.membershipPayload(destination, adapter, set.SetID, protocol.ActionRemoveValidator, validator, key)
		if err != nil {
			return err
		}
		raw, err := adapter.EncodeMembership(payload)
		if err != nil {
			return err
		}
		req := wireRequest{Chain: destination.Name, Payload: raw, Forced: true}
		if err := w.acceptRequest(req); err != nil {
			continue
		}
		_ = w.vote(req)
		_ = w.publish(wireEnvelope{Type: "request", Req: &req})
	}
	return nil
}

func (w *Worker) handleWire(raw []byte) error {
	return w.handleWireFrom(raw, "")
}
func (w *Worker) handleWireFrom(raw []byte, from peer.ID) error {
	if len(raw) > 128<<10 {
		return fmt.Errorf("oversized interchain gossip frame")
	}
	var envelope wireEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	switch envelope.Type {
	case "request":
		if envelope.Req == nil {
			return fmt.Errorf("missing request")
		}
		if err := w.acceptRequest(*envelope.Req); err != nil {
			return err
		}
		return w.vote(*envelope.Req)
	case "vote":
		if envelope.Vote == nil {
			return fmt.Errorf("missing vote")
		}
		return w.acceptVote(*envelope.Vote)
	case "checkpoint_vote":
		return fmt.Errorf("legacy v3.1.1 checkpoint votes are disabled in v3.1.3")
	case "iln_quorum_request":
		if envelope.ILNQuorumRequest == nil {
			return fmt.Errorf("missing ILN quorum request")
		}
		return w.acceptRequestedILNQuorum(*envelope.ILNQuorumRequest)
	case "iln_route_notice":
		if envelope.ILNRouteNotice == nil {
			return fmt.Errorf("missing ILN route notice")
		}
		if from == "" {
			return fmt.Errorf("ILN route notice requires authenticated peer")
		}
		return w.acceptVerifiedILNRouteNotice(from, *envelope.ILNRouteNotice)
	case "iln_checkpoint_vote":
		if envelope.ILNCheckpointVote == nil {
			return fmt.Errorf("missing ILN checkpoint vote")
		}
		return w.acceptILNCheckpointVote(*envelope.ILNCheckpointVote)
	case "iln_completed_quorum":
		if envelope.ILNCompletedQuorum == nil {
			return fmt.Errorf("missing completed Direct-BLS quorum")
		}
		if !w.allowILNCompletedQuorumFrom(from) {
			return fmt.Errorf("rate limited Direct-BLS completed quorum gossip")
		}
		return w.acceptILNCompletedQuorum(*envelope.ILNCompletedQuorum)
	case "governance_proposal":
		if envelope.GovernanceProposal == nil {
			return fmt.Errorf("missing governance proposal")
		}
		return w.acceptGovernanceProposal(*envelope.GovernanceProposal)
	case "governance_vote":
		if envelope.GovernanceVote == nil {
			return fmt.Errorf("missing governance vote")
		}
		return w.acceptGovernanceVote(*envelope.GovernanceVote)
	default:
		return fmt.Errorf("unsupported interchain gossip type %q", envelope.Type)
	}
}

func (w *Worker) acceptRequest(req wireRequest) error {
	destination := w.destinations[req.Chain]
	if destination == nil {
		return fmt.Errorf("destination %q is not configured", req.Chain)
	}

	adapter, err := destination.AuthorizationAdapter()
	if err != nil {
		return err
	}
	payload, err := adapter.DecodeMembership(req.Payload)
	if err != nil {
		return err
	}
	if adapter.Scheme() == authorization.SchemeECDSA && payload.Registry != types.StringToAddress(destination.RegistryAddress) {
		return fmt.Errorf("membership registry binding mismatch")
	}
	if payload.OriginChainID != w.state.OriginChainID() {
		return fmt.Errorf("origin chain id mismatch")
	}
	if payload.DestinationDomain != destination.Domain {
		return fmt.Errorf("destination domain mismatch")
	}
	now := uint64(time.Now().Unix())
	if payload.ValidUntil < now {
		return fmt.Errorf("interchain membership request expired")
	}
	if payload.ValidUntil > now+uint64(maximumMembershipLifetime/time.Second) {
		return fmt.Errorf("interchain membership request validity exceeds maximum lifetime")
	}

	set, err := getMembershipValidatorSet(destination)
	if err != nil {
		return err
	}
	if _, err := validateAuthorizationSet(destination, set); err != nil {
		return err
	}
	if set.SetID != payload.ExpectedSetID {
		return fmt.Errorf("stale request set id")
	}

	info, err := w.state.ValidatorInfo(payload.Validator)
	if err != nil {
		return err
	}
	switch payload.Action {
	case protocol.ActionAddValidator:
		if info == nil || !info.Exists || !info.Active {
			return fmt.Errorf("activation candidate is not active in XGR PoS")
		}
		if !validatorIdentityMatches(adapter, payload.Validator, info, membershipVerificationKey(adapter, payload)) {
			return fmt.Errorf("activation candidate BLS key mismatch")
		}
		if len(req.CandidateSignature) == 0 {
			return fmt.Errorf("activation request lacks candidate proof of key control")
		}
		if err := adapter.VerifyMembershipVote(candidateSet(adapter, payload), 0, payload, req.CandidateSignature); err != nil {
			return fmt.Errorf("invalid activation candidate signature: %w", err)
		}
	case protocol.ActionRemoveValidator:
		if len(set.Validators) <= 1 {
			return fmt.Errorf("refusing to remove the final interchain validator")
		}
		idx := indexOf(set.Validators, payload.Validator)
		if idx < 0 {
			return fmt.Errorf("removal target is not in current interchain set")
		}
		if !bytes.Equal(set.AuthorizationSet().PublicKeys[idx], membershipVerificationKey(adapter, payload)) {
			return fmt.Errorf("removal target BLS key differs from destination set")
		}
		xgrEligible := info != nil && info.Active && validatorIdentityMatches(adapter, payload.Validator, info, membershipVerificationKey(adapter, payload))
		if xgrEligible {
			if req.Forced {
				return fmt.Errorf("forced removal target remains eligible in XGR PoS")
			}
			if len(req.CandidateSignature) == 0 {
				return fmt.Errorf("voluntary removal lacks validator signature")
			}
			if err := adapter.VerifyMembershipVote(candidateSet(adapter, payload), 0, payload, req.CandidateSignature); err != nil {
				return fmt.Errorf("invalid voluntary removal signature: %w", err)
			}
		} else if !req.Forced {
			return fmt.Errorf("ineligible removal must be marked forced")
		}
	default:
		return fmt.Errorf("unsupported membership action")
	}

	hash := crypto.Keccak256Hash(req.Payload)
	w.mu.Lock()
	if _, exists := w.requests[hash]; !exists {
		w.requests[hash] = &requestState{
			request: req, payload: payload, firstSeen: time.Now(), lastBroadcast: time.Now(),
			localRequestIDs: make(map[string]struct{}), votes: make(map[types.Address][]byte),
		}
	}
	w.mu.Unlock()
	return nil
}

func (w *Worker) interchainSignerEligible(set *evmInterchain.ValidatorSet, validator types.Address) (bool, error) {
	if set == nil {
		return false, nil
	}
	idx := indexOf(set.Validators, validator)
	keys := set.AuthorizationSet().PublicKeys
	if idx < 0 {
		return false, nil
	}
	if idx >= len(keys) {
		return false, fmt.Errorf("validator set has no matching verification key")
	}
	scheme := set.Scheme
	if scheme == "" {
		scheme = authorization.SchemeBLS
	}
	adapter, err := authorization.ForScheme(scheme)
	if err != nil {
		return false, err
	}
	info, err := w.state.ValidatorInfo(validator)
	if err != nil {
		return false, err
	}
	return info != nil && info.Active && validatorIdentityMatches(adapter, validator, info, keys[idx]), nil
}

func (w *Worker) vote(req wireRequest) error {
	if w.state == nil || !w.state.IsSynchronized() {
		return nil
	}
	destination := w.destinations[req.Chain]
	adapter, err := destination.AuthorizationAdapter()
	if err != nil {
		return err
	}
	// Every signing entry reuses the same complete consent/eligibility checks;
	// a peer request or an expired in-memory record is never signing authority.
	if err := w.acceptRequest(req); err != nil {
		return err
	}
	payload, err := adapter.DecodeMembership(req.Payload)
	if err != nil {
		return err
	}
	set, err := getMembershipValidatorSet(destination)
	if err != nil {
		return err
	}
	if _, err := validateAuthorizationSet(destination, set); err != nil {
		return err
	}
	if set.SetID != payload.ExpectedSetID {
		return fmt.Errorf("membership set rotated before signing")
	}
	localIndex := indexOf(set.Validators, w.localAddr)
	if localIndex < 0 {
		return nil
	}
	eligible, err := w.interchainSignerEligible(set, w.localAddr)
	if err != nil {
		return err
	}
	if !eligible {
		return nil
	}
	if !bytes.Equal(set.AuthorizationSet().PublicKeys[localIndex], w.localVerificationKey(adapter)) {
		return fmt.Errorf("destination signing identity differs from local validator identity")
	}
	if !w.state.IsSynchronized() {
		return fmt.Errorf("membership signing lost XGR synchronization")
	}
	sig, err := adapter.SignMembership(w.authorizationKeys(), payload, time.Now())
	if err != nil {
		return err
	}
	vote := wireVote{Chain: req.Chain, Payload: req.Payload, Signer: w.localAddr, Signature: sig}
	if err := w.acceptVote(vote); err != nil {
		return err
	}
	return w.publish(wireEnvelope{Type: "vote", Vote: &vote})
}

func (w *Worker) acceptVote(vote wireVote) error {
	destination := w.destinations[vote.Chain]
	if destination == nil {
		return fmt.Errorf("unknown vote destination")
	}
	hash := crypto.Keccak256Hash(vote.Payload)

	w.mu.Lock()
	rs := w.requests[hash]
	w.mu.Unlock()
	if rs == nil {
		return nil
	}
	if rs.request.Chain != vote.Chain {
		return fmt.Errorf("membership vote destination differs from accepted request")
	}
	if !bytes.Equal(rs.request.Payload, vote.Payload) {
		return fmt.Errorf("vote payload mismatch")
	}

	set, err := getMembershipValidatorSet(destination)
	if err != nil {
		return err
	}
	index := indexOf(set.Validators, vote.Signer)
	if index < 0 {
		return fmt.Errorf("vote signer is not in current interchain set")
	}
	eligible, err := w.interchainSignerEligible(set, vote.Signer)
	if err != nil {
		return err
	}
	if !eligible {
		return fmt.Errorf("vote signer is not currently eligible for interchain signing")
	}
	adapter, err := validateAuthorizationSet(destination, set)
	if err != nil {
		return err
	}
	if rs.payload.ExpectedSetID != set.SetID || rs.payload.ValidUntil < uint64(time.Now().Unix()) {
		return fmt.Errorf("stale or expired membership vote")
	}
	if err := adapter.VerifyMembershipVote(set.AuthorizationSet(), index, rs.payload, vote.Signature); err != nil {
		return fmt.Errorf("invalid interchain vote: %w", err)
	}

	w.mu.Lock()
	rs.votes[vote.Signer] = append([]byte(nil), vote.Signature...)
	w.mu.Unlock()
	return nil
}

func (w *Worker) trySubmissions() {
	w.mu.Lock()
	hashes := make([]types.Hash, 0, len(w.requests))
	for hash := range w.requests {
		hashes = append(hashes, hash)
	}
	w.mu.Unlock()

	for _, hash := range hashes {
		if err := w.trySubmit(hash); err != nil {
			w.logger.Debug("interchain request not submitted", "hash", hash.String(), "err", err)
		}
	}
}

func (w *Worker) trySubmit(hash types.Hash) error {
	w.mu.Lock()
	rs := w.requests[hash]
	if rs == nil {
		w.mu.Unlock()
		return nil
	}
	request := rs.request
	payload := rs.payload
	firstSeen := rs.firstSeen
	votesByAddress := make(map[types.Address][]byte, len(rs.votes))
	for addr, sig := range rs.votes {
		votesByAddress[addr] = append([]byte(nil), sig...)
	}
	w.mu.Unlock()

	if payload.ValidUntil < uint64(time.Now().Unix()) {
		w.finishExpired(hash, request)
		return nil
	}
	if !w.state.IsSynchronized() {
		return nil
	}
	destination := w.destinations[request.Chain]
	set, err := getMembershipValidatorSet(destination)
	if err != nil {
		return err
	}
	if set.SetID != payload.ExpectedSetID {
		return w.finishStale(hash, request, payload, destination)
	}

	votes := make([]protocol.Vote, 0, len(votesByAddress))
	for addr, sig := range votesByAddress {
		idx := indexOf(set.Validators, addr)
		if idx < 0 {
			continue
		}
		eligible, err := w.interchainSignerEligible(set, addr)
		if err != nil {
			return err
		}
		if !eligible {
			continue
		}
		votes = append(votes, protocol.Vote{ValidatorIndex: idx, Signature: sig})
	}
	sort.Slice(votes, func(i, j int) bool { return votes[i].ValidatorIndex < votes[j].ValidatorIndex })
	adapter, err := validateAuthorizationSet(destination, set)
	if err != nil {
		return err
	}
	proof, err := adapter.AssembleMembershipQuorum(set.AuthorizationSet(), votes, payload)
	if err != nil {
		return err
	}

	info, err := w.state.ValidatorInfo(payload.Validator)
	if err != nil {
		return err
	}
	if payload.Action == protocol.ActionAddValidator {
		if info == nil || !info.Active || !validatorIdentityMatches(adapter, payload.Validator, info, membershipVerificationKey(adapter, payload)) {
			return fmt.Errorf("activation candidate lost XGR PoS eligibility before submission")
		}
		// The candidate funds its own activation reserve and destination gas.
		if w.localAddr != payload.Validator {
			return nil
		}
	} else {
		xgrEligible := info != nil && info.Active && validatorIdentityMatches(adapter, payload.Validator, info, membershipVerificationKey(adapter, payload))
		if request.Forced && xgrEligible {
			return fmt.Errorf("forced removal target regained XGR PoS eligibility before submission")
		}
		voluntary := xgrEligible
		rank, err := w.executorRank(set, payload.Validator, voluntary)
		if err != nil {
			return err
		}
		if rank < 0 {
			return nil
		}
		if time.Since(firstSeen) < time.Duration(rank)*time.Duration(destination.ExecutorStepDelaySeconds)*time.Second {
			return nil
		}
	}

	reserve := big.NewInt(0)
	if payload.Action == protocol.ActionAddValidator {
		reserve, err = evmInterchain.MinimumDeactivationReserve(destination)
		if err != nil {
			return fmt.Errorf("read on-chain activation reserve: %w", err)
		}
		balance, err := destinationBalance(destination, w.txKey.Address())
		if err != nil {
			return err
		}
		if balance.Cmp(reserve) < 0 {
			return fmt.Errorf("destination gas wallet balance %s is below activation reserve %s before gas", balance, reserve)
		}
	}

	result, err := evmInterchain.SubmitMembershipAuthorization(destination, w.txKey, payload, proof, reserve)
	if err != nil {
		return err
	}

	w.mu.Lock()
	localRequestIDs := make([]string, 0)
	if current := w.requests[hash]; current != nil {
		for id := range current.localRequestIDs {
			localRequestIDs = append(localRequestIDs, id)
		}
		delete(w.requests, hash)
	}
	w.mu.Unlock()
	for _, id := range localRequestIDs {
		_ = w.completeLocalResult(id, localResult{
			Done: true, Validator: payload.Validator.String(), Active: result.Active, Changed: true,
			TxHash: result.TxHash, SetID: result.SetID,
		})
	}
	return nil
}

func (w *Worker) executorRank(set *evmInterchain.ValidatorSet, target types.Address, preferTarget bool) (int, error) {
	if preferTarget && w.localAddr == target {
		return 0, nil
	}

	rank := 0
	if preferTarget {
		rank = 1
	}
	for _, validator := range set.Validators {
		if validator == target {
			continue
		}
		eligible, err := w.interchainSignerEligible(set, validator)
		if err != nil {
			return -1, err
		}
		if !eligible {
			continue
		}
		if validator == w.localAddr {
			return rank, nil
		}
		rank++
	}
	return -1, nil
}

func (w *Worker) finishStale(hash types.Hash, _ wireRequest, payload authorization.Membership, destination *evmInterchain.Destination) error {
	status, err := evmInterchain.GetValidatorStatusForDestination(destination, payload.Validator)
	if err != nil {
		return err
	}
	desiredReached := !status.Active && payload.Action == protocol.ActionRemoveValidator
	if payload.Action == protocol.ActionAddValidator && status.Active {
		details, detailErr := evmInterchain.GetValidator(destination, payload.Validator)
		if detailErr != nil {
			return detailErr
		}
		adapter, adapterErr := destination.AuthorizationAdapter()
		if adapterErr != nil {
			return adapterErr
		}
		key := details.VerificationKey
		if key == nil && adapter.Scheme() == authorization.SchemeBLS {
			key = details.BLSPublicKey
		}
		desiredReached = details.Active && bytes.Equal(key, membershipVerificationKey(adapter, payload))
	}

	w.mu.Lock()
	localRequestIDs := make([]string, 0)
	if current := w.requests[hash]; current != nil {
		for id := range current.localRequestIDs {
			localRequestIDs = append(localRequestIDs, id)
		}
		delete(w.requests, hash)
	}
	w.mu.Unlock()

	for _, id := range localRequestIDs {
		if desiredReached {
			_ = w.completeLocalResult(id, localResult{
				Done: true, Validator: payload.Validator.String(), Active: status.Active, Changed: true, SetID: status.SetID,
			})
		} else {
			_ = w.completeLocalResult(id, localResult{
				Done: true, Error: "request became stale because destination setId changed before the requested state was reached",
			})
		}
	}
	return nil
}

func (w *Worker) finishExpired(hash types.Hash, _ wireRequest) {
	w.mu.Lock()
	localRequestIDs := make([]string, 0)
	if current := w.requests[hash]; current != nil {
		for id := range current.localRequestIDs {
			localRequestIDs = append(localRequestIDs, id)
		}
		delete(w.requests, hash)
	}
	w.mu.Unlock()
	for _, id := range localRequestIDs {
		_ = w.completeLocalResult(id, localResult{Done: true, Error: "interchain membership request expired before submission"})
	}
}

func (w *Worker) rebroadcastPendingRequests() {
	now := time.Now()
	var pending []wireRequest
	w.mu.Lock()
	for _, state := range w.requests {
		if state.payload.ValidUntil < uint64(now.Unix()) {
			continue
		}
		if now.Sub(state.lastBroadcast) >= requestRebroadcastInterval {
			state.lastBroadcast = now
			pending = append(pending, state.request)
		}
	}
	w.mu.Unlock()
	for i := range pending {
		_ = w.publish(wireEnvelope{Type: "request", Req: &pending[i]})
	}
}

func (w *Worker) publish(envelope wireEnvelope) error {
	if w.topic == nil {
		return fmt.Errorf("interchain gossip topic is not started")
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return w.topic.Publish(&wrapperspb.BytesValue{Value: raw})
}

func (w *Worker) writeLocalResult(id string, result localResult) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	tmp := filepath.Join(w.resultDir(), id+".json.tmp")
	dst := filepath.Join(w.resultDir(), id+".json")
	if err := os.WriteFile(tmp, raw, 0o660); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func indexOf(values []types.Address, value types.Address) int {
	for i := range values {
		if values[i] == value {
			return i
		}
	}
	return -1
}

func destinationBalance(destination *evmInterchain.Destination, address ethgo.Address) (*big.Int, error) {
	return evmInterchain.GetDestinationGasBalance(destination, address)
}

func stringsTrim(value string) string {
	for len(value) > 0 && (value[0] == ' ' || value[0] == '\t' || value[0] == '\n' || value[0] == '\r') {
		value = value[1:]
	}
	for len(value) > 0 {
		n := len(value) - 1
		if value[n] != ' ' && value[n] != '\t' && value[n] != '\n' && value[n] != '\r' {
			break
		}
		value = value[:n]
	}
	return value
}
