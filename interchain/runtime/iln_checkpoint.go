package runtime

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
	"github.com/xgr-network/xgr-node/types"
)

const ilnOperationScanChunk = uint64(1000)

type ilnCheckpointVote struct {
	Route     string        `json:"route"`
	Payload   []byte        `json:"payload"`
	Signer    types.Address `json:"signer"`
	Signature []byte        `json:"signature"`
}

type ilnCheckpointState struct {
	route   string
	payload protocol.ILNCheckpointPayload
	raw     []byte
	votes   map[types.Address][]byte
}

type ilnLocalVoteState struct {
	vote          ilnCheckpointVote
	lastBroadcast time.Time
}

type ilnCheckpointAttestation struct {
	Version                      string `json:"version"`
	Chain                        string `json:"chain"`
	Destination                  string `json:"destination"`
	OriginChainID                uint64 `json:"originChainId"`
	OriginDomain                 uint32 `json:"originDomain"`
	DestinationDomain            uint32 `json:"destinationDomain"`
	RouteID                      string `json:"routeId"`
	SetID                        uint64 `json:"setId"`
	SourceBlockNumber            uint64 `json:"sourceBlockNumber"`
	Registry                     string `json:"registry"`
	Gateway                      string `json:"gateway"`
	SourceRouter                 string `json:"sourceRouter"`
	Mailbox                      string `json:"mailbox"`
	MerkleTreeHook               string `json:"merkleTreeHook"`
	DestinationRouter            string `json:"destinationRouter"`
	ValidatorFeeWei              string `json:"validatorFeeWei"`
	AuthorizedMessageID          string `json:"authorizedMessageId"`
	Root                         string `json:"root"`
	Index                        uint32 `json:"index"`
	Payload                      string `json:"payload"`
	SignerBitmap                 string `json:"signerBitmap"`
	AggregateSignature           string `json:"aggregateSignature"`
	AggregateSignatureCompressed string `json:"aggregateSignatureCompressed"`
}

var (
	getConfirmedILNRoute          = evmInterchain.GetConfirmedILNRoute
	getILNRouteAtBlock            = evmInterchain.GetILNRouteAtBlock
	getConfirmedILNCheckpoint     = evmInterchain.GetConfirmedILNCheckpoint
	getILNValidatorSet            = evmInterchain.GetValidatorSet
	getConfirmedILNHead           = evmInterchain.GetConfirmedILNHead
	getILNGatewayActivationBlock  = evmInterchain.GetILNGatewayActivationBlock
	getConfirmedILNOperations     = evmInterchain.GetConfirmedILNOperations
	getILNOperationAtBlock        = evmInterchain.GetILNOperationAtBlock
)

func (w *Worker) ilnDir() string {
	return filepath.Join(w.dataDir, "interchain", "iln")
}

func (w *Worker) ilnCursorDir() string {
	return filepath.Join(w.ilnDir(), "cursors")
}

func (w *Worker) ilnVoteDir() string {
	return filepath.Join(w.ilnDir(), "local-votes")
}

func (w *Worker) ilnCursorPath(route string) string {
	return filepath.Join(w.ilnCursorDir(), route+".cursor")
}

func (w *Worker) readILNCursor(route string) (uint64, bool, error) {
	raw, err := os.ReadFile(w.ilnCursorPath(route))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	value, err := strconv.ParseUint(stringsTrim(string(raw)), 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("decode ILN cursor for %s: %w", route, err)
	}
	return value, true, nil
}

