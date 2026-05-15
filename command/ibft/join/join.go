package join

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/spf13/cobra"
	"github.com/umbracle/ethgo"
	ethgowallet "github.com/umbracle/ethgo/wallet"

	"github.com/xgr-network/xgr-node/command"
	"github.com/xgr-network/xgr-node/command/helper"
	"github.com/xgr-network/xgr-node/contracts/abis"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/secrets"
	secretsHelper "github.com/xgr-network/xgr-node/secrets/helper"
	"github.com/xgr-network/xgr-node/secrets/local"
	"github.com/xgr-network/xgr-node/txrelayer"
	"github.com/xgr-network/xgr-node/types"
)

const (
	flagDataDir  = "data-dir"
	flagConfig   = "config"
	flagInitKeys = "init-keys"
	flagInitOnly = "init-only"
	flagStakeXGR = "stake"
	flagDecimals = "decimals"

	defaultStakeXGR = "200000"
	defaultDecimals = 18
)

type params struct {
	jsonRPC  string
	dataDir  string
	config   string
	initKeys bool
	initOnly bool
	stakeStr string
	decimals int
}

var p params

func GetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "join",
		Short: "One-shot PoS validator join: ensure keys, register BLS pubkey, stake required amount",
		Example: `  # Local validator
  xgrchain ibft join --jsonrpc http://127.0.0.1:8545 --data-dir ./data --stake 1000000

  # Remote chain node
  xgrchain ibft join --jsonrpc http://<rpc-host>:8545 --data-dir ./data --stake 1000000`,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			p.jsonRPC = helper.GetJSONRPCAddress(cmd)
			if p.jsonRPC == "" {
				return fmt.Errorf("missing --jsonrpc")
			}

			p.jsonRPC = normalizeJSONRPCAddress(p.jsonRPC)
			if p.dataDir == "" && p.config == "" {
				return fmt.Errorf("either --%s or --%s must be provided", flagDataDir, flagConfig)
			}
			if p.decimals < 0 || p.decimals > 30 {
				return fmt.Errorf("invalid --%s", flagDecimals)
			}
			if _, ok := new(big.Int).SetString(p.stakeStr, 10); !ok {
				return fmt.Errorf("invalid --%s (must be integer, in XGR units): %s", flagStakeXGR, p.stakeStr)
			}
			return nil
		},
		RunE: run,
	}

	cmd.Flags().StringVar(&p.jsonRPC, "jsonrpc", "http://127.0.0.1:8545", "JSON-RPC endpoint")

	cmd.Flags().StringVar(&p.dataDir, flagDataDir, "", "data dir for local secrets manager (validator keys)")
	cmd.Flags().StringVar(&p.config, flagConfig, "", "secrets manager config (cloud)")
	cmd.Flags().BoolVar(&p.initKeys, flagInitKeys, true, "auto-generate missing ECDSA/BLS keys into secrets manager")
	cmd.Flags().BoolVar(&p.initOnly, flagInitOnly, false, "only generate keys and print the validator address (no onchain tx)")
	cmd.Flags().StringVar(&p.stakeStr, flagStakeXGR, defaultStakeXGR, "stake amount in XGR (integer units, default 200,000)")
	cmd.Flags().IntVar(&p.decimals, flagDecimals, defaultDecimals, "native decimals for XGR (default 18)")

	cmd.MarkFlagsMutuallyExclusive(flagDataDir, flagConfig)
	return cmd
}

