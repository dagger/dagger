package cache

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/dagger/dagger/engine"
	cachedb "github.com/dagger/dagger/engine/cache/db"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/hashutil"
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

	// Run a blocking loop that periodically garbage collects expired entries from the cache db.
	GCLoop(context.Context)
}

type Result[K KeyType, V any] interface {
	Result() V
	Release(context.Context) error
	PostCall(context.Context) error
	HitCache() bool
	HitContentDigestCache() bool
}

type KeyType = interface {
	~string
}

type CacheKey[K KeyType] struct {
	// CallKey is identifies the the call. If a call has already been completed with this
	// CallKey and it is not expired, its cached result will be returned.
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

	// TTL is the time-to-live for the cached result of this call, in seconds.
	TTL int64

	// DoNotCache indicates that this call should not be cached at all, simply ran.
	DoNotCache bool

	// An optional separate key representing the content digest of the result. The CallKey is preferred
	// for cache hits, but the content digest key is checked as a secondary fallback if it is set.
	ContentDigestKey K
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

	// If true, indicates that it is safe to persist this value in the cache db (i.e. does not have
	// any in-memory only data).
	SafeToPersistCache bool

	// An optional separate key representing the content digest of the result.
	ContentDigestKey string

	// An optional separate call key for the result. This may be set for instance when a call returns a result from
	// a separate call, in which case ResultCallKey would be the call key of that separate call.
	// e.g. address("/foo").directory may return a result with ResultCallKey for the call host.directory("/foo")
	ResultCallKey string
}

type ctxStorageKey struct{}

// Get the key that should be used (or mixed into) persistent cache storage
// We smuggle this around in the context for now since we have to incorporate
// it with buildkit's persistent cache for now.
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
			"_pragma": []string{ // ref: https://www.sqlite.org/pragma.html
				// WAL mode for better concurrency behavior and performance
				"journal_mode=WAL",

				// wait up to 10s when there are concurrent writers
				"busy_timeout=10000",

				// for now, it's okay if we lose cache after a catastrophic crash
				// (it's just a cache afterall), we'll take the better performance
				"synchronous=OFF",

				// other pragmas to possible worth consideration someday:
				// cache_size
				// threads
				// optimize
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
	if _, err := db.Exec(cachedb.Schema); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	c.db, err = cachedb.Prepare(ctx, db)
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
	ongoingCalls map[callConcurrencyKeys]*result[K, V]

	// calls that have completed successfully and are cached, keyed by the storage key
	completedCalls map[string]*resultList[K, V]

	// calls that have completed successfully and are cached, keyed by content digest key
	completedCallsByContent map[string]*resultList[K, V]

	// db for persistence; currently only used for metadata supporting ttl-based expiration
	db *cachedb.Queries
}

type callConcurrencyKeys struct {
	callKey        string
	concurrencyKey string
}

var _ Cache[string, string] = &cache[string, string]{}

type result[K KeyType, V any] struct {
	cache *cache[K, V]

	storageKey          string              // key to cache.completedCalls
	callConcurrencyKeys callConcurrencyKeys // key to cache.ongoingCalls

	val                V
	err                error
	safeToPersistCache bool

	// optional content digest key set on the result
	contentDigestKey K
	// optional result call key set on the result (see ValueWithCallbacks.ResultCallKey)
	resultCallKey K

	persistToDB func(context.Context) error
	postCall    PostCallFunc
	onRelease   OnReleaseFunc

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

	// whether there was a cache hit for this call
	hitCache bool
	// whether there was a content digest cache hit specifically for this call
	hitContentDigestCache bool
}

func (r *perCallResult[K, V]) HitCache() bool {
	return r.hitCache
}

func (r *perCallResult[K, V]) HitContentDigestCache() bool {
	return r.hitContentDigestCache
}

var _ Result[string, string] = &perCallResult[string, string]{}

type cacheContextKey[K KeyType, V any] struct {
	key   K
	cache *cache[K, V]
}

