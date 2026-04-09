package policy

import (
	"container/heap"

	"gocache/internal/value"
)

type lfuEntry struct {
	key   string
	value value.ByteView
	freq  int
	tick  int64
	index int
}

type lfuHeap []*lfuEntry

func (h lfuHeap) Len() int { return len(h) }

func (h lfuHeap) Less(i, j int) bool {
	if h[i].freq == h[j].freq {
		return h[i].tick < h[j].tick
	}
	return h[i].freq < h[j].freq
}

func (h lfuHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *lfuHeap) Push(x interface{}) {
	item := x.(*lfuEntry)
	item.index = len(*h)
	*h = append(*h, item)
}

func (h *lfuHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	item.index = -1
	*h = old[:n-1]
	return item
}

// LFUCache evicts the least frequently used entry first.
type LFUCache struct {
	maxBytes  int64
	nbytes    int64
	clock     int64
	items     map[string]*lfuEntry
	pq        lfuHeap
	OnEvicted func(key string, value value.ByteView)
}

// NewLFU creates an LFU cache with an optional eviction callback.
func NewLFU(maxBytes int64, onEvicted func(string, value.ByteView)) *LFUCache {
	c := &LFUCache{
		maxBytes:  maxBytes,
		items:     make(map[string]*lfuEntry),
		OnEvicted: onEvicted,
	}
	heap.Init(&c.pq)
	return c
}

// Get returns the cached value if present and bumps its frequency.
func (c *LFUCache) Get(key string) (value.ByteView, bool) {
	item, ok := c.items[key]
	if !ok {
		return value.ByteView{}, false
	}
	c.touch(item)
	return item.value, true
}

// Add inserts or updates a cache entry.
func (c *LFUCache) Add(key string, value value.ByteView) {
	if item, ok := c.items[key]; ok {
		c.nbytes += int64(value.Len()) - int64(item.value.Len())
		item.value = value
		c.touch(item)
	} else {
		c.clock++
		item := &lfuEntry{
			key:   key,
			value: value,
			freq:  1,
			tick:  c.clock,
		}
		c.items[key] = item
		heap.Push(&c.pq, item)
		c.nbytes += int64(len(key)) + int64(value.Len())
	}

	for c.maxBytes != 0 && c.nbytes > c.maxBytes {
		c.RemoveLFU()
	}
}

// RemoveLFU evicts the least frequently used entry.
func (c *LFUCache) RemoveLFU() {
	if c.pq.Len() == 0 {
		return
	}

	item := heap.Pop(&c.pq).(*lfuEntry)
	delete(c.items, item.key)
	c.nbytes -= int64(len(item.key)) + int64(item.value.Len())
	if c.OnEvicted != nil {
		c.OnEvicted(item.key, item.value)
	}
}

// Len returns the number of cached entries.
func (c *LFUCache) Len() int {
	return len(c.items)
}

func (c *LFUCache) touch(item *lfuEntry) {
	c.clock++
	item.freq++
	item.tick = c.clock
	heap.Fix(&c.pq, item.index)
}
