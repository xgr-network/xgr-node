package ibft

import (
	"fmt"
 "math/big"
 "github.com/umbracle/ethgo"
	"path/filepath"
	"time"

	stakingcontract "github.com/xgr-network/xgr-node/contracts/staking"
	evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
	interchainRuntime "github.com/xgr-network/xgr-node/interchain/runtime"
	"github.com/xgr-network/xgr-node/types"
)

type ibftInterchainState struct {
	ibft *backendIBFT
}

func (s *ibftInterchainState) OriginChainID() uint64 {
	if s == nil || s.ibft == nil || s.ibft.config == nil || s.ibft.config.Params == nil {
		return 0
	}
	if s.ibft.config.Params.ChainID <= 0 {
		return 0
	}
	return uint64(s.ibft.config.Params.ChainID)
}

func (s *ibftInterchainState) IsSynchronized() bool {
	if s == nil || s.ibft == nil || s.ibft.syncer == nil || s.ibft.blockchain == nil || s.ibft.network == nil {
		return false
	}
	if s.ibft.syncer.GetSyncProgression() != nil || s.ibft.syncer.HasSyncPeer() {
		return false
	}
	// An isolated node cannot prove that its local head is globally fresh.
	if len(s.ibft.network.Peers()) == 0 {
		return false
	}
	header := s.ibft.blockchain.Header()
	if header == nil || header.Timestamp == 0 {
		return false
	}

	now := time.Now()
	if header.Timestamp > uint64(now.Add(15*time.Second).Unix()) {
		return false
	}
	freshness := 5 * s.ibft.blockTime
	if freshness < 30*time.Second {
		freshness = 30 * time.Second
	}
	if now.Sub(time.Unix(int64(header.Timestamp), 0)) > freshness {
		return false
	}

	return true
}

func (s *ibftInterchainState) ValidatorInfo(addr types.Address) (*stakingcontract.ValidatorInfo, error) {
	if s == nil || s.ibft == nil {
		return nil, fmt.Errorf("interchain XGR state reader is unavailable")
	}
	header := s.ibft.blockchain.Header()
	if header == nil {
		return nil, fmt.Errorf("XGR head is unavailable")
	}
	tx, err := s.ibft.executor.BeginTxn(header.StateRoot, header, types.ZeroAddress)
	if err != nil {
		return nil, fmt.Errorf("open XGR state at head %d: %w", header.Number, err)
	}
	info, err := stakingcontract.QueryValidatorInfo(tx, types.ZeroAddress, addr)
	if err != nil {
		return nil, fmt.Errorf("read XGR validator %s at head %d: %w", addr, header.Number, err)
	}
	return info, nil
}

func (s *ibftInterchainState) NativeXGRHead()(uint64,error){
 if !s.IsSynchronized(){return 0,fmt.Errorf("native XGR validator not synchronized")}
 h:=s.ibft.blockchain.Header()
 if h==nil||h.Number==0||h.Hash==types.ZeroHash{return 0,fmt.Errorf("no local committed XGR head")}
 return h.Number,nil
}

func (s *ibftInterchainState) NativeXGRBlock(n uint64)(*evmInterchain.NativeXGRBlock,error){
 head,err:=s.NativeXGRHead()
 if err!=nil{return nil,err}
 if n==0||n>head{return nil,fmt.Errorf("block outside committed head")}
 block,ok:=s.ibft.blockchain.GetBlockByNumber(n,true)
 if !ok||block==nil||block.Header==nil||block.Header.Hash==types.ZeroHash{return nil,fmt.Errorf("canonical block missing")}
 canonical,ok:=s.ibft.blockchain.GetHeaderByNumber(n)
 if !ok||canonical==nil||canonical.Hash!=block.Header.Hash{return nil,fmt.Errorf("block hash not canonical")}
 receipts,err:=s.ibft.blockchain.GetReceiptsByHash(block.Header.Hash)
 if err!=nil||len(receipts)!=len(block.Transactions){return nil,fmt.Errorf("missing canonical receipts: %v",err)}
 view:=func(to types.Address,data []byte)([]byte,error){
  if !s.IsSynchronized(){return nil,fmt.Errorf("local IBFT head stale")}
  h:=s.ibft.blockchain.Header()
  if h==nil||h.Number<n{return nil,fmt.Errorf("local head behind evidence")}
  txn,err:=s.ibft.executor.BeginTxn(h.StateRoot,h,types.ZeroAddress)
  if err!=nil{return nil,err}
  txn.SetNonPayable(true)
  call:=&types.Transaction{Type:types.LegacyTx,From:types.ZeroAddress,To:&to,Gas:h.GasLimit,
   GasPrice:big.NewInt(0),Value:big.NewInt(0),Input:append([]byte(nil),data...)}
  result,err:=txn.Call(call)
  if err!=nil||result==nil||result.Failed(){return nil,fmt.Errorf("native XGR view rejected: %v",err)}
  return append([]byte(nil),result.ReturnValue...),nil
 }
 return &evmInterchain.NativeXGRBlock{Number:n,Hash:block.Header.Hash,
  Transactions:block.Transactions,Receipts:receipts,View:view},nil
}

