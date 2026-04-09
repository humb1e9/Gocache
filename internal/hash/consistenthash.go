package hash

import (
	"hash/crc32"
	"sort"
	"strconv"
)

// Hash computes the hash of a byte slice.
type Hash func(data []byte) uint32

// HashRing stores virtual nodes used by consistent hashing.
type HashRing struct {
	hash     Hash
	replicas int
	keys     []int
	hashMap  map[int]string
}

// NewHashRing creates a new consistent hash ring.
func NewHashRing(replicas int, fn Hash) *HashRing {
	m := &HashRing{
		replicas: replicas,
		hash:     fn,
		hashMap:  make(map[int]string),
	}
	if m.hash == nil {
		m.hash = crc32.ChecksumIEEE
	}
	return m
}

// Add inserts real nodes and their virtual replicas.
func (m *HashRing) Add(keys ...string) {
	for _, key := range keys {
		for i := range m.replicas {
			hash := int(m.hash([]byte(strconv.Itoa(i) + key)))
			m.keys = append(m.keys, hash)
			m.hashMap[hash] = key
		}
	}
	sort.Ints(m.keys)
}

// Remove deletes real nodes and their virtual replicas.
func (m *HashRing) Remove(keys ...string) {
	if len(keys) == 0 || len(m.keys) == 0 {
		return
	}

	removeSet := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		removeSet[key] = struct{}{}
	}

	filtered := m.keys[:0]
	for _, hash := range m.keys {
		node := m.hashMap[hash]
		if _, ok := removeSet[node]; ok {
			delete(m.hashMap, hash)
			continue
		}
		filtered = append(filtered, hash)
	}
	m.keys = filtered
}

// Set rebuilds the hash ring from scratch.
func (m *HashRing) Set(keys ...string) {
	m.keys = nil
	m.hashMap = make(map[int]string)
	m.Add(keys...)
}

// Get returns the nearest node in the ring.
func (m *HashRing) Get(key string) string {
	if len(m.keys) == 0 {
		return ""
	}

	hash := int(m.hash([]byte(key)))
	idx := sort.Search(len(m.keys), func(i int) bool {
		return m.keys[i] >= hash
	})

	return m.hashMap[m.keys[idx%len(m.keys)]]
}
