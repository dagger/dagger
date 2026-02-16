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
	"strings"
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

const (
	// Enable concise cache lookup tracing. Pair this with focused path filters below.
	cacheLookupTraceEnabled = false
	// Enable high-volume per-hit/per-candidate tracing for short-lived deep dives only.
	cacheLookupVerboseTraceEnabled = false
)

var cacheLookupTracePathContains = []string{
	// Add focused path substrings temporarily while debugging.
	// Example: "Container.from"
}

func shouldTraceCacheFlow(id *call.ID) bool {
	if !cacheLookupTraceEnabled {
		return false
	}
	if id == nil || len(cacheLookupTracePathContains) == 0 {
		return false
	}
	path := id.Path()
	for _, needle := range cacheLookupTracePathContains {
		if needle != "" && strings.Contains(path, needle) {
			return true
		}
	}
	return false
}

func shouldTraceCacheFlowVerbose(id *call.ID) bool {
	return cacheLookupVerboseTraceEnabled && shouldTraceCacheFlow(id)
}

type cacheLookupRejectReason string

const (
	cacheLookupRejectReasonNone                  cacheLookupRejectReason = ""
	cacheLookupRejectReasonTTLStorageKeyMismatch cacheLookupRejectReason = "ttl-storage-key-mismatch"
)

type cacheLookupDecision struct {
	accepted           bool
	rejectReason       cacheLookupRejectReason
	ttlMismatchAllowed bool
}

type lookupRejectSummary struct {
	ttlMismatchAllowed         int
	rejectedTTLStorageMismatch int
	rejectedOther              int
}

func evaluateCacheLookupCandidate(
	requestTTL int64,
	requestNow int64,
	requestStorageKey string,
	requestSessionID string,
	res *sharedResult,
) cacheLookupDecision {
	if res == nil {
		return cacheLookupDecision{
			accepted:     false,
			rejectReason: cacheLookupRejectReasonNone,
		}
	}

	if requestTTL == 0 {
		return cacheLookupDecision{
			accepted:     true,
			rejectReason: cacheLookupRejectReasonNone,
		}
	}
	if res.storageKey == requestStorageKey {
		return cacheLookupDecision{
			accepted:     true,
			rejectReason: cacheLookupRejectReasonNone,
		}
	}
	if !res.safeToPersistCache {
		// Non-persistable entries (e.g. secret-dependent call results) may need
		// storage-key mismatch reuse within a session, but must not bleed across
		// sessions.
		if requestSessionID != "" && res.sessionID != "" && requestSessionID != res.sessionID {
			return cacheLookupDecision{
				accepted:     false,
				rejectReason: cacheLookupRejectReasonTTLStorageKeyMismatch,
			}
		}
		return cacheLookupDecision{
			accepted:           true,
			rejectReason:       cacheLookupRejectReasonNone,
			ttlMismatchAllowed: true,
		}
	}
	// Equivalent hits (from call-term/output-witness evidence) may come from a
	// different storage-key version; allow that reuse as long as the candidate
	// entry is still within its TTL window.
	if requestNow != 0 && res.expiration != 0 && res.expiration >= requestNow {
		return cacheLookupDecision{
			accepted:           true,
			rejectReason:       cacheLookupRejectReasonNone,
			ttlMismatchAllowed: true,
		}
	}
	return cacheLookupDecision{
		accepted:     false,
		rejectReason: cacheLookupRejectReasonTTLStorageKeyMismatch,
	}
}

// withExtraDigests preserves the caller's ID shape while carrying over
// additional digest facts.
func withExtraDigests(baseID *call.ID, extras []call.ExtraDigest) *call.ID {
	if baseID == nil {
		return nil
	}
	if len(extras) == 0 {
		return baseID
	}
	id := baseID
	for _, extra := range extras {
		if extra.Digest == "" {
			continue
		}
		id = id.With(call.WithExtraDigest(extra))
	}
	return id
}

type cacheHitKind uint8

const (
	cacheHitKindStorage cacheHitKind = iota
	cacheHitKindEquivalent
)

