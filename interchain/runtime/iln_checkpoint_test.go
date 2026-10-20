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

var testCheckpointRouteID = types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

// ilnTestCanonicalRoot independently reconstructs the 32-depth Hyperlane
// root of exactly one confirmed message. No test can fake inclusion by
// supplying a disconnected or caller-controlled root.
func ilnTestCanonicalRoot(messageID types.Hash)types.Hash{
    h:=messageID
    zero:=types.ZeroHash
    for i:=0;i<32;i++{
        pair:=append(append([]byte(nil),h.Bytes()...),zero.Bytes()...)
        h=crypto.Keccak256Hash(pair)
        zero=crypto.Keccak256Hash(append(append([]byte(nil),zero.Bytes()...),zero.Bytes()...))
    }
    return h
}
func installILNCheckpointTestReplica(t *testing.T,w *Worker,hook types.Address,message types.Hash,block uint64){
    t.Helper()
    root:=ilnTestCanonicalRoot(message)
    c:=evm.ILNReplicaCheckpoint{
        ChainID:8453,Hook:hook,Root:root,Count:1,BlockNumber:block,
    }
    replica:=evm.ILNReplica{ChainID:8453,Hook:hook,Root:root,
        CheckpointBlock:block,Leaves:[]types.Hash{message}}
    require.NoError(t,evm.StoreVerifiedILNReplica(w.ilnReplicaPath(c),replica,c))
}
func mockConfirmedILNFeeAllocation(t *testing.T,expected types.Hash){
    t.Helper()
    previous:=verifyILNFeeAllocation
    t.Cleanup(func(){verifyILNFeeAllocation=previous})
    verifyILNFeeAllocation=func(source *evm.Destination,snapshot *evm.ILNRouteSnapshot,
        messageID types.Hash,block uint64)error{
        if block!=100 || source.ChainID!=8453 {return fmt.Errorf("fee checkpoint mismatch")}
        if messageID!=expected{return evm.ErrILNFeeNotAllocated}
        return nil
    }
}