func run(cmd *cobra.Command, _ []string) error {
	outputter := command.InitializeOutputter(cmd)
	defer outputter.WriteOutput()

	// 1) secrets manager
	sm, err := initSecretsManager()
	if err != nil {
		outputter.SetError(err)
		return nil
	}

	// 2) ensure keys exist (optional)
	if p.initKeys {
		if err := ensureKeys(sm); err != nil {
			outputter.SetError(err)
			return nil
		}
	}

	// 3) load ECDSA + BLS pubkey from secrets
	ecdsaKey, blsPub, err := loadKeysFromSecrets(sm)
	if err != nil {
		outputter.SetError(err)
		return nil
	}
	valAddr := types.Address(ecdsaKey.Address())

	// INIT-ONLY: print address + pubkey and exit.
	if p.initOnly {
		outputter.SetCommandResult(&JoinResult{
			Validator:       valAddr.String(),
			JoinedAtBlock:   0,
			BLSRegistered:   false,
			StakeWei:        stakeToWei(p.stakeStr, p.decimals).String(),
			AccountStakeWei: "0",
			Eligible:        false,
			Note:            "Keys generated. Fund this address (gas + stake) and rerun without --init-only.",
		})
		return nil
	}

	// 4) relayer
	relayer, err := txrelayer.NewTxRelayer(
		txrelayer.WithIPAddress(p.jsonRPC),
		txrelayer.WithReceiptTimeout(250*time.Millisecond),
	)
	if err != nil {
		outputter.SetError(err)
		return nil
	}

	// 5) validate requested stake against validator self-stake minimum
	requiredStakeWei := stakeToWei(p.stakeStr, p.decimals)
	thresholdWei, err := queryValidatorMinSelfStake(relayer)
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to query VALIDATOR_MIN_SELF_STAKE: %w", err))
		return nil
	}
	if requiredStakeWei.Cmp(thresholdWei) < 0 {
		outputter.SetError(fmt.Errorf("requested --%s=%s is below validator self-stake minimum (%s XGR)", flagStakeXGR, p.stakeStr, weiToXGRString(thresholdWei, p.decimals)))
		return nil
	}

	// 6) check current onchain state
	onchainStake, err := queryAccountStake(relayer, valAddr)
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to query accountStake: %w", err))
		return nil
	}

	alreadyRegistered, err := isBLSRegistered(relayer, valAddr, blsPub)
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to query validator list/pubkeys: %w", err))
		return nil
	}

	// 7) stake if needed (must happen before registerBLSPublicKey for new validators)
	if onchainStake.Cmp(requiredStakeWei) < 0 {
		stakeDeltaWei := new(big.Int).Sub(requiredStakeWei, onchainStake)

		balanceWei, balErr := relayer.Client().Eth().GetBalance(ecdsaKey.Address(), ethgo.Latest)
		if balErr != nil {
			outputter.SetError(fmt.Errorf("failed to query account balance: %w", balErr))
			return nil
		}
		if balanceWei.Cmp(stakeDeltaWei) < 0 {
			outputter.SetError(fmt.Errorf(
				"insufficient balance for staking value: need %s wei (%s XGR), have %s wei (%s XGR); fund validator address %s",
				stakeDeltaWei.String(), weiToXGRString(stakeDeltaWei, p.decimals),
				balanceWei.String(), weiToXGRString(balanceWei, p.decimals),
				valAddr.String(),
			))
			return nil
		}

		if err := sendStake(relayer, ecdsaKey, stakeDeltaWei); err != nil {
			outputter.SetError(err)
			return nil
		}
		onchainStake = new(big.Int).Add(onchainStake, stakeDeltaWei)
	}

	// 8) register BLS pubkey if needed
	if !alreadyRegistered {
		if err := sendRegisterBLS(relayer, ecdsaKey, blsPub); err != nil {
			outputter.SetError(err)
			return nil
		}
	}

	// 9) done
	joinedAt, _ := queryJoinedAtBlock(relayer, valAddr)
	outputter.SetCommandResult(&JoinResult{
		Validator:       valAddr.String(),
		JoinedAtBlock:   joinedAt,
		BLSRegistered:   true,
		StakeWei:        requiredStakeWei.String(),
		AccountStakeWei: onchainStake.String(),
		Eligible:        true,
		Note:            "Join requires >=200k self stake and becomes effective in next epoch. Eligibility in fetcher still additionally requires >=2M effective total support stake.",
	})

	return nil
}

