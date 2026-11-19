package jsonrpc

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/interchain/authorization"
	"github.com/xgr-network/xgr-node/types"
)

func testArchiveBLSProof(t *testing.T, payload []byte) (bitmap, expanded, compressed string) {
	t.Helper()
	keys := make([][]byte, 2)
	votes := make([]protocol.Vote, 2)
	for i := range keys {
		key, err := crypto.GenerateBLSKey()
		require.NoError(t, err)
		keys[i], err = crypto.BLSSecretKeyToPubkeyBytes(key)
		require.NoError(t, err)
		sig, err := crypto.SignByBLS(key, payload)
		require.NoError(t, err)
		votes[i] = protocol.Vote{ValidatorIndex: i, Signature: sig}
	}
	bits, aggregate, err := protocol.AggregateVotes(keys, votes, payload)
	require.NoError(t, err)
	uncompressed, err := crypto.BLSSignatureToEIP2537(aggregate)
	require.NoError(t, err)
	return "0x" + hex.EncodeToString(bits.Bytes()), "0x" + hex.EncodeToString(uncompressed), "0x" + hex.EncodeToString(aggregate)
}

func writeTestILNAttestation(t *testing.T, dataDir, chain, messageID, _ string) {
	t.Helper()
	dir := filepath.Join(dataDir, "interchain", "attestations", chain)
	require.NoError(t, os.MkdirAll(dir, 0o770))
	p := protocol.XITADirectMessage{
		SourceChainID: 8453, SourceDomain: 8453, DestinationDomain: 1643,
		RouteID: types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		SetID:   7, SourceBlockNumber: 100,
		SourceRegistry:    types.StringToAddress("0x5555555555555555555555555555555555555555"),
		Gateway:           types.StringToAddress("0x1111111111111111111111111111111111111111"),
		SourceRouter:      types.StringToAddress("0x6666666666666666666666666666666666666666"),
		SourceMailbox:     types.StringToAddress("0x2222222222222222222222222222222222222222"),
		DestinationRouter: types.StringToAddress("0x4444444444444444444444444444444444444444"),
		ValidatorFeeWei:   big.NewInt(10), MessageID: types.StringToHash(messageID),
		DestinationISM:      types.StringToAddress("0x8888888888888888888888888888888888888888"),
		DestinationRegistry: types.StringToAddress("0x9999999999999999999999999999999999999999"),
	}
	rawPayload, err := p.MarshalBinary()
	require.NoError(t, err)
	bitmap, expanded, compressed := testArchiveBLSProof(t, rawPayload)
	value := interchainAttestationRPC{
		Version: protocol.XITADirectMessageDomain,
		Chain:   chain, Destination: "xgr", OriginChainID: p.SourceChainID,
		OriginDomain: p.SourceDomain, DestinationDomain: p.DestinationDomain,
		RouteID: p.RouteID.String(), SetID: p.SetID, SourceBlockNumber: p.SourceBlockNumber,
		Registry: p.SourceRegistry.String(), Gateway: p.Gateway.String(),
		SourceRouter: p.SourceRouter.String(), Mailbox: p.SourceMailbox.String(),
		DestinationRouter: p.DestinationRouter.String(), ValidatorFeeWei: p.ValidatorFeeWei.String(),
		AuthorizedMessageID: p.MessageID.String(), DestinationISM: p.DestinationISM.String(),
		DestinationRegistry: p.DestinationRegistry.String(),
		Payload:             "0x" + hex.EncodeToString(rawPayload), SignerBitmap: bitmap,
		AggregateSignature:           expanded,
		AggregateSignatureCompressed: compressed,
	}
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "latest.json"), raw, 0o660))
	require.NoError(t, os.WriteFile(filepath.Join(dir, messageID+".json"), raw, 0o660))
}

func TestXGRGetInterchainAttestationReadsLatestILNStore(t *testing.T) {
	dataDir := t.TempDir()
	messageID := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	root := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	writeTestILNAttestation(t, dataDir, "base_to_xgr", messageID, root)

	ep := newXGREndpoint(nil, dataDir)
	got, err := ep.GetInterchainAttestation("BASE_TO_XGR")
	require.NoError(t, err)
	require.Equal(t, messageID, got.AuthorizedMessageID)
	require.Equal(t, "0x8888888888888888888888888888888888888888", got.DestinationISM)
	require.Equal(t, "0x6666666666666666666666666666666666666666", got.SourceRouter)
}

