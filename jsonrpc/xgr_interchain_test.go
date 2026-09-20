package jsonrpc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeTestAttestation(t *testing.T, dataDir, chain string, setID uint64, index uint32, root string) {
	t.Helper()
	dir := filepath.Join(dataDir, "interchain", "attestations", chain)
	require.NoError(t, os.MkdirAll(dir, 0o770))
	value := interchainAttestationRPC{
		Version:            "XGR_INTERCHAIN_CHECKPOINT_V1",
		Chain:              chain,
		OriginChainID:      1643,
		DestinationDomain:  8453,
		SetID:              setID,
		Mailbox:            "0x1111111111111111111111111111111111111111",
		MerkleTreeHook:     "0x2222222222222222222222222222222222222222",
		Root:               root,
		Index:              index,
		Payload:            "0x01",
		SignerBitmap:       "0x03",
		AggregateSignature: "0x02",
	}
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "latest.json"), raw, 0o660))
	name := "7-42-" + root + ".json"
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), raw, 0o660))
}

func TestXGRGetInterchainAttestationReadsWorkerStore(t *testing.T) {
	dataDir := t.TempDir()
	root := "0x3333333333333333333333333333333333333333333333333333333333333333"
	writeTestAttestation(t, dataDir, "base", 7, 42, root)

	ep := newXGREndpoint(nil, dataDir)
	got, err := ep.GetInterchainAttestation("BASE")
	require.NoError(t, err)
	require.Equal(t, uint64(7), got.SetID)
	require.Equal(t, uint32(42), got.Index)
	require.Equal(t, root, got.Root)
}

func TestXGRGetInterchainAttestationByCheckpoint(t *testing.T) {
	dataDir := t.TempDir()
	root := "0x3333333333333333333333333333333333333333333333333333333333333333"
	writeTestAttestation(t, dataDir, "base", 7, 42, root)

	ep := newXGREndpoint(nil, dataDir)
	got, err := ep.GetInterchainAttestationByCheckpoint("base", 7, 42, root)
	require.NoError(t, err)
	require.Equal(t, uint64(7), got.SetID)
	require.Equal(t, root, got.Root)
}

func TestXGRGetInterchainAttestationRejectsUnsafeLookup(t *testing.T) {
	ep := newXGREndpoint(nil, t.TempDir())
	_, err := ep.GetInterchainAttestation("../base")
	require.Error(t, err)
	_, err = ep.GetInterchainAttestationByCheckpoint("base", 0, 1, "0x3333333333333333333333333333333333333333333333333333333333333333")
	require.Error(t, err)
}
