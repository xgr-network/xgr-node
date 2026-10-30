package runtime

import (
    "bytes"
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
	payload protocol.XITADirectMessage
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
	DestinationRouter            string `json:"destinationRouter"`
	ValidatorFeeWei              string `json:"validatorFeeWei"`
	AuthorizedMessageID          string `json:"authorizedMessageId"`
	DestinationISM               string `json:"destinationIsm"`
	DestinationRegistry          string `json:"destinationRegistry"`
	Payload                      string `json:"payload"`
	SignerBitmap                 string `json:"signerBitmap"`
	AggregateSignature           string `json:"aggregateSignature"`
	AggregateSignatureCompressed string `json:"aggregateSignatureCompressed"`
}

var (
	getConfirmedILNRoute          = evmInterchain.GetConfirmedILNRoute
	getILNRouteAtBlock            = evmInterchain.GetILNRouteAtBlock
	getILNValidatorSet            = evmInterchain.GetValidatorSet
	getConfirmedILNHead           = evmInterchain.GetConfirmedILNHead
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

func (w *Worker) acceptILNCheckpointVote(vote ilnCheckpointVote)error{
    var payload protocol.XITADirectMessage
    if err:=payload.UnmarshalBinary(vote.Payload);err!=nil{return err}
    // M-06: the peer-controlled route name is only a hint. Resolve by the
    // signed chain/domain/route identity, never by a possibly reused alias.
    w.mu.Lock()
    var route *evmInterchain.CheckpointRoute
    for _,candidate:=range w.routes {
        src,dst:=w.destinations[candidate.SourceNetwork],w.destinations[candidate.Destination]
        if src==nil||dst==nil {continue}
        if candidate.RouteID==payload.RouteID &&
            src.ChainID==payload.SourceChainID &&
            src.Domain==payload.SourceDomain &&
            dst.Domain==payload.DestinationDomain {
            if route!=nil {w.mu.Unlock();return fmt.Errorf("ambiguous canonical XITA Direct-BLS route")}
            route=candidate
        }
    }
    w.mu.Unlock()
    if route==nil{return fmt.Errorf("unknown canonical XITA Direct-BLS route")}
    ctx,err:=w.verifyDirectILNEvidence(route,payload.MessageID,payload.SourceBlockNumber)
    if err!=nil{return err}
    canonical:=w.directILNPayload(ctx)
    packed,err:=canonical.MarshalBinary()
    if err!=nil||!bytes.Equal(packed,vote.Payload){
        return fmt.Errorf("received Direct-BLS vote disagrees with independent source receipt, destination ISM or current validator set")
    }
    index:=indexOf(ctx.set.Validators,vote.Signer)
    if index<0{return fmt.Errorf("Direct-BLS voter not in destination validator set")}
    eligible,err:=w.interchainSignerEligible(ctx.set,vote.Signer)
    if err!=nil{return err}
    if !eligible{return fmt.Errorf("inactive Direct-BLS validator vote")}
    if err:=crypto.VerifyBLSSignatureFromBytes(
        ctx.set.BLSPublicKeys[index],vote.Signature,vote.Payload);err!=nil{
        return fmt.Errorf("invalid signed Direct-BLS vote: %w",err)
    }
    hash:=crypto.Keccak256Hash(vote.Payload)
    w.mu.Lock()
    state:=w.ilnCheckpoints[hash]
    if state==nil{
        state=&ilnCheckpointState{route:route.Name,payload:payload,
            raw:append([]byte(nil),vote.Payload...),votes:make(map[types.Address][]byte)}
        w.ilnCheckpoints[hash]=state
    }
    state.votes[vote.Signer]=append([]byte(nil),vote.Signature...)
    count:=len(state.votes)
    w.mu.Unlock()
    threshold,err:=protocol.QuorumThreshold(len(ctx.set.Validators))
    if err!=nil{return err}
    if err:=w.recordILNQuorumProgress(ilnQuorumProgress{
        Route:route.Name,MessageID:payload.MessageID.String(),
        SourceBlockNumber:payload.SourceBlockNumber,Status:"PARTIAL_QUORUM",
        Votes:count,QuorumThreshold:threshold,
    });err!=nil{w.logger.Warn("Direct-BLS quorum progress write failed","err",err)}
    return w.finalizeILNCheckpointAttestation(hash,route,ctx.destination,ctx.set)
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
	delivered, err := getILNMessageDelivered(destination, payload.DestinationRouter, payload.MessageID)
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
		Version: protocol.XITADirectMessageDomain,
		Chain: route.Name,
		Destination: destination.Name,
		OriginChainID: payload.SourceChainID,
		OriginDomain: payload.SourceDomain,
		DestinationDomain: payload.DestinationDomain,
		RouteID: payload.RouteID.String(),
		SetID: payload.SetID,
		SourceBlockNumber: payload.SourceBlockNumber,
		Registry: payload.SourceRegistry.String(),
		Gateway: payload.Gateway.String(),
		SourceRouter: payload.SourceRouter.String(),
		Mailbox: payload.SourceMailbox.String(),
		DestinationRouter: payload.DestinationRouter.String(),
		ValidatorFeeWei: payload.ValidatorFeeWei.String(),
		AuthorizedMessageID: payload.MessageID.String(),
		DestinationISM: payload.DestinationISM.String(),
		DestinationRegistry: payload.DestinationRegistry.String(),
		Payload: "0x" + hex.EncodeToString(raw),
		SignerBitmap: "0x" + hex.EncodeToString(bitmap.Bytes()),
		AggregateSignature: "0x" + hex.EncodeToString(eipSignature),
		AggregateSignatureCompressed: "0x" + hex.EncodeToString(aggregate),
	}
	if err := w.writeILNCheckpointAttestation(attestation); err != nil {
		return err
	}
	if progressErr:=w.recordILNQuorumProgress(ilnQuorumProgress{
		Route:route.Name,MessageID:payload.MessageID.String(),
		SourceBlockNumber:payload.SourceBlockNumber,Status:"AVAILABLE_UNVERIFIED",
		Votes:len(votes),QuorumThreshold:threshold,
	});progressErr!=nil {
		w.logger.Warn("ILN quorum completion status persistence failed",
			"route",route.Name,"err",progressErr)
	}
    // Disseminate the verified aggregate itself; lagging nodes must not need
    // a fresh external source-RPC witness quorum to obtain an existing 2/3 proof.
    w.publishILNCompletedQuorum(attestation)
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
	// messageId.json remains the latest attestation view.
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
    now:=time.Now()
    var pending []ilnCheckpointVote
    w.mu.Lock()
    for _,state:=range w.ilnLocalVotes {
        if state==nil||len(state.vote.Signature)==0||
           now.Sub(state.lastBroadcast)<requestRebroadcastInterval{continue}
        state.lastBroadcast=now
        v:=state.vote
        v.Payload=append([]byte(nil),state.vote.Payload...)
        v.Signature=append([]byte(nil),state.vote.Signature...)
        pending=append(pending,v)
    }
    w.mu.Unlock()
    for _,vote:=range pending {
        hash:=crypto.Keccak256Hash(vote.Payload)
        var payload protocol.XITADirectMessage
        if payload.UnmarshalBinary(vote.Payload)!=nil{
            w.removeILNLocalVote(hash)
            continue
        }
        w.mu.Lock();route:=w.routes[vote.Route];w.mu.Unlock()
        if route==nil{continue}
        dest:=w.destinations[route.Destination]
        if dest==nil{continue}
        set,err:=getILNValidatorSet(dest)
        if err!=nil{continue}
        delivered,err:=getILNMessageDelivered(dest,payload.DestinationRouter,payload.MessageID)
        if err!=nil{continue}
        if delivered{
            w.removeILNLocalVote(hash)
            continue
        }
        if set.SetID!=payload.SetID{
            if err:=w.signDirectILNMessage(route,payload.MessageID,payload.SourceBlockNumber);err!=nil{
                w.logger.Debug("Direct-BLS message awaits current validator set",
                  "route",route.Name,"err",err)
            }else{
                // Successor is persisted by the normal signing path before
                // the obsolete-set vote can be removed.
                w.removeILNLocalVote(hash)
            }
            continue
        }
        if err:=w.acceptILNCheckpointVote(vote);err!=nil{
            w.logger.Debug("Direct-BLS vote cannot yet be reverified","route",route.Name,"err",err)
            continue
        }
        _=w.publish(wireEnvelope{Type:"iln_checkpoint_vote",ILNCheckpointVote:&vote})
    }
}
