package runtime

import (
	"bytes"
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
	"github.com/umbracle/ethgo/jsonrpc"
	ethgowallet "github.com/umbracle/ethgo/wallet"
	"google.golang.org/protobuf/types/known/wrapperspb"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	stakingcontract "github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
	"github.com/xgr-network/xgr-node/network"
	"github.com/xgr-network/xgr-node/secrets"
	"github.com/xgr-network/xgr-node/types"
)

const (
	TopicID                    = "/xgr/interchain/1.0.0"
	defaultPollInterval        = 2 * time.Second
	maximumMembershipLifetime  = 10 * time.Minute
	requestRebroadcastInterval = 10 * time.Second
)

type StateReader interface {
	OriginChainID() uint64
	ValidatorInfo(types.Address) (*stakingcontract.ValidatorInfo, error)
	OriginCheckpoint(types.Address, types.Address) (types.Hash, uint32, error)
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
	Type           string          `json:"type"`
	Req            *wireRequest    `json:"request,omitempty"`
	Vote           *wireVote       `json:"vote,omitempty"`
	CheckpointVote *checkpointVote `json:"checkpointVote,omitempty"`
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

type checkpointVote struct {
	Chain     string        `json:"chain"`
	Payload   []byte        `json:"payload"`
	Signer    types.Address `json:"signer"`
	Signature []byte        `json:"signature"`
}

type checkpointState struct {
	chain   string
	payload protocol.CheckpointPayload
	raw     []byte
	votes   map[types.Address][]byte
}

type checkpointAttestation struct {
	Version                      string `json:"version"`
	Chain                        string `json:"chain"`
	OriginChainID                uint64 `json:"originChainId"`
	DestinationDomain            uint32 `json:"destinationDomain"`
	SetID                        uint64 `json:"setId"`
	Mailbox                      string `json:"mailbox"`
	MerkleTreeHook               string `json:"merkleTreeHook"`
	Root                         string `json:"root"`
	Index                        uint32 `json:"index"`
	Payload                      string `json:"payload"`
	SignerBitmap                 string `json:"signerBitmap"`
	AggregateSignature           string `json:"aggregateSignature"`
	AggregateSignatureCompressed string `json:"aggregateSignatureCompressed"`
}

type requestState struct {
	request         wireRequest
	payload         protocol.MembershipPayload
	firstSeen       time.Time
	lastBroadcast   time.Time
	localRequestIDs map[string]struct{}
	votes           map[types.Address][]byte
}

type Worker struct {
	logger       hclog.Logger
	state        StateReader
	network      *network.Server
	secrets      secrets.SecretsManager
	dataDir         string
	originContracts *evmInterchain.OriginContracts
	destinations    map[string]*evmInterchain.Destination

	txKey       ethgo.Key
	localAddr   types.Address
	blsRaw      []byte
	blsPubKey   []byte

	topic   *network.Topic
	closeCh chan struct{}
	wg      sync.WaitGroup

	mu               sync.Mutex
	requests         map[types.Hash]*requestState
	reserveWarned    map[string]bool
	checkpointSigned        map[string]types.Hash
	checkpointLocalVotes    map[string]checkpointVote
	checkpointLastBroadcast map[string]time.Time
	checkpoints             map[types.Hash]*checkpointState
}

func New(
	logger hclog.Logger,
	state StateReader,
	networkServer *network.Server,
	secretsManager secrets.SecretsManager,
	dataDir string,
	originContracts *evmInterchain.OriginContracts,
	destinations []*evmInterchain.Destination,
) (*Worker, error) {
	if logger == nil {
		logger = hclog.NewNullLogger()
	}
	if state == nil || networkServer == nil || secretsManager == nil {
		return nil, fmt.Errorf("interchain worker requires state, network, and secrets manager")
	}
	if originContracts == nil || originContracts.Mailbox == types.ZeroAddress || originContracts.MerkleTreeHook == types.ZeroAddress {
		return nil, fmt.Errorf("interchain worker requires origin Hyperlane contracts")
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

	blsRaw, err := secretsManager.GetSecret(secrets.ValidatorBLSKey)
	if err != nil {
		return nil, fmt.Errorf("load validator BLS key: %w", err)
	}
	blsSecret, err := crypto.BytesToBLSSecretKey(blsRaw)
	if err != nil {
		return nil, fmt.Errorf("parse validator BLS key: %w", err)
	}
	blsPubKey, err := crypto.BLSSecretKeyToPubkeyBytes(blsSecret)
	if err != nil {
		return nil, fmt.Errorf("derive validator BLS public key: %w", err)
	}

	byName := make(map[string]*evmInterchain.Destination, len(destinations))
	for _, d := range destinations {
		if d == nil {
			continue
		}
		if err := d.ValidateMembershipRead(); err != nil {
			return nil, fmt.Errorf("destination %q: %w", d.Name, err)
		}
		byName[d.Name] = d
	}

	return &Worker{
		logger:       logger.Named("interchain"),
		state:        state,
		network:      networkServer,
		secrets:      secretsManager,
		dataDir:         dataDir,
		originContracts: originContracts,
		destinations:    byName,
		txKey:        txKey,
		localAddr:    types.Address(txKey.Address()),
		blsRaw:       blsRaw,
		blsPubKey:    blsPubKey,
		closeCh:       make(chan struct{}),
		requests:         make(map[types.Hash]*requestState),
		reserveWarned:    make(map[string]bool),
		checkpointSigned:        make(map[string]types.Hash),
		checkpointLocalVotes:    make(map[string]checkpointVote),
		checkpointLastBroadcast: make(map[string]time.Time),
		checkpoints:             make(map[types.Hash]*checkpointState),
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
	if err := topic.Subscribe(func(obj interface{}, _ peer.ID) {
		defer func() {
			if recovered := recover(); recovered != nil {
				w.logger.Error("interchain gossip panic isolated", "panic", recovered)
			}
		}()
		value, ok := obj.(*wrapperspb.BytesValue)
		if !ok || value == nil {
			return
		}
		if err := w.handleWire(value.Value); err != nil {
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
	if err := w.touchHeartbeat(); err != nil {
		return err
	}

	w.wg.Add(1)
	go w.loop()
	return nil
}

func (w *Worker) Close() {
	select {
	case <-w.closeCh:
	default:
		close(w.closeCh)
	}
	w.wg.Wait()
	_ = os.Remove(w.heartbeatPath())
	if w.topic != nil {
		w.topic.Close()
	}
}

func (w *Worker) loop() {
	defer w.wg.Done()
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

func (w *Worker) tick() {
	defer func() {
		if recovered := recover(); recovered != nil {
			w.logger.Error("interchain worker panic isolated", "panic", recovered)
		}
	}()
	if err := w.touchHeartbeat(); err != nil {
		w.logger.Warn("update interchain worker heartbeat failed", "err", err)
	}
	if !w.state.IsSynchronized() {
		w.logger.Debug("interchain signing paused while XGR node is synchronizing")
		return
	}
	if err := w.processLocalRequests(); err != nil {
		w.logger.Warn("process local interchain requests failed", "err", err)
	}
	if err := w.recoverLocalPending(); err != nil {
		w.logger.Warn("recover local interchain requests failed", "err", err)
	}
	for _, destination := range w.destinations {
		if err := w.detectForcedRemovals(destination); err != nil {
			w.logger.Warn("forced removal scan failed", "chain", destination.Name, "err", err)
		}
		if err := w.checkLocalReserve(destination); err != nil {
			w.logger.Warn("interchain reserve check failed", "chain", destination.Name, "err", err)
		}
		if err := w.signLatestCheckpoint(destination); err != nil {
			w.logger.Debug("interchain checkpoint not signed", "chain", destination.Name, "err", err)
		}
	}
	w.rebroadcastPendingRequests()
	w.trySubmissions()
}

func (w *Worker) requestDir() string { return filepath.Join(w.dataDir, "interchain", "requests") }
func (w *Worker) resultDir() string  { return filepath.Join(w.dataDir, "interchain", "results") }
func (w *Worker) pendingDir() string { return filepath.Join(w.dataDir, "interchain", "pending") }
func (w *Worker) pendingPath(id string) string { return filepath.Join(w.pendingDir(), id+".json") }
func (w *Worker) attestationDir() string { return filepath.Join(w.dataDir, "interchain", "attestations") }
func (w *Worker) heartbeatPath() string { return filepath.Join(w.dataDir, "interchain", "worker.heartbeat") }

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
			state.payload.SetID == setID &&
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
			state.payload.SetID == setID &&
			state.payload.Action == action &&
			state.payload.Validator == validator {
			return true
		}
	}
	return false
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

	info, err := w.state.ValidatorInfo(w.localAddr)
	if err != nil {
		return err
	}
	if !info.Exists {
		return fmt.Errorf("local validator is not registered in XGR PoS")
	}
	if req.Active && !info.Active {
		return fmt.Errorf("local validator is not active in XGR PoS")
	}
	if !bytes.Equal(info.BLSPubKey, w.blsPubKey) {
		return fmt.Errorf("local BLS key does not match XGR staking state")
	}

	set, err := evmInterchain.GetValidatorSet(destination)
	if err != nil {
		return err
	}
	currentIndex := indexOf(set.Validators, w.localAddr)
	currentActive := currentIndex >= 0
	currentIdentityMatches := currentActive && bytes.Equal(set.BLSPublicKeys[currentIndex], w.blsPubKey)
	if (!req.Active && !currentActive) || (req.Active && currentIdentityMatches) {
		return w.completeLocalResult(id, localResult{
			Done: true, Validator: w.localAddr.String(), Active: req.Active, Changed: false, SetID: set.SetID,
		})
	}
	if req.Active && currentActive && !currentIdentityMatches {
		return fmt.Errorf("destination still contains the validator with an old BLS key; wait for forced removal before re-activation")
	}

	action := protocol.ActionRemoveValidator
	if req.Active {
		action = protocol.ActionAddValidator
	}
	if w.attachLocalToPending(id, destination.Name, set.SetID, action, w.localAddr, w.blsPubKey) {
		return nil
	}
	eip2537Key, err := crypto.BLSPublicKeyToEIP2537(w.blsPubKey)
	if err != nil {
		return err
	}
	payload := protocol.MembershipPayload{
		OriginChainID:     w.state.OriginChainID(),
		DestinationDomain: destination.Domain,
		SetID:             set.SetID,
		ValidUntil:        uint64(time.Now().Add(time.Duration(destination.MembershipValiditySeconds) * time.Second).Unix()),
		Action:            action,
		Validator:           w.localAddr,
		BLSPublicKey:        w.blsPubKey,
		BLSPublicKeyEIP2537: eip2537Key,
	}
	rawPayload, err := payload.MarshalBinary()
	if err != nil {
		return err
	}
	blsKey, err := crypto.BytesToBLSSecretKey(w.blsRaw)
	if err != nil {
		return err
	}
	selfSig, err := crypto.SignByBLS(blsKey, rawPayload)
	if err != nil {
		return err
	}

	reqWire := wireRequest{
		Chain:              destination.Name,
		Payload:            rawPayload,
		CandidateSignature: selfSig,
	}
	if action == protocol.ActionRemoveValidator && !info.Active {
		reqWire.Forced = true
		reqWire.CandidateSignature = nil
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
	// Do not rely on libp2p self-delivery for the local validator's own vote.
	if err := w.vote(reqWire); err != nil {
		return err
	}
	return w.publish(wireEnvelope{Type: "request", Req: &reqWire})
}

func (w *Worker) checkLocalReserve(destination *evmInterchain.Destination) error {
	if destination == nil || destination.DeactivationReserveWei == nil {
		return nil
	}
	details, err := evmInterchain.GetValidator(destination, w.localAddr)
	if err != nil {
		return err
	}
	if !details.Active {
		w.reserveWarned[destination.Name] = false
		return nil
	}
	if details.ReserveWei.Cmp(destination.DeactivationReserveWei) >= 0 {
		w.reserveWarned[destination.Name] = false
		return nil
	}
	if !w.reserveWarned[destination.Name] {
		w.logger.Warn(
			"interchain deactivation reserve below configured target",
			"chain", destination.Name,
			"validator", w.localAddr.String(),
			"reserveWei", details.ReserveWei.String(),
			"targetWei", destination.DeactivationReserveWei.String(),
		)
		w.reserveWarned[destination.Name] = true
	}
	return nil
}

func (w *Worker) detectForcedRemovals(destination *evmInterchain.Destination) error {
	set, err := evmInterchain.GetValidatorSet(destination)
	if err != nil {
		return err
	}
	for idx, validator := range set.Validators {
		info, err := w.state.ValidatorInfo(validator)
		if err != nil {
			return err
		}
		if info.Exists && info.Active && bytes.Equal(info.BLSPubKey, set.BLSPublicKeys[idx]) {
			continue
		}
		if w.hasPendingMembership(destination.Name, set.SetID, protocol.ActionRemoveValidator, validator) {
			continue
		}
		eip2537Key, err := crypto.BLSPublicKeyToEIP2537(set.BLSPublicKeys[idx])
		if err != nil {
			return err
		}
		payload := protocol.MembershipPayload{
			OriginChainID:     w.state.OriginChainID(),
			DestinationDomain: destination.Domain,
			SetID:             set.SetID,
			ValidUntil:        uint64(time.Now().Add(time.Duration(destination.MembershipValiditySeconds) * time.Second).Unix()),
			Action:            protocol.ActionRemoveValidator,
			Validator:           validator,
			BLSPublicKey:        set.BLSPublicKeys[idx],
			BLSPublicKeyEIP2537: eip2537Key,
		}
		raw, err := payload.MarshalBinary()
		if err != nil {
			return err
		}
		req := wireRequest{Chain: destination.Name, Payload: raw, Forced: true}
		if err := w.acceptRequest(req); err != nil {
			continue
		}
		// Sign locally immediately; remote delivery is only for the other validators.
		_ = w.vote(req)
		_ = w.publish(wireEnvelope{Type: "request", Req: &req})
	}
	return nil
}

func (w *Worker) handleWire(raw []byte) error {
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
		if envelope.CheckpointVote == nil {
			return fmt.Errorf("missing checkpoint vote")
		}
		return w.acceptCheckpointVote(*envelope.CheckpointVote)
	default:
		return fmt.Errorf("unsupported interchain gossip type %q", envelope.Type)
	}
}

func (w *Worker) acceptRequest(req wireRequest) error {
	destination := w.destinations[req.Chain]
	if destination == nil {
		return fmt.Errorf("destination %q is not configured", req.Chain)
	}

	var payload protocol.MembershipPayload
	if err := payload.UnmarshalBinary(req.Payload); err != nil {
		return err
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

	set, err := evmInterchain.GetValidatorSet(destination)
	if err != nil {
		return err
	}
	if set.SetID != payload.SetID {
		return fmt.Errorf("stale request set id")
	}

	info, err := w.state.ValidatorInfo(payload.Validator)
	if err != nil {
		return err
	}
	switch payload.Action {
	case protocol.ActionAddValidator:
		if !info.Exists || !info.Active {
			return fmt.Errorf("activation candidate is not active in XGR PoS")
		}
		if !bytes.Equal(info.BLSPubKey, payload.BLSPublicKey) {
			return fmt.Errorf("activation candidate BLS key mismatch")
		}
		if len(req.CandidateSignature) == 0 {
			return fmt.Errorf("activation request lacks candidate proof of BLS key control")
		}
		if err := crypto.VerifyBLSSignatureFromBytes(payload.BLSPublicKey, req.CandidateSignature, req.Payload); err != nil {
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
		if !bytes.Equal(set.BLSPublicKeys[idx], payload.BLSPublicKey) {
			return fmt.Errorf("removal target BLS key differs from destination set")
		}
		xgrEligible := info.Exists && info.Active && bytes.Equal(info.BLSPubKey, payload.BLSPublicKey)
		if xgrEligible {
			if req.Forced {
				return fmt.Errorf("forced removal target remains eligible in XGR PoS")
			}
			if len(req.CandidateSignature) == 0 {
				return fmt.Errorf("voluntary removal lacks validator signature")
			}
			if err := crypto.VerifyBLSSignatureFromBytes(payload.BLSPublicKey, req.CandidateSignature, req.Payload); err != nil {
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
	if idx < 0 {
		return false, nil
	}
	info, err := w.state.ValidatorInfo(validator)
	if err != nil {
		return false, err
	}
	return info.Exists && info.Active && bytes.Equal(info.BLSPubKey, set.BLSPublicKeys[idx]), nil
}

func (w *Worker) vote(req wireRequest) error {
	if !w.state.IsSynchronized() {
		return nil
	}
	destination := w.destinations[req.Chain]
	set, err := evmInterchain.GetValidatorSet(destination)
	if err != nil {
		return err
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
	if !bytes.Equal(set.BLSPublicKeys[localIndex], w.blsPubKey) {
		return fmt.Errorf("destination BLS identity differs from local XGR BLS identity")
	}

	blsKey, err := crypto.BytesToBLSSecretKey(w.blsRaw)
	if err != nil {
		return err
	}
	sig, err := crypto.SignByBLS(blsKey, req.Payload)
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
	if !bytes.Equal(rs.request.Payload, vote.Payload) {
		return fmt.Errorf("vote payload mismatch")
	}

	set, err := evmInterchain.GetValidatorSet(destination)
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
	if err := crypto.VerifyBLSSignatureFromBytes(set.BLSPublicKeys[index], vote.Signature, vote.Payload); err != nil {
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
	set, err := evmInterchain.GetValidatorSet(destination)
	if err != nil {
		return err
	}
	if set.SetID != payload.SetID {
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
	bitmap, aggregate, err := protocol.AggregateVotes(set.BLSPublicKeys, votes, request.Payload)
	if err != nil {
		return err
	}

	info, err := w.state.ValidatorInfo(payload.Validator)
	if err != nil {
		return err
	}
	if payload.Action == protocol.ActionAddValidator {
		if !info.Exists || !info.Active || !bytes.Equal(info.BLSPubKey, payload.BLSPublicKey) {
			return fmt.Errorf("activation candidate lost XGR PoS eligibility before submission")
		}
		// The candidate funds its own activation reserve and destination gas.
		if w.localAddr != payload.Validator {
			return nil
		}
	} else {
		xgrEligible := info.Exists && info.Active && bytes.Equal(info.BLSPubKey, payload.BLSPublicKey)
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
		reserve = destination.DeactivationReserveWei
		if reserve == nil {
			return fmt.Errorf("activation reserve is not configured")
		}
		balance, err := destinationBalance(destination, w.txKey.Address())
		if err != nil {
			return err
		}
		if balance.Cmp(reserve) < 0 {
			return fmt.Errorf("destination gas wallet balance %s is below activation reserve %s before gas", balance, reserve)
		}
	}

	result, err := evmInterchain.SubmitMembership(destination, w.txKey, payload, bitmap, aggregate, reserve)
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

func (w *Worker) finishStale(hash types.Hash, _ wireRequest, payload protocol.MembershipPayload, destination *evmInterchain.Destination) error {
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
		desiredReached = details.Active && bytes.Equal(details.BLSPublicKey, payload.BLSPublicKey)
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

func (w *Worker) signLatestCheckpoint(destination *evmInterchain.Destination) error {
	set, err := evmInterchain.GetValidatorSet(destination)
	if err != nil {
		return err
	}
	eligible, err := w.interchainSignerEligible(set, w.localAddr)
	if err != nil {
		return err
	}
	if !eligible {
		return nil
	}

	root, index, err := w.originCheckpoint()
	if err != nil {
		return err
	}
	if root == types.ZeroHash {
		return nil
	}
	payload := protocol.CheckpointPayload{
		OriginChainID:     w.state.OriginChainID(),
		DestinationDomain: destination.Domain,
		SetID:             set.SetID,
		Mailbox:           w.originContracts.Mailbox,
		MerkleTreeHook:    w.originContracts.MerkleTreeHook,
		Root:              root,
		Index:             index,
	}
	raw, err := payload.MarshalBinary()
	if err != nil {
		return err
	}
	hash := crypto.Keccak256Hash(raw)

	w.mu.Lock()
	alreadySigned := w.checkpointSigned[destination.Name] == hash
	if alreadySigned {
		last := w.checkpointLastBroadcast[destination.Name]
		vote := w.checkpointLocalVotes[destination.Name]
		if time.Since(last) >= requestRebroadcastInterval && len(vote.Signature) != 0 {
			w.checkpointLastBroadcast[destination.Name] = time.Now()
			w.mu.Unlock()
			return w.publish(wireEnvelope{Type: "checkpoint_vote", CheckpointVote: &vote})
		}
		w.mu.Unlock()
		return nil
	}
	w.mu.Unlock()

	blsKey, err := crypto.BytesToBLSSecretKey(w.blsRaw)
	if err != nil {
		return err
	}
	sig, err := crypto.SignByBLS(blsKey, raw)
	if err != nil {
		return err
	}
	vote := checkpointVote{Chain: destination.Name, Payload: raw, Signer: w.localAddr, Signature: sig}
	if err := w.acceptCheckpointVote(vote); err != nil {
		return err
	}

	w.mu.Lock()
	w.checkpointSigned[destination.Name] = hash
	w.checkpointLocalVotes[destination.Name] = vote
	w.checkpointLastBroadcast[destination.Name] = time.Now()
	for existingHash, state := range w.checkpoints {
		if existingHash != hash && state.chain == destination.Name {
			delete(w.checkpoints, existingHash)
		}
	}
	w.mu.Unlock()

	return w.publish(wireEnvelope{Type: "checkpoint_vote", CheckpointVote: &vote})
}

func (w *Worker) acceptCheckpointVote(vote checkpointVote) error {
	destination := w.destinations[vote.Chain]
	if destination == nil {
		return fmt.Errorf("unknown checkpoint destination")
	}
	var payload protocol.CheckpointPayload
	if err := payload.UnmarshalBinary(vote.Payload); err != nil {
		return err
	}
	if payload.OriginChainID != w.state.OriginChainID() ||
		payload.DestinationDomain != destination.Domain ||
		payload.Mailbox != w.originContracts.Mailbox ||
		payload.MerkleTreeHook != w.originContracts.MerkleTreeHook {
		return fmt.Errorf("checkpoint context mismatch")
	}

	set, err := evmInterchain.GetValidatorSet(destination)
	if err != nil {
		return err
	}
	if payload.SetID != set.SetID {
		return fmt.Errorf("stale checkpoint validator set id")
	}
	idx := indexOf(set.Validators, vote.Signer)
	if idx < 0 {
		return fmt.Errorf("checkpoint signer is not in current interchain set")
	}
	eligible, err := w.interchainSignerEligible(set, vote.Signer)
	if err != nil {
		return err
	}
	if !eligible {
		return fmt.Errorf("checkpoint signer is not currently eligible")
	}
	if err := crypto.VerifyBLSSignatureFromBytes(set.BLSPublicKeys[idx], vote.Signature, vote.Payload); err != nil {
		return fmt.Errorf("invalid checkpoint vote: %w", err)
	}

	hash := crypto.Keccak256Hash(vote.Payload)
	w.mu.Lock()
	state := w.checkpoints[hash]
	if state == nil {
		state = &checkpointState{
			chain: vote.Chain, payload: payload, raw: append([]byte(nil), vote.Payload...),
			votes: make(map[types.Address][]byte),
		}
		w.checkpoints[hash] = state
	}
	state.votes[vote.Signer] = append([]byte(nil), vote.Signature...)
	w.mu.Unlock()

	return w.finalizeCheckpointAttestation(hash, destination, set)
}

func (w *Worker) finalizeCheckpointAttestation(
	hash types.Hash,
	destination *evmInterchain.Destination,
	set *evmInterchain.ValidatorSet,
) error {
	w.mu.Lock()
	state := w.checkpoints[hash]
	if state == nil {
		w.mu.Unlock()
		return nil
	}
	payload := state.payload
	raw := append([]byte(nil), state.raw...)
	votesByAddress := make(map[types.Address][]byte, len(state.votes))
	for addr, sig := range state.votes {
		votesByAddress[addr] = append([]byte(nil), sig...)
	}
	w.mu.Unlock()

	threshold, err := protocol.QuorumThreshold(len(set.Validators))
	if err != nil {
		return err
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
	if len(votes) < threshold {
		return nil
	}
	sort.Slice(votes, func(i, j int) bool { return votes[i].ValidatorIndex < votes[j].ValidatorIndex })
	bitmap, aggregate, err := protocol.AggregateVotes(set.BLSPublicKeys, votes, raw)
	if err != nil {
		return err
	}
	eipSignature, err := crypto.BLSSignatureToEIP2537(aggregate)
	if err != nil {
		return err
	}
	attestation := checkpointAttestation{
		Version: protocol.CheckpointDomainV1,
		Chain: destination.Name,
		OriginChainID: payload.OriginChainID,
		DestinationDomain: payload.DestinationDomain,
		SetID: payload.SetID,
		Mailbox: payload.Mailbox.String(),
		MerkleTreeHook: payload.MerkleTreeHook.String(),
		Root: payload.Root.String(),
		Index: payload.Index,
		Payload: "0x" + hex.EncodeToString(raw),
		SignerBitmap: "0x" + hex.EncodeToString(bitmap.Bytes()),
		AggregateSignature: "0x" + hex.EncodeToString(eipSignature),
		AggregateSignatureCompressed: "0x" + hex.EncodeToString(aggregate),
	}
	return w.writeCheckpointAttestation(attestation)
}

func (w *Worker) originCheckpoint() (types.Hash, uint32, error) {
	return w.state.OriginCheckpoint(w.originContracts.Mailbox, w.originContracts.MerkleTreeHook)
}

func (w *Worker) writeCheckpointAttestation(attestation checkpointAttestation) error {
	raw, err := json.Marshal(attestation)
	if err != nil {
		return err
	}
	chainDir := filepath.Join(w.attestationDir(), attestation.Chain)
	if err := os.MkdirAll(chainDir, 0o770); err != nil {
		return err
	}
	name := fmt.Sprintf("%d-%d-%s.json", attestation.SetID, attestation.Index, stringsTrim(attestation.Root))
	name = stringsTrim(name)
	tmp := filepath.Join(chainDir, "latest.json.tmp")
	latest := filepath.Join(chainDir, "latest.json")
	if err := os.WriteFile(tmp, raw, 0o660); err != nil {
		return err
	}
	if err := os.Rename(tmp, latest); err != nil {
		return err
	}
	archive := filepath.Join(chainDir, name)
	if err := os.WriteFile(archive+".tmp", raw, 0o660); err != nil {
		return err
	}
	return os.Rename(archive+".tmp", archive)
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
	if err := evmInterchain.VerifyDestinationChainID(destination); err != nil {
		return nil, err
	}
	client, err := jsonrpc.NewClient(destination.RPCURL)
	if err != nil {
		return nil, err
	}
	balance, err := client.Eth().GetBalance(address, ethgo.Latest)
	if err != nil {
		return nil, fmt.Errorf("query destination gas balance: %w", err)
	}
	return balance, nil
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