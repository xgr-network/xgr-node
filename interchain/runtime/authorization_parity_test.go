package runtime

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	stakingcontract "github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/interchain/authorization"
	"github.com/xgr-network/xgr-node/interchain/evm"
	"github.com/xgr-network/xgr-node/types"
)

type authorizationParityState struct {
	*fakeInterchainState
	synchronized bool
}

func (s *authorizationParityState) IsSynchronized() bool { return s.synchronized }

// Both schemes enter the same runtime with an explicit per-destination set.
// ECDSA workers deliberately receive no BLS signing secret.
func authorizationParityFixture(t *testing.T, scheme string, n int) (*authorization.Adapter, *evm.ValidatorSet, []authorization.Keys, *authorizationParityState, *evm.Destination) {
	t.Helper()
	adapter, err := authorization.ForScheme(scheme)
	require.NoError(t, err)
	set := &evm.ValidatorSet{Scheme: scheme, SetID: 7}
	keys := make([]authorization.Keys, n)
	state := &authorizationParityState{fakeInterchainState: &fakeInterchainState{origin: 1643, validators: map[types.Address]*stakingcontract.ValidatorInfo{}}, synchronized: true}
	for i := range keys {
		keys[i].ECDSA, err = crypto.GenerateECDSAKey()
		require.NoError(t, err)
		address := crypto.PubKeyToAddress(&keys[i].ECDSA.PublicKey)
		set.Validators = append(set.Validators, address)
		blsKey, err := crypto.GenerateBLSKey()
		require.NoError(t, err)
		pub, err := crypto.BLSSecretKeyToPubkeyBytes(blsKey)
		require.NoError(t, err)
		state.validators[address] = &stakingcontract.ValidatorInfo{Exists: true, Active: true, BLSPubKey: pub}
		if scheme == authorization.SchemeBLS {
			raw, err := blsKey.MarshalBinary()
			require.NoError(t, err)
			keys[i].BLS = []byte(hex.EncodeToString(raw))
			set.VerificationKeys = append(set.VerificationKeys, pub)
			set.BLSPublicKeys = append(set.BLSPublicKeys, pub)
		} else {
			set.VerificationKeys = append(set.VerificationKeys, append([]byte(nil), address.Bytes()...))
		}
	}
	destination := &evm.Destination{Name: "base", ChainID: 8453, Domain: 8453, SignatureScheme: scheme, VerifierFormat: evm.VerifierFormatEIP2537,
		RegistryAddress: types.StringToAddress("0x7777").String(), ILNRegistryAddress: types.StringToAddress("0xabcd").String(), MembershipValiditySeconds: 300}
	if scheme == authorization.SchemeECDSA {
		destination.Name = "xdc"
		destination.ChainID = 50
		destination.Domain = 50
		destination.VerifierFormat = evm.VerifierFormatECDSA
	}
	require.NoError(t, adapter.ValidateSet(set.AuthorizationSet()))
	return adapter, set, keys, state, destination
}

func authorizationParityMessage(destination *evm.Destination, setID uint64) protocol.XITADirectMessage {
	return protocol.XITADirectMessage{SourceChainID: 1643, SourceDomain: 1643, DestinationDomain: destination.Domain,
		RouteID: types.StringToHash("0x1234"), SetID: setID, SourceBlockNumber: 500,
		SourceRegistry: types.StringToAddress("0x1111"), Gateway: types.StringToAddress("0x2222"),
		SourceRouter: types.StringToAddress("0x3333"), SourceMailbox: types.StringToAddress("0x4444"), DestinationRouter: types.StringToAddress("0x5555"),
		ValidatorFeeWei: big.NewInt(8000), MessageID: types.StringToHash("0x5678"), DestinationISM: types.StringToAddress("0x6666"), DestinationRegistry: types.StringToAddress(destination.RegistryAddress)}
}

