package state

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"

	"golang.org/x/crypto/sha3"

	"github.com/hashicorp/go-hclog"

	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/contracts"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/state/runtime"
	"github.com/xgr-network/xgr-node/state/runtime/addresslist"
	"github.com/xgr-network/xgr-node/state/runtime/evm"
	"github.com/xgr-network/xgr-node/state/runtime/precompiled"
	"github.com/xgr-network/xgr-node/state/runtime/tracer"
	"github.com/xgr-network/xgr-node/types"
)

var SystemAddress = types.StringToAddress("0x0000000000000000000000000000000000009999")

func Keccak256Hash(data []byte) [32]byte {
	hash := sha3.NewLegacyKeccak256()
	hash.Write(data)
	var result [32]byte
	hash.Sum(result[:0])
	return result
}

func HexToAddress(hexStr string) [20]byte {
	var addr [20]byte

	// "0x" entfernen
	clean := strings.TrimPrefix(hexStr, "0x")
	data, err := hex.DecodeString(clean)
	if err != nil {
		panic("invalid address: " + hexStr)
	}

	copy(addr[20-len(data):], data)
	return addr
}

// LeftPadBytes füllt ein Byte-Slice links mit 0 auf die angegebene Länge auf
func LeftPadBytes(input []byte, length int) []byte {
	if len(input) >= length {
		return input
	}
	padded := make([]byte, length)
	copy(padded[length-len(input):], input)
	return padded
}

func hashToUint64(h types.Hash) uint64 {
	return binary.BigEndian.Uint64(h[24:])
}

const (
	SpuriousDragonMaxCodeSize = 24576
	TxPoolMaxInitCodeSize     = 2 * SpuriousDragonMaxCodeSize

	TxGas                     uint64 = 21000 // Per transaction not creating a contract
	TxGasContractCreation     uint64 = 53000 // Per transaction that creates a contract
	TxAccessListAddressGas    uint64 = 2400  // Per EIP-2930 access list address
	TxAccessListStorageKeyGas uint64 = 1900  // Per EIP-2930 access list storage key
)

// GetHashByNumber returns the hash function of a block number
type GetHashByNumber = func(i uint64) types.Hash

type GetHashByNumberHelper = func(*types.Header) GetHashByNumber

// Executor is the main entity
type Executor struct {
	logger  hclog.Logger
	config  *chain.Params
	state   State
	GetHash GetHashByNumberHelper

	PostHook        func(txn *Transition)
	GenesisPostHook func(*Transition) error
}

// NewExecutor creates a new executor
func NewExecutor(config *chain.Params, s State, logger hclog.Logger) *Executor {
	return &Executor{
		logger: logger,
		config: config,
		state:  s,
	}
}

func (e *Executor) WriteGenesis(
	alloc map[types.Address]*chain.GenesisAccount,
	initialStateRoot types.Hash) (types.Hash, error) {
	var (
		snap Snapshot
		err  error
	)

	if initialStateRoot == types.ZeroHash {
		snap = e.state.NewSnapshot()
	} else {
		snap, err = e.state.NewSnapshotAt(initialStateRoot)
	}

	if err != nil {
		return types.Hash{}, err
	}

	txn := NewTxn(snap)
	config := e.config.Forks.At(0)

	env := runtime.TxContext{
		ChainID: e.config.ChainID,
	}

	transition := &Transition{
		logger:      e.logger,
		ctx:         env,
		state:       txn,
		auxState:    e.state,
		gasPool:     uint64(env.GasLimit),
		config:      config,
		precompiles: precompiled.NewPrecompiled(),
	}

	for addr, account := range alloc {
		if account.Balance != nil {
			txn.AddBalance(addr, account.Balance)
		}

		if account.Nonce != 0 {
			txn.SetNonce(addr, account.Nonce)
		}

		if len(account.Code) != 0 {
			txn.SetCode(addr, account.Code)
		}

		for key, value := range account.Storage {
			txn.SetState(addr, key, value)
		}
	}

	if e.GenesisPostHook != nil {
		if err := e.GenesisPostHook(transition); err != nil {
			return types.Hash{}, fmt.Errorf("Error writing genesis block: %w", err)
		}
	}

	objs, err := txn.Commit(false)
	if err != nil {
		return types.Hash{}, err
	}

	_, root, err := snap.Commit(objs)
	if err != nil {
		return types.Hash{}, err
	}

	return types.BytesToHash(root), nil
}

type BlockResult struct {
	Root     types.Hash
	Receipts []*types.Receipt
	TotalGas uint64
}

// ProcessBlock already does all the handling of the whole process
func (e *Executor) ProcessBlock(
	parentRoot types.Hash,
	block *types.Block,
	blockCreator types.Address,
) (*Transition, error) {
	txn, err := e.BeginTxn(parentRoot, block.Header, blockCreator)
	if err != nil {
		return nil, err
	}

	for _, t := range block.Transactions {
		if t.Gas > block.Header.GasLimit {
			continue
		}

		if err = txn.Write(t); err != nil {
			return nil, err
		}
	}

	return txn, nil
}

// StateAt returns snapshot at given root
func (e *Executor) State() State {
	return e.state
}

// StateAt returns snapshot at given root
func (e *Executor) StateAt(root types.Hash) (Snapshot, error) {
	return e.state.NewSnapshotAt(root)
}

// GetForksInTime returns the active forks at the given block height
func (e *Executor) GetForksInTime(blockNumber uint64) chain.ForksInTime {
	return e.config.Forks.At(blockNumber)
}

