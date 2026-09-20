package runtime

import (
	"bytes"
	"fmt"
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
	checkpointRoot    types.Hash
	checkpointIndex   uint32
	checkpointMailbox types.Address
	checkpointHook    types.Address
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
func (f *fakeInterchainState) OriginCheckpoint(mailbox, hook types.Address) (types.Hash, uint32, error) {
	if f.checkpointMailbox != types.ZeroAddress && mailbox != f.checkpointMailbox {
		return types.ZeroHash, 0, fmt.Errorf("unexpected mailbox")
	}
	if f.checkpointHook != types.ZeroAddress && hook != f.checkpointHook {
		return types.ZeroHash, 0, fmt.Errorf("unexpected hook")
	}
	return f.checkpointRoot, f.checkpointIndex, nil
}

func TestInterchainSignerEligibilityIsOptInActiveAndMatchingBLS(t *testing.T) {
	validator := types.StringToAddress("0x1111111111111111111111111111111111111111")
	key := bytes.Repeat([]byte{0x42}, 48)
	state := &fakeInterchainState{
		origin: 1643,
		validators: map[types.Address]*stakingcontract.ValidatorInfo{
			validator: {Exists: true, Active: true, StakedAmount: big.NewInt(1), BLSPubKey: append([]byte(nil), key...)},
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

func TestOriginCheckpointReadsConfiguredMerkleTreeHook(t *testing.T) {
	mailbox := types.StringToAddress("0x1111111111111111111111111111111111111111")
	hook := types.StringToAddress("0x2222222222222222222222222222222222222222")
	root := types.StringToHash("0x3333333333333333333333333333333333333333333333333333333333333333")
	state := &fakeInterchainState{
		origin:            1643,
		validators:        map[types.Address]*stakingcontract.ValidatorInfo{},
		checkpointRoot:    root,
		checkpointIndex:   2,
		checkpointMailbox: mailbox,
		checkpointHook:    hook,
	}
	w := &Worker{
		state: state,
		originContracts: &evm.OriginContracts{Mailbox: mailbox, MerkleTreeHook: hook},
	}

	gotRoot, index, err := w.originCheckpoint()
	require.NoError(t, err)
	require.Equal(t, root, gotRoot)
	require.Equal(t, uint32(2), index)
}

func TestOriginCheckpointRejectsHookForDifferentMailbox(t *testing.T) {
	mailbox := types.StringToAddress("0x1111111111111111111111111111111111111111")
	other := types.StringToAddress("0x9999999999999999999999999999999999999999")
	hook := types.StringToAddress("0x2222222222222222222222222222222222222222")
	state := &fakeInterchainState{
		origin:            1643,
		validators:        map[types.Address]*stakingcontract.ValidatorInfo{},
		checkpointMailbox: other,
		checkpointHook:    hook,
	}
	w := &Worker{
		state: state,
		originContracts: &evm.OriginContracts{Mailbox: mailbox, MerkleTreeHook: hook},
	}

	_, _, err := w.originCheckpoint()
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected mailbox")
}
