package precompiled

import (
	"bytes"
	"math/big"

	ethgo "github.com/umbracle/ethgo"
	ethabi "github.com/umbracle/ethgo/abi"
	"github.com/xgr-network/xgr-node/contracts/engineabi"

	"github.com/xgr-network/xgr-node/chain"
	"github.com/xgr-network/xgr-node/contracts"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/state/runtime"
	"github.com/xgr-network/xgr-node/types"
)

type engineExecute struct{ p *Precompiled }

var (
	slotNextPid = crypto.Keccak256([]byte("XGR:ENGINE:NEXT_PID"))
)

var getNextPidABI = ethabi.MustNewABI(engineabi.GetNextPidABI)
var isPidUsedABI = ethabi.MustNewABI(engineabi.IsPidUsedABI)
var engineABI = ethabi.MustNewABI(engineabi.ExecuteABI)

func authorizeEngineCaller(host runtime.Host, caller types.Address) (types.Address, bool) {
	reg := chain.EngineRegistryAddress
	if reg == (types.Address{}) {
		// Bootstrap mode (no registry address configured yet)
		if chain.BootstrapEngineEOA != (types.Address{}) && caller == chain.BootstrapEngineEOA {
			return caller, true
		}
		return types.Address{}, false
	}

	// Registry address configured, but contract might not be deployed yet (bootstrap blocks)
	if len(host.GetCode(reg)) == 0 {
		if chain.BootstrapEngineEOA != (types.Address{}) && caller == chain.BootstrapEngineEOA {
			return caller, true
		}
		return types.Address{}, false
	}

	// paused?
	if host.GetStorage(reg, chain.EngineRegistrySlotKeyPaused()) != (types.Hash{}) {
		return types.Address{}, false
	}

	// authorizedEngines[caller] == true ?
	k := chain.EngineRegistrySlotKeyAuthorizedEngine(caller)
	if host.GetStorage(reg, k) == (types.Hash{}) {
		return types.Address{}, false
	}

	return caller, true
}

// custom 32+20 key schema (slot ‖ addr[20])
func kNext(a ethgo.Address) types.Hash {
	var b [52]byte
	copy(b[:32], slotNextPid[:])
	copy(b[32:], a[:])
	return types.BytesToHash(crypto.Keccak256(b[:]))
}

func sloadU256(h runtime.Host, k types.Hash) *big.Int {
	v := h.GetStorage(contracts.EngineExecutePrecompile, k)
	if v == (types.Hash{}) {
		return nil
	}
	return new(big.Int).SetBytes(v[:])
}

func sstoreU256(h runtime.Host, k types.Hash, v *big.Int) {
	var buf [32]byte
	if v != nil {
		v.FillBytes(buf[:])
	}
	h.SetStorage(contracts.EngineExecutePrecompile, k, types.BytesToHash(buf[:]), &chain.ForksInTime{})
}

func encodeBool(b bool) []byte {
	if b {
		return abiBoolTrue
	}
	return abiBoolFalse
}

func pidUsed(host runtime.Host, user ethgo.Address, pid *big.Int) bool {
	if pid == nil || pid.Sign() <= 0 {
		return false
	}
	next := sloadU256(host, kNext(user))
	if next == nil || next.Sign() == 0 {
		next = big.NewInt(1)
	}
	return pid.Cmp(next) < 0
}

// Kein fixes Overhead mehr
const engineOverheadGas = uint64(0)

var engineMetaEvent = ethabi.MustNewABI(engineabi.EngineMetaEventABI).Events["EngineMeta"]

func EngineMetaEventABI() string { return engineabi.EngineMetaEventABI }

const engineExtrasEventABI = engineabi.EngineExtrasEventABI

var engineExtrasEvent = ethabi.MustNewABI(engineExtrasEventABI).Events["EngineExtrasV2"]

func EngineExtrasEventABI() string { return engineExtrasEventABI }

type inGrant struct {
	From, Engine, XRC729 ethgo.Address
	OstcId               string
	OstcHash             [32]byte
	ProcessId            *big.Int
	SessionId            *big.Int
	MaxTotalGas          *big.Int
	Expiry               *big.Int
	ChainId              *big.Int
}

type inCall struct {
	To                 ethgo.Address
	Data               []byte
	ValueWei           *big.Int
	GasLimit           uint64
	ValidationGas      uint64
	MaxFeePerGas       *big.Int
	Deadline           uint64
	GrantFeeSeconds    uint64
	GrantFeePerYearWei *big.Int
}

