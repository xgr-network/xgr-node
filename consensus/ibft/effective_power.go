package ibft

import (
	"encoding/binary"
	"fmt"
	"math/big"

	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	xcrypto "github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
	contractstore "github.com/xgr-network/xgr-node/validators/store/contract"
)

func (i *backendIBFT) effectiveVotingPowerSnapshot(
	height uint64,
	validatorSet validators.Validators,
	parentHeader *types.Header,
) (map[string]*big.Int, effectivePowerSnapshot, error) {
	var (
		stateTx *state.Transition
		err     error
	)
	if parentHeader == nil {
		return nil, effectivePowerSnapshot{}, fmt.Errorf("parent header required for stake-weighted snapshot at height %d", height)
	}
	stateTx, err = i.executor.BeginTxn(parentHeader.StateRoot, parentHeader, types.ZeroAddress)
	if err != nil {
		return nil, effectivePowerSnapshot{}, err
	}

	// Block 1 in direct-PoS chains has parent=genesis(0). Genesis bootstrap stakes
	// are marked joinedAtBlock=1, so snapshot(0) is intentionally not mature yet.
	// Keep block 1 in unit voting mode and enable stake-weighted mode from block 2.
	parentPoSActive := parentHeader != nil && parentHeader.Number > 0 && i.forkManager.IsPosActive(parentHeader.Number)
	heightPoSActive := i.forkManager.IsPosActive(height)
	stakeWeightedActive := parentPoSActive
	snapshotHeight, err := stakeSnapshotHeight(height, parentHeader, stakeWeightedActive)
	if err != nil {
		return nil, effectivePowerSnapshot{}, err
	}

	result, snapshot, err := i.snapshotVotingPowers(height, snapshotHeight, validatorSet, stateTx, stakeWeightedActive)
	if err != nil {
		return nil, effectivePowerSnapshot{}, err
	}
	if stakeWeightedActive && stateTx != nil && !contractstore.IsEmergencyModeActive(stateTx) && i.uptimeCfg.MicroEpochNominalWeight > 1 && validatorSet.Len() > 1 {
		total := uint64(0)
		allUnit := true
		for idx := 0; idx < validatorSet.Len(); idx++ {
			addr := validatorSet.At(uint64(idx)).Addr()
			p := result[types.AddressToString(addr)]
			if p == nil {
				p = big.NewInt(0)
			}
			v := p.Uint64()
			total += v
			if v != 1 {
				allUnit = false
			}
		}
		if total == uint64(validatorSet.Len()) && allUnit {
			parentNumber := uint64(0)
			parentHash := types.ZeroHash.String()
			parentStateRoot := types.ZeroHash.String()
			if parentHeader != nil {
				parentNumber = parentHeader.Number
				parentHash = parentHeader.Hash.String()
				parentStateRoot = parentHeader.StateRoot.String()
			}
			return nil, effectivePowerSnapshot{}, fmt.Errorf(
				"stake-weighted verify rejected classic-unit fallback: height=%d parentNumber=%d parentHash=%s parentStateRoot=%s isPosActiveAtParent=%t isPosActiveAtHeight=%t nominalWeightUnits=%d total=%d validatorCount=%d votingPowers=%v",
				height,
				parentNumber,
				parentHash,
				parentStateRoot,
				parentPoSActive,
				heightPoSActive,
				i.uptimeCfg.MicroEpochNominalWeight,
				total,
				validatorSet.Len(),
				snapshot.powers,
			)
		}
	}

	return result, snapshot, nil
}

func (i *backendIBFT) snapshotVotingPowers(
	height uint64,
	snapshotHeight uint64,
	validatorSet validators.Validators,
	stateTx *state.Transition,
	stakeWeightedActive bool,
) (map[string]*big.Int, effectivePowerSnapshot, error) {
	result := make(map[string]*big.Int, validatorSet.Len())
	snapshotPowers := make(map[string]string, validatorSet.Len())
	total := big.NewInt(0)
	emergency := stakeWeightedActive && stateTx != nil && contractstore.IsEmergencyModeActive(stateTx)
	for index := 0; index < validatorSet.Len(); index++ {
		addr := validatorSet.At(uint64(index)).Addr()
		if !stakeWeightedActive || stateTx == nil {
			result[types.AddressToString(addr)] = big.NewInt(1)
			snapshotPowers[addr.String()] = "1"
			total.Add(total, big.NewInt(1))
			continue
		}
		weight := pos.EffectiveWeight(stateTx.Txn(), addr, i.uptimeCfg)
		nominal := pos.UptimeNominalWeight(stateTx.Txn(), addr)
		if nominal == 0 {
			nominal = i.uptimeCfg.MicroEpochNominalWeight
		}
		stake := contractstore.ReadValidatorVotingStakeAt(stateTx, addr, snapshotHeight)

		if stake == nil || stake.Sign() <= 0 {
			return nil, effectivePowerSnapshot{}, fmt.Errorf("missing effective stake for stake-weighted validator %s at height %d", addr, height)
		}

		w := big.NewInt(1)
		if !emergency {
			w = pos.WeightedStake(stake, weight, nominal)
			if stake.Sign() > 0 && weight > 0 && nominal > 0 && w.Sign() == 0 {
				w = big.NewInt(1)
			}
		}
		result[types.AddressToString(addr)] = w
		snapshotPowers[addr.String()] = w.String()
		total.Add(total, w)
	}

	quorum := weightedQuorumThreshold(total)

	return result, effectivePowerSnapshot{
		hash:             hashEffectiveWeights(result, validatorSet, total, quorum),
		powers:           snapshotPowers,
		totalVotingPower: total.String(),
		quorumThreshold:  quorum.String(),
	}, nil
}

func stakeSnapshotHeight(height uint64, parentHeader *types.Header, stakeWeightedActive bool) (uint64, error) {
	if !stakeWeightedActive {
		// Unit voting mode ignores stake snapshots entirely.
		return 0, nil
	}

	if parentHeader != nil {
		return parentHeader.Number, nil
	}

	if height == 0 {
		return 0, fmt.Errorf("stake-weighted voting power snapshot requires parent state (height=0)")
	}

	return height - 1, nil
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
