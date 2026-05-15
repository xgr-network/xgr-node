package withdraw

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

type withdrawParams struct {
	jsonRPC  string
	config   string
	dataDir  string
	insecure bool
	initKeys bool

	amountStr string
	decimals  int
}

func GetCommand() *cobra.Command {
	p := &withdrawParams{}

	cmd := &cobra.Command{
		Use:   "withdraw",
		Short: "Withdraw unstaked funds from staking contract (withdraw(uint256))",
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

			amountWei, err := toWei(p.amountStr, p.decimals)
			if err != nil {
				outputter.SetError(err)
				return nil
			}
			if amountWei.Sign() <= 0 {
				outputter.SetError(fmt.Errorf("amount must be > 0"))
				return nil
			}

			if err := sendWithdraw(relayer, key, amountWei); err != nil {
				outputter.SetError(err)
				return nil
			}

			onchainStake, err := queryAccountStake(relayer, types.Address(key.Address()))
			if err != nil {
				outputter.SetError(err)
				return nil
			}

			outputter.SetCommandResult(&WithdrawResult{
				Validator:       types.Address(key.Address()).String(),
				AmountWei:       amountWei.String(),
				AccountStakeWei: onchainStake.String(),
			})

			return nil
		},
	}

	setFlags(cmd, p)

	cmd.Example = `# Partial withdraw from stake (keeps validator in staking contract)
# Preconditions: validator is deactivated and remaining stake after withdraw stays >= MIN_STAKE
xgrchain ibft set-active --value false --jsonrpc http://127.0.0.1:8545 --data-dir ./data --insecure
xgrchain ibft withdraw --amount 1000000 --jsonrpc http://127.0.0.1:8545 --data-dir ./data --insecure

# Full exit uses unstake (transfers full stake and removes validator metadata)
xgrchain ibft unstake --jsonrpc http://127.0.0.1:8545 --data-dir ./data --insecure`

	return cmd
}

func setFlags(cmd *cobra.Command, p *withdrawParams) {
	cmd.Flags().StringVar(&p.jsonRPC, "jsonrpc", "http://127.0.0.1:8545", "JSON-RPC endpoint")
	cmd.Flags().StringVar(&p.dataDir, "data-dir", "./data", "Data directory for local secrets")
	cmd.Flags().StringVar(&p.config, "config", "", "SecretsManager config file (optional)")
	cmd.Flags().BoolVar(&p.insecure, "insecure", false, "Allow insecure key storage")
	cmd.Flags().BoolVar(&p.initKeys, "init-keys", false, "Generate missing ECDSA key in data-dir/config")

	cmd.Flags().StringVar(&p.amountStr, "amount", "0", "Withdraw amount (integer, in token units)")
	cmd.Flags().IntVar(&p.decimals, "decimals", 18, "Token decimals")
	_ = cmd.MarkFlagRequired("amount")
}

func initSecretsManager(p *withdrawParams) (secrets.SecretsManager, error) {
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

func toWei(amountStr string, decimals int) (*big.Int, error) {
	if decimals < 0 {
		return nil, fmt.Errorf("decimals must be >= 0")
	}

	a, ok := new(big.Int).SetString(amountStr, 10)
	if !ok {
		return nil, fmt.Errorf("invalid amount %q", amountStr)
	}

	m := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	return new(big.Int).Mul(a, m), nil
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

func sendWithdraw(relayer txrelayer.TxRelayer, key ethgo.Key, amountWei *big.Int) error {
	m := abis.StakingABI.Methods["withdraw"]
	if m == nil {
		return fmt.Errorf("staking ABI missing withdraw")
	}
	inp, err := m.Inputs.Encode(map[string]interface{}{"amount": amountWei})
	if err != nil {
		return fmt.Errorf("encode withdraw input failed: %w", err)
	}

	to := ethgo.Address(staking.AddrStakingContract)

	// Preflight call from validator address to surface deterministic revert reasons
	// before gas estimation/sending wraps node-side execution errors.
	if key != nil {
		if _, err := relayer.Call(key.Address(), to, append(m.ID(), inp...)); err != nil {
			return mapWithdrawError(err)
		}
	}

	tx := &ethgo.Transaction{
		To:    &to,
		Input: append(m.ID(), inp...),
		Type:  ethgo.TransactionDynamicFee,
	}
	receipt, err := relayer.SendTransaction(tx, key)
	if err != nil {
		return mapWithdrawError(err)
	}
	if receipt == nil || receipt.Status != uint64(types.ReceiptSuccess) {
		return fmt.Errorf("withdraw reverted")
	}
	return nil
}

func mapWithdrawError(err error) error {
	if err == nil {
		return nil
	}

	msg := err.Error()
	switch {
	case strings.Contains(msg, "0x34637cc9"):
		return fmt.Errorf("withdraw rejected: validator must be deactivated first and epoch transition must pass (run: xgrchain ibft set-active --value false, then wait next epoch)")
	case strings.Contains(msg, "0x5c940c69"):
		return fmt.Errorf("withdraw rejected: too early after deactivation (wait until next epoch)")
	case strings.Contains(msg, "0x6eda21dd"):
		return fmt.Errorf("withdraw rejected: remaining stake would fall below MIN_STAKE (2,000,000 XGR)")
	case strings.Contains(msg, "0xdb73cdf0"):
		return fmt.Errorf("withdraw rejected: invalid amount (must be > 0 and less than current stake)")
	case strings.Contains(msg, "0x580e542f"):
		return fmt.Errorf("withdraw rejected: validator not found in staking contract")
	default:
		return fmt.Errorf("withdraw tx failed: %w", err)
	}
}

func hexToBytes(s string) ([]byte, error) {
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return []byte{}, nil
	}
	return hex.DecodeString(s)
}
