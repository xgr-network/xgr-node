package runtime

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
	"github.com/xgr-network/xgr-node/types"
)

type governanceProposalWire struct {
	Payload []byte `json:"payload"`
}

type governanceVote struct {
	Payload   []byte        `json:"payload"`
	Signer    types.Address `json:"signer"`
	Signature []byte        `json:"signature"`
}

type governanceState struct {
	proposal      protocol.ILNGovernanceProposal
	raw           []byte
	firstSeen     time.Time
	lastBroadcast time.Time
	localVote     *governanceVote
	votes         map[types.Address][]byte
}

type storedGovernanceProposal struct {
	ProposalID string `json:"proposalId"`
	Payload    string `json:"payload"`
}

type localGovernanceRequest struct {
	Action     string `json:"action"`
	Payload    []byte `json:"payload,omitempty"`
	ProposalID string `json:"proposalId,omitempty"`
}

type localGovernanceResult struct {
	Done       bool   `json:"done"`
	Error      string `json:"error,omitempty"`
	ProposalID string `json:"proposalId,omitempty"`
	Approved   bool   `json:"approved,omitempty"`
	TxHash     string `json:"txHash,omitempty"`
	Nonce      uint64 `json:"nonce,omitempty"`
	Quorum     bool   `json:"quorum,omitempty"`
}

type GovernanceProposalView struct {
	ProposalID        string
	Type              protocol.ILNProposalType
	SetID             uint64
	Nonce             uint64
	ValidUntil        uint64
	SourceChainID     uint64
	SourceDomain      uint32
	Registry          types.Address
	DestinationDomain uint32
	RouteID           types.Hash
	Gateway           types.Address
	SourceRouter      types.Address
	Mailbox           types.Address
	MerkleTreeHook    types.Address
	DestinationRouter types.Address
	ValidatorFeeWei   string
	Enabled           bool
	Payload           string
}

type governanceQuorum struct {
	Version                      string `json:"version"`
	ProposalID                   string `json:"proposalId"`
	ProposalType                 uint8  `json:"proposalType"`
	SourceChainID                uint64 `json:"sourceChainId"`
	SourceDomain                 uint32 `json:"sourceDomain"`
	Registry                     string `json:"registry"`
	DestinationDomain            uint32 `json:"destinationDomain"`
	RouteID                      string `json:"routeId"`
	ValidatorFeeWei              string `json:"validatorFeeWei"`
	SetID                        uint64 `json:"setId"`
	Nonce                        uint64 `json:"nonce"`
	ValidUntil                   uint64 `json:"validUntil"`
	Payload                      string `json:"payload"`
	SignerBitmap                 string `json:"signerBitmap"`
	AggregateSignature           string `json:"aggregateSignature"`
	AggregateSignatureCompressed string `json:"aggregateSignatureCompressed"`
}

var getGovernanceValidatorSet = evmInterchain.GetValidatorSet

// ProposeILNGovernance validates and gossips a governance proposal. Creating a
// proposal does not approve it and never creates a validator signature.
func (w *Worker) ProposeILNGovernance(proposal protocol.ILNGovernanceProposal) (types.Hash, error) {
	raw, err := proposal.MarshalBinary()
	if err != nil {
		return types.ZeroHash, err
	}
	wire := governanceProposalWire{Payload: raw}
	if err := w.acceptGovernanceProposal(wire); err != nil {
		return types.ZeroHash, err
	}
	id := crypto.Keccak256Hash(raw)
	if err := w.publish(wireEnvelope{Type: "governance_proposal", GovernanceProposal: &wire}); err != nil {
		return types.ZeroHash, err
	}
	return id, nil
}

// ApproveILNGovernance is the explicit local validator action that signs a
// previously received proposal. Proposal receipt alone never calls this method.
func (w *Worker) ApproveILNGovernance(id types.Hash) error {
	vote, err := w.createGovernanceVote(id)
	if err != nil {
		return err
	}
	if vote == nil {
		return nil
	}
	if err := w.acceptGovernanceVote(*vote); err != nil {
		return err
	}

	w.mu.Lock()
	if state := w.governance[id]; state != nil {
		copyVote := *vote
		copyVote.Payload = append([]byte(nil), vote.Payload...)
		copyVote.Signature = append([]byte(nil), vote.Signature...)
		state.localVote = &copyVote
		state.lastBroadcast = time.Now()
	}
	w.mu.Unlock()

	return w.publish(wireEnvelope{Type: "governance_vote", GovernanceVote: vote})
}

