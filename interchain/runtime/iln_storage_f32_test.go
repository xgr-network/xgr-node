package runtime

import (
 "os"
 "path/filepath"
 "testing"
 "time"

 "github.com/stretchr/testify/require"
)

func TestF32PrunesOnlyExpiredUnknownQuorumRequests(t *testing.T){
 w:=&Worker{dataDir:t.TempDir()}
 dir:=filepath.Join(w.ilnDir(),"quorum-requests","not-yet-registered")
 require.NoError(t,os.MkdirAll(dir,0o770))
 old:=filepath.Join(dir,"old.json")
 fresh:=filepath.Join(dir,"fresh.json")
 require.NoError(t,os.WriteFile(old,[]byte("{}"),0o660))
 require.NoError(t,os.WriteFile(fresh,[]byte("{}"),0o660))
 stamp:=time.Now().Add(-2*maxILNQuorumHintAge)
 require.NoError(t,os.Chtimes(old,stamp,stamp))
 w.pruneExpiredUnknownILNRequests()
 _,err:=os.Stat(old)
 require.True(t,os.IsNotExist(err),"expired unverified request must be removed")
 _,err=os.Stat(fresh)
 require.NoError(t,err,"recent request must remain")
 w.pruneExpiredUnknownILNRequests()
 _,err=os.Stat(fresh)
 require.NoError(t,err,"repeated cleanup must preserve recent requests")
}
