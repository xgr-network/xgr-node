package compatibility

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"

	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/consensus"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/state"
	itrie "github.com/xgr-network/xgr-node/state/immutable-trie"
	"github.com/xgr-network/xgr-node/types"
)

const (
	compatChainID  = int64(1643)
	compatGasLimit = uint64(30_000_000)
	compatBaseTime = uint64(1_700_000_000)
)

var (
	compatSender      = types.StringToAddress("0x1000000000000000000000000000000000000001")
	compatA           = types.StringToAddress("0x2000000000000000000000000000000000000002")
	compatB           = types.StringToAddress("0x3000000000000000000000000000000000000003")
	sha256Addr        = types.StringToAddress("0x0000000000000000000000000000000000000002")
	interchainBLSAddr = types.StringToAddress("0x0000000000000000000000000000000000002040")
)

type compatStep struct {
	Label        string `json:"label"`
	BlockNumber  uint64 `json:"blockNumber"`
	StateRoot    string `json:"stateRoot"`
	TxRoot       string `json:"txRoot"`
	ReceiptsRoot string `json:"receiptsRoot"`
	BlockHash    string `json:"blockHash"`
	GasUsed      uint64 `json:"gasUsed"`
}

type compatVector struct {
	GenesisRoot string       `json:"genesisRoot"`
	Steps       []compatStep `json:"steps"`
}

func TestConsensusCompatibilityVector(t *testing.T) {
	t.Parallel()

	st := itrie.NewState(itrie.NewMemoryStorage())
	forks := chain.AllForksEnabled.Copy()
	ex := state.NewExecutor(
		&chain.Params{
			Forks:   forks,
			ChainID: compatChainID,
			BurnContract: map[uint64]types.Address{
				0: types.ZeroAddress,
			},
		},
		st,
		hclog.NewNullLogger(),
	)
	ex.GetHash = func(*types.Header) state.GetHashByNumber {
		return func(uint64) types.Hash { return types.ZeroHash }
	}

	genesisBalance := new(big.Int).Mul(big.NewInt(1_000), big.NewInt(1_000_000_000_000_000_000))
	root, err := ex.WriteGenesis(
		map[types.Address]*chain.GenesisAccount{
			compatSender: &chain.GenesisAccount{Balance: genesisBalance},
		},
		types.ZeroHash,
	)
	require.NoError(t, err)

	storageInitCode, err := hex.DecodeString("6007600c60003960076000f360003560005500")
	require.NoError(t, err)

	gasPrice := new(big.Int).Mul(big.NewInt(2), new(big.Int).SetUint64(chain.MinBaseFee))
	contractAddr := crypto.CreateAddress(compatSender, 2)
	storageValue := make([]byte, 32)
	storageValue[31] = 0x2a

	transactions := []struct {
		label string
		tx    *types.Transaction
	}{
		{
			label: "native-transfer-a",
			tx: &types.Transaction{
				From:     compatSender,
				To:       &compatA,
				Nonce:    0,
				Gas:      21_000,
				GasPrice: new(big.Int).Set(gasPrice),
				Value:    big.NewInt(1_000_000_000_000_000_000),
			},
		},
		{
			label: "sha256-precompile",
			tx: &types.Transaction{
				From:     compatSender,
				To:       &sha256Addr,
				Nonce:    1,
				Gas:      100_000,
				GasPrice: new(big.Int).Set(gasPrice),
				Value:    big.NewInt(0),
				Input:    []byte("xgr-v3.1-consensus-compatibility"),
			},
		},
		{
			label: "contract-deploy",
			tx: &types.Transaction{
				From:     compatSender,
				Nonce:    2,
				Gas:      500_000,
				GasPrice: new(big.Int).Set(gasPrice),
				Value:    big.NewInt(0),
				Input:    storageInitCode,
			},
		},
		{
			label: "contract-sstore",
			tx: &types.Transaction{
				From:     compatSender,
				To:       &contractAddr,
				Nonce:    3,
				Gas:      150_000,
				GasPrice: new(big.Int).Set(gasPrice),
				Value:    big.NewInt(0),
				Input:    storageValue,
			},
		},
		{
			label: "native-transfer-b",
			tx: &types.Transaction{
				From:     compatSender,
				To:       &compatB,
				Nonce:    4,
				Gas:      21_000,
				GasPrice: new(big.Int).Set(gasPrice),
				Value:    big.NewInt(2_000_000_000_000_000_000),
			},
		},
	}

	vector := compatVector{GenesisRoot: root.String()}
	parentHash := types.ZeroHash

	for i, tc := range transactions {
		require.NotNil(t, tc.tx)
		if tc.tx.To != nil {
			require.NotEqual(t, interchainBLSAddr, *tc.tx.To, "compatibility vector must never invoke 0x2040")
		}

		blockNumber := uint64(i + 1)
		header := &types.Header{
			ParentHash: parentHash,
			Number:     blockNumber,
			Miner:      types.ZeroAddress.Bytes(),
			GasLimit:   compatGasLimit,
			Timestamp:  compatBaseTime + blockNumber,
			Difficulty: blockNumber,
			BaseFee:    chain.MinBaseFee,
		}

		tc.tx.ComputeHash(blockNumber)

		transition, err := ex.BeginTxn(root, header, types.ZeroAddress)
		require.NoError(t, err)
		require.NoError(t, transition.Write(tc.tx))

		_, nextRoot, err := transition.Commit()
		require.NoError(t, err)
		header.StateRoot = nextRoot
		header.GasUsed = transition.TotalGas()

		block := consensus.BuildBlock(consensus.BuildBlockParams{
			Header:   header,
			Txns:     []*types.Transaction{tc.tx},
			Receipts: transition.Receipts(),
		})

		vector.Steps = append(vector.Steps, compatStep{
			Label:        tc.label,
			BlockNumber:  blockNumber,
			StateRoot:    block.Header.StateRoot.String(),
			TxRoot:       block.Header.TxRoot.String(),
			ReceiptsRoot: block.Header.ReceiptsRoot.String(),
			BlockHash:    block.Header.Hash.String(),
			GasUsed:      block.Header.GasUsed,
		})

		root = nextRoot
		parentHash = block.Header.Hash
	}

	raw, err := json.Marshal(vector)
	require.NoError(t, err)
	fmt.Printf("XGR_COMPAT_VECTOR=%s\n", raw)
}
