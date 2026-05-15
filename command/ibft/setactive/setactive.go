package setactive

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
	"github.com/xgr-network/xgr-node/crypto"
	secretsHelper "github.com/xgr-network/xgr-node/helper/secrets"
	"github.com/xgr-network/xgr-node/secrets"
	"github.com/xgr-network/xgr-node/secrets/local"
	"github.com/xgr-network/xgr-node/txrelayer"
	"github.com/xgr-network/xgr-node/types"
)

type setActiveParams struct {
	jsonRPC  string
	config   string
	dataDir  string
	insecure bool
	initKeys bool
	value    bool
}

func GetCommand() *cobra.Command {
	p := &setActiveParams{}

	cmd := &cobra.Command{
		Use:   "set-active",
		Short: "Set validator active flag in staking contract (setActive(bool))",
		Args:  cobra.NoArgs,
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

			if err := sendSetActive(relayer, key, p.value); err != nil {
				outputter.SetError(err)
				return nil
			}

			validatorAddr := types.Address(key.Address())

			validatorStatus, err := queryValidatorStatus(relayer, validatorAddr)
			if err != nil {
				outputter.SetError(err)
				return nil
			}

			epochSize, _ := queryEpochSize(relayer)

			onchainStake, err := queryAccountStake(relayer, validatorAddr)
			if err != nil {
				outputter.SetError(err)
				return nil
			}

			res := &SetActiveResult{
				Validator:       validatorAddr.String(),
				JoinedAtBlock:   validatorStatus.JoinedAtBlock,
				Active:          validatorStatus.Active,
				AccountStakeWei: onchainStake.String(),
			}

			if validatorStatus.DeactivatedAtBlock > 0 {
				res.DeactivatedAtBlock = validatorStatus.DeactivatedAtBlock
				if epochSize > 0 {
					deactEpoch := validatorStatus.DeactivatedAtBlock / epochSize
					res.UnstakeAvailableAtBlock = (deactEpoch + 1) * epochSize
				}
			}

			outputter.SetCommandResult(res)

			return nil
		},
	}

	setFlags(cmd, p)
	return cmd
}

func setFlags(cmd *cobra.Command, p *setActiveParams) {
	cmd.Flags().StringVar(&p.jsonRPC, "jsonrpc", "http://127.0.0.1:8545", "JSON-RPC endpoint")
	cmd.Flags().StringVar(&p.dataDir, "data-dir", "./data", "Data directory for local secrets")
	cmd.Flags().StringVar(&p.config, "config", "", "SecretsManager config file (optional)")
	cmd.Flags().BoolVar(&p.insecure, "insecure", false, "Allow insecure key storage")
	cmd.Flags().BoolVar(&p.initKeys, "init-keys", false, "Generate missing ECDSA key in data-dir/config")
	cmd.Flags().BoolVar(&p.value, "value", true, "Set active flag to true/false")
}

func initSecretsManager(p *setActiveParams) (secrets.SecretsManager, error) {
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

type validatorStatus struct {
	Active             bool
	DeactivatedAtBlock uint64
	JoinedAtBlock      uint64
}

func queryValidatorStatus(relayer txrelayer.TxRelayer, addr types.Address) (*validatorStatus, error) {
	method := abis.StakingABI.Methods["validatorInfo"]
	if method == nil {
		return nil, fmt.Errorf("staking ABI missing validatorInfo")
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
		return nil, fmt.Errorf("decode validatorInfo: unexpected type")
	}

	active, ok := m["1"].(bool)
	if !ok {
		active, ok = m["active"].(bool)
		if !ok {
			return nil, fmt.Errorf("decode validatorInfo: missing active")
		}
	}

	deactivatedAt := uint64(0)
	if deact, ok := m["3"].(*big.Int); ok && deact != nil {
		deactivatedAt = deact.Uint64()
	} else if deact, ok := m["deactivatedAtBlock"].(*big.Int); ok && deact != nil {
		deactivatedAt = deact.Uint64()
	}

	joinedAt, _ := queryJoinedAtBlock(relayer, addr)

	return &validatorStatus{
		Active:             active,
		DeactivatedAtBlock: deactivatedAt,
		JoinedAtBlock:      joinedAt,
	}, nil
}

func queryJoinedAtBlock(relayer txrelayer.TxRelayer, addr types.Address) (uint64, error) {
	selector := crypto.Keccak256([]byte("joinedAtBlock(address)"))[:4]
	input := make([]byte, 4+32)
	copy(input[:4], selector)
	copy(input[4+12:], addr.Bytes())

	resp, err := relayer.Call(ethgo.ZeroAddress, ethgo.Address(staking.AddrStakingContract), input)
	if err != nil {
		return 0, err
	}
	b, err := hexToBytes(resp)
	if err != nil {
		return 0, err
	}
	if len(b) == 0 {
		return 0, nil
	}

	return new(big.Int).SetBytes(b).Uint64(), nil
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

func epochOf(number, epochSize uint64) uint64 {
	if number == 0 || epochSize == 0 {
		return 0
	}
	if number%epochSize == 0 {
		return number / epochSize
	}
	return number/epochSize + 1
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

func sendSetActive(relayer txrelayer.TxRelayer, key ethgo.Key, value bool) error {
	m := abis.StakingABI.Methods["setActive"]
	if m == nil {
		return fmt.Errorf("staking ABI missing setActive")
	}
	inp, err := m.Inputs.Encode(map[string]interface{}{"active_": value})
	if err != nil {
		return fmt.Errorf("encode setActive input failed: %w", err)
	}
	to := ethgo.Address(staking.AddrStakingContract)
	tx := &ethgo.Transaction{
		To:    &to,
		Input: append(m.ID(), inp...),
		Type:  ethgo.TransactionDynamicFee,
	}
	receipt, err := relayer.SendTransaction(tx, key)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "0xa291f7f5") {
			return nil
		}
		if strings.Contains(msg, "0x580e542f") {
			return fmt.Errorf("set-active rejected: validator %s not found in staking set on this chain (check --data-dir/--config key and --jsonrpc endpoint)", key.Address())
		}
		return fmt.Errorf("setActive tx failed: %w", err)
	}
	if receipt == nil || receipt.Status != uint64(types.ReceiptSuccess) {
		return fmt.Errorf("setActive reverted")
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
