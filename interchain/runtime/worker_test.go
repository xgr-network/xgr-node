package runtime

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	stakingcontract "github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/interchain/evm"
	"github.com/xgr-network/xgr-node/types"
)

type fakeInterchainState struct {
	origin     uint64
	validators map[types.Address]*stakingcontract.ValidatorInfo
}

func (f *fakeInterchainState) OriginChainID() uint64 { return f.origin }
func (f *fakeInterchainState) IsSynchronized() bool  { return true }
func (f *fakeInterchainState) ValidatorInfo(addr types.Address) (*stakingcontract.ValidatorInfo, error) {
	info := f.validators[addr]
	if info == nil {
		return &stakingcontract.ValidatorInfo{}, nil
	}
	return info, nil
}

func TestInterchainSignerEligibilityIsOptInActiveAndMatchingBLS(t *testing.T) {
	validator := types.StringToAddress("0x1111111111111111111111111111111111111111")
	key := bytes.Repeat([]byte{0x42}, 48)
	state := &fakeInterchainState{
		origin: 1643,
		validators: map[types.Address]*stakingcontract.ValidatorInfo{
			validator: {
				Exists: true,
				Active: true,
				StakedAmount: big.NewInt(1),
				BLSPubKey: append([]byte(nil), key...),
			},
		},
	}
	w := &Worker{state: state}
	set := &evm.ValidatorSet{
		SetID:         1,
		Validators:    []types.Address{validator},
		BLSPublicKeys: [][]byte{append([]byte(nil), key...)},
	}

	ok, err := w.interchainSignerEligible(set, validator)
	require.NoError(t, err)
	require.True(t, ok)

	state.validators[validator].Active = false
	ok, err = w.interchainSignerEligible(set, validator)
	require.NoError(t, err)
	require.False(t, ok)

	state.validators[validator].Active = true
	state.validators[validator].BLSPubKey = bytes.Repeat([]byte{0x99}, 48)
	ok, err = w.interchainSignerEligible(set, validator)
	require.NoError(t, err)
	require.False(t, ok)

	state.validators[validator].BLSPubKey = append([]byte(nil), key...)
	set.Validators = nil
	set.BLSPublicKeys = nil
	ok, err = w.interchainSignerEligible(set, validator)
	require.NoError(t, err)
	require.False(t, ok)
}


func TestInterchainProtocolTopicV313(t *testing.T) {
	require.Equal(t, "/xgr/interchain/3.0.0", TopicID)
}
