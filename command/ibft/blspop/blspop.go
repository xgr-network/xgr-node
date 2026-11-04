package blspop

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"

	"github.com/hashicorp/go-hclog"
	"github.com/spf13/cobra"
	protocol "github.com/xgr-network/xgr-node/consensus/ibft/blspop"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/secrets"
	secretsHelper "github.com/xgr-network/xgr-node/secrets/helper"
	"github.com/xgr-network/xgr-node/secrets/local"
	"github.com/xgr-network/xgr-node/types"
)

type proofRecord struct {
	Version  uint32         `json:"version"`
	ChainID  uint64         `json:"chainId"`
	Contract string         `json:"stakingContract"`
	Entry    protocol.Entry `json:"entry"`
}

// GetCommand provides offline collection only. It never submits a transaction,
// creates a key, changes stake, or selects a production activation height.
func GetCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "bls-pop", Short: "Prepare and verify public staking BLS migration proofs offline"}
	cmd.AddCommand(proofCommand(), assembleCommand(), verifyCommand(), configureCommand())
	return cmd
}

func canonical(v interface{}) ([]byte, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func readBounded(filename string, limit int64) ([]byte, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("input exceeds size limit")
	}
	return raw, nil
}

func writeNew(filename string, raw []byte) error {
	f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(raw)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func proofCommand() *cobra.Command {
	var dataDir, config, out string
	var chainID uint64
	cmd := &cobra.Command{Use: "proof", Short: "Create a nonce-zero migration proof using existing local validator keys", Args: cobra.NoArgs}
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "existing local secrets directory")
	cmd.Flags().StringVar(&config, "config", "", "existing cloud secrets-manager configuration")
	cmd.Flags().StringVar(&out, "out", "", "new public-proof JSON file (never overwritten)")
	cmd.Flags().Uint64Var(&chainID, "chain-id", 0, "explicit chain ID for this proof")
	cmd.MarkFlagsMutuallyExclusive("data-dir", "config")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if (dataDir == "" && config == "") || out == "" || chainID == 0 {
			return fmt.Errorf("provide --data-dir or --config, --chain-id and --out")
		}
		var sm secrets.SecretsManager
		var err error
		if config != "" {
			cfg, readErr := secrets.ReadConfig(config)
			if readErr != nil {
				return readErr
			}
			sm, err = secretsHelper.InitCloudSecretsManager(cfg)
		} else {
			sm, err = local.SecretsManagerFactory(nil, &secrets.SecretsManagerParams{Logger: hclog.NewNullLogger(), Extra: map[string]interface{}{secrets.Path: dataDir}})
		}
		if err != nil {
			return err
		}
		ecdsaRaw, err := sm.GetSecret(secrets.ValidatorKey)
		if err != nil {
			return fmt.Errorf("load existing validator ECDSA key: %w", err)
		}
		ecdsaKey, err := crypto.BytesToECDSAPrivateKey(ecdsaRaw)
		if err != nil {
			return fmt.Errorf("parse validator ECDSA key: %w", err)
		}
		addr, err := crypto.GetAddressFromKey(ecdsaKey)
		if err != nil {
			return err
		}
		blsRaw, err := sm.GetSecret(secrets.ValidatorBLSKey)
		if err != nil {
			return fmt.Errorf("load existing validator BLS key: %w", err)
		}
		key, err := crypto.BytesToBLSSecretKey(blsRaw)
		if err != nil {
			return fmt.Errorf("parse validator BLS key: %w", err)
		}
		publicKey, err := crypto.BLSSecretKeyToPubkeyBytes(key)
		if err != nil {
			return err
		}
		contract := types.StringToAddress(protocol.StakingContractAddress)
		proof, err := crypto.CreateStakingPoP(key, new(big.Int).SetUint64(chainID), contract, addr, new(big.Int))
		if err != nil {
			return err
		}
		entry := protocol.Entry{Validator: "0x" + hex.EncodeToString(addr[:]), PublicKey: "0x" + hex.EncodeToString(publicKey), Proof: "0x" + hex.EncodeToString(proof)}
		// Validate the record before emitting any public proof artifact.
		if _, err := protocol.NewManifest(chainID, 1, []protocol.Entry{entry}); err != nil {
			return err
		}
		raw, err := canonical(proofRecord{Version: protocol.ManifestVersion, ChainID: chainID, Contract: protocol.StakingContractAddress, Entry: entry})
		if err != nil {
			return err
		}
		if err := writeNew(out, raw); err != nil {
			return err
		}
		cmd.Printf("Public possession proof written to %s\n", out)
		return nil
	}
	return cmd
}

func assembleCommand() *cobra.Command {
	var files []string
	var chainID, height uint64
	var out string
	cmd := &cobra.Command{Use: "assemble", Short: "Verify proofs and build a canonical migration manifest", Args: cobra.NoArgs}
	cmd.Flags().StringArrayVar(&files, "proof", nil, "public proof file (repeat for each address/key pair)")
	cmd.Flags().Uint64Var(&chainID, "chain-id", 0, "expected chain ID")
	cmd.Flags().Uint64Var(&height, "activation-height", 0, "proposed single activation height; does not activate it")
	cmd.Flags().StringVar(&out, "out", "", "new canonical manifest file (never overwritten)")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if len(files) == 0 || len(files) > protocol.MaxEntries || out == "" {
			return fmt.Errorf("provide --proof files and --out")
		}
		entries := make([]protocol.Entry, 0, len(files))
		for _, filename := range files {
			raw, err := readBounded(filename, 4096)
			if err != nil {
				return err
			}
			var record proofRecord
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&record); err != nil {
				return err
			}
			expected, err := canonical(record)
			if err != nil {
				return err
			}
			if !bytes.Equal(raw, expected) || record.Version != protocol.ManifestVersion || record.ChainID != chainID || record.Contract != protocol.StakingContractAddress {
				return fmt.Errorf("invalid public proof record %s", filename)
			}
			entries = append(entries, record.Entry)
		}
		manifest, err := protocol.NewManifest(chainID, height, entries)
		if err != nil {
			return err
		}
		raw, err := manifest.CanonicalJSON()
		if err != nil {
			return err
		}
		if err := writeNew(out, raw); err != nil {
			return err
		}
		verified, err := manifest.Verify(chainID, height)
		if err != nil {
			return err
		}
		cmd.Printf("Manifest written to %s\nDigest: %s\nParent-state and epoch-snapshot coverage still require independent validation.\n", out, verified.Digest())
		return nil
	}
	return cmd
}

func verifyCommand() *cobra.Command {
	var filename string
	var chainID, height uint64
	cmd := &cobra.Command{Use: "verify", Short: "Independently verify every public proof and report the manifest digest", Args: cobra.NoArgs}
	cmd.Flags().StringVar(&filename, "manifest", "", "canonical migration manifest file")
	cmd.Flags().Uint64Var(&chainID, "chain-id", 0, "expected chain ID")
	cmd.Flags().Uint64Var(&height, "activation-height", 0, "expected activation height")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if filename == "" {
			return fmt.Errorf("provide --manifest")
		}
		raw, err := readBounded(filename, protocol.MaxManifestBytes)
		if err != nil {
			return err
		}
		_, verified, err := protocol.DecodeManifest(raw, chainID, height)
		if err != nil {
			return err
		}
		cmd.Printf("All possession proofs valid. Digest: %s\nParent-state and epoch-snapshot coverage are not established by this offline command.\n", verified.Digest())
		return nil
	}
	return cmd
}
