package server

import (
	"context"
	"sync"

	"github.com/dagger/dagger/internal/odag/derive"
	"github.com/dagger/dagger/internal/odag/store"
	"github.com/dagger/dagger/internal/odag/transform"
	"resenje.org/singleflight"
)

type v2TraceScope struct {
	spans    []store.SpanRecord
	proj     *transform.TraceProjection
	scopeIdx *derive.ScopeIndex
}

type v2TraceScopeCache struct {
	mu      sync.RWMutex
	items   map[string]v2TraceScope
	flights singleflight.Group[string, v2TraceScope]
}

func newV2TraceScopeCache() *v2TraceScopeCache {
	return &v2TraceScopeCache{
		items: make(map[string]v2TraceScope),
	}
}

func (c *v2TraceScopeCache) load(traceID string) (v2TraceScope, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	scope, ok := c.items[traceID]
	return scope, ok
}

func (c *v2TraceScopeCache) store(traceID string, scope v2TraceScope) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[traceID] = scope
}

func (c *v2TraceScopeCache) invalidate(traceIDs ...string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(traceIDs) == 0 {
		c.items = make(map[string]v2TraceScope)
		return
	}
	for _, traceID := range traceIDs {
		delete(c.items, traceID)
		c.flights.Forget(traceID)
	}
}

func (c *v2TraceScopeCache) loadOrCompute(
	ctx context.Context,
	traceID string,
	fn func(context.Context) (v2TraceScope, error),
) (v2TraceScope, error) {
	if c == nil {
		return fn(ctx)
	}
	if scope, ok := c.load(traceID); ok {
		return scope, nil
	}
	scope, _, err := c.flights.Do(ctx, traceID, func(ctx context.Context) (v2TraceScope, error) {
		if cached, ok := c.load(traceID); ok {
			return cached, nil
		}
		fresh, err := fn(ctx)
		if err != nil {
			return v2TraceScope{}, err
		}
		c.store(traceID, fresh)
		return fresh, nil
	})
	return scope, err
}