func TestAuthorizationParityGovernanceRequiresExplicitApprovalAndSync(t *testing.T) {
	for _, scheme := range []string{authorization.SchemeBLS, authorization.SchemeECDSA} {
		t.Run(scheme, func(t *testing.T) {
			adapter, set, keys, state, destination := authorizationParityFixture(t, scheme, 3)
			old := getGovernanceValidatorSet
			getGovernanceValidatorSet = func(got *evm.Destination) (*evm.ValidatorSet, error) {
				require.Same(t, destination, got)
				return set, nil
			}
			defer func() { getGovernanceValidatorSet = old }()
			w := &Worker{state: state, dataDir: t.TempDir(), localAddr: set.Validators[0], ecdsaKey: keys[0].ECDSA, blsRaw: keys[0].BLS,
				destinations: map[string]*evm.Destination{destination.Name: destination}, governance: map[types.Hash]*governanceState{}}
			if scheme == authorization.SchemeBLS {
				w.blsPubKey = set.BLSPublicKeys[0]
			} else {
				require.Empty(t, w.blsRaw)
			}
			p := protocol.ILNSourceFeeProposal{SourceChainID: destination.ChainID, SourceDomain: destination.Domain, Registry: types.StringToAddress(destination.ILNRegistryAddress),
				SetID: set.SetID, Nonce: 1, ValidUntil: uint64(time.Now().Add(300 * time.Second).Unix()), ValidatorFeeWei: big.NewInt(8000)}
			raw, err := p.MarshalBinary()
			require.NoError(t, err)
			id := crypto.Keccak256Hash(raw)
			require.NoError(t, w.acceptGovernanceProposal(governanceProposalWire{Payload: raw}))
			require.Nil(t, w.governance[id].localVote)
			require.Empty(t, w.governance[id].votes, "receiving a proposal cannot approve it")
			// A restart restores the proposal without inventing approval or a vote.
			recovered := &Worker{state: state, dataDir: w.dataDir, destinations: w.destinations, governance: map[types.Hash]*governanceState{}}
			require.NoError(t, recovered.recoverGovernanceProposals())
			require.NotNil(t, recovered.governance[id])
			require.Nil(t, recovered.governance[id].localVote)
			require.Empty(t, recovered.governance[id].votes)
			state.synchronized = false
			require.Error(t, w.ApproveILNGovernance(id))
			require.Nil(t, w.governance[id].localVote)
			state.synchronized = true
			// This fixture has no live gossip topic. Approval must still record its
			// local vote before the explicit, expected publish error is returned.
			err = w.ApproveILNGovernance(id)
			require.ErrorContains(t, err, "gossip topic is not started")
			require.NotNil(t, w.governance[id].localVote)
			require.Len(t, w.governance[id].votes, 1)
			require.NoError(t, adapter.VerifySourceFeeVote(set.AuthorizationSet(), 0, p, w.governance[id].localVote.Signature))
			quorumPath := filepath.Join(w.governanceQuorumDir(), id.String()+".json")
			_, err = os.Stat(quorumPath)
			require.True(t, os.IsNotExist(err), "one of three votes is not quorum")
			sig, err := adapter.SignSourceFee(keys[1], p)
			require.NoError(t, err)
			require.NoError(t, w.acceptGovernanceVote(governanceVote{Payload: raw, Signer: set.Validators[1], Signature: sig}))
			contents, err := os.ReadFile(quorumPath)
			require.NoError(t, err)
			var quorum governanceQuorum
			require.NoError(t, json.Unmarshal(contents, &quorum))
			proof, err := decodeProofArchive(adapter, quorum.SignatureScheme, quorum.SignerBitmap, quorum.AggregateSignature, quorum.AggregateSignatureCompressed, quorum.Signatures)
			require.NoError(t, err)
			require.NoError(t, adapter.VerifySourceFeeQuorum(set.AuthorizationSet(), p, proof))
			if scheme == authorization.SchemeECDSA {
				require.Empty(t, quorum.AggregateSignatureCompressed)
				require.Equal(t, scheme, quorum.SignatureScheme)
			} else {
				require.Empty(t, quorum.SignatureScheme)
				require.NotEmpty(t, quorum.AggregateSignatureCompressed)
			}
			set.SetID++
			require.Error(t, w.acceptGovernanceVote(governanceVote{Payload: raw, Signer: set.Validators[1], Signature: sig}), "retired approvals cannot enter the new set")
		})
	}
}

