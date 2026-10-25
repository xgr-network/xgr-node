package evm

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"
	

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

const (
	maxILNLogRange = uint64(1000)
	ilnOperationEventSignature = "ILNOperation(bytes32,bytes32,uint32,uint256)"
)

const ilnRegistryJSONABI = `[
	{"inputs":[{"internalType":"uint32","name":"destinationDomain","type":"uint32"},{"internalType":"bytes32","name":"routeId","type":"bytes32"}],"name":"getRoute","outputs":[
		{"internalType":"uint64","name":"sourceChainId","type":"uint64"},
		{"internalType":"uint32","name":"sourceDomain","type":"uint32"},
		{"internalType":"address","name":"gateway","type":"address"},
		{"internalType":"address","name":"sourceRouter","type":"address"},
		{"internalType":"address","name":"mailbox","type":"address"},
		{"internalType":"address","name":"dispatchHook","type":"address"},
		{"internalType":"address","name":"destinationRouter","type":"address"},
		{"internalType":"uint256","name":"validatorFeeWei","type":"uint256"},
		{"internalType":"bool","name":"enabled","type":"bool"}
	],"stateMutability":"view","type":"function"}
]`

var ilnRegistryABI = abi.MustNewABI(ilnRegistryJSONABI)

type ILNRouteSnapshot struct {
	Registry    types.Address
	Route       protocol.ILNRoute
	BlockNumber uint64
}

type ILNOperation struct {
	RouteID           types.Hash
	MessageID         types.Hash
	DestinationDomain uint32
	ValidatorFeeWei   *big.Int
	BlockNumber       uint64
	TransactionHash types.Hash
	LogIndex uint64
	// CanonicalBlockHash is the successful, finality-checked receipt block.
	// Only set by GetDirectILNOperation; never trusted from a gossip event.
	CanonicalBlockHash types.Hash
}

func (d *Destination) ValidateILNRead() error {
	if d == nil {
		return fmt.Errorf("ILN network config is nil")
	}
	if d.ChainID == 0 || d.Domain == 0 {
		return fmt.Errorf("ILN network chain id and domain are required")
	}
	if d.ChainID != 1643 && strings.TrimSpace(d.RPCURL) == "" {
        return fmt.Errorf("external ILN network RPC is required")
    }
	if strings.TrimSpace(d.ILNRegistryAddress) == "" {
		return fmt.Errorf("ILN registry address is required")
	}
	if err := types.IsValidAddress(d.ILNRegistryAddress); err != nil {
		return fmt.Errorf("ILN registry address is invalid: %w", err)
	}
	if types.StringToAddress(d.ILNRegistryAddress) == types.ZeroAddress {
		return fmt.Errorf("ILN registry address must be non-zero")
	}
	return nil
}

func ilnSourceClient(source *Destination) (ilnRPCClient, error) {
	if err := source.ValidateILNRead(); err != nil {
		return nil, err
	}
	client, err := newVerifiedRPCClient(source)
	if err != nil {
		return nil, fmt.Errorf("create ILN source RPC client: %w", err)
	}
	return client, nil
}

// GetConfirmedILNHead returns the native finality-qualified source height. It is used as the upper bound for Gateway event scans.
func GetConfirmedILNHead(source *Destination) (uint64, error) {
	client, head, err := newVerifiedRPCClientWithHead(source)
	if err != nil { return 0, err }
	defer client.Close()
	return head, nil
}

func confirmedILNHead(client ilnRPCClient, source *Destination) (uint64,error) {
 return finalizedILNHead(client,source)
}

