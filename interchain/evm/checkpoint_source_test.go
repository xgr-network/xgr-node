package evm
import (
 "testing"
 "github.com/stretchr/testify/require"
 "github.com/xgr-network/xgr-node/types"
)
func TestDirectRouteWithoutMerkleOrStaticENV(t *testing.T){
 r:=&CheckpointRoute{Name:"base_to_xgr",SourceNetwork:"base",Destination:"xgr",
  RouteID:types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}
 require.NoError(t,r.Validate())
 r.RouteID=types.ZeroHash
 require.Error(t,r.Validate())
 t.Setenv("XGR_INTERCHAIN_ROUTE_BASE_TO_XGR_DESTINATION","xgr")
 all,err:=LoadAll()
 require.NoError(t,err)
 require.Len(t,all,5)
}