func (w *Worker) createGovernanceVote(id types.Hash) (*governanceVote, error) {
	if !w.state.IsSynchronized() {
		return nil, fmt.Errorf("interchain governance signing is paused while XGR node is synchronizing")
	}

	w.mu.Lock()
	state := w.governance[id]
	if state == nil {
		w.mu.Unlock()
		return nil, fmt.Errorf("ILN governance proposal %s is unknown", id.String())
	}
	proposal := state.proposal
	raw := append([]byte(nil), state.raw...)
	if state.localVote != nil {
		vote := *state.localVote
		vote.Payload = append([]byte(nil), state.localVote.Payload...)
		vote.Signature = append([]byte(nil), state.localVote.Signature...)
		w.mu.Unlock()
		return &vote, nil
	}
	w.mu.Unlock()

	if proposal.ValidUntil < uint64(time.Now().Unix()) {
		return nil, fmt.Errorf("ILN governance proposal expired")
	}
	source, err := w.governanceSource(proposal)
	if err != nil {
		return nil, err
	}
	if _, err := w.governanceDestination(proposal); err != nil {
		return nil, err
	}
	set, err := getGovernanceValidatorSet(source)
	if err != nil {
		return nil, err
	}
	if set.SetID != proposal.SetID {
		return nil, fmt.Errorf("stale ILN governance proposal set id")
	}

	index := indexOf(set.Validators, w.localAddr)
	if index < 0 {
		return nil, fmt.Errorf("local validator is not in the proposal interchain set")
	}
	eligible, err := w.interchainSignerEligible(set, w.localAddr)
	if err != nil {
		return nil, err
	}
	if !eligible {
		return nil, fmt.Errorf("local validator is not eligible for interchain governance signing")
	}
	if !bytes.Equal(set.BLSPublicKeys[index], w.blsPubKey) {
		return nil, fmt.Errorf("governance validator BLS identity differs from local XGR BLS identity")
	}

	blsKey, err := crypto.BytesToBLSSecretKey(w.blsRaw)
	if err != nil {
		return nil, err
	}
	signature, err := crypto.SignByBLS(blsKey, raw)
	if err != nil {
		return nil, err
	}

	return &governanceVote{
		Payload:   raw,
		Signer:    w.localAddr,
		Signature: signature,
	}, nil
}

func (w *Worker) acceptGovernanceProposal(wire governanceProposalWire) error {
	var proposal protocol.ILNGovernanceProposal
	if err := proposal.UnmarshalBinary(wire.Payload); err != nil {
		return err
	}
	if proposal.ValidUntil < uint64(time.Now().Unix()) {
		return fmt.Errorf("ILN governance proposal expired")
	}
	source, err := w.governanceSource(proposal)
	if err != nil {
		return err
	}
	if _, err := w.governanceDestination(proposal); err != nil {
		return err
	}
	set, err := getGovernanceValidatorSet(source)
	if err != nil {
		return err
	}
	if set.SetID != proposal.SetID {
		return fmt.Errorf("stale ILN governance proposal set id")
	}

	id := crypto.Keccak256Hash(wire.Payload)
	w.mu.Lock()
	if existing := w.governance[id]; existing != nil {
		w.mu.Unlock()
		if !bytes.Equal(existing.raw, wire.Payload) {
			return fmt.Errorf("ILN governance proposal id collision")
		}
		return nil
	}

	w.governance[id] = &governanceState{
		proposal:      proposal,
		raw:           append([]byte(nil), wire.Payload...),
		firstSeen:     time.Now(),
		lastBroadcast: time.Now(),
		votes:         make(map[types.Address][]byte),
	}
	w.mu.Unlock()

	return w.persistGovernanceProposal(id, wire.Payload)
}

