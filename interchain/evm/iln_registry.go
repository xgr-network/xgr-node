package evm

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"
	"github.com/umbracle/ethgo/jsonrpc"

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
}

func (d *Destination) ValidateILNRead() error {
	if d == nil {
		return fmt.Errorf("ILN network config is nil")
	}
	if d.ChainID == 0 || d.Domain == 0 {
		return fmt.Errorf("ILN network chain id and domain are required")
	}
	if strings.TrimSpace(d.RPCURL) == "" {
		return fmt.Errorf("ILN network RPC is required")
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
	if d.Confirmations == 0 {
		return fmt.Errorf("ILN network confirmations must be non-zero")
	}
	return nil
}

func ilnSourceClient(source *Destination) (*jsonrpc.Client, error) {
	if err := source.ValidateILNRead(); err != nil {
		return nil, err
	}
	client, err := jsonrpc.NewClient(source.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("create ILN source RPC client: %w", err)
	}
	gotChainID, err := client.Eth().ChainID()
	if err != nil {
		return nil, fmt.Errorf("query ILN source chain id: %w", err)
	}
	if gotChainID == nil || !gotChainID.IsUint64() || gotChainID.Uint64() != source.ChainID {
		return nil, fmt.Errorf("ILN source chain id mismatch")
	}
	return client, nil
}

// GetConfirmedILNHead returns the source height after applying the configured
// confirmation policy. It is used as the upper bound for Gateway event scans.
func GetConfirmedILNHead(source *Destination) (uint64, error) {
	client, err := ilnSourceClient(source)
	if err != nil {
		return 0, err
	}
	return confirmedILNHead(client, source)
}

func confirmedILNHead(client *jsonrpc.Client, source *Destination) (uint64, error) {
	head, err := client.Eth().BlockNumber()
	if err != nil {
		return 0, fmt.Errorf("query ILN source head: %w", err)
	}
	if head < source.Confirmations {
		return 0, fmt.Errorf("ILN source has insufficient confirmed history")
	}
	return head - source.Confirmations, nil
}

// GetConfirmedILNGovernanceNonce reads the single source-chain fee nonce.
func GetConfirmedILNGovernanceNonce(source *Destination) (uint64, error) {
 client,err:=ilnSourceClient(source); if err!=nil{return 0,err}
 _,err=confirmedILNHead(client,source);if err!=nil{return 0,err}
 // An ILN fee nonce is current governance state, not a historical checkpoint.
 raw,err:=callILNSignatureView(client,types.StringToAddress(source.ILNRegistryAddress),"sourceFeeNonce()",ethgo.Latest)
 if err!=nil{return 0,fmt.Errorf("read source fee nonce: %w",err)}
 if len(raw)!=32{return 0,fmt.Errorf("invalid source fee nonce response")}
 value:=new(big.Int).SetBytes(raw); if !value.IsUint64(){return 0,fmt.Errorf("source fee nonce out of range")}
 return value.Uint64(),nil
}

// GetConfirmedILNRoute reads the canonical source route at the latest confirmed
// block. Contract addresses and fee state are protocol truth in the source
// network ILN registry.
func GetConfirmedILNRoute(source *Destination, destinationDomain uint32, routeID types.Hash) (*ILNRouteSnapshot, error) {
	client, err := ilnSourceClient(source)
	if err != nil {
		return nil, err
	}
	confirmed, err := confirmedILNHead(client, source)
	if err != nil {
		return nil, err
	}
	return getILNRouteAtBlock(client, source, destinationDomain, routeID, confirmed, false)
}

// GetILNRouteAtBlock re-reads the exact historical route used by an incoming
// validator vote. The block must already satisfy the configured confirmation
// policy.
func GetILNRouteAtBlock(source *Destination, destinationDomain uint32, routeID types.Hash, blockNumber uint64) (*ILNRouteSnapshot, error) {
	client, err := ilnSourceClient(source)
	if err != nil {
		return nil, err
	}
	confirmed, err := confirmedILNHead(client, source)
	if err != nil {
		return nil, err
	}
	if blockNumber == 0 || blockNumber > confirmed {
		return nil, fmt.Errorf("ILN source block %d is not sufficiently confirmed", blockNumber)
	}
	return getILNRouteAtBlock(client, source, destinationDomain, routeID, blockNumber, true)
}

func getILNRouteAtBlock(
	client *jsonrpc.Client,
	source *Destination,
	destinationDomain uint32,
	routeID types.Hash,
	blockNumber uint64,
	allowHistoricalDisabled bool,
) (*ILNRouteSnapshot, error) {
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
	factory, err := callILNAddressGetter(client, registry, "factory()", ethgo.Latest)
	if err != nil || factory == types.ZeroAddress {
		return nil, fmt.Errorf("archive-free ILN route proof requires an append-only V315 registry: %v", err)
	}
	raw, err := callILNView(client, registry, append(method.ID(), input...), ethgo.Latest)
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
	if err := verifyILNGatewayBinding(client, registry, route, ethgo.Latest); err != nil {
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
	client *jsonrpc.Client,
	registry types.Address,
	route protocol.ILNRoute,
	block ethgo.BlockNumber,
) error {
	gatewayRegistry, err := callILNAddressGetter(client, route.Gateway, "ilnRegistry()", block)
	if err != nil {
		return fmt.Errorf("verify ILN gateway registry: %w", err)
	}
	if gatewayRegistry != registry {
		return fmt.Errorf("ILN gateway registry mismatch")
	}
	warpRouter, err := callILNAddressGetter(client, route.Gateway, "warpRouter()", block)
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
	gatewayMailbox, err := callILNAddressGetter(client, route.Gateway, "mailbox()", block)
	if err != nil {
		return fmt.Errorf("verify ILN gateway mailbox: %w", err)
	}
	if gatewayMailbox != route.Mailbox {
		return fmt.Errorf("ILN gateway mailbox mismatch")
	}
	gatewayHook, err := callILNAddressGetter(client, route.Gateway, "dispatchHook()", block)
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
	client, err := ilnSourceClient(source)
	if err != nil {
		return nil, err
	}
	confirmed, err := confirmedILNHead(client, source)
	if err != nil {
		return nil, err
	}
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
	logs, err := client.Eth().GetLogs(filter)
	if err != nil {
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
	if destination == nil || destinationRouter == types.ZeroAddress || messageID == types.ZeroHash {
		return false, fmt.Errorf("invalid ILN delivery-status query")
	}
	if err := destination.ValidateMembershipRead(); err != nil {
		return false, err
	}
	client, err := jsonrpc.NewClient(destination.RPCURL)
	if err != nil {
		return false, fmt.Errorf("create ILN destination RPC client: %w", err)
	}
	chainID, err := client.Eth().ChainID()
	if err != nil || chainID == nil || !chainID.IsUint64() || chainID.Uint64() != destination.ChainID {
		return false, fmt.Errorf("destination chain ID mismatch or RPC failure: %v", err)
	}
	mailbox, err := callILNAddressGetter(client, destinationRouter, "mailbox()", ethgo.Latest)
	if err != nil {
		return false, fmt.Errorf("resolve destination Mailbox: %w", err)
	}
	selector := crypto.Keccak256([]byte("delivered(bytes32)"))
	data := append(append([]byte(nil), selector[:4]...), messageID.Bytes()...)
	raw, err := callILNView(client, mailbox, data, ethgo.Latest)
	if err != nil {
		return false, fmt.Errorf("query destination Mailbox.delivered: %w", err)
	}
	if len(raw) != 32 {
		return false, fmt.Errorf("invalid delivered result length %d", len(raw))
	}
	for _, b := range raw[:31] {
		if b != 0 { return false, fmt.Errorf("noncanonical delivered boolean encoding") }
	}
	if raw[31] != 0 && raw[31] != 1 {
		return false, fmt.Errorf("invalid delivered boolean value")
	}
	return raw[31] == 1, nil
}

// callILNAddressGetter reads immutable/current contract bindings.
func callILNAddressGetter(client *jsonrpc.Client, address types.Address, signature string, block ethgo.BlockNumber) (types.Address, error) {
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

func callILNSignatureView(client *jsonrpc.Client, address types.Address, signature string, block ethgo.BlockNumber) ([]byte, error) {
	hash := crypto.Keccak256([]byte(signature))
	return callILNView(client, address, hash[:4], block)
}

func callILNView(client *jsonrpc.Client, address types.Address, data []byte, block ethgo.BlockNumber) ([]byte, error) {
	to := ethgo.Address(address)
	raw, err := client.Eth().Call(
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
