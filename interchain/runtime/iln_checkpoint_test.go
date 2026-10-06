package runtime

import (
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	stakingcontract "github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/interchain/evm"
	"github.com/xgr-network/xgr-node/types"
)

func TestHandleWireRejectsLegacyCheckpointVoteV312(t *testing.T) {
	w := &Worker{}
	err := w.handleWire([]byte(`{"type":"checkpoint_vote","checkpointVote":{}}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "disabled in v3.1.2")
}

func TestAcceptILNCheckpointVoteRejectsUnauthorizedMessage(t *testing.T) {
	key, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	pub, err := crypto.BLSSecretKeyToPubkeyBytes(key)
	require.NoError(t, err)

	validator := types.StringToAddress("0x1111111111111111111111111111111111111111")
	registry := types.StringToAddress("0x5555555555555555555555555555555555555555")
	gateway := types.StringToAddress("0x2222222222222222222222222222222222222222")
	sourceRouter := types.StringToAddress("0x7777777777777777777777777777777777777777")
	mailbox := types.StringToAddress("0x3333333333333333333333333333333333333333")
	hook := types.StringToAddress("0x4444444444444444444444444444444444444444")
	destinationRouter := types.StringToAddress("0x6666666666666666666666666666666666666666")
	root := types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	authorizedMessage := types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	wrongMessage := types.StringToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")

	source := &evm.Destination{Name: "base", ChainID: 8453, Domain: 8453, ILNRegistryAddress: registry.String()}
	destination := &evm.Destination{Name: "xgr", ChainID: 1643, Domain: 1643}
	route := &evm.CheckpointRoute{Name: "base_to_xgr", SourceNetwork: "base", Destination: "xgr"}
	canonical := protocol.ILNRoute{
		Key: protocol.ILNRouteKey{SourceChainID: 8453, SourceDomain: 8453, DestinationDomain: 1643},
		Gateway: gateway, SourceRouter: sourceRouter, Mailbox: mailbox, MerkleTreeHook: hook,
		DestinationRouter: destinationRouter, ValidatorFeeWei: big.NewInt(10), Enabled: true,
	}

	oldRoute := getILNRouteAtBlock
	oldCheckpoint := getConfirmedILNCheckpoint
	oldOperation := getILNOperationAtBlock
	oldSet := getILNValidatorSet
	defer func() {
		getILNRouteAtBlock = oldRoute
		getConfirmedILNCheckpoint = oldCheckpoint
		getILNOperationAtBlock = oldOperation
		getILNValidatorSet = oldSet
	}()

	getILNRouteAtBlock = func(*evm.Destination, uint32, uint64) (*evm.ILNRouteSnapshot, error) {
		return &evm.ILNRouteSnapshot{Registry: registry, Route: canonical, BlockNumber: 100}, nil
	}
	getILNOperationAtBlock = func(
		*evm.Destination, types.Address, uint32, types.Hash, uint64,
	) (*evm.ILNOperation, error) {
		return nil, os.ErrNotExist
	}
	getConfirmedILNCheckpoint = func(*evm.Destination, *evm.ILNRouteSnapshot) (*evm.ConfirmedCheckpoint, error) {
		return &evm.ConfirmedCheckpoint{Root: root, Index: 3, BlockNumber: 100}, nil
	}
	getILNValidatorSet = func(*evm.Destination) (*evm.ValidatorSet, error) {
		return &evm.ValidatorSet{SetID: 7, Validators: []types.Address{validator}, BLSPublicKeys: [][]byte{pub}}, nil
	}

	w := &Worker{
		state: &fakeInterchainState{
			origin: 1643,
			validators: map[types.Address]*stakingcontract.ValidatorInfo{
				validator: {Exists: true, Active: true, BLSPubKey: append([]byte(nil), pub...)},
			},
		},
		dataDir: t.TempDir(),
		destinations: map[string]*evm.Destination{"base": source, "xgr": destination},
		routes: map[string]*evm.CheckpointRoute{route.Name: route},
		ilnCheckpoints: make(map[types.Hash]*ilnCheckpointState),
		ilnLocalVotes: make(map[types.Hash]*ilnLocalVoteState),
	}

	payload := protocol.ILNCheckpointPayload{
		SourceChainID: 8453, SourceDomain: 8453, DestinationDomain: 1643,
		SetID: 7, SourceBlockNumber: 100, Registry: registry,
		Gateway: gateway, SourceRouter: sourceRouter, Mailbox: mailbox,
		MerkleTreeHook: hook, DestinationRouter: destinationRouter,
		ValidatorFeeWei: big.NewInt(10), AuthorizedMessageID: wrongMessage,
		Root: root, Index: 3,
	}
	raw, err := payload.MarshalBinary()
	require.NoError(t, err)
	sig, err := crypto.SignByBLS(key, raw)
	require.NoError(t, err)

	err = w.acceptILNCheckpointVote(ilnCheckpointVote{
		Route: route.Name, Payload: raw, Signer: validator, Signature: sig,
	})
	require.Error(t, err)
	require.NotEqual(t, authorizedMessage, wrongMessage)
}

func TestAcceptILNCheckpointVoteCreatesMessageSpecificQuorum(t *testing.T) {
	key, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	pub, err := crypto.BLSSecretKeyToPubkeyBytes(key)
	require.NoError(t, err)

	validator := types.StringToAddress("0x1111111111111111111111111111111111111111")
	registry := types.StringToAddress("0x5555555555555555555555555555555555555555")
	gateway := types.StringToAddress("0x2222222222222222222222222222222222222222")
	sourceRouter := types.StringToAddress("0x7777777777777777777777777777777777777777")
	mailbox := types.StringToAddress("0x3333333333333333333333333333333333333333")
	hook := types.StringToAddress("0x4444444444444444444444444444444444444444")
	destinationRouter := types.StringToAddress("0x6666666666666666666666666666666666666666")
	root := types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	messageID := types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	canonical := protocol.ILNRoute{
		Key: protocol.ILNRouteKey{SourceChainID: 8453, SourceDomain: 8453, DestinationDomain: 1643},
		Gateway: gateway, SourceRouter: sourceRouter, Mailbox: mailbox, MerkleTreeHook: hook,
		DestinationRouter: destinationRouter, ValidatorFeeWei: big.NewInt(10), Enabled: true,
	}

	oldRoute := getILNRouteAtBlock
	oldCheckpoint := getConfirmedILNCheckpoint
	oldOperation := getILNOperationAtBlock
	oldSet := getILNValidatorSet
	defer func() {
		getILNRouteAtBlock = oldRoute
		getConfirmedILNCheckpoint = oldCheckpoint
		getILNOperationAtBlock = oldOperation
		getILNValidatorSet = oldSet
	}()
	getILNRouteAtBlock = func(*evm.Destination, uint32, uint64) (*evm.ILNRouteSnapshot, error) {
		return &evm.ILNRouteSnapshot{Registry: registry, Route: canonical, BlockNumber: 100}, nil
	}
	getILNOperationAtBlock = func(
		*evm.Destination, types.Address, uint32, types.Hash, uint64,
	) (*evm.ILNOperation, error) {
		return &evm.ILNOperation{
			MessageID: messageID, DestinationDomain: 1643,
			ValidatorFeeWei: big.NewInt(10), BlockNumber: 100,
		}, nil
	}
	getConfirmedILNCheckpoint = func(*evm.Destination, *evm.ILNRouteSnapshot) (*evm.ConfirmedCheckpoint, error) {
		return &evm.ConfirmedCheckpoint{Root: root, Index: 3, BlockNumber: 100}, nil
	}
	getILNValidatorSet = func(*evm.Destination) (*evm.ValidatorSet, error) {
		return &evm.ValidatorSet{SetID: 7, Validators: []types.Address{validator}, BLSPublicKeys: [][]byte{pub}}, nil
	}

	dataDir := t.TempDir()
	route := &evm.CheckpointRoute{Name: "base_to_xgr", SourceNetwork: "base", Destination: "xgr"}
	w := &Worker{
		state: &fakeInterchainState{
			origin: 1643,
			validators: map[types.Address]*stakingcontract.ValidatorInfo{
				validator: {Exists: true, Active: true, BLSPubKey: append([]byte(nil), pub...)},
			},
		},
		dataDir: dataDir,
		destinations: map[string]*evm.Destination{
			"base": {Name: "base", ChainID: 8453, Domain: 8453, ILNRegistryAddress: registry.String()},
			"xgr": {Name: "xgr", ChainID: 1643, Domain: 1643},
		},
		routes: map[string]*evm.CheckpointRoute{route.Name: route},
		ilnCheckpoints: make(map[types.Hash]*ilnCheckpointState),
		ilnLocalVotes: make(map[types.Hash]*ilnLocalVoteState),
	}

	payload := protocol.ILNCheckpointPayload{
		SourceChainID: 8453, SourceDomain: 8453, DestinationDomain: 1643,
		SetID: 7, SourceBlockNumber: 100, Registry: registry,
		Gateway: gateway, SourceRouter: sourceRouter, Mailbox: mailbox,
		MerkleTreeHook: hook, DestinationRouter: destinationRouter,
		ValidatorFeeWei: big.NewInt(10), AuthorizedMessageID: messageID,
		Root: root, Index: 3,
	}
	raw, err := payload.MarshalBinary()
	require.NoError(t, err)
	sig, err := crypto.SignByBLS(key, raw)
	require.NoError(t, err)
	require.NoError(t, w.acceptILNCheckpointVote(ilnCheckpointVote{
		Route: route.Name, Payload: raw, Signer: validator, Signature: sig,
	}))

	path := filepath.Join(w.attestationDir(), route.Name, messageID.String()+".json")
	attestationRaw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(attestationRaw), protocol.ILNCheckpointDomainV1)
	require.Contains(t, string(attestationRaw), messageID.String())
	require.Contains(t, string(attestationRaw), sourceRouter.String())
}


func TestILNCursorRoundTrip(t *testing.T) {
	w := &Worker{dataDir: t.TempDir()}
	require.NoError(t, w.writeILNCursor("base_to_xgr", 12345))
	got, exists, err := w.readILNCursor("base_to_xgr")
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, uint64(12345), got)
}
