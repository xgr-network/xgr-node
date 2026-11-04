package blspop

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	protocol "github.com/xgr-network/xgr-node/consensus/ibft/blspop"
	"github.com/xgr-network/xgr-node/types"
)

func configureCommand() *cobra.Command {
	var source, manifestFile, out string
	var height uint64
	cmd := &cobra.Command{Use: "configure", Short: "Write a separate candidate chain configuration with one explicit PoP gate", Args: cobra.NoArgs}
	cmd.Flags().StringVar(&source, "chain", "", "existing chain configuration (read only)")
	cmd.Flags().StringVar(&manifestFile, "manifest", "", "verified canonical manifest")
	cmd.Flags().Uint64Var(&height, "activation-height", 0, "explicitly chosen activation height matching the manifest")
	cmd.Flags().StringVar(&out, "out", "", "new candidate configuration file (never overwritten)")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if source == "" || manifestFile == "" || out == "" || height == 0 {
			return fmt.Errorf("provide --chain, --manifest, --activation-height and --out")
		}
		original, err := readBounded(source, 32*1024*1024)
		if err != nil {
			return err
		}
		var config map[string]json.RawMessage
		if err := json.Unmarshal(original, &config); err != nil {
			return err
		}
		var params map[string]json.RawMessage
		if err := json.Unmarshal(config["params"], &params); err != nil || params == nil {
			return fmt.Errorf("chain parameters are required")
		}
		var chainID uint64
		if err := json.Unmarshal(params["chainID"], &chainID); err != nil || chainID == 0 {
			return fmt.Errorf("invalid chain ID")
		}
		raw, err := readBounded(manifestFile, protocol.MaxManifestBytes)
		if err != nil {
			return err
		}
		_, verified, err := protocol.DecodeManifest(raw, chainID, height)
		if err != nil {
			return err
		}
		var forks map[string]json.RawMessage
		if existing := params["forks"]; len(existing) > 0 {
			if err := json.Unmarshal(existing, &forks); err != nil {
				return err
			}
		}
		if forks == nil {
			forks = map[string]json.RawMessage{}
		}
		if existing := forks["stakingBlsPoP"]; len(existing) > 0 {
			var gate struct {
				Block uint64 `json:"block"`
			}
			if err := json.Unmarshal(existing, &gate); err != nil || gate.Block != height {
				return fmt.Errorf("existing PoP activation conflicts with requested height")
			}
		} else {
			forks["stakingBlsPoP"], err = json.Marshal(struct {
				Block uint64 `json:"block"`
			}{height})
			if err != nil {
				return err
			}
		}
		migration := struct {
			Manifest string     `json:"manifest"`
			Digest   types.Hash `json:"digest"`
		}{string(raw), verified.Digest()}
		if existing := params["stakingBlsPoPMigration"]; len(existing) > 0 {
			var prior struct {
				Manifest string     `json:"manifest"`
				Digest   types.Hash `json:"digest"`
			}
			if err := json.Unmarshal(existing, &prior); err != nil || prior != migration {
				return fmt.Errorf("existing PoP migration conflicts with requested manifest")
			}
		}
		params["stakingBlsPoPMigration"], err = json.Marshal(migration)
		if err != nil {
			return err
		}
		params["forks"], err = json.Marshal(forks)
		if err != nil {
			return err
		}
		config["params"], err = json.Marshal(params)
		if err != nil {
			return err
		}
		candidate, err := canonical(config)
		if err != nil {
			return err
		}
		if err := writeNew(out, candidate); err != nil {
			return err
		}
		cmd.Printf("Candidate configuration written to %s\nDigest: %s\nThe input configuration was preserved. This command does not deploy or activate the fork.\n", out, verified.Digest())
		return nil
	}
	return cmd
}
