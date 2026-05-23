package ibft

import (
	"fmt"
	"math/big"

	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func computeVotingPowersFromStakeSnapshot(
	height uint64,
	validatorSet validators.Validators,
	stakeSnapshot map[types.Address]*big.Int,
	decayTx *state.Transition,
	stakeWeightedActive bool,
	uptimeCfg pos.UptimeConfig,
) (map[string]*big.Int, effectivePowerSnapshot, error) {
	if validatorSet == nil || validatorSet.Len() == 0 {
		return nil, effectivePowerSnapshot{}, fmt.Errorf("validator set is empty at height %d", height)
	}

	if stakeWeightedActive {
		if decayTx == nil {
			return nil, effectivePowerSnapshot{}, fmt.Errorf("stake-weighted voting power requires decay state at height %d", height)
		}

		if stakeSnapshot == nil {
			return nil, effectivePowerSnapshot{}, fmt.Errorf("stake-weighted voting power requires stake snapshot at height %d", height)
		}
	}

	result := make(map[string]*big.Int, validatorSet.Len())
	snapshotPowers := make(map[string]string, validatorSet.Len())
	total := big.NewInt(0)

	for idx := 0; idx < validatorSet.Len(); idx++ {
		addr := validatorSet.At(uint64(idx)).Addr()

		if !stakeWeightedActive {
			result[types.AddressToString(addr)] = big.NewInt(1)
			snapshotPowers[addr.String()] = "1"
			total.Add(total, big.NewInt(1))
			continue
		}

		stake, ok := stakeSnapshot[addr]
		if !ok || stake == nil || stake.Sign() <= 0 {
			return nil, effectivePowerSnapshot{}, fmt.Errorf("missing effective stake for stake-weighted validator %s at height %d", addr, height)
		}

		weight := pos.EffectiveWeight(decayTx.Txn(), addr, uptimeCfg)
		nominal := pos.UptimeNominalWeight(decayTx.Txn(), addr)
		if nominal == 0 {
			nominal = uptimeCfg.MicroEpochNominalWeight
		}

		power := pos.WeightedStake(stake, weight, nominal)
		if stake.Sign() > 0 && weight > 0 && nominal > 0 && power.Sign() == 0 {
			power = big.NewInt(1)
		}

		result[types.AddressToString(addr)] = power
		snapshotPowers[addr.String()] = power.String()
		total.Add(total, power)
	}

	quorum := weightedQuorumThreshold(total)

	return result, effectivePowerSnapshot{
		hash:             hashEffectiveWeights(result, validatorSet, total, quorum),
		powers:           snapshotPowers,
		totalVotingPower: total.String(),
		quorumThreshold:  quorum.String(),
	}, nil
}