// NativeXGRRead executes a read-only view at the current committed state root.
func (s *ibftInterchainState) NativeXGRRead(to types.Address,data []byte)([]byte,error){
 head,err:=s.NativeXGRHead();if err!=nil{return nil,err}
 block,err:=s.NativeXGRBlock(head);if err!=nil{return nil,err}
 if block.View==nil{return nil,fmt.Errorf("local EVM view unavailable")}
 return block.View(to,data)
}
func (s *ibftInterchainState) NativeXGRBalance(address types.Address)(*big.Int,error){
 _,err:=s.NativeXGRHead();if err!=nil{return nil,err}
 h:=s.ibft.blockchain.Header()
 txn,err:=s.ibft.executor.BeginTxn(h.StateRoot,h,types.ZeroAddress)
 if err!=nil{return nil,err}
 return new(big.Int).Set(txn.GetBalance(address)),nil
}
func (s *ibftInterchainState) NativeXGREstimateCost(from,to types.Address,data []byte,value *big.Int)(*big.Int,error){
 _,err:=s.NativeXGRHead();if err!=nil{return nil,err}
 h:=s.ibft.blockchain.Header()
 txn,err:=s.ibft.executor.BeginTxn(h.StateRoot,h,types.ZeroAddress)
 if err!=nil{return nil,err}
 txn.SetNonPayable(true)
 call:=&types.Transaction{Type:types.LegacyTx,From:from,To:&to,
  Gas:h.GasLimit,GasPrice:big.NewInt(0),Value:new(big.Int).Set(value),Input:append([]byte(nil),data...)}
 result,err:=txn.Call(call)
 if err!=nil||result==nil||result.Failed(){return nil,fmt.Errorf("native gas simulation rejected: %v",err)}
 price:=new(big.Int).SetUint64(h.BaseFee)
 if price.Sign()==0{price.SetUint64(1879048192)}
 price.Mul(price,big.NewInt(2))
 return new(big.Int).Mul(new(big.Int).SetUint64(result.GasUsed),price),nil
}
func (s *ibftInterchainState) NativeXGRVerifyReceipt(receipt *ethgo.Receipt)error{
 if receipt==nil||receipt.Status!=uint64(types.ReceiptSuccess){return fmt.Errorf("missing successful native receipt")}
 _,err:=s.NativeXGRHead();if err!=nil{return err}
 blockHash,exists:=s.ibft.blockchain.ReadTxLookup(types.Hash(receipt.TransactionHash))
 if !exists||blockHash!=types.Hash(receipt.BlockHash){return fmt.Errorf("native receipt not canonical")}
 b,ok:=s.ibft.blockchain.GetBlockByHash(blockHash,true)
 if !ok||b==nil||b.Number()!=receipt.BlockNumber{return fmt.Errorf("native receipt block not committed")}
 entries,err:=s.ibft.blockchain.GetReceiptsByHash(blockHash)
 if err!=nil{return err}
 for i,tx:=range b.Transactions{
  if tx!=nil&&tx.Hash==types.Hash(receipt.TransactionHash)&&i<len(entries)&&entries[i]!=nil&&
   entries[i].Status!=nil&&*entries[i].Status==types.ReceiptSuccess{return nil}
 }
 return fmt.Errorf("native receipt absent or reverted")
}

// startInterchainRuntime is deliberately outside every consensus-critical hook.
// Destination failures disable only interchain processing; they must never stop
// XGR block production, validation, finalization, or syncing.
func (i *backendIBFT) startInterchainRuntime() {
	destinations, err := evmInterchain.LoadAll()
	if err != nil {
		i.logger.Warn("interchain disabled: invalid destination configuration", "err", err)
		return
	}
	if len(destinations) == 0 {
		return
	}
	// v3.3.2: receipt-pinned chains, no static route ENV.
	// The worker discovers permissionless routes from source Registry logs.
	var routes []*evmInterchain.CheckpointRoute
	stateReader := &ibftInterchainState{ibft: i}
	if stateReader.OriginChainID() == 0 {
		i.logger.Warn("interchain disabled: invalid XGR origin chain ID")
		return
	}

	for _,destination:=range destinations {if destination.ChainID==1643 {destination.NativeXGR=stateReader}}
 dataDir := filepath.Dir(i.config.Path)
	worker, err := interchainRuntime.New(
		i.logger,
		stateReader,
		i.network,
		i.secretsManager,
		dataDir,
		destinations,
		routes,
	)
	if err != nil {
		i.logger.Warn("interchain disabled: worker initialization failed", "err", err)
		return
	}
	if err := worker.Start(); err != nil {
		worker.Close()
		i.logger.Warn("interchain disabled: worker start failed", "err", err)
		return
	}

	i.interchainClose = worker.Close
	i.logger.Info("interchain worker started", "destinations", len(destinations), "routes", len(routes))
}
