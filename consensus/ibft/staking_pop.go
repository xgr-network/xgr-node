package ibft

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/xgr-network/xgr-node/chain"
	protocol "github.com/xgr-network/xgr-node/consensus/ibft/blspop"
	ibftFork "github.com/xgr-network/xgr-node/consensus/ibft/fork"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
	contractstore "github.com/xgr-network/xgr-node/validators/store/contract"
)

func (i *backendIBFT) configureStakingPoP() error {
	params := i.config.Params
	if params == nil || params.Forks == nil {
		return nil
	}
	fork, configured := (*params.Forks)[chain.StakingBLSPoP]
	if !configured {
		if params.StakingBLSPoPMigration != nil {
			return fmt.Errorf("PoP migration supplied without its activation fork")
		}
		return nil
	}
	if fork.Block <= 1 || !i.forkManager.IsPosActive(fork.Block) || !i.forkManager.IsPosActive(fork.Block-1) || i.epochSize < 2 {
		return fmt.Errorf("PoP migration requires an established PoS parent and macro snapshot")
	}
	if len(i.config.Config) > 0 {
		ibftForks, err := ibftFork.GetIBFTForks(i.config.Config)
		if err != nil {
			return err
		}
		for _, scheduled := range ibftForks {
			if scheduled.Deployment != nil && scheduled.Deployment.Value >= fork.Block {
				return fmt.Errorf("legacy staking deployment overlaps or follows PoP activation")
			}
			if scheduled.To != nil && scheduled.To.Value < fork.Block {
				continue
			}
			if scheduled.Type != ibftFork.PoS || scheduled.ValidatorType != validators.BLSValidatorType {
				return fmt.Errorf("PoP activation requires continuing BLS PoS consensus")
			}
		}
	}
	if params.ChainID <= 0 || params.StakingBLSPoPMigration == nil {
		return fmt.Errorf("PoP migration evidence is required")
	}
	m, verified, err := protocol.DecodeManifest([]byte(params.StakingBLSPoPMigration.Manifest), uint64(params.ChainID), fork.Block)
	if err != nil {
		return err
	}
	if verified.Digest() != params.StakingBLSPoPMigration.Digest {
		return fmt.Errorf("PoP manifest digest mismatch")
	}
	i.stakingPoPHeight = fork.Block
	i.stakingPoPManifest = m
	i.stakingPoPVerified = verified
	i.executor.BeginTxnHook = i.applyStakingPoPTransition
	return nil
}

func (i *backendIBFT) applyStakingPoPTransition(header *types.Header, transition *state.Transition) error {
	if !transition.ForksInTime().StakingBLSPoP {
		return nil
	}
	if i.stakingPoPManifest == nil || i.stakingPoPVerified == nil {
		return fmt.Errorf("PoP migration was not configured")
	}
	legacy, candidate, err := staking.PoPBytecodes()
	if err != nil {
		return err
	}
	markerKey := types.BytesToHash(big.NewInt(state.StakingPoPMigrationSlot).Bytes())
	marker := transition.Txn().GetState(staking.AddrStakingContract, markerKey)
	code := transition.GetCode(staking.AddrStakingContract)
	if marker != types.ZeroHash {
		if marker != i.stakingPoPVerified.Digest() || !bytes.Equal(code, candidate) {
			return fmt.Errorf("PoP migration marker/runtime mismatch")
		}
		return nil
	}
	if header.Number != i.stakingPoPHeight {
		return fmt.Errorf("PoP migration missing after activation")
	}
	if !bytes.Equal(code, legacy) {
		return fmt.Errorf("staking runtime does not match frozen legacy bytecode")
	}
	registered, err := contractstore.RegisteredBLSKeys(transition)
	if err != nil {
		return err
	}
	epoch := ((header.Number - 1) / i.epochSize) + 1
	snapshot, exists, err := pos.LoadMacroEpochValidatorSet(transition, epoch, validators.BLSValidatorType)
	if err != nil {
		return err
	}
	if !exists || snapshot == nil || snapshot.Len() == 0 {
		return fmt.Errorf("canonical boundary validator snapshot missing")
	}
	required := make([]protocol.ValidatorKey, 0, registered.Len()+snapshot.Len())
	for _, set := range []validators.Validators{registered, snapshot} {
		for idx := 0; idx < set.Len(); idx++ {
			v, ok := set.At(uint64(idx)).(*validators.BLSValidator)
			if !ok {
				return fmt.Errorf("non-BLS migration validator")
			}
			required = append(required, protocol.ValidatorKey{Validator: v.Addr(), PublicKey: v.BLSPublicKey})
		}
	}
	if err := i.stakingPoPVerified.RequireExactCoverage(required); err != nil {
		return err
	}
	// Validate every write precondition before touching code or storage.
	for _, entry := range i.stakingPoPManifest.Entries {
		addr := types.StringToAddress(entry.Validator)
		if transition.Txn().GetState(staking.AddrStakingContract, state.StakingPoPNonceSlot(addr)) != types.ZeroHash {
			return fmt.Errorf("unexpected pre-migration PoP nonce")
		}
	}
	if err := transition.SetCodeDirectly(staking.AddrStakingContract, candidate); err != nil {
		return err
	}
	for _, entry := range i.stakingPoPManifest.Entries {
		addr := types.StringToAddress(entry.Validator)
		key, err := hex.DecodeString(entry.PublicKey[2:])
		if err != nil {
			return err
		}
		transition.Txn().SetState(staking.AddrStakingContract, state.StakingPoPVerifiedSlot(addr, key), types.Hash{31: 1})
		transition.Txn().SetState(staking.AddrStakingContract, state.StakingPoPNonceSlot(addr), types.Hash{31: 1})
	}
	transition.Txn().SetState(staking.AddrStakingContract, markerKey, i.stakingPoPVerified.Digest())
	return nil
}
