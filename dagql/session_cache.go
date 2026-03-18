package dagql

import (
	"context"
	"errors"
	"sync"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
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

func (c *SessionCache) traceCache() *cache {
	inner, _ := c.cache.(*cache)
	return inner
}

func (c *SessionCache) GetOrInitCall(
	ctx context.Context,
	req *CallRequest,
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
	if req == nil {
		return nil, errors.New("call request is nil")
	}
	recipeID, err := c.cache.RecipeIDForCall(req.ResultCall)
	if err != nil {
		return nil, err
	}
	callDigest := recipeID.Digest()

	keys := telemetryKeys(ctx)
	if keys == nil {
		keys = &c.seenKeys
	}
	callKey := callDigest.String()
	switch o.TelemetryPolicy {
	case TelemetryPolicyCacheHitOnly:
		res, err = c.cache.GetOrInitCall(ctx, req, fn)
		if err != nil {
			return nil, err
		}

		if o.Telemetry != nil && res != nil && res.HitCache() {
			_, seen := keys.LoadOrStore(callKey, struct{}{})
			if !seen || req.DoNotCache {
				// track keys globally in addition to any local key stores, otherwise we'll
				// see dupes when e.g. IDs returned out of the "bubble" are loaded
				c.seenKeys.Store(callKey, struct{}{})

				_, done := o.Telemetry(ctx, res)
				done(res, true, &err)
			}
		}

	default:
		_, seen := keys.LoadOrStore(callKey, struct{}{})
		if o.Telemetry != nil && (!seen || req.DoNotCache) {
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

		res, err = c.cache.GetOrInitCall(ctx, req, fn)
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
	var traceCache *cache
	trackedCount := 0
	trackedCountForResult := 0
	if !isClosed && res != nil && !req.DoNotCache {
		c.results = append(c.results, res)
		traceCache = c.traceCache()
		trackedCount = len(c.results)
		if traceCache != nil && traceCache.traceEnabled() {
			if shared := res.cacheSharedResult(); shared != nil {
				for _, tracked := range c.results {
					if tracked == nil {
						continue
					}
					trackedShared := tracked.cacheSharedResult()
					if trackedShared != nil && trackedShared.id == shared.id {
						trackedCountForResult++
					}
				}
			}
		}
	}
	c.mu.Unlock()
	if traceCache != nil && traceCache.traceEnabled() {
		traceCache.traceSessionResultTracked(ctx, c, res, res.HitCache(), trackedCount, trackedCountForResult)
	}

	// if the session cache is closed, ensure we release the result so it doesn't leak
	if isClosed {
		err := errors.New("session cache was closed during execution")
		if res != nil {
			if traceCache := c.traceCache(); traceCache != nil && traceCache.traceEnabled() {
				traceCache.traceSessionResultReleasing(ctx, c, res, "closed_during_execution", 0, 0, 0, 0)
			}
			err = errors.Join(err, res.Release(context.WithoutCancel(ctx)))
		}
		return nil, err
	}

	if nilResult {
		return nil, nil
	}

	return res, nil
}

func (c *SessionCache) RecipeIDForCall(call *ResultCall) (*call.ID, error) {
	return c.cache.RecipeIDForCall(call)
}

func (c *SessionCache) LookupCacheForDigests(
	ctx context.Context,
	recipeDigest digest.Digest,
	extraDigests []call.ExtraDigest,
) (res AnyResult, hit bool, err error) {
	c.mu.Lock()
	if c.isClosed {
		c.mu.Unlock()
		return nil, false, errors.New("session cache is closed")
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

	res, hit, err = c.cache.LookupCacheForDigests(ctx, recipeDigest, extraDigests)
	if err != nil || !hit {
		return res, hit, err
	}
	if res != nil {
		base, baseErr := c.basePersistedCache()
		if baseErr != nil {
			return nil, false, baseErr
		}
		res, err = base.ensurePersistedHitValueLoaded(ctx, CurrentDagqlServer(ctx), res)
		if err != nil {
			return nil, false, err
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
	if !isClosed && res != nil {
		c.results = append(c.results, res)
	}
	c.mu.Unlock()

	if isClosed {
		err := errors.New("session cache was closed during execution")
		if res != nil {
			err = errors.Join(err, res.Release(context.WithoutCancel(ctx)))
		}
		return nil, false, err
	}
	if nilResult {
		return nil, true, nil
	}
	return res, true, nil
}

func (c *SessionCache) lookupCallRequest(
	ctx context.Context,
	req *CallRequest,
) (res AnyResult, hit bool, err error) {
	c.mu.Lock()
	if c.isClosed {
		c.mu.Unlock()
		return nil, false, errors.New("session cache is closed")
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

	base, err := c.basePersistedCache()
	if err != nil {
		return nil, false, err
	}

	res, hit, err = base.lookupCallRequest(ctx, req)
	if err != nil || !hit {
		return res, hit, err
	}

	nilResult := false
	if res != nil {
		if shared := res.cacheSharedResult(); shared != nil && !shared.hasValue {
			nilResult = true
		}
	}

	c.mu.Lock()
	isClosed := c.isClosed
	if !isClosed && res != nil {
		c.results = append(c.results, res)
	}
	c.mu.Unlock()

	if isClosed {
		err := errors.New("session cache was closed during execution")
		if res != nil {
			err = errors.Join(err, res.Release(context.WithoutCancel(ctx)))
		}
		return nil, false, err
	}
	if nilResult {
		return nil, true, nil
	}
	return res, true, nil
}

func (c *SessionCache) TeachCallEquivalentToResult(ctx context.Context, call *ResultCall, res AnyResult) error {
	if res != nil {
		attached, err := c.AttachResult(ctx, res)
		if err != nil {
			return err
		}
		res = attached
	}
	return c.cache.TeachCallEquivalentToResult(ctx, call, res)
}

func (c *SessionCache) AttachResult(ctx context.Context, res AnyResult) (AnyResult, error) {
	return c.cache.AttachResult(ctx, res)
}

func (c *SessionCache) AddExplicitDependency(ctx context.Context, parent AnyResult, dep AnyResult, reason string) error {
	return c.cache.AddExplicitDependency(ctx, parent, dep, reason)
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
	traceCache := c.traceCache()
	trackedCountByResult := make(map[sharedResultID]int)
	for _, res := range results {
		if res == nil {
			continue
		}
		if shared := res.cacheSharedResult(); shared != nil {
			trackedCountByResult[shared.id]++
		}
	}
	releasedCountByResult := make(map[sharedResultID]int, len(trackedCountByResult))
	for i, res := range results {
		if res == nil {
			continue
		}
		ordinalForResult := 0
		trackedCountForResult := 0
		if shared := res.cacheSharedResult(); shared != nil {
			ordinalForResult = releasedCountByResult[shared.id] + 1
			releasedCountByResult[shared.id] = ordinalForResult
			trackedCountForResult = trackedCountByResult[shared.id]
		}
		if traceCache != nil && traceCache.traceEnabled() {
			traceCache.traceSessionResultReleasing(ctx, c, res, "release_and_close", i+1, len(results), trackedCountForResult, ordinalForResult)
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
