package runtime

import (
 "errors"
 "os"
 "path/filepath"
 "testing"

 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/stretchr/testify/require"
)
func TestDirectILNHealthRecoversPersistentCursorWithoutFabricatingHead(t *testing.T){
 dir:=t.TempDir()
 route:=&evm.CheckpointRoute{Name:"iln_8453_1643_example",SourceNetwork:"base",Destination:"xgrchain"}
 w:=&Worker{dataDir:dir}
 file:=filepath.Join(dir,"interchain","iln","cursors","_direct_"+route.Name+".cursor")
 require.NoError(t,os.MkdirAll(filepath.Dir(file),0o770))
 require.NoError(t,os.WriteFile(file,[]byte("53037030\n"),0o660))
 w.recordILNDirectScanFailure(route,errors.New("HTTP 429"))
 got:=w.ilnRouteHealthByName[route.Name]
 require.Equal(t,"unavailable",got.State)
 require.True(t,got.ScanCursorKnown)
 require.Equal(t,uint64(53037030),got.ScanCursor)
 require.False(t,got.SourceHeadKnown)
 require.Zero(t,got.BlockLag)
 require.Zero(t,got.LastSuccessfulScanUnix)
}
