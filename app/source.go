package app

import (
	"fmt"
	"log"

	cache "gocache/internal/cache"
)

var db = map[string]string{
	"Tom":  "630",
	"Jack": "589",
	"Sam":  "567",
}

func createGroupWithPolicy(groupName string, cacheBytes int64, policy cache.EvictionPolicy) *cache.Group {
	return cache.NewGroupWithPolicy(groupName, cacheBytes, cache.GetterFunc(func(key string) ([]byte, error) {
		log.Printf("[SlowDB] search key=%s", key)
		if v, ok := db[key]; ok {
			return []byte(v), nil
		}
		return nil, fmt.Errorf("%s not found", key)
	}), policy)
}
