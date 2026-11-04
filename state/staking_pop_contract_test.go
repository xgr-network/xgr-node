package state_test

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo/abi"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	stakingHelper "github.com/xgr-network/xgr-node/helper/staking"
	"github.com/xgr-network/xgr-node/state"
	itrie "github.com/xgr-network/xgr-node/state/immutable-trie"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

// Exercises the actual Solidity candidate and existing native verifier inside
// the node EVM; the separate multi-validator test covers fork and block import.
func TestStakingPoPCandidateContract(t *testing.T) {
	raw, err := os.ReadFile("../contracts/staking/pop-artifact/StakingV2PoP.json")
	require.NoError(t, err)
	var artifact struct {
		Runtime string `json:"runtimeBytecode"`
	}
	require.NoError(t, json.Unmarshal(raw, &artifact))
	candidate, err := hex.DecodeString(artifact.Runtime[2:])
	require.NoError(t, err)
	key, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	pk, err := crypto.BLSSecretKeyToPubkeyBytes(key)
	require.NoError(t, err)
	addr := types.Address{19: 1}
	legacy, err := stakingHelper.PredeployStakingSC(validators.NewBLSValidatorSet(validators.NewBLSValidator(addr, pk)), stakingHelper.PredeployParams{MinValidatorCount: 1, MaxValidatorCount: 5, EpochSize: 10})
	require.NoError(t, err)
	forks := chain.AllForksEnabled.Copy()
	ex := state.NewExecutor(&chain.Params{Forks: forks, ChainID: 1643}, itrie.NewState(itrie.NewMemoryStorage()), hclog.NewNullLogger())
	ex.GetHash = func(*types.Header) state.GetHashByNumber { return func(uint64) types.Hash { return types.ZeroHash } }
	root, err := ex.WriteGenesis(map[types.Address]*chain.GenesisAccount{
		addr: {Balance: new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil)}, staking.AddrStakingContract: legacy,
	}, types.ZeroHash)
	require.NoError(t, err)
	tr, err := ex.BeginTxn(root, &types.Header{Number: 20, GasLimit: 60_000_000, BaseFee: 100_000_000_000}, types.ZeroAddress)
	require.NoError(t, err)
	before := map[types.Hash]types.Hash{}
	for slot := range legacy.Storage {
		before[slot] = tr.Txn().GetState(staking.AddrStakingContract, slot)
	}
	require.NoError(t, tr.SetCodeDirectly(staking.AddrStakingContract, candidate))
	for slot, expected := range before {
		require.Equal(t, expected, tr.Txn().GetState(staking.AddrStakingContract, slot), "code replacement changed a legacy storage word")
	}
	input := func(signature, tuple string, args []interface{}) []byte {
		encoded, err := abi.MustNewType(tuple).Encode(args)
		require.NoError(t, err)
		return append(crypto.Keccak256([]byte(signature))[:4], encoded...)
	}
	apply := func(data []byte) bool {
		result, err := tr.Apply(&types.Transaction{From: addr, To: &staking.AddrStakingContract, Nonce: tr.GetNonce(addr), Gas: 3_000_000, GasPrice: big.NewInt(100_000_000_000), Value: new(big.Int), Input: data})
		require.NoError(t, err)
		return result.Err == nil
	}
	chainID := big.NewInt(1643)
	proof, err := crypto.CreateStakingPoP(key, chainID, staking.AddrStakingContract, addr, new(big.Int))
	require.NoError(t, err)
	require.False(t, apply(input("registerBLSPublicKey(bytes)", "tuple(bytes)", []interface{}{pk})))
	require.False(t, apply(input("registerBLSPublicKey(bytes,bytes)", "tuple(bytes,bytes)", []interface{}{pk, []byte{}})))
	wrongProof, err := crypto.CreateStakingPoP(key, big.NewInt(1887), staking.AddrStakingContract, addr, new(big.Int))
	require.NoError(t, err)
	require.False(t, apply(input("registerBLSPublicKey(bytes,bytes)", "tuple(bytes,bytes)", []interface{}{pk, wrongProof})))
	require.True(t, apply(input("registerBLSPublicKey(bytes,bytes)", "tuple(bytes,bytes)", []interface{}{pk, proof})), "valid Go proof must pass Solidity/native verification")
	require.False(t, apply(input("registerBLSPublicKey(bytes,bytes)", "tuple(bytes,bytes)", []interface{}{pk, proof})), "consumed nonce must reject replay")
	proof1, err := crypto.CreateStakingPoP(key, chainID, staking.AddrStakingContract, addr, big.NewInt(1))
	require.NoError(t, err)
	require.True(t, apply(input("registerBLSPublicKey(bytes,bytes)", "tuple(bytes,bytes)", []interface{}{pk, proof1})))
	secureView := state.NewTransition(chain.ForksInTime{StakingBLSPoP: true}, nil, tr.Txn())
	require.NoError(t, secureView.RequireStakingPoP(addr, pk), "Go status reader must match Solidity's actual mapping layout")
	// Existing stake, membership, epoch configuration and history are unchanged
	// by registrations. The same key was used to exclude key-value changes here.
	for slot, expected := range before {
		require.Equal(t, expected, tr.Txn().GetState(staking.AddrStakingContract, slot), "PoP registration changed a legacy storage word")
	}
	rotated, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	rotatedPK, err := crypto.BLSSecretKeyToPubkeyBytes(rotated)
	require.NoError(t, err)
	rotationProof, err := crypto.CreateStakingPoP(rotated, chainID, staking.AddrStakingContract, addr, big.NewInt(2))
	require.NoError(t, err)
	require.False(t, apply(input("registerBLSPublicKey(bytes,bytes)", "tuple(bytes,bytes)", []interface{}{rotatedPK, proof1})))
	require.True(t, apply(input("registerBLSPublicKey(bytes,bytes)", "tuple(bytes,bytes)", []interface{}{rotatedPK, rotationProof})))
	require.NoError(t, secureView.RequireStakingPoP(addr, rotatedPK))
	require.NoError(t, secureView.RequireStakingPoP(addr, pk), "retained macro snapshots still need the old proved key")
	require.False(t, apply(input("registerBLSPublicKey(bytes,bytes)", "tuple(bytes,bytes)", []interface{}{rotatedPK, rotationProof})))

}

