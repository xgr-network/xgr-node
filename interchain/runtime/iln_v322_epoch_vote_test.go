package runtime

import (
 "math/big"
 "fmt"
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

// This test verifies aggregate BLS quorum over an old immutable payload while
// the receiver's confirmed source is one epoch ahead and unchanged in content.
// It deliberately does not claim historical source-state proof or wallet settlement.
func TestV322ThreeValidatorsAggregateSamePayloadAcrossDivergentConfirmedEpochs(t *testing.T) {
	key, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	pub, err := crypto.BLSSecretKeyToPubkeyBytes(key)
	require.NoError(t, err)
	key2, err := crypto.GenerateBLSKey()
	require.NoError(t,err)
	pub2, err := crypto.BLSSecretKeyToPubkeyBytes(key2)
	require.NoError(t,err)
	key3, err := crypto.GenerateBLSKey()
	require.NoError(t,err)
	pub3, err := crypto.BLSSecretKeyToPubkeyBytes(key3)
	require.NoError(t,err)

	validator := types.StringToAddress("0x1111111111111111111111111111111111111111")
	validator2 := types.StringToAddress("0x2222222222222222222222222222222222222222")
	validator3 := types.StringToAddress("0x3333333333333333333333333333333333333333")
	registry := types.StringToAddress("0x5555555555555555555555555555555555555555")
	gateway := types.StringToAddress("0x2222222222222222222222222222222222222222")
	sourceRouter := types.StringToAddress("0x7777777777777777777777777777777777777777")
	mailbox := types.StringToAddress("0x3333333333333333333333333333333333333333")
	hook := types.StringToAddress("0x4444444444444444444444444444444444444444")
	destinationRouter := types.StringToAddress("0x6666666666666666666666666666666666666666")
	messageID := types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
    root := ilnTestCanonicalRoot(messageID)
	canonical := protocol.ILNRoute{
		Key: protocol.ILNRouteKey{SourceChainID: 8453, SourceDomain: 8453, DestinationDomain: 1643, RouteID: testCheckpointRouteID},
		Gateway: gateway, SourceRouter: sourceRouter, Mailbox: mailbox, MerkleTreeHook: hook,
		DestinationRouter: destinationRouter, ValidatorFeeWei: big.NewInt(10), Enabled: true,
	}

	oldHead := getConfirmedILNHead
    getConfirmedILNHead=func(*evm.Destination)(uint64,error){return 132,nil}
    defer func(){getConfirmedILNHead=oldHead}()
    oldRoute := getILNRouteAtBlock
	oldCheckpoint := getConfirmedILNCheckpoint
	oldOperation := getILNOperationAtBlock
	oldSet := getILNValidatorSet
	oldDelivered := getILNMessageDelivered
	defer func() {
		getILNRouteAtBlock = oldRoute
		getConfirmedILNCheckpoint = oldCheckpoint
		getILNOperationAtBlock = oldOperation
		getILNValidatorSet = oldSet
		getILNMessageDelivered = oldDelivered
	}()
	getILNRouteAtBlock = func(*evm.Destination, uint32, types.Hash, uint64) (*evm.ILNRouteSnapshot, error) {
		return &evm.ILNRouteSnapshot{Registry: registry, Route: canonical, BlockNumber: 132}, nil
	}
	getILNOperationAtBlock = func(
		*evm.Destination, types.Address, types.Hash, uint32, types.Hash, uint64,
	) (*evm.ILNOperation, error) {
		return &evm.ILNOperation{
			RouteID: testCheckpointRouteID,
			MessageID: messageID, DestinationDomain: 1643,
			ValidatorFeeWei: big.NewInt(10), BlockNumber: 100,
		}, nil
	}
	getConfirmedILNCheckpoint = func(*evm.Destination, *evm.ILNRouteSnapshot) (*evm.ConfirmedCheckpoint, error) {
		return &evm.ConfirmedCheckpoint{Root: root, Index: 0, BlockNumber: 132}, nil
	}
	getILNMessageDelivered = func(*evm.Destination, types.Address, types.Hash) (bool,error) { return false,nil }
	getILNValidatorSet = func(*evm.Destination) (*evm.ValidatorSet, error) {
		return &evm.ValidatorSet{SetID: 7, Validators: []types.Address{validator,validator2,validator3}, BLSPublicKeys: [][]byte{pub,pub2,pub3}}, nil
	}

	dataDir := t.TempDir()
	route := &evm.CheckpointRoute{Name: "base_to_xgr", SourceNetwork: "base", Destination: "xgr", RouteID: testCheckpointRouteID}
	w := &Worker{
		state: &fakeInterchainState{
			origin: 1643,
			validators: map[types.Address]*stakingcontract.ValidatorInfo{
				validator: {Exists: true, Active: true, BLSPubKey: append([]byte(nil), pub...)},
				validator2: {Exists: true, Active: true, BLSPubKey: append([]byte(nil), pub2...)},
				validator3: {Exists: true, Active: true, BLSPubKey: append([]byte(nil), pub3...)},
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
    installILNCheckpointTestReplica(t,w,hook,messageID,132)
    previousFeeVerifier := verifyILNFeeAllocation
    t.Cleanup(func(){ verifyILNFeeAllocation=previousFeeVerifier })
    verifyILNFeeAllocation=func(source *evm.Destination,_ *evm.ILNRouteSnapshot,id types.Hash,block uint64)error{
        if source.ChainID!=8453 || id!=messageID || block!=132 {
            return fmt.Errorf("unexpected source fee eligibility identity")
        }
        return nil
    }

	payload := protocol.ILNCheckpointPayload{
		SourceChainID: 8453, SourceDomain: 8453, DestinationDomain: 1643, RouteID: testCheckpointRouteID,
		SetID: 7, SourceBlockNumber: 100, Registry: registry,
		Gateway: gateway, SourceRouter: sourceRouter, Mailbox: mailbox,
		MerkleTreeHook: hook, DestinationRouter: destinationRouter,
		ValidatorFeeWei: big.NewInt(10), AuthorizedMessageID: messageID,
		Root: root, Index: 0,
	}
	raw, err := payload.MarshalBinary()
	require.NoError(t, err)
	sig, err := crypto.SignByBLS(key, raw)
	require.NoError(t, err)
	require.NoError(t, w.acceptILNCheckpointVote(ilnCheckpointVote{
		Route: route.Name, Payload: raw, Signer: validator, Signature: sig,
	}))
	path := filepath.Join(w.attestationDir(), route.Name, messageID.String()+".json")
	_, err = os.Stat(path)
	require.True(t,os.IsNotExist(err),"one of three independent signatures must not meet the 2-of-3 quorum")
	rawProgress,err:=os.ReadFile(w.ilnQuorumProgressPath(route.Name,messageID.String()))
	require.NoError(t,err)
	require.Contains(t,string(rawProgress),"\"status\":\"PARTIAL_QUORUM\"")
	require.Contains(t,string(rawProgress),"\"votes\":1")
	// A second validator observes the SAME content 32 blocks later.
	// The signer payload remains immutable; no synthetic BLS threshold.
	otherSig, err := crypto.SignByBLS(key2, raw)
	require.NoError(t,err)
	require.NoError(t,w.acceptILNCheckpointVote(ilnCheckpointVote{
		Route:route.Name,Payload:raw,Signer:validator2,Signature:otherSig,
	}))
	attestationRaw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(attestationRaw), protocol.ILNCheckpointDomainV1)
	require.Contains(t, string(attestationRaw), messageID.String())
	require.Contains(t, string(attestationRaw), sourceRouter.String())
	require.Contains(t,string(attestationRaw),"\"setId\":7")
	rawProgress,err=os.ReadFile(w.ilnQuorumProgressPath(route.Name,messageID.String()))
	require.NoError(t,err)
	require.Contains(t,string(rawProgress),"\"status\":\"AVAILABLE_UNVERIFIED\"")
	require.Contains(t,string(rawProgress),"\"votes\":2")
}


