package snapshot

import (
	"testing"

	"github.com/stretchr/testify/require"
	ibftOp "github.com/xgr-network/xgr-node/consensus/ibft/proto"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func TestIBFTSnapshotResult_GetOutput_HidesUptimeFieldsWhenInactive(t *testing.T) {
	v := validators.NewECDSAValidator(types.StringToAddress("0x123"))
	res, err := newIBFTSnapshotResult(&ibftOp.Snapshot{
		Number: 1,
		Hash:   "0x1",
		Validators: []*ibftOp.Snapshot_Validator{{
			Type:    "ecdsa",
			Address: v.Addr().String(),
			Data:    v.Bytes(),
		}},
	})
	require.NoError(t, err)

	out := res.GetOutput()
	require.NotContains(t, out, "Micro-epoch")
	require.Contains(t, out, "[VALIDATORS]")
	require.Contains(t, out, "ADDRESS")
	require.NotContains(t, out, "UPTIME NOMINAL")
}

func TestIBFTSnapshotResult_GetOutput_ShowsUptimeFieldsWhenActive(t *testing.T) {
	v := validators.NewECDSAValidator(types.StringToAddress("0x123"))
	res, err := newIBFTSnapshotResult(&ibftOp.Snapshot{
		Number:                     2,
		Hash:                       "0x2",
		MicroEpoch:                 10,
		UptimeEpoch:                3,
		UptimeTotalEffectiveWeight: 100,
		UptimeActiveEffectiveWeight: 90,
		Validators: []*ibftOp.Snapshot_Validator{{
			Type:                  "ecdsa",
			Address:               v.Addr().String(),
			Data:                  v.Bytes(),
			UptimeNominalWeight:   100,
			UptimeEffectiveWeight: 90,
			UptimeInactivity:      1,
		}},
	})
	require.NoError(t, err)

	out := res.GetOutput()
	require.Contains(t, out, "Micro-epoch")
	require.Contains(t, out, "UPTIME NOMINAL")
}

func TestIBFTSnapshotResult_GetOutput_ShowsUptimeColumnsWhenWeightsAreZero(t *testing.T) {
	v := validators.NewECDSAValidator(types.StringToAddress("0x123"))
	res, err := newIBFTSnapshotResult(&ibftOp.Snapshot{
		Number:      329,
		Hash:        "0x329",
		MicroEpoch:  329,
		UptimeEpoch: 32,
		Validators: []*ibftOp.Snapshot_Validator{{
			Type:    "ecdsa",
			Address: v.Addr().String(),
			Data:    v.Bytes(),
		}},
	})
	require.NoError(t, err)

	out := res.GetOutput()
	require.Contains(t, out, "Micro-epoch")
	require.Contains(t, out, "UPTIME NOMINAL")
	require.Contains(t, out, "  0               0                 0")
}