// GetConfirmedILNGovernanceNonce reads the single source-chain fee nonce.
func GetConfirmedILNGovernanceNonce(source *Destination) (uint64, error) {
 if err:=source.ValidateILNRead();err!=nil{return 0,err}
 // A source ILN registry is independent from the destination ValidatorRegistry.
 // Do not require a membership deployment for governance nonce reads.
 selector:=crypto.Keccak256([]byte("sourceFeeNonce()"))
 var raw []byte
 var err error
 if source.ChainID==1643 {
    reader,accessErr:=source.nativeAccess()
    if accessErr!=nil{return 0,accessErr}
    raw,err=reader.NativeXGRRead(types.StringToAddress(source.ILNRegistryAddress),selector[:4])
 } else {
    client,accessErr:=newVerifiedRPCClient(source)
    if accessErr!=nil{return 0,accessErr}
    defer client.Close()
    raw,err=callILNView(client,types.StringToAddress(source.ILNRegistryAddress),selector[:4],ethgo.Latest)
 }
 if err!=nil{return 0,fmt.Errorf("read source fee nonce: %w",err)}
 if len(raw)!=32{return 0,fmt.Errorf("invalid source fee nonce response")}
 value:=new(big.Int).SetBytes(raw); if !value.IsUint64(){return 0,fmt.Errorf("source fee nonce out of range")}
 return value.Uint64(),nil
}

// GetConfirmedILNRoute reads the canonical source route at the latest confirmed
// block. Contract addresses and fee state are protocol truth in the source
// network ILN registry.
func GetConfirmedILNRoute(source *Destination, destinationDomain uint32, routeID types.Hash) (*ILNRouteSnapshot, error) {
	client, confirmed, err := newVerifiedRPCClientWithHead(source)
	if err != nil { return nil, err }
	defer client.Close()
	return getILNRouteAtBlock(client, source, destinationDomain, routeID, confirmed, false)
}

// GetILNRouteAtBlock re-reads the exact historical route used by an incoming
// validator vote. The block must already satisfy the configured confirmation
// policy.
func GetILNRouteAtBlock(source *Destination, destinationDomain uint32, routeID types.Hash, blockNumber uint64) (*ILNRouteSnapshot, error) {
	client, confirmed, err := newVerifiedRPCClientWithHead(source)
	if err != nil { return nil, err }
	defer client.Close()
	if blockNumber == 0 || blockNumber > confirmed {
		return nil, fmt.Errorf("ILN source block %d is not sufficiently confirmed", blockNumber)
	}
	return getILNRouteAtBlock(client, source, destinationDomain, routeID, blockNumber, true)
}

func getILNRouteAtBlock(
	client ilnRPCClient,
	source *Destination,
	destinationDomain uint32,
	routeID types.Hash,
	blockNumber uint64,
	allowHistoricalDisabled bool,
) (*ILNRouteSnapshot, error) {
    return getILNRouteAtBlockWithView(rpcILNView(client),source,destinationDomain,routeID,blockNumber,allowHistoricalDisabled)
}

