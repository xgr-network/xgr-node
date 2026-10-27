package ibft

import (
	"bytes"
	"fmt"
	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/consensus/ibft/pos"
	"github.com/xgr-network/xgr-node/types"
)

func (i *backendIBFT) securityAccountingActive(height uint64) bool {
	return i.config != nil && i.config.Params != nil && i.config.Params.Forks != nil &&
		i.config.Params.Forks.IsActive(chain.SecurityAccounting, height)
}

func (i *backendIBFT) securityEpochBoundary(height uint64) bool {
	return height > 1 && i.epochSize != 0 && height%i.epochSize == 0 &&
		i.forkManager.IsPosActive(height) && i.forkManager.IsPosActive(height-1)
}

func (i *backendIBFT) verifySecurityTransactions(block *types.Block) error {
	if !i.securityAccountingActive(block.Number()) {
		return nil
	}
	expected := i.securityEpochBoundary(block.Number())
	if expected {
		canonical := pos.EpochFinalizationSystemTx(block.Number())
		if len(block.Transactions) != 1 || block.Transactions[0] == nil ||
			!bytes.Equal(block.Transactions[0].MarshalRLP(), canonical.MarshalRLP()) ||
			block.Transactions[0].Hash != canonical.Hash {
			return fmt.Errorf("invalid epoch system transaction at block %d", block.Number())
		}
		return nil
	}
	for _, tx := range block.Transactions {
		if tx == nil || tx.Type == types.StateTx {
			return fmt.Errorf("unauthorized StateTx at block %d", block.Number())
		}
	}
	return nil
}

func (i *backendIBFT) securityBaseFee(parent *types.Header, height uint64) (uint64, error) {
	if !i.securityAccountingActive(height) {
		return i.blockchain.CalculateBaseFee(parent), nil
	}
	return i.blockchain.CalculateBaseFeeChecked(parent)
}

func (i *backendIBFT) verifySecurityBaseFee(parent, header *types.Header) error {
	if !i.securityAccountingActive(header.Number) {
		return nil
	}
	expected, err := i.securityBaseFee(parent, header.Number)
	if err != nil {
		return err
	}
	if header.BaseFee != expected {
		return fmt.Errorf("invalid base fee: have %d want %d", header.BaseFee, expected)
	}
	return nil
}