func TestAuthorizationParityEligibilityUsesOptInAndSelectedIdentity(t *testing.T) {
	for _, scheme := range []string{authorization.SchemeBLS, authorization.SchemeECDSA} {
		t.Run(scheme, func(t *testing.T) {
			_, set, _, state, _ := authorizationParityFixture(t, scheme, 3)
			w := &Worker{state: state}
			validator := set.Validators[0]
			eligible, err := w.interchainSignerEligible(set, validator)
			require.NoError(t, err)
			require.True(t, eligible)
			outsider := types.StringToAddress("0xffff")
			state.validators[outsider] = &stakingcontract.ValidatorInfo{Exists: true, Active: true, BLSPubKey: state.validators[validator].BLSPubKey}
			eligible, err = w.interchainSignerEligible(set, outsider)
			require.NoError(t, err)
			require.False(t, eligible, "PoS membership alone never opts a validator into a destination")
			originalBLS := state.validators[validator].BLSPubKey
			state.validators[validator].BLSPubKey = []byte{1, 2, 3}
			eligible, err = w.interchainSignerEligible(set, validator)
			require.NoError(t, err)
			require.Equal(t, scheme == authorization.SchemeECDSA, eligible, "ECDSA identity survives a BLS key rotation; BLS identity must match")
			state.validators[validator].BLSPubKey = originalBLS
			state.validators[validator].Active = false
			eligible, err = w.interchainSignerEligible(set, validator)
			require.NoError(t, err)
			require.False(t, eligible)
			state.validators[validator].Active = true
			state.validators[validator].Exists = false
			eligible, err = w.interchainSignerEligible(set, validator)
			require.NoError(t, err)
			require.False(t, eligible)
		})
	}
}

