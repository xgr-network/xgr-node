package server

import (
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/consensus"
	consensusDev "github.com/xgr-network/xgr-node/consensus/dev"
	consensusDummy "github.com/xgr-network/xgr-node/consensus/dummy"
	consensusIBFT "github.com/xgr-network/xgr-node/consensus/ibft"
	consensusPolyBFT "github.com/xgr-network/xgr-node/consensus/polybft"
	"github.com/xgr-network/xgr-node/forkmanager"
	"github.com/xgr-network/xgr-node/secrets"
	"github.com/xgr-network/xgr-node/secrets/awsssm"
	"github.com/xgr-network/xgr-node/secrets/gcpssm"
	"github.com/xgr-network/xgr-node/secrets/hashicorpvault"
	"github.com/xgr-network/xgr-node/secrets/local"
	"github.com/xgr-network/xgr-node/state"
)

type GenesisFactoryHook func(config *chain.Chain, engineName string) func(*state.Transition) error

type ConsensusType string

type ForkManagerFactory func(forks *chain.Forks) error

type ForkManagerInitialParamsFactory func(config *chain.Chain) (*forkmanager.ForkParams, error)

const (
	DevConsensus     ConsensusType = "dev"
	IBFTConsensus    ConsensusType = "ibft"
	PolyBFTConsensus ConsensusType = consensusPolyBFT.ConsensusName
	DummyConsensus   ConsensusType = "dummy"
)

var consensusBackends = map[ConsensusType]consensus.Factory{
	DevConsensus:     consensusDev.Factory,
	IBFTConsensus:    consensusIBFT.Factory,
	PolyBFTConsensus: consensusPolyBFT.Factory,
	DummyConsensus:   consensusDummy.Factory,
}

// secretsManagerBackends defines the SecretManager factories for different
// secret management solutions
var secretsManagerBackends = map[secrets.SecretsManagerType]secrets.SecretsManagerFactory{
	secrets.Local:          local.SecretsManagerFactory,
	secrets.HashicorpVault: hashicorpvault.SecretsManagerFactory,
	secrets.AWSSSM:         awsssm.SecretsManagerFactory,
	secrets.GCPSSM:         gcpssm.SecretsManagerFactory,
}

var genesisCreationFactory = map[ConsensusType]GenesisFactoryHook{
	PolyBFTConsensus: consensusPolyBFT.GenesisPostHookFactory,
}

var forkManagerFactory = map[ConsensusType]ForkManagerFactory{
	PolyBFTConsensus: consensusPolyBFT.ForkManagerFactory,
}

var forkManagerInitialParamsFactory = map[ConsensusType]ForkManagerInitialParamsFactory{
	PolyBFTConsensus: consensusPolyBFT.ForkManagerInitialParamsFactory,
}

func ConsensusSupported(value string) bool {
	_, ok := consensusBackends[ConsensusType(value)]

	return ok
}
