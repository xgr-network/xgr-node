package interchain

import (
	"encoding/hex"
	"fmt"

	"github.com/hashicorp/go-hclog"
	"github.com/spf13/cobra"

	"github.com/xgr-network/xgr-node/command"
	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/secrets"
	secretsHelper "github.com/xgr-network/xgr-node/secrets/helper"
	"github.com/xgr-network/xgr-node/secrets/local"
)

type bootstrapProofParams struct {
	dataDir           string
	config            string
	originChainID      uint64
	destinationDomain uint32
}

type BootstrapProofResult struct {
	Validator             string `json:"validator"`
	OriginChainID         uint64 `json:"originChainId"`
	DestinationDomain     uint32 `json:"destinationDomain"`
	BLSPublicKey          string `json:"blsPublicKey"`
	BLSPublicKeyEIP2537   string `json:"blsPublicKeyEIP2537"`
	Payload               string `json:"payload"`
	PossessionProof       string `json:"possessionProof"`
}

func (r *BootstrapProofResult) GetOutput() string {
	return fmt.Sprintf(
		"\n[IBFT INTERCHAIN BOOTSTRAP-PROOF]\nValidator|%s\nOrigin chain ID|%d\nDestination domain|%d\nBLS public key|%s\nBLS public key EIP-2537|%s\nPayload|%s\nPossession proof EIP-2537|%s\n",
		r.Validator, r.OriginChainID, r.DestinationDomain, r.BLSPublicKey,
		r.BLSPublicKeyEIP2537, r.Payload, r.PossessionProof,
	)
}

func getBootstrapProofCommand() *cobra.Command {
	p := &bootstrapProofParams{}
	cmd := &cobra.Command{
		Use:   "bootstrap-proof",
		Short: "Create the validator BLS proof required to bootstrap an interchain registry",
		Args:  cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if p.dataDir == "" && p.config == "" {
				return fmt.Errorf("either --data-dir or --config is required")
			}
			if p.dataDir != "" && p.config != "" {
				return fmt.Errorf("--data-dir and --config are mutually exclusive")
			}
			if p.originChainID == 0 {
				return fmt.Errorf("--origin-chain-id must be non-zero")
			}
			if p.destinationDomain == 0 {
				return fmt.Errorf("--destination-domain must be non-zero")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			outputter := command.InitializeOutputter(cmd)
			defer outputter.WriteOutput()

			result, err := createBootstrapProof(p)
			if err != nil {
				outputter.SetError(err)
				return nil
			}
			outputter.SetCommandResult(result)
			return nil
		},
	}
	cmd.Flags().StringVar(&p.dataDir, "data-dir", "", "data directory of the validator secrets manager")
	cmd.Flags().StringVar(&p.config, "config", "", "cloud secrets manager configuration")
	cmd.Flags().Uint64Var(&p.originChainID, "origin-chain-id", 0, "XGR origin chain ID")
	cmd.Flags().Uint32Var(&p.destinationDomain, "destination-domain", 0, "destination interchain domain")
	return cmd
}

func createBootstrapProof(p *bootstrapProofParams) (*BootstrapProofResult, error) {
	sm, err := bootstrapSecretsManager(p)
	if err != nil {
		return nil, err
	}
	ecdsaRaw, err := sm.GetSecret(secrets.ValidatorKey)
	if err != nil {
		return nil, fmt.Errorf("load validator ECDSA key: %w", err)
	}
	ecdsaKey, err := crypto.BytesToECDSAPrivateKey(ecdsaRaw)
	if err != nil {
		return nil, fmt.Errorf("parse validator ECDSA key: %w", err)
	}
	validator, err := crypto.GetAddressFromKey(ecdsaKey)
	if err != nil {
		return nil, fmt.Errorf("derive validator address: %w", err)
	}

	blsRaw, err := sm.GetSecret(secrets.ValidatorBLSKey)
	if err != nil {
		return nil, fmt.Errorf("load validator BLS key: %w", err)
	}
	blsKey, err := crypto.BytesToBLSSecretKey(blsRaw)
	if err != nil {
		return nil, fmt.Errorf("parse validator BLS key: %w", err)
	}
	blsPub, err := crypto.BLSSecretKeyToPubkeyBytes(blsKey)
	if err != nil {
		return nil, fmt.Errorf("derive validator BLS public key: %w", err)
	}
	eipPub, err := crypto.BLSPublicKeyToEIP2537(blsPub)
	if err != nil {
		return nil, fmt.Errorf("convert validator BLS public key to EIP-2537: %w", err)
	}
	payload, err := protocol.MarshalBootstrapPayload(
		p.originChainID, p.destinationDomain, validator, blsPub, eipPub,
	)
	if err != nil {
		return nil, err
	}
	compressedProof, err := crypto.SignByBLS(blsKey, payload)
	if err != nil {
		return nil, fmt.Errorf("sign bootstrap proof: %w", err)
	}
	eipProof, err := crypto.BLSSignatureToEIP2537(compressedProof)
	if err != nil {
		return nil, fmt.Errorf("convert bootstrap proof to EIP-2537: %w", err)
	}

	return &BootstrapProofResult{
		Validator: validator.String(),
		OriginChainID: p.originChainID,
		DestinationDomain: p.destinationDomain,
		BLSPublicKey: "0x" + hex.EncodeToString(blsPub),
		BLSPublicKeyEIP2537: "0x" + hex.EncodeToString(eipPub),
		Payload: "0x" + hex.EncodeToString(payload),
		PossessionProof: "0x" + hex.EncodeToString(eipProof),
	}, nil
}

func bootstrapSecretsManager(p *bootstrapProofParams) (secrets.SecretsManager, error) {
	if p.config != "" {
		cfg, err := secrets.ReadConfig(p.config)
		if err != nil {
			return nil, err
		}
		return secretsHelper.InitCloudSecretsManager(cfg)
	}
	return local.SecretsManagerFactory(nil, &secrets.SecretsManagerParams{
		Logger: hclog.NewNullLogger(),
		Extra: map[string]interface{}{secrets.Path: p.dataDir},
	})
}