func TestXGRGetILNInterchainAttestationByMessageID(t *testing.T) {
	dataDir := t.TempDir()
	messageID := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	root := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	writeTestILNAttestation(t, dataDir, "base_to_xgr", messageID, root)

	ep := newXGREndpoint(nil, dataDir)
	got, err := ep.GetILNInterchainAttestation("base_to_xgr", messageID)
	require.NoError(t, err)
	require.Equal(t, messageID, got.AuthorizedMessageID)
	require.Equal(t, "0x8888888888888888888888888888888888888888", got.DestinationISM)
}

func TestXGRGetILNInterchainAttestationRejectsUnsafeLookup(t *testing.T) {
	ep := newXGREndpoint(nil, t.TempDir())
	_, err := ep.GetILNInterchainAttestation("../base", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	require.Error(t, err)
	_, err = ep.GetILNInterchainAttestation("base_to_xgr", "0x1234")
	require.Error(t, err)
}

func writeTestILNFeeQuorum(t *testing.T, dataDir string) (string, *interchainSourceFeeQuorumRPC) {
	t.Helper()
	p := protocol.ILNSourceFeeProposal{
		SourceChainID: 8453, SourceDomain: 8453,
		Registry: types.StringToAddress("0x5555555555555555555555555555555555555555"),
		SetID:    7, Nonce: 12, ValidUntil: 1900000000,
		ValidatorFeeWei: big.NewInt(25),
	}
	payload, err := p.MarshalBinary()
	require.NoError(t, err)
	id := crypto.Keccak256Hash(payload).String()
	bitmap, expanded, compressed := testArchiveBLSProof(t, payload)
	value := &interchainSourceFeeQuorumRPC{
		Version: protocol.ILNFeeDomainV315, ProposalID: id,
		SourceChainID: p.SourceChainID, SourceDomain: p.SourceDomain,
		Registry: p.Registry.String(), ValidatorFeeWei: p.ValidatorFeeWei.String(),
		SetID: p.SetID, Nonce: p.Nonce, ValidUntil: p.ValidUntil,
		Payload:      "0x" + hex.EncodeToString(payload),
		SignerBitmap: bitmap, AggregateSignature: expanded,
		AggregateSignatureCompressed: compressed,
	}
	writeTestILNFeeQuorumFile(t, dataDir, id, value)
	return id, value
}

func writeTestILNFeeQuorumFile(t *testing.T, dataDir, id string, value *interchainSourceFeeQuorumRPC) {
	t.Helper()
	dir := filepath.Join(dataDir, "interchain", "governance", "quorums")
	require.NoError(t, os.MkdirAll(dir, 0o770))
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".json"), raw, 0o660))
}

func TestXGRGetILNGovernanceQuorumV315Fee(t *testing.T) {
	dir := t.TempDir()
	id, _ := writeTestILNFeeQuorum(t, dir)
	got, err := newXGREndpoint(nil, dir).GetILNGovernanceQuorum(id)
	require.NoError(t, err)
	require.Equal(t, id, got.ProposalID)
	require.Equal(t, protocol.ILNFeeDomainV315, got.Version)
	require.Equal(t, uint64(8453), got.SourceChainID)
	require.Equal(t, uint32(8453), got.SourceDomain)
	require.Equal(t, uint64(7), got.SetID)
	require.Equal(t, uint64(12), got.Nonce)
	require.Equal(t, "25", got.ValidatorFeeWei)
	require.Equal(t, "0x03", got.SignerBitmap)
}

func TestXGRGetILNGovernanceQuorumRejectsLegacyVersion(t *testing.T) {
	dir := t.TempDir()
	id, value := writeTestILNFeeQuorum(t, dir)
	value.Version = "XGR_ILN_GOVERNANCE_V2"
	writeTestILNFeeQuorumFile(t, dir, id, value)
	_, err := newXGREndpoint(nil, dir).GetILNGovernanceQuorum(id)
	require.ErrorContains(t, err, "incomplete")
}

