package dagql

import (
	"context"
	"errors"
	"sync"
)

type SessionCache struct {
	cache Cache

	results          []AnyResult
	arbitraryResults []ArbitraryCachedResult
	mu               sync.Mutex

	// isClosed is set to true when ReleaseAndClose is called.
	// Any in-progress results will be released and errors returned.
	isClosed bool

	seenKeys sync.Map

	// noCacheNext keeps track of keys for which the next cache attempt should bypass the cache.
	noCacheNext sync.Map
}

func NewSessionCache(
	baseCache Cache,
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

type TelemetryFunc func(context.Context) (context.Context, func(AnyResult, bool, *error))

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
	return WithNonInternalTelemetry(
		context.WithValue(ctx, seenKeysCtxKey{}, &sync.Map{}),
	)
}

// WithNonInternalTelemetry marks telemetry within the context as non-internal,
// so that Server.Select does not mark its spans internal.
func WithNonInternalTelemetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, internalKey{}, false)
}

func telemetryKeys(ctx context.Context) *sync.Map {
	if v := ctx.Value(seenKeysCtxKey{}); v != nil {
		return v.(*sync.Map)
	}
	return nil
}

func (c *SessionCache) GetOrInitCall(
	ctx context.Context,
	key CacheKey,
	fn func(context.Context) (AnyResult, error),
	opts ...CacheCallOpt,
) (res AnyResult, err error) {
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
	callKey := key.ID.Digest().String()
	_, seen := keys.LoadOrStore(callKey, struct{}{})
	if o.Telemetry != nil && (!seen || key.DoNotCache) {
		// track keys globally in addition to any local key stores, otherwise we'll
		// see dupes when e.g. IDs returned out of the "bubble" are loaded
		c.seenKeys.Store(callKey, struct{}{})

		telemetryCtx, done := o.Telemetry(ctx)
		defer func() {
			var cached bool
			if res != nil {
				cached = res.HitCache()
			}
			done(res, cached, &err)
		}()
		ctx = telemetryCtx
	}

	// If this callKey previously failed, force the next attempt to be DoNotCache
	// to bypass any potentially stale cached error.
	forcedDoNotCache := false
	if _, ok := c.noCacheNext.Load(callKey); ok && !key.DoNotCache {
		key.DoNotCache = true
		forcedDoNotCache = true
	}

	ctx = withTrackNilResult(ctx)

	res, err = c.cache.GetOrInitCall(ctx, key, fn)
	if err != nil {
		// mark that the next attempt should run with DoNotCache
		c.noCacheNext.Store(callKey, struct{}{})
		return nil, err
	}

	// success: we're in a good state now, allow normal caching again
	c.noCacheNext.Delete(callKey)

	nilResult := false
	if valueRes, ok := res.(cacheValueResult); ok && !valueRes.cacheHasValue() {
		nilResult = true
	}

	// If we forced DoNotCache due to a prior failure, we need to re-insert the successful
	// result into the underlying cache under the original key so subsequent calls find it.
	// The call above used a random storage key (due to DoNotCache=true), so the result
	// won't be found by future lookups using the original key.
	// NOTE: this complication can be removed once we are off the buildkit solver as errors
	// won't be cached for the duration of a session.
	if forcedDoNotCache {
		key.DoNotCache = false
		cachedRes, cacheErr := c.cache.GetOrInitCall(ctx, key, ValueFunc(res))
		if cacheErr == nil {
			res = cachedRes
		}
		// If caching fails, we still return the successful result, just won't be cached
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// if the session cache is closed, ensure we release the result so it doesn't leak
	if c.isClosed {
		err := errors.New("session cache was closed during execution")
		if res != nil {
			err = errors.Join(err, res.Release(context.WithoutCancel(ctx)))
		}
		return nil, err
	}

	if res != nil && (!key.DoNotCache || forcedDoNotCache) {
		c.results = append(c.results, res)
	}

	if nilResult {
		return nil, nil
	}

	return res, nil
}

func (c *SessionCache) GetOrInitArbitrary(
	ctx context.Context,
	callKey string,
	fn func(context.Context) (any, error),
) (ArbitraryCachedResult, error) {
	c.mu.Lock()
	if c.isClosed {
		c.mu.Unlock()
		return nil, errors.New("session cache is closed")
	}
	c.mu.Unlock()

	res, err := c.cache.GetOrInitArbitrary(ctx, callKey, fn)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isClosed {
		closeErr := errors.New("session cache was closed during execution")
		if res != nil {
			closeErr = errors.Join(closeErr, res.Release(context.WithoutCancel(ctx)))
		}
		return nil, closeErr
	}

	if res == nil {
		return nil, nil
	}
	c.arbitraryResults = append(c.arbitraryResults, res)
	return res, nil
}

func (c *SessionCache) ReleaseAndClose(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.isClosed = true

	var rerr error
	for _, res := range c.results {
		if res == nil {
			continue
		}
		rerr = errors.Join(rerr, res.Release(context.WithoutCancel(ctx)))
	}
	c.results = nil
	for _, res := range c.arbitraryResults {
		if res == nil {
			continue
		}
		rerr = errors.Join(rerr, res.Release(context.WithoutCancel(ctx)))
	}
	c.arbitraryResults = nil

	return rerr
}
