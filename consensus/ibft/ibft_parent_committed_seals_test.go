package ibft

import (
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/blockchain"
	"github.com/xgr-network/xgr-node/consensus/ibft/fork"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

type parentSealsTestSigner struct {
	extra *signer.IstanbulExtra
	seals signer.Seals
}

func (m *parentSealsTestSigner) Type() validators.ValidatorType { return validators.ECDSAValidatorType }
func (m *parentSealsTestSigner) Address() types.Address         { return types.ZeroAddress }
func (m *parentSealsTestSigner) InitIBFTExtra(*types.Header, validators.Validators, signer.Seals) {
}
func (m *parentSealsTestSigner) GetIBFTExtra(*types.Header) (*signer.IstanbulExtra, error) {
	return m.extra, nil
}
func (m *parentSealsTestSigner) GetValidators(*types.Header) (validators.Validators, error) {
	return m.extra.Validators, nil
}
func (m *parentSealsTestSigner) WriteProposerSeal(h *types.Header) (*types.Header, error) {
	return h, nil
}
func (m *parentSealsTestSigner) EcrecoverFromHeader(*types.Header) (types.Address, error) {
	return types.ZeroAddress, nil
}
func (m *parentSealsTestSigner) CreateCommittedSeal([]byte) ([]byte, error) { return nil, nil }
func (m *parentSealsTestSigner) VerifyCommittedSeal(validators.Validators, types.Address, []byte, []byte) error {
	return nil
}
func (m *parentSealsTestSigner) WriteCommittedSeals(*types.Header, uint64, map[types.Address][]byte) (*types.Header, error) {
	return nil, nil
}
func (m *parentSealsTestSigner) VerifyCommittedSeals(types.Hash, signer.Seals, validators.Validators, int) error {
	return nil
}
func (m *parentSealsTestSigner) VerifyParentCommittedSeals(types.Hash, *types.Header, validators.Validators, int, bool) error {
	return nil
}
func (m *parentSealsTestSigner) GetParentCommittedSeals(*types.Header) (signer.Seals, error) {
	return m.seals, nil
}
func (m *parentSealsTestSigner) SignIBFTMessage([]byte) ([]byte, error) { return nil, nil }
func (m *parentSealsTestSigner) EcrecoverFromIBFTMessage([]byte, []byte) (types.Address, error) {
	return types.ZeroAddress, nil
}
func (m *parentSealsTestSigner) CalculateHeaderHash(*types.Header) (types.Hash, error) {
	return types.ZeroHash, nil
}
func (m *parentSealsTestSigner) FilterHeaderForHash(h *types.Header) (*types.Header, error) {
	return h, nil
}

type parentSealsForkManager struct {
	signer signer.Signer
	vals   validators.Validators
	pos    bool
}

func (m *parentSealsForkManager) Initialize() error                       { return nil }
func (m *parentSealsForkManager) Close() error                            { return nil }
func (m *parentSealsForkManager) GetSigner(uint64) (signer.Signer, error) { return m.signer, nil }
func (m *parentSealsForkManager) GetValidatorStore(uint64) (fork.ValidatorStore, error) {
	return nil, nil
}
func (m *parentSealsForkManager) GetValidators(uint64) (validators.Validators, error) {
	return m.vals, nil
}
func (m *parentSealsForkManager) GetHooks(uint64) fork.HooksInterface { return nil }
func (m *parentSealsForkManager) IsPosActive(uint64) bool             { return m.pos }

func TestVerifyParentCommittedSeals_PoSRequiresNonEmptySeals(t *testing.T) {
	val := validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001"))
	valSet := validators.NewECDSAValidatorSet(val)
	round := uint64(1)
	ts := &parentSealsTestSigner{extra: &signer.IstanbulExtra{Validators: valSet, RoundNumber: &round}, seals: nil}

	genesis := &types.Header{Number: 0, Difficulty: 1}
	genesis.ComputeHash()
	parent := &types.Header{Number: 1, ParentHash: genesis.Hash, Difficulty: 1}
	parent.ComputeHash()
	child := &types.Header{Number: 2, ParentHash: parent.Hash, Difficulty: 1}
	bc := blockchain.NewTestBlockchain(t, []*types.Header{genesis, parent})

	backend := &backendIBFT{logger: hclog.NewNullLogger(), blockchain: bc, forkManager: &parentSealsForkManager{signer: ts, vals: valSet, pos: true}}

	err := backend.verifyParentCommittedSeals(parent, child, true)
	require.EqualError(t, err, "missing parent committed seals in PoS weighted mode at height 1")
}

func TestVerifyParentCommittedSeals_NonPoSKeepsCompatibility(t *testing.T) {
	val := validators.NewECDSAValidator(types.StringToAddress("0x1000000000000000000000000000000000000001"))
	valSet := validators.NewECDSAValidatorSet(val)
	round := uint64(1)
	ts := &parentSealsTestSigner{extra: &signer.IstanbulExtra{Validators: valSet, RoundNumber: &round}, seals: nil}

	genesis := &types.Header{Number: 0, Difficulty: 1}
	genesis.ComputeHash()
	parent := &types.Header{Number: 1, ParentHash: genesis.Hash, Difficulty: 1}
	parent.ComputeHash()
	child := &types.Header{Number: 2, ParentHash: parent.Hash, Difficulty: 1}
	bc := blockchain.NewTestBlockchain(t, []*types.Header{genesis, parent})

	backend := &backendIBFT{logger: hclog.NewNullLogger(), blockchain: bc, forkManager: &parentSealsForkManager{signer: ts, vals: valSet, pos: false}}

	require.NoError(t, backend.verifyParentCommittedSeals(parent, child, true))
}