func TestXGRGetILNGovernanceQuorumRejectsForgedFee(t *testing.T) {
	dir := t.TempDir()
	id, value := writeTestILNFeeQuorum(t, dir)
	value.ValidatorFeeWei = "200"
	writeTestILNFeeQuorumFile(t, dir, id, value)
	_, err := newXGREndpoint(nil, dir).GetILNGovernanceQuorum(id)
	require.ErrorContains(t, err, "does not match")
}

func TestXGRGetILNGovernanceQuorumRejectsWrongProposalID(t *testing.T) {
	dir := t.TempDir()
	id, value := writeTestILNFeeQuorum(t, dir)
	forged := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	require.NotEqual(t, id, forged)
	value.ProposalID = forged
	writeTestILNFeeQuorumFile(t, dir, forged, value)
	_, err := newXGREndpoint(nil, dir).GetILNGovernanceQuorum(forged)
	require.ErrorContains(t, err, "does not match")
}

func TestXGRGetILNGovernanceQuorumRejectsUnsafeID(t *testing.T) {
	ep := newXGREndpoint(nil, t.TempDir())
	_, err := ep.GetILNGovernanceQuorum("../proposal")
	require.Error(t, err)
	_, err = ep.GetILNGovernanceQuorum("0x1234")
	require.Error(t, err)
}

func TestXGRGetILNGovernanceQuorumNotFound(t *testing.T) {
	ep := newXGREndpoint(nil, t.TempDir())
	_, err := ep.GetILNGovernanceQuorum(
		"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestXGRSetSpecificILNQuorumRetrieval(t *testing.T) {
	dir := t.TempDir()
	messageID := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	root := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	writeTestILNAttestation(t, dir, "base_to_xgr", messageID, root)
	legacy := filepath.Join(dir, "interchain", "attestations", "base_to_xgr", messageID+".json")
	raw, err := os.ReadFile(legacy)
	require.NoError(t, err)
	setDir := filepath.Join(dir, "interchain", "attestations", "base_to_xgr", messageID)
	require.NoError(t, os.MkdirAll(setDir, 0o770))
	require.NoError(t, os.WriteFile(filepath.Join(setDir, "7.json"), raw, 0o660))
	ep := newXGREndpoint(nil, dir)
	got, err := ep.GetILNQuorumAttestation("BASE_TO_XGR", messageID, 7)
	require.NoError(t, err)
	require.Equal(t, uint64(7), got.SetID)
	require.Equal(t, messageID, got.AuthorizedMessageID)
	_, err = ep.GetILNQuorumAttestation("base_to_xgr", messageID, 8)
	require.Error(t, err)
	_, err = ep.GetILNQuorumAttestation("../base", messageID, 7)
	require.Error(t, err)
	_, err = ep.GetILNQuorumAttestation("base_to_xgr", messageID, 0)
	require.Error(t, err)
}

func testArchiveECDSASet(t *testing.T) (*authorization.Adapter, authorization.Set, []authorization.Keys) {
	t.Helper()
	a, err := authorization.ForScheme(authorization.SchemeECDSA)
	require.NoError(t, err)
	set := authorization.Set{SetID: 7}
	keys := make([]authorization.Keys, 9)
	for i := range keys {
		key, err := crypto.GenerateECDSAKey()
		require.NoError(t, err)
		keys[i].ECDSA = key
		address := crypto.PubKeyToAddress(&key.PublicKey)
		set.Validators = append(set.Validators, address)
		set.PublicKeys = append(set.PublicKeys, address.Bytes())
	}
	return a, set, keys
}

func testECDSAAttestation(t *testing.T, dir string) (string, *interchainAttestationRPC) {
	t.Helper()
	messageID := "0x" + strings.Repeat("a", 64)
	route := "base_to_xdc"
	writeTestILNAttestation(t, dir, route, messageID, "")
	path := filepath.Join(dir, "interchain", "attestations", route, messageID+".json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var out interchainAttestationRPC
	require.NoError(t, json.Unmarshal(raw, &out))
	rawPayload, err := hex.DecodeString(out.Payload[2:])
	require.NoError(t, err)
	var payload protocol.XITADirectMessage
	require.NoError(t, payload.UnmarshalBinary(rawPayload))
	payload.DestinationDomain = 50
	a, set, keys := testArchiveECDSASet(t)
	votes := make([]authorization.Vote, 0, 6)
	for _, i := range []int{8, 6, 5, 4, 3, 2} {
		sig, err := a.SignDirect(keys[i], payload)
		require.NoError(t, err)
		votes = append(votes, authorization.Vote{ValidatorIndex: i, Signature: sig})
	}
	proof, err := a.AssembleDirectQuorum(set, votes, payload)
	require.NoError(t, err)
	rawPayload, err = a.EncodeDirect(payload)
	require.NoError(t, err)
	out.Version = a.DirectDomain()
	out.Destination = "xdc"
	out.DestinationDomain = 50
	out.Payload = "0x" + hex.EncodeToString(rawPayload)
	out.SignerBitmap = "0x" + hex.EncodeToString(proof.Bitmap)
	out.SignatureScheme = authorization.SchemeECDSA
	out.Signatures = "0x" + hex.EncodeToString(proof.Signature)
	out.AggregateSignature = ""
	out.AggregateSignatureCompressed = ""
	return messageID, &out
}

func writeRPCArchive(t *testing.T, path string, value interface{}) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o770))
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o660))
}