func (e *Executor) BeginTxn(
	parentRoot types.Hash,
	header *types.Header,
	coinbaseReceiver types.Address,
) (*Transition, error) {
	forkConfig := e.config.Forks.At(header.Number)

	auxSnap2, err := e.state.NewSnapshotAt(parentRoot)
	if err != nil {
		return nil, err
	}

	burnContract := types.ZeroAddress
	if forkConfig.London {
		burnContract, err = e.config.CalculateBurnContract(header.Number)
		if err != nil {
			return nil, err
		}
	}

	newTxn := NewTxn(auxSnap2)

	txCtx := runtime.TxContext{
		Coinbase:     coinbaseReceiver,
		Timestamp:    int64(header.Timestamp),
		Number:       int64(header.Number),
		Difficulty:   types.BytesToHash(new(big.Int).SetUint64(header.Difficulty).Bytes()),
		BaseFee:      new(big.Int).SetUint64(header.BaseFee),
		GasLimit:     int64(header.GasLimit),
		ChainID:      e.config.ChainID,
		BurnContract: burnContract,
	}

	txn := &Transition{
		logger:   e.logger,
		ctx:      txCtx,
		state:    newTxn,
		snap:     auxSnap2,
		getHash:  e.GetHash(header),
		auxState: e.state,
		config:   forkConfig,
		gasPool:  uint64(txCtx.GasLimit),

		receipts:     []*types.Receipt{},
		totalGas:     0,
		donationFee:  nil,
		validatorFee: nil,
		burnedFee:    nil,

		evm:         evm.NewEVM(),
		precompiles: precompiled.NewPrecompiled(),
		PostHook:    e.PostHook,
	}

	// enable contract deployment allow list (if any)
	if e.config.ContractDeployerAllowList != nil {
		txn.deploymentAllowList = addresslist.NewAddressList(txn, contracts.AllowListContractsAddr)
	}

	if e.config.ContractDeployerBlockList != nil {
		txn.deploymentBlockList = addresslist.NewAddressList(txn, contracts.BlockListContractsAddr)
	}

	// enable transactions allow list (if any)
	if e.config.TransactionsAllowList != nil {
		txn.txnAllowList = addresslist.NewAddressList(txn, contracts.AllowListTransactionsAddr)
	}

	if e.config.TransactionsBlockList != nil {
		txn.txnBlockList = addresslist.NewAddressList(txn, contracts.BlockListTransactionsAddr)
	}

	// enable transactions allow list (if any)
	if e.config.BridgeAllowList != nil {
		txn.bridgeAllowList = addresslist.NewAddressList(txn, contracts.AllowListBridgeAddr)
	}

	if e.config.BridgeBlockList != nil {
		txn.bridgeBlockList = addresslist.NewAddressList(txn, contracts.BlockListBridgeAddr)
	}

	return txn, nil
}

type Transition struct {
	logger hclog.Logger

	// dummy
	auxState State
	snap     Snapshot

	config  chain.ForksInTime
	state   *Txn
	getHash GetHashByNumber
	ctx     runtime.TxContext
	gasPool uint64

	// result
	receipts     []*types.Receipt
	totalGas     uint64
	donationFee  *big.Int
	validatorFee *big.Int
	burnedFee    *big.Int
	PostHook     func(t *Transition)

	// runtimes
	evm         *evm.EVM
	precompiles *precompiled.Precompiled

	// allow list runtimes
	deploymentAllowList *addresslist.AddressList
	deploymentBlockList *addresslist.AddressList
	txnAllowList        *addresslist.AddressList
	txnBlockList        *addresslist.AddressList
	bridgeAllowList     *addresslist.AddressList
	bridgeBlockList     *addresslist.AddressList
}

func NewTransition(config chain.ForksInTime, snap Snapshot, radix *Txn) *Transition {
	return &Transition{
		config:      config,
		state:       radix,
		snap:        snap,
		evm:         evm.NewEVM(),
		precompiles: precompiled.NewPrecompiled(),
	}
}

func (t *Transition) WithStateOverride(override types.StateOverride) error {
	for addr, o := range override {
		if o.State != nil && o.StateDiff != nil {
			return fmt.Errorf("cannot override both state and state diff")
		}

		if o.Nonce != nil {
			t.state.SetNonce(addr, *o.Nonce)
		}

		if o.Balance != nil {
			t.state.SetBalance(addr, o.Balance)
		}

		if o.Code != nil {
			t.state.SetCode(addr, o.Code)
		}

		if o.State != nil {
			t.state.SetFullStorage(addr, o.State)
		}

		for k, v := range o.StateDiff {
			t.state.SetState(addr, k, v)
		}
	}

	return nil
}

func (t *Transition) TotalGas() uint64 {
	return t.totalGas
}

func (t *Transition) Receipts() []*types.Receipt {
	return t.receipts
}

var emptyFrom = types.Address{}

