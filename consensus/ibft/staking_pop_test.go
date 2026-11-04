package ibft

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/consensus"
	protocol "github.com/xgr-network/xgr-node/consensus/ibft/blspop"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	stakingHelper "github.com/xgr-network/xgr-node/helper/staking"
	"github.com/xgr-network/xgr-node/state"
	itrie "github.com/xgr-network/xgr-node/state/immutable-trie"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func TestStakingPoPSingleGateMigrationAndRestart(t *testing.T) {
	for _, height := range []uint64{20, 21, 25} {
		t.Run(new(big.Int).SetUint64(height).String(), func(t *testing.T) {
			forks := chain.AllForksEnabled.Copy()
			forks.SetFork(chain.StakingBLSPoP, chain.NewFork(height))
			params := &chain.Params{Forks: forks, ChainID: 1643}
			storage := itrie.NewState(itrie.NewMemoryStorage())
			ex := state.NewExecutor(params, storage, hclog.NewNullLogger())
			ex.GetHash = func(*types.Header) state.GetHashByNumber { return func(uint64) types.Hash { return types.ZeroHash } }
			vals := validators.NewBLSValidatorSet()
			entries := []protocol.Entry{}
			for n := byte(1); n <= 4; n++ {
				key, err := crypto.GenerateBLSKey()
				require.NoError(t, err)
				pk, err := crypto.BLSSecretKeyToPubkeyBytes(key)
				require.NoError(t, err)
				addr := types.Address{19: n}
				proof, err := crypto.CreateStakingPoP(key, big.NewInt(1643), staking.AddrStakingContract, addr, new(big.Int))
				require.NoError(t, err)
				require.NoError(t, vals.Add(validators.NewBLSValidator(addr, pk)))
				entries = append(entries, protocol.Entry{Validator: "0x" + hex.EncodeToString(addr[:]), PublicKey: "0x" + hex.EncodeToString(pk), Proof: "0x" + hex.EncodeToString(proof)})
			}
			legacy, err := stakingHelper.PredeployStakingSC(vals, stakingHelper.PredeployParams{MinValidatorCount: 4, MaxValidatorCount: 5, EpochSize: 10})
			require.NoError(t, err)
			// Sentinel history storage must survive exactly, even though the upgrade
			// code has no reason to understand its application-level meaning.
			historyKey := types.StringToHash("0xabc123")
			legacy.Storage[historyKey] = types.StringToHash("0xdeadbeef")
			root, err := ex.WriteGenesis(map[types.Address]*chain.GenesisAccount{staking.AddrStakingContract: legacy}, types.ZeroHash)
			require.NoError(t, err)
			parent, err := ex.BeginTxn(root, &types.Header{Number: height - 1, GasLimit: 60_000_000}, types.ZeroAddress)
			require.NoError(t, err)
			epoch := ((height - 1) / 10) + 1
			require.NoError(t, pos.StoreMacroEpochValidatorSet(parent, epoch, vals))
			_, parentRoot, err := parent.Commit()
			require.NoError(t, err)
			manifest, err := protocol.NewManifest(1643, height, entries)
			require.NoError(t, err)
			raw, err := manifest.CanonicalJSON()
			require.NoError(t, err)
			verified, err := manifest.Verify(1643, height)
			require.NoError(t, err)
			params.StakingBLSPoPMigration = &chain.StakingBLSPoPMigration{Manifest: string(raw), Digest: verified.Digest()}
			backend := &backendIBFT{config: &consensus.Config{Params: params}, executor: ex, epochSize: 10, forkManager: &parentSealsForkManager{pos: true}}
			require.NoError(t, backend.configureStakingPoP())
			tr, err := ex.ProcessBlock(parentRoot, &types.Block{Header: &types.Header{Number: height, GasLimit: 60_000_000}}, types.ZeroAddress)
			require.NoError(t, err)
			for slot, expected := range legacy.Storage {
				require.Equal(t, expected, tr.Txn().GetState(staking.AddrStakingContract, slot))
			}
			require.Equal(t, legacy.Balance, tr.Txn().GetBalance(staking.AddrStakingContract))
			_, candidate, err := staking.PoPBytecodes()
			require.NoError(t, err)
			require.Equal(t, candidate, tr.GetCode(staking.AddrStakingContract))
			for idx := 0; idx < vals.Len(); idx++ {
				v := vals.At(uint64(idx)).(*validators.BLSValidator)
				require.NoError(t, tr.RequireStakingPoP(v.Addr(), v.BLSPublicKey))
				require.Equal(t, types.Hash{31: 1}, tr.Txn().GetState(staking.AddrStakingContract, state.StakingPoPNonceSlot(v.Addr())))
				args, err := abi.MustNewType("tuple(address,bytes)").Encode([]interface{}{ethgo.Address(v.Addr()), []byte(v.BLSPublicKey)})
				require.NoError(t, err)
				result := tr.Call2(types.ZeroAddress, staking.AddrStakingContract, append(crypto.Keccak256([]byte("blsPoPVerified(address,bytes)"))[:4], args...), new(big.Int), 1_000_000)
				require.NoError(t, result.Err)
				require.Equal(t, types.Hash{31: 1}.Bytes(), result.ReturnValue, "Solidity getter must recognize Go-migrated proof status")
			}
			allowed := map[types.Hash]types.Hash{types.BytesToHash(big.NewInt(state.StakingPoPMigrationSlot).Bytes()): verified.Digest()}
			for idx := 0; idx < vals.Len(); idx++ {
				v := vals.At(uint64(idx)).(*validators.BLSValidator)
				allowed[state.StakingPoPVerifiedSlot(v.Addr(), v.BLSPublicKey)] = types.Hash{31: 1}
				allowed[state.StakingPoPNonceSlot(v.Addr())] = types.Hash{31: 1}
			}
			objects, err := tr.Txn().Commit(false)
			require.NoError(t, err)
			for _, object := range objects {
				if object.Address != staking.AddrStakingContract {
					require.Empty(t, object.Storage, "migration must not write any other account storage")
					require.False(t, object.DirtyCode)
					continue
				}
				require.Len(t, object.Storage, len(allowed), "migration write footprint must contain only new PoP slots")
				for _, word := range object.Storage {
					expected, ok := allowed[types.BytesToHash(word.Key)]
					require.True(t, ok, "migration wrote outside appended PoP storage")
					require.False(t, word.Deleted)
					require.Equal(t, expected, types.BytesToHash(word.Val))
				}
			}
			_, afterRoot, err := tr.Commit()
			require.NoError(t, err)
			// Read-only queries over H's already migrated state must be idempotent.
			view, err := ex.BeginTxn(afterRoot, &types.Header{Number: height, GasLimit: 60_000_000}, types.ZeroAddress)
			require.NoError(t, err)
			_, viewRoot, err := view.Commit()
			require.NoError(t, err)
			require.Equal(t, afterRoot, viewRoot)
			// A newly constructed executor/backend must recover from persisted state.
			restarted := state.NewExecutor(params, storage, hclog.NewNullLogger())
			restarted.GetHash = ex.GetHash
			backend.executor = restarted
			require.NoError(t, backend.configureStakingPoP())
			view, err = restarted.BeginTxn(afterRoot, &types.Header{Number: height + 1, GasLimit: 60_000_000}, types.ZeroAddress)
			require.NoError(t, err)
			_, viewRoot, err = view.Commit()
			require.NoError(t, err)
			require.Equal(t, afterRoot, viewRoot)
			_, err = restarted.BeginTxn(parentRoot, &types.Header{Number: height + 1, GasLimit: 60_000_000}, types.ZeroAddress)
			require.ErrorContains(t, err, "missing after activation")
			// Historical parent-state reads do not require flags written at H.
			historical, err := restarted.BeginTxn(parentRoot, &types.Header{Number: height - 1, GasLimit: 60_000_000}, types.ZeroAddress)
			require.NoError(t, err)
			require.Equal(t, legacy.Code, historical.GetCode(staking.AddrStakingContract))
			// Removing one valid proof must halt migration, not silently shrink quorum.
			missing, err := protocol.NewManifest(1643, height, entries[:3])
			require.NoError(t, err)
			missingRaw, err := missing.CanonicalJSON()
			require.NoError(t, err)
			missingVerified, err := missing.Verify(1643, height)
			require.NoError(t, err)
			params.StakingBLSPoPMigration = &chain.StakingBLSPoPMigration{Manifest: string(missingRaw), Digest: missingVerified.Digest()}
			require.NoError(t, backend.configureStakingPoP())
			_, err = restarted.ProcessBlock(parentRoot, &types.Block{Header: &types.Header{Number: height, GasLimit: 60_000_000}}, types.ZeroAddress)
			require.ErrorContains(t, err, "missing")
		})
	}
}
