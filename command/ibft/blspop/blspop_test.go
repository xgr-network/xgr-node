package blspop

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	protocol "github.com/xgr-network/xgr-node/consensus/ibft/blspop"
	"github.com/xgr-network/xgr-node/secrets"
	secretsHelper "github.com/xgr-network/xgr-node/secrets/helper"
)

func TestOfflineProofCollectionRoundTrip(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) (string, error) {
		cmd := GetCommand()
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return buf.String(), err
	}
	proofs := make([]string, 2)
	for i := range proofs {
		dir := filepath.Join(root, string(rune('a'+i)))
		sm, err := secretsHelper.SetupLocalSecretsManager(dir)
		require.NoError(t, err)
		_, err = secretsHelper.InitECDSAValidatorKey(sm)
		require.NoError(t, err)
		_, err = secretsHelper.InitBLSValidatorKey(sm)
		require.NoError(t, err)
		proofs[i] = filepath.Join(root, string(rune('a'+i))+".json")
		output, err := run("proof", "--data-dir", dir, "--chain-id", "1643", "--out", proofs[i])
		require.NoError(t, err)
		raw, err := os.ReadFile(proofs[i])
		require.NoError(t, err)
		for _, secret := range []string{secrets.ValidatorKey, secrets.ValidatorBLSKey} {
			before, err := sm.GetSecret(secret)
			require.NoError(t, err)
			require.False(t, bytes.Contains(raw, before), "private test key leaked into public proof")
			require.False(t, bytes.Contains([]byte(output), before), "private test key leaked into output")
			_, err = run("proof", "--data-dir", dir, "--chain-id", "1643", "--out", proofs[i])
			require.Error(t, err, "existing proof files must not be overwritten")
			after, err := sm.GetSecret(secret)
			require.NoError(t, err)
			require.True(t, bytes.Equal(before, after), "proof creation must preserve key files")
		}
	}
	one, two := filepath.Join(root, "manifest1.json"), filepath.Join(root, "manifest2.json")
	_, err := run("assemble", "--proof", proofs[0], "--proof", proofs[1], "--chain-id", "1643", "--activation-height", "100", "--out", one)
	require.NoError(t, err)
	_, err = run("assemble", "--proof", proofs[1], "--proof", proofs[0], "--chain-id", "1643", "--activation-height", "100", "--out", two)
	require.NoError(t, err)
	a, err := os.ReadFile(one)
	require.NoError(t, err)
	b, err := os.ReadFile(two)
	require.NoError(t, err)
	require.Equal(t, a, b, "assembly must not depend on proof-file input order")
	_, verified, err := protocol.DecodeManifest(a, 1643, 100)
	require.NoError(t, err)
	output, err := run("verify", "--manifest", one, "--chain-id", "1643", "--activation-height", "100")
	require.NoError(t, err)
	require.Contains(t, output, verified.Digest().String())
	chainFile := filepath.Join(root, "chain.json")
	original := []byte(`{"params":{"chainID":1643,"forks":{"securityAccounting":{"block":50}},"engine":{"ibft":{"type":"PoS","validator_type":"bls"}}},"name":"isolated-fixture"}`)
	require.NoError(t, os.WriteFile(chainFile, original, 0600))
	candidatePath := filepath.Join(root, "chain-pop-candidate.json")
	_, err = run("configure", "--chain", chainFile, "--manifest", one, "--activation-height", "100", "--out", candidatePath)
	require.NoError(t, err)
	stillOriginal, err := os.ReadFile(chainFile)
	require.NoError(t, err)
	require.Equal(t, original, stillOriginal)
	candidate, err := os.ReadFile(candidatePath)
	require.NoError(t, err)
	var configured struct {
		Params struct {
			ChainID uint64 `json:"chainID"`
			Forks   map[string]struct {
				Block uint64 `json:"block"`
			} `json:"forks"`
			Migration struct {
				Manifest string `json:"manifest"`
				Digest   string `json:"digest"`
			} `json:"stakingBlsPoPMigration"`
		} `json:"params"`
	}
	require.NoError(t, json.Unmarshal(candidate, &configured))
	require.Equal(t, uint64(1643), configured.Params.ChainID)
	require.Len(t, configured.Params.Forks, 2)
	require.Equal(t, uint64(50), configured.Params.Forks["securityAccounting"].Block)
	require.Equal(t, uint64(100), configured.Params.Forks["stakingBlsPoP"].Block)
	require.Equal(t, string(a), configured.Params.Migration.Manifest)
	require.Equal(t, verified.Digest().String(), configured.Params.Migration.Digest)
	_, err = run("verify", "--manifest", one, "--chain-id", "1887", "--activation-height", "100")
	require.Error(t, err)
	_, err = run("assemble", "--proof", proofs[0], "--proof", proofs[0], "--chain-id", "1643", "--activation-height", "100", "--out", filepath.Join(root, "duplicate.json"))
	require.Error(t, err)
}