func TestAuthorizationParityCompletedArchivesBindSchemeAndCurrentSet(t *testing.T) {
	for _, scheme := range []string{authorization.SchemeBLS, authorization.SchemeECDSA} {
		t.Run(scheme, func(t *testing.T) {
			adapter, set, keys, _, destination := authorizationParityFixture(t, scheme, 9)
			p := authorizationParityMessage(destination, set.SetID)
			raw, err := adapter.EncodeDirect(p)
			require.NoError(t, err)
			votes := []authorization.Vote{}
			for _, index := range []int{8, 4, 3, 2, 1, 0} {
				sig, err := adapter.SignDirect(keys[index], p)
				require.NoError(t, err)
				votes = append(votes, authorization.Vote{ValidatorIndex: index, Signature: sig})
			}
			proof, err := adapter.AssembleDirectQuorum(set.AuthorizationSet(), votes, p)
			require.NoError(t, err)
			tag, expanded, compact, packed, err := encodeProofArchive(adapter, proof)
			require.NoError(t, err)
			source := &evm.Destination{Name: "xgr", ChainID: p.SourceChainID, Domain: p.SourceDomain, ILNRegistryAddress: p.SourceRegistry.String()}
			route := &evm.CheckpointRoute{Name: "xgr_to_" + destination.Name, SourceNetwork: source.Name, Destination: destination.Name, RouteID: p.RouteID}
			archive := ilnCheckpointAttestation{Version: adapter.DirectDomain(), Chain: route.Name, Destination: destination.Name,
				OriginChainID: p.SourceChainID, OriginDomain: p.SourceDomain, DestinationDomain: p.DestinationDomain, RouteID: p.RouteID.String(), SetID: p.SetID, SourceBlockNumber: p.SourceBlockNumber,
				Registry: p.SourceRegistry.String(), Gateway: p.Gateway.String(), SourceRouter: p.SourceRouter.String(), Mailbox: p.SourceMailbox.String(), DestinationRouter: p.DestinationRouter.String(),
				ValidatorFeeWei: p.ValidatorFeeWei.String(), AuthorizedMessageID: p.MessageID.String(), DestinationISM: p.DestinationISM.String(), DestinationRegistry: p.DestinationRegistry.String(),
				Payload: "0x" + hex.EncodeToString(raw), SignerBitmap: "0x" + hex.EncodeToString(proof.Bitmap), SignatureScheme: tag, AggregateSignature: expanded, AggregateSignatureCompressed: compact, Signatures: packed}
			decoded, err := verifyImportedILNQuorum(archive, route, source, destination, set, p.DestinationISM)
			require.NoError(t, err)
			require.Equal(t, p, decoded)
			stale := *set
			stale.SetID++
			_, err = verifyImportedILNQuorum(archive, route, source, destination, &stale, p.DestinationISM)
			require.Error(t, err)
			changed := archive
			changed.ValidatorFeeWei = "8001"
			_, err = verifyImportedILNQuorum(changed, route, source, destination, set, p.DestinationISM)
			require.Error(t, err)
			changed = archive
			changed.DestinationRegistry = types.StringToAddress("0xffff").String()
			_, err = verifyImportedILNQuorum(changed, route, source, destination, set, p.DestinationISM)
			require.Error(t, err)
			changed = archive
			if scheme == authorization.SchemeECDSA {
				changed.SignatureScheme = ""
			} else {
				changed.SignatureScheme = authorization.SchemeECDSA
			}
			_, err = verifyImportedILNQuorum(changed, route, source, destination, set, p.DestinationISM)
			require.Error(t, err)
			changed = archive
			changed.SignerBitmap = "0x" + hex.EncodeToString([]byte{proof.Bitmap[1], proof.Bitmap[0]})
			_, err = verifyImportedILNQuorum(changed, route, source, destination, set, p.DestinationISM)
			require.Error(t, err)
			foreign := *destination
			if scheme == authorization.SchemeECDSA {
				foreign.SignatureScheme = authorization.SchemeBLS
				foreign.VerifierFormat = evm.VerifierFormatCompressed
			} else {
				foreign.SignatureScheme = authorization.SchemeECDSA
				foreign.VerifierFormat = evm.VerifierFormatECDSA
			}
			_, err = verifyImportedILNQuorum(archive, route, source, &foreign, set, p.DestinationISM)
			require.Error(t, err, "archive bytes cannot select or override destination scheme")
		})
	}
}

