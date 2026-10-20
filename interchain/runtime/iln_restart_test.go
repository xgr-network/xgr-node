package runtime

import (
 "os"
 "path/filepath"
 "testing"

 "github.com/stretchr/testify/require"
 "github.com/xgr-network/xgr-node/crypto"
 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

func TestILNValidatorRestartRetainsVerifiedReplicaAndPerSetCursor(t *testing.T){
 dir:=t.TempDir()
 hook:=types.StringToAddress("0x4444444444444444444444444444444444444444")
 leaf:=crypto.Keccak256Hash([]byte("confirmed message"))
 root:=ilnTestCanonicalRoot(leaf)
 c:=evm.ILNReplicaCheckpoint{ChainID:8453,Hook:hook,Root:root,Count:1,BlockNumber:128}
 original:=&Worker{dataDir:dir}
 r:=evm.ILNReplica{ChainID:8453,Hook:hook,Root:root,Leaves:[]types.Hash{leaf},CheckpointBlock:128}
 path:=original.ilnReplicaPath(c)
 require.NoError(t,evm.StoreVerifiedILNReplica(path,r,c))
 require.NoError(t,original.writeILNLeafCursor("base_to_xgr",7,1))
 // SetID 8 must begin at zero after rotation; a new validator cannot
 // silently reuse an older quorum's progress marker.
 cold:=&Worker{dataDir:dir}
 restored,err:=evm.LoadVerifiedILNReplica(cold.ilnReplicaPath(c),c)
 require.NoError(t,err);require.Equal(t,[]types.Hash{leaf},restored.Leaves)
 oldCursor,err:=cold.readILNLeafCursor("base_to_xgr",7)
 require.NoError(t,err);require.Equal(t,uint64(1),oldCursor)
 freshCursor,err:=cold.readILNLeafCursor("base_to_xgr",8)
 require.NoError(t,err);require.Zero(t,freshCursor)
 _,err=evm.BuildILNReplicaProof(restored,c,0)
 require.NoError(t,err)
 // An abruptly interrupted atomic rewrite leaves no corrupted accepted
 // snapshot. The restarted node verifies the root on every load.
 require.NoError(t,os.WriteFile(filepath.Join(filepath.Dir(path),".iln-replica-interrupted"),[]byte("{"),0o660))
 restored,err=evm.LoadVerifiedILNReplica(cold.ilnReplicaPath(c),c)
 require.NoError(t,err);require.Equal(t,[]types.Hash{leaf},restored.Leaves)
 require.NoError(t,os.WriteFile(path,[]byte("{}"),0o660))
 _,err=evm.LoadVerifiedILNReplica(cold.ilnReplicaPath(c),c)
 require.Error(t,err)
}