func normalizeJSONRPCAddress(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}

	if parsed.Hostname() != "0.0.0.0" {
		return raw
	}

	port := parsed.Port()
	if port == "" {
		return raw
	}

	parsed.Host = "127.0.0.1:" + port

	return parsed.String()
}

func initSecretsManager() (secrets.SecretsManager, error) {
	if p.config != "" {
		cfg, err := secrets.ReadConfig(p.config)
		if err != nil {
			return nil, err
		}
		return secretsHelper.InitCloudSecretsManager(cfg)
	}

	// local FS secrets manager
	baseConfig := &secrets.SecretsManagerParams{
		Logger: hclog.NewNullLogger(),
		Extra: map[string]interface{}{
			secrets.Path: p.dataDir,
		},
	}

	return local.SecretsManagerFactory(nil, baseConfig)
}

func ensureKeys(sm secrets.SecretsManager) error {
	// ECDSA validator key
	if !sm.HasSecret(secrets.ValidatorKey) {
		if _, err := secretsHelper.InitECDSAValidatorKey(sm); err != nil {
			return err
		}
	}
	// BLS validator key
	if !sm.HasSecret(secrets.ValidatorBLSKey) {
		if _, err := secretsHelper.InitBLSValidatorKey(sm); err != nil {
			return err
		}
	}
	return nil
}

func loadKeysFromSecrets(sm secrets.SecretsManager) (*ethgowallet.Key, []byte, error) {
	ecdsaRaw, err := sm.GetSecret(secrets.ValidatorKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load validator ecdsa key: %w", err)
	}
	ecdsaPriv, err := crypto.BytesToECDSAPrivateKey(ecdsaRaw)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse validator ecdsa key: %w", err)
	}
	ecdsaKey := ethgowallet.NewKey(ecdsaPriv)

	blsRaw, err := sm.GetSecret(secrets.ValidatorBLSKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load validator bls key: %w", err)
	}
	blsKey, err := crypto.BytesToBLSSecretKey(blsRaw)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse validator bls key: %w", err)
	}
	blsPub, err := crypto.BLSSecretKeyToPubkeyBytes(blsKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to derive validator bls pubkey: %w", err)
	}

	return ecdsaKey, blsPub, nil
}

func weiToXGRString(wei *big.Int, decimals int) string {
	if wei == nil {
		return "0"
	}
	base := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	q := new(big.Int)
	r := new(big.Int)
	q.QuoRem(wei, base, r)
	if r.Sign() == 0 {
		return q.String()
	}

	frac := r.Text(10)
	if len(frac) < decimals {
		frac = strings.Repeat("0", decimals-len(frac)) + frac
	}
	frac = strings.TrimRight(frac, "0")

	return q.String() + "." + frac
}

func stakeToWei(amountStr string, decimals int) *big.Int {
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

func queryValidatorMinSelfStake(relayer txrelayer.TxRelayer) (*big.Int, error) {
	method := abis.StakingABI.Methods["VALIDATOR_MIN_SELF_STAKE"]
	if method == nil {
		return nil, fmt.Errorf("staking ABI missing VALIDATOR_MIN_SELF_STAKE")
	}

	resp, err := relayer.Call(ethgo.ZeroAddress, ethgo.Address(staking.AddrStakingContract), method.ID())
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
	mp, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("decode VALIDATOR_MIN_SELF_STAKE: unexpected type")
	}
	v, ok := mp["0"].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("decode VALIDATOR_MIN_SELF_STAKE: missing value")
	}

	return v, nil
}

