package runtime

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "regexp"
 "strings"

 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

var ilnIndexSourceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// These are untrusted, finality-bounded discovery observations, NOT complete
// verified receipts, delivery proofs, consensus data, or authorization to sign.
// Index publication is local to a node, and replayable from route cursors.
type ilnIndexedOperation struct {
 Version int `json:"version"`
 Source string `json:"source"`
 Destination string `json:"destination"`
 Route string `json:"route"`
 Gateway string `json:"gateway"`
 RouteID string `json:"routeId"`
 MessageID string `json:"messageId"`
 SourceTxHash string `json:"sourceTxHash"`
 SourceBlockNumber uint64 `json:"sourceBlockNumber"`
 SourceLogIndex uint64 `json:"sourceLogIndex"`
 DestinationDomain uint32 `json:"destinationDomain"`
 ValidatorFeeWei string `json:"validatorFeeWei"`
 EvidenceStatus string `json:"evidenceStatus"`
}

func (w *Worker) persistILNIndexedOperation(source *evm.Destination,
 route *evm.CheckpointRoute,gateway types.Address,op evm.ILNOperation)error{
 if w==nil||source==nil||route==nil||!ilnIndexSourceName.MatchString(source.Name)||
  op.MessageID==types.ZeroHash||op.TransactionHash==types.ZeroHash||
  op.RouteID!=route.RouteID||op.ValidatorFeeWei==nil||
  op.ValidatorFeeWei.Sign()<=0||op.BlockNumber==0||gateway==types.ZeroAddress {
  return fmt.Errorf("invalid ILN index observation")
 }
 item:=ilnIndexedOperation{
  Version:1,Source:source.Name,Destination:route.Destination,
  Route:route.Name,Gateway:gateway.String(),RouteID:op.RouteID.String(),
  MessageID:op.MessageID.String(),SourceTxHash:op.TransactionHash.String(),
  SourceBlockNumber:op.BlockNumber,SourceLogIndex:op.LogIndex,
  DestinationDomain:op.DestinationDomain,
  ValidatorFeeWei:op.ValidatorFeeWei.String(),
  EvidenceStatus:"UNTRUSTED_FINALITY_BOUNDED_HINT",
 }
 dir:=filepath.Join(w.ilnDir(),"observed",source.Name)
 if err:=os.MkdirAll(dir,0o770);err!=nil{return err}
 name:=strings.ToLower(op.MessageID.String())+".json"
 path:=filepath.Join(dir,name)
 data,err:=json.Marshal(item);if err!=nil{return err}
 if old,err:=os.ReadFile(path);err==nil{
  var existing ilnIndexedOperation
  if json.Unmarshal(old,&existing)!=nil||existing!=item {
   return fmt.Errorf("conflicting persisted ILN observation")
  }
  return nil
 }else if !os.IsNotExist(err){return err}
 file,err:=os.CreateTemp(dir,".observed-*");if err!=nil{return err}
 defer os.Remove(file.Name())
 if _,err=file.Write(data);err!=nil{file.Close();return err}
 if err=file.Sync();err!=nil{file.Close();return err}
 if err=file.Close();err!=nil{return err}
 // Atomic publication; the scanner does NOT advance its durable cursor
 // until this observation and all signing/retry work is complete.
 return os.Rename(file.Name(),path)
}
