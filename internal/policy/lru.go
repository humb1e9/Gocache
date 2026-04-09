package policy

import (
	"container/list"

	"gocache/internal/value"
)

type entry struct {
	key   string
	value value.ByteView
}

// LRUCache is a simple LRU implementation.
type LRUCache struct {
	maxBytes  int64
	nbytes    int64
	ll        *list.List
	cache     map[string]*list.Element
	OnEvicted func(key string, value value.ByteView)
}

// NewLRU creates an LRU cache with an optional eviction callback.
func NewLRU(maxBytes int64, onEvicted func(string, value.ByteView)) *LRUCache {
	return &LRUCache{
		maxBytes:  maxBytes,
		ll:        list.New(),
		cache:     make(map[string]*list.Element),
		OnEvicted: onEvicted,
	}
}

// Get returns the cached value if present.
func (c *LRUCache) Get(key string) (value.ByteView, bool) {
	if ele, ok := c.cache[key]; ok {
		c.ll.MoveToFront(ele)
		kv := ele.Value.(*entry)
		return kv.value, true
	}
	return value.ByteView{}, false
}

// RemoveOldest evicts the oldest entry.
func (c *LRUCache) RemoveOldest() {
	ele := c.ll.Back()
	if ele == nil {
		return
	}

	c.ll.Remove(ele)
	kv := ele.Value.(*entry)
	delete(c.cache, kv.key)
	c.nbytes -= int64(len(kv.key)) + int64(kv.value.Len())
	if c.OnEvicted != nil {
		c.OnEvicted(kv.key, kv.value)
	}
}

// Add inserts or updates the cache entry.
func (c *LRUCache) Add(key string, value value.ByteView) {
	if ele, ok := c.cache[key]; ok {
		c.ll.MoveToFront(ele)
		kv := ele.Value.(*entry)
		c.nbytes += int64(value.Len()) - int64(kv.value.Len())
		kv.value = value
	} else {
		ele := c.ll.PushFront(&entry{key: key, value: value})
		c.cache[key] = ele
		c.nbytes += int64(len(key)) + int64(value.Len())
	}

	for c.maxBytes != 0 && c.nbytes > c.maxBytes {
		c.RemoveOldest()
	}
}

// Len returns the number of entries.
func (c *LRUCache) Len() int {
	return c.ll.Len()
}
