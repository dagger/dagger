package dagql

import (
	"context"
	"errors"
	"sync"

	"github.com/dagger/dagger/engine/cache"
)

type CacheKeyType = string
type CacheValueType = AnyResult

type CacheKey = cache.CacheKey[CacheKeyType]

type CacheResult = cache.Result[CacheKeyType, CacheValueType]

type CacheValWithCallbacks = cache.ValueWithCallbacks[CacheValueType]

type SessionCache struct {
	cache cache.Cache[CacheKeyType, CacheValueType]

	results []cache.Result[CacheKeyType, CacheValueType]
	mu      sync.Mutex

	// isClosed is set to true when ReleaseAndClose is called.
	// Any in-progress results will be released and errors returned.
	isClosed bool

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

type TelemetryFunc func(context.Context) (context.Context, func(AnyResult, bool, error))

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
	key cache.CacheKey[CacheKeyType],
	val CacheValueType,
	opts ...CacheCallOpt,
) (CacheResult, error) {
	return c.GetOrInitialize(ctx, key, func(_ context.Context) (CacheValueType, error) {
		return val, nil
	}, opts...)
}

func (c *SessionCache) GetOrInitialize(
	ctx context.Context,
	key cache.CacheKey[CacheKeyType],
	fn func(context.Context) (CacheValueType, error),
	opts ...CacheCallOpt,
) (CacheResult, error) {
	return c.GetOrInitializeWithCallbacks(ctx, key, func(ctx context.Context) (*CacheValWithCallbacks, error) {
		val, err := fn(ctx)
		if err != nil {
			return nil, err
		}
		return &CacheValWithCallbacks{Value: val}, nil
	}, opts...)
}

type seenKeysCtxKey struct{}

// WithRepeatedTelemetry resets the state of seen cache keys so that we emit
// telemetry for spans that we've already seen within the session.
//
// This is useful in scenarios where we want to see actions performed, even if
// they had been performed already (e.g. an LLM running tools).
//
// Additionally, it explicitly sets the internal flag to false, to prevent
// Server.Select from marking its spans internal.
func WithRepeatedTelemetry(ctx context.Context) context.Context {
	return context.WithValue(
		context.WithValue(ctx, seenKeysCtxKey{}, &sync.Map{}),
		internalKey{},
		false,
	)
}

func telemetryKeys(ctx context.Context) *sync.Map {
	if v := ctx.Value(seenKeysCtxKey{}); v != nil {
		return v.(*sync.Map)
	}
	return nil
}

func (c *SessionCache) GetOrInitializeWithCallbacks(
	ctx context.Context,
	key cache.CacheKey[CacheKeyType],
	fn func(context.Context) (*CacheValWithCallbacks, error),
	opts ...CacheCallOpt,
) (res CacheResult, err error) {
	// do a quick check to see if the cache is closed; we do another check
	// at the end in case the cache is closed while we're waiting for the call
	c.mu.Lock()
	if c.isClosed {
		c.mu.Unlock()
		return nil, errors.New("session cache is closed")
	}
	c.mu.Unlock()

	var o CacheCallOpts
	for _, opt := range opts {
		opt.SetCacheCallOpt(&o)
	}

	keys := telemetryKeys(ctx)
	if keys == nil {
		keys = &c.seenKeys
	}
	_, seen := keys.LoadOrStore(key.CallKey, struct{}{})
	if o.Telemetry != nil && (!seen || key.DoNotCache) {
		// track keys globally in addition to any local key stores, otherwise we'll
		// see dupes when e.g. IDs returned out of the "bubble" are loaded
		c.seenKeys.Store(key.CallKey, struct{}{})

		telemetryCtx, done := o.Telemetry(ctx)
		defer func() {
			var val AnyResult
			var cached bool
			if res != nil {
				val = res.Result()
				cached = res.HitCache()
			}
			done(val, cached, err)
		}()
		ctx = telemetryCtx
	}

	res, err = c.cache.GetOrInitializeWithCallbacks(ctx, key, fn)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// if the session cache is closed, ensure we release the result so it doesn't leak
	if c.isClosed {
		err := errors.New("session cache was closed during execution")
		err = errors.Join(err, res.Release(context.WithoutCancel(ctx)))
		return nil, err
	}

	if !key.DoNotCache {
		c.results = append(c.results, res)
	}

	return res, nil
}

func (c *SessionCache) ReleaseAndClose(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.isClosed = true

	var rerr error
	for _, res := range c.results {
		rerr = errors.Join(rerr, res.Release(ctx))
	}
	c.results = nil

	return rerr
}
