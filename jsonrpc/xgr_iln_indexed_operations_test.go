package jsonrpc

import (
 "encoding/json"
 "os"
 "path/filepath"
 "strings"
 "testing"

 "github.com/stretchr/testify/require"
)

func TestILNIndexedOperationReadAndBoundedPagination(t *testing.T){
 dir:=t.TempDir()
 source:="bsc"
 path:=filepath.Join(dir,"interchain","iln","observed",source)
 require.NoError(t,os.MkdirAll(path,0o700))
 mk:=func(id string){
  item:=ilnIndexedOperationRPC{
   Version:1,Source:source,Destination:"xgrchain",Route:"bsc_to_xgr",
   Gateway:"0x"+strings.Repeat("1",40),RouteID:"0x"+strings.Repeat("2",64),
   MessageID:id,SourceTxHash:"0x"+strings.Repeat("3",64),
   SourceBlockNumber:100,DestinationDomain:1643,
   ValidatorFeeWei:"100",EvidenceStatus:"UNTRUSTED_FINALITY_BOUNDED_HINT",
  }
  raw,err:=json.Marshal(item);require.NoError(t,err)
  require.NoError(t,os.WriteFile(filepath.Join(path,id+".json"),raw,0o600))
 }
 a:="0x"+strings.Repeat("a",64)
 b:="0x"+strings.Repeat("b",64)
 mk(a);mk(b)
 ep:=newXGREndpoint(nil,dir)
 one,err:=ep.GetILNIndexedOperation(source,a)
 require.NoError(t,err)
 require.Equal(t,a,one.MessageID)
 require.Equal(t,"UNTRUSTED_FINALITY_BOUNDED_HINT",one.EvidenceStatus)
 first,err:=ep.GetILNIndexedOperations(source,"",1)
 require.NoError(t,err)
 require.Len(t,first.Operations,1)
 require.Equal(t,a,first.Operations[0].MessageID)
 require.Equal(t,a,first.NextCursor)
 second,err:=ep.GetILNIndexedOperations(source,first.NextCursor,1)
 require.NoError(t,err)
 require.Len(t,second.Operations,1)
 require.Equal(t,b,second.Operations[0].MessageID)
 require.Empty(t,second.NextCursor)
 _,err=ep.GetILNIndexedOperations(source,"",101)
 require.Error(t,err)
 _,err=ep.GetILNIndexedOperation("../private",a)
 require.Error(t,err)
 _,err=ep.GetILNIndexedOperation(source,"../../etc/passwd")
 require.Error(t,err)
 _,err=ep.GetILNIndexedOperations(source,"not-a-hash",1)
 require.Error(t,err)
}
func TestILNIndexedOperationNeverPromotesCorruptRecord(t *testing.T){
 dir:=t.TempDir()
 path:=filepath.Join(dir,"interchain","iln","observed","base")
 require.NoError(t,os.MkdirAll(path,0o700))
 id:="0x"+strings.Repeat("a",64)
 require.NoError(t,os.WriteFile(filepath.Join(path,id+".json"),
  []byte(`{"version":1,"source":"base","messageId":"0xdead","evidenceStatus":"VERIFIED"}`),0o600))
 ep:=newXGREndpoint(nil,dir)
 _,err:=ep.GetILNIndexedOperation("base",id)
 require.Error(t,err)
 _,err=ep.GetILNIndexedOperations("base","",10)
 require.Error(t,err)
}