type inMeta struct {
	Iteration     uint64
	StepId        string
	RuleContract  ethgo.Address
	RuleHash      [32]byte
	Payload       []byte
	ApiSaves      []byte
	ContractSaves []byte
	Extras        []byte
}

// ---------- Gas-/Fee-Accounting (Single Source of Truth) -------------------
//
// WICHTIG:
// - Precompile.gas() rechnet NUR Precompile-Kosten (ohne TX-Base 21k und ohne calldata-cost).
// - Settlement/Preflight rechnet TX-Base + calldata-cost zusätzlich, weil der Engine-EOA diese bezahlt.
//
// Bausteine:
// - validationGas: deterministisch (explizit übergeben)
// - Logs: deterministisch über calcEngineMetaLen / calcEngineExtrasLen
// - CALL-Overhead: deterministisch aus len(call.Data)
// - execLimit: deterministisch (explizit übergeben)

func calldataCostUnits(b []byte) uint64 {
	var c uint64
	for _, x := range b {
		if x == 0 {
			c += 4
		} else {
			c += 16
		}
	}
	return c
}

func logCostUnits(topics int, dataLen int) uint64 {
	// 375 (base) + 375 * topics + 8 * len(data)
	return 375 + 375*uint64(topics) + 8*uint64(dataLen)
}

func callOverheadUnits(to ethgo.Address, gasLimit uint64, dataLen int) uint64 {
	if to == (ethgo.Address{}) || gasLimit == 0 {
		return 0
	}
	words := (uint64(dataLen) + 31) / 32
	return 700 + 2600 + 3*words
}

type feeCalc struct {
	calldata      uint64
	metaLen       int
	extrasLen     int
	callOverhead  uint64
	execLimit     uint64
	validationGas uint64
}

func calcFee(input []byte, grant inGrant, call inCall, meta inMeta) feeCalc {
	var exec uint64
	if call.To != (ethgo.Address{}) && call.GasLimit > 0 {
		exec = call.GasLimit
	}
	return feeCalc{
		calldata:      calldataCostUnits(input),
		metaLen:       calcEngineMetaLen(grant, meta),
		extrasLen:     calcEngineExtrasLen(meta),
		callOverhead:  callOverheadUnits(call.To, call.GasLimit, len(call.Data)),
		execLimit:     exec,
		validationGas: call.ValidationGas,
	}
}

func (f feeCalc) logUnits() uint64 {
	return logCostUnits(1, f.metaLen) + logCostUnits(1, f.extrasLen)
}

// Precompile-Gas (ohne TX-Base + calldata)
func (f feeCalc) precompileGasUnits() uint64 {
	return f.validationGas + f.logUnits() + f.callOverhead + f.execLimit + engineOverheadGas
}

// EVM-TX Units (TX-Base + calldata + Events + CALL-Overhead + execLimit), ohne validationGas
func (f feeCalc) evmTxUnits() uint64 {
	return 21_000 + f.calldata + f.logUnits() + f.callOverhead + f.execLimit + engineOverheadGas
}

// Gesamt für Abrechnung/Receipt-Summe (EVM + Validation)
func (f feeCalc) totalTxUnits() uint64 {
	return f.evmTxUnits() + f.validationGas
}

func (e *engineExecute) gas(input []byte, _ *chain.ForksInTime) uint64 {
	// Minimum (User-Wunsch): niemals 0 zurückgeben für ENGINE_EXECUTE, auch wenn Decode fehlschlägt.
	const minMalformedExecuteGas = uint64(21_000)
	if len(input) < 4 {
		return 0
	}
	if !bytes.Equal(input[:4], engineABI.GetMethod("ENGINE_EXECUTE").ID()) {
		return 0
	}
	vals, err := engineABI.GetMethod("ENGINE_EXECUTE").Inputs.Decode(input[4:])
	if err != nil {
		return minMalformedExecuteGas
	}
	args, ok := vals.(map[string]interface{})
	if !ok {
		return minMalformedExecuteGas
	}
	grantMap, ok := args["grant"].(map[string]interface{})
	if !ok {
		return minMalformedExecuteGas
	}
	callMap, ok := args["call"].(map[string]interface{})
	if !ok {
		return minMalformedExecuteGas
	}
	metaMap, ok := args["meta"].(map[string]interface{})
	if !ok {
		return minMalformedExecuteGas
	}

	grant := decodeGrant(grantMap)
	call := decodeCall(callMap)
	meta := decodeMeta(metaMap)

	fc := calcFee(input, grant, call, meta)
	return fc.precompileGasUnits()
}

