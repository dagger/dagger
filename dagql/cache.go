package dagql

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/dagger/dagger/dagql/call"
	cachedb "github.com/dagger/dagger/dagql/db"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
)

type Cache interface {
	// GetOrInitCall caches dagql call results keyed by CacheKey.ID (a call ID).
	// It returns the call result for that ID, initializing it with fn when needed.
	GetOrInitCall(
		context.Context,
		CacheKey,
		func(context.Context) (AnyResult, error),
	) (AnyResult, error)

	// Like GetOrInitCall, but for in-memory opaque values keyed by a plain string key.
	// These values are not persisted and are only cached in memory.
	GetOrInitArbitrary(
		context.Context,
		string,
		func(context.Context) (any, error),
	) (ArbitraryCachedResult, error)

	// Returns the number of entries in the cache.
	Size() int

	// Returns a breakdown of cache entries by internal index.
	EntryStats() CacheEntryStats

	// Run a blocking loop that periodically garbage collects expired entries from the cache db.
	GCLoop(context.Context)
}

func ValueFunc(v AnyResult) func(context.Context) (AnyResult, error) {
	return func(context.Context) (AnyResult, error) {
		return v, nil
	}
}

type CacheKey struct {
	ID *call.ID

	// ConcurrencyKey is used to determine whether *in-progress* calls should be deduplicated.
	// If a call with a given (ResultKey, ConcurrencyKey) pair is already in progress, and
	// another one comes in with the same pair, the second caller will wait for the first
	// to complete and receive the same result.
	//
	// If two calls have the same ResultKey but different ConcurrencyKeys, they will not be deduped.
	//
	// If ConcurrencyKey is empty, no deduplication of in-progress calls will be done.
	ConcurrencyKey string

	// TTL is the time-to-live for the cached result of this call, in seconds.
	TTL int64

	// DoNotCache indicates that this call should not be cached at all, simply ran.
	DoNotCache bool
}

