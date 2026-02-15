package polybft

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xgr-network/xgr-node/consensus/polybft/bitmap"
	"github.com/xgr-network/xgr-node/consensus/polybft/signer"
	"github.com/xgr-network/xgr-node/consensus/polybft/validator"
	"github.com/xgr-network/xgr-node/consensus/polybft/wallet"
	"github.com/xgr-network/xgr-node/types"
)

func Test_setupHeaderHashFunc(t *testing.T) {
	extra := &Extra{
		Validators: &validator.ValidatorSetDelta{Removed: bitmap.Bitmap{1}},
		Parent:     createSignature(t, []*wallet.Account{generateTestAccount(t)}, types.ZeroHash, signer.DomainCheckpointManager),
		Checkpoint: &CheckpointData{},
		Committed:  &Signature{},
	}

	header := &types.Header{
		Number:    2,
		GasLimit:  10000003,
		Timestamp: 18,
	}

	header.ExtraData = extra.MarshalRLPTo(nil)
	notFullExtraHash := types.HeaderHash(header)

	extra.Committed = createSignature(t, []*wallet.Account{generateTestAccount(t)}, types.ZeroHash, signer.DomainCheckpointManager)
	header.ExtraData = extra.MarshalRLPTo(nil)
	fullExtraHash := types.HeaderHash(header)

	assert.Equal(t, notFullExtraHash, fullExtraHash)

	header.ExtraData = []byte{1, 2, 3, 4, 100, 200, 255}
	assert.Equal(t, types.ZeroHash, types.HeaderHash(header)) // to small extra data
}
