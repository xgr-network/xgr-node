package jsonrpc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
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

func TestXGRGetILNGovernanceQuorum(t *testing.T) {
	dataDir := t.TempDir()
	proposalID := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dir := filepath.Join(dataDir, "interchain", "governance", "quorums")
	require.NoError(t, os.MkdirAll(dir, 0o770))

	value := interchainGovernanceQuorumRPC{
		Version:                      "XGR_ILN_GOVERNANCE_V2",
		ProposalID:                   proposalID,
		ProposalType:                 1,
		SourceChainID:                8453,
		SourceDomain:                 8453,
		Registry:                     "0x5555555555555555555555555555555555555555",
		DestinationDomain:            1643,
		RouteID:                      "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ValidatorFeeWei:              "25",
		SetID:                        7,
		Nonce:                        12,
		ValidUntil:                   1900000000,
		Payload:                      "0x01",
		SignerBitmap:                 "0x03",
		AggregateSignature:           "0x02",
		AggregateSignatureCompressed: "0x03",
	}
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, proposalID+".json"), raw, 0o660))

	ep := newXGREndpoint(nil, dataDir)
	got, err := ep.GetILNGovernanceQuorum(proposalID)
	require.NoError(t, err)
	require.Equal(t, proposalID, got.ProposalID)
	require.Equal(t, uint64(7), got.SetID)
	require.Equal(t, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", got.RouteID)
	require.Equal(t, "25", got.ValidatorFeeWei)
	require.Equal(t, "0x03", got.SignerBitmap)
	require.Equal(t, "0x02", got.AggregateSignature)
}


func TestXGRGetILNGovernanceQuorumRejectsLegacyVersion(t *testing.T) {
	dataDir := t.TempDir()
	proposalID := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dir := filepath.Join(dataDir, "interchain", "governance", "quorums")
	require.NoError(t, os.MkdirAll(dir, 0o770))

	value := interchainGovernanceQuorumRPC{
		Version:            "XGR_ILN_GOVERNANCE_V1",
		ProposalID:         proposalID,
		ProposalType:       1,
		SourceChainID:      8453,
		SourceDomain:       8453,
		Registry:           "0x5555555555555555555555555555555555555555",
		DestinationDomain:  1643,
		RouteID:            "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SetID:              7,
		Nonce:              12,
		ValidUntil:         1900000000,
		Payload:            "0x01",
		SignerBitmap:       "0x03",
		AggregateSignature: "0x02",
	}
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, proposalID+".json"), raw, 0o660))

	ep := newXGREndpoint(nil, dataDir)
	_, err = ep.GetILNGovernanceQuorum(proposalID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "incomplete")
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
