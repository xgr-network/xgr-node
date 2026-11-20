package runtime

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strings"

 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

// The operation feed is cheap and independently replayable. Its on-demand
// enriched source receipt is a single-operator read-only snapshot; no BLS
// validator may ever use it as one of its independent source witnesses.
func (w *Worker) persistIndexedSourceReceipt(source *evm.Destination,
 gateway types.Address,op evm.ILNOperation)error{
 if w==nil||source==nil{return fmt.Errorf("invalid indexed source receipt")}
 // XGR uses the locally committed native reader; external chains use
 // one independently chain-ID-verified provider for operational snapshots.
 if !ilnIndexSourceName.MatchString(source.Name)||
  op.MessageID==types.ZeroHash||op.TransactionHash==types.ZeroHash{
  return fmt.Errorf("invalid indexed receipt identity")
 }
 dir:=filepath.Join(w.ilnDir(),"observed",source.Name,"evidence")
 if err:=os.MkdirAll(dir,0o770);err!=nil{return err}
 path:=filepath.Join(dir,strings.ToLower(op.MessageID.String())+".json")
 if old,err:=os.ReadFile(path);err==nil{
  var saved evm.IndexedILNSourceReceipt
  if json.Unmarshal(old,&saved)!=nil||saved.Version!=1||
   saved.SourceChainID!=source.ChainID||
   !strings.EqualFold(saved.SourceTxHash,op.TransactionHash.String())||
   saved.SourceBlockNumber!=op.BlockNumber||
   (saved.VerifiedAs!="FINALIZED_SINGLE_OPERATOR_OBSERVATION_NOT_BLS_EVIDENCE"&&
    saved.VerifiedAs!="FINALIZED_LOCAL_IBFT_OBSERVATION_NOT_BLS_EVIDENCE"){
   return fmt.Errorf("conflicting persisted source receipt observation")
  }
  return nil
 }else if !os.IsNotExist(err){return err}
 var snapshot *evm.IndexedILNSourceReceipt
 var err error
 if source.ChainID==1643{
  snapshot,err=evm.GetIndexedNativeILNSourceReceipt(source,op)
 }else{
  snapshot,err=evm.GetIndexedILNSourceReceipt(source,op,gateway)
 }
 if err!=nil{return fmt.Errorf("indexed source receipt unavailable: %w",err)}
 raw,err:=json.Marshal(snapshot);if err!=nil{return err}
 if len(raw)>4<<20{return fmt.Errorf("indexed receipt snapshot exceeds safe size")}
 return writeILNIndexFile(path,raw)
}