func (e *engineExecute) run(input []byte, caller types.Address, host runtime.Host) ([]byte, error) {
	if len(input) < 4 {
		return nil, runtime.ErrInvalidInputData
	}
	selector := input[:4]

	if bytes.Equal(selector, getNextPidABI.GetMethod("ENGINE_GET_NEXT_PID").ID()) {
		return e.getNextPid(input, host)
	}
	if bytes.Equal(selector, isPidUsedABI.GetMethod("ENGINE_IS_PID_USED").ID()) {
		vals, err := isPidUsedABI.GetMethod("ENGINE_IS_PID_USED").Inputs.Decode(input[4:])
		if err != nil {
			return nil, runtime.ErrInvalidInputData
		}
		args := vals.(map[string]interface{})
		user := args["user"].(ethgo.Address)
		pid := args["pid"].(*big.Int)
		used := pidUsed(host, user, pid)
		return encodeBool(used), nil
	}
	if bytes.Equal(selector, engineABI.GetMethod("BILL_GRANTS_ONLY").ID()) {
		return e.billGrantsOnly(input, caller, host)
	}
	if !bytes.Equal(selector, engineABI.GetMethod("ENGINE_EXECUTE").ID()) {
		return nil, runtime.ErrInvalidInputData
	}
	vals, err := engineABI.GetMethod("ENGINE_EXECUTE").Inputs.Decode(input[4:])
	if err != nil {
		return nil, runtime.ErrInvalidInputData
	}
	args := vals.(map[string]interface{})
	gv := args["grant"].(map[string]interface{})
	cv := args["call"].(map[string]interface{})
	mv := args["meta"].(map[string]interface{})

	grant := decodeGrant(gv)
	call := decodeCall(cv)
	meta := decodeMeta(mv)

	fc := calcFee(input, grant, call, meta)

	// ---- Authorize caller: only the configured Engine EOA may invoke this precompile ----
	engine, ok := authorizeEngineCaller(host, caller)
	if !ok {
		return nil, runtime.ErrInvalidInputData
	}
	user := types.Address(grant.From) // kept for downstream logic; grant.Engine is ignored for auth

	if call.GrantFeeSeconds > 0 {
		fee, err := billGrants(host, user, engine, call.GrantFeeSeconds, call.GrantFeePerYearWei)
		if err != nil {
			return nil, err
		}
		logGrantFeeCharged(host, user, engine, call.GrantFeeSeconds, call.GrantFeePerYearWei, fee)
	}

	if grant.SessionId == nil || grant.SessionId.Sign() < 0 {
		return nil, runtime.ErrInvalidInputData
	}

	// --- Monotone Guard (einziger Wahrheitsanker: kNext) --------------------
	// Regeln:
	// - Neuer Root  : sessionId == kNext
	// - Follow-up   : sessionId <  kNext
	// - Reject      : sessionId >  kNext
	curNext := sloadU256(host, kNext(grant.From))
	if curNext == nil || curNext.Sign() == 0 {
		curNext = big.NewInt(1) // erste Session überhaupt
	}
	switch grant.SessionId.Cmp(curNext) {
	case 1:
		// versucht zu springen (größer als kNext)
		return nil, runtime.ErrInvalidInputData
	case 0:
		// neuer Root -> nach den Sicherheitsprüfungen wird kNext erhöht
	default:
		// follow-up (< curNext) -> ok
	}

	txTime := uint64(host.GetTxContext().Timestamp)

	// **SOFORT** persistieren, wenn dies ein neuer Root ist (sessionId == kNext)
	if grant.SessionId.Cmp(curNext) == 0 {
		p1 := new(big.Int).Add(grant.SessionId, big.NewInt(1))
		sstoreU256(host, kNext(grant.From), p1) // eine Wahrheit: next = current+1
	}
	// Deadline-Guard
	if call.Deadline != 0 && txTime > call.Deadline {
		return nil, runtime.ErrUnauthorizedCaller
	}

	// --- Tatsächlich bezahlter TX-Gaspreis (Source of Truth für Settlement) ---
	// In dieser Codebase ist TxContext.GasPrice ein uint256 als types.Hash (big-endian).
	txCtx := host.GetTxContext()
	paidWeiPerGas := new(big.Int).SetBytes(txCtx.GasPrice[:])
	if paidWeiPerGas.Sign() <= 0 {
		return nil, runtime.ErrInvalidInputData
	}
	// --- Gebühren-Guards ---------------------------------------------------
	// Semantik: fehlender/0er MaxFeePerGas => "erbt BaseFee" (falls vorhanden)
	if bf := txCtx.BaseFee; bf != nil {
		if call.MaxFeePerGas == nil || call.MaxFeePerGas.Sign() == 0 {
			// kein Cap übergeben -> auf tatsächlich bezahlten Preis heben
			// (sonst über-/unterverrechnen wir, sobald cap != effective price)
			call.MaxFeePerGas = new(big.Int).Set(paidWeiPerGas)
		} else if call.MaxFeePerGas.Cmp(bf) < 0 {
			// explizit gesetzter Cap < BaseFee
			return nil, runtime.ErrInvalidInputData
		}
	}
	// Wenn selbst nach dem Fallback noch nil/<=0 -> unzulässig
	if call.MaxFeePerGas == nil || call.MaxFeePerGas.Sign() <= 0 {
		return nil, runtime.ErrInvalidInputData
	}
	// Grant enthält kein MaxFeePerGas- oder PriorityFee-Feld mehr
	// Wenn kein Ziel, muss GasLimit 0 sein
	if (call.To == (ethgo.Address{})) && call.GasLimit != 0 {
		return nil, runtime.ErrInvalidInputData
	}

	// Settlement/Preflight immer mit dem tatsächlich bezahlten Preis (nicht mit dem Cap).
	effectiveWeiPerGas := paidWeiPerGas
	// ---------- Konservativer Preflight-Guthabencheck -----------------------
	// Ziel: spätere Erstattung darf NICHT mehr fehlschlagen → Engine bleibt
	// niemals auf Kosten sitzen (auch bei Reverts).
	//
	// Enthaltene Einheiten (SSOT):
	//   fc.totalTxUnits() + Value (+ ggf. GrantFee)
	worstWei := new(big.Int).Mul(new(big.Int).SetUint64(fc.totalTxUnits()), effectiveWeiPerGas)
	worstTotal := new(big.Int).Set(worstWei)
	worstTotal.Add(worstTotal, nz(call.ValueWei))
	// Include grant billing in worst-case balance check (ceil(seconds * perYear / YEAR))
	if call.GrantFeeSeconds > 0 {
		num := new(big.Int).Mul(nz(call.GrantFeePerYearWei), new(big.Int).SetUint64(call.GrantFeeSeconds))
		den := big.NewInt(31_536_000)
		// ceil
		num.Add(num, new(big.Int).Sub(den, big.NewInt(1)))
		grantFeeWei := new(big.Int).Div(num, den)
		worstTotal.Add(worstTotal, grantFeeWei)
	}
	if host.GetBalance(user).Cmp(worstTotal) < 0 {
		return nil, runtime.ErrNotEnoughFunds
	}
	var execResGasUsed uint64
	// Default: log-only (kein innerer CALL) als Fehler markieren
	success := false
	if call.GasLimit > 0 && (call.To != (ethgo.Address{})) {
		code := host.GetCode(types.Address(call.To))
		contract := runtime.NewContractCall(
			1,
			user,
			user,
			types.Address(call.To),
			nz(call.ValueWei),
			call.GasLimit,
			code,
			call.Data,
		)
		res := host.Callx(contract, host)
		execResGasUsed = res.GasUsed
		success = res.Succeeded()
	}

	// --- Einheitliche Erstattung an den Engine-EOA ---
	// WICHTIG: Im Meta-Event die ROOT-ID (sessionId) loggen, NICHT die Node-PID
	metaData, _ := engineMetaEvent.Inputs.Encode([]interface{}{
		grant.SessionId,    // processId-Feld trägt jetzt die rootId (Session)
		meta.Iteration,     // iteration
		grant.XRC729,       // orchestration
		grant.OstcId,       // ostcId
		grant.OstcHash,     // ostcHash
		meta.StepId,        // stepId
		meta.RuleContract,  // ruleContract
		meta.RuleHash,      // ruleHash
		call.To,            // execContract
		success,            // execResult
		meta.Payload,       // payload
		meta.ApiSaves,      // apiSaves
		meta.ContractSaves, // contractSaves
	})
	extrasData, _ := engineExtrasEvent.Inputs.Encode([]interface{}{
		new(big.Int).SetUint64(execResGasUsed),
		meta.Extras,
	})
	// Abrechnungseinheiten sind SSOT aus fc:
	//   - EVM-Units (Base+Calldata+Logs+CALL+execLimit)
	//   - Validation-Units (call.ValidationGas)
	evmUnits := fc.evmTxUnits()
	totalUnits := fc.totalTxUnits()

	// Erstattung deckungsgleich zur SSOT-Aufteilung:
	//   Feld 3: EVM-Refund (ohne Validation)
	//   Feld 4: Validation-Wei (nur Breakdown)
	evmFeeRefund := new(big.Int).Mul(new(big.Int).SetUint64(evmUnits), effectiveWeiPerGas)
	engineFeeWei := new(big.Int).Mul(new(big.Int).SetUint64(fc.validationGas), effectiveWeiPerGas)
	totalPay := new(big.Int).Add(new(big.Int).Set(evmFeeRefund), engineFeeWei)
	if totalPay.Sign() > 0 {
		if err := host.Transfer(user, engine, totalPay); err != nil {
			return nil, err
		}
	}
	// EngineFee ist separat ausgewiesen (siehe Output-Feld 4).

	emitExtras := true
	// Emit EngineMeta event (exact order)
	id := engineMetaEvent.ID()
	topics := []types.Hash{types.BytesToHash(id[:])}
	host.EmitLog(contracts.EngineExecutePrecompile, topics, metaData)
	// Emit EngineExtrasV2 event deckungsgleich zu gas(): immer loggen
	if emitExtras {
		id2 := engineExtrasEvent.ID()
		topics2 := []types.Hash{types.BytesToHash(id2[:])}
		host.EmitLog(contracts.EngineExecutePrecompile, topics2, extrasData)
	}

	// Für UI:
	//   - Feld 2 („billedUnits“) entspricht den verrechneten Gas-Units.
	//   - Feld 3 enthält die EVM-Refund-Wei (Gas).
	//   - Feld 4 enthält die EngineFee-Wei (ValidationGas).
	out, _ := ethabi.MustNewType("tuple(bool,uint64,uint256,uint256)").Encode([]interface{}{
		success,
		totalUnits,
		evmFeeRefund,
		engineFeeWei,
	})

	// Persistierung von kNext erfolgte bereits oben **vor** dem EVM-Call.
	// Hier KEIN weiteres State-Tuning mehr, um Doppelwahrheiten auszuschließen.

	return out, nil
}

