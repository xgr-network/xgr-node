package pos

import (
	"math/big"

	"github.com/xgr-network/xgr-node/types"
)

func EffectiveWeight(tx TxnWrapper, addr types.Address, cfg UptimeConfig) uint64 {
	eff := u64FromHash(tx.GetState(PosSysAddr, keyUptimeEffectiveWeight(addr)))
	if eff > 0 {
		return eff
	}

	nom := u64FromHash(tx.GetState(PosSysAddr, keyUptimeNominalWeight(addr)))
	if nom > 0 {
		return nom
	}

	return cfg.MicroEpochNominalWeight
}

func WeightedStake(stake *big.Int, effective, nominal uint64) *big.Int {
	if stake == nil || stake.Sign() == 0 || nominal == 0 {
		return new(big.Int)
	}
	if effective >= nominal {
		return new(big.Int).Set(stake)
	}
	num := new(big.Int).Mul(stake, new(big.Int).SetUint64(effective))
	return num.Div(num, new(big.Int).SetUint64(nominal))
}

func UptimeNominalWeight(tx TxnWrapper, addr types.Address) uint64 {
	return u64FromHash(tx.GetState(PosSysAddr, keyUptimeNominalWeight(addr)))
}

func UptimeEffectiveWeight(tx TxnWrapper, addr types.Address) uint64 {
	return u64FromHash(tx.GetState(PosSysAddr, keyUptimeEffectiveWeight(addr)))
}

func UptimeInactivity(tx TxnWrapper, addr types.Address) uint64 {
	return u64FromHash(tx.GetState(PosSysAddr, keyUptimeInactivity(addr)))
}