// Write writes another transaction to the executor
func (t *Transition) Write(txn *types.Transaction) error {
	var err error

	if txn.From == emptyFrom && txn.Type != types.StateTx {
		// Decrypt the from address
		signer := crypto.NewSigner(t.config, uint64(t.ctx.ChainID))

		txn.From, err = signer.Sender(txn)
		if err != nil {
			return NewTransitionApplicationError(err, false)
		}
	}

	// Make a local copy and apply the transaction
	msg := txn.Copy()

	result, e := t.Apply(msg)
	if e != nil {
		t.logger.Error("failed to apply tx", "err", e)

		return e
	}

	t.totalGas += result.GasUsed

	topics := []types.Hash{
		Keccak256Hash([]byte("XGRFeeSplit(uint256,uint256,uint256)")),
	}

	data := append(
		LeftPadBytes(t.donationFee.Bytes(), 32),
		append(
			LeftPadBytes(t.validatorFee.Bytes(), 32),
			LeftPadBytes(t.burnedFee.Bytes(), 32)...,
		)...,
	)

	myLog := &types.Log{
		Address:     HexToAddress("0x000000000000000000000000000000000000fEE1"),
		Topics:      topics,
		Data:        data,
		BlockNumber: uint64(t.ctx.Number),
		TxHash:      txn.Hash,
	}

	logs := t.state.Logs()
	logs = append(logs, myLog)

	receipt := &types.Receipt{
		CumulativeGasUsed: t.totalGas,
		TransactionType:   txn.Type,
		TxHash:            txn.Hash,
		GasUsed:           result.GasUsed,
	}

	// The suicided accounts are set as deleted for the next iteration
	if err := t.state.CleanDeleteObjects(true); err != nil {
		return fmt.Errorf("failed to clean deleted objects: %w", err)
	}

	if result.Failed() {
		receipt.SetStatus(types.ReceiptFailed)
	} else {
		receipt.SetStatus(types.ReceiptSuccess)
	}

	// if the transaction created a contract, store the creation address in the receipt.
	if msg.To == nil {
		receipt.ContractAddress = crypto.CreateAddress(msg.From, txn.Nonce).Ptr()
	}

	// Set the receipt logs and create a bloom for filtering
	receipt.Logs = logs
	receipt.LogsBloom = types.CreateBloom([]*types.Receipt{receipt})
	t.receipts = append(t.receipts, receipt)

	return nil
}

// Commit commits the final result
func (t *Transition) Commit() (Snapshot, types.Hash, error) {
	objs, err := t.state.Commit(t.config.EIP155)
	if err != nil {
		return nil, types.ZeroHash, err
	}

	s2, root, err := t.snap.Commit(objs)
	if err != nil {
		return nil, types.ZeroHash, err
	}

	return s2, types.BytesToHash(root), nil
}

func (t *Transition) subGasPool(amount uint64) error {
	if t.gasPool < amount {
		return ErrBlockLimitReached
	}

	t.gasPool -= amount

	return nil
}

func (t *Transition) addGasPool(amount uint64) {
	t.gasPool += amount
}

func (t *Transition) Txn() *Txn {
	return t.state
}

// Apply applies a new transaction
func (t *Transition) Apply(msg *types.Transaction) (*runtime.ExecutionResult, error) {
	s := t.state.Snapshot()

	result, err := t.apply(msg)
	if err != nil {
		if revertErr := t.state.RevertToSnapshot(s); revertErr != nil {
			return nil, revertErr
		}
	}

	if t.PostHook != nil {
		t.PostHook(t)
	}

	return result, err
}

// ContextPtr returns reference of context
// This method is called only by test
func (t *Transition) ContextPtr() *runtime.TxContext {
	return &t.ctx
}

func (t *Transition) subGasLimitPrice(msg *types.Transaction) error {
	upfrontGasCost := GetLondonFixHandler(uint64(t.ctx.Number)).getUpfrontGasCost(msg, t.ctx.BaseFee)

	if err := t.state.SubBalance(msg.From, upfrontGasCost); err != nil {
		if errors.Is(err, runtime.ErrNotEnoughFunds) {
			return ErrNotEnoughFundsForGas
		}

		return err
	}

	return nil
}

func (t *Transition) nonceCheck(msg *types.Transaction) error {
	nonce := t.state.GetNonce(msg.From)

	if nonce != msg.Nonce {
		return ErrNonceIncorrect
	}

	return nil
}

// checkDynamicFees checks correctness of the EIP-1559 feature-related fields.
// Basically, makes sure gas tip cap and gas fee cap are good for dynamic and legacy transactions
func (t *Transition) checkDynamicFees(msg *types.Transaction) error {
	return GetLondonFixHandler(uint64(t.ctx.Number)).checkDynamicFees(msg, t)
}

// errors that can originate in the consensus rules checks of the apply method below
// surfacing of these errors reject the transaction thus not including it in the block

var (
	ErrNonceIncorrect          = errors.New("incorrect nonce")
	ErrNotEnoughFundsForGas    = errors.New("not enough funds to cover gas costs")
	ErrBlockLimitReached       = errors.New("gas limit reached in the pool")
	ErrIntrinsicGasOverflow    = errors.New("overflow in intrinsic gas calculation")
	ErrNotEnoughIntrinsicGas   = errors.New("not enough gas supplied for intrinsic gas costs")
	ErrMaxInitCodeSizeExceeded = errors.New("max initcode size exceeded")
	ErrTxTypeNotSupported      = errors.New("transaction type not supported for current fork")
	ErrTypedTxNotAllowed       = errors.New("typed transactions not allowed before txHashWithType fork")
	ErrGasPriceNotSet          = errors.New("gas price is not set")

	// ErrTipAboveFeeCap is a sanity error to ensure no one is able to specify a
	// transaction with a tip higher than the total fee cap.
	ErrTipAboveFeeCap = errors.New("max priority fee per gas higher than max fee per gas")

	// ErrTipVeryHigh is a sanity error to avoid extremely big numbers specified
	// in the tip field.
	ErrTipVeryHigh = errors.New("max priority fee per gas higher than 2^256-1")

	// ErrFeeCapVeryHigh is a sanity error to avoid extremely big numbers specified
	// in the fee cap field.
	ErrFeeCapVeryHigh = errors.New("max fee per gas higher than 2^256-1")

	// ErrFeeCapTooLow is returned if the transaction fee cap is less than the
	// the base fee of the block.
	ErrFeeCapTooLow = errors.New("max fee per gas less than block base fee")

	// ErrNonceUintOverflow is returned if uint64 overflow happens
	ErrNonceUintOverflow = errors.New("nonce uint64 overflow")
)

