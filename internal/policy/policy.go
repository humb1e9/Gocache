package policy

import "gocache/internal/value"

// EvictionPolicy controls which local eviction strategy is used.
type EvictionPolicy string

const (
	// PolicyLRU evicts the least recently used entries first.
	PolicyLRU EvictionPolicy = "lru"
	// PolicyLFU evicts the least frequently used entries first.
	PolicyLFU EvictionPolicy = "lfu"
)

// Store is the common behavior shared by local cache strategies.
type Store interface {
	Get(key string) (value.ByteView, bool)
	Add(key string, val value.ByteView)
	Len() int
}

// New creates a local cache store for the selected policy.
func New(evictionPolicy EvictionPolicy, maxBytes int64) Store {
	switch evictionPolicy {
	case "", PolicyLRU:
		return NewLRU(maxBytes, nil)
	case PolicyLFU:
		return NewLFU(maxBytes, nil)
	default:
		return NewLRU(maxBytes, nil)
	}
}
