package cache

import "sync"

type call struct {
	wg  sync.WaitGroup
	val interface{}
	err error
}

// FlightGroup ensures only one in-flight load happens for the same key.
type FlightGroup struct {
	mu sync.Mutex
	m  map[string]*call
}

// Do executes fn once for the given key while other callers wait for the result.
func (g *FlightGroup) Do(key string, fn func() (interface{}, error)) (interface{}, error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*call)
	}
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}

	c := new(call)
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()

	return c.val, c.err
}
