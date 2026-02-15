package chain

import (
	"golang.org/x/crypto/sha3"

	"github.com/xgr-network/xgr-node/types"
)

// - EngineRegistryAddress is sourced from genesis.json (params.engineRegistryAddress).
// - If unset or not deployed yet (code-size == 0), chain code falls back to defaults.
var EngineRegistryAddress = types.Address{}

// BootstrapEngineEOA is used only while EngineRegistryAddress is unset or the registry
// is not deployed yet (code-size == 0). It must be identical across all nodes.
//
// If left as zero, no engine caller will be authorized during bootstrap.
var BootstrapEngineEOA = types.Address{} // SET BEFORE LAUNCH (or keep zero to deny-all)

// DefaultBurnedAddress is used für default burn of 1000 Gwei
var DefaultBurnedAddress = types.StringToAddress("0x0000000000000000000000000000000000000666")

// DefaultDonationAddress is used when no on-chain donation recipient is available.
var DefaultDonationAddress = DefaultBurnedAddress //types.StringToAddress("0xCfD008a1de815f402aD8E7e6F8461d3a878DEF59")

// DefaultDonationPercent is the fallback donation fee percent (0-100).
const DefaultDonationPercent uint64 = 15

const (
	engineRegistrySlotAuthorizedEngines uint64 = 2
	engineRegistrySlotMinBaseFee        uint64 = 5
	engineRegistrySlotPaused            uint64 = 6
	// Keep in sync with EngineRegistry.sol storage layout (appended after `paused`)
	// Keep in sync with EngineRegistry.sol storage layout.
	// slot 7: __reserved0 (uint256)
	engineRegistrySlotDonationAddress uint64 = 8
	engineRegistrySlotDonationPercent uint64 = 9
)

// EngineRegistrySlotKeyMinBaseFee returns the storage slot key for minBaseFee.
func EngineRegistrySlotKeyMinBaseFee() types.Hash { return u256Slot(engineRegistrySlotMinBaseFee) }

// EngineRegistrySlotKeyPaused returns the storage slot key for paused.
func EngineRegistrySlotKeyPaused() types.Hash { return u256Slot(engineRegistrySlotPaused) }

// EngineRegistrySlotKeyDonationAddress returns the storage slot key for donationAddress.
func EngineRegistrySlotKeyDonationAddress() types.Hash {
	return u256Slot(engineRegistrySlotDonationAddress)
}

// EngineRegistrySlotKeyDonationPercent returns the storage slot key for donationPercent.
func EngineRegistrySlotKeyDonationPercent() types.Hash {
	return u256Slot(engineRegistrySlotDonationPercent)
}

// EngineRegistrySlotKeyAuthorizedEngine returns the mapping slot key for authorizedEngines[engine].
func EngineRegistrySlotKeyAuthorizedEngine(engine types.Address) types.Hash {
	// keccak256(pad32(engine) || pad32(slot))
	var buf [64]byte
	copy(buf[12:32], engine[:])
	slot := u256Slot(engineRegistrySlotAuthorizedEngines)
	copy(buf[32:], slot[:])

	keccak := sha3.NewLegacyKeccak256()
	keccak.Write(buf[:])
	return types.BytesToHash(keccak.Sum(nil))
}

func u256Slot(n uint64) types.Hash {
	var b [32]byte
	// big-endian uint256 with n in the last 8 bytes
	b[24] = byte(n >> 56)
	b[25] = byte(n >> 48)
	b[26] = byte(n >> 40)
	b[27] = byte(n >> 32)
	b[28] = byte(n >> 24)
	b[29] = byte(n >> 16)
	b[30] = byte(n >> 8)
	b[31] = byte(n)
	return types.BytesToHash(b[:])
}
