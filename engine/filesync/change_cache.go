package filesync

import (
	"context"
	"fmt"
	"sync"
)

func newChangeCache() *changeCache {
	return &changeCache{}
}

type changeCache struct {
	mu sync.Mutex

	// calls that are in progress, keyed by call key
	ongoingCalls map[string]*cachedChange

	// calls that have completed successfully and are cached, keyed by call key
	completedCalls map[string]*cachedChange
}

type cachedChange struct {
	cache *changeCache

	callKey string

	val *ChangeWithStat
	err error

	waitCh  chan struct{}
	cancel  context.CancelCauseFunc
	waiters int

	refCount int
}

func (c *changeCache) getOrInit(
	ctx context.Context,
	callKey string,
	fn func(context.Context) (*ChangeWithStat, error),
) (*cachedChange, error) {
	if callKey == "" {
		return nil, fmt.Errorf("cache call key is empty")
	}

	c.mu.Lock()
	if c.ongoingCalls == nil {
		c.ongoingCalls = make(map[string]*cachedChange)
	}
	if c.completedCalls == nil {
		c.completedCalls = make(map[string]*cachedChange)
	}

	if res, ok := c.completedCalls[callKey]; ok {
		res.refCount++
		c.mu.Unlock()
		return res, nil
	}

	if res, ok := c.ongoingCalls[callKey]; ok {
		res.waiters++
		c.mu.Unlock()
		return c.wait(ctx, res)
	}

	callCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	res := &cachedChange{
		cache: c,

		callKey: callKey,

		waitCh:  make(chan struct{}),
		cancel:  cancel,
		waiters: 1,
	}
	c.ongoingCalls[callKey] = res

	go func() {
		defer close(res.waitCh)
		val, err := fn(callCtx)
		res.err = err
		if err == nil {
			res.val = val
		}
	}()

	c.mu.Unlock()
	return c.wait(ctx, res)
}

func (c *changeCache) wait(ctx context.Context, res *cachedChange) (*cachedChange, error) {
	var err error

	select {
	case <-res.waitCh:
		err = res.err
	default:
		select {
		case <-res.waitCh:
			err = res.err
		case <-ctx.Done():
			err = context.Cause(ctx)
		}
	}

	c.mu.Lock()

	res.waiters--
	if res.waiters == 0 {
		res.cancel(err)
	}

	if err == nil {
		if existing := c.completedCalls[res.callKey]; existing != nil {
			res = existing
		} else {
			c.completedCalls[res.callKey] = res
		}
		delete(c.ongoingCalls, res.callKey)

		res.refCount++
		c.mu.Unlock()
		return res, nil
	}

	if res.refCount == 0 && res.waiters == 0 {
		if existing := c.ongoingCalls[res.callKey]; existing == res {
			delete(c.ongoingCalls, res.callKey)
		}
		if existing := c.completedCalls[res.callKey]; existing == res {
			delete(c.completedCalls, res.callKey)
		}
	}

	c.mu.Unlock()
	return nil, err
}

func (res *cachedChange) result() *ChangeWithStat {
	if res == nil {
		return nil
	}
	return res.val
}

func (res *cachedChange) release() {
	if res == nil || res.cache == nil {
		return
	}

	res.cache.mu.Lock()
	res.refCount--
	if res.refCount == 0 && res.waiters == 0 {
		if existing := res.cache.ongoingCalls[res.callKey]; existing == res {
			delete(res.cache.ongoingCalls, res.callKey)
		}
		if existing := res.cache.completedCalls[res.callKey]; existing == res {
			delete(res.cache.completedCalls, res.callKey)
		}
	}
	res.cache.mu.Unlock()
}