func TestAuthorizationParityRecoveryRequiresDurableSameMessageSuccessor(t *testing.T) {
	for _, scheme := range []string{authorization.SchemeBLS, authorization.SchemeECDSA} {
		t.Run(scheme, func(t *testing.T) {
			adapter, set, keys, _, destination := authorizationParityFixture(t, scheme, 3)
			w := &Worker{dataDir: t.TempDir(), localAddr: set.Validators[0], destinations: map[string]*evm.Destination{destination.Name: destination}, ilnLocalVotes: map[types.Hash]*ilnLocalVoteState{}}
			makeVote := func(message protocol.XITADirectMessage) ilnCheckpointVote {
				raw, err := adapter.EncodeDirect(message)
				require.NoError(t, err)
				sig, err := adapter.SignDirect(keys[0], message)
				require.NoError(t, err)
				return ilnCheckpointVote{Route: "route", Payload: raw, Signer: w.localAddr, Signature: sig}
			}
			previous := authorizationParityMessage(destination, 7)
			old := makeVote(previous)
			nextMessage := previous
			nextMessage.SetID = 8
			next := makeVote(nextMessage)
			hash := crypto.Keccak256Hash(next.Payload)
			require.False(t, w.hasDurableILNSuccessorVote(old, 8))
			w.ilnLocalVotes[hash] = &ilnLocalVoteState{vote: next}
			require.False(t, w.hasDurableILNSuccessorVote(old, 8), "memory alone cannot authorize deletion")
			require.NoError(t, w.persistILNLocalVote(hash, next))
			require.True(t, w.hasDurableILNSuccessorVote(old, 8))
			require.False(t, w.hasDurableILNSuccessorVote(old, 9))
			changedMessage := nextMessage
			changedMessage.ValidatorFeeWei = big.NewInt(8001)
			changed := makeVote(changedMessage)
			changedHash := crypto.Keccak256Hash(changed.Payload)
			w.ilnLocalVotes = map[types.Hash]*ilnLocalVoteState{changedHash: {vote: changed}}
			require.NoError(t, w.persistILNLocalVote(changedHash, changed))
			require.False(t, w.hasDurableILNSuccessorVote(old, 8), "same message ID with a different signed fee is not successor evidence")
			w.ilnLocalVotes = map[types.Hash]*ilnLocalVoteState{hash: {vote: next}}
			require.NoError(t, os.WriteFile(filepath.Join(w.ilnVoteDir(), hash.String()+".json"), []byte("{}"), 0600))
			require.False(t, w.hasDurableILNSuccessorVote(old, 8), "corrupt durable journal must retain old recovery evidence")
			require.NoError(t, w.persistILNLocalVote(hash, next))
			foreignScheme := authorization.SchemeBLS
			if scheme == authorization.SchemeBLS {
				foreignScheme = authorization.SchemeECDSA
			}
			foreign, err := authorization.ForScheme(foreignScheme)
			require.NoError(t, err)
			foreignRaw, err := foreign.EncodeDirect(nextMessage)
			require.NoError(t, err)
			foreignVote := next
			foreignVote.Payload = foreignRaw
			foreignHash := crypto.Keccak256Hash(foreignRaw)
			w.ilnLocalVotes = map[types.Hash]*ilnLocalVoteState{foreignHash: {vote: foreignVote}}
			require.NoError(t, w.persistILNLocalVote(foreignHash, foreignVote))
			require.False(t, w.hasDurableILNSuccessorVote(old, 8), "a foreign signing domain cannot retire a local vote")
		})
	}
}

// A StateReader is allowed to report an unavailable candidate as nil. The
// common membership gate must reject it without dereferencing the result.
type authorizationNilCandidateState struct {
	StateReader
	candidate types.Address
}

func (s *authorizationNilCandidateState) ValidatorInfo(address types.Address) (*stakingcontract.ValidatorInfo, error) {
	if address == s.candidate {
		return nil, nil
	}
	return s.StateReader.ValidatorInfo(address)
}

