package e2e

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/xgr-network/xgr-node/contracts/abis"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/e2e/framework"
	"github.com/xgr-network/xgr-node/helper/common"
	"github.com/xgr-network/xgr-node/types"
)

type validatorPoolConfigE2E struct {
	exists            bool
	active            bool
	delegationEnabled bool
	maxDelegatedStake *big.Int
	minDelegatorStake *big.Int
	commissionBps     uint16
}

func TestPoS_StakingV2_DelegationPoolDefaultsAndOpenFlow(t *testing.T) {
	servers, keys, addrs, _ := setupStakingV2ClusterNoAutoStake(t, 2, 5, 1, 2)

	minStake, err := queryMinStake(servers[0], addrs[0])
	require.NoError(t, err)
	require.NoError(t, framework.StakeAmount(addrs[0], keys[0], minStake, servers[0]))

	pool, err := queryValidatorPoolConfig(servers[0], addrs[0], addrs[0])
	require.NoError(t, err)
	require.False(t, pool.delegationEnabled)
	require.Zero(t, pool.maxDelegatedStake.Sign())
	require.Equal(t, uint16(0), pool.commissionBps)

	delegatorKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)
	delegator := crypto.PubKeyToAddress(&delegatorKey.PublicKey)

	delegatorMinStake, err := queryDelegatorMinStake(servers[0], addrs[0])
	require.NoError(t, err)
	fundAccount(t, servers[0], addrs[0], keys[0], delegator, new(big.Int).Mul(delegatorMinStake, big.NewInt(3)))

	receipt, err := sendDelegateTx(servers[0], delegator, delegatorKey, addrs[0], delegatorMinStake)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptFailed), receipt.Status)

	require.NoError(t, sendSetValidatorPoolConfigTx(servers[0], addrs[0], keys[0], true, new(big.Int).Mul(delegatorMinStake, big.NewInt(10)), 150))

	receipt, err = sendDelegateTx(servers[0], delegator, delegatorKey, addrs[0], delegatorMinStake)
	require.NoError(t, err)
	require.Equal(t, uint64(types.ReceiptSuccess), receipt.Status)
}

func queryDelegatorMinStake(srv *framework.TestServer, from types.Address) (*big.Int, error) {
	m := abis.StakingABI.Methods["DELEGATOR_MIN_STAKE"]
	if m == nil {
		return nil, errors.New("staking ABI missing DELEGATOR_MIN_STAKE")
	}

	to := ethgo.Address(staking.AddrStakingContract)
	resp, err := srv.JSONRPC().Eth().Call(&ethgo.CallMsg{
		From:     ethgo.Address(from),
		To:       &to,
		Data:     m.ID(),
		GasPrice: framework.TestGasPriceUint64(),
		Value:    big.NewInt(0),
	}, ethgo.Latest)
	if err != nil {
		return nil, err
	}

	return common.ParseUint256orHex(&resp)
}

func queryValidatorPoolConfig(srv *framework.TestServer, from, validator types.Address) (*validatorPoolConfigE2E, error) {
	m := abis.StakingABI.Methods["validatorPool"]
	if m == nil {
		return nil, errors.New("staking ABI missing validatorPool")
	}

	input, err := m.Inputs.Encode(map[string]interface{}{"validator": ethgo.Address(validator)})
	if err != nil {
		return nil, err
	}

	to := ethgo.Address(staking.AddrStakingContract)
	resp, err := srv.JSONRPC().Eth().Call(&ethgo.CallMsg{
		From:     ethgo.Address(from),
		To:       &to,
		Data:     append(m.ID(), input...),
		GasPrice: framework.TestGasPriceUint64(),
		Value:    big.NewInt(0),
	}, ethgo.Latest)
	if err != nil {
		return nil, err
	}

	bytesResp, err := common.ParseBytes(&resp)
	if err != nil {
		return nil, err
	}

	decoded, err := m.Outputs.Decode(bytesResp)
	if err != nil {
		return nil, err
	}
	out := decoded.(map[string]interface{})["0"].(map[string]interface{})

	cfg := &validatorPoolConfigE2E{}
	cfg.exists, _ = out["exists"].(bool)
	cfg.active, _ = out["active"].(bool)
	cfg.delegationEnabled, _ = out["delegationEnabled"].(bool)
	cfg.maxDelegatedStake, _ = out["maxTotalDelegatedStake"].(*big.Int)
	cfg.minDelegatorStake, _ = out["minDelegatorStake"].(*big.Int)
	cfg.commissionBps, _ = out["commissionBps"].(uint16)
	if cfg.maxDelegatedStake == nil {
		cfg.maxDelegatedStake = big.NewInt(0)
	}
	if cfg.minDelegatorStake == nil {
		cfg.minDelegatorStake = big.NewInt(0)
	}

	return cfg, nil
}

func sendSetValidatorPoolConfigTx(
	srv *framework.TestServer,
	from types.Address,
	key *ecdsa.PrivateKey,
	enabled bool,
	maxStake *big.Int,
	commissionBps uint16,
) error {
	m := abis.StakingABI.Methods["setValidatorPoolConfig"]
	if m == nil {
		return errors.New("staking ABI missing setValidatorPoolConfig")
	}

	inp, err := m.Inputs.Encode(map[string]interface{}{
		"delegationEnabled":      enabled,
		"maxTotalDelegatedStake": maxStake,
		"minDelegatorStake":      big.NewInt(0),
		"commissionBps":          commissionBps,
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), framework.DefaultTimeout)
	defer cancel()

	receipt, err := srv.SendRawTx(ctx, &framework.PreparedTransaction{
		From:     from,
		To:       &staking.AddrStakingContract,
		GasPrice: framework.TestGasPrice(),
		Gas:      framework.DefaultGasLimit,
		Value:    big.NewInt(0),
		Input:    append(m.ID(), inp...),
	}, key)
	if err != nil {
		return err
	}
	if receipt.Status != uint64(types.ReceiptSuccess) {
		return errors.New("setValidatorPoolConfig transaction failed")
	}

	return nil
}

func sendDelegateTx(
	srv *framework.TestServer,
	from types.Address,
	key *ecdsa.PrivateKey,
	validator types.Address,
	amount *big.Int,
) (*ethgo.Receipt, error) {
	m := abis.StakingABI.Methods["delegate"]
	if m == nil {
		return nil, errors.New("staking ABI missing delegate")
	}

	inp, err := m.Inputs.Encode(map[string]interface{}{"validator": ethgo.Address(validator)})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), framework.DefaultTimeout)
	defer cancel()

	return srv.SendRawTx(ctx, &framework.PreparedTransaction{
		From:     from,
		To:       &staking.AddrStakingContract,
		GasPrice: framework.TestGasPrice(),
		Gas:      framework.DefaultGasLimit,
		Value:    amount,
		Input:    append(m.ID(), inp...),
	}, key)
}
