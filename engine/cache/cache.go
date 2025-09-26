package cache

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/dagger/dagger/core/modfunccache"
	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
)

type Cache[K KeyType, V any] interface {
	// Using the given key, either return an already cached value for that key or initialize
	// an entry in the cache with the given value for that key.
	GetOrInitializeValue(context.Context, CacheKey[K], V) (Result[K, V], error)

	// Using the given key, either return an already cached value for that key or initialize a
	// new value using the given function. If the function returns an error, the error is returned.
	GetOrInitialize(
		context.Context,
		CacheKey[K],
		func(context.Context) (V, error),
	) (Result[K, V], error)

	// Using the given key, either return an already cached value for that key or initialize a
	// new value using the given function. If the function returns an error, the error is returned.
	// The function returns a ValueWithCallbacks struct that contains the value and optionally
	// any additional callbacks for various parts of the cache lifecycle.
	GetOrInitializeWithCallbacks(
		context.Context,
		CacheKey[K],
		func(context.Context) (*ValueWithCallbacks[V], error),
	) (Result[K, V], error)

	// Returns the number of entries in the cache.
	Size() int
}

type Result[K KeyType, V any] interface {
	Result() V
	Release(context.Context) error
	PostCall(context.Context) error
	HitCache() bool
}

type KeyType = interface {
	~string
}

type CacheKey[K KeyType] struct {
	// CallKey is identifies the completed result of this call. If a call has already
	// been completed with this CallKey, its cached result will be returned.
	//
	// If CallKey is the zero value for K, the call will not be cached and will always
	// run.
	// TODO: update docs
	CallKey K

	// ConcurrencyKey is used to determine whether *in-progress* calls should be deduplicated.
	// If a call with a given (ResultKey, ConcurrencyKey) pair is already in progress, and
	// another one comes in with the same pair, the second caller will wait for the first
	// to complete and receive the same result.
	//
	// If two calls have the same ResultKey but different ConcurrencyKeys, they will not be deduped.
	//
	// If ConcurrencyKey is the zero value for K, no deduplication of in-progress calls will be done.
	ConcurrencyKey K

	// TODO: doc
	TTL int64
}

type PostCallFunc = func(context.Context) error

type OnReleaseFunc = func(context.Context) error

type ValueWithCallbacks[V any] struct {
	// The actual value to cache
	Value V

	// If set, a function that should be called whenever the value is returned from the cache (whether newly initialized or not)
	PostCall PostCallFunc

	// If set, this function will be called when a result is removed from the cache
	OnRelease OnReleaseFunc

	// TODO: doc
	SafeToPersistCache bool
}

// TODO: cleanup/re-assess
type ctxStorageKey struct{}

func CurrentStorageKey(ctx context.Context) string {
	if v := ctx.Value(ctxStorageKey{}); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func ctxWithStorageKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, ctxStorageKey{}, key)
}

var ErrCacheRecursiveCall = fmt.Errorf("recursive call detected")

func NewCache[K KeyType, V any](ctx context.Context, dbPath string) (Cache[K, V], error) {
	c := &cache[K, V]{}

	if dbPath == "" {
		return c, nil
	}

	connURL := &url.URL{
		Scheme: "file",
		Path:   dbPath,
		RawQuery: url.Values{
			"_pragma": []string{
				// ref: https://www.sqlite.org/pragma.html
				"journal_mode=WAL",
				"busy_timeout=10000", // wait up to 10s when there are concurrent writers
				// TODO: handle loading corrupt db on startup
				"synchronous=OFF",

				// TODO: ?
				// cache_size
				// threads
				// optimize https://www.sqlite.org/pragma.html#pragma_optimize
			},
			"_txlock": []string{"immediate"}, // use BEGIN IMMEDIATE for transactions
		}.Encode(),
	}
	db, err := sql.Open("sqlite", connURL.String())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", connURL, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping %s: %w", connURL, err)
	}
	if _, err := db.Exec(modfunccache.Schema); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	c.db, err = modfunccache.Prepare(ctx, db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("prepare queries: %w", err)
	}

	return c, nil
}

type cache[K KeyType, V any] struct {
	mu sync.Mutex

	// calls that are in progress, keyed by a combination of the call key and the concurrency key
	// two calls with the same call+concurrency key will be "single-flighted" (only one will actually run)
	ongoingCalls map[string]*result[K, V]

	// calls that have completed successfully and are cached, keyed just by the call key
	completedCalls map[string]*result[K, V]

	// TODO: doc
	db *modfunccache.Queries
}

var _ Cache[string, string] = &cache[string, string]{}