type TransitionApplicationError struct {
	Err           error
	IsRecoverable bool // Should the transaction be discarded, or put back in the queue.
}

func (e *TransitionApplicationError) Error() string {
	return e.Err.Error()
}

func NewTransitionApplicationError(err error, isRecoverable bool) *TransitionApplicationError {
	return &TransitionApplicationError{
		Err:           err,
		IsRecoverable: isRecoverable,
	}
}

type GasLimitReachedTransitionApplicationError struct {
	TransitionApplicationError
}

func NewGasLimitReachedTransitionApplicationError(err error) *GasLimitReachedTransitionApplicationError {
	return &GasLimitReachedTransitionApplicationError{
		*NewTransitionApplicationError(err, true),
	}
}

func (t *Transition) apply(msg *types.Transaction) (*runtime.ExecutionResult, error) {
	var err error

	if msg.Type == types.StateTx {
		err = checkAndProcessStateTx(msg)
	} else {
		err = checkAndProcessTx(msg, t)
	}

	if err != nil {
		return nil, err
	}

	// the amount of gas required is available in the block
	if err = t.subGasPool(msg.Gas); err != nil {
		return nil, NewGasLimitReachedTransitionApplicationError(err)
	}

	if t.ctx.Tracer != nil {
		t.ctx.Tracer.TxStart(msg.Gas)
	}

	// 4. there is no overflow when calculating intrinsic gas
	intrinsicGasCost, err := TransactionGasCost(
		msg,
		t.config.Homestead,
		t.config.Istanbul,
		t.config.EIP3860,
		t.config.EIP2930,
	)
	if err != nil {
		return nil, NewTransitionApplicationError(err, false)
	}

	// the purchased gas is enough to cover intrinsic usage
	gasLeft := msg.Gas - intrinsicGasCost
	// because we are working with unsigned integers for gas, the `>` operator is used instead of the more intuitive `<`
	if gasLeft > msg.Gas {
		return nil, NewTransitionApplicationError(ErrNotEnoughIntrinsicGas, false)
	}

	gasPrice := msg.GetGasPrice(t.ctx.BaseFee.Uint64())
	value := new(big.Int).Set(msg.Value)
	// set the specific transaction fields in the context
	t.ctx.GasPrice = types.BytesToHash(gasPrice.Bytes())
	t.ctx.Origin = msg.From

	// EIP-2929 (Berlin): initialize per-tx access list (warm set)
	if t.config.EIP2929 {
		init := []types.Address{msg.From}
		if msg.IsContractCreation() {
			// tx "to" for creations is the created address
			init = append(init, crypto.CreateAddress(msg.From, msg.Nonce))
		} else if msg.To != nil {
			init = append(init, *msg.To)
		}
		if t.config.EIP3651 {
			init = append(init, t.ctx.Coinbase)
		}
		t.ctx.AccessList = runtime.NewAccessList(init...)

		if t.config.EIP2930 && len(msg.AccessList) > 0 {
			for _, tuple := range msg.AccessList {
				t.ctx.AccessList.AddAddress(tuple.Address)
				for _, key := range tuple.StorageKeys {
					t.ctx.AccessList.AddSlot(tuple.Address, key)
				}
			}
		}
	} else {
		t.ctx.AccessList = nil
	}

	var result *runtime.ExecutionResult
	if msg.IsContractCreation() {
		result = t.Create2(msg.From, msg.Input, value, gasLeft)
	} else {
		if err := t.state.IncrNonce(msg.From); err != nil {
			return nil, err
		}
		result = t.Call2(msg.From, *msg.To, msg.Input, value, gasLeft)
	}

	refund := t.state.GetRefund()
	result.UpdateGasUsed(msg.Gas, refund)

	if t.ctx.Tracer != nil {
		t.ctx.Tracer.TxEnd(result.GasLeft)
	}

	// Refund the sender
	remaining := new(big.Int).Mul(new(big.Int).SetUint64(result.GasLeft), gasPrice)
	t.state.AddBalance(msg.From, remaining)

	// Berechne gesamte Fee = gasUsed × gasPrice
	totalFeeRaw := new(big.Int).Mul(new(big.Int).SetUint64(result.GasUsed), gasPrice)

	// ziehe Burning Betrag ab (clamped; niemals negative Fees erzeugen)
	burned := big.NewInt(0).Mul(big.NewInt(1000), big.NewInt(1_000_000_000))
	burnedApplied := new(big.Int).Set(burned)
	totalFee := new(big.Int).Set(totalFeeRaw)
	if totalFee.Cmp(burnedApplied) <= 0 {
		// Fee reicht nicht für den fixen Burn -> alles geht an Burn, Rest = 0
		burnedApplied.Set(totalFee)
		totalFee.SetInt64(0)
	} else {
		totalFee.Sub(totalFee, burnedApplied)
	}

	// Lade optionale Konfigurationen aus dem State
	burnedAddr := chain.DefaultBurnedAddress
	donationAddr := chain.DefaultDonationAddress
	donationPercent := chain.DefaultDonationPercent

	// Donation config analog minBaseFee: read from EngineRegistry storage slots (if deployed).
	// If registry is missing (address==0 or code-size==0), keep DefaultDonation*.
	if chain.EngineRegistryAddress != (types.Address{}) {
		if code := t.state.GetCode(chain.EngineRegistryAddress); len(code) > 0 {
			// donationAddress: address is right-aligned in last 20 bytes of the slot
			addrSlot := t.state.GetState(chain.EngineRegistryAddress, chain.EngineRegistrySlotKeyDonationAddress())
			var regAddr types.Address
			copy(regAddr[:], addrSlot[12:32])
			// donationPercent: uint256 (accept 0..100)
			pctSlot := t.state.GetState(chain.EngineRegistryAddress, chain.EngineRegistrySlotKeyDonationPercent())
			pct := new(big.Int).SetBytes(pctSlot[:])
			if pct.Sign() >= 0 && pct.BitLen() <= 64 {
				p := pct.Uint64()
				if p <= 100 {
					donationPercent = p
				}
			}
			// Safety: if address is zero => donation disabled
			if regAddr == types.ZeroAddress {
				donationPercent = 0
			} else {
				donationAddr = regAddr
			}
		}
	}

	// Berechne Aufteilung: Donation + Validator
	donation := new(big.Int).Mul(totalFee, new(big.Int).SetUint64(donationPercent))
	donation.Div(donation, big.NewInt(100))
	if donation.Sign() < 0 {
		donation.SetInt64(0)
	}
	if donation.Cmp(totalFee) > 0 {
		donation.Set(totalFee)
	}
	validator := new(big.Int).Sub(totalFee, donation)
	if validator.Sign() < 0 {
		validator.SetInt64(0)
	}
	// Verteile Fee
	if donation.Sign() > 0 {
		t.state.AddBalance(donationAddr, donation)
	}
	if validator.Sign() > 0 {
		t.state.AddBalance(t.ctx.Coinbase, validator)
	}
	if burnedApplied.Sign() > 0 {
		t.state.AddBalance(burnedAddr, burnedApplied)
	}
	t.donationFee = donation
	t.validatorFee = validator
	t.burnedFee = burnedApplied
	// return gas to the pool
	t.addGasPool(result.GasLeft)
	return result, nil
}