func (w *Worker) acceptGovernanceVote(vote governanceVote) error {
	id := crypto.Keccak256Hash(vote.Payload)

	w.mu.Lock()
	state := w.governance[id]
	if state == nil {
		w.mu.Unlock()
		// Votes can race ahead of their proposal. The proposal rebroadcast will
		// allow a later rebroadcast vote to be accepted without treating gossip
		// ordering as a protocol error.
		return nil
	}
	proposal := state.proposal
	raw := append([]byte(nil), state.raw...)
	w.mu.Unlock()

	if !bytes.Equal(raw, vote.Payload) {
		return fmt.Errorf("ILN governance vote payload mismatch")
	}
	if proposal.ValidUntil < uint64(time.Now().Unix()) {
		return fmt.Errorf("ILN governance proposal expired")
	}
	source, err := w.governanceSource(proposal)
	if err != nil {
		return err
	}
	if _, err := w.governanceDestination(proposal); err != nil {
		return err
	}
	set, err := getGovernanceValidatorSet(source)
	if err != nil {
		return err
	}
	if set.SetID != proposal.SetID {
		return fmt.Errorf("stale ILN governance proposal set id")
	}

	index := indexOf(set.Validators, vote.Signer)
	if index < 0 {
		return fmt.Errorf("ILN governance signer is not in current interchain set")
	}
	eligible, err := w.interchainSignerEligible(set, vote.Signer)
	if err != nil {
		return err
	}
	if !eligible {
		return fmt.Errorf("ILN governance signer is not currently eligible")
	}
	if err := crypto.VerifyBLSSignatureFromBytes(set.BLSPublicKeys[index], vote.Signature, vote.Payload); err != nil {
		return fmt.Errorf("invalid ILN governance vote: %w", err)
	}

	w.mu.Lock()
	if current := w.governance[id]; current != nil {
		current.votes[vote.Signer] = append([]byte(nil), vote.Signature...)
	}
	w.mu.Unlock()

	return w.finalizeGovernanceQuorum(id, set)
}

func (w *Worker) finalizeGovernanceQuorum(
	id types.Hash,
	set *evmInterchain.ValidatorSet,
) error {
	w.mu.Lock()
	state := w.governance[id]
	if state == nil {
		w.mu.Unlock()
		return nil
	}
	proposal := state.proposal
	raw := append([]byte(nil), state.raw...)
	votesByAddress := make(map[types.Address][]byte, len(state.votes))
	for validator, sig := range state.votes {
		votesByAddress[validator] = append([]byte(nil), sig...)
	}
	w.mu.Unlock()

	threshold, err := protocol.QuorumThreshold(len(set.Validators))
	if err != nil {
		return err
	}

	votes := make([]protocol.Vote, 0, len(votesByAddress))
	for validator, signature := range votesByAddress {
		index := indexOf(set.Validators, validator)
		if index < 0 {
			continue
		}
		eligible, err := w.interchainSignerEligible(set, validator)
		if err != nil {
			return err
		}
		if !eligible {
			continue
		}
		votes = append(votes, protocol.Vote{ValidatorIndex: index, Signature: signature})
	}
	if len(votes) < threshold {
		return nil
	}

	sort.Slice(votes, func(i, j int) bool {
		return votes[i].ValidatorIndex < votes[j].ValidatorIndex
	})
	bitmap, aggregate, err := protocol.AggregateVotes(set.BLSPublicKeys, votes, raw)
	if err != nil {
		return err
	}
	eipSignature, err := crypto.BLSSignatureToEIP2537(aggregate)
	if err != nil {
		return err
	}

	quorum := governanceQuorum{
		Version:                      protocol.ILNGovernanceDomainV1,
		ProposalID:                   id.String(),
		ProposalType:                 uint8(proposal.Type),
		SourceChainID:                proposal.Route.Key.SourceChainID,
		SourceDomain:                 proposal.Route.Key.SourceDomain,
		Registry:                     proposal.Registry.String(),
		DestinationDomain:            proposal.Route.Key.DestinationDomain,
		RouteID:                      proposal.Route.Key.RouteID.String(),
		ValidatorFeeWei:              governanceFeeString(proposal),
		SetID:                        proposal.SetID,
		Nonce:                        proposal.Nonce,
		ValidUntil:                   proposal.ValidUntil,
		Payload:                      "0x" + hex.EncodeToString(raw),
		SignerBitmap:                 "0x" + hex.EncodeToString(bitmap.Bytes()),
		AggregateSignature:           "0x" + hex.EncodeToString(eipSignature),
		AggregateSignatureCompressed: "0x" + hex.EncodeToString(aggregate),
	}
	return w.writeGovernanceQuorum(quorum)
}

