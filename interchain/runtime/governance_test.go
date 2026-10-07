package runtime

import (
	"encoding/hex"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	stakingcontract "github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/interchain/evm"
	"github.com/xgr-network/xgr-node/types"
)

func TestAcceptGovernanceProposalDoesNotAutoSign(t *testing.T) {
	key, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	pub, err := crypto.BLSSecretKeyToPubkeyBytes(key)
	require.NoError(t, err)
	rawKey, err := key.MarshalBinary()
	require.NoError(t, err)

	validator := types.StringToAddress("0x1111111111111111111111111111111111111111")
	destination := &evm.Destination{Name: "xgr", Domain: 1643}

	old := getGovernanceValidatorSet
	defer func() { getGovernanceValidatorSet = old }()
	getGovernanceValidatorSet = func(*evm.Destination) (*evm.ValidatorSet, error) {
		return &evm.ValidatorSet{
			SetID:         7,
			Validators:    []types.Address{validator},
			BLSPublicKeys: [][]byte{pub},
		}, nil
	}

	w := &Worker{
		state: &fakeInterchainState{
			origin: 1643,
			validators: map[types.Address]*stakingcontract.ValidatorInfo{
				validator: {Exists: true, Active: true, BLSPubKey: append([]byte(nil), pub...)},
			},
		},
		dataDir:      t.TempDir(),
		localAddr:    validator,
		blsRaw:       []byte(hex.EncodeToString(rawKey)),
		blsPubKey:    pub,
		destinations: map[string]*evm.Destination{"xgr": destination, "base": &evm.Destination{
			Name:               "base",
			ChainID:            8453,
			Domain:             8453,
			ILNRegistryAddress: "0x5555555555555555555555555555555555555555",
		}},
		governance:   make(map[types.Hash]*governanceState),
	}

	proposal := testGovernanceProposal()
	raw, err := proposal.MarshalBinary()
	require.NoError(t, err)
	require.NoError(t, w.acceptGovernanceProposal(governanceProposalWire{Payload: raw}))

	id := crypto.Keccak256Hash(raw)
	require.NotNil(t, w.governance[id])
	require.Nil(t, w.governance[id].localVote)
	require.Empty(t, w.governance[id].votes)
}

func TestGovernanceExplicitApprovalCreatesVoteAndQuorum(t *testing.T) {
	key, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	pub, err := crypto.BLSSecretKeyToPubkeyBytes(key)
	require.NoError(t, err)
	rawKey, err := key.MarshalBinary()
	require.NoError(t, err)

	validator := types.StringToAddress("0x1111111111111111111111111111111111111111")
	destination := &evm.Destination{Name: "xgr", Domain: 1643}

	old := getGovernanceValidatorSet
	defer func() { getGovernanceValidatorSet = old }()
	getGovernanceValidatorSet = func(*evm.Destination) (*evm.ValidatorSet, error) {
		return &evm.ValidatorSet{
			SetID:         7,
			Validators:    []types.Address{validator},
			BLSPublicKeys: [][]byte{pub},
		}, nil
	}

	dataDir := t.TempDir()
	w := &Worker{
		state: &fakeInterchainState{
			origin: 1643,
			validators: map[types.Address]*stakingcontract.ValidatorInfo{
				validator: {Exists: true, Active: true, BLSPubKey: append([]byte(nil), pub...)},
			},
		},
		dataDir:      dataDir,
		localAddr:    validator,
		blsRaw:       []byte(hex.EncodeToString(rawKey)),
		blsPubKey:    pub,
		destinations: map[string]*evm.Destination{"xgr": destination, "base": &evm.Destination{
			Name:               "base",
			ChainID:            8453,
			Domain:             8453,
			ILNRegistryAddress: "0x5555555555555555555555555555555555555555",
		}},
		governance:   make(map[types.Hash]*governanceState),
	}

	proposal := testGovernanceProposal()
	raw, err := proposal.MarshalBinary()
	require.NoError(t, err)
	require.NoError(t, w.acceptGovernanceProposal(governanceProposalWire{Payload: raw}))
	id := crypto.Keccak256Hash(raw)

	vote, err := w.createGovernanceVote(id)
	require.NoError(t, err)
	require.NotNil(t, vote)
	require.NoError(t, w.acceptGovernanceVote(*vote))

	require.Len(t, w.governance[id].votes, 1)
	path := filepath.Join(dataDir, "interchain", "governance", "quorums", id.String()+".json")
	_, err = os.Stat(path)
	require.NoError(t, err)
}

func TestGovernanceRejectsStaleSetID(t *testing.T) {
	destination := &evm.Destination{Name: "xgr", Domain: 1643}
	old := getGovernanceValidatorSet
	defer func() { getGovernanceValidatorSet = old }()
	getGovernanceValidatorSet = func(*evm.Destination) (*evm.ValidatorSet, error) {
		return &evm.ValidatorSet{SetID: 8}, nil
	}

	w := &Worker{
		state: &fakeInterchainState{
			origin:     1643,
			validators: map[types.Address]*stakingcontract.ValidatorInfo{},
		},
		dataDir: t.TempDir(),
		destinations: map[string]*evm.Destination{"xgr": destination, "base": &evm.Destination{
			Name:               "base",
			ChainID:            8453,
			Domain:             8453,
			ILNRegistryAddress: "0x5555555555555555555555555555555555555555",
		}},
		governance:   make(map[types.Hash]*governanceState),
	}

	raw, err := testGovernanceProposal().MarshalBinary()
	require.NoError(t, err)
	err = w.acceptGovernanceProposal(governanceProposalWire{Payload: raw})
	require.Error(t, err)
	require.Contains(t, err.Error(), "stale")
}

func testGovernanceProposal() protocol.ILNGovernanceProposal {
	return protocol.ILNGovernanceProposal{
		Type:       protocol.ILNProposalFeeUpdate,
		Registry:   types.StringToAddress("0x5555555555555555555555555555555555555555"),
		SetID:      7,
		Nonce:      1,
		ValidUntil: uint64(time.Now().Add(5 * time.Minute).Unix()),
		Route: protocol.ILNRoute{
			Key: protocol.ILNRouteKey{
				SourceChainID:     8453,
				SourceDomain:      8453,
				DestinationDomain: 1643,
				RouteID:           types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			},
			ValidatorFeeWei: big.NewInt(1),
		},
	}
}