func (t *Transition) Create2(
	caller types.Address,
	code []byte,
	value *big.Int,
	gas uint64,
) *runtime.ExecutionResult {
	address := crypto.CreateAddress(caller, t.state.GetNonce(caller))
	contract := runtime.NewContractCreation(1, caller, caller, address, value, gas, code)

	return t.applyCreate(contract, t)
}

func (t *Transition) Call2(
	caller types.Address,
	to types.Address,
	input []byte,
	value *big.Int,
	gas uint64,
) *runtime.ExecutionResult {
	c := runtime.NewContractCall(1, caller, caller, to, value, gas, t.state.GetCode(to), input)

	return t.applyCall(c, runtime.Call, t)
}

func (t *Transition) run(contract *runtime.Contract, host runtime.Host) *runtime.ExecutionResult {
	if result := t.handleAllowBlockListsUpdate(contract, host); result != nil {
		return result
	}

	// check txns access lists, allow list takes precedence over block list
	if t.txnAllowList != nil {
		if contract.Caller != contracts.SystemCaller {
			role := t.txnAllowList.GetRole(contract.Caller)
			if !role.Enabled() {
				t.logger.Debug(
					"Failing transaction. Caller is not in the transaction allowlist",
					"contract.Caller", contract.Caller,
					"contract.Address", contract.Address,
				)

				return &runtime.ExecutionResult{
					GasLeft: 0,
					Err:     runtime.ErrNotAuth,
				}
			}
		}
	} else if t.txnBlockList != nil {
		if contract.Caller != contracts.SystemCaller {
			role := t.txnBlockList.GetRole(contract.Caller)
			if role == addresslist.EnabledRole {
				t.logger.Debug(
					"Failing transaction. Caller is in the transaction blocklist",
					"contract.Caller", contract.Caller,
					"contract.Address", contract.Address,
				)

				return &runtime.ExecutionResult{
					GasLeft: 0,
					Err:     runtime.ErrNotAuth,
				}
			}
		}
	}

	// check the precompiles
	if t.precompiles.CanRun(contract, host, &t.config) {
		return t.precompiles.Run(contract, host, &t.config)
	}
	// check the evm
	if t.evm.CanRun(contract, host, &t.config) {
		return t.evm.Run(contract, host, &t.config)
	}

	return &runtime.ExecutionResult{
		Err: fmt.Errorf("runtime not found"),
	}
}

func (t *Transition) Transfer(from, to types.Address, amount *big.Int) error {
	if amount == nil {
		return nil
	}

	if err := t.state.SubBalance(from, amount); err != nil {
		if errors.Is(err, runtime.ErrNotEnoughFunds) {
			return runtime.ErrInsufficientBalance
		}

		return err
	}

	t.state.AddBalance(to, amount)

	return nil
}

func (t *Transition) applyCall(
	c *runtime.Contract,
	callType runtime.CallType,
	host runtime.Host,
) *runtime.ExecutionResult {
	if c.Depth > int(1024)+1 {
		return &runtime.ExecutionResult{
			GasLeft: c.Gas,
			Err:     runtime.ErrDepth,
		}
	}

	snapshot := t.state.Snapshot()
	var accessListSnap *runtime.AccessList
	if t.config.EIP2929 && t.ctx.AccessList != nil {
		accessListSnap = t.ctx.AccessList.Copy()
	}
	t.state.TouchAccount(c.Address)

	if callType == runtime.Call {
		// Transfers only allowed on calls
		if err := t.Transfer(c.Caller, c.Address, c.Value); err != nil {
			// Revert state + access list snapshot (scope revert)
			if revertErr := t.state.RevertToSnapshot(snapshot); revertErr != nil {
				return &runtime.ExecutionResult{
					GasLeft: c.Gas,
					Err:     revertErr,
				}
			}
			if accessListSnap != nil {
				t.ctx.AccessList.RevertTo(accessListSnap)
			}
			return &runtime.ExecutionResult{
				GasLeft: c.Gas,
				Err:     err,
			}
		}
	}

	var result *runtime.ExecutionResult

	t.captureCallStart(c, callType)

	result = t.run(c, host)
	if result.Failed() {
		if err := t.state.RevertToSnapshot(snapshot); err != nil {
			return &runtime.ExecutionResult{
				GasLeft: c.Gas,
				Err:     err,
			}
		}
		// EIP-2929: access list must revert to state before this call frame
		if accessListSnap != nil {
			t.ctx.AccessList.RevertTo(accessListSnap)
		}
	}

	t.captureCallEnd(c, result)

	return result
}