type CacheEntryStats struct {
	OngoingCalls            int
	CompletedCalls          int
	CompletedCallsByContent int
	OngoingArbitrary        int
	CompletedArbitrary      int
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

func NewCache(ctx context.Context, dbPath string) (Cache, error) {
	c := &cache{}

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

type cache struct {
	mu sync.Mutex

	// calls that are in progress, keyed by a combination of the call key and the concurrency key
	// two calls with the same call+concurrency key will be "single-flighted" (only one will actually run)
	ongoingCalls map[callConcurrencyKeys]*ongoingCall

	// e-graph index for call-result equivalence.
	egraphDigestToClass map[string]eqClassID
	egraphParents       []eqClassID
	egraphRanks         []uint8

	egraphClassTerms    map[eqClassID]map[egraphTermID]struct{}
	egraphTerms         map[egraphTermID]*egraphTerm
	egraphTermsByDigest map[string]map[egraphTermID]struct{}
	egraphResultTerms   map[*sharedResult]map[egraphTermID]struct{}

	nextEgraphClassID eqClassID
	nextEgraphTermID  egraphTermID

	// in-progress and completed opaque in-memory calls, keyed by call key
	ongoingArbitraryCalls   map[string]*sharedArbitraryResult
	completedArbitraryCalls map[string]*sharedArbitraryResult

	// db for persistence; currently only used for metadata supporting ttl-based expiration
	db *cachedb.Queries
}

type callConcurrencyKeys struct {
	callKey        string
	concurrencyKey string
}

var _ Cache = &cache{}

type PostCallFunc = func(context.Context) error

type OnReleaseFunc = func(context.Context) error

// sharedResult holds cache-entry state and immutable payload shared by per-call Result values.
type sharedResult struct {
	cache *cache

	// Immutable payload shared by all per-call Result values.
	self    Typed
	objType ObjectType
	// hasValue distinguishes "initialized with a nil value" from "not initialized".
	hasValue bool
	postCall PostCallFunc

	safeToPersistCache bool
	onRelease          OnReleaseFunc

	// Digest facts learned from the returned result ID, consumed by cache/egraph
	// indexing at wait time. Kept as digest facts instead of retaining an ID.
	outputDigest       digest.Digest
	outputExtraDigests []call.ExtraDigest
	resultTermSelf     digest.Digest
	resultTermInputs   []digest.Digest
	hasResultTerm      bool

	refCount int
}

// ongoingCall tracks one in-flight GetOrInitCall execution and points at the
// shared result payload that will be returned to waiters.
type ongoingCall struct {
	callConcurrencyKeys callConcurrencyKeys
	persistToDB         func(context.Context) error
	persistToDBOnce     sync.Once

	waitCh  chan struct{}
	cancel  context.CancelCauseFunc
	waiters int
	err     error
	val     AnyResult

	res *sharedResult
}

// newDetachedResult creates a non-cache-backed Result from an explicit call ID and value.
func newDetachedResult[T Typed](resultID *call.ID, self T) Result[T] {
	return Result[T]{
		shared: &sharedResult{
			self:     self,
			hasValue: true,
		},
		id: resultID,
	}
}

func (res *sharedResult) release(ctx context.Context) error {
	if res == nil || res.cache == nil {
		// wasn't cached, nothing to do
		return nil
	}

	res.cache.mu.Lock()
	res.refCount--
	var onRelease OnReleaseFunc
	if res.refCount == 0 {
		// Always release in-memory dagql/egraph state when refs drain. The
		// safe-to-persist flag only governs persistence metadata behavior.
		res.cache.removeResultFromEgraphLocked(res)
		onRelease = res.onRelease
	}
	res.cache.mu.Unlock()

	if onRelease != nil {
		return onRelease(ctx)
	}
	return nil
}

type Result[T Typed] struct {
	// shared points at immutable payload + lifecycle state shared by all per-call Result values.
	shared *sharedResult

	// id is the caller-facing ID for this specific returned result.
	id *call.ID

	// per-call cache-hit signal for callers/tests.
	hitCache bool
}

var _ AnyResult = Result[Typed]{}

func (r Result[T]) Type() *ast.Type {
	if r.shared == nil || r.shared.self == nil {
		var zero T
		return zero.Type()
	}
	return r.shared.self.Type()
}

// ID returns the ID of the instance.
func (r Result[T]) ID() *call.ID {
	return r.id
}

func (r Result[T]) Self() T {
	var zero T
	if r.shared == nil || r.shared.self == nil {
		return zero
	}
	self, ok := UnwrapAs[T](r.shared.self)
	if !ok {
		return zero
	}
	return self
}

func (r Result[T]) SetField(field reflect.Value) error {
	return assign(field, r.Self())
}

// Unwrap returns the inner value of the instance.
func (r Result[T]) Unwrap() Typed {
	if r.shared == nil {
		var zero T
		return zero
	}
	if r.shared.self == nil {
		var zero T
		return zero
	}
	return r.shared.self
}

func (r Result[T]) DerefValue() (AnyResult, bool) {
	self := r.Self()
	derefableSelf, ok := any(self).(DerefableResult)
	if !ok {
		return r, true
	}
	var postCall PostCallFunc
	if r.shared != nil {
		postCall = r.shared.postCall
	}
	return derefableSelf.DerefToResult(r.ID(), postCall, r.IsSafeToPersistCache())
}

func (r Result[T]) NthValue(nth int) (AnyResult, error) {
	self := r.Self()
	enumerableSelf, ok := any(self).(Enumerable)
	if !ok {
		return nil, fmt.Errorf("cannot get %dth value from %T", nth, self)
	}
	return enumerableSelf.NthValue(nth, r.ID())
}

// withDetachedPayload clones shared payload so per-call mutations do not affect other Results.
func (r Result[T]) withDetachedPayload() Result[T] {
	if r.shared != nil {
		r.shared = &sharedResult{
			self:               r.shared.self,
			objType:            r.shared.objType,
			hasValue:           r.shared.hasValue,
			postCall:           r.shared.postCall,
			safeToPersistCache: r.shared.safeToPersistCache,
		}
	} else {
		r.shared = &sharedResult{}
	}
	return r
}

func (r Result[T]) WithPostCall(fn PostCallFunc) AnyResult {
	return r.ResultWithPostCall(fn)
}

func (r Result[T]) ResultWithPostCall(fn PostCallFunc) Result[T] {
	r = r.withDetachedPayload()
	r.shared.postCall = fn
	return r
}

func (r Result[T]) WithSafeToPersistCache(safe bool) AnyResult {
	r = r.withDetachedPayload()
	r.shared.safeToPersistCache = safe
	return r
}

func (r Result[T]) IsSafeToPersistCache() bool {
	return r.shared != nil && r.shared.safeToPersistCache
}

func (r Result[T]) WithContentDigest(contentDigest digest.Digest) Result[T] {
	id := r.ID()
	if id == nil {
		return r
	}
	r.id = id.With(call.WithContentDigest(contentDigest))
	return r
}

// WithContentDigestAny is WithContentDigest but returns an AnyResult, required
// for polymorphic code paths like module function call plumbing.
func (r Result[T]) WithContentDigestAny(customDigest digest.Digest) AnyResult {
	return r.WithContentDigest(customDigest)
}

// String returns the instance in Class@sha256:... format.
func (r Result[T]) String() string {
	typ := r.Type()
	if typ == nil {
		return "<nil>@<nil>"
	}
	id := r.ID()
	if id == nil {
		return fmt.Sprintf("%s@<nil>", typ.Name())
	}
	return fmt.Sprintf("%s@%s", typ.Name(), id.Digest())
}

func (r Result[T]) PostCall(ctx context.Context) error {
	if r.shared != nil && r.shared.postCall != nil {
		return r.shared.postCall(ctx)
	}
	return nil
}

func (r Result[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.ID())
}

func (r Result[T]) HitCache() bool {
	return r.hitCache
}

func (r Result[T]) Release(ctx context.Context) error {
	if r.shared == nil {
		// malformed result, nothing to do
		return nil
	}
	if r.shared.cache == nil {
		if r.shared.onRelease != nil {
			return r.shared.onRelease(ctx)
		}
		return nil
	}
	return r.shared.release(ctx)
}

func (r Result[T]) cacheSharedResult() *sharedResult {
	return r.shared
}

type ObjectResult[T Typed] struct {
	Result[T]
	class Class[T]
}

var _ AnyObjectResult = ObjectResult[Typed]{}

func (r ObjectResult[T]) MarshalJSON() ([]byte, error) {
	return r.Result.MarshalJSON()
}

func (r ObjectResult[T]) DerefValue() (AnyResult, bool) {
	self := r.Self()
	derefableSelf, ok := any(self).(DerefableResult)
	if !ok {
		return r, true
	}
	var postCall PostCallFunc
	if r.shared != nil {
		postCall = r.shared.postCall
	}
	return derefableSelf.DerefToResult(r.ID(), postCall, r.IsSafeToPersistCache())
}

func (r ObjectResult[T]) SetField(field reflect.Value) error {
	return assign(field, r.Result)
}

// ObjectType returns the ObjectType of the instance.
func (r ObjectResult[T]) ObjectType() ObjectType {
	return r.class
}

func (r ObjectResult[T]) WithContentDigest(contentDigest digest.Digest) ObjectResult[T] {
	return ObjectResult[T]{
		Result: r.Result.WithContentDigest(contentDigest),
		class:  r.class,
	}
}

// WithContentDigestAny is WithContentDigest but returns an AnyResult, required
// for polymorphic code paths like module function call plumbing.
func (r ObjectResult[T]) WithContentDigestAny(customDigest digest.Digest) AnyResult {
	return ObjectResult[T]{
		Result: r.Result.WithContentDigest(customDigest),
		class:  r.class,
	}
}

func (r ObjectResult[T]) ObjectResultWithPostCall(fn PostCallFunc) ObjectResult[T] {
	r.Result = r.Result.ResultWithPostCall(fn)
	return r
}

func (r ObjectResult[T]) cacheSharedResult() *sharedResult {
	return r.shared
}

type cacheContextKey struct {
	key   string
	cache *cache
}

func (c *cache) GCLoop(ctx context.Context) {
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

func (c *cache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	// TODO: Re-implement size accounting directly from egraph state instead of
	// relying on mixed index-oriented counters.
	total := len(c.ongoingCalls)
	total += len(c.egraphResultTerms)
	total += len(c.ongoingArbitraryCalls)
	total += len(c.completedArbitraryCalls)
	return total
}

func (c *cache) EntryStats() CacheEntryStats {
	c.mu.Lock()
	defer c.mu.Unlock()

	var stats CacheEntryStats
	stats.OngoingCalls = len(c.ongoingCalls)
	stats.CompletedCalls = len(c.egraphResultTerms)
	stats.OngoingArbitrary = len(c.ongoingArbitraryCalls)
	stats.CompletedArbitrary = len(c.completedArbitraryCalls)
	return stats
}

//nolint:gocyclo // Core cache lookup/insert flow is intentionally centralized here.
func (c *cache) GetOrInitCall(
	ctx context.Context,
	key CacheKey,
	fn func(context.Context) (AnyResult, error),
) (AnyResult, error) {
	if key.ID == nil {
		return nil, fmt.Errorf("cache key ID is nil")
	}

	if key.DoNotCache {
		// don't cache, don't dedupe calls, just call it

		// we currently still have to appease the buildkit cache key machinery underlying function calls,
		// so make sure it gets a random storage key
		ctx = ctxWithStorageKey(ctx, rand.Text())

		val, err := fn(ctx)
		if err != nil {
			return nil, err
		}
		if val == nil {
			return nil, nil
		}

		// Normalize the returned value for this outer call boundary. Even though this
		// call itself is DoNotCache, we still clear nested call metadata (cache-hit flags,
		// ID overrides, and cache entry ownership) so this outer call reports its own
		// metadata rather than inheriting inner call state.
		detached := &sharedResult{
			self:               val.Unwrap(),
			hasValue:           true,
			postCall:           val.PostCall,
			safeToPersistCache: val.IsSafeToPersistCache(),
		}
		if onReleaser, ok := UnwrapAs[OnReleaser](val); ok {
			detached.onRelease = onReleaser.OnRelease
		}
		if obj, ok := val.(AnyObjectResult); ok {
			detached.objType = obj.ObjectType()
		}
		perCall := Result[Typed]{
			shared: detached,
			id:     val.ID(),
		}
		if detached.objType != nil {
			normalized, err := detached.objType.New(perCall)
			if err != nil {
				return nil, fmt.Errorf("normalize do-not-cache object result: %w", err)
			}
			return normalized, nil
		}
		return perCall, nil
	}

	// Call identity is recipe-based; extra digests are output-equivalence facts.
	callKey := key.ID.Digest().String()
	callConcKeys := callConcurrencyKeys{
		callKey:        callKey,
		concurrencyKey: key.ConcurrencyKey,
	}

	// The storage key is the key for what's actually stored on disk.
	// By default it's just the call key, but if we have a TTL then there
	// can be different results stored on disk for a single call key, necessitating
	// this separate storage key.
	storageKey := callKey

	var persistToDB func(context.Context) error
	if key.TTL != 0 && c.db != nil {
		var cachedCall *cachedb.Call
		candidateCall, err := c.db.SelectCall(ctx, callKey)
		if err == nil {
			cachedCall = candidateCall
		} else if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("failed to select call from cache", "callKey", callKey, "err", err)
		}

		now := time.Now().Unix()
		expiration := now + key.TTL

		// TODO:(sipsma) we unfortunately have to incorporate the session ID into the storage key
		// for now in order to get functions that make SetSecret calls to behave as "per-session"
		// caches (while *also* retaining the correct behavior in all other cases). It would be
		// nice to find some more elegant way of modeling this that disentangles this cache
		// from engine client metadata.
		switch {
		case cachedCall == nil:
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
					CallKey:        callKey,
					StorageKey:     storageKey,
					Expiration:     expiration,
					PrevStorageKey: "",
				})
			}

		case cachedCall.Expiration < now:
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
					CallKey:        callKey,
					StorageKey:     storageKey,
					Expiration:     expiration,
					PrevStorageKey: cachedCall.StorageKey,
				})
			}

		default:
			// We have a cached entry and it hasn't expired yet, use it
			storageKey = cachedCall.StorageKey
		}
	}

	ctx = ctxWithStorageKey(ctx, storageKey)

	if ctx.Value(cacheContextKey{storageKey, c}) != nil {
		return nil, ErrCacheRecursiveCall
	}

	c.mu.Lock()
	if c.ongoingCalls == nil {
		c.ongoingCalls = make(map[callConcurrencyKeys]*ongoingCall)
	}
	hitRes, hit, err := c.lookupCacheForID(ctx, key.ID)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if hit {
		c.mu.Unlock()
		return hitRes, nil
	}

	if key.ConcurrencyKey != "" {
		if oc := c.ongoingCalls[callConcKeys]; oc != nil {
			// already an ongoing call
			oc.waiters++
			c.mu.Unlock()
			return c.wait(ctx, oc, key.ID)
		}
	}

	// make a new call with ctx that's only canceled when all caller contexts are canceled
	callCtx := context.WithValue(ctx, cacheContextKey{storageKey, c}, struct{}{})
	callCtx, cancel := context.WithCancelCause(context.WithoutCancel(callCtx))
	oc := &ongoingCall{
		callConcurrencyKeys: callConcKeys,
		persistToDB:         persistToDB,
		waitCh:              make(chan struct{}),
		cancel:              cancel,
		waiters:             1,
	}

	if key.ConcurrencyKey != "" {
		c.ongoingCalls[callConcKeys] = oc
	}

	go func() {
		defer close(oc.waitCh)
		val, err := fn(callCtx)
		oc.err = err
		oc.val = val
	}()

	c.mu.Unlock()
	return c.wait(ctx, oc, key.ID)
}