// BILL_GRANTS_ONLY selector handler
func (e *engineExecute) billGrantsOnly(input []byte, caller types.Address, host runtime.Host) ([]byte, error) {
	engine, ok := authorizeEngineCaller(host, caller)
	if !ok {
		return nil, runtime.ErrUnauthorizedCaller
	}

	m := engineABI.GetMethod("BILL_GRANTS_ONLY")
	vals, err := m.Inputs.Decode(input[4:])
	if err != nil {
		return nil, runtime.ErrInvalidInputData
	}
	args := vals.(map[string]interface{})
	payer := types.Address(args["payer"].(ethgo.Address))
	seconds, _ := args["grantFeeSeconds"].(uint64)
	perYearWei := nz(getBig(args["grantFeePerYearWei"]))

	fee, err := billGrants(host, payer, engine, seconds, perYearWei)
	if err != nil {
		return nil, err
	}
	logGrantFeeCharged(host, payer, engine, seconds, perYearWei, fee)

	return m.Outputs.Encode([]interface{}{fee})
}

func billGrants(host runtime.Host, payer, engine types.Address, seconds uint64, perYearWei *big.Int) (*big.Int, error) {
	if seconds == 0 {
		return big.NewInt(0), nil
	}
	rate := nz(perYearWei)
	fee := new(big.Int).Mul(rate, new(big.Int).SetUint64(seconds))
	fee.Add(fee, big.NewInt(31_536_000-1)) // ceil
	fee.Div(fee, big.NewInt(31_536_000))
	if fee.Sign() == 0 {
		return fee, nil
	}
	if err := host.Transfer(payer, engine, fee); err != nil {
		return nil, err
	}
	return fee, nil
}

