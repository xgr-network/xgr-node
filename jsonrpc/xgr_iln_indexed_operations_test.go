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
 feed:=filepath.Join(path,"feed")
 require.NoError(t,os.MkdirAll(feed,0o700))
 mk:=func(id,sequence string){
  item:=ilnIndexedOperationRPC{
   Version:1,Cursor:sequence,Source:source,Destination:"xgrchain",Route:"bsc_to_xgr",
   Gateway:"0x"+strings.Repeat("1",40),RouteID:"0x"+strings.Repeat("2",64),
   MessageID:id,SourceTxHash:"0x"+strings.Repeat("3",64),
   SourceBlockNumber:100,DestinationDomain:1643,
   ValidatorFeeWei:"100",EvidenceStatus:"UNTRUSTED_FINALITY_BOUNDED_HINT",
  }
  raw,err:=json.Marshal(item);require.NoError(t,err)
  require.NoError(t,os.WriteFile(filepath.Join(path,id+".json"),raw,0o600))
  require.NoError(t,os.WriteFile(filepath.Join(feed,sequence+".json"),raw,0o600))
 }
 a:="0x"+strings.Repeat("a",64)
 b:="0x"+strings.Repeat("b",64)
 mk(a,"00000000000000000002");mk(b,"00000000000000000001")
 ep:=newXGREndpoint(nil,dir)
 one,err:=ep.GetILNIndexedOperation(source,a)
 require.NoError(t,err)
 require.Equal(t,a,one.MessageID)
 require.Equal(t,"UNTRUSTED_FINALITY_BOUNDED_HINT",one.EvidenceStatus)
 first,err:=ep.GetILNIndexedOperations(source,"",1)
 require.NoError(t,err)
 require.Len(t,first.Operations,1)
 require.Equal(t,b,first.Operations[0].MessageID)
 require.Equal(t,"00000000000000000001",first.NextCursor)
 second,err:=ep.GetILNIndexedOperations(source,first.NextCursor,1)
 require.NoError(t,err)
 require.Len(t,second.Operations,1)
 require.Equal(t,a,second.Operations[0].MessageID)
 require.Empty(t,second.NextCursor)
 require.Equal(t,"00000000000000000002",second.Operations[0].Cursor)
 // Missing durable sequence is corruption, never silently skipped.
 require.NoError(t,os.Remove(filepath.Join(feed,"00000000000000000001.json")))
 _,err=ep.GetILNIndexedOperations(source,"",5)
 require.ErrorContains(t,err,"sequence gap")
 _,err=ep.GetILNIndexedOperations(source,"",101)
 require.Error(t,err)
 _,err=ep.GetILNIndexedOperation("../private",a)
 require.Error(t,err)
 _,err=ep.GetILNIndexedOperation(source,"../../etc/passwd")
 require.Error(t,err)
 _,err=ep.GetILNIndexedOperations(source,"not-a-cursor",1)
 require.Error(t,err)
}
func TestILNIndexedOperationNeverPromotesCorruptRecord(t *testing.T){
 dir:=t.TempDir()
 path:=filepath.Join(dir,"interchain","iln","observed","base")
 require.NoError(t,os.MkdirAll(path,0o700))
 id:="0x"+strings.Repeat("a",64)
 corrupt:=[]byte(`{"version":1,"source":"base","messageId":"0xdead","evidenceStatus":"VERIFIED"}`)
 require.NoError(t,os.WriteFile(filepath.Join(path,id+".json"),corrupt,0o600))
 feed:=filepath.Join(path,"feed")
 require.NoError(t,os.MkdirAll(feed,0o700))
 require.NoError(t,os.WriteFile(filepath.Join(feed,"00000000000000000001.json"),corrupt,0o600))
 ep:=newXGREndpoint(nil,dir)
 _,err:=ep.GetILNIndexedOperation("base",id)
 require.Error(t,err)
 _,err=ep.GetILNIndexedOperations("base","",10)
 require.Error(t,err)
}