func governanceFeeString(proposal protocol.ILNGovernanceProposal) string {
	if proposal.Route.ValidatorFeeWei == nil {
		return "0"
	}
	return proposal.Route.ValidatorFeeWei.String()
}

func (w *Worker) governanceSource(
	proposal protocol.ILNGovernanceProposal,
) (*evmInterchain.Destination, error) {
	for _, network := range w.destinations {
		if network == nil {
			continue
		}
		if network.ChainID == proposal.Route.Key.SourceChainID &&
			network.Domain == proposal.Route.Key.SourceDomain {
			if stringsTrim(network.ILNRegistryAddress) == "" {
				return nil, fmt.Errorf("ILN governance source network %q has no ILN registry", network.Name)
			}
			if types.StringToAddress(network.ILNRegistryAddress) != proposal.Registry {
				return nil, fmt.Errorf("ILN governance registry does not match configured source registry")
			}
			return network, nil
		}
	}
	return nil, fmt.Errorf(
		"ILN governance source chain/domain %d/%d is not configured",
		proposal.Route.Key.SourceChainID,
		proposal.Route.Key.SourceDomain,
	)
}

func (w *Worker) governanceDestination(
	proposal protocol.ILNGovernanceProposal,
) (*evmInterchain.Destination, error) {
	for _, destination := range w.destinations {
		if destination != nil && destination.Domain == proposal.Route.Key.DestinationDomain {
			return destination, nil
		}
	}
	return nil, fmt.Errorf(
		"ILN governance destination domain %d is not configured",
		proposal.Route.Key.DestinationDomain,
	)
}

func (w *Worker) governanceDir() string {
	return filepath.Join(w.dataDir, "interchain", "governance")
}

func (w *Worker) governanceProposalDir() string {
	return filepath.Join(w.governanceDir(), "proposals")
}

func (w *Worker) governanceRequestDir() string {
	return filepath.Join(w.governanceDir(), "requests")
}

func (w *Worker) governanceResultDir() string {
	return filepath.Join(w.governanceDir(), "results")
}

func (w *Worker) governanceQuorumDir() string {
	return filepath.Join(w.governanceDir(), "quorums")
}