func (t *Transition) hasCodeOrNonce(addr types.Address) bool {
	if t.state.GetNonce(addr) != 0 {
		return true
	}

	codeHash := t.state.GetCodeHash(addr)

	return codeHash != types.EmptyCodeHash && codeHash != types.ZeroHash
}

func (t *Transition) applyCreate(c *runtime.Contract, host runtime.Host) *runtime.ExecutionResult {
	gasLimit := c.Gas

	if c.Depth > int(1024)+1 {
		return &runtime.ExecutionResult{
			GasLeft: gasLimit,
			Err:     runtime.ErrDepth,
		}
	}

	// Increment the nonce of the caller
	if err := t.state.IncrNonce(c.Caller); err != nil {
		return &runtime.ExecutionResult{Err: err}
	}

	// Check if there is a collision and the address already exists
	if t.hasCodeOrNonce(c.Address) {
		return &runtime.ExecutionResult{
			GasLeft: 0,
			Err:     runtime.ErrContractAddressCollision,
		}
	}

	// Take snapshot of the current state
	snapshot := t.state.Snapshot()
	var accessListSnap *runtime.AccessList
	if t.config.EIP2929 && t.ctx.AccessList != nil {
		accessListSnap = t.ctx.AccessList.Copy()
	}

	// EIP-3860: oversize initcode for CREATE/CREATE2 opcodes is an exceptional abort (OOG).
	// Contract-creation transactions are rejected in checkAndProcessTx (consensus), not here.
	if t.config.EIP3860 && c.Depth > 1 && len(c.Code) > TxPoolMaxInitCodeSize {
		if accessListSnap != nil {
			t.ctx.AccessList.RevertTo(accessListSnap)
		}
		return &runtime.ExecutionResult{
			GasLeft: 0,
			Err:     runtime.ErrOutOfGas,
		}
	}
	if t.config.EIP158 {
		// Force the creation of the account
		t.state.CreateAccount(c.Address)

		if err := t.state.IncrNonce(c.Address); err != nil {
			return &runtime.ExecutionResult{Err: err}
		}
	}

	// Transfer the value
	if err := t.Transfer(c.Caller, c.Address, c.Value); err != nil {
		return &runtime.ExecutionResult{
			GasLeft: gasLimit,
			Err:     err,
		}
	}

	var result *runtime.ExecutionResult

	t.captureCallStart(c, evm.CREATE)

	defer func() {
		// pass result to be set later
		t.captureCallEnd(c, result)
	}()

	// check if contract creation allow list is enabled
	if t.deploymentAllowList != nil {
		role := t.deploymentAllowList.GetRole(c.Caller)

		if !role.Enabled() {
			t.logger.Debug(
				"Failing contract deployment. Caller is not in the deployment allowlist",
				"contract.Caller", c.Caller,
				"contract.Address", c.Address,
			)

			return &runtime.ExecutionResult{
				GasLeft: 0,
				Err:     runtime.ErrNotAuth,
			}
		}
	} else if t.deploymentBlockList != nil {
		role := t.deploymentBlockList.GetRole(c.Caller)

		if role == addresslist.EnabledRole {
			t.logger.Debug(
				"Failing contract deployment. Caller is in the deployment blocklist",
				"contract.Caller", c.Caller,
				"contract.Address", c.Address,
			)

			return &runtime.ExecutionResult{
				GasLeft: 0,
				Err:     runtime.ErrNotAuth,
			}
		}
	}

	result = t.run(c, host)
	if result.Failed() {
		if err := t.state.RevertToSnapshot(snapshot); err != nil {
			return &runtime.ExecutionResult{
				Err: err,
			}
		}
		if accessListSnap != nil {
			t.ctx.AccessList.RevertTo(accessListSnap)
		}

		return result
	}

	if t.config.EIP158 && len(result.ReturnValue) > SpuriousDragonMaxCodeSize {
		// Contract size exceeds 'SpuriousDragon' size limit
		if err := t.state.RevertToSnapshot(snapshot); err != nil {
			return &runtime.ExecutionResult{
				Err: err,
			}
		}
		if accessListSnap != nil {
			t.ctx.AccessList.RevertTo(accessListSnap)
		}

		return &runtime.ExecutionResult{
			GasLeft: 0,
			Err:     runtime.ErrMaxCodeSizeExceeded,
		}
	}

	gasCost := uint64(len(result.ReturnValue)) * 200

	if result.GasLeft < gasCost {
		result.Err = runtime.ErrCodeStoreOutOfGas
		result.ReturnValue = nil

		// Out of gas creating the contract
		if t.config.Homestead {
			if err := t.state.RevertToSnapshot(snapshot); err != nil {
				return &runtime.ExecutionResult{
					Err: err,
				}
			}
			if accessListSnap != nil {
				t.ctx.AccessList.RevertTo(accessListSnap)
			}

			result.GasLeft = 0
		}

		return result
	}

	result.GasLeft -= gasCost
	result.Address = c.Address
	t.state.SetCode(c.Address, result.ReturnValue)

	return result
}