func (w *Worker) writeILNCursor(route string, block uint64) error {
	if err := os.MkdirAll(w.ilnCursorDir(), 0o770); err != nil {
		return err
	}
	path := w.ilnCursorPath(route)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.FormatUint(block, 10)+"\n"), 0o660); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (w *Worker) persistILNLocalVote(hash types.Hash, vote ilnCheckpointVote) error {
	if err := os.MkdirAll(w.ilnVoteDir(), 0o770); err != nil {
		return err
	}
	raw, err := json.Marshal(vote)
	if err != nil {
		return err
	}
	path := filepath.Join(w.ilnVoteDir(), hash.String()+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o660); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (w *Worker) recoverILNLocalVotes() error {
	entries, err := os.ReadDir(w.ilnVoteDir())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(w.ilnVoteDir(), entry.Name()))
		if err != nil {
			continue
		}
		var vote ilnCheckpointVote
		if err := json.Unmarshal(raw, &vote); err != nil {
			w.logger.Warn("invalid persisted ILN local vote", "file", entry.Name(), "err", err)
			continue
		}
		hash := crypto.Keccak256Hash(vote.Payload)
		if entry.Name() != hash.String()+".json" {
			w.logger.Warn("persisted ILN local vote hash mismatch", "file", entry.Name())
			continue
		}
		w.ilnLocalVotes[hash] = &ilnLocalVoteState{vote: vote}
	}
	return nil
}

func (w *Worker) removeILNLocalVote(hash types.Hash) {
	w.mu.Lock()
	delete(w.ilnLocalVotes, hash)
	delete(w.ilnCheckpoints, hash)
	w.mu.Unlock()
	_ = os.Remove(filepath.Join(w.ilnVoteDir(), hash.String()+".json"))
}

func (w *Worker) signLatestILNCheckpoint(route *evmInterchain.CheckpointRoute) error {
	if route == nil || stringsTrim(route.SourceNetwork) == "" {
		return fmt.Errorf("ILN route source network is required")
	}
	source := w.destinations[route.SourceNetwork]
	destination := w.destinations[route.Destination]
	if source == nil || destination == nil {
		return fmt.Errorf("ILN route networks are not configured")
	}

	set, err := getILNValidatorSet(destination)
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

	confirmedHead, err := getConfirmedILNHead(source)
	if err != nil {
		return err
	}
	cursor, exists, err := w.readILNCursor(route.Name)
	if err != nil {
		return err
	}
	if !exists {
		currentRoute, err := getConfirmedILNRoute(source, destination.Domain, route.RouteID)
		if err != nil {
			return err
		}
		activation, err := getILNGatewayActivationBlock(source, currentRoute.Route.Gateway)
		if err != nil {
			return err
		}
		cursor = activation - 1
		if err := w.writeILNCursor(route.Name, cursor); err != nil {
			return err
		}
	}
	if cursor >= confirmedHead {
		return nil
	}

	from := cursor + 1
	to := confirmedHead
	if to-from+1 > ilnOperationScanChunk {
		to = from + ilnOperationScanChunk - 1
	}

	currentRoute, err := getConfirmedILNRoute(source, destination.Domain, route.RouteID)
	if err != nil {
		return err
	}
	operations, err := getConfirmedILNOperations(
		source, currentRoute.Route.Gateway, route.RouteID, destination.Domain, from, to,
	)
	if err != nil {
		return err
	}
	sort.SliceStable(operations, func(i, j int) bool {
		return operations[i].BlockNumber < operations[j].BlockNumber
	})
	for i := range operations {
		if err := w.signILNOperation(route, source, destination, set, operations[i]); err != nil {
			return err
		}
	}
	return w.writeILNCursor(route.Name, to)
}