func TestHandleWireRejectsLegacyCheckpointVoteV313(t *testing.T) {
	w := &Worker{}
	err := w.handleWire([]byte(`{"type":"checkpoint_vote","checkpointVote":{}}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "disabled in v3.1.3")
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
	authorizedMessage := types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	wrongMessage := types.StringToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
    root := ilnTestCanonicalRoot(authorizedMessage)

	source := &evm.Destination{Name: "base", ChainID: 8453, Domain: 8453, ILNRegistryAddress: registry.String()}
	destination := &evm.Destination{Name: "xgr", ChainID: 1643, Domain: 1643}
	route := &evm.CheckpointRoute{Name: "base_to_xgr", SourceNetwork: "base", Destination: "xgr", RouteID: testCheckpointRouteID}
	canonical := protocol.ILNRoute{
		Key: protocol.ILNRouteKey{SourceChainID: 8453, SourceDomain: 8453, DestinationDomain: 1643, RouteID: testCheckpointRouteID},
		Gateway: gateway, SourceRouter: sourceRouter, Mailbox: mailbox, MerkleTreeHook: hook,
		DestinationRouter: destinationRouter, ValidatorFeeWei: big.NewInt(10), Enabled: true,
	}

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
		return &evm.ILNRouteSnapshot{Registry: registry, Route: canonical, BlockNumber: 100}, nil
	}
	getILNOperationAtBlock = func(
		*evm.Destination, types.Address, types.Hash, uint32, types.Hash, uint64,
	) (*evm.ILNOperation, error) {
		return nil, os.ErrNotExist
	}
	getConfirmedILNCheckpoint = func(*evm.Destination, *evm.ILNRouteSnapshot) (*evm.ConfirmedCheckpoint, error) {
		return &evm.ConfirmedCheckpoint{Root: root, Index: 0, BlockNumber: 100}, nil
	}
	getILNMessageDelivered = func(*evm.Destination, types.Address, types.Hash) (bool,error) { return false,nil }
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
    installILNCheckpointTestReplica(t,w,hook,authorizedMessage,100)
    mockConfirmedILNFeeAllocation(t,authorizedMessage)

	payload := protocol.ILNCheckpointPayload{
		SourceChainID: 8453, SourceDomain: 8453, DestinationDomain: 1643, RouteID: testCheckpointRouteID,
		SetID: 7, SourceBlockNumber: 100, Registry: registry,
		Gateway: gateway, SourceRouter: sourceRouter, Mailbox: mailbox,
		MerkleTreeHook: hook, DestinationRouter: destinationRouter,
		ValidatorFeeWei: big.NewInt(10), AuthorizedMessageID: wrongMessage,
		Root: root, Index: 0,
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
	messageID := types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
    root := ilnTestCanonicalRoot(messageID)
	canonical := protocol.ILNRoute{
		Key: protocol.ILNRouteKey{SourceChainID: 8453, SourceDomain: 8453, DestinationDomain: 1643, RouteID: testCheckpointRouteID},
		Gateway: gateway, SourceRouter: sourceRouter, Mailbox: mailbox, MerkleTreeHook: hook,
		DestinationRouter: destinationRouter, ValidatorFeeWei: big.NewInt(10), Enabled: true,
	}

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
		return &evm.ILNRouteSnapshot{Registry: registry, Route: canonical, BlockNumber: 100}, nil
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
		return &evm.ConfirmedCheckpoint{Root: root, Index: 0, BlockNumber: 100}, nil
	}
	getILNMessageDelivered = func(*evm.Destination, types.Address, types.Hash) (bool,error) { return false,nil }
	getILNValidatorSet = func(*evm.Destination) (*evm.ValidatorSet, error) {
		return &evm.ValidatorSet{SetID: 7, Validators: []types.Address{validator}, BLSPublicKeys: [][]byte{pub}}, nil
	}

	dataDir := t.TempDir()
	route := &evm.CheckpointRoute{Name: "base_to_xgr", SourceNetwork: "base", Destination: "xgr", RouteID: testCheckpointRouteID}
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
    installILNCheckpointTestReplica(t,w,hook,messageID,100)
    mockConfirmedILNFeeAllocation(t,messageID)

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
	attestationRaw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(attestationRaw), protocol.ILNCheckpointDomainV1)
	require.Contains(t, string(attestationRaw), messageID.String())
	require.Contains(t, string(attestationRaw), sourceRouter.String())
}


func TestILNCheckpointRequiresTwoIndependentVotesFromThree(t *testing.T) {
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
		return &evm.ILNRouteSnapshot{Registry: registry, Route: canonical, BlockNumber: 100}, nil
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
		return &evm.ConfirmedCheckpoint{Root: root, Index: 0, BlockNumber: 100}, nil
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
    installILNCheckpointTestReplica(t,w,hook,messageID,100)
    mockConfirmedILNFeeAllocation(t,messageID)

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



func TestAcceptILNCheckpointVoteRejectsAlreadyDelivered(t *testing.T) {
 key,err:=crypto.GenerateBLSKey()
 require.NoError(t,err)
 pub,err:=crypto.BLSSecretKeyToPubkeyBytes(key)
 require.NoError(t,err)
 validator:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 registry:=types.StringToAddress("0x5555555555555555555555555555555555555555")
 gateway:=types.StringToAddress("0x2222222222222222222222222222222222222222")
 sourceRouter:=types.StringToAddress("0x7777777777777777777777777777777777777777")
 mailbox:=types.StringToAddress("0x3333333333333333333333333333333333333333")
 hook:=types.StringToAddress("0x4444444444444444444444444444444444444444")
 destinationRouter:=types.StringToAddress("0x6666666666666666666666666666666666666666")
 messageID:=types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
 canonical:=protocol.ILNRoute{
 Key:protocol.ILNRouteKey{SourceChainID:8453,SourceDomain:8453,DestinationDomain:1643,RouteID:testCheckpointRouteID},
 Gateway:gateway,SourceRouter:sourceRouter,Mailbox:mailbox,MerkleTreeHook:hook,
 DestinationRouter:destinationRouter,ValidatorFeeWei:big.NewInt(10),Enabled:true,
 }
 oldRoute,oldOperation,oldDelivered,oldCheckpoint:=getILNRouteAtBlock,getILNOperationAtBlock,getILNMessageDelivered,getConfirmedILNCheckpoint
 defer func(){getILNRouteAtBlock=oldRoute;getILNOperationAtBlock=oldOperation;getILNMessageDelivered=oldDelivered;getConfirmedILNCheckpoint=oldCheckpoint}()
 getILNRouteAtBlock=func(*evm.Destination,uint32,types.Hash,uint64)(*evm.ILNRouteSnapshot,error){
 return &evm.ILNRouteSnapshot{Registry:registry,Route:canonical,BlockNumber:100},nil
 }
 getILNOperationAtBlock=func(*evm.Destination,types.Address,types.Hash,uint32,types.Hash,uint64)(*evm.ILNOperation,error){
 return &evm.ILNOperation{RouteID:testCheckpointRouteID,MessageID:messageID,DestinationDomain:1643,ValidatorFeeWei:big.NewInt(10),BlockNumber:100},nil
 }
 getILNMessageDelivered=func(*evm.Destination,types.Address,types.Hash)(bool,error){return true,nil}
 getConfirmedILNCheckpoint=func(*evm.Destination,*evm.ILNRouteSnapshot)(*evm.ConfirmedCheckpoint,error){
 return &evm.ConfirmedCheckpoint{Root:ilnTestCanonicalRoot(messageID),Index:0,BlockNumber:100},nil
 }
 payload:=protocol.ILNCheckpointPayload{
 SourceChainID:8453,SourceDomain:8453,DestinationDomain:1643,RouteID:testCheckpointRouteID,
 SetID:7,SourceBlockNumber:100,Registry:registry,Gateway:gateway,SourceRouter:sourceRouter,
 Mailbox:mailbox,MerkleTreeHook:hook,DestinationRouter:destinationRouter,
 ValidatorFeeWei:big.NewInt(10),AuthorizedMessageID:messageID,Root:ilnTestCanonicalRoot(messageID),Index:0,
 }
 raw,err:=payload.MarshalBinary()
 require.NoError(t,err)
 sig,err:=crypto.SignByBLS(key,raw)
 require.NoError(t,err)
 w:=&Worker{dataDir:t.TempDir(),destinations:map[string]*evm.Destination{
 "base":{Name:"base",ChainID:8453,Domain:8453},"xgr":{Name:"xgr",ChainID:1643,Domain:1643}},
 routes:map[string]*evm.CheckpointRoute{"base_to_xgr":{Name:"base_to_xgr",SourceNetwork:"base",Destination:"xgr",RouteID:testCheckpointRouteID}},
 ilnCheckpoints:make(map[types.Hash]*ilnCheckpointState),
 }
 
 installILNCheckpointTestReplica(t,w,hook,messageID,100)
 mockConfirmedILNFeeAllocation(t,messageID)
 err=w.acceptILNCheckpointVote(ilnCheckpointVote{Route:"base_to_xgr",Payload:raw,Signer:validator,Signature:sig})
 require.ErrorContains(t,err,"already delivered")
 require.Empty(t,w.ilnCheckpoints)
 require.NotEmpty(t,pub)
}



func TestAcceptILNCheckpointVoteAllowsFeeUpdatedLaterInSameBlock(t *testing.T) {
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
	messageID := types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
    root := ilnTestCanonicalRoot(messageID)

	source := &evm.Destination{Name: "base", ChainID: 8453, Domain: 8453, ILNRegistryAddress: registry.String()}
	destination := &evm.Destination{Name: "xgr", ChainID: 1643, Domain: 1643}
	route := &evm.CheckpointRoute{Name: "base_to_xgr", SourceNetwork: "base", Destination: "xgr", RouteID: testCheckpointRouteID}
	canonical := protocol.ILNRoute{
		Key: protocol.ILNRouteKey{SourceChainID: 8453, SourceDomain: 8453, DestinationDomain: 1643, RouteID: testCheckpointRouteID},
		Gateway: gateway, SourceRouter: sourceRouter, Mailbox: mailbox, MerkleTreeHook: hook,
		DestinationRouter: destinationRouter, ValidatorFeeWei: big.NewInt(10), Enabled: true,
	}

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
		return &evm.ILNRouteSnapshot{Registry: registry, Route: canonical, BlockNumber: 100}, nil
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
		return &evm.ConfirmedCheckpoint{Root: root, Index: 0, BlockNumber: 100}, nil
	}
	getILNMessageDelivered = func(*evm.Destination, types.Address, types.Hash) (bool,error) { return false,nil }
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
    installILNCheckpointTestReplica(t,w,hook,messageID,100)
    mockConfirmedILNFeeAllocation(t,messageID)

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

	err = w.acceptILNCheckpointVote(ilnCheckpointVote{
		// This peer has auto-discovered the route under its canonical name,
		// while the local validator still uses the old ENV route alias.
		Route: "iln_8453_1643_"+testCheckpointRouteID.String()[2:], Payload: raw, Signer: validator, Signature: sig,
	})
	// The historical receipt need not be fetched. The immutable FeeVault
    // proves allocation even when the governance fee policy later changes.
	require.NoError(t, err)
}

func TestILNCursorRoundTrip(t *testing.T) {
	w := &Worker{dataDir: t.TempDir()}
	require.NoError(t, w.writeILNCursor("base_to_xgr", 12345))
	got, exists, err := w.readILNCursor("base_to_xgr")
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, uint64(12345), got)
}
