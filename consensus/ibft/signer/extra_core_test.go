package signer

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func TestIstanbulExtra_CoreRLPFieldsOnly(t *testing.T) {
	t.Parallel()

	round := uint64(7)
	set := validators.NewECDSAValidatorSet(
		validators.NewECDSAValidator(types.StringToAddress("0x1001")),
	)
	extra := &IstanbulExtra{
		Validators:           set,
		ProposerSeal:         []byte{0x1, 0x2},
		CommittedSeals:       &SerializedSeal{[]byte{0xaa}},
		ParentCommittedSeals: &SerializedSeal{[]byte{0xbb}},
		RoundNumber:          &round,
	}

	enc := extra.MarshalRLPTo(nil)
	dec := &IstanbulExtra{Validators: validators.NewECDSAValidatorSet(), CommittedSeals: &SerializedSeal{}, ParentCommittedSeals: &SerializedSeal{}}
	require.NoError(t, dec.UnmarshalRLP(enc))
	require.Equal(t, 1, dec.Validators.Len())
	require.Equal(t, []byte{0x1, 0x2}, dec.ProposerSeal)
	require.Equal(t, 1, dec.CommittedSeals.Num())
	require.Equal(t, 1, dec.ParentCommittedSeals.Num())
	require.NotNil(t, dec.RoundNumber)
	require.Equal(t, round, *dec.RoundNumber)
}
