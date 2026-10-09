package jsonrpc

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

func writeTestILNAttestation(t *testing.T, dataDir, chain, messageID, root string) {
	t.Helper()
	dir := filepath.Join(dataDir, "interchain", "attestations", chain)
	require.NoError(t, os.MkdirAll(dir, 0o770))
	value := interchainAttestationRPC{
		Version:             "XGR_ILN_CHECKPOINT_V2",
		Chain:               chain,
		Destination:         "xgr",
		OriginChainID:       8453,
		OriginDomain:        8453,
		DestinationDomain:   1643,
		RouteID:             "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SetID:               7,
		SourceBlockNumber:   100,
		Registry:            "0x5555555555555555555555555555555555555555",
		Gateway:             "0x1111111111111111111111111111111111111111",
		SourceRouter:        "0x6666666666666666666666666666666666666666",
		Mailbox:             "0x2222222222222222222222222222222222222222",
		MerkleTreeHook:      "0x3333333333333333333333333333333333333333",
		DestinationRouter:   "0x4444444444444444444444444444444444444444",
		ValidatorFeeWei:     "10",
		AuthorizedMessageID: messageID,
		Root:                root,
		Index:               42,
		Payload:             "0x01",
		SignerBitmap:        "0x03",
		AggregateSignature:  "0x02",
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
	require.Equal(t, root, got.Root)
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
	require.Equal(t, root, got.Root)
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
		SetID: 7, Nonce: 12, ValidUntil: 1900000000,
		ValidatorFeeWei: big.NewInt(25),
	}
	payload, err := p.MarshalBinary()
	require.NoError(t, err)
	id := crypto.Keccak256Hash(payload).String()
	value := &interchainSourceFeeQuorumRPC{
		Version: protocol.ILNFeeDomainV315, ProposalID: id,
		SourceChainID: p.SourceChainID, SourceDomain: p.SourceDomain,
		Registry: p.Registry.String(), ValidatorFeeWei: p.ValidatorFeeWei.String(),
		SetID: p.SetID, Nonce: p.Nonce, ValidUntil: p.ValidUntil,
		Payload: "0x" + hex.EncodeToString(payload),
		SignerBitmap: "0x03", AggregateSignature: "0x02",
		AggregateSignatureCompressed: "0x03",
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
 dir:=t.TempDir()
 messageID:="0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
 root:="0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
 writeTestILNAttestation(t,dir,"base_to_xgr",messageID,root)
 legacy:=filepath.Join(dir,"interchain","attestations","base_to_xgr",messageID+".json")
 raw,err:=os.ReadFile(legacy)
 require.NoError(t,err)
 setDir:=filepath.Join(dir,"interchain","attestations","base_to_xgr",messageID)
 require.NoError(t,os.MkdirAll(setDir,0o770))
 require.NoError(t,os.WriteFile(filepath.Join(setDir,"7.json"),raw,0o660))
 ep:=newXGREndpoint(nil,dir)
 got,err:=ep.GetILNQuorumAttestation("BASE_TO_XGR",messageID,7)
 require.NoError(t,err)
 require.Equal(t,uint64(7),got.SetID)
 require.Equal(t,messageID,got.AuthorizedMessageID)
 _,err=ep.GetILNQuorumAttestation("base_to_xgr",messageID,8)
 require.Error(t,err)
 _,err=ep.GetILNQuorumAttestation("../base",messageID,7)
 require.Error(t,err)
 _,err=ep.GetILNQuorumAttestation("base_to_xgr",messageID,0)
 require.Error(t,err)
}