func (w *Worker) persistGovernanceProposal(id types.Hash, raw []byte) error {
	if len(raw) == 0 {
		return fmt.Errorf("ILN governance proposal payload is empty")
	}
	if err := os.MkdirAll(w.governanceProposalDir(), 0o770); err != nil {
		return err
	}
	stored := storedGovernanceProposal{
		ProposalID: id.String(),
		Payload:    "0x" + hex.EncodeToString(raw),
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	path := filepath.Join(w.governanceProposalDir(), id.String()+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0o660); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (w *Worker) recoverGovernanceProposals() error {
	entries, err := os.ReadDir(w.governanceProposalDir())
	if err != nil {
		return err
	}
	now := uint64(time.Now().Unix())
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(w.governanceProposalDir(), entry.Name())
		stored, proposal, raw, err := readStoredGovernanceProposal(path)
		if err != nil {
			w.logger.Warn("invalid persisted ILN governance proposal", "path", path, "err", err)
			continue
		}
		if proposal.ValidUntil < now {
			_ = os.Remove(path)
			continue
		}
		id := crypto.Keccak256Hash(raw)
		if stored.ProposalID != id.String() {
			w.logger.Warn("persisted ILN governance proposal id mismatch", "path", path)
			continue
		}
		if err := w.acceptGovernanceProposal(governanceProposalWire{Payload: raw}); err != nil {
			w.logger.Debug("persisted ILN governance proposal not recovered", "proposal", id.String(), "err", err)
		}
	}
	return nil
}

func readStoredGovernanceProposal(path string) (storedGovernanceProposal, protocol.ILNGovernanceProposal, []byte, error) {
	var stored storedGovernanceProposal
	var proposal protocol.ILNGovernanceProposal

	rawFile, err := os.ReadFile(path)
	if err != nil {
		return stored, proposal, nil, err
	}
	if err := json.Unmarshal(rawFile, &stored); err != nil {
		return stored, proposal, nil, err
	}
	payloadHex := stringsTrim(stored.Payload)
	if len(payloadHex) < 3 || len(payloadHex)%2 != 0 || payloadHex[:2] != "0x" {
		return stored, proposal, nil, fmt.Errorf("invalid stored ILN governance payload")
	}
	raw, err := hex.DecodeString(payloadHex[2:])
	if err != nil {
		return stored, proposal, nil, fmt.Errorf("decode stored ILN governance payload: %w", err)
	}
	if err := proposal.UnmarshalBinary(raw); err != nil {
		return stored, proposal, nil, err
	}
	return stored, proposal, raw, nil
}

func ReadGovernanceProposal(dataDir, proposalID string) (*GovernanceProposalView, error) {
	id, err := parseGovernanceProposalID(proposalID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "interchain", "governance", "proposals", id.String()+".json")
	stored, proposal, raw, err := readStoredGovernanceProposal(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("ILN governance proposal not found")
		}
		return nil, err
	}
	if stored.ProposalID != id.String() || crypto.Keccak256Hash(raw) != id {
		return nil, fmt.Errorf("ILN governance proposal id mismatch")
	}
	fee := "0"
	if proposal.Route.ValidatorFeeWei != nil {
		fee = proposal.Route.ValidatorFeeWei.String()
	}
	return &GovernanceProposalView{
		ProposalID:        id.String(),
		Type:              proposal.Type,
		SetID:             proposal.SetID,
		Nonce:             proposal.Nonce,
		ValidUntil:        proposal.ValidUntil,
		SourceChainID:     proposal.Route.Key.SourceChainID,
		SourceDomain:      proposal.Route.Key.SourceDomain,
		Registry:          proposal.Registry,
		DestinationDomain: proposal.Route.Key.DestinationDomain,
		RouteID:           proposal.Route.Key.RouteID,
		Gateway:           proposal.Route.Gateway,
		SourceRouter:      proposal.Route.SourceRouter,
		Mailbox:           proposal.Route.Mailbox,
		MerkleTreeHook:    proposal.Route.MerkleTreeHook,
		DestinationRouter: proposal.Route.DestinationRouter,
		ValidatorFeeWei:   fee,
		Enabled:           proposal.Route.Enabled,
		Payload:           "0x" + hex.EncodeToString(raw),
	}, nil
}

func parseGovernanceProposalID(value string) (types.Hash, error) {
	value = stringsTrim(value)
	if len(value) != 66 || value[:2] != "0x" {
		return types.ZeroHash, fmt.Errorf("ILN governance proposal id must be a 32-byte 0x-prefixed hash")
	}
	raw, err := hex.DecodeString(value[2:])
	if err != nil || len(raw) != types.HashLength {
		return types.ZeroHash, fmt.Errorf("ILN governance proposal id must be hexadecimal")
	}
	return types.BytesToHash(raw), nil
}

