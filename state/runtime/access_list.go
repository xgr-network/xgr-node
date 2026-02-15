package runtime

import (
	"github.com/xgr-network/xgr-node/contracts"
	"github.com/xgr-network/xgr-node/types"
)

// EIP-2929: precompiled contracts are warm at transaction start (Berlin and later).
var precompiledContracts = []types.Address{
	types.StringToAddress("0x0000000000000000000000000000000000000001"),
	types.StringToAddress("0x0000000000000000000000000000000000000002"),
	types.StringToAddress("0x0000000000000000000000000000000000000003"),
	types.StringToAddress("0x0000000000000000000000000000000000000004"),
	types.StringToAddress("0x0000000000000000000000000000000000000005"),
	types.StringToAddress("0x0000000000000000000000000000000000000006"),
	types.StringToAddress("0x0000000000000000000000000000000000000007"),
	types.StringToAddress("0x0000000000000000000000000000000000000008"),
	types.StringToAddress("0x0000000000000000000000000000000000000009"),

	// XGR custom precompiles (must be warm too under EIP-2929)
	contracts.NativeTransferPrecompile,
	contracts.BLSAggSigsVerificationPrecompile,
	contracts.ConsolePrecompile,
	contracts.EngineExecutePrecompile,
}

// AccessList tracks warm addresses and (address, storage-slot) pairs (EIP-2929).
// IMPORTANT: AccessList is scope-revertible (CALL/CREATE frames).
type AccessList struct {
	addresses map[types.Address]struct{}
	slots     map[types.Address]map[types.Hash]struct{}
}

func NewAccessList(init ...types.Address) *AccessList {
	al := &AccessList{
		addresses: make(map[types.Address]struct{}, len(init)+len(precompiledContracts)),
		slots:     make(map[types.Address]map[types.Hash]struct{}),
	}

	for _, a := range init {
		al.addresses[a] = struct{}{}
	}
	for _, a := range precompiledContracts {
		al.addresses[a] = struct{}{}
	}

	return al
}

func (al *AccessList) Copy() *AccessList {
	cp := &AccessList{
		addresses: make(map[types.Address]struct{}, len(al.addresses)),
		slots:     make(map[types.Address]map[types.Hash]struct{}, len(al.slots)),
	}

	for a := range al.addresses {
		cp.addresses[a] = struct{}{}
	}
	for a, sm := range al.slots {
		nm := make(map[types.Hash]struct{}, len(sm))
		for k := range sm {
			nm[k] = struct{}{}
		}
		cp.slots[a] = nm
	}

	return cp
}

// RevertTo replaces the current access list content with the snapshot content (pointer stays stable).
func (al *AccessList) RevertTo(snapshot *AccessList) {
	al.addresses = snapshot.addresses
	al.slots = snapshot.slots
}

func (al *AccessList) ContainsAddress(a types.Address) bool {
	_, ok := al.addresses[a]
	return ok
}

func (al *AccessList) AddAddress(a types.Address) (changed bool) {
	if al.ContainsAddress(a) {
		return false
	}
	al.addresses[a] = struct{}{}
	return true
}

func (al *AccessList) ContainsSlot(a types.Address, slot types.Hash) bool {
	sm, ok := al.slots[a]
	if !ok {
		return false
	}
	_, ok = sm[slot]
	return ok
}

func (al *AccessList) AddSlot(a types.Address, slot types.Hash) (addrAdded bool, slotAdded bool) {
	addrAdded = al.AddAddress(a)

	sm, ok := al.slots[a]
	if !ok {
		sm = make(map[types.Hash]struct{})
		al.slots[a] = sm
	}

	if _, exists := sm[slot]; exists {
		return addrAdded, false
	}

	sm[slot] = struct{}{}
	return addrAdded, true
}
