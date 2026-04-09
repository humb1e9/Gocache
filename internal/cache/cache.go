package cache

import (
	"hash/fnv"
	"sync"

	"gocache/internal/policy"
)

const (
	defaultShardCount = 32
	hotCacheDivisor   = 8
)

type cache struct {
	hot  *shardedCache
	main *shardedCache
}

func (c *cache) add(key string, value ByteView) {
	if c == nil {
		return
	}
	c.main.add(key, value)
	c.hot.add(key, value)
}

func (c *cache) get(key string) (ByteView, bool) {
	if c == nil {
		return ByteView{}, false
	}
	if value, ok := c.hot.get(key); ok {
		return value, true
	}
	value, ok := c.main.get(key)
	if !ok {
		return ByteView{}, false
	}
	c.hot.add(key, value)
	return value, true
}

func newCache(cacheBytes int64, evictionPolicy EvictionPolicy) cache {
	hotBytes := cacheBytes / hotCacheDivisor
	mainBytes := cacheBytes - hotBytes
	if cacheBytes > 0 && hotBytes == 0 {
		hotBytes = cacheBytes / 4
		if hotBytes == 0 {
			hotBytes = 1
		}
		mainBytes = cacheBytes - hotBytes
	}
	if mainBytes < 0 {
		mainBytes = 0
	}

	return cache{
		hot:  newShardedCache(hotBytes, evictionPolicy, defaultShardCount),
		main: newShardedCache(mainBytes, evictionPolicy, defaultShardCount),
	}
}

type shardedCache struct {
	shards []cacheShard
}

type cacheShard struct {
	mu    sync.Mutex
	store policy.Store
}

func newShardedCache(cacheBytes int64, evictionPolicy EvictionPolicy, shardCount int) *shardedCache {
	if shardCount <= 0 {
		shardCount = 1
	}

	perShardBytes := cacheBytes
	if cacheBytes > 0 {
		perShardBytes = cacheBytes / int64(shardCount)
		if cacheBytes%int64(shardCount) != 0 {
			perShardBytes++
		}
		if perShardBytes == 0 {
			perShardBytes = 1
		}
	}

	shards := make([]cacheShard, shardCount)
	for i := range shards {
		shards[i].store = policy.New(evictionPolicy, perShardBytes)
	}
	return &shardedCache{shards: shards}
}

func (c *shardedCache) add(key string, value ByteView) {
	if c == nil || len(c.shards) == 0 {
		return
	}
	shard := c.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	shard.store.Add(key, value)
}

func (c *shardedCache) get(key string) (ByteView, bool) {
	if c == nil || len(c.shards) == 0 {
		return ByteView{}, false
	}
	shard := c.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	return shard.store.Get(key)
}

func (c *shardedCache) shardFor(key string) *cacheShard {
	idx := int(hashKey(key) % uint32(len(c.shards)))
	return &c.shards[idx]
}

func hashKey(key string) uint32 {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(key))
	return hasher.Sum32()
}
