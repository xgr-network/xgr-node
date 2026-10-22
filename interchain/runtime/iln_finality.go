package runtime

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strconv"
 "strings"

 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

// Local observations are never imported from P2P. A latest-state Merkle root
// becomes finality-eligible only after that SAME verified prefix remains
// canonical beyond the configured source confirmation depth.
type ilnObservedPrefix struct {
 Root types.Hash `json:"root"`
 Count uint64 `json:"count"`
 FirstSeenBlock uint64 `json:"firstSeenBlock"`
}
type ilnFinalityObservations struct {
 ChainID uint64 `json:"chainId"`
 Hook types.Address `json:"hook"`
 Pending *ilnObservedPrefix `json:"pending,omitempty"`
 Confirmed *ilnObservedPrefix `json:"confirmed,omitempty"`
}
func (w *Worker) ilnFinalityPath(c evm.ILNReplicaCheckpoint) string {
 return filepath.Join(w.ilnDir(),"observations",strconv.FormatUint(c.ChainID,10),
  strings.ToLower(c.Hook.String())+".json")
}

// observedILNFinalizedCount is an independent source-chain confirmation gate,
// not a second Merkle tree. The current COMPLETE replica has already been
// verified against the locally read Hook root/count before this function.
// A later append is safe: the old confirmed prefix must still reconstruct.
func (w *Worker) observedILNFinalizedCount(
 source *evm.Destination,c evm.ILNReplicaCheckpoint,replica evm.ILNReplica,
) (uint64,error) {
 if source==nil||c.ChainID!=source.ChainID||c.Hook==types.ZeroAddress||
  c.Root==types.ZeroHash||c.Count==0||c.BlockNumber==0 {
  return 0,fmt.Errorf("invalid independent ILN finality identity")
 }
 if err:=evm.VerifyILNReplica(replica,c);err!=nil{return 0,err}
 confirmedHead,err:=getConfirmedILNHead(source)
 if err!=nil{return 0,err}
 if confirmedHead>c.BlockNumber {return 0,fmt.Errorf("source head moved backward during finality check")}
 path:=w.ilnFinalityPath(c)
 state:=ilnFinalityObservations{ChainID:c.ChainID,Hook:c.Hook}
 if data,err:=os.ReadFile(path);err==nil{
  if len(data)>4096||json.Unmarshal(data,&state)!=nil||
   state.ChainID!=c.ChainID||state.Hook!=c.Hook {
   return 0,fmt.Errorf("corrupt local ILN finality observations")
  }
 }else if !os.IsNotExist(err){return 0,err}
 valid:=func(o *ilnObservedPrefix) bool {
  if o==nil||o.Count==0||o.Count>c.Count||o.FirstSeenBlock==0||
   o.FirstSeenBlock>c.BlockNumber||o.Root==types.ZeroHash {return false}
  _,prefix,err:=evm.VerifiedILNMessagePrefix(replica,c,uint32(o.Count-1),o.FirstSeenBlock)
  return err==nil&&prefix.Root==o.Root
 }
 if !valid(state.Confirmed){state.Confirmed=nil}
 if !valid(state.Pending){state.Pending=nil}
 if state.Pending!=nil&&state.Pending.FirstSeenBlock<=confirmedHead {
  if state.Confirmed==nil||state.Pending.Count>state.Confirmed.Count {
   state.Confirmed=state.Pending
  }
  state.Pending=nil
 }
 ready:=uint64(0)
 if state.Confirmed!=nil{ready=state.Confirmed.Count}
 if state.Pending==nil&&ready<c.Count {
  state.Pending=&ilnObservedPrefix{
   Root:c.Root,Count:c.Count,FirstSeenBlock:c.BlockNumber,
  }
 }
 if err:=os.MkdirAll(filepath.Dir(path),0o770);err!=nil{return 0,err}
 encoded,err:=json.Marshal(state)
 if err!=nil{return 0,err}
 tmp,err:=os.CreateTemp(filepath.Dir(path),".iln-finality-*")
 if err!=nil{return 0,err}
 defer os.Remove(tmp.Name())
 if err:=tmp.Chmod(0o660);err!=nil{tmp.Close();return 0,err}
 if _,err:=tmp.Write(encoded);err!=nil{tmp.Close();return 0,err}
 if err:=tmp.Sync();err!=nil{tmp.Close();return 0,err}
 if err:=tmp.Close();err!=nil{return 0,err}
 if err:=os.Rename(tmp.Name(),path);err!=nil{return 0,err}
 return ready,nil
}
