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

// A validator records ITS OWN observation of the live Hook tree. No peer
// supplies its first-seen block or the confirmation decision.
type ilnObservedPrefix struct {
 Root types.Hash `json:"root"`
 Count uint64 `json:"count"`
 SeenAt uint64 `json:"seenAt"`
}
type ilnPrefixFinality struct {
 ChainID uint64 `json:"chainId"`
 Hook types.Address `json:"hook"`
 Pending *ilnObservedPrefix `json:"pending,omitempty"`
 Confirmed *ilnObservedPrefix `json:"confirmed,omitempty"`
}

// confirmedILNPrefixCount is the maximal locally confirmed Hook insertion
// count. It first independently checks the ENTIRE latest source tree, then
// checks the previously observed immutable prefix still matches that tree.
// This never queries a past RPC block and never relies on P2P finality.
func (w *Worker) confirmedILNPrefixCount(
 source *evm.Destination,c evm.ILNReplicaCheckpoint,r evm.ILNReplica,
)(uint64,error){
 if source==nil||c.ChainID!=source.ChainID||c.Hook==types.ZeroAddress||
    c.BlockNumber==0||c.Root==types.ZeroHash||c.Count==0{
    return 0,fmt.Errorf("invalid source Hook finality identity")
 }
 if err:=evm.VerifyILNReplica(r,c);err!=nil{return 0,err}
 confirmedHead,err:=getConfirmedILNHead(source)
 if err!=nil{return 0,err}
 path:=filepath.Join(w.ilnDir(),"observations",strconv.FormatUint(c.ChainID,10),strings.ToLower(c.Hook.String())+".json")
 state:=ilnPrefixFinality{ChainID:c.ChainID,Hook:c.Hook}
 if raw,readErr:=os.ReadFile(path);readErr==nil{
   if len(raw)>4096||json.Unmarshal(raw,&state)!=nil||
      state.ChainID!=c.ChainID||state.Hook!=c.Hook {
      return 0,fmt.Errorf("invalid local Hook finality checkpoint")
   }
 }else if !os.IsNotExist(readErr){return 0,readErr}
 valid:=func(p *ilnObservedPrefix)bool{
   if p==nil||p.SeenAt==0||p.Count==0||p.Count>c.Count||
      p.Root==types.ZeroHash||p.SeenAt>c.BlockNumber{return false}
   _,prefix,e:=evm.VerifiedILNMessagePrefix(r,c,uint32(p.Count-1))
   return e==nil&&prefix.Root==p.Root
 }
 if !valid(state.Confirmed){state.Confirmed=nil}
 if !valid(state.Pending){state.Pending=nil}
 if state.Pending!=nil&&state.Pending.SeenAt<=confirmedHead{
   if state.Confirmed==nil||state.Pending.Count>state.Confirmed.Count{
      state.Confirmed=state.Pending
   }
   state.Pending=nil
 }
 var ready uint64
 if state.Confirmed!=nil{ready=state.Confirmed.Count}
 if ready<c.Count&&state.Pending==nil{
   state.Pending=&ilnObservedPrefix{Root:c.Root,Count:c.Count,SeenAt:c.BlockNumber}
 }
 if err:=os.MkdirAll(filepath.Dir(path),0o770);err!=nil{return 0,err}
 raw,err:=json.Marshal(state);if err!=nil{return 0,err}
 f,err:=os.CreateTemp(filepath.Dir(path),".observation-*")
 if err!=nil{return 0,err}
 defer os.Remove(f.Name())
 if _,err=f.Write(raw);err!=nil{f.Close();return 0,err}
 if err=f.Sync();err!=nil{f.Close();return 0,err}
 if err=f.Close();err!=nil{return 0,err}
 if err=os.Rename(f.Name(),path);err!=nil{return 0,err}
 return ready,nil
}