func (w *Worker) signILNOperation(
	route *evmInterchain.CheckpointRoute,
	source *evmInterchain.Destination,
	destination *evmInterchain.Destination,
	set *evmInterchain.ValidatorSet,
	operation evmInterchain.ILNOperation,
) error {
	snapshot, err := getILNRouteAtBlock(source, destination.Domain, route.RouteID, operation.BlockNumber)
	if err != nil {
		return err
	}
	if operation.RouteID != route.RouteID ||
		operation.DestinationDomain != destination.Domain ||
		operation.ValidatorFeeWei == nil ||
		operation.ValidatorFeeWei.Sign() <= 0 {
		return fmt.Errorf("ILN operation destination or validator fee is invalid")
	}
	if snapshot.Route.ValidatorFeeWei == nil ||
		operation.ValidatorFeeWei.Cmp(snapshot.Route.ValidatorFeeWei) != 0 {
		return fmt.Errorf("ILN operation validator fee does not match canonical route fee")
	}
	checkpoint, err := getConfirmedILNCheckpoint(source, snapshot)
	if err != nil {
		return err
	}
	if checkpoint.Root == types.ZeroHash {
		return fmt.Errorf("ILN operation has no non-zero checkpoint root")
	}

	payload := protocol.ILNCheckpointPayload{
		SourceChainID:       snapshot.Route.Key.SourceChainID,
		SourceDomain:        snapshot.Route.Key.SourceDomain,
		DestinationDomain:   snapshot.Route.Key.DestinationDomain,
		RouteID:             snapshot.Route.Key.RouteID,
		SetID:               set.SetID,
		SourceBlockNumber:   snapshot.BlockNumber,
		Registry:            snapshot.Registry,
		Gateway:             snapshot.Route.Gateway,
		SourceRouter:        snapshot.Route.SourceRouter,
		Mailbox:             snapshot.Route.Mailbox,
		MerkleTreeHook:      snapshot.Route.MerkleTreeHook,
		DestinationRouter:   snapshot.Route.DestinationRouter,
		ValidatorFeeWei:     new(big.Int).Set(operation.ValidatorFeeWei),
		AuthorizedMessageID: operation.MessageID,
		Root:                checkpoint.Root,
		Index:               checkpoint.Index,
	}
	raw, err := payload.MarshalBinary()
	if err != nil {
		return err
	}
	hash := crypto.Keccak256Hash(raw)

	w.mu.Lock()
	_, alreadySigned := w.ilnLocalVotes[hash]
	w.mu.Unlock()
	if alreadySigned {
		return nil
	}

	blsKey, err := crypto.BytesToBLSSecretKey(w.blsRaw)
	if err != nil {
		return err
	}
	signature, err := crypto.SignByBLS(blsKey, raw)
	if err != nil {
		return err
	}
	vote := ilnCheckpointVote{
		Route: route.Name,
		Payload: raw,
		Signer: w.localAddr,
		Signature: signature,
	}
	if err := w.persistILNLocalVote(hash, vote); err != nil {
		return err
	}
	w.mu.Lock()
	w.ilnLocalVotes[hash] = &ilnLocalVoteState{
		vote: vote,
		lastBroadcast: time.Now(),
	}
	w.mu.Unlock()

	if err := w.acceptILNCheckpointVote(vote); err != nil {
		return err
	}
	return w.publish(wireEnvelope{Type: "iln_checkpoint_vote", ILNCheckpointVote: &vote})
}

