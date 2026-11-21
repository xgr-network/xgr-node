package evm

import (
 "net/http"
 "net/http/httptest"
 "testing"

 "github.com/stretchr/testify/require"
)

func TestIndexedSourceReceiptSnapshotsOneFinalizedProviderButNeverReplacesBLSWitnesses(t *testing.T){
 f:=newPinnedReceiptRPCFixture(56)
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  f.handle(t,w,r)
 }))
 defer srv.Close()
 source:=&Destination{Name:"bsc",ChainID:56,Domain:56,
  ILNRegistryAddress:f.registry.String(),
  RPCURLs:[]string{srv.URL},IndependentOperatorIDs:[]string{"one-operator"}}
 snapshot,err:=GetIndexedILNSourceReceipt(source,f.operation,f.gateway)
 require.NoError(t,err)
 require.Equal(t,uint64(56),snapshot.SourceChainID)
 require.Equal(t,f.operation.TransactionHash.String(),snapshot.SourceTxHash)
 require.Equal(t,"FINALIZED_SINGLE_OPERATOR_OBSERVATION_NOT_BLS_EVIDENCE",snapshot.VerifiedAs)
 require.NotEmpty(t,snapshot.Receipt)
 require.NotEmpty(t,snapshot.FinalizedHeadBlock)
 require.Zero(t,f.logRequests.Load(),"receipt indexing must not run historical Gateway logs")
 f.badCanonical.Store(true)
 _,err=GetIndexedILNSourceReceipt(source,f.operation,f.gateway)
 require.Error(t,err,"a forged receipt block hash must not enter the indexed read model")
}
