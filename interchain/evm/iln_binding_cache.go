package evm

import (
	"maps"
	"sync"
	"time"

	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

// These are Solidity immutable getters of the V315 Registry, factory-created
// Gateway and its FeeVault. Keep getRoute, mailbox/dispatchHook and the
// message's FeeVault allocation live. The registry is release-pinned.
var ilnImmutableSelectors = func() map[[4]byte]bool {
	selectors := make(map[[4]byte]bool)
	for _, signature := range []string{
		"factory()", "ilnRegistry()", "warpRouter()", "feeVault()", "gateway()",
		"routeId()", "destinationDomain()", "governanceRegistry()", "validatorRegistry()",
	} {
		hash := crypto.Keccak256Hash([]byte(signature))
		selectors[[4]byte(hash[:4])] = true
	}
	return selectors
}()

type ilnBindingCall struct {
	address  types.Address
	selector [4]byte
}

type ilnBindingValues map[ilnBindingCall][32]byte

type ilnBindingObservation struct {
	cached ilnBindingValues
	seen   ilnBindingValues
	reused bool
}

func (observation *ilnBindingObservation) wrap(view ILNView) ILNView {
	return func(address types.Address, data []byte) ([]byte, error) {
		if len(data) != 4 || !ilnImmutableSelectors[[4]byte(data)] {
			return view(address, data)
		}
		key := ilnBindingCall{address, [4]byte(data)}
		if value, ok := observation.cached[key]; ok {
			observation.reused = true
			observation.seen[key] = value
			return value[:], nil
		}
		result, err := view(address, data)
		if err == nil && len(result) == 32 {
			observation.seen[key] = [32]byte(result)
		}
		return result, err
	}
}

type ilnBindingCacheKey struct {
	chain       uint64
	registry    string
	destination uint32
	route       types.Hash
}

type ilnBindingCacheEntry struct {
	values    ilnBindingValues
	endpoints map[string]bool
	expires   time.Time
}

type ilnBindingCache struct {
	sync.Mutex
	entries map[ilnBindingCacheKey]ilnBindingCacheEntry
}

var ilnSourceBindings = ilnBindingCache{entries: make(map[ilnBindingCacheKey]ilnBindingCacheEntry)}

func (cache *ilnBindingCache) get(key ilnBindingCacheKey) ilnBindingCacheEntry {
	cache.Lock()
	defer cache.Unlock()
	entry := cache.entries[key]
	if time.Now().Before(entry.expires) {
		return entry
	}
	delete(cache.entries, key)
	return ilnBindingCacheEntry{}
}

func (cache *ilnBindingCache) remember(key ilnBindingCacheKey, first, second string, a, b *ilnBindingObservation) {
	// Only two matching, independently completed witnesses can fill the
	// cache. A cached observation must never extend its own lifetime.
	if a.reused || b.reused || len(a.seen) == 0 || !maps.Equal(a.seen, b.seen) {
		return
	}
	cache.Lock()
	defer cache.Unlock()
	if _, exists := cache.entries[key]; !exists && len(cache.entries) >= 128 {
		var oldestKey ilnBindingCacheKey
		var oldestExpiry time.Time
		for candidate, entry := range cache.entries {
			if oldestExpiry.IsZero() || entry.expires.Before(oldestExpiry) {
				oldestKey, oldestExpiry = candidate, entry.expires
			}
		}
		delete(cache.entries, oldestKey)
	}
	cache.entries[key] = ilnBindingCacheEntry{
		values: a.seen, endpoints: map[string]bool{first: true, second: true},
		expires: time.Now().Add(10 * time.Minute),
	}
}
