package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestILNPersistedScanHealthFollowsScanCadence(t *testing.T) {
	const scannedAt = int64(1000)
	scan := ilnRouteHealth{State: "ok", UpdatedUnix: scannedAt}
	require.False(t, ilnRouteScanStale(scan, scannedAt+91))
	require.False(t, ilnRouteScanStale(scan, scannedAt+int64(ilnExternalCheckpointInterval/time.Second)))
	require.True(t, ilnRouteScanStale(scan, scannedAt+int64((ilnExternalCheckpointInterval+2*time.Minute)/time.Second)+1))
	scan.State = "unavailable"
	require.False(t, ilnRouteScanStale(scan, scannedAt+3600), "preserve the actual RPC failure")
}