func TestAuthorizationParityMembershipAdmissionRequiresCandidateConsent(t *testing.T) {
	for _, scheme := range []string{authorization.SchemeBLS, authorization.SchemeECDSA} {
		t.Run(scheme, func(t *testing.T) {
			adapter, fullSet, keys, state, destination := authorizationParityFixture(t, scheme, 4)
			// The candidate is active in XGR PoS, but has not opted into this
			// destination. The registry snapshot contains only the first three.
			set := *fullSet
			set.Validators = fullSet.Validators[:3]
			set.VerificationKeys = fullSet.VerificationKeys[:3]
			if scheme == authorization.SchemeBLS {
				set.BLSPublicKeys = fullSet.BLSPublicKeys[:3]
			}
			candidate := fullSet.Validators[3]
			original := getMembershipValidatorSet
			getMembershipValidatorSet = func(got *evm.Destination) (*evm.ValidatorSet, error) {
				require.Same(t, destination, got)
				return &set, nil
			}
			defer func() { getMembershipValidatorSet = original }()
			w := &Worker{state: state, destinations: map[string]*evm.Destination{destination.Name: destination}, requests: map[types.Hash]*requestState{}}
			m, err := w.membershipPayload(destination, adapter, set.SetID, protocol.ActionAddValidator, candidate, fullSet.VerificationKeys[3])
			require.NoError(t, err)
			raw, err := adapter.EncodeMembership(m)
			require.NoError(t, err)
			candidateSig, err := adapter.SignMembership(keys[3], m, time.Now())
			require.NoError(t, err)
			request := wireRequest{Chain: destination.Name, Payload: raw, CandidateSignature: candidateSig}
			missing := request
			missing.CandidateSignature = nil
			require.ErrorContains(t, w.acceptRequest(missing), "lacks candidate proof")
			wrong := request
			wrong.CandidateSignature, err = adapter.SignMembership(keys[0], m, time.Now())
			require.NoError(t, err)
			require.ErrorContains(t, w.acceptRequest(wrong), "invalid activation candidate signature")
			require.Empty(t, w.requests, "failed consent checks must not create a pending membership")

			info := state.validators[candidate]
			info.Active = false
			require.ErrorContains(t, w.acceptRequest(request), "not active in XGR PoS")
			info.Active = true
			info.Exists = false
			require.ErrorContains(t, w.acceptRequest(request), "not active in XGR PoS")
			info.Exists = true
			w.state = &authorizationNilCandidateState{StateReader: state, candidate: candidate}
			require.ErrorContains(t, w.acceptRequest(request), "not active in XGR PoS")
			w.state = state

			stale := m
			stale.ExpectedSetID--
			staleRaw, err := adapter.EncodeMembership(stale)
			require.NoError(t, err)
			staleSignature, err := adapter.SignMembership(keys[3], stale, time.Now())
			require.NoError(t, err)
			require.ErrorContains(t, w.acceptRequest(wireRequest{Chain: destination.Name, Payload: staleRaw, CandidateSignature: staleSignature}), "stale request")
			expired := m
			expired.ValidUntil = uint64(time.Now().Add(-time.Minute).Unix())
			expiredRaw, err := adapter.EncodeMembership(expired)
			require.NoError(t, err)
			require.ErrorContains(t, w.acceptRequest(wireRequest{Chain: destination.Name, Payload: expiredRaw, CandidateSignature: candidateSig}), "expired")

			if scheme == authorization.SchemeECDSA {
				replay := m
				replay.Registry = types.StringToAddress("0x8888")
				replayRaw, err := adapter.EncodeMembership(replay)
				require.NoError(t, err)
				replaySignature, err := adapter.SignMembership(keys[3], replay, time.Now())
				require.NoError(t, err)
				require.ErrorContains(t, w.acceptRequest(wireRequest{Chain: destination.Name, Payload: replayRaw, CandidateSignature: replaySignature}), "registry binding mismatch")
			}
			require.NoError(t, w.acceptRequest(request))
			hash := crypto.Keccak256Hash(raw)
			require.NotNil(t, w.requests[hash])
			require.Empty(t, w.requests[hash].votes, "candidate consent is separate from current-member quorum votes")
			require.Len(t, set.Validators, 3, "accepting a request cannot mutate the registry snapshot")
			require.Equal(t, -1, indexOf(set.Validators, candidate), "joining requires a separately committed registry transition")
			require.NoError(t, w.acceptRequest(request))
			require.Len(t, w.requests, 1, "repeated request is idempotent")
		})
	}
}