func (c *cache) wait(
	ctx context.Context,
	oc *ongoingCall,
	requestID *call.ID,
) (AnyResult, error) {
	var waitErr error

	// wait for completion or caller cancellation.
	select {
	case <-oc.waitCh:
		waitErr = oc.err
	case <-ctx.Done():
		waitErr = context.Cause(ctx)
	}

	retRes, err := c.waitLocked(oc, requestID, waitErr)
	if err != nil {
		return nil, err
	}

	if oc.persistToDB != nil && oc.res.safeToPersistCache {
		oc.persistToDBOnce.Do(func() {
			persistErr := oc.persistToDB(ctx)
			if persistErr != nil {
				slog.Error("failed to persist cache expiration", "err", persistErr)
			}
		})
	}

	if !retRes.shared.hasValue {
		return retRes, nil
	}
	if retRes.shared.objType == nil {
		return retRes, nil
	}
	retObjRes, constructErr := retRes.shared.objType.New(retRes)
	if constructErr != nil {
		return nil, fmt.Errorf("reconstruct object result from cache wait: %w", constructErr)
	}
	return retObjRes, nil
}

func (c *cache) waitLocked(
	oc *ongoingCall,
	requestID *call.ID,
	waitErr error,
) (Result[Typed], error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	oc.waiters--
	lastWaiter := oc.waiters == 0
	if lastWaiter {
		oc.cancel(waitErr)
	}
	if waitErr == nil || lastWaiter {
		delete(c.ongoingCalls, oc.callConcurrencyKeys)
	}
	if waitErr != nil {
		return Result[Typed]{}, waitErr
	}

	if oc.res != nil {
		oc.res.refCount++

		retID := requestID
		for _, extra := range oc.res.outputExtraDigests {
			if extra.Digest == "" {
				continue
			}
			retID = retID.With(call.WithExtraDigest(extra))
		}
		return Result[Typed]{
			shared:   oc.res,
			id:       retID,
			hitCache: false,
		}, nil
	}

	var requestSelf digest.Digest
	var requestInputs []digest.Digest
	var deriveErr error
	requestSelf, requestInputs, deriveErr = requestID.SelfDigestAndInputs()
	if deriveErr != nil {
		return Result[Typed]{}, fmt.Errorf("derive request term digests: %w", deriveErr)
	}

	resWasCacheBacked := false

	// Materialize shared result for this completed call.
	oc.res = &sharedResult{
		cache: c,
	}
	if oc.val != nil {
		if existingRes := oc.val.cacheSharedResult(); existingRes != nil && existingRes.cache != nil {
			oc.res = existingRes
			resWasCacheBacked = true
		} else {
			oc.res.self = oc.val.Unwrap()
			oc.res.hasValue = true
			oc.res.postCall = oc.val.PostCall
			oc.res.safeToPersistCache = oc.val.IsSafeToPersistCache()
			oc.res.outputDigest = oc.val.ID().Digest()
			oc.res.outputExtraDigests = oc.val.ID().ExtraDigests()

			selfDigest, inputDigests, deriveErr := oc.val.ID().SelfDigestAndInputs()
			if deriveErr != nil {
				return Result[Typed]{}, fmt.Errorf("derive result term digests: %w", deriveErr)
			}
			oc.res.resultTermSelf = selfDigest
			oc.res.resultTermInputs = inputDigests
			oc.res.hasResultTerm = true

			if onReleaser, ok := UnwrapAs[OnReleaser](oc.val); ok {
				oc.res.onRelease = onReleaser.OnRelease
			}
			if obj, ok := oc.val.(AnyObjectResult); ok {
				oc.res.objType = obj.ObjectType()
			}
		}
	}

	oc.res.refCount++

	c.indexWaitResultInEgraphLocked(requestID, requestSelf, requestInputs, oc.res, resWasCacheBacked)

	retID := requestID
	for _, extra := range oc.res.outputExtraDigests {
		if extra.Digest == "" {
			continue
		}
		retID = retID.With(call.WithExtraDigest(extra))
	}

	return Result[Typed]{
		shared:   oc.res,
		id:       retID,
		hitCache: false,
	}, nil
}