func logGrantFeeCharged(host runtime.Host, payer, engine types.Address, seconds uint64, perYearWei, fee *big.Int) {
	payload, err := ethabi.MustNewType("tuple(address,address,uint64,uint256,uint256)").Encode([]interface{}{
		payer,
		engine,
		new(big.Int).SetUint64(seconds),
		nz(perYearWei),
		nz(fee),
	})
	if err != nil {
		return
	}
	host.EmitLog(contracts.EngineExecutePrecompile, nil, payload)
}

// ---------- arithmetische Längen-Helfer (exakt, ohne Encode) ----------------
func pad32(n int) int   { return ((n + 31) / 32) * 32 }
func dynLen(n int) int  { return 32 + pad32(n) }  // 32B len + padded payload
func bLen(s string) int { return len([]byte(s)) } // UTF-8 Bytes

// EngineMeta: 13 Felder (8 static im Head, 5 dynamic im Tail)
const nArgsMeta = 13

// EngineExtrasV2: 2 Felder (1 dynamic)
const nArgsExtras = 2

func calcEngineMetaLen(g inGrant, m inMeta) int {
	head := 32 * nArgsMeta
	tail := 0
	tail += dynLen(bLen(g.OstcId))       // string
	tail += dynLen(bLen(m.StepId))       // string
	tail += dynLen(len(m.Payload))       // bytes
	tail += dynLen(len(m.ApiSaves))      // bytes
	tail += dynLen(len(m.ContractSaves)) // bytes
	return head + tail
}