func TestAuthorizationParityMembershipRemovalPreservesVoluntaryAndForcedRules(t *testing.T) {
	for _, scheme := range []string{authorization.SchemeBLS, authorization.SchemeECDSA} {
		t.Run(scheme, func(t *testing.T) {
			adapter, set, keys, state, destination := authorizationParityFixture(t, scheme, 3)
			original := getMembershipValidatorSet
			currentSet := set
			getMembershipValidatorSet = func(got *evm.Destination) (*evm.ValidatorSet, error) {
				require.Same(t, destination, got)
				return currentSet, nil
			}
			defer func() { getMembershipValidatorSet = original }()
			w := &Worker{state: state, destinations: map[string]*evm.Destination{destination.Name: destination}, requests: map[types.Hash]*requestState{}}
			target := set.Validators[2]
			m, err := w.membershipPayload(destination, adapter, set.SetID, protocol.ActionRemoveValidator, target, set.VerificationKeys[2])
			require.NoError(t, err)
			raw, err := adapter.EncodeMembership(m)
			require.NoError(t, err)
			signature, err := adapter.SignMembership(keys[2], m, time.Now())
			require.NoError(t, err)
			voluntary := wireRequest{Chain: destination.Name, Payload: raw, CandidateSignature: signature}
			require.NoError(t, w.acceptRequest(voluntary))
			require.False(t, w.requests[crypto.Keccak256Hash(raw)].request.Forced)
			w.requests = map[types.Hash]*requestState{}
			noConsent := voluntary
			noConsent.CandidateSignature = nil
			require.ErrorContains(t, w.acceptRequest(noConsent), "voluntary removal lacks")
			wrongConsent := voluntary
			wrongConsent.CandidateSignature, err = adapter.SignMembership(keys[0], m, time.Now())
			require.NoError(t, err)
			require.ErrorContains(t, w.acceptRequest(wrongConsent), "invalid voluntary removal")
			forced := wireRequest{Chain: destination.Name, Payload: raw, Forced: true}
			require.ErrorContains(t, w.acceptRequest(forced), "remains eligible")

			// A changed BLS staking key makes a BLS membership obsolete. It must
			// not turn an active ECDSA address into a forced-removal candidate.
			originalBLS := state.validators[target].BLSPubKey
			state.validators[target].BLSPubKey = []byte{1, 2, 3}
			if scheme == authorization.SchemeECDSA {
				require.ErrorContains(t, w.acceptRequest(forced), "remains eligible")
				require.NoError(t, w.acceptRequest(voluntary))
			} else {
				require.NoError(t, w.acceptRequest(forced))
			}
			state.validators[target].BLSPubKey = originalBLS
			state.validators[target].Active = false
			w.requests = map[types.Hash]*requestState{}
			require.ErrorContains(t, w.acceptRequest(voluntary), "must be marked forced")
			require.NoError(t, w.acceptRequest(forced), "inactive PoS member can be removed without its consent")
			require.True(t, w.requests[crypto.Keccak256Hash(raw)].request.Forced)
			require.Empty(t, w.requests[crypto.Keccak256Hash(raw)].request.CandidateSignature)
			w.state = &authorizationNilCandidateState{StateReader: state, candidate: target}
			require.NoError(t, w.acceptRequest(forced), "missing PoS member can be removed without its consent")
			w.state = state

			single := *set
			single.Validators = []types.Address{target}
			single.VerificationKeys = [][]byte{set.VerificationKeys[2]}
			if scheme == authorization.SchemeBLS {
				single.BLSPublicKeys = single.VerificationKeys
			}
			currentSet = &single
			require.ErrorContains(t, w.acceptRequest(forced), "final interchain validator")
			require.ErrorContains(t, w.acceptRequest(voluntary), "final interchain validator")
		})
	}
}