func TestXGRReadsAndExportsExplicitECDSAQuorumArchive(t *testing.T) {
	dir := t.TempDir()
	id, value := testECDSAAttestation(t, dir)
	base := filepath.Join(dir, "interchain", "attestations", value.Chain)
	for _, path := range []string{filepath.Join(base, "latest.json"), filepath.Join(base, id+".json"), filepath.Join(base, id, "7.json")} {
		writeRPCArchive(t, path, value)
	}
	ep := newXGREndpoint(nil, dir)
	latest, err := ep.GetInterchainAttestation(value.Chain)
	require.NoError(t, err)
	byID, err := ep.GetILNInterchainAttestation(value.Chain, id)
	require.NoError(t, err)
	bySet, err := ep.GetILNQuorumAttestation(value.Chain, id, 7)
	require.NoError(t, err)
	for _, got := range []*interchainAttestationRPC{latest, byID, bySet} {
		require.Equal(t, value, got)
		require.Equal(t, "0x7c01", got.SignerBitmap)
		raw, err := json.Marshal(got)
		require.NoError(t, err)
		require.Contains(t, string(raw), `"signatureScheme":"ecdsa"`)
		require.Contains(t, string(raw), `"signatures":`)
		require.NotContains(t, string(raw), `"aggregateSignature"`)
		require.NotContains(t, string(raw), `"aggregateSignatureCompressed"`)
	}
	status, err := ep.GetILNQuorum(value.Chain, id)
	require.NoError(t, err)
	require.Equal(t, "AVAILABLE_UNVERIFIED", status.Status, "offline archive parsing must not claim current-set authority")
}

func TestXGRArchiveRejectsCrossSchemeAndMalformedProofs(t *testing.T) {
	dir := t.TempDir()
	id, original := testECDSAAttestation(t, dir)
	path := filepath.Join(dir, "interchain", "attestations", original.Chain, id+".json")
	for _, tc := range []struct {
		name   string
		mutate func(*interchainAttestationRPC)
	}{
		{"missing explicit scheme", func(a *interchainAttestationRPC) { a.SignatureScheme = "" }},
		{"wrong scheme", func(a *interchainAttestationRPC) { a.SignatureScheme = authorization.SchemeBLS }},
		{"unknown scheme", func(a *interchainAttestationRPC) { a.SignatureScheme = "guess" }},
		{"wrong direct domain", func(a *interchainAttestationRPC) { a.Version = protocol.XITADirectMessageDomain }},
		{"BLS field injected", func(a *interchainAttestationRPC) { a.AggregateSignature = "0x11" }},
		{"compressed BLS field injected", func(a *interchainAttestationRPC) { a.AggregateSignatureCompressed = "0x11" }},
		{"missing signature", func(a *interchainAttestationRPC) { a.Signatures = "" }},
		{"wrong signature count", func(a *interchainAttestationRPC) { a.SignerBitmap = "0x0300" }},
		{"unprefixed bitmap", func(a *interchainAttestationRPC) { a.SignerBitmap = "7c01" }},
		{"invalid recovery byte", func(a *interchainAttestationRPC) { a.Signatures = a.Signatures[:130] + "00" + a.Signatures[132:] }},
		{"forged signed field", func(a *interchainAttestationRPC) {
			a.DestinationRegistry = "0x1111111111111111111111111111111111111111"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := *original
			tc.mutate(&value)
			writeRPCArchive(t, path, &value)
			_, err := newXGREndpoint(nil, dir).GetILNInterchainAttestation(original.Chain, id)
			require.Error(t, err)
		})
	}
}

