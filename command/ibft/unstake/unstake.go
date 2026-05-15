package unstake

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/spf13/cobra"
	"github.com/umbracle/ethgo"

	"github.com/xgr-network/xgr-node/command"
	ibftHelper "github.com/xgr-network/xgr-node/command/ibft/helper"
	"github.com/xgr-network/xgr-node/contracts/abis"
	"github.com/xgr-network/xgr-node/contracts/staking"
	secretsHelper "github.com/xgr-network/xgr-node/helper/secrets"
	"github.com/xgr-network/xgr-node/secrets"
	"github.com/xgr-network/xgr-node/secrets/local"
	"github.com/xgr-network/xgr-node/txrelayer"
	"github.com/xgr-network/xgr-node/types"
)

type unstakeParams struct {
	jsonRPC  string
	config   string
	dataDir  string
	insecure bool
	initKeys bool
}

func GetCommand() *cobra.Command {
	p := &unstakeParams{}

	cmd := &cobra.Command{
		Use:   "unstake",
		Short: "Request unstake from staking contract (unstake())",
		RunE: func(cmd *cobra.Command, _ []string) error {
			outputter := command.InitializeOutputter(cmd)
			defer outputter.WriteOutput()

			sm, err := initSecretsManager(p)
			if err != nil {
				outputter.SetError(err)
				return nil
			}

			if p.initKeys {
				if err := ensureECDSAKey(sm); err != nil {
					outputter.SetError(err)
					return nil
				}
			}

			key, err := ibftHelper.GetECDSAKeyFromSecret(sm)
			if err != nil {
				outputter.SetError(err)
				return nil
			}

			relayer, err := txrelayer.NewTxRelayer(
				txrelayer.WithIPAddress(p.jsonRPC),
				txrelayer.WithReceiptTimeout(250*time.Millisecond),
			)
			if err != nil {
				outputter.SetError(err)
				return nil
			}

			epochSize, err := queryEpochSize(relayer)
			if err != nil {
				outputter.SetError(err)
				return nil
			}
			if epochSize == 0 {
				outputter.SetError(fmt.Errorf("staking contract returned epochSize=0; staking predeploy is invalid on this chain"))
				return nil
			}

			if err := sendUnstake(relayer, key); err != nil {
				outputter.SetError(err)
				return nil
			}

			onchainStake, err := queryAccountStake(relayer, types.Address(key.Address()))
			if err != nil {
				outputter.SetError(err)
				return nil
			}

			outputter.SetCommandResult(&UnstakeResult{
				Validator:       types.Address(key.Address()).String(),
				AccountStakeWei: onchainStake.String(),
			})

			return nil
		},
	}

	setFlags(cmd, p)
	cmd.Example = `# You must deactivate before unstake
xgrchain ibft set-active --value false --jsonrpc http://127.0.0.1:8545 --data-dir ./data --insecure

# Request unstake (withdraw is a separate step)
xgrchain ibft unstake --jsonrpc http://127.0.0.1:8545 --data-dir ./data --insecure`
	return cmd
}

func setFlags(cmd *cobra.Command, p *unstakeParams) {
	cmd.Flags().StringVar(&p.jsonRPC, "jsonrpc", "http://127.0.0.1:8545", "JSON-RPC endpoint")
	cmd.Flags().StringVar(&p.dataDir, "data-dir", "./data", "Data directory for local secrets")
	cmd.Flags().StringVar(&p.config, "config", "", "SecretsManager config file (optional)")
	cmd.Flags().BoolVar(&p.insecure, "insecure", false, "Allow insecure key storage")
	cmd.Flags().BoolVar(&p.initKeys, "init-keys", false, "Generate missing ECDSA key in data-dir/config")
}

func initSecretsManager(p *unstakeParams) (secrets.SecretsManager, error) {
	if p.config != "" {
		cfg, err := secrets.ReadConfig(p.config)
		if err != nil {
			return nil, err
		}
		return secretsHelper.InitCloudSecretsManager(cfg)
	}

	baseConfig := &secrets.SecretsManagerParams{
		Logger: hclog.NewNullLogger(),
		Extra: map[string]interface{}{
			secrets.Path: p.dataDir,
		},
	}

	return local.SecretsManagerFactory(nil, baseConfig)
}