func (t *Transition) handleAllowBlockListsUpdate(contract *runtime.Contract,
	host runtime.Host) *runtime.ExecutionResult {
	// check contract deployment allow list (if any)
	if t.deploymentAllowList != nil && t.deploymentAllowList.Addr() == contract.CodeAddress {
		return t.deploymentAllowList.Run(contract, host, &t.config)
	}

	// check contract deployment block list (if any)
	if t.deploymentBlockList != nil && t.deploymentBlockList.Addr() == contract.CodeAddress {
		return t.deploymentBlockList.Run(contract, host, &t.config)
	}

	// check bridge allow list (if any)
	if t.bridgeAllowList != nil && t.bridgeAllowList.Addr() == contract.CodeAddress {
		return t.bridgeAllowList.Run(contract, host, &t.config)
	}

	// check bridge block list (if any)
	if t.bridgeBlockList != nil && t.bridgeBlockList.Addr() == contract.CodeAddress {
		return t.bridgeBlockList.Run(contract, host, &t.config)
	}

	// check transaction allow list (if any)
	if t.txnAllowList != nil && t.txnAllowList.Addr() == contract.CodeAddress {
		return t.txnAllowList.Run(contract, host, &t.config)
	}

	// check transaction block list (if any)
	if t.txnBlockList != nil && t.txnBlockList.Addr() == contract.CodeAddress {
		return t.txnBlockList.Run(contract, host, &t.config)
	}

	return nil
}

func (t *Transition) SetState(addr types.Address, key types.Hash, value types.Hash) {
	t.state.SetState(addr, key, value)
}

func (t *Transition) SetStorage(
	addr types.Address,
	key types.Hash,
	value types.Hash,
	config *chain.ForksInTime,
) runtime.StorageStatus {
	return t.state.SetStorage(addr, key, value, config)
}

func (t *Transition) GetTxContext() runtime.TxContext {
	return t.ctx
}

func (t *Transition) GetBlockHash(number int64) (res types.Hash) {
	return t.getHash(uint64(number))
}

func (t *Transition) EmitLog(addr types.Address, topics []types.Hash, data []byte) {
	t.state.EmitLog(addr, topics, data)
}

func (t *Transition) GetCodeSize(addr types.Address) int {
	return t.state.GetCodeSize(addr)
}

func (t *Transition) GetCodeHash(addr types.Address) (res types.Hash) {
	return t.state.GetCodeHash(addr)
}

func (t *Transition) GetCode(addr types.Address) []byte {
	return t.state.GetCode(addr)
}

func (t *Transition) GetBalance(addr types.Address) *big.Int {
	return t.state.GetBalance(addr)
}

func (t *Transition) GetStorage(addr types.Address, key types.Hash) types.Hash {
	return t.state.GetState(addr, key)
}

func (t *Transition) AccountExists(addr types.Address) bool {
	return t.state.Exist(addr)
}

func (t *Transition) Empty(addr types.Address) bool {
	return t.state.Empty(addr)
}

func (t *Transition) GetNonce(addr types.Address) uint64 {
	return t.state.GetNonce(addr)
}

func (t *Transition) Selfdestruct(addr types.Address, beneficiary types.Address) {
	if !t.state.HasSuicided(addr) {
		t.state.AddRefund(24000)
	}

	t.state.AddBalance(beneficiary, t.state.GetBalance(addr))
	t.state.Suicide(addr)
}

func (t *Transition) Callx(c *runtime.Contract, h runtime.Host) *runtime.ExecutionResult {
	if c.Type == runtime.Create {
		return t.applyCreate(c, h)
	}

	return t.applyCall(c, c.Type, h)
}

// SetAccountDirectly sets an account to the given address
// NOTE: SetAccountDirectly changes the world state without a transaction
func (t *Transition) SetAccountDirectly(addr types.Address, account *chain.GenesisAccount) error {
	if t.AccountExists(addr) {
		return fmt.Errorf("can't add account to %+v because an account exists already", addr)
	}

	t.state.SetCode(addr, account.Code)

	for key, value := range account.Storage {
		t.state.SetStorage(addr, key, value, &t.config)
	}

	t.state.SetBalance(addr, account.Balance)
	t.state.SetNonce(addr, account.Nonce)

	return nil
}

// SetCodeDirectly sets new code into the account with the specified address
// NOTE: SetCodeDirectly changes the world state without a transaction
func (t *Transition) SetCodeDirectly(addr types.Address, code []byte) error {
	if !t.AccountExists(addr) {
		return fmt.Errorf("account doesn't exist at %s", addr)
	}

	t.state.SetCode(addr, code)

	return nil
}

// SetNonPayable deactivates the check of tx cost against tx executor balance.
func (t *Transition) SetNonPayable(nonPayable bool) {
	t.ctx.NonPayable = nonPayable
}

// SetTracer sets tracer to the context in order to enable it
func (t *Transition) SetTracer(tracer tracer.Tracer) {
	t.ctx.Tracer = tracer
}

// GetTracer returns a tracer in context
func (t *Transition) GetTracer() runtime.VMTracer {
	return t.ctx.Tracer
}

func (t *Transition) GetRefund() uint64 {
	return t.state.GetRefund()
}

