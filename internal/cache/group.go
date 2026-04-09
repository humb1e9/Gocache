package cache

import (
	"context"
	"fmt"
	"log"
	"sync"

	cachevalue "gocache/internal/value"
)

// Getter loads data from the underlying data source on cache miss.
type Getter interface {
	Get(key string) ([]byte, error)
}

// GetterFunc turns a function into a Getter.
type GetterFunc func(key string) ([]byte, error)

// Get implements Getter.
func (f GetterFunc) Get(key string) ([]byte, error) {
	return f(key)
}

// Group coordinates the local cache, peer lookup, and source loading.
type Group struct {
	name      string
	getter    Getter
	mainCache cache
	peers     PeerPicker
	loader    *FlightGroup
}

var (
	mu     sync.RWMutex
	groups = make(map[string]*Group)
)

// NewGroup creates a namespace-scoped cache group.
func NewGroup(name string, cacheBytes int64, getter Getter) *Group {
	return NewGroupWithPolicy(name, cacheBytes, getter, PolicyLRU)
}

// NewGroupWithPolicy creates a namespace-scoped cache group with a specific eviction policy.
func NewGroupWithPolicy(name string, cacheBytes int64, getter Getter, policy EvictionPolicy) *Group {
	if getter == nil {
		panic("nil Getter")
	}

	g := &Group{
		name:      name,
		getter:    getter,
		mainCache: newCache(cacheBytes, policy),
		loader:    &FlightGroup{},
	}

	mu.Lock()
	groups[name] = g
	mu.Unlock()

	return g
}

// GetGroup returns a previously created group by name.
func GetGroup(name string) *Group {
	mu.RLock()
	defer mu.RUnlock()
	return groups[name]
}

// RegisterPeers wires a peer picker into the group.
func (g *Group) RegisterPeers(peers PeerPicker) {
	if g.peers != nil {
		panic("RegisterPeers called more than once")
	}
	g.peers = peers
}

// Get returns the cached value for key.
func (g *Group) Get(ctx context.Context, key string) (ByteView, error) {
	if key == "" {
		return ByteView{}, fmt.Errorf("key is required")
	}

	if value, ok := g.mainCache.get(key); ok {
		log.Printf("[GeeCache] hit key=%s", key)
		return value, nil
	}

	return g.load(ctx, key)
}

func (g *Group) load(ctx context.Context, key string) (ByteView, error) {
	view, err := g.loader.Do(key, func() (interface{}, error) {
		if g.peers != nil {
			if peer, ok := g.peers.PickPeer(key); ok {
				if value, err := g.getFromPeer(ctx, peer, key); err == nil {
					g.populateCache(key, value)
					return value, nil
				} else {
					log.Printf("[GeeCache] peer fetch failed for key=%s: %v", key, err)
				}
			}
		}
		return g.getLocally(key)
	})
	if err != nil {
		return ByteView{}, err
	}
	return view.(ByteView), nil
}

func (g *Group) getLocally(key string) (ByteView, error) {
	bytes, err := g.getter.Get(key)
	if err != nil {
		return ByteView{}, err
	}
	view := cachevalue.NewByteView(bytes)
	g.populateCache(key, view)
	return view, nil
}

func (g *Group) populateCache(key string, value ByteView) {
	g.mainCache.add(key, value)
}

func (g *Group) getFromPeer(ctx context.Context, peer PeerGetter, key string) (ByteView, error) {
	req := &Request{
		Group: g.name,
		Key:   key,
	}
	res := &Response{}
	if err := peer.Get(ctx, req, res); err != nil {
		return ByteView{}, err
	}
	return cachevalue.NewByteView(res.Value), nil
}