type result[K KeyType, V any] struct {
	cache *cache[K, V]

	// TODO: doc, probably find some better names
	callKey        string
	storageKey     string
	concurrencyKey string

	val                V
	err                error
	safeToPersistCache bool

	persist   func(context.Context) error
	postCall  PostCallFunc
	onRelease OnReleaseFunc

	waitCh  chan struct{}
	cancel  context.CancelCauseFunc
	waiters int

	refCount int
}

// perCallResult wraps result with metadata that is specific to a single call,
// as opposed to the shared metadata for all instances of a result. e.g. whether
// the result was returned from cache or new.
type perCallResult[K KeyType, V any] struct {
	*result[K, V]

	hitCache bool
}

func (r *perCallResult[K, V]) HitCache() bool {
	return r.hitCache
}

var _ Result[string, string] = &perCallResult[string, string]{}

type cacheContextKey[K KeyType, V any] struct {
	key   K
	cache *cache[K, V]
}

func (c *cache[K, V]) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.ongoingCalls) + len(c.completedCalls)
}

func (c *cache[K, V]) GetOrInitializeValue(
	ctx context.Context,
	key CacheKey[K],
	val V,
) (Result[K, V], error) {
	return c.GetOrInitialize(ctx, key, func(_ context.Context) (V, error) {
		return val, nil
	})
}

func (c *cache[K, V]) GetOrInitialize(
	ctx context.Context,
	key CacheKey[K],
	fn func(context.Context) (V, error),
) (Result[K, V], error) {
	return c.GetOrInitializeWithCallbacks(ctx, key, func(ctx context.Context) (*ValueWithCallbacks[V], error) {
		val, err := fn(ctx)
		if err != nil {
			return nil, err
		}
		return &ValueWithCallbacks[V]{Value: val}, nil
	})
}

func (c *cache[K, V]) GetOrInitializeWithCallbacks(
	ctx context.Context,
	key CacheKey[K],
	fn func(context.Context) (*ValueWithCallbacks[V], error),
) (Result[K, V], error) {
	var zeroKey K
	if key.CallKey == zeroKey {
		// don't cache, don't dedupe calls, just call it
		valWithCallbacks, err := fn(ctx)
		if err != nil {
			return nil, err
		}
		res := &perCallResult[K, V]{result: &result[K, V]{}}
		if valWithCallbacks != nil {
			res.val = valWithCallbacks.Value
			res.postCall = valWithCallbacks.PostCall
			res.onRelease = valWithCallbacks.OnRelease
		}
		return res, nil
	}

	// TODO: cleanup
	callKey := string(key.CallKey)
	concurrencyKey := digest.FromString(callKey + ":" + string(key.ConcurrencyKey)).String()

	storageKey := callKey

	var saveStorageKey func(context.Context) error
	if key.TTL != 0 && c.db != nil {
		call, err := c.db.SelectCall(ctx, callKey)
		switch {
		case err == nil:
		case errors.Is(err, sql.ErrNoRows):
		default:
			// TODO: lower to log out of caution?
			return nil, fmt.Errorf("select call: %w", err)
		}

		now := time.Now().Unix()
		newExpiration := now + key.TTL

		switch {
		case call == nil:
			// Nothing saved in the cache yet, use a new storage key. Don't save yet, that only happens
			// once a call completes successfully and has been determined to be safe to cache.
			expirationMixin := intToStr(newExpiration)
			storageKey = digest.FromString(storageKey + "\x00" + expirationMixin).String()
			saveStorageKey = func(ctx context.Context) error {
				return c.db.SetExpiration(ctx, modfunccache.SetExpirationParams{
					CallKey:        string(key.CallKey),
					StorageKey:     storageKey,
					Expiration:     newExpiration,
					PrevStorageKey: "",
				})
			}

		case call.Expiration < now:
			// We do have a cached entry, but it expired, so don't use it. Use a new mixin, but again
			// don't store it yet until the call completes successfully and is determined to be safe
			// to cache.
			expirationMixin := intToStr(newExpiration)
			storageKey = digest.FromString(storageKey + "\x00" + expirationMixin).String()
			saveStorageKey = func(ctx context.Context) error {
				return c.db.SetExpiration(ctx, modfunccache.SetExpirationParams{
					CallKey:        string(key.CallKey),
					StorageKey:     storageKey,
					Expiration:     newExpiration,
					PrevStorageKey: call.StorageKey,
				})
			}

		default:
			// We have a cached entry and it hasn't expired yet, use the cached mixin
			storageKey = call.StorageKey
			saveStorageKey = func(context.Context) error { return nil }
		}
	}

	ctx = ctxWithStorageKey(ctx, storageKey)

	if ctx.Value(cacheContextKey[K, V]{K(storageKey), c}) != nil {
		return nil, ErrCacheRecursiveCall
	}

	c.mu.Lock()
	if c.ongoingCalls == nil {
		c.ongoingCalls = make(map[string]*result[K, V])
	}
	if c.completedCalls == nil {
		c.completedCalls = make(map[string]*result[K, V])
	}

	if res, ok := c.completedCalls[storageKey]; ok {
		res.refCount++
		c.mu.Unlock()
		return &perCallResult[K, V]{
			result:   res,
			hitCache: true,
		}, nil
	}

	if key.ConcurrencyKey != zeroKey {
		if res, ok := c.ongoingCalls[concurrencyKey]; ok {
			// already an ongoing call
			res.waiters++
			c.mu.Unlock()
			return c.wait(ctx, res, false)
		}
	}

	// make a new call with ctx that's only canceled when all caller contexts are canceled
	callCtx := context.WithValue(ctx, cacheContextKey[K, V]{K(storageKey), c}, struct{}{})
	callCtx, cancel := context.WithCancelCause(context.WithoutCancel(callCtx))
	res := &result[K, V]{
		cache: c,

		callKey:        string(key.CallKey),
		storageKey:     storageKey,
		concurrencyKey: concurrencyKey,

		// TODO: rename persist?
		persist: saveStorageKey,

		waitCh:  make(chan struct{}),
		cancel:  cancel,
		waiters: 1,
	}

	if key.ConcurrencyKey != zeroKey {
		c.ongoingCalls[concurrencyKey] = res
	}

	go func() {
		defer close(res.waitCh)
		valWithCallbacks, err := fn(callCtx)
		res.err = err
		if valWithCallbacks != nil {
			res.val = valWithCallbacks.Value
			res.postCall = valWithCallbacks.PostCall
			res.onRelease = valWithCallbacks.OnRelease
			res.safeToPersistCache = valWithCallbacks.SafeToPersistCache
		}
	}()

	c.mu.Unlock()
	perCallRes, err := c.wait(ctx, res, true)
	if err != nil {
		return nil, err
	}

	// ensure that this is never marked as hit cache, even in the case
	// where fn returned very quickly and was done by the time wait got
	// called
	perCallRes.hitCache = false
	return perCallRes, nil
}