func getILNRouteAtBlockWithView(
    view ILNView, source *Destination, destinationDomain uint32,
    routeID types.Hash, blockNumber uint64, allowHistoricalDisabled bool,
) (*ILNRouteSnapshot,error) {
    if view==nil||source==nil{return nil,fmt.Errorf("missing verified ILN contract reader")}
	if destinationDomain == 0 {
		return nil, fmt.Errorf("ILN destination domain must be non-zero")
	}
	if routeID == types.ZeroHash {
		return nil, fmt.Errorf("ILN route id must be non-zero")
	}
	block := ethgo.BlockNumber(blockNumber)
	if block < 0 || uint64(block) != blockNumber {
		return nil, fmt.Errorf("ILN source block does not fit ethgo.BlockNumber")
	}

	registry := types.StringToAddress(source.ILNRegistryAddress)
	method := ilnRegistryABI.Methods["getRoute"]
	if method == nil {
		return nil, fmt.Errorf("ILN registry ABI missing getRoute")
	}
	input, err := method.Inputs.Encode(map[string]interface{}{
		"destinationDomain": destinationDomain,
		"routeId": ethgo.Hash(routeID),
	})
	if err != nil {
		return nil, fmt.Errorf("encode ILN getRoute: %w", err)
	}
	// v3.1.7: V315 routes are append-only. The canonical successful Gateway
	// receipt proves the route was registered and enabled when the source
	// transfer executed. Historical eth_call is not available on pruned nodes.
	// The gateway and router bindings are immutable across all source chains.
	factory, err := viewILNAddress(view, registry, "factory()")
	if err != nil || factory == types.ZeroAddress {
		return nil, fmt.Errorf("archive-free ILN route proof requires an append-only V315 registry: %v", err)
	}
	raw, err := view(registry, append(method.ID(), input...))
	if err != nil {
		return nil, fmt.Errorf("call ILN getRoute: %w", err)
	}
	decoded, err := method.Outputs.Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("decode ILN getRoute: %w", err)
	}
	values, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("decode ILN getRoute: unexpected type")
	}

	sourceChainID, err := decodedUint64(values, "sourceChainId", "0")
	if err != nil {
		return nil, err
	}
	sourceDomain64, err := decodedUint64(values, "sourceDomain", "1")
	if err != nil || sourceDomain64 > uint64(^uint32(0)) {
		return nil, fmt.Errorf("decode ILN getRoute: invalid source domain")
	}
	gateway, err := decodedAddress(values, "gateway", "2")
	if err != nil {
		return nil, err
	}
	sourceRouter, err := decodedAddress(values, "sourceRouter", "3")
	if err != nil {
		return nil, err
	}
	mailbox, err := decodedAddress(values, "mailbox", "4")
	if err != nil {
		return nil, err
	}
	hook, err := decodedAddress(values, "dispatchHook", "5")
	if err != nil {
		return nil, err
	}
	destinationRouter, err := decodedAddress(values, "destinationRouter", "6")
	if err != nil {
		return nil, err
	}
	fee, err := decodedBig(values, "validatorFeeWei", "7")
	if err != nil {
		return nil, err
	}
	enabledValue, ok := firstDecoded(values, "enabled", "8")
	if !ok {
		return nil, fmt.Errorf("decode ILN getRoute: missing enabled")
	}
	enabled, ok := enabledValue.(bool)
	if !ok {
		return nil, fmt.Errorf("decode ILN getRoute: invalid enabled")
	}

	if sourceChainID != source.ChainID || uint32(sourceDomain64) != source.Domain {
		return nil, fmt.Errorf("ILN registry source identity mismatch")
	}
	route := protocol.ILNRoute{
		Key: protocol.ILNRouteKey{
			SourceChainID: sourceChainID,
			SourceDomain: uint32(sourceDomain64),
			DestinationDomain: destinationDomain,
			RouteID:           routeID,
		},
		Gateway: gateway,
		SourceRouter: sourceRouter,
		Mailbox: mailbox,
		DispatchHook: hook,
		DestinationRouter: destinationRouter,
		ValidatorFeeWei: fee,
		Enabled: enabled,
	}
	if !route.Enabled && !allowHistoricalDisabled {
		return nil, fmt.Errorf("canonical ILN route is disabled")
	}
	// For historical settlement, the Gateway event/receipt proves that the
	// original bridge was executed while this route was enabled. A governance
	// disable later in that same block must not strand already locked assets.
	validationRoute := route
	validationRoute.Enabled = true
	if err := protocol.ValidateILNRoute(validationRoute); err != nil {
		return nil, fmt.Errorf("invalid canonical ILN route record: %w", err)
	}
	if err := verifyILNGatewayBindingWithView(view, registry, route); err != nil {
		return nil, err
	}
	return &ILNRouteSnapshot{Registry: registry, Route: route, BlockNumber: blockNumber}, nil
}

// verifyILNGatewayBinding checks the control boundary that the fee-qualified
// Gateway uses the canonical Warp source router and the same Mailbox / hook
// recorded by the registry. The Gateway does not become the Hyperlane sender;
// the Warp router remains the sender. Per-message authorization is therefore
// proven separately by the ILNOperation(messageId,...) event.
func verifyILNGatewayBinding(
	client ilnRPCClient,
	registry types.Address,
	route protocol.ILNRoute,
	block ethgo.BlockNumber,
) error {
    return verifyILNGatewayBindingWithView(func(addr types.Address,data []byte)([]byte,error){
        return callILNView(client,addr,data,block)
    },registry,route)
}