func TransactionGasCost(
	msg *types.Transaction,
	isHomestead,
	isIstanbul,
	isEIP3860,
	isEIP2930 bool,
) (uint64, error) {
	cost := uint64(0)

	// Contract creation is only paid on the homestead fork
	if msg.IsContractCreation() && isHomestead {
		cost += TxGasContractCreation
	} else {
		cost += TxGas
	}

	payload := msg.Input
	if len(payload) > 0 {
		zeros := uint64(0)

		for i := 0; i < len(payload); i++ {
			if payload[i] == 0 {
				zeros++
			}
		}

		nonZeros := uint64(len(payload)) - zeros
		nonZeroCost := uint64(68)

		if isIstanbul {
			nonZeroCost = 16
		}

		if (math.MaxUint64-cost)/nonZeroCost < nonZeros {
			return 0, ErrIntrinsicGasOverflow
		}

		cost += nonZeros * nonZeroCost

		if (math.MaxUint64-cost)/4 < zeros {
			return 0, ErrIntrinsicGasOverflow
		}

		cost += zeros * 4
	}

	if msg.IsContractCreation() && isEIP3860 {
		words := (uint64(len(payload)) + 31) / 32
		if (math.MaxUint64-cost)/2 < words {
			return 0, ErrIntrinsicGasOverflow
		}

		cost += words * 2
	}

	if isEIP2930 && (msg.Type == types.AccessListTx || msg.Type == types.DynamicFeeTx) {
		var addrCount, slotCount uint64
		for _, tuple := range msg.AccessList {
			addrCount++
			slotCount += uint64(len(tuple.StorageKeys))
		}

		cost += addrCount*TxAccessListAddressGas + slotCount*TxAccessListStorageKeyGas
	}

	return cost, nil
}

// checkAndProcessTx - first check if this message satisfies all consensus rules before
// applying the message. The rules include these clauses:
// 1. the nonce of the message caller is correct
// 2. caller has enough balance to cover transaction fee(gaslimit * gasprice * val) or fee(gasfeecap * gasprice * val)
func checkAndProcessTx(msg *types.Transaction, t *Transition) error {
	// ---------------------------------------------------------------------
	// Fork / type gating (CONSENSUS CRITICAL)
	// ---------------------------------------------------------------------
	if msg.Type == types.AccessListTx {
		if !t.config.EIP2930 {
			return NewTransitionApplicationError(ErrTxTypeNotSupported, true)
		}
		// Polygon Edge historically gates typed-tx hashes behind TxHashWithType.
		// TxPool checks are not sufficient for block validation.
		if !t.config.TxHashWithType {
			return NewTransitionApplicationError(ErrTypedTxNotAllowed, true)
		}
	}

	// GasPrice is mandatory for LegacyTx + AccessListTx (and any other non-1559 tx).
	// This avoids panics in GetGasPrice()/Cost() paths and rejects invalid blocks.
	if msg.Type != types.DynamicFeeTx && msg.Type != types.StateTx && msg.GasPrice == nil {
		return NewTransitionApplicationError(ErrGasPriceNotSet, true)
	}
	// EIP-3860 (Shanghai): contract-creation *transactions* with oversized initcode are invalid
	// and must be rejected by consensus before execution.
	// NOTE: For CREATE/CREATE2 opcodes the oversize rule is handled as an exceptional abort (OOG).
	if t.config.EIP3860 && msg.IsContractCreation() && len(msg.Input) > TxPoolMaxInitCodeSize {
		return NewTransitionApplicationError(ErrMaxInitCodeSizeExceeded, true)
	}
	// 1. the nonce of the message caller is correct
	if err := t.nonceCheck(msg); err != nil {
		return NewTransitionApplicationError(err, true)
	}

	if !t.ctx.NonPayable {
		// 2. check dynamic fees of the transaction
		if err := t.checkDynamicFees(msg); err != nil {
			return NewTransitionApplicationError(err, true)
		}

		// 3. caller has enough balance to cover transaction
		// Skip this check if the given flag is provided.
		// It happens for eth_call and for other operations that do not change the state.
		if err := t.subGasLimitPrice(msg); err != nil {
			return NewTransitionApplicationError(err, true)
		}
	}

	return nil
}

func checkAndProcessStateTx(msg *types.Transaction) error {
	if msg.GasPrice.Cmp(big.NewInt(0)) != 0 {
		return NewTransitionApplicationError(
			errors.New("gasPrice of state transaction must be zero"),
			true,
		)
	}

	if msg.Gas != types.StateTransactionGasLimit {
		return NewTransitionApplicationError(
			fmt.Errorf("gas of state transaction must be %d", types.StateTransactionGasLimit),
			true,
		)
	}

	if msg.From != contracts.SystemCaller {
		return NewTransitionApplicationError(
			fmt.Errorf("state transaction sender must be %v, but got %v", contracts.SystemCaller, msg.From),
			true,
		)
	}

	if msg.To == nil || *msg.To == types.ZeroAddress {
		return NewTransitionApplicationError(
			errors.New("to of state transaction must be specified"),
			true,
		)
	}

	return nil
}

// captureCallStart calls CallStart in Tracer if context has the tracer
func (t *Transition) captureCallStart(c *runtime.Contract, callType runtime.CallType) {
	if t.ctx.Tracer == nil {
		return
	}

	t.ctx.Tracer.CallStart(
		c.Depth,
		c.Caller,
		c.Address,
		int(callType),
		c.Gas,
		c.Value,
		c.Input,
	)
}

// captureCallEnd calls CallEnd in Tracer if context has the tracer
func (t *Transition) captureCallEnd(c *runtime.Contract, result *runtime.ExecutionResult) {
	if t.ctx.Tracer == nil {
		return
	}

	t.ctx.Tracer.CallEnd(
		c.Depth,
		result.ReturnValue,
		result.Err,
	)
}
