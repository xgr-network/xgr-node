package stake

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

type stakeParams struct {
	jsonRPC  string
	config   string
	dataDir  string
	insecure bool
	initKeys bool

	amountStr string
	decimals  int
}

func GetCommand() *cobra.Command {
	p := &stakeParams{}

	cmd := &cobra.Command{
		Use:   "stake",
		Short: "Increase stake in the staking contract (payable stake())",
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

			amountWei := toWei(p.amountStr, p.decimals)
			if amountWei.Sign() <= 0 {
				outputter.SetError(fmt.Errorf("amount must be > 0"))
				return nil
			}

			// send stake()
			if err := sendStake(relayer, key, amountWei); err != nil {
				outputter.SetError(err)
				return nil
			}

			onchainStake, err := queryAccountStake(relayer, types.Address(key.Address()))
			if err != nil {
				outputter.SetError(err)
				return nil
			}

			outputter.SetCommandResult(&StakeResult{
				Validator:       types.Address(key.Address()).String(),
				AmountWei:       amountWei.String(),
				AccountStakeWei: onchainStake.String(),
			})

			return nil
		},
	}

	setFlags(cmd, p)
	return cmd
}

func setFlags(cmd *cobra.Command, p *stakeParams) {
	cmd.Flags().StringVar(&p.jsonRPC, "jsonrpc", "http://127.0.0.1:8545", "JSON-RPC endpoint")
	cmd.Flags().StringVar(&p.dataDir, "data-dir", "./data", "Data directory for local secrets")
	cmd.Flags().StringVar(&p.config, "config", "", "SecretsManager config file (optional)")
	cmd.Flags().BoolVar(&p.insecure, "insecure", false, "Allow insecure key storage")
	cmd.Flags().BoolVar(&p.initKeys, "init-keys", false, "Generate missing ECDSA key in data-dir/config")

	cmd.Flags().StringVar(&p.amountStr, "amount", "0", "Stake amount (integer, in token units)")
	cmd.Flags().IntVar(&p.decimals, "decimals", 18, "Token decimals")

	_ = cmd.MarkFlagRequired("amount")
}

func initSecretsManager(p *stakeParams) (secrets.SecretsManager, error) {
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

func toWei(amountStr string, decimals int) *big.Int {
	a, _ := new(big.Int).SetString(amountStr, 10)
	m := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	return new(big.Int).Mul(a, m)
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

func sendStake(relayer txrelayer.TxRelayer, key ethgo.Key, stakeWei *big.Int) error {
	m := abis.StakingABI.Methods["stake"]
	if m == nil {
		return fmt.Errorf("staking ABI missing stake")
	}
	to := ethgo.Address(staking.AddrStakingContract)
	tx := &ethgo.Transaction{
		To:    &to,
		Input: m.ID(),
		Value: stakeWei,
		Type:  ethgo.TransactionDynamicFee,
	}
	receipt, err := relayer.SendTransaction(tx, key)
	if err != nil {
		return fmt.Errorf("stake tx failed: %w", err)
	}
	if receipt == nil || receipt.Status != uint64(types.ReceiptSuccess) {
		return fmt.Errorf("stake reverted")
	}
	return nil
}

func hexToBytes(s string) ([]byte, error) {
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return []byte{}, nil
	}
	return hex.DecodeString(s)
}