func (w *Worker) acceptILNCheckpointVote(vote ilnCheckpointVote) error {
	route := w.routes[vote.Route]
	if route == nil || stringsTrim(route.SourceNetwork) == "" {
		return fmt.Errorf("unknown ILN checkpoint route")
	}
	source := w.destinations[route.SourceNetwork]
	destination := w.destinations[route.Destination]
	if source == nil || destination == nil {
		return fmt.Errorf("ILN checkpoint route networks are not configured")
	}

	var payload protocol.ILNCheckpointPayload
	if err := payload.UnmarshalBinary(vote.Payload); err != nil {
		return err
	}

	if payload.RouteID != route.RouteID {
		return fmt.Errorf("ILN checkpoint route id mismatch")
	}
	snapshot, err := getILNRouteAtBlock(source, destination.Domain, payload.RouteID, payload.SourceBlockNumber)
	if err != nil {
		return err
	}
	if payload.Registry != snapshot.Registry ||
		payload.SourceChainID != snapshot.Route.Key.SourceChainID ||
		payload.SourceDomain != snapshot.Route.Key.SourceDomain ||
		payload.DestinationDomain != snapshot.Route.Key.DestinationDomain ||
		payload.RouteID != snapshot.Route.Key.RouteID ||
		payload.Gateway != snapshot.Route.Gateway ||
		payload.SourceRouter != snapshot.Route.SourceRouter ||
		payload.Mailbox != snapshot.Route.Mailbox ||
		payload.MerkleTreeHook != snapshot.Route.MerkleTreeHook ||
		payload.DestinationRouter != snapshot.Route.DestinationRouter ||
		payload.ValidatorFeeWei == nil ||
		payload.ValidatorFeeWei.Sign() <= 0 {
		return fmt.Errorf("ILN checkpoint canonical route mismatch")
	}

	operation, err := getILNOperationAtBlock(
		source,
		snapshot.Route.Gateway,
		payload.RouteID,
		destination.Domain,
		payload.AuthorizedMessageID,
		payload.SourceBlockNumber,
	)
	if err != nil {
		return err
	}
	if operation.ValidatorFeeWei == nil ||
		operation.ValidatorFeeWei.Cmp(payload.ValidatorFeeWei) != 0 ||
		snapshot.Route.ValidatorFeeWei == nil ||
		operation.ValidatorFeeWei.Cmp(snapshot.Route.ValidatorFeeWei) != 0 {
		return fmt.Errorf("ILN checkpoint operation fee mismatch")
	}

	checkpoint, err := getConfirmedILNCheckpoint(source, snapshot)
	if err != nil {
		return err
	}
	if checkpoint.Root != payload.Root || checkpoint.Index != payload.Index {
		return fmt.Errorf("ILN checkpoint root or index mismatch at source block")
	}

	set, err := getILNValidatorSet(destination)
	if err != nil {
		return err
	}
	if payload.SetID != set.SetID {
		return fmt.Errorf("stale ILN checkpoint validator set id")
	}
	index := indexOf(set.Validators, vote.Signer)
	if index < 0 {
		return fmt.Errorf("ILN checkpoint signer is not in current interchain set")
	}
	eligible, err := w.interchainSignerEligible(set, vote.Signer)
	if err != nil {
		return err
	}
	if !eligible {
		return fmt.Errorf("ILN checkpoint signer is not currently eligible")
	}
	if err := crypto.VerifyBLSSignatureFromBytes(
		set.BLSPublicKeys[index], vote.Signature, vote.Payload,
	); err != nil {
		return fmt.Errorf("invalid ILN checkpoint vote: %w", err)
	}

	hash := crypto.Keccak256Hash(vote.Payload)
	w.mu.Lock()
	state := w.ilnCheckpoints[hash]
	if state == nil {
		state = &ilnCheckpointState{
			route: vote.Route,
			payload: payload,
			raw: append([]byte(nil), vote.Payload...),
			votes: make(map[types.Address][]byte),
		}
		w.ilnCheckpoints[hash] = state
	}
	state.votes[vote.Signer] = append([]byte(nil), vote.Signature...)
	w.mu.Unlock()

	return w.finalizeILNCheckpointAttestation(hash, route, destination, set)
}

func (w *Worker) finalizeILNCheckpointAttestation(
	hash types.Hash,
	route *evmInterchain.CheckpointRoute,
	destination *evmInterchain.Destination,
	set *evmInterchain.ValidatorSet,
) error {
	w.mu.Lock()
	state := w.ilnCheckpoints[hash]
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

	attestation := ilnCheckpointAttestation{
		Version: protocol.ILNCheckpointDomainV1,
		Chain: route.Name,
		Destination: destination.Name,
		OriginChainID: payload.SourceChainID,
		OriginDomain: payload.SourceDomain,
		DestinationDomain: payload.DestinationDomain,
		RouteID: payload.RouteID.String(),
		SetID: payload.SetID,
		SourceBlockNumber: payload.SourceBlockNumber,
		Registry: payload.Registry.String(),
		Gateway: payload.Gateway.String(),
		SourceRouter: payload.SourceRouter.String(),
		Mailbox: payload.Mailbox.String(),
		MerkleTreeHook: payload.MerkleTreeHook.String(),
		DestinationRouter: payload.DestinationRouter.String(),
		ValidatorFeeWei: payload.ValidatorFeeWei.String(),
		AuthorizedMessageID: payload.AuthorizedMessageID.String(),
		Root: payload.Root.String(),
		Index: payload.Index,
		Payload: "0x" + hex.EncodeToString(raw),
		SignerBitmap: "0x" + hex.EncodeToString(bitmap.Bytes()),
		AggregateSignature: "0x" + hex.EncodeToString(eipSignature),
		AggregateSignatureCompressed: "0x" + hex.EncodeToString(aggregate),
	}
	if err := w.writeILNCheckpointAttestation(attestation); err != nil {
		return err
	}
	w.removeILNLocalVote(hash)
	return nil
}

