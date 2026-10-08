package runtime

import (
 "encoding/json"
 "os"
 "path/filepath"
 "testing"
 "time"

 "github.com/stretchr/testify/require"
 protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

func TestILNStoragePrunesOnlyProvenDeliveredQuorums(t *testing.T) {
 id:="0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
 root:=t.TempDir()
 source:=&evm.Destination{Name:"base",Domain:8453,ChainID:8453}
 destination:=&evm.Destination{Name:"xgr",Domain:1643,ChainID:1643}
 router:=types.StringToAddress("0x6666666666666666666666666666666666666666")
 route:=&evm.CheckpointRoute{Name:"base_to_xgr",SourceNetwork:"base",Destination:"xgr",RouteID:types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")}
 w:=&Worker{dataDir:root, destinations:map[string]*evm.Destination{"base":source,"xgr":destination},routes:map[string]*evm.CheckpointRoute{"base_to_xgr":route}}
 dir:=filepath.Join(w.attestationDir(),"base_to_xgr")
 nested:=filepath.Join(dir,id)
 require.NoError(t,os.MkdirAll(nested,0o770))
 data,_:=json.Marshal(ilnCheckpointAttestation{AuthorizedMessageID:id,SourceBlockNumber:51,Chain:"base_to_xgr"})
 for _,p:=range []string{filepath.Join(nested,"7.json"),filepath.Join(dir,id+".json"),filepath.Join(dir,"latest.json")} {
  require.NoError(t,os.WriteFile(p,data,0o660))
 }
 old:=time.Now().Add(-8*24*time.Hour)
 require.NoError(t,os.Chtimes(nested,old,old))
 oldRoute,oldDelivered:=getConfirmedILNRoute,getILNMessageDelivered
 defer func(){getConfirmedILNRoute=oldRoute;getILNMessageDelivered=oldDelivered}()
 getConfirmedILNRoute=func(*evm.Destination,uint32,types.Hash)(*evm.ILNRouteSnapshot,error){
  return &evm.ILNRouteSnapshot{Route:evmRouteForStorageTest(router)},nil
 }
 getILNMessageDelivered=func(*evm.Destination,types.Address,types.Hash)(bool,error){return false,nil}
 w.pruneILNQuorumCaches()
 _,err:=os.Stat(nested);require.NoError(t,err,"young pending quorum must survive")
 // Even after the TTL, an unreadable historical proof must protect the cache.
 old30:=time.Now().Add(-31*24*time.Hour)
 require.NoError(t,os.Chtimes(nested,old30,old30))
 require.NoError(t,os.Chtimes(filepath.Join(dir,id+".json"),old30,old30))
 w.pruneILNQuorumCaches()
 _,err=os.Stat(nested)
 require.NoError(t,err,"unverifiable old pending quorum must survive")
 getILNMessageDelivered=func(*evm.Destination,types.Address,types.Hash)(bool,error){return true,nil}
 w.pruneILNQuorumCaches()
 _,err=os.Stat(nested);require.True(t,os.IsNotExist(err))
 _,err=os.Stat(filepath.Join(dir,id+".json"));require.True(t,os.IsNotExist(err))
}

func evmRouteForStorageTest(router types.Address) protocol.ILNRoute {
 return protocol.ILNRoute{DestinationRouter:router}
}
