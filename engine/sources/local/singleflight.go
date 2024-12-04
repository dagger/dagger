package local

import (
	"context"
	"sync"
)

// SingleflightGroup is similar to sync.Singleflight but:
// 1. Handles context cancellation (ctx provided to callback is only cancelled once no one is waiting on the result)
// 2. Caches results until all returned CachedResults are released
type SingleflightGroup[K comparable, V any] struct {
	mu    sync.Mutex
	calls map[K]*CachedResult[K, V]
}

type CachedResult[K comparable, V any] struct {
	g *SingleflightGroup[K, V]

	key K
	val V
	err error

	done    chan struct{}
	cancel  context.CancelCauseFunc
	waiters int

	refCount int
}

func (g *SingleflightGroup[K, V]) Do(ctx context.Context, key K, fn func(context.Context) (V, error)) (*CachedResult[K, V], error) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[K]*CachedResult[K, V])
	}

	if res, ok := g.calls[key]; ok {
		res.waiters++
		g.mu.Unlock()
		return g.wait(ctx, key, res)
	}

	callCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	res := &CachedResult[K, V]{
		g: g,

		key: key,

		done:    make(chan struct{}),
		cancel:  cancel,
		waiters: 1,
	}
	g.calls[key] = res
	go func() {
		defer close(res.done)
		res.val, res.err = fn(callCtx)
	}()

	g.mu.Unlock()
	return g.wait(ctx, key, res)
}

func (g *SingleflightGroup[K, V]) wait(ctx context.Context, key K, res *CachedResult[K, V]) (*CachedResult[K, V], error) {
	var err error
	select {
	case <-res.done:
		err = res.err
	case <-ctx.Done():
		err = context.Cause(ctx)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	res.waiters--
	if res.waiters == 0 {
		res.cancel(err)
	}

	if err == nil {
		res.refCount++
		return res, nil
	}

	if res.refCount == 0 {
		delete(g.calls, key)
	}
	return nil, err
}

func (res *CachedResult[K, V]) Result() V {
	return res.val
}

func (res *CachedResult[K, V]) Release() {
	res.g.mu.Lock()
	defer res.g.mu.Unlock()

	res.refCount--
	if res.refCount == 0 && res.waiters == 0 {
		delete(res.g.calls, res.key)
	}
}