func TestXGRBLSArchiveCompatibilityAndProofConsistency(t *testing.T) {
	dir := t.TempDir()
	id := "0x" + strings.Repeat("a", 64)
	writeTestILNAttestation(t, dir, "base_to_xgr", id, "")
	ep := newXGREndpoint(nil, dir)
	got, err := ep.GetILNInterchainAttestation("base_to_xgr", id)
	require.NoError(t, err)
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"aggregateSignature"`)
	require.Contains(t, string(raw), `"aggregateSignatureCompressed"`)
	require.NotContains(t, string(raw), `"signatureScheme"`)
	require.NotContains(t, string(raw), `"signatures"`)
	got.AggregateSignature = "0x" + strings.Repeat("00", 256)
	path := filepath.Join(dir, "interchain", "attestations", "base_to_xgr", id+".json")
	writeRPCArchive(t, path, got)
	_, err = ep.GetILNInterchainAttestation("base_to_xgr", id)
	require.ErrorContains(t, err, "mismatched")
}

func TestXGRFeeQuorumECDSAUsesExplicitSchemeWithUnchangedProposalDomain(t *testing.T) {
	dir := t.TempDir()
	id, value := writeTestILNFeeQuorum(t, dir)
	raw, err := hex.DecodeString(value.Payload[2:])
	require.NoError(t, err)
	var proposal protocol.ILNSourceFeeProposal
	require.NoError(t, proposal.UnmarshalBinary(raw))
	a, set, keys := testArchiveECDSASet(t)
	votes := make([]authorization.Vote, 0, 6)
	for _, i := range []int{8, 6, 5, 4, 3, 2} {
		sig, err := a.SignSourceFee(keys[i], proposal)
		require.NoError(t, err)
		votes = append(votes, authorization.Vote{ValidatorIndex: i, Signature: sig})
	}
	proof, err := a.AssembleSourceFeeQuorum(set, votes, proposal)
	require.NoError(t, err)
	value.SignatureScheme = authorization.SchemeECDSA
	value.Signatures = "0x" + hex.EncodeToString(proof.Signature)
	value.SignerBitmap = "0x" + hex.EncodeToString(proof.Bitmap)
	value.AggregateSignature = ""
	value.AggregateSignatureCompressed = ""
	writeTestILNFeeQuorumFile(t, dir, id, value)
	got, err := newXGREndpoint(nil, dir).GetILNGovernanceQuorum(id)
	require.NoError(t, err)
	require.Equal(t, value, got)
	require.Equal(t, protocol.ILNFeeDomainV315, got.Version)
	require.Equal(t, crypto.Keccak256Hash(raw).String(), got.ProposalID)
	require.Equal(t, "0x7c01", got.SignerBitmap)
	value.SignatureScheme = ""
	writeTestILNFeeQuorumFile(t, dir, id, value)
	_, err = newXGREndpoint(nil, dir).GetILNGovernanceQuorum(id)
	require.Error(t, err, "shared proposal domain must never select ECDSA without explicit archive tagging")
}

func TestXGRArchiveLookupRejectsDifferentRequestedIdentity(t *testing.T) {
	dir := t.TempDir()
	id := "0x" + strings.Repeat("a", 64)
	writeTestILNAttestation(t, dir, "base_to_xgr", id, "")
	path := filepath.Join(dir, "interchain", "attestations", "base_to_xgr", id+".json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	other := "0x" + strings.Repeat("b", 64)
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), other+".json"), raw, 0o660))
	ep := newXGREndpoint(nil, dir)
	_, err = ep.GetILNInterchainAttestation("base_to_xgr", other)
	require.ErrorContains(t, err, "route/message mismatch")
	status, err := ep.GetILNQuorum("base_to_xgr", other)
	require.NoError(t, err)
	require.Equal(t, "NOT_FOUND", status.Status)
}
