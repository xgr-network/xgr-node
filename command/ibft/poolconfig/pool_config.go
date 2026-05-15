package poolconfig

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

type params struct {
	jsonRPC              string
	config               string
	dataDir              string
	initKeys             bool
	enabled              bool
	maxTotalDelegatedXGR string
	minDelegatorXGR      string
	commissionBps        uint64
}

func GetCommand() *cobra.Command {
	p := &params{}

	cmd := &cobra.Command{
		Use:   "pool-config",
		Short: "Configure validator delegation pool parameters via JSON-RPC",
		Long:  "Updates validator pool configuration by submitting a transaction over the configured JSON-RPC endpoint.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			outputter := command.InitializeOutputter(cmd)
			defer outputter.WriteOutput()

			if err := validateCLIParams(p); err != nil {
				_ = cmd.Help()
				outputter.SetError(err)
				return nil
			}

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

			maxTotalDelegatedWei, ok := new(big.Int).SetString(p.maxTotalDelegatedXGR, 10)
			if !ok {
				outputter.SetError(fmt.Errorf("invalid --max-total-delegated"))
				return nil
			}
			maxTotalDelegatedWei.Mul(maxTotalDelegatedWei, new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
			if p.enabled && maxTotalDelegatedWei.Sign() <= 0 {
				outputter.SetError(fmt.Errorf("--max-total-delegated must be > 0 when --enabled=true"))
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

			validatorAddr := types.Address(key.Address())
			if err := ensureJoinedSelfValidator(relayer, validatorAddr); err != nil {
				outputter.SetError(fmt.Errorf("%w (wallet=%s, jsonrpc=%s)", err, validatorAddr.String(), p.jsonRPC))
				return nil
			}

			minDelegatorWei, ok := new(big.Int).SetString(p.minDelegatorXGR, 10)
			if !ok {
				outputter.SetError(fmt.Errorf("invalid --min-delegator-stake"))
				return nil
			}
			minDelegatorWei.Mul(minDelegatorWei, new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))

			if err := sendPoolConfigTx(relayer, key, p.enabled, maxTotalDelegatedWei, minDelegatorWei, p.commissionBps, p.jsonRPC); err != nil {
				outputter.SetError(err)
				return nil
			}

			outputter.SetCommandResult(&Result{Validator: validatorAddr.String(), Enabled: p.enabled, MaxTotalDelegatedWei: maxTotalDelegatedWei.String(), MinDelegatorWei: minDelegatorWei.String(), CommissionBps: p.commissionBps})
			return nil
		},
	}

	cmd.Flags().StringVar(&p.jsonRPC, "jsonrpc", "http://127.0.0.1:8545", "JSON-RPC endpoint")
	cmd.Flags().StringVar(&p.dataDir, "data-dir", "", "Data directory for local secrets")
	cmd.Flags().StringVar(&p.config, "config", "", "SecretsManager config file (optional)")
	cmd.Flags().BoolVar(&p.initKeys, "init-keys", false, "Generate missing ECDSA key in data-dir/config")
	cmd.Flags().BoolVar(&p.enabled, "enabled", true, "Enable or disable delegation pool")
	cmd.Flags().StringVar(&p.maxTotalDelegatedXGR, "max-total-delegated", "0", "maximum total delegated stake accepted by this validator pool")
	cmd.Flags().StringVar(&p.minDelegatorXGR, "min-delegator-stake", "0", "minimum stake required per delegator; 0 uses global DELEGATOR_MIN_STAKE")
	cmd.Flags().Uint64Var(&p.commissionBps, "commission-bps", 0, "Validator commission in basis points")

	return cmd
}

func validateCLIParams(p *params) error {
	if p.dataDir == "" && p.config == "" {
		return fmt.Errorf("either --data-dir or --config must be provided")
	}

	return nil
}

func sendPoolConfigTx(relayer txrelayer.TxRelayer, key ethgo.Key, enabled bool, maxTotalDelegatedWei *big.Int, minDelegatorWei *big.Int, commissionBps uint64, jsonRPC string) error {
	if commissionBps > 10000 {
		return fmt.Errorf("commission-bps must be <= 10000")
	}
	if enabled && maxTotalDelegatedWei.Sign() <= 0 {
		return fmt.Errorf("--max-total-delegated must be > 0 when --enabled=true")
	}

	method := abis.StakingABI.Methods["setValidatorPoolConfig"]
	if method == nil {
		return fmt.Errorf("staking ABI missing setValidatorPoolConfig")
	}

	inputArgs, err := method.Inputs.Encode(map[string]interface{}{
		"delegationEnabled":      enabled,
		"maxTotalDelegatedStake": maxTotalDelegatedWei,
		"minDelegatorStake":      minDelegatorWei,
		"commissionBps":          uint16(commissionBps),
	})
	if err != nil {
		return err
	}

	input := append(method.ID(), inputArgs...)

	to := ethgo.Address(staking.AddrStakingContract)
	tx := &ethgo.Transaction{To: &to, From: key.Address(), Input: input, Type: ethgo.TransactionDynamicFee}
	receipt, err := relayer.SendTransaction(tx, key)
	if err != nil {
		return fmt.Errorf("pool-config transaction failed for wallet %s via jsonrpc %s: %w", key.Address(), jsonRPC, err)
	}
	if receipt == nil || receipt.Status != uint64(types.ReceiptSuccess) {
		return fmt.Errorf("pool-config transaction reverted for wallet %s via jsonrpc %s", key.Address(), jsonRPC)
	}
	return nil
}

func ensureJoinedSelfValidator(relayer txrelayer.TxRelayer, addr types.Address) error {
	method := abis.StakingABI.Methods["validatorSelfStake"]
	if method == nil {
		return fmt.Errorf("staking ABI missing validatorSelfStake")
	}

	inp, err := method.Inputs.Encode(map[string]interface{}{"validator": ethgo.Address(addr)})
	if err != nil {
		return err
	}

	resp, err := relayer.Call(ethgo.ZeroAddress, ethgo.Address(staking.AddrStakingContract), append(method.ID(), inp...))
	if err != nil {
		return err
	}

	b, err := hexToBytes(resp)
	if err != nil {
		return err
	}

	decoded, err := method.Outputs.Decode(b)
	if err != nil {
		return err
	}

	outMap, ok := decoded.(map[string]interface{})
	if !ok {
		return fmt.Errorf("decode validatorSelfStake: unexpected type")
	}

	selfStake, ok := outMap["0"].(*big.Int)
	if !ok {
		return fmt.Errorf("decode validatorSelfStake: missing value")
	}

	if selfStake.Sign() <= 0 {
		return fmt.Errorf("wallet %s is not a joined validator self-position; run ibft join first or use the correct --data-dir", addr.String())
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

func initSecretsManager(p *params) (secrets.SecretsManager, error) {
	if p.config != "" {
		cfg, err := secrets.ReadConfig(p.config)
		if err != nil {
			return nil, err
		}
		return secretsHelper.InitCloudSecretsManager(cfg)
	}
	baseConfig := &secrets.SecretsManagerParams{Logger: hclog.NewNullLogger(), Extra: map[string]interface{}{secrets.Path: p.dataDir}}
	return local.SecretsManagerFactory(nil, baseConfig)
}

func ensureECDSAKey(sm secrets.SecretsManager) error {
	if sm.HasSecret(secrets.ValidatorKey) {
		return nil
	}
	_, err := secretsHelper.InitECDSAValidatorKey(sm)
	return err
}
