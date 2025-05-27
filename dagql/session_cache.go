package dagql

import (
	"context"
	"errors"
	"sync"

	"github.com/dagger/dagger/engine/cache"
	"github.com/opencontainers/go-digest"
)

type CacheKeyType = digest.Digest
type CacheValueType = Typed

type CacheResult = cache.Result[CacheKeyType, CacheValueType]

type CacheValWithCallbacks = cache.ValueWithCallbacks[CacheValueType]

type SessionCache struct {
	cache cache.Cache[CacheKeyType, CacheValueType]

	results []cache.Result[CacheKeyType, CacheValueType]
	mu      sync.Mutex

	seenKeys sync.Map
}

func NewSessionCache(
	baseCache cache.Cache[CacheKeyType, CacheValueType],
) *SessionCache {
	return &SessionCache{
		cache: baseCache,
	}
}

type CacheCallOpt interface {
	SetCacheCallOpt(*CacheCallOpts)
}

type CacheCallOpts struct {
	Telemetry TelemetryFunc
}

type TelemetryFunc func(context.Context) (context.Context, func(Typed, bool, error) error)

func (o CacheCallOpts) SetCacheCallOpt(opts *CacheCallOpts) {
	*opts = o
}

type CacheCallOptFunc func(*CacheCallOpts)

func (f CacheCallOptFunc) SetCacheCallOpt(opts *CacheCallOpts) {
	f(opts)
}

func WithTelemetry(telemetry TelemetryFunc) CacheCallOpt {
	return CacheCallOptFunc(func(opts *CacheCallOpts) {
		opts.Telemetry = telemetry
	})
}

func (c *SessionCache) GetOrInitializeValue(
	ctx context.Context,
	key CacheKeyType,
	val CacheValueType,
	opts ...CacheCallOpt,
) (CacheResult, error) {
	return c.GetOrInitialize(ctx, key, func(_ context.Context) (CacheValueType, error) {
		return val, nil
	}, opts...)
}

func (c *SessionCache) GetOrInitialize(
	ctx context.Context,
	key CacheKeyType,
	fn func(context.Context) (CacheValueType, error),
	opts ...CacheCallOpt,
) (CacheResult, error) {
	return c.GetOrInitializeWithCallbacks(ctx, key, false, func(ctx context.Context) (*CacheValWithCallbacks, error) {
		val, err := fn(ctx)
		if err != nil {
			return nil, err
		}
		return &CacheValWithCallbacks{Value: val}, nil
	}, opts...)
}

func (c *SessionCache) GetOrInitializeWithCallbacks(
	ctx context.Context,
	key CacheKeyType,
	skipDedupe bool,
	fn func(context.Context) (*CacheValWithCallbacks, error),
	opts ...CacheCallOpt,
) (res CacheResult, err error) {
	var o CacheCallOpts
	for _, opt := range opts {
		opt.SetCacheCallOpt(&o)
	}

	var zeroKey CacheKeyType
	isZero := key == zeroKey

	_, seen := c.seenKeys.LoadOrStore(key, struct{}{})
	if o.Telemetry != nil && (!seen || isZero) {
		telemetryCtx, done := o.Telemetry(ctx)
		defer func() {
			var val Typed
			var cached bool
			if res != nil {
				val = res.Result()
				cached = res.HitCache()
			}
			err = done(val, cached, err)
		}()
		ctx = telemetryCtx
	}

	res, err = c.cache.GetOrInitializeWithCallbacks(ctx, key, skipDedupe, fn)
	if err != nil {
		return nil, err
	}
	if !isZero {
		c.mu.Lock()
		c.results = append(c.results, res)
		c.mu.Unlock()
	}

	return res, nil
}

func (c *SessionCache) ReleaseAll(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var rerr error
	for _, res := range c.results {
		rerr = errors.Join(rerr, res.Release(ctx))
	}
	c.results = nil

	return rerr
}
