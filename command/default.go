package command

import (
	"fmt"

	"github.com/umbracle/ethgo"

	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/server"
)

const (
	DefaultGenesisFileName           = "genesis.json"
	DefaultChainName                 = "xgrchain"
	DefaultChainID                   = 100
	DefaultConsensus                 = server.PolyBFTConsensus
	DefaultGenesisGasUsed            = 458752  // 0x70000
	DefaultGenesisGasLimit           = 5242880 // 0x500000
	DefaultGenesisBaseFeeEM          = chain.GenesisBaseFeeEM
	DefaultGenesisBaseFeeChangeDenom = chain.BaseFeeChangeDenom
)

var (
	DefaultStake                = ethgo.Ether(1e6)
	DefaultPremineBalance       = ethgo.Ether(1e6)
	DefaultGenesisBaseFee       = chain.GenesisBaseFee
	DefaultGenesisBaseFeeConfig = fmt.Sprintf(
		"%d:%d:%d",
		DefaultGenesisBaseFee,
		DefaultGenesisBaseFeeEM,
		DefaultGenesisBaseFeeChangeDenom,
	)
)

const (
	JSONOutputFlag  = "json"
	GRPCAddressFlag = "grpc-address"
	JSONRPCFlag     = "jsonrpc"
)

// GRPCAddressFlagLEGACY Legacy flag that needs to be present to preserve backwards
// compatibility with running clients
const (
	GRPCAddressFlagLEGACY = "grpc"
)