// --- Entfernte Legacy-Helfer ------------------------------------------------
// Alle kRootSession-Referenzen wurden eliminiert.
// Als Invariante gilt:
//   - Neuer Root:   sessionId == kNext
//   - Follow-up:    sessionId <  kNext
//   - Ablehnung:    sessionId >  kNext
//   - lastRoot := kNext - 1 (implizit)

func calcEngineExtrasLen(m inMeta) int {
	return 32*nArgsExtras + dynLen(len(m.Extras))
}

func nz(b *big.Int) *big.Int {
	if b != nil {
		return b
	}
	return new(big.Int)
}

func decodeGrant(m map[string]interface{}) inGrant {
	return inGrant{
		From:        m["from"].(ethgo.Address),
		Engine:      m["engine"].(ethgo.Address),
		XRC729:      m["xrc729"].(ethgo.Address),
		OstcId:      m["ostcId"].(string),
		OstcHash:    m["ostcHash"].([32]byte),
		ProcessId:   getBig(m["processId"]),
		MaxTotalGas: m["maxTotalGas"].(*big.Int),
		Expiry:      getBig(m["expiry"]),
		SessionId:   getBig(m["sessionId"]),
		ChainId:     getBig(m["chainId"]),
	}
}

func decodeCall(m map[string]interface{}) inCall {
	return inCall{
		To:            m["to"].(ethgo.Address),
		Data:          m["data"].([]byte),
		ValueWei:      getBig(m["valueWei"]),
		GasLimit:      m["gasLimit"].(uint64),
		ValidationGas: m["validationGas"].(uint64),
		MaxFeePerGas:  getBig(m["maxFeePerGas"]),
		Deadline:      m["deadline"].(uint64),
		GrantFeeSeconds: func() uint64 {
			if v, ok := m["grantFeeSeconds"].(uint64); ok {
				return v
			}
			return 0
		}(),
		GrantFeePerYearWei: getBig(m["grantFeePerYearWei"]),
	}
}

func decodeMeta(m map[string]interface{}) inMeta {
	return inMeta{
		Iteration:     m["iteration"].(uint64),
		StepId:        m["stepId"].(string),
		RuleContract:  m["ruleContract"].(ethgo.Address),
		RuleHash:      m["ruleHash"].([32]byte),
		Payload:       m["payload"].([]byte),
		ApiSaves:      m["apiSaves"].([]byte),
		ContractSaves: m["contractSaves"].([]byte),
		Extras:        m["extras"].([]byte),
	}
}

func getBig(v interface{}) *big.Int {
	if v == nil {
		return nil
	}
	if bi, ok := v.(*big.Int); ok {
		return bi
	}
	return nil
}

// getNextPid implements ENGINE_GET_NEXT_PID(user) → pid:uint256.
// Deterministic fallback: next > 0 ? next : 1.
func (e *engineExecute) getNextPid(input []byte, host runtime.Host) ([]byte, error) {
	m := getNextPidABI.GetMethod("ENGINE_GET_NEXT_PID")
	vals, err := m.Inputs.Decode(input[4:])
	if err != nil {
		return nil, runtime.ErrInvalidInputData
	}
	args := vals.(map[string]interface{})
	user := args["user"].(ethgo.Address)

	next := sloadU256(host, kNext(user))
	if next == nil || next.Sign() == 0 {
		next = big.NewInt(1)
	}

	out, err := m.Outputs.Encode([]interface{}{next})
	if err != nil {
		return nil, err
	}
	return out, nil
}
