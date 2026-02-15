package framework

import "math/big"

const (
	// TestGasPriceBump is added on top of DefaultGasPrice in tests to ensure
	// transactions are never rejected as underpriced when MinBaseFee is enforced.
	TestGasPriceBump int64 = 2_000_000_000 // 2 gwei

	// TestLegacyGasPrice is the legacy (type-0) gas price used in e2e tests.
	TestLegacyGasPrice int64 = int64(DefaultGasPrice) + TestGasPriceBump

	// Test1559TipCap is the EIP-1559 priority fee used in e2e tests.
	Test1559TipCap int64 = int64(DefaultGasPrice) + TestGasPriceBump

	// Test1559FeeCap is the EIP-1559 max fee used in e2e tests.
	// Keep it comfortably above the floor so it stays valid even if BaseFee drifts upward.
	Test1559FeeCap int64 = int64(DefaultGasPrice)*2 + TestGasPriceBump
)

func TestGasPrice() *big.Int {
	return big.NewInt(TestLegacyGasPrice)
}

func TestGasPriceUint64() uint64 {
	return uint64(TestLegacyGasPrice)
}

func TestGasTipCap() *big.Int {
	return big.NewInt(Test1559TipCap)
}

func TestGasFeeCap() *big.Int {
	return big.NewInt(Test1559FeeCap)
}