func intToStr(i int64) string {
	return string(binary.BigEndian.AppendUint64(nil, uint64(i)))
}

func (c *cache[K, V]) wait(ctx context.Context, res *result[K, V], isFirstCaller bool) (*perCallResult[K, V], error) {
	var hitCache bool
	var err error

	// first check just if the call is done already, if it is we consider it a cache hit
	select {
	case <-res.waitCh:
		hitCache = true
		err = res.err
	default:
		// call wasn't done in fast path check, wait for either the call to
		// be done or the caller's ctx to be canceled
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
		// no one else is waiting, can cancel the callCtx
		res.cancel(err)
	}

	if err == nil {
		delete(c.ongoingCalls, res.concurrencyKey)
		if existingRes, ok := c.completedCalls[res.storageKey]; ok {
			res = existingRes
		} else {
			c.completedCalls[res.storageKey] = res
		}

		res.refCount++
		c.mu.Unlock()

		if isFirstCaller && res.persist != nil && res.safeToPersistCache {
			err := res.persist(ctx)
			if err != nil {
				// TODO: if you make this a returned error, be sure to release it
				slog.Error("failed to persist cache expiration", "err", err)
			}
		}

		return &perCallResult[K, V]{
			result:   res,
			hitCache: hitCache,
		}, nil
	}

	if res.refCount == 0 && res.waiters == 0 {
		// error happened and no refs left, delete it now
		delete(c.ongoingCalls, res.concurrencyKey)
		delete(c.completedCalls, res.storageKey)
	}

	c.mu.Unlock()
	return nil, err
}

func (res *result[K, V]) Result() V {
	if res == nil {
		var zero V
		return zero
	}
	return res.val
}

func (res *result[K, V]) Release(ctx context.Context) error {
	if res == nil || res.cache == nil {
		// wasn't cached, nothing to do
		return nil
	}

	res.cache.mu.Lock()
	res.refCount--
	var onRelease OnReleaseFunc
	if res.refCount == 0 && res.waiters == 0 {
		// no refs left and no one waiting on it, delete from cache
		delete(res.cache.ongoingCalls, res.concurrencyKey)
		delete(res.cache.completedCalls, res.storageKey)
		onRelease = res.onRelease
	}
	res.cache.mu.Unlock()

	if onRelease != nil {
		return onRelease(ctx)
	}
	return nil
}

func (res *result[K, V]) PostCall(ctx context.Context) error {
	if res.postCall == nil {
		return nil
	}
	return res.postCall(ctx)
}
