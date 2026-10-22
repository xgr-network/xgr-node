package runtime

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	getILNOperationAtBlock        = evmInterchain.GetILNOperationAtBlock
    getConfirmedILNOperations    = evmInterchain.GetConfirmedILNOperations
	getILNMessageDelivered       = evmInterchain.GetILNMessageDelivered
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

func (w *Worker) acceptILNCheckpointVote(vote ilnCheckpointVote) error {
	var payload protocol.ILNCheckpointPayload
	if err := payload.UnmarshalBinary(vote.Payload); err != nil {
		return err
	}
	w.mu.Lock()
	route := w.routes[vote.Route]
	// Old ENV route aliases and new deterministic on-chain-discovery route
	// names must interoperate: the SIGNED canonical route identity wins.
	if route == nil {
		for _,candidate := range w.routes {
			src := w.destinations[candidate.SourceNetwork]
			dst := w.destinations[candidate.Destination]
			if src!=nil && dst!=nil && candidate.RouteID==payload.RouteID &&
				src.Domain==payload.SourceDomain && dst.Domain==payload.DestinationDomain {
				route=candidate
				break
			}
		}
	}
	w.mu.Unlock()
	if route == nil || stringsTrim(route.SourceNetwork) == "" {
		return fmt.Errorf("unknown ILN checkpoint route")
	}
	source := w.destinations[route.SourceNetwork]
	destination := w.destinations[route.Destination]
	if source == nil || destination == nil {
		return fmt.Errorf("ILN checkpoint route networks are not configured")
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

    // Reconstruct the receiver's independently confirmed current full Hook
    // tree. The signed root is a deterministic earlier insertion prefix,
    // authenticated by the original successful Gateway/Dispatch/Hook receipt.
    current,err:=w.verifiedILNReplicaRoute(route)
    if err!=nil{return fmt.Errorf("ILN peer vote current source replica unavailable: %w",err)}
    if current.snapshot.Registry!=payload.Registry ||
        current.snapshot.Route.Gateway!=payload.Gateway ||
        current.snapshot.Route.MerkleTreeHook!=payload.MerkleTreeHook ||
        current.snapshot.Route.Mailbox!=payload.Mailbox ||
        current.snapshot.Route.SourceRouter!=payload.SourceRouter ||
        current.snapshot.Route.DestinationRouter!=payload.DestinationRouter {
        return fmt.Errorf("ILN canonical source route changed while proving vote")
    }
    if payload.SourceBlockNumber>current.canonical.BlockNumber {
        return fmt.Errorf("ILN vote references unconfirmed source insertion")
    }
    if uint64(payload.Index)>=current.canonical.Count ||
        current.replica.Leaves[payload.Index]!=payload.AuthorizedMessageID {
        return fmt.Errorf("ILN vote message not at signed source insertion index")
    }
    operation,err:=getILNOperationAtBlock(source,snapshot.Route.Gateway,
        payload.RouteID,destination.Domain,payload.AuthorizedMessageID,
        payload.SourceBlockNumber)
    if err!=nil{return fmt.Errorf("ILN receipt-bound vote proof unavailable: %w",err)}
    if operation==nil || operation.BlockNumber!=payload.SourceBlockNumber ||
        operation.InsertionIndex!=payload.Index {
        return fmt.Errorf("ILN receipt insertion does not match signed checkpoint")
    }
    _,prefix,err:=evmInterchain.VerifiedILNMessagePrefix(
        current.replica,current.canonical,payload.Index,payload.SourceBlockNumber)
    if err!=nil{return err}
    if prefix.Root!=payload.Root || prefix.Count!=uint64(payload.Index)+1 {
        return fmt.Errorf("ILN signed insertion root differs from confirmed receipt-bound Merkle prefix")
    }
    if snapshot.Route.ValidatorFeeWei==nil ||
        payload.ValidatorFeeWei.Cmp(snapshot.Route.ValidatorFeeWei)!=0 {
        return fmt.Errorf("ILN signed source fee policy differs from canonical current route")
    }
    if err:=verifyILNFeeAllocation(source,snapshot,payload.AuthorizedMessageID,
        current.canonical.BlockNumber);err!=nil {
        return fmt.Errorf("ILN vote lacks source FeeVault eligibility: %w",err)
    }
    delivered,err:=getILNMessageDelivered(destination,snapshot.Route.DestinationRouter,payload.AuthorizedMessageID)
    if err!=nil{return fmt.Errorf("ILN vote destination state unavailable: %w",err)}
    if delivered{return fmt.Errorf("ILN message already delivered; reject historical vote")}

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
	voteCount := len(state.votes)
	w.mu.Unlock()

	threshold, err := protocol.QuorumThreshold(len(set.Validators))
	if err != nil { return err }
	if progressErr:=w.recordILNQuorumProgress(ilnQuorumProgress{
		Route:route.Name,MessageID:payload.AuthorizedMessageID.String(),
		SourceBlockNumber:payload.SourceBlockNumber,Status:"PARTIAL_QUORUM",
		Votes:voteCount,QuorumThreshold:threshold,
	});progressErr!=nil {
		w.logger.Warn("ILN partial-quorum status persistence failed",
			"route",route.Name,"err",progressErr)
	}
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

	// BLS votes already collected for a retired set are audit material only.
	// Recheck both current destination membership and delivery before writing.
	currentSet, err := getILNValidatorSet(destination)
	if err != nil { return fmt.Errorf("ILN finalization set unavailable: %w", err) }
	if currentSet.SetID != payload.SetID || currentSet.SetID != set.SetID { return fmt.Errorf("ILN finalization refused stale validator set") }
	delivered, err := getILNMessageDelivered(destination, payload.DestinationRouter, payload.AuthorizedMessageID)
	if err != nil { return fmt.Errorf("ILN finalization delivery check failed closed: %w", err) }
	if delivered { return fmt.Errorf("ILN finalized delivery already recorded by destination") }

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
	if progressErr:=w.recordILNQuorumProgress(ilnQuorumProgress{
		Route:route.Name,MessageID:payload.AuthorizedMessageID.String(),
		SourceBlockNumber:payload.SourceBlockNumber,Status:"AVAILABLE_UNVERIFIED",
		Votes:len(votes),QuorumThreshold:threshold,
	});progressErr!=nil {
		w.logger.Warn("ILN quorum completion status persistence failed",
			"route",route.Name,"err",progressErr)
	}
	// Keep locally signed votes until Mailbox.delivered becomes true. A
	// validator that assembled 2/3 first must continue gossiping its
	// authenticated vote so that lagging peers can independently assemble
	// the same quorum after an outage or restart.
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
	// Immutable per-validator-set history survives subsequent rotations; the
	// legacy messageId.json remains the latest compatibility view.
	setDir := filepath.Join(routeDir, stringsTrim(attestation.AuthorizedMessageID))
	if attestation.SetID == 0 { return fmt.Errorf("cannot archive empty ILN validator set") }
	if err := os.MkdirAll(setDir, 0o770); err != nil { return err }
	setPath := filepath.Join(setDir, strconv.FormatUint(attestation.SetID, 10)+".json")
	if err := os.WriteFile(setPath+".tmp", raw, 0o660); err != nil { return err }
	if err := os.Rename(setPath+".tmp", setPath); err != nil { return err }
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
		w.mu.Lock()
		route := w.routes[pending[i].vote.Route]
		w.mu.Unlock()
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
		// On-chain delivery allows reclaiming local vote memory and disk.
        // A failed destination RPC is never grounds for deletion.
        if delivered,deliveryErr:=getILNMessageDelivered(destination,payload.DestinationRouter,payload.AuthorizedMessageID);deliveryErr==nil&&delivered {
          w.removeILNLocalVote(pending[i].hash)
          continue
        }
        // The immutable signed vote is still usable across later epochs
        // while the independently confirmed source content stays identical.
        // Do not re-sign merely because another RPC node is 32 blocks ahead.
        // Rotate only on actual content or destination validator-set changes.
        needsRefresh:=set.SetID!=payload.SetID
        if !needsRefresh {
            if current,verifyErr:=w.verifiedILNReplicaRoute(route);verifyErr==nil {
                needsRefresh=payload.Root!=current.canonical.Root ||
                    payload.Index!=current.checkpoint.Index
            }
        }
        if needsRefresh {
            w.removeILNLocalVote(pending[i].hash)
            if err:=w.signVerifiedILNReplicaMessage(route,payload.AuthorizedMessageID,payload.SourceBlockNumber);err!=nil {
                w.logger.Debug("ILN local vote awaits fresh confirmed root/set",
                    "route",route.Name,"message",payload.AuthorizedMessageID,"err",err)
            }
            continue
        }

        // Re-accept the local vote after restart so it contributes to this
		// process's in-memory aggregate state before rebroadcast.
		_ = w.acceptILNCheckpointVote(pending[i].vote)
		_ = w.publish(wireEnvelope{Type: "iln_checkpoint_vote", ILNCheckpointVote: &pending[i].vote})
	}
}