func verifyILNGatewayBindingWithView(view ILNView, registry types.Address, route protocol.ILNRoute) error {
    if view==nil{return fmt.Errorf("missing verified gateway reader")}
	gatewayRegistry, err := viewILNAddress(view, route.Gateway, "ilnRegistry()")
	if err != nil {
		return fmt.Errorf("verify ILN gateway registry: %w", err)
	}
	if gatewayRegistry != registry {
		return fmt.Errorf("ILN gateway registry mismatch")
	}
	warpRouter, err := viewILNAddress(view, route.Gateway, "warpRouter()")
	if err != nil {
		return fmt.Errorf("verify ILN gateway warp router: %w", err)
	}
	if warpRouter != route.SourceRouter {
		return fmt.Errorf("ILN gateway source router mismatch")
	}
	// Dynamic Gateway.mailbox()/dispatchHook() getters deliberately reject
	// disabled routes. For historical proofs, check immutable gateway registry
	// and router here; the same-tx canonical Mailbox.Dispatch proof provides
	// the remaining binding to the source message.
	if !route.Enabled { return nil }
	gatewayMailbox, err := viewILNAddress(view, route.Gateway, "mailbox()")
	if err != nil {
		return fmt.Errorf("verify ILN gateway mailbox: %w", err)
	}
	if gatewayMailbox != route.Mailbox {
		return fmt.Errorf("ILN gateway mailbox mismatch")
	}
	gatewayHook, err := viewILNAddress(view, route.Gateway, "dispatchHook()")
	if err != nil {
		return fmt.Errorf("verify ILN gateway hook: %w", err)
	}
	if gatewayHook != route.DispatchHook {
		return fmt.Errorf("ILN gateway hook mismatch")
	}
	return nil
}

// GetConfirmedILNOperations scans only bounded, confirmed source blocks
// for fee-qualified canonical Gateway messages.
func GetConfirmedILNOperations(
	source *Destination,
	gateway types.Address,
	routeID types.Hash,
	destinationDomain uint32,
	fromBlock uint64,
	toBlock uint64,
) ([]ILNOperation, error) {
	client, confirmed, err := newVerifiedRPCClientWithHead(source)
	if err != nil { return nil, err }
	defer client.Close()
	return confirmedILNOperationsOnClient(client,source,gateway,routeID,destinationDomain,fromBlock,toBlock,confirmed)
}

// confirmedILNOperationsOnClient keeps a bounded log scan on the exact
// verified provider, sharing its already-checked finality boundary.
func confirmedILNOperationsOnClient(
    client ilnRPCClient, source *Destination, gateway types.Address,
    routeID types.Hash, destinationDomain uint32, fromBlock,toBlock,confirmed uint64,
) ([]ILNOperation,error) {
	if fromBlock == 0 || toBlock < fromBlock || toBlock > confirmed {
		return nil, fmt.Errorf("invalid or unconfirmed ILN operation block range")
	}
	if toBlock-fromBlock+1 > maxILNLogRange {
		return nil, fmt.Errorf("ILN operation block range exceeds %d blocks", maxILNLogRange)
	}
	if routeID == types.ZeroHash {
		return nil, fmt.Errorf("ILN operation route id must be non-zero")
	}
	if destinationDomain == 0 {
		return nil, fmt.Errorf("ILN operation destination domain must be non-zero")
	}

	topic0 := ethgo.Hash(crypto.Keccak256Hash([]byte(ilnOperationEventSignature)))
	filter := &ethgo.LogFilter{
		Address: []ethgo.Address{ethgo.Address(gateway)},
		Topics: [][]*ethgo.Hash{{&topic0}},
	}
	filter.SetFromUint64(fromBlock)
	filter.SetToUint64(toBlock)
	logs, err := ilnRPCLogs(client,filter)
	if err != nil {
		recordInterchainRPCReadFailure(source)
		return nil, fmt.Errorf("query ILN gateway operations: %w", err)
	}

	out := make([]ILNOperation, 0, len(logs))
	for _, log := range logs {
		operation, err := parseILNOperationLog(log)
		if err != nil {
			return nil, err
		}
		if operation.RouteID != routeID || operation.DestinationDomain != destinationDomain {
			continue
		}
		out = append(out, operation)
	}
	return out, nil
}

