package runtime

import (
 "fmt"
 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/xgr-network/xgr-node/types"
)

func (w *Worker) nativeReader(source *evm.Destination)(evm.NativeXGRReader,error){
 if source==nil||source.ChainID!=1643{return nil,fmt.Errorf("not native XGR")}
 r,ok:=w.state.(evm.NativeXGRReader)
 if !ok||r==nil{return nil,fmt.Errorf("native IBFT XGR reader unavailable")}
 return r,nil
}

func (w *Worker) sourceILNHead(source *evm.Destination)(uint64,error){
 if source!=nil&&source.ChainID==1643 {
  r,err:=w.nativeReader(source)
  if err!=nil{return 0,err}
  return r.NativeXGRHead()
 }
 return getConfirmedILNHead(source)
}

func (w *Worker) sourceILNRouteAtBlock(source *evm.Destination,domain uint32,id types.Hash,block uint64)(*evm.ILNRouteSnapshot,error){
 if source!=nil&&source.ChainID==1643 {
  r,err:=w.nativeReader(source)
  if err!=nil{return nil,err}
  return evm.NativeXGRRoute(r,source,domain,id,block,true)
 }
 return getILNRouteAtBlock(source,domain,id,block)
}

func (w *Worker) sourceILNRoute(source *evm.Destination,domain uint32,id types.Hash)(*evm.ILNRouteSnapshot,error){
 if source!=nil&&source.ChainID==1643 {
  head,err:=w.sourceILNHead(source)
  if err!=nil{return nil,err}
  return w.sourceILNRouteAtBlock(source,domain,id,head)
 }
 return getConfirmedILNRoute(source,domain,id)
}

func (w *Worker) sourceILNRouteAdds(source *evm.Destination,from,to uint64)([]evm.ILNRouteAdded,error){
 if source!=nil&&source.ChainID==1643 {
  r,err:=w.nativeReader(source)
  if err!=nil{return nil,err}
  return evm.NativeXGRRouteAdds(r,source,from,to)
 }
 return getConfirmedILNRouteAdds(source,from,to)
}
func (w *Worker) sourceILNActivation(source *evm.Destination)(uint64,error){
 if source!=nil&&source.ChainID==1643{
  h,err:=w.sourceILNHead(source)
  if err!=nil{return 0,err}
  if source.ILNRegistryActivationBlock==0||source.ILNRegistryActivationBlock>h{
   return 0,fmt.Errorf("XGR registry activation outside committed native head")
  }
  return source.ILNRegistryActivationBlock,nil
 }
 return getILNRegistryActivationBlock(source)
}
