package contract

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/contracts/staking"
	stakingHelper "github.com/xgr-network/xgr-node/helper/staking"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func TestPoPExcludedFromAllSelectionFallbacks(t *testing.T) {
	for _, mode := range []string{"normal", "emergency-active", "emergency-weighted"} {
		t.Run(mode, func(t *testing.T) {
			vals := validators.NewBLSValidatorSet(validators.NewBLSValidator(addr1, testBLSPubKey1), validators.NewBLSValidator(addr2, testBLSPubKey2))
			base := newTransitionWithParams(t, vals, stakingHelper.PredeployParams{MinValidatorCount: 1, MaxValidatorCount: 10, EpochSize: 10})
			tr := state.NewTransition(chain.ForksInTime{StakingBLSPoP: true}, nil, base.Txn())
			*tr.ContextPtr() = *base.ContextPtr()
			stake := mustBig("2000000000000000000000000")
			if mode != "normal" {
				stake = mustBig("100000000000000000000000")
			}
			setMetaStake(t, tr, addr1, stake)
			setMetaStake(t, tr, addr2, stake)
			if mode == "emergency-weighted" {
				setMetaActive(t, tr, addr1, false)
				setMetaActive(t, tr, addr2, false)
			}
			tr.Txn().SetState(staking.AddrStakingContract, state.StakingPoPVerifiedSlot(addr1, testBLSPubKey1), types.Hash{31: 1})
			selected, err := FetchBLSValidators(tr, types.ZeroAddress)
			require.NoError(t, err)
			require.Equal(t, validators.NewBLSValidatorSet(validators.NewBLSValidator(addr1, testBLSPubKey1)), selected)
			tr.Txn().SetState(staking.AddrStakingContract, state.StakingPoPVerifiedSlot(addr1, testBLSPubKey1), types.ZeroHash)
			selected, err = FetchBLSValidators(tr, types.ZeroAddress)
			require.NoError(t, err)
			require.Zero(t, selected.Len(), "unproved keys must not restore liveness through fallback")
		})
	}
}