// parseILNOperationLog rejects invalid, removed or zero-fee Gateway events.
func parseILNOperationLog(log *ethgo.Log) (ILNOperation, error) {
	var out ILNOperation
	if log == nil || log.Removed {
		return out, fmt.Errorf("invalid removed ILN operation log")
	}
	if len(log.Topics) != 4 {
		return out, fmt.Errorf("invalid ILN operation topic count %d", len(log.Topics))
	}
	expected := ethgo.Hash(crypto.Keccak256Hash([]byte(ilnOperationEventSignature)))
	if log.Topics[0] != expected {
		return out, fmt.Errorf("invalid ILN operation event signature")
	}
	routeID := types.Hash(log.Topics[1])
	messageID := types.Hash(log.Topics[2])
	destinationBig := new(big.Int).SetBytes(log.Topics[3][:])
	if !destinationBig.IsUint64() || destinationBig.Uint64() == 0 || destinationBig.Uint64() > uint64(^uint32(0)) {
		return out, fmt.Errorf("invalid ILN operation destination domain")
	}
	if len(log.Data) != 32 {
		return out, fmt.Errorf("invalid ILN operation fee encoding")
	}
	fee := new(big.Int).SetBytes(log.Data)
	if fee.Sign() <= 0 {
		return out, fmt.Errorf("invalid ILN operation fee")
	}
	if routeID == types.ZeroHash || messageID == types.ZeroHash || log.BlockNumber == 0 {
		return out, fmt.Errorf("invalid ILN operation route id, message id or block")
	}
	return ILNOperation{
		RouteID: routeID,
		MessageID: messageID,
		DestinationDomain: uint32(destinationBig.Uint64()),
		ValidatorFeeWei: fee,
		BlockNumber: log.BlockNumber,
		TransactionHash: types.Hash(log.TransactionHash),
		LogIndex: log.LogIndex,
	}, nil
}

// GetILNMessageDelivered queries the canonical destination router's Mailbox.
// Unknown delivery state fails closed: it must never trigger replacement signing.
func GetILNMessageDelivered(destination *Destination, destinationRouter types.Address, messageID types.Hash) (bool, error) {
 if destination==nil||destinationRouter==types.ZeroAddress||messageID==types.ZeroHash{
  return false,fmt.Errorf("invalid delivery status query")
 }
 view:=func(to types.Address,data []byte)([]byte,error){return DestinationCall(destination,to,data)}
 mailbox,err:=viewILNAddress(view,destinationRouter,"mailbox()")
 if err!=nil{return false,fmt.Errorf("resolve Mailbox: %w",err)}
 selector:=crypto.Keccak256([]byte("delivered(bytes32)"))
 calldata:=append(append([]byte(nil),selector[:4]...),messageID.Bytes()...)
 raw,err:=view(mailbox,calldata)
 if err!=nil{return false,fmt.Errorf("query Mailbox.delivered: %w",err)}
 if len(raw)!=32{return false,fmt.Errorf("invalid delivered response")}
 for _,b:=range raw[:31]{if b!=0{return false,fmt.Errorf("invalid bool encoding")}}
 if raw[31]!=0&&raw[31]!=1{return false,fmt.Errorf("invalid delivery boolean")}
 return raw[31]==1,nil
}