func (c *cache[K, V]) GCLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		now := time.Now().Unix()
		if err := c.db.GCExpiredCalls(ctx, cachedb.GCExpiredCallsParams{
			Now: now,
		}); err != nil {
			slog.Warn("failed to GC expired function calls", "err", err)
		}
	}
}

func (c *cache[K, V]) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	total := len(c.ongoingCalls)
	for _, lst := range c.completedCalls {
		total += lst.len()
	}
	for _, lst := range c.completedCallsByContent {
		total += lst.len()
	}
	return total
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
	if key.DoNotCache {
		// don't cache, don't dedupe calls, just call it

		// we currently still have to appease the buildkit cache key machinery underlying function calls,
		// so make sure it gets a random storage key
		ctx = ctxWithStorageKey(ctx, rand.Text())

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

	callKey := string(key.CallKey)
	callConcKeys := callConcurrencyKeys{
		callKey:        callKey,
		concurrencyKey: string(key.ConcurrencyKey),
	}

	// The storage key is the key for what's actually stored on disk.
	// By default it's just the call key, but if we have a TTL then there
	// can be different results stored on disk for a single call key, necessitating
	// this separate storage key.
	storageKey := callKey

	var persistToDB func(context.Context) error
	if key.TTL != 0 && c.db != nil {
		call, err := c.db.SelectCall(ctx, callKey)
		if err == nil || errors.Is(err, sql.ErrNoRows) {
			now := time.Now().Unix()
			expiration := now + key.TTL

			// TODO:(sipsma) we unfortunately have to incorporate the session ID into the storage key
			// for now in order to get functions that make SetSecret calls to behave as "per-session"
			// caches (while *also* retaining the correct behavior in all other cases). It would be
			// nice to find some more elegant way of modeling this that disentangles this cache
			// from engine client metadata.
			switch {
			case call == nil:
				md, err := engine.ClientMetadataFromContext(ctx)
				if err != nil {
					return nil, fmt.Errorf("get client metadata: %w", err)
				}
				storageKey = hashutil.NewHasher().
					WithString(storageKey).
					WithString(md.SessionID).
					DigestAndClose()

				// Nothing saved in the cache yet, use a new expiration. Don't save yet, that only happens
				// once a call completes successfully and has been determined to be safe to cache.
				persistToDB = func(ctx context.Context) error {
					return c.db.SetExpiration(ctx, cachedb.SetExpirationParams{
						CallKey:        string(key.CallKey),
						StorageKey:     storageKey,
						Expiration:     expiration,
						PrevStorageKey: "",
					})
				}

			case call.Expiration < now:
				md, err := engine.ClientMetadataFromContext(ctx)
				if err != nil {
					return nil, fmt.Errorf("get client metadata: %w", err)
				}
				storageKey = hashutil.NewHasher().
					WithString(storageKey).
					WithString(md.SessionID).
					DigestAndClose()

				// We do have a cached entry, but it expired, so don't use it. Use a new expiration, but again
				// don't store it yet until the call completes successfully and is determined to be safe
				// to cache.
				persistToDB = func(ctx context.Context) error {
					return c.db.SetExpiration(ctx, cachedb.SetExpirationParams{
						CallKey:        string(key.CallKey),
						StorageKey:     storageKey,
						Expiration:     expiration,
						PrevStorageKey: call.StorageKey,
					})
				}

			default:
				// We have a cached entry and it hasn't expired yet, use it
				storageKey = call.StorageKey
			}
		} else {
			slog.Error("failed to select call from cache", "err", err)
		}
	}

	ctx = ctxWithStorageKey(ctx, storageKey)

	if ctx.Value(cacheContextKey[K, V]{K(storageKey), c}) != nil {
		return nil, ErrCacheRecursiveCall
	}

	c.mu.Lock()
	if c.ongoingCalls == nil {
		c.ongoingCalls = make(map[callConcurrencyKeys]*result[K, V])
	}
	if c.completedCalls == nil {
		c.completedCalls = make(map[string]*resultList[K, V])
	}
	if c.completedCallsByContent == nil {
		c.completedCallsByContent = make(map[string]*resultList[K, V])
	}

	if lst, ok := c.completedCalls[storageKey]; ok {
		if res := lst.first(); res != nil {
			res.refCount++
			c.mu.Unlock()
			return &perCallResult[K, V]{
				result:   res,
				hitCache: true,
			}, nil
		}
	}

	if key.ContentDigestKey != "" {
		if lst, ok := c.completedCallsByContent[string(key.ContentDigestKey)]; ok {
			if res := lst.first(); res != nil {
				res.refCount++
				c.mu.Unlock()
				return &perCallResult[K, V]{
					result:                res,
					hitCache:              true,
					hitContentDigestCache: true,
				}, nil
			}
		}
	}

	var zeroKey K
	if key.ConcurrencyKey != zeroKey {
		if res, ok := c.ongoingCalls[callConcKeys]; ok {
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

		storageKey:          storageKey,
		callConcurrencyKeys: callConcKeys,

		persistToDB: persistToDB,

		waitCh:  make(chan struct{}),
		cancel:  cancel,
		waiters: 1,
	}

	if key.ConcurrencyKey != zeroKey {
		c.ongoingCalls[callConcKeys] = res
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
			res.contentDigestKey = K(valWithCallbacks.ContentDigestKey)
			res.resultCallKey = K(valWithCallbacks.ResultCallKey)
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
		delete(c.ongoingCalls, res.callConcurrencyKeys)
		lst, ok := c.completedCalls[res.storageKey]
		if ok {
			if existing := lst.first(); existing != nil {
				res = existing
			} else {
				lst.add(res)
			}
		} else {
			lst = newResultList[K, V]()
			lst.add(res)
			c.completedCalls[res.storageKey] = lst
		}

		if res.resultCallKey != "" && res.resultCallKey != K(res.storageKey) {
			resultKey := string(res.resultCallKey)
			lst := c.completedCalls[resultKey]
			if lst == nil {
				lst = newResultList[K, V]()
				c.completedCalls[resultKey] = lst
			}
			lst.add(res)
		}

		if res.contentDigestKey != "" {
			contentKey := string(res.contentDigestKey)
			lst := c.completedCallsByContent[contentKey]
			if lst == nil {
				lst = newResultList[K, V]()
				c.completedCallsByContent[contentKey] = lst
			}
			lst.add(res)
		}

		res.refCount++
		c.mu.Unlock()

		if isFirstCaller && res.persistToDB != nil && res.safeToPersistCache {
			err := res.persistToDB(ctx)
			if err != nil {
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
		delete(c.ongoingCalls, res.callConcurrencyKeys)
		if lst := c.completedCalls[res.storageKey]; lst != nil {
			lst.remove(res)
			if lst.empty() {
				delete(c.completedCalls, res.storageKey)
			}
		}
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
		delete(res.cache.ongoingCalls, res.callConcurrencyKeys)
		if lst := res.cache.completedCalls[res.storageKey]; lst != nil {
			lst.remove(res)
			if lst.empty() {
				delete(res.cache.completedCalls, res.storageKey)
			}
		}
		if res.resultCallKey != "" && res.resultCallKey != K(res.storageKey) {
			key := string(res.resultCallKey)
			if lst := res.cache.completedCalls[key]; lst != nil {
				lst.remove(res)
				if lst.empty() {
					delete(res.cache.completedCalls, key)
				}
			}
		}
		if res.contentDigestKey != "" {
			key := string(res.contentDigestKey)
			if lst := res.cache.completedCallsByContent[key]; lst != nil {
				lst.remove(res)
				if lst.empty() {
					delete(res.cache.completedCallsByContent, key)
				}
			}
		}
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