func isBLSRegistered(relayer txrelayer.TxRelayer, addr types.Address, pub []byte) (bool, error) {
	mInfo := abis.StakingABI.Methods["validatorInfo"]
	if mInfo == nil {
		return false, fmt.Errorf("staking ABI missing validatorInfo")
	}

	inp, err := mInfo.Inputs.Encode(map[string]interface{}{"account": ethgo.Address(addr)})
	if err != nil {
		return false, err
	}

	rawInfo, err := relayer.Call(ethgo.ZeroAddress, ethgo.Address(staking.AddrStakingContract), append(mInfo.ID(), inp...))
	if err != nil {
		return false, err
	}

	infoBytes, err := hexToBytes(rawInfo)
	if err != nil {
		return false, err
	}

	decoded, err := mInfo.Outputs.Decode(infoBytes)
	if err != nil {
		return false, err
	}

	mp, ok := decoded.(map[string]interface{})
	if !ok {
		return false, fmt.Errorf("decode validatorInfo: unexpected type")
	}

	blsRaw, ok := mp["blsPubKey"]
	if !ok {
		blsRaw = mp["4"]
	}

	chainPub, err := toBytes(blsRaw)
	if err != nil {
		return false, fmt.Errorf("decode validatorInfo blsPubKey: %w", err)
	}

	return bytes.Equal(chainPub, pub), nil
}

func toBytes(raw interface{}) ([]byte, error) {
	switch v := raw.(type) {
	case []byte:
		return v, nil
	case string:
		b, err := hexToBytes(v)
		if err != nil {
			return nil, fmt.Errorf("invalid hex value")
		}

		return b, nil
	}

	rv := reflect.ValueOf(raw)
	if !rv.IsValid() {
		return nil, fmt.Errorf("unexpected item type")
	}

	kind := rv.Kind()
	if kind == reflect.Array || kind == reflect.Slice {
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			b := make([]byte, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				b[i] = byte(rv.Index(i).Uint())
			}

			return b, nil
		}
	}

	return nil, fmt.Errorf("unexpected item type")
}

func methodSelector(signature string) []byte {
	return crypto.Keccak256([]byte(signature))[:4]
}

func ensureMethodSelector(methodName, signature string, id []byte) error {
	expected := methodSelector(signature)
	if !bytes.Equal(id, expected) {
		return fmt.Errorf("staking ABI selector mismatch for %s: got 0x%s expected 0x%s", methodName, hex.EncodeToString(id), hex.EncodeToString(expected))
	}

	return nil
}

func sendRegisterBLS(relayer txrelayer.TxRelayer, key ethgo.Key, pub []byte) error {
	m := abis.StakingABI.Methods["registerBLSPublicKey"]
	if m == nil {
		return fmt.Errorf("staking ABI missing registerBLSPublicKey")
	}
	if err := ensureMethodSelector("registerBLSPublicKey", "registerBLSPublicKey(bytes)", m.ID()); err != nil {
		return err
	}
	inp, err := m.Inputs.Encode(map[string]interface{}{
		"blsPubKey": pub,
	})
	if err != nil {
		return err
	}
	to := ethgo.Address(staking.AddrStakingContract)
	tx := &ethgo.Transaction{
		To:    &to,
		Input: append(m.ID(), inp...),
		Type:  ethgo.TransactionDynamicFee,
	}
	receipt, err := relayer.SendTransaction(tx, key)
	if err != nil {
		return fmt.Errorf("registerBLSPublicKey tx failed: %w", err)
	}
	if receipt == nil || receipt.Status != uint64(types.ReceiptSuccess) {
		return fmt.Errorf("registerBLSPublicKey reverted")
	}
	return nil
}

func sendStake(relayer txrelayer.TxRelayer, key ethgo.Key, stakeWei *big.Int) error {
	m := abis.StakingABI.Methods["stake"]
	if m == nil {
		return fmt.Errorf("staking ABI missing stake")
	}
	if err := ensureMethodSelector("stake", "stake()", m.ID()); err != nil {
		return err
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

// compile-time sanity: ensure we pull in crypto package (kept for future signature checks)
var _ = crypto.Keccak256

func hexToBytes(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0x" {
		return []byte{}, nil
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		s = s[2:]
	}
	if len(s)%2 == 1 {
		s = "0" + s
	}
	return hex.DecodeString(s)
}