// callILNAddressGetter reads immutable/current contract bindings.
func callILNAddressGetter(client ilnRPCClient, address types.Address, signature string, block ethgo.BlockNumber) (types.Address, error) {
	raw, err := callILNSignatureView(client, address, signature, block)
	if err != nil {
		return types.ZeroAddress, err
	}
	if len(raw) != 32 {
		return types.ZeroAddress, fmt.Errorf("unexpected %s response length %d", signature, len(raw))
	}
	out := types.BytesToAddress(raw[12:])
	if out == types.ZeroAddress {
		return types.ZeroAddress, fmt.Errorf("%s returned zero address", signature)
	}
	return out, nil
}

func callILNSignatureView(client ilnRPCClient, address types.Address, signature string, block ethgo.BlockNumber) ([]byte, error) {
	hash := crypto.Keccak256([]byte(signature))
	return callILNView(client, address, hash[:4], block)
}

func callILNView(client ilnRPCClient, address types.Address, data []byte, block ethgo.BlockNumber) ([]byte, error) {
	to := ethgo.Address(address)
	raw, err := ilnRPCCall(client,
		&ethgo.CallMsg{To: &to, Data: append([]byte(nil), data...)},
		block,
	)
	if err != nil {
		return nil, err
	}
	return decodeRPCHex(raw)
}

func decodedUint64(values map[string]interface{}, keys ...string) (uint64, error) {
	value, ok := firstDecoded(values, keys...)
	if !ok {
		return 0, fmt.Errorf("decode ILN getRoute: missing uint value")
	}
	out, ok := uint64Value(value)
	if !ok {
		return 0, fmt.Errorf("decode ILN getRoute: invalid uint value")
	}
	return out, nil
}

func decodedAddress(values map[string]interface{}, keys ...string) (types.Address, error) {
	value, ok := firstDecoded(values, keys...)
	if !ok {
		return types.ZeroAddress, fmt.Errorf("decode ILN getRoute: missing address")
	}
	switch v := value.(type) {
	case ethgo.Address:
		return types.Address(v), nil
	case types.Address:
		return v, nil
	default:
		return types.ZeroAddress, fmt.Errorf("decode ILN getRoute: invalid address")
	}
}

func decodedBig(values map[string]interface{}, keys ...string) (*big.Int, error) {
	value, ok := firstDecoded(values, keys...)
	if !ok {
		return nil, fmt.Errorf("decode ILN getRoute: missing uint256")
	}
	out, ok := value.(*big.Int)
	if !ok || out == nil || out.Sign() < 0 {
		return nil, fmt.Errorf("decode ILN getRoute: invalid uint256")
	}
	return new(big.Int).Set(out), nil
}


// ScanDirectILNOperationWindow uses ONE verified source RPC session for
// finality head, route binding and a bounded Gateway log window. Scan results
// are discovery hints only: each candidate still requires TWO independent
// complete receipt/FeeVault proofs before a BLS vote can be signed.
func ScanDirectILNOperationWindow(
    source *Destination, destinationDomain uint32, routeID types.Hash,
    cursor uint64, cursorExists bool, window uint64,
) (head uint64, from uint64, to uint64, operations []ILNOperation, err error) {
    if source==nil||window==0||window>maxILNLogRange{
        err=fmt.Errorf("invalid Direct-BLS scan window")
        return
    }
    var client ilnRPCClient
    client,head,err=newVerifiedRPCClientWithHead(source)
    if err!=nil{return}
    defer client.Close()
    if !cursorExists&&head>128{cursor=head-128}
    if cursor>head{
        err=fmt.Errorf("source RPC head behind persisted confirmed operation cursor")
        return
    }
    if cursor==head{return}
    from=cursor+1
    to=cursor+window
    if to<cursor||to>head{to=head}
    var snapshot *ILNRouteSnapshot
    snapshot,err=getILNRouteAtBlock(client,source,destinationDomain,routeID,head,false)
    if err!=nil{
        if rpcTransientFailure(err){recordInterchainRPCReadFailure(source)}
        return
    }
    operations,err=confirmedILNOperationsOnClient(client,source,snapshot.Route.Gateway,
        routeID,destinationDomain,from,to,head)
    if err!=nil{recordInterchainRPCReadError(source,rpcEndpoints(source)[0],err)}
    return
}