// A post-upgrade validator/delegator without any recorded PoP must retain the
// ordinary withdrawal path. Proof admission governs consensus, not fund access.
func TestStakingPoPUnprovedValidatorAndDelegatorCanExit(t *testing.T) {
	validator, delegator := types.Address{19: 1}, types.Address{19: 2}
	key, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	pk, err := crypto.BLSSecretKeyToPubkeyBytes(key)
	require.NoError(t, err)
	account, err := stakingHelper.PredeployStakingSC(validators.NewBLSValidatorSet(validators.NewBLSValidator(validator, pk)), stakingHelper.PredeployParams{MinValidatorCount: 1, MaxValidatorCount: 5, EpochSize: 10})
	require.NoError(t, err)
	_, account.Code, err = staking.PoPBytecodes()
	require.NoError(t, err)
	funding := new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil)
	ex := state.NewExecutor(&chain.Params{Forks: chain.AllForksEnabled.Copy(), ChainID: 1643}, itrie.NewState(itrie.NewMemoryStorage()), hclog.NewNullLogger())
	ex.GetHash = func(*types.Header) state.GetHashByNumber { return func(uint64) types.Hash { return types.ZeroHash } }
	root, err := ex.WriteGenesis(map[types.Address]*chain.GenesisAccount{
		validator: {Balance: new(big.Int).Set(funding)}, delegator: {Balance: new(big.Int).Set(funding)}, staking.AddrStakingContract: account,
	}, types.ZeroHash)
	require.NoError(t, err)
	tr, err := ex.BeginTxn(root, &types.Header{Number: 20, GasLimit: 60_000_000}, types.ZeroAddress)
	require.NoError(t, err)
	call := func(from types.Address, signature, tuple string, args []interface{}, value *big.Int) {
		data, err := abi.MustNewType(tuple).Encode(args)
		require.NoError(t, err)
		result := tr.Call2(from, staking.AddrStakingContract, append(crypto.Keccak256([]byte(signature))[:4], data...), value, 3_000_000)
		require.NoError(t, result.Err, signature)
	}
	zero := new(big.Int)
	stake := new(big.Int).Mul(big.NewInt(200_000), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	delegated := new(big.Int).Div(new(big.Int).Set(stake), big.NewInt(20))
	call(validator, "stake()", "tuple()", nil, stake)
	call(validator, "setValidatorPoolConfig(bool,uint256,uint256,uint16)", "tuple(bool,uint256,uint256,uint16)", []interface{}{true, stake, delegated, uint16(0)}, zero)
	call(delegator, "delegate(address)", "tuple(address)", []interface{}{validator}, delegated)
	call(validator, "setActive(bool)", "tuple(bool)", []interface{}{false}, zero)
	call(delegator, "setDelegationActive(address,bool)", "tuple(address,bool)", []interface{}{validator, false}, zero)
	_, root, err = tr.Commit()
	require.NoError(t, err)
	tr, err = ex.BeginTxn(root, &types.Header{Number: 30, GasLimit: 60_000_000}, types.ZeroAddress)
	require.NoError(t, err)
	secure := state.NewTransition(chain.ForksInTime{StakingBLSPoP: true}, nil, tr.Txn())
	require.Error(t, secure.RequireStakingPoP(validator, pk))
	beforeV, beforeD := new(big.Int).Set(tr.Txn().GetBalance(validator)), new(big.Int).Set(tr.Txn().GetBalance(delegator))
	call(delegator, "unstakeDelegation(address)", "tuple(address)", []interface{}{validator}, zero)
	call(validator, "unstake()", "tuple()", nil, zero)
	require.Equal(t, new(big.Int).Add(beforeD, delegated), tr.Txn().GetBalance(delegator))
	require.Equal(t, new(big.Int).Add(beforeV, new(big.Int).Add(stake, big.NewInt(10))), tr.Txn().GetBalance(validator))
	require.Zero(t, tr.Txn().GetBalance(staking.AddrStakingContract).Sign())
}