// resultIDForRequest returns the caller-facing ID by preserving request recipe
// shape and mixing in learned output-equivalence digests.
func resultIDForRequest(requestID *call.ID, outputExtraDigests []call.ExtraDigest) *call.ID {
	return withExtraDigests(requestID, outputExtraDigests)
}

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
	ongoingCalls map[callConcurrencyKeys]*sharedResult

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

	storageKey          string              // persistent cache storage key for this result
	callConcurrencyKeys callConcurrencyKeys // key to cache.ongoingCalls
	sessionID           string              // originating client session ID (if available)
	expiration          int64               // unix timestamp for ttl-based entries (0 means no ttl)

	// Immutable payload shared by all per-call Result values.
	self    Typed
	objType ObjectType
	// hasValue distinguishes "initialized with a nil value" from "not initialized".
	hasValue bool
	postCall PostCallFunc

	err error

	safeToPersistCache bool
	onRelease          OnReleaseFunc

	// Digest facts learned from the returned result ID, consumed by cache/egraph
	// indexing at wait time. Kept as digest facts instead of retaining an ID.
	outputDigest       digest.Digest
	outputExtraDigests []call.ExtraDigest
	resultTermSelf     digest.Digest
	resultTermInputs   []digest.Digest
	hasResultTerm      bool

	persistToDB func(context.Context) error

	waitCh  chan struct{}
	cancel  context.CancelCauseFunc
	waiters int

	refCount int
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
	if res.refCount == 0 && res.waiters == 0 {
		// Always release in-memory dagql/egraph state when refs drain. The
		// safe-to-persist flag only governs persistence metadata behavior.
		delete(res.cache.ongoingCalls, res.callConcurrencyKeys)
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

	// per-call fields
	hitCache              bool
	hitContentDigestCache bool
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

// WithExtraDigest returns an updated instance with an extra known digest.
func (r Result[T]) WithExtraDigest(extra call.ExtraDigest) Result[T] {
	if extra.Digest == "" {
		return r
	}
	id := r.ID()
	if id == nil {
		return r
	}
	r.id = id.With(call.WithExtraDigest(extra))
	return r
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

func (r Result[T]) HitContentDigestCache() bool {
	return r.hitContentDigestCache
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

func (r ObjectResult[T]) WithExtraDigest(extra call.ExtraDigest) ObjectResult[T] {
	return ObjectResult[T]{
		Result: r.Result.WithExtraDigest(extra),
		class:  r.class,
	}
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

type cacheBackedResult interface {
	cacheSharedResult() *sharedResult
}

func (r Result[T]) cacheHasValue() bool {
	return r.shared != nil && r.shared.hasValue
}

func (r ObjectResult[T]) cacheHasValue() bool {
	return r.shared != nil && r.shared.hasValue
}

type cacheValueResult interface {
	cacheHasValue() bool
}

type cacheContextKey struct {
	key   string
	cache *cache
}

type trackNilResultCtxKey struct{}

func withTrackNilResult(ctx context.Context) context.Context {
	return context.WithValue(ctx, trackNilResultCtxKey{}, struct{}{})
}

func shouldTrackNilResult(ctx context.Context) bool {
	return ctx.Value(trackNilResultCtxKey{}) != nil
}

func materializeCacheHitResult(
	ctx context.Context,
	res *sharedResult,
	id *call.ID,
	hitCache bool,
	hitContentDigestCache bool,
	objectReconstructErrPrefix string,
) (AnyResult, error) {
	if !res.hasValue {
		if shouldTrackNilResult(ctx) {
			return Result[Typed]{
				shared:                res,
				id:                    id,
				hitCache:              hitCache,
				hitContentDigestCache: hitContentDigestCache,
			}, nil
		}
		return nil, nil
	}

	retRes := Result[Typed]{
		shared:                res,
		id:                    id,
		hitCache:              hitCache,
		hitContentDigestCache: hitContentDigestCache,
	}
	if res.objType == nil {
		return retRes, nil
	}

	retObjRes, err := res.objType.New(retRes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", objectReconstructErrPrefix, err)
	}
	return retObjRes, nil
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
	storageExpiration := int64(0)

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
			storageExpiration = expiration
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
			storageExpiration = expiration
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
			storageExpiration = cachedCall.Expiration
		}
	}

	ctx = ctxWithStorageKey(ctx, storageKey)

	requestSessionID := ""
	if md, err := engine.ClientMetadataFromContext(ctx); err == nil {
		requestSessionID = md.SessionID
	}

	if ctx.Value(cacheContextKey{storageKey, c}) != nil {
		return nil, ErrCacheRecursiveCall
	}

	/*
		Derive the structural term used for e-graph lookup.

		We always attempt to derive self digest + input digests from the request ID.
		(self digest + input digests). If derivation fails, we fail fast because
		equivalence matching would be unsafe.
	*/
	lookupSelfDigest, lookupInputDigests, err := key.ID.SelfDigestAndInputs()
	if err != nil {
		return nil, fmt.Errorf("derive call term: %w", err)
	}

	c.mu.Lock()
	if c.ongoingCalls == nil {
		c.ongoingCalls = make(map[callConcurrencyKeys]*sharedResult)
	}
	requestNow := int64(0)
	if key.TTL != 0 {
		requestNow = time.Now().Unix()
	}
	hitTerm, rejectSummary := c.lookupResultByTermLocked(
		lookupSelfDigest,
		lookupInputDigests,
		storageKey,
		key.TTL,
		requestNow,
		requestSessionID,
	)
	if hitTerm != nil {
		res := hitTerm.result
		res.refCount++
		c.mergeRequestIntoHitLocked(key.ID, hitTerm)
		c.mu.Unlock()

		hitKind := cacheHitKindEquivalent
		if res.storageKey == storageKey {
			hitKind = cacheHitKindStorage
		}
		retID := resultIDForRequest(key.ID, hitTerm.outputExtraDigests)
		return materializeCacheHitResult(
			ctx,
			res,
			retID,
			true,
			hitKind == cacheHitKindEquivalent,
			"reconstruct structural-hit object result from cache",
		)
	}

	if key.ConcurrencyKey != "" {
		if res, ok := c.ongoingCalls[callConcKeys]; ok {
			// already an ongoing call
			res.waiters++
			c.mu.Unlock()
			return c.wait(ctx, res, key.ID, false)
		}
	}

	// make a new call with ctx that's only canceled when all caller contexts are canceled
	callCtx := context.WithValue(ctx, cacheContextKey{storageKey, c}, struct{}{})
	callCtx, cancel := context.WithCancelCause(context.WithoutCancel(callCtx))
	res := &sharedResult{
		cache: c,

		storageKey:          storageKey,
		callConcurrencyKeys: callConcKeys,
		sessionID:           requestSessionID,
		expiration:          storageExpiration,

		persistToDB: persistToDB,

		waitCh:  make(chan struct{}),
		cancel:  cancel,
		waiters: 1,
	}

	if key.ConcurrencyKey != "" {
		c.ongoingCalls[callConcKeys] = res
	}
	if shouldTraceCacheFlow(key.ID) {
		slog.Info("cache trace miss",
			"requestPath", key.ID.Path(),
			"requestDigest", key.ID.Digest(),
			"storageKey", storageKey,
			"concurrencyKey", key.ConcurrencyKey,
			"lookupInputCount", len(lookupInputDigests),
			"rejectedTTLStorageKeyMismatch", rejectSummary.rejectedTTLStorageMismatch,
			"rejectedOther", rejectSummary.rejectedOther,
			"ttlStorageKeyMismatchAllowed", rejectSummary.ttlMismatchAllowed,
		)
	}

	go func() {
		defer close(res.waitCh)
		val, err := fn(callCtx)
		res.err = err
		if val != nil {
			// Normalize nested call metadata at this outer call boundary.
			res.self = val.Unwrap()
			res.postCall = val.PostCall
			res.safeToPersistCache = val.IsSafeToPersistCache()
			res.hasValue = true
			if id := val.ID(); id != nil {
				res.outputDigest = id.Digest()
				res.outputExtraDigests = id.ExtraDigests()
				selfDigest, inputDigests, err := id.SelfDigestAndInputs()
				if err == nil {
					res.resultTermSelf = selfDigest
					res.resultTermInputs = inputDigests
					res.hasResultTerm = true
				} else {
					slog.Warn("failed to derive result term digests", "err", err)
				}
			}
			if cacheBackedRes, ok := val.(cacheBackedResult); ok {
				if shared := cacheBackedRes.cacheSharedResult(); shared != nil && shared.cache != nil {
					// Transfer ownership of the inner cache ref to this outer cache entry.
					res.onRelease = val.Release
				}
			}
			if res.onRelease == nil {
				if onReleaser, ok := UnwrapAs[OnReleaser](val); ok {
					res.onRelease = onReleaser.OnRelease
				}
			}
			if obj, ok := val.(AnyObjectResult); ok {
				res.objType = obj.ObjectType()
			}
		}
	}()

	c.mu.Unlock()
	return c.wait(ctx, res, key.ID, true)
}

func (c *cache) wait(ctx context.Context, res *sharedResult, requestID *call.ID, isFirstCaller bool) (AnyResult, error) {
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
		safeToPersistCache := res.safeToPersistCache

		delete(c.ongoingCalls, res.callConcurrencyKeys)
		c.indexResultInEgraphLocked(requestID, res)

		res.refCount++
		retID := resultIDForRequest(requestID, res.outputExtraDigests)
		c.mu.Unlock()

		if isFirstCaller && res.persistToDB != nil && safeToPersistCache {
			err := res.persistToDB(ctx)
			if err != nil {
				slog.Error("failed to persist cache expiration", "err", err)
			}
		}

		if isFirstCaller {
			hitCache = false
		}
		return materializeCacheHitResult(
			ctx,
			res,
			retID,
			hitCache,
			false,
			"reconstruct object result from cache wait",
		)
	}

	if shouldTraceCacheFlow(requestID) {
		slog.Info("cache trace wait-error",
			"requestPath", func() string {
				if requestID == nil {
					return ""
				}
				return requestID.Path()
			}(),
			"requestDigest", func() digest.Digest {
				if requestID == nil {
					return ""
				}
				return requestID.Digest()
			}(),
			"storageKey", res.storageKey,
			"err", err,
			"waiters", res.waiters,
			"refCount", res.refCount,
		)
	}

	if res.refCount == 0 && res.waiters == 0 {
		// error happened and no refs left, delete it now
		delete(c.ongoingCalls, res.callConcurrencyKeys)
	}

	c.mu.Unlock()
	return nil, err
}
