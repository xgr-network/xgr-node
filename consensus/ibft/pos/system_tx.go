package pos

import (
	"math/big"

	"github.com/xgr-network/xgr-node/contracts"
	"github.com/xgr-network/xgr-node/types"
)

// EpochFinalizationSystemTx returns the deterministic native
// epoch-finalization system transaction for boundary blocks. It is
// consensus-generated, not mempool/user-provided, and represents native epoch
// finalization for receipt/log/indexing purposes.
func EpochFinalizationSystemTx(blockNumber uint64) *types.Transaction {
	tx := &types.Transaction{
		Nonce:    blockNumber,
		GasPrice: big.NewInt(0),
		Gas:      types.StateTransactionGasLimit,
		To:       &PosSysAddr,
		Value:    big.NewInt(0),
		Input:    []byte(types.EpochFinalizationSystemTxInput),
		From:     contracts.SystemCaller,
		Type:     types.StateTx,
	}

	return tx.ComputeHash(blockNumber)
}

// IsEpochFinalizationSystemTx reports whether tx is the deterministic native
// PoS epoch-finalization system transaction for blockNumber.
func IsEpochFinalizationSystemTx(tx *types.Transaction, blockNumber uint64) bool {
	return types.IsEpochFinalizationSystemTxForBlock(tx, blockNumber, contracts.SystemCaller)
}