func TestAuthorizationParityMembershipVotesBindChainSetAndEligibleSigner(t *testing.T) {
	for _, scheme := range []string{authorization.SchemeBLS, authorization.SchemeECDSA} {
		t.Run(scheme, func(t *testing.T) {
			adapter, fullSet, keys, state, destination := authorizationParityFixture(t, scheme, 4)
			set := *fullSet
			set.Validators = fullSet.Validators[:3]
			set.VerificationKeys = fullSet.VerificationKeys[:3]
			if scheme == authorization.SchemeBLS {
				set.BLSPublicKeys = fullSet.BLSPublicKeys[:3]
			}
			candidate := fullSet.Validators[3]
			reads := 0
			original := getMembershipValidatorSet
			getMembershipValidatorSet = func(got *evm.Destination) (*evm.ValidatorSet, error) {
				reads++
				require.Same(t, destination, got)
				return &set, nil
			}
			defer func() { getMembershipValidatorSet = original }()
			alias := *destination
			alias.Name = "another-destination"
			w := &Worker{state: state, destinations: map[string]*evm.Destination{destination.Name: destination, alias.Name: &alias}, requests: map[types.Hash]*requestState{}}
			m, err := w.membershipPayload(destination, adapter, set.SetID, protocol.ActionAddValidator, candidate, fullSet.VerificationKeys[3])
			require.NoError(t, err)
			raw, err := adapter.EncodeMembership(m)
			require.NoError(t, err)
			consent, err := adapter.SignMembership(keys[3], m, time.Now())
			require.NoError(t, err)
			require.NoError(t, w.acceptRequest(wireRequest{Chain: destination.Name, Payload: raw, CandidateSignature: consent}))
			sig0, err := adapter.SignMembership(keys[0], m, time.Now())
			require.NoError(t, err)
			vote := wireVote{Chain: destination.Name, Payload: raw, Signer: set.Validators[0], Signature: sig0}
			wrongChain := vote
			wrongChain.Chain = alias.Name
			beforeReads := reads
			require.ErrorContains(t, w.acceptVote(wrongChain), "destination differs from accepted request")
			require.Equal(t, beforeReads, reads, "another chain must not select the validator snapshot for an existing request")
			hash := crypto.Keccak256Hash(raw)
			require.Empty(t, w.requests[hash].votes)
			outside := vote
			outside.Signer = candidate
			outside.Signature = consent
			require.ErrorContains(t, w.acceptVote(outside), "not in current interchain set", "candidate consent never counts as current-member quorum")
			forged := vote
			forged.Signer = set.Validators[1]
			require.ErrorContains(t, w.acceptVote(forged), "invalid interchain vote")
			state.validators[vote.Signer].Active = false
			require.ErrorContains(t, w.acceptVote(vote), "not currently eligible")
			state.validators[vote.Signer].Active = true
			require.NoError(t, w.acceptVote(vote))
			require.Len(t, w.requests[hash].votes, 1)
			require.NoError(t, w.acceptVote(vote))
			require.Len(t, w.requests[hash].votes, 1, "repeated vote cannot count twice")
			sig1, err := adapter.SignMembership(keys[1], m, time.Now())
			require.NoError(t, err)
			require.NoError(t, w.acceptVote(wireVote{Chain: destination.Name, Payload: raw, Signer: set.Validators[1], Signature: sig1}))
			require.Len(t, w.requests[hash].votes, 2)
			// Both accepted votes also form a genuine quorum in the selected codec.
			proof, err := adapter.AssembleMembershipQuorum(set.AuthorizationSet(), []authorization.Vote{
				{ValidatorIndex: 0, Signature: w.requests[hash].votes[set.Validators[0]]},
				{ValidatorIndex: 1, Signature: w.requests[hash].votes[set.Validators[1]]},
			}, m)
			require.NoError(t, err)
			require.NoError(t, adapter.VerifyMembershipQuorum(set.AuthorizationSet(), m, proof))
			set.SetID++
			require.ErrorContains(t, w.acceptVote(vote), "stale or expired membership vote")
			set.SetID--
			w.requests[hash].payload.ValidUntil = uint64(time.Now().Add(-time.Minute).Unix())
			require.ErrorContains(t, w.acceptVote(vote), "stale or expired membership vote")
		})
	}
}