func (w *Worker) writeILNCheckpointAttestation(attestation ilnCheckpointAttestation) error {
	raw, err := json.Marshal(attestation)
	if err != nil {
		return err
	}
	routeDir := filepath.Join(w.attestationDir(), attestation.Chain)
	if err := os.MkdirAll(routeDir, 0o770); err != nil {
		return err
	}
	latestTmp := filepath.Join(routeDir, "latest.json.tmp")
	latest := filepath.Join(routeDir, "latest.json")
	if err := os.WriteFile(latestTmp, raw, 0o660); err != nil {
		return err
	}
	if err := os.Rename(latestTmp, latest); err != nil {
		return err
	}
	archive := filepath.Join(routeDir, stringsTrim(attestation.AuthorizedMessageID)+".json")
	if err := os.WriteFile(archive+".tmp", raw, 0o660); err != nil {
		return err
	}
	return os.Rename(archive+".tmp", archive)
}

func (w *Worker) rebroadcastILNCheckpointVotes() {
	now := time.Now()
	var pending []struct {
		hash types.Hash
		vote ilnCheckpointVote
	}

	w.mu.Lock()
	for hash, state := range w.ilnLocalVotes {
		if state == nil || len(state.vote.Signature) == 0 {
			continue
		}
		if now.Sub(state.lastBroadcast) < requestRebroadcastInterval {
			continue
		}
		state.lastBroadcast = now
		vote := state.vote
		vote.Payload = append([]byte(nil), state.vote.Payload...)
		vote.Signature = append([]byte(nil), state.vote.Signature...)
		pending = append(pending, struct {
			hash types.Hash
			vote ilnCheckpointVote
		}{hash: hash, vote: vote})
	}
	w.mu.Unlock()

	for i := range pending {
		var payload protocol.ILNCheckpointPayload
		if err := payload.UnmarshalBinary(pending[i].vote.Payload); err != nil {
			w.removeILNLocalVote(pending[i].hash)
			continue
		}
		route := w.routes[pending[i].vote.Route]
		if route == nil {
			w.removeILNLocalVote(pending[i].hash)
			continue
		}
		source := w.destinations[route.SourceNetwork]
		destination := w.destinations[route.Destination]
		if source == nil || destination == nil {
			continue
		}
		set, err := getILNValidatorSet(destination)
		if err != nil {
			continue
		}
		if set.SetID != payload.SetID {
			operation, err := getILNOperationAtBlock(
				source,
				payload.Gateway,
				payload.RouteID,
				destination.Domain,
				payload.AuthorizedMessageID,
				payload.SourceBlockNumber,
			)
			if err != nil {
				continue
			}
			w.removeILNLocalVote(pending[i].hash)
			if err := w.signILNOperation(route, source, destination, set, *operation); err != nil {
				w.logger.Debug(
					"ILN local vote not refreshed for new validator set",
					"route", route.Name,
					"message", payload.AuthorizedMessageID.String(),
					"err", err,
				)
			}
			continue
		}

		// Re-accept the local vote after restart so it contributes to this
		// process's in-memory aggregate state before rebroadcast.
		_ = w.acceptILNCheckpointVote(pending[i].vote)
		_ = w.publish(wireEnvelope{Type: "iln_checkpoint_vote", ILNCheckpointVote: &pending[i].vote})
	}
}

