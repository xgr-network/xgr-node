package interchain

import (
 "os"
 "path/filepath"
 "testing"

 "github.com/stretchr/testify/require"
)

func TestILNStorageStatusReportsQuorumDiskWithoutDeleting(t *testing.T) {
 dir:=t.TempDir()
 files:=[]struct{path string;data string}{
  {filepath.Join("interchain","attestations","base_to_xgr","msg","1.json"),"quorum"},
  {filepath.Join("interchain","iln","local-votes","vote.json"),"vote"},
  {filepath.Join("interchain","iln","quorum-requests","base_to_xgr","hint.json"),"hint"},
  {filepath.Join("interchain","governance","quorums","quorum.json"),"governance"},
 }
 for _,f:=range files {
  path:=filepath.Join(dir,f.path)
  require.NoError(t,os.MkdirAll(filepath.Dir(path),0o770))
  require.NoError(t,os.WriteFile(path,[]byte(f.data),0o660))
 }
 result,err:=readILNStorageStatus(dir)
 require.NoError(t,err)
 require.EqualValues(t,4,result.TotalFiles)
 require.EqualValues(t,6+4+4+10,result.TotalBytes)
 require.EqualValues(t,1,result.AttestationFiles)
 require.EqualValues(t,1,result.LocalVoteFiles)
 require.EqualValues(t,1,result.RequestFiles)
 require.EqualValues(t,1,result.GovernanceFiles)
 for _,f:=range files {
  _,err:=os.Stat(filepath.Join(dir,f.path))
  require.NoError(t,err,"storage-status may never delete files")
 }
}
