package evm

import "fmt"

type handler struct {
	inst  instruction
	stack int
	gas   uint64
}

var dispatchTable [256]handler

func register(op OpCode, h handler) {
	if dispatchTable[op].inst != nil {
		panic(fmt.Errorf("instruction already exists")) //nolint:gocritic
	}

	dispatchTable[op] = h
}

func registerRange(from, to OpCode, factory func(n int) instruction, gas uint64) {
	c := 1
	for i := from; i <= to; i++ {
		register(i, handler{factory(c), 0, gas})
		c++
	}
}

func init() {

	// unsigned arithmetic operations
	register(STOP, handler{opStop, 0, 0})
	register(ADD, handler{opAdd, 2, 3})
	register(SUB, handler{opSub, 2, 3})
	register(MUL, handler{opMul, 2, 5})
	register(DIV, handler{opDiv, 2, 5})
	register(SDIV, handler{opSDiv, 2, 5})
	register(MOD, handler{opMod, 2, 5})
	register(SMOD, handler{opSMod, 2, 5})
	register(EXP, handler{opExp, 2, 10})
	register(ADDMOD, handler{opAddMod, 3, 8})
	register(MULMOD, handler{opMulMod, 3, 8})

	// bitwise operations
	register(AND, handler{opAnd, 2, 3})
	register(OR, handler{opOr, 2, 3})
	register(XOR, handler{opXor, 2, 3})
	register(BYTE, handler{opByte, 2, 3})
	register(NOT, handler{opNot, 1, 3})
	register(ISZERO, handler{opIsZero, 1, 3})

	// comparison
	register(EQ, handler{opEq, 2, 3})
	register(LT, handler{opLt, 2, 3})
	register(GT, handler{opGt, 2, 3})
	register(SLT, handler{opSlt, 2, 3})
	register(SGT, handler{opSgt, 2, 3})

	// sign extension and shifting
	register(SIGNEXTEND, handler{opSignExtension, 1, 5})
	register(SHL, handler{opShl, 2, 3})
	register(SHR, handler{opShr, 2, 3})
	register(SAR, handler{opSar, 2, 3})

	// transient storage (EIP-1153)
	register(TLOAD, handler{opTload, 1, 3})
	register(TSTORE, handler{opTstore, 2, 5})

	// relative jumps (EIP-4200)
	register(RJUMP, handler{opRjump, 1, 2})
	register(RJUMPI, handler{opRjumpi, 2, 3})
	register(RJUMPV, handler{opRjumpv, 2, 5})

	// functions (EIP-2315, 4750)
	register(CALLF, handler{opCallf, 1, 25})
	register(RETF, handler{opRetf, 0, 1})
	register(JUMPF, handler{opJumpf, 1, 3})

	// push0 and other standard stack ops
	register(PUSH0, handler{opPush0, 0, 2})
	registerRange(PUSH1, PUSH32, opPush, 3)
	registerRange(DUP1, DUP16, opDup, 3)
	registerRange(SWAP1, SWAP16, opSwap, 3)
	registerRange(LOG0, LOG4, opLog, 375)

	// create and call
	register(CREATE, handler{opCreate(CREATE), 3, 32000})
	register(CREATE2, handler{opCreate(CREATE2), 4, 32000})
	register(CALL, handler{opCall(CALL), 7, 0})
	register(CALLCODE, handler{opCall(CALLCODE), 7, 0})
	register(DELEGATECALL, handler{opCall(DELEGATECALL), 6, 0})
	register(STATICCALL, handler{opCall(STATICCALL), 6, 0})

	// halt instructions
	register(REVERT, handler{opHalt(REVERT), 2, 0})
	register(RETURN, handler{opHalt(RETURN), 2, 0})
	register(SELFDESTRUCT, handler{opSelfDestruct, 1, 0})

	// memory operations
	register(MLOAD, handler{opMload, 1, 3})
	register(MSTORE, handler{opMStore, 2, 3})
	register(MSTORE8, handler{opMStore8, 2, 3})
	register(MCOPY, handler{opMcopy, 3, 3}) // doppelt für Lesbarkeit

	// storage
	register(SLOAD, handler{opSload, 1, 0})
	register(SSTORE, handler{opSStore, 2, 0})
	register(SHA3, handler{opSha3, 2, 30})
	register(POP, handler{opPop, 1, 2})

	// extcode
	register(EXTCODEHASH, handler{opExtCodeHash, 1, 0})
	register(EXTCODESIZE, handler{opExtCodeSize, 1, 0})
	register(EXTCODECOPY, handler{opExtCodeCopy, 4, 0})

	// environment info
	register(ADDRESS, handler{opAddress, 0, 2})
	register(BALANCE, handler{opBalance, 1, 0})
	register(SELFBALANCE, handler{opSelfBalance, 0, 5})
	register(ORIGIN, handler{opOrigin, 0, 2})
	register(CALLER, handler{opCaller, 0, 2})
	register(CALLVALUE, handler{opCallValue, 0, 2})
	register(CALLDATALOAD, handler{opCallDataLoad, 1, 3})
	register(CALLDATASIZE, handler{opCallDataSize, 0, 2})
	register(CODESIZE, handler{opCodeSize, 0, 2})
	register(GASPRICE, handler{opGasPrice, 0, 2})
	register(CHAINID, handler{opChainID, 0, 2})
	register(PC, handler{opPC, 0, 2})
	register(MSIZE, handler{opMSize, 0, 2})
	register(GAS, handler{opGas, 0, 2})
	register(CALLDATACOPY, handler{opCallDataCopy, 3, 3})
	register(RETURNDATASIZE, handler{opReturnDataSize, 0, 2})
	register(RETURNDATACOPY, handler{opReturnDataCopy, 3, 3})
	register(CODECOPY, handler{opCodeCopy, 3, 3})

	// block
	register(BLOCKHASH, handler{opBlockHash, 1, 20})
	register(COINBASE, handler{opCoinbase, 0, 2})
	register(TIMESTAMP, handler{opTimestamp, 0, 2})
	register(NUMBER, handler{opNumber, 0, 2})
	register(DIFFICULTY, handler{opDifficulty, 0, 2})
	register(GASLIMIT, handler{opGasLimit, 0, 2})
	register(BASEFEE, handler{opBaseFee, 0, 2})

	// jumps
	register(JUMP, handler{opJump, 1, 8})
	register(JUMPI, handler{opJumpi, 2, 10})
	register(JUMPDEST, handler{opJumpDest, 0, 1})
}