func (w *Worker) writeGovernanceQuorum(quorum governanceQuorum) error {
	if stringsTrim(quorum.ProposalID) == "" {
		return fmt.Errorf("ILN governance quorum proposal id is required")
	}
	raw, err := json.Marshal(quorum)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(w.governanceQuorumDir(), 0o770); err != nil {
		return err
	}
	path := filepath.Join(w.governanceQuorumDir(), quorum.ProposalID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o660); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (w *Worker) rebroadcastGovernance() {
	now := time.Now()
	var proposals []governanceProposalWire
	var votes []governanceVote
	var expired []types.Hash

	w.mu.Lock()
	for id, state := range w.governance {
		if state.proposal.ValidUntil < uint64(now.Unix()) {
			expired = append(expired, id)
			continue
		}
		if now.Sub(state.lastBroadcast) < requestRebroadcastInterval {
			continue
		}
		state.lastBroadcast = now
		proposals = append(proposals, governanceProposalWire{
			Payload: append([]byte(nil), state.raw...),
		})
		if state.localVote != nil {
			votes = append(votes, governanceVote{
				Payload:   append([]byte(nil), state.localVote.Payload...),
				Signer:    state.localVote.Signer,
				Signature: append([]byte(nil), state.localVote.Signature...),
			})
		}
	}
	for _, id := range expired {
		delete(w.governance, id)
	}
	w.mu.Unlock()

	for i := range proposals {
		_ = w.publish(wireEnvelope{Type: "governance_proposal", GovernanceProposal: &proposals[i]})
	}
	for i := range votes {
		_ = w.publish(wireEnvelope{Type: "governance_vote", GovernanceVote: &votes[i]})
	}
}


func (w *Worker) processLocalGovernanceRequests() error {
	entries, err := os.ReadDir(w.governanceRequestDir())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-len(".json")]
		path := filepath.Join(w.governanceRequestDir(), entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var req localGovernanceRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			_ = w.writeLocalGovernanceResult(id, localGovernanceResult{Done: true, Error: err.Error()})
			_ = os.Remove(path)
			continue
		}

		result := localGovernanceResult{Done: true}
		switch req.Action {
		case "create":
			var proposal protocol.ILNGovernanceProposal
			if err := proposal.UnmarshalBinary(req.Payload); err != nil {
				result.Error = err.Error()
				break
			}
			proposalID, err := w.ProposeILNGovernance(proposal)
			if err != nil {
				result.Error = err.Error()
				break
			}
			result.ProposalID = proposalID.String()
			_, statErr := os.Stat(filepath.Join(w.governanceQuorumDir(), proposalID.String()+".json"))
			result.Quorum = statErr == nil
		case "approve":
			proposalID, err := parseGovernanceProposalID(req.ProposalID)
			if err != nil {
				result.Error = err.Error()
				break
			}
			if err := w.ApproveILNGovernance(proposalID); err != nil {
				result.Error = err.Error()
				break
			}
			result.ProposalID = proposalID.String()
			result.Approved = true
			_, statErr := os.Stat(filepath.Join(w.governanceQuorumDir(), proposalID.String()+".json"))
			result.Quorum = statErr == nil
		case "execute":
            // Offload any EVM submission and confirmation waiting: holding the
            // 2s validator tick while a remote RPC waits would starve quorum
            // signing, route discovery, expiry processing and heartbeat.
            if w.governanceExecuteCh == nil {
                result.Error = "ILN governance executor not running"
                break
            }
            w.mu.Lock()
            _,inFlight := w.governanceExecuteInFlight[id]
            if !inFlight { w.governanceExecuteInFlight[id]=struct{}{} }
            w.mu.Unlock()
            if inFlight { continue }
            select {
            case w.governanceExecuteCh <- governanceExecuteRequest{id:id,proposalID:req.ProposalID}:
                // Durable JSON request is deleted only after executor commits
                // its result. Do not acknowledge execution here.
                continue
            default:
                w.mu.Lock()
                delete(w.governanceExecuteInFlight,id)
                w.mu.Unlock()
                continue
            }
		default:
			result.Error = fmt.Sprintf("unsupported ILN governance local action %q", req.Action)
		}
		_ = w.writeLocalGovernanceResult(id, result)
		_ = os.Remove(path)
	}
	return nil
}

func (w *Worker) writeLocalGovernanceResult(id string, result localGovernanceResult) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	tmp := filepath.Join(w.governanceResultDir(), id+".json.tmp")
	dst := filepath.Join(w.governanceResultDir(), id+".json")
	if err := os.WriteFile(tmp, raw, 0o660); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
