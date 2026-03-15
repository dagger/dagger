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
	inflight         int
	cond             *sync.Cond

	// isClosed is set to true when ReleaseAndClose is called.
	// Any in-progress results will be released and errors returned.
	isClosed bool

	seenKeys sync.Map
}

func NewSessionCache(
	baseCache Cache,
) *SessionCache {
	sc := &SessionCache{
		cache: baseCache,
	}
	sc.cond = sync.NewCond(&sc.mu)
	return sc
}

type CacheCallOpt interface {
	SetCacheCallOpt(*CacheCallOpts)
}

type CacheCallOpts struct {
	Telemetry       TelemetryFunc
	TelemetryPolicy TelemetryPolicy
}

type TelemetryFunc func(context.Context, AnyResult) (context.Context, func(AnyResult, bool, *error))

// TelemetryPolicy controls when telemetry is emitted for a cache call.
type TelemetryPolicy int

const (
	// TelemetryPolicyDefault emits telemetry around first-seen calls.
	TelemetryPolicyDefault TelemetryPolicy = iota
	// TelemetryPolicyCacheHitOnly emits telemetry only when the call result is a cache hit.
	TelemetryPolicyCacheHitOnly
)

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

func WithTelemetryPolicy(policy TelemetryPolicy) CacheCallOpt {
	return CacheCallOptFunc(func(opts *CacheCallOpts) {
		opts.TelemetryPolicy = policy
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
	c.inflight++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.inflight--
		if c.inflight == 0 {
			c.cond.Broadcast()
		}
		c.mu.Unlock()
	}()

	var o CacheCallOpts
	for _, opt := range opts {
		opt.SetCacheCallOpt(&o)
	}
	if key.ID == nil {
		return nil, errors.New("cache key ID is nil")
	}

	keys := telemetryKeys(ctx)
	if keys == nil {
		keys = &c.seenKeys
	}
	callKey := key.ID.Digest().String()
	switch o.TelemetryPolicy {
	case TelemetryPolicyCacheHitOnly:
		res, err = c.cache.GetOrInitCall(ctx, key, fn)
		if err != nil {
			return nil, err
		}

		if o.Telemetry != nil && res != nil && res.HitCache() {
			_, seen := keys.LoadOrStore(callKey, struct{}{})
			if !seen || key.DoNotCache {
				// track keys globally in addition to any local key stores, otherwise we'll
				// see dupes when e.g. IDs returned out of the "bubble" are loaded
				c.seenKeys.Store(callKey, struct{}{})

				_, done := o.Telemetry(ctx, res)
				done(res, true, &err)
			}
		}

	default:
		_, seen := keys.LoadOrStore(callKey, struct{}{})
		if o.Telemetry != nil && (!seen || key.DoNotCache) {
			// track keys globally in addition to any local key stores, otherwise we'll
			// see dupes when e.g. IDs returned out of the "bubble" are loaded
			c.seenKeys.Store(callKey, struct{}{})

			telemetryCtx, done := o.Telemetry(ctx, nil)
			defer func() {
				var cached bool
				if res != nil {
					cached = res.HitCache()
				}
				done(res, cached, &err)
			}()
			ctx = telemetryCtx
		}

		res, err = c.cache.GetOrInitCall(ctx, key, fn)
		if err != nil {
			return nil, err
		}
	}

	nilResult := false
	if res != nil {
		if shared := res.cacheSharedResult(); shared != nil && !shared.hasValue {
			nilResult = true
		}
	}

	c.mu.Lock()
	isClosed := c.isClosed
	if !isClosed && res != nil && !key.DoNotCache {
		c.results = append(c.results, res)
	}
	c.mu.Unlock()

	// if the session cache is closed, ensure we release the result so it doesn't leak
	if isClosed {
		err := errors.New("session cache was closed during execution")
		if res != nil {
			err = errors.Join(err, res.Release(context.WithoutCancel(ctx)))
		}
		return nil, err
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
	c.inflight++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.inflight--
		if c.inflight == 0 {
			c.cond.Broadcast()
		}
		c.mu.Unlock()
	}()

	res, err := c.cache.GetOrInitArbitrary(ctx, callKey, fn)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	isClosed := c.isClosed
	if !isClosed && res != nil {
		c.arbitraryResults = append(c.arbitraryResults, res)
	}
	c.mu.Unlock()

	if isClosed {
		closeErr := errors.New("session cache was closed during execution")
		if res != nil {
			closeErr = errors.Join(closeErr, res.Release(context.WithoutCancel(ctx)))
		}
		return nil, closeErr
	}

	if res == nil {
		return nil, nil
	}
	return res, nil
}

func (c *SessionCache) ReleaseAndClose(ctx context.Context) error {
	c.mu.Lock()
	c.isClosed = true
	for c.inflight > 0 {
		c.cond.Wait()
	}
	results := c.results
	arbitraryResults := c.arbitraryResults
	c.results = nil
	c.arbitraryResults = nil
	c.mu.Unlock()

	var rerr error
	for _, res := range results {
		if res == nil {
			continue
		}
		rerr = errors.Join(rerr, res.Release(context.WithoutCancel(ctx)))
	}
	for _, res := range arbitraryResults {
		if res == nil {
			continue
		}
		rerr = errors.Join(rerr, res.Release(context.WithoutCancel(ctx)))
	}

	return rerr
}