func ensureECDSAKey(sm secrets.SecretsManager) error {
	if sm.HasSecret(secrets.ValidatorKey) {
		return nil
	}
	_, err := secretsHelper.InitECDSAValidatorKey(sm)
	return err
}

func queryAccountStake(relayer txrelayer.TxRelayer, addr types.Address) (*big.Int, error) {
	method := abis.StakingABI.Methods["accountStake"]
	if method == nil {
		return nil, fmt.Errorf("staking ABI missing accountStake")
	}
	inp, err := method.Inputs.Encode(map[string]interface{}{
		"account": ethgo.Address(addr),
	})
	if err != nil {
		return nil, err
	}
	resp, err := relayer.Call(ethgo.ZeroAddress, ethgo.Address(staking.AddrStakingContract), append(method.ID(), inp...))
	if err != nil {
		return nil, err
	}
	b, err := hexToBytes(resp)
	if err != nil {
		return nil, err
	}
	decoded, err := method.Outputs.Decode(b)
	if err != nil {
		return nil, err
	}
	m, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("decode accountStake: unexpected type")
	}
	v, ok := m["0"].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("decode accountStake: missing value")
	}
	return v, nil
}

func queryEpochSize(relayer txrelayer.TxRelayer) (uint64, error) {
	method := abis.StakingABI.Methods["epochSize"]
	if method == nil {
		return 0, fmt.Errorf("staking ABI missing epochSize")
	}

	resp, err := relayer.Call(ethgo.ZeroAddress, ethgo.Address(staking.AddrStakingContract), method.ID())
	if err != nil {
		return 0, err
	}

	b, err := hexToBytes(resp)
	if err != nil {
		return 0, err
	}

	decoded, err := method.Outputs.Decode(b)
	if err != nil {
		return 0, err
	}

	m, ok := decoded.(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("decode epochSize: unexpected type")
	}

	if epoch, ok := uint64FromDecodedValue(m["0"]); ok {
		return epoch, nil
	}
	if epoch, ok := uint64FromDecodedValue(m["epochSize"]); ok {
		return epoch, nil
	}
	for _, value := range m {
		if epoch, ok := uint64FromDecodedValue(value); ok {
			return epoch, nil
		}
	}

	return 0, fmt.Errorf("decode epochSize: missing value")
}

func uint64FromDecodedValue(v interface{}) (uint64, bool) {
	switch x := v.(type) {
	case *big.Int:
		if x == nil {
			return 0, false
		}
		return x.Uint64(), true
	case uint64:
		return x, true
	case uint32:
		return uint64(x), true
	case int:
		if x < 0 {
			return 0, false
		}
		return uint64(x), true
	}

	return 0, false
}

func sendUnstake(relayer txrelayer.TxRelayer, key ethgo.Key) error {
	m := abis.StakingABI.Methods["unstake"]
	if m == nil {
		return fmt.Errorf("staking ABI missing unstake")
	}
	to := ethgo.Address(staking.AddrStakingContract)

	// Preflight call from validator address to surface deterministic revert reasons
	// before gas estimation/sending wraps the node error.
	if key != nil {
		if _, err := relayer.Call(key.Address(), to, m.ID()); err != nil {
			return mapUnstakeError(err)
		}
	}

	tx := &ethgo.Transaction{
		To:    &to,
		Input: m.ID(),
		Type:  ethgo.TransactionDynamicFee,
	}
	receipt, err := relayer.SendTransaction(tx, key)
	if err != nil {
		return mapUnstakeError(err)
	}
	if receipt == nil || receipt.Status != uint64(types.ReceiptSuccess) {
		return fmt.Errorf("unstake reverted")
	}
	return nil
}

func mapUnstakeError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "0x34637cc9") {
		return fmt.Errorf("unstake rejected: validator must be deactivated first (run: xgrchain ibft set-active --value false)")
	}
	if strings.Contains(msg, "0x4e487b71") && strings.Contains(msg, "00000012") {
		return fmt.Errorf("unstake rejected: staking contract panic 0x12 (division/modulo by zero). likely epochSize=0 in staking contract state")
	}
	if strings.Contains(msg, "0x4e487b71") {
		return fmt.Errorf("unstake rejected: staking contract panic during unstake (panic selector 0x4e487b71): %w", err)
	}

	return fmt.Errorf("unstake tx failed: %w", err)
}

func hexToBytes(s string) ([]byte, error) {
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return []byte{}, nil
	}
	return hex.DecodeString(s)
}
