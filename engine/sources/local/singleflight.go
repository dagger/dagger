package local

import (
	"context"
	"sync"

	"resenje.org/singleflight"
)

// TODO: consolidate with dagql.CacheMap, it might benefit from the context handling here
type CachedSingleFlightGroup[K comparable, V any] struct {
	inner singleflight.Group[K, V]
	mu    sync.Mutex
	cache map[K]V
}

func (g *CachedSingleFlightGroup[K, V]) Do(ctx context.Context, key K, fn func(context.Context) (V, error)) (V, error) {
	g.mu.Lock()
	if v, ok := g.cache[key]; ok {
		g.mu.Unlock()
		return v, nil
	}
	g.mu.Unlock()
	v, _, err := g.inner.Do(ctx, key, fn)
	if err == nil {
		g.mu.Lock()
		if g.cache == nil {
			g.cache = make(map[K]V)
		}
		g.cache[key] = v
		g.mu.Unlock()
	}
	return v, err
}
