package ibft

import (
	"encoding/binary"
	"fmt"
	"math/big"

	xcrypto "github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func (i *backendIBFT) effectiveVotingPowerSnapshot(
	height uint64,
	validatorSet validators.Validators,
	parentHeader *types.Header,
) (map[string]*big.Int, effectivePowerSnapshot, error) {
	if parentHeader == nil {
		return nil, effectivePowerSnapshot{}, fmt.Errorf("parent header required for stake-weighted snapshot at height %d", height)
	}
	// Block 1 in direct-PoS chains has parent=genesis(0). Genesis bootstrap stakes
	// are marked joinedAtBlock=1, so snapshot(0) is intentionally not mature yet.
	// Keep block 1 in unit voting mode and enable stake-weighted mode from block 2.
	parentPoSActive := parentHeader != nil && parentHeader.Number > 0 && i.forkManager.IsPosActive(parentHeader.Number)
	stakeWeightedActive := parentPoSActive
	if !stakeWeightedActive {
		return computeVotingPowersFromStakeSnapshot(
			height,
			validatorSet,
			nil,
			nil,
			false,
			i.uptimeCfg,
		)
	}

	decayTx, err := i.executor.BeginTxn(parentHeader.StateRoot, parentHeader, types.ZeroAddress)
	if err != nil {
		return nil, effectivePowerSnapshot{}, err
	}

	var stakeSnapshot map[types.Address]*big.Int
	if stakeWeightedActive {
		stakeSnapshot, err = i.forkManager.GetValidatorStakeSnapshot(height, validatorSet)
		if err != nil {
			return nil, effectivePowerSnapshot{}, err
		}
	}

	return computeVotingPowersFromStakeSnapshot(
		height,
		validatorSet,
		stakeSnapshot,
		decayTx,
		stakeWeightedActive,
		i.uptimeCfg,
	)
}

func hashEffectiveWeights(weights map[string]*big.Int, set validators.Validators, total, quorum *big.Int) types.Hash {
	hasher := make([]byte, 0, set.Len()*64+64)
	for idx := 0; idx < set.Len(); idx++ {
		addr := set.At(uint64(idx)).Addr()
		hasher = append(hasher, addr.Bytes()...)
		w := weights[types.AddressToString(addr)]
		if w == nil {
			w = big.NewInt(0)
		}
		hasher = append(hasher, w.FillBytes(make([]byte, 32))...)
	}

	hasher = append(hasher, total.FillBytes(make([]byte, 32))...)
	hasher = append(hasher, quorum.FillBytes(make([]byte, 32))...)

	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(set.Len()))
	hasher = append(hasher, size[:]...)

	if len(hasher) == 0 {
		return types.ZeroHash
	}

	return types.BytesToHash(xcrypto.Keccak256(hasher))
}
