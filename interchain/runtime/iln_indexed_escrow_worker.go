package runtime

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "regexp"
 "strings"
 "time"

 evm "github.com/xgr-network/xgr-node/interchain/evm"
)

const indexedEscrowMaxWatches=24
var indexedEscrowAsset=regexp.MustCompile(`^[A-Z0-9_]{1,32}$`)
type ilnEscrowWatches struct {
 Version int `json:"version"`
 Watches []evm.IndexedILNEscrowWatch `json:"watches"`
}

// This operator-provided watchlist contains contract addresses, NOT RPC URLs.
// It is an optional observational service. It never modifies validator,
// chain/route signing policy, balances or the source Gateway observer.
func (w *Worker) updateILNIndexedEscrows()error{
 if w==nil||os.Getenv("XGR_ILN_READ_INDEX")!="1"{return nil}
 path:=filepath.Join(w.ilnDir(),"escrow-watches.json")
 info,err:=os.Lstat(path)
 if os.IsNotExist(err){return nil} // TVL absent, never silently zero.
 if err!=nil{return err}
 if !info.Mode().IsRegular()||info.Size()<=0||info.Size()>32<<10{
  return fmt.Errorf("invalid ILN read-index escrow watchlist")
 }
 raw,err:=os.ReadFile(path);if err!=nil{return err}
 var config ilnEscrowWatches
 if err=json.Unmarshal(raw,&config);err!=nil{return err}
 if config.Version!=1||len(config.Watches)>indexedEscrowMaxWatches{
  return fmt.Errorf("unsupported escrow index policy")
 }
 seen:=make(map[string]bool)
 for _,watch:=range config.Watches{
  if !indexedEscrowAsset.MatchString(watch.Asset)||!ilnIndexSourceName.MatchString(watch.Chain)||
   seen[watch.Asset]||watch.Kind!="erc20"||watch.Token==""{
   // Native XGR balance/code proofs come from locally committed XGR
   // state, never a remote RPC and never through this foreign-chain probe.
   return fmt.Errorf("invalid, duplicated or unsupported external escrow watch")
  }
  seen[watch.Asset]=true
  chain:=w.destinations[watch.Chain]
  if chain==nil||chain.ChainID==1643{
   return fmt.Errorf("external escrow watch chain not configured")
  }
 }
 target:=filepath.Join(w.ilnDir(),"indexed-escrows")
 if err=os.MkdirAll(target,0o770);err!=nil{return err}
 failed:=0
 for _,watch:=range config.Watches{
  chain:=w.destinations[watch.Chain]
  snapshot,e:=evm.GetIndexedILNEscrowSnapshot(chain,watch)
  if e!=nil{
   failed++
   w.logger.Warn("read-only ILN escrow snapshot unavailable","asset",watch.Asset,"chain",watch.Chain,"err",e)
   continue
  }
  data,e:=json.Marshal(snapshot)
  if e!=nil{return e}
  path:=filepath.Join(target,strings.ToUpper(watch.Asset)+".json")
  temp,e:=os.CreateTemp(target,".escrow-*");if e!=nil{return e}
  if _,e=temp.Write(data);e!=nil{temp.Close();os.Remove(temp.Name());return e}
  if e=temp.Sync();e!=nil{temp.Close();os.Remove(temp.Name());return e}
  if e=temp.Close();e!=nil{os.Remove(temp.Name());return e}
  if e=os.Rename(temp.Name(),path);e!=nil{os.Remove(temp.Name());return e}
 }
 if failed>0{return fmt.Errorf("%d external escrow observations unavailable",failed)}
 return nil
}
func (w *Worker) indexedEscrowLoop(){
 defer w.wg.Done()
 ticker:=time.NewTicker(time.Minute)
 defer ticker.Stop()
 for{
  if err:=w.updateILNIndexedEscrows();err!=nil{
   w.logger.Warn("indexed escrow snapshot incomplete","err",err)
  }
  select{case <-w.closeCh:return;case <-ticker.C:}
 }
}
