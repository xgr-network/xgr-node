package pos

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/state"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func requireSnapshotCreated(t *testing.T, txn *state.Transition, epoch uint64, vals validators.Validators) {
	t.Helper()

	created, err := ensureEpochValidatorsSnapshot(txn, epoch, vals)
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, StoreMacroEpochNoSlashMode(txn, epoch, false))
}

func makeEpochValidatorSet(t *testing.T, count uint64) validators.Validators {
	t.Helper()

	set := validators.NewECDSAValidatorSet()
	for i := uint64(0); i < count; i++ {
		n := i + 1
		addr := types.Address{}
		addr[12] = byte(n >> 56)
		addr[13] = byte(n >> 48)
		addr[14] = byte(n >> 40)
		addr[15] = byte(n >> 32)
		addr[16] = byte(n >> 24)
		addr[17] = byte(n >> 16)
		addr[18] = byte(n >> 8)
		addr[19] = byte(n)
		require.NoError(t, set.Add(validators.NewECDSAValidator(addr)))
	}

	return set
}

func TestEnsureEpochValidatorsSnapshotRejectsOverMaxWithoutWrites(t *testing.T) {
	txn := newPosTestTransition(t)
	epoch := uint64(2)
	set := makeEpochValidatorSet(t, maxEpochValidatorsSnapshot+1)

	created, err := ensureEpochValidatorsSnapshot(txn, epoch, set)
	require.Error(t, err)
	require.False(t, created)
	require.Contains(t, err.Error(), "epoch validator snapshot exceeds max")
	require.Equal(t, uint64(0), u64FromHash(txn.Txn().GetState(PosSysAddr, keyEpochValidatorsLen(epoch))))
	for i := uint64(0); i < uint64(set.Len()); i++ {
		require.Equal(t, types.ZeroHash, txn.Txn().GetState(PosSysAddr, keyEpochValidator(epoch, i)))
	}
}

func TestEnsureEpochValidatorsSnapshotValidCountAndLoadMax(t *testing.T) {
	txn := newPosTestTransition(t)
	epoch := uint64(3)
	set := makeEpochValidatorSet(t, maxEpochValidatorsSnapshot)

	created, err := ensureEpochValidatorsSnapshot(txn, epoch, set)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, maxEpochValidatorsSnapshot, u64FromHash(txn.Txn().GetState(PosSysAddr, keyEpochValidatorsLen(epoch))))

	loaded := loadEpochValidatorsSnapshot(txn, epoch)
	require.Len(t, loaded, int(maxEpochValidatorsSnapshot))
	for i := uint64(0); i < maxEpochValidatorsSnapshot; i++ {
		require.Equal(t, set.At(i).Addr(), loaded[i])
	}
}

func TestLoadEpochValidatorsSnapshotReturnsNilForMalformedOverMaxLen(t *testing.T) {
	txn := newPosTestTransition(t)
	epoch := uint64(4)
	txn.Txn().SetState(PosSysAddr, keyEpochValidatorsLen(epoch), u64ToHash(maxEpochValidatorsSnapshot+1))

	require.Nil(t, loadEpochValidatorsSnapshot(txn, epoch))
}
