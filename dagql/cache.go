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

// mergeOutputEquivalentDigests preserves the caller's ID shape while carrying
// over output-equivalence digest facts from the semantic constructor.
func mergeOutputEquivalentDigests(baseID, fromID *call.ID) *call.ID {
	if baseID == nil {
		return nil
	}
	if fromID == nil {
		return baseID
	}
	id := baseID
	for _, extra := range fromID.ExtraDigests() {
		if extra.Digest == "" {
			continue
		}
		if extra.Kind != call.ExtraDigestKindOutputEquivalence {
			continue
		}
		id = id.With(call.WithExtraDigest(extra))
	}
	return id
}

// cacheIndexIDForResult returns the ID view to use for secondary cache indexes.
// Keep semantic constructor identity for recipe-rewritten results, but preserve
// request-attached output-equivalence digests when the recipe itself is unchanged.
func cacheIndexIDForResult(requestID *call.ID, cachedRes *sharedResult) *call.ID {
	if cachedRes == nil || cachedRes.constructor == nil {
		return nil
	}
	if requestID == nil || requestID.Digest() != cachedRes.constructor.Digest() {
		return cachedRes.constructor
	}
	return mergeOutputEquivalentDigests(requestID, cachedRes.constructor)
}

type cacheHitKind uint8

const (
	cacheHitKindStorage cacheHitKind = iota
	cacheHitKindEquivalent
)

func recipeRemapAllowedForHit(hitKind cacheHitKind) bool {
	return false
}

// presentationIDForResult returns the caller-facing ID for a cache hit.
//
// Policy table:
// - storage hit:
//   - never remap recipe digest; only preserve caller ID shape + merge constructor
//     output-equivalence digests when constructor/request differ by digest facts.
//
// - equivalent/output-eq hit:
//   - currently also do not remap recipe digest; preserve constructor identity.
func presentationIDForResult(requestID *call.ID, cachedRes *sharedResult, hitKind cacheHitKind) *call.ID {
	if requestID == nil || cachedRes == nil || cachedRes.constructor == nil {
		return nil
	}
	constructorID := cachedRes.constructor
	recipeMatches := requestID.Digest() == constructorID.Digest()
	if !recipeMatches && !recipeRemapAllowedForHit(hitKind) {
		return nil
	}
	if requestID.CacheDigest() == constructorID.CacheDigest() && requestID.Path() == constructorID.Path() {
		return nil
	}
	return mergeOutputEquivalentDigests(requestID, constructorID)
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

	// calls that have completed successfully and are cached, keyed by the storage key
	completedCalls map[string]*resultList

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

	storageKey          string              // key to cache.completedCalls
	callConcurrencyKeys callConcurrencyKeys // key to cache.ongoingCalls
	requestID           *call.ID            // original call ID requested for this cache entry
	sessionID           string              // originating client session ID (if available)
	expiration          int64               // unix timestamp for ttl-based entries (0 means no ttl)

	// Immutable payload shared by all per-call Result values.
	constructor *call.ID
	self        Typed
	objType     ObjectType
	// hasValue distinguishes "initialized with a nil value" from "not initialized".
	hasValue bool
	postCall PostCallFunc

	err error

	safeToPersistCache bool
	resultCallKey      string
	onRelease          OnReleaseFunc

	persistToDB func(context.Context) error

	waitCh  chan struct{}
	cancel  context.CancelCauseFunc
	waiters int

	refCount int
}

// newDetachedResult creates a non-cache-backed Result from an explicit call ID and value.
func newDetachedResult[T Typed](constructor *call.ID, self T) Result[T] {
	return Result[T]{
		shared: &sharedResult{
			constructor: constructor,
			self:        self,
			hasValue:    true,
		},
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
		if lst := res.cache.completedCalls[res.storageKey]; lst != nil {
			lst.remove(res)
			if lst.empty() {
				delete(res.cache.completedCalls, res.storageKey)
			}
		}
		if res.resultCallKey != "" && res.resultCallKey != res.storageKey {
			key := res.resultCallKey
			if lst := res.cache.completedCalls[key]; lst != nil {
				lst.remove(res)
				if lst.empty() {
					delete(res.cache.completedCalls, key)
				}
			}
		}
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

	// presentationID is the caller-facing ID for this specific returned result.
	// When nil, shared.constructor is presented.
	presentationID *call.ID

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
	if r.presentationID != nil {
		return r.presentationID
	}
	if r.shared == nil {
		return nil
	}
	return r.shared.constructor
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
	id := r.ID()
	if r.shared != nil {
		r.shared = &sharedResult{
			constructor:        r.shared.constructor,
			self:               r.shared.self,
			objType:            r.shared.objType,
			hasValue:           r.shared.hasValue,
			postCall:           r.shared.postCall,
			safeToPersistCache: r.shared.safeToPersistCache,
		}
	} else {
		r.shared = &sharedResult{}
	}
	if id != nil {
		r.shared.constructor = id
	}
	r.presentationID = nil
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
	id := r.ID()
	if id == nil {
		return r
	}
	r = r.withDetachedPayload()
	r.shared.constructor = id.With(call.WithExtraDigest(extra))
	return r
}

// WithAdditionalDigest returns an updated instance with an unlabeled extra
// digest.
func (r Result[T]) WithAdditionalDigest(additionalDigest digest.Digest) Result[T] {
	return r.WithExtraDigest(call.ExtraDigest{
		Digest: additionalDigest,
	})
}

// WithLegacyCustomDigest returns an updated instance with the legacy "custom"
// digest metadata set.
func (r Result[T]) WithLegacyCustomDigest(customDigest digest.Digest) Result[T] {
	id := r.ID()
	if id == nil {
		return r
	}
	r = r.withDetachedPayload()
	r.shared.constructor = id.With(call.WithCustomDigest(customDigest))
	return r
}

// WithDigest is retained as a compatibility alias for
// WithLegacyCustomDigest.
func (r Result[T]) WithDigest(customDigest digest.Digest) Result[T] {
	return r.WithLegacyCustomDigest(customDigest)
}

func (r Result[T]) WithContentDigest(contentDigest digest.Digest) Result[T] {
	id := r.ID()
	if id == nil {
		return r
	}
	r = r.withDetachedPayload()
	r.shared.constructor = id.With(call.WithContentDigest(contentDigest))
	return r
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

func (r ObjectResult[T]) WithAdditionalDigest(additionalDigest digest.Digest) ObjectResult[T] {
	return ObjectResult[T]{
		Result: r.Result.WithAdditionalDigest(additionalDigest),
		class:  r.class,
	}
}

func (r ObjectResult[T]) WithLegacyCustomDigest(customDigest digest.Digest) ObjectResult[T] {
	return ObjectResult[T]{
		Result: r.Result.WithLegacyCustomDigest(customDigest),
		class:  r.class,
	}
}

// WithObjectDigest is retained as a compatibility alias for
// WithLegacyCustomDigest.
func (r ObjectResult[T]) WithObjectDigest(customDigest digest.Digest) ObjectResult[T] {
	return ObjectResult[T]{
		Result: r.Result.WithLegacyCustomDigest(customDigest),
		class:  r.class,
	}
}

func (r ObjectResult[T]) WithContentDigest(contentDigest digest.Digest) ObjectResult[T] {
	return ObjectResult[T]{
		Result: r.Result.WithContentDigest(contentDigest),
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
	presentationID *call.ID,
	hitCache bool,
	hitContentDigestCache bool,
	objectReconstructErrPrefix string,
) (AnyResult, error) {
	if !res.hasValue {
		if shouldTrackNilResult(ctx) {
			return Result[Typed]{
				shared:                res,
				presentationID:        presentationID,
				hitCache:              hitCache,
				hitContentDigestCache: hitContentDigestCache,
			}, nil
		}
		return nil, nil
	}

	retRes := Result[Typed]{
		shared:                res,
		presentationID:        presentationID,
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

	total := len(c.ongoingCalls)
	for _, lst := range c.completedCalls {
		total += lst.len()
	}
	total += len(c.ongoingArbitraryCalls)
	total += len(c.completedArbitraryCalls)
	return total
}

func (c *cache) EntryStats() CacheEntryStats {
	c.mu.Lock()
	defer c.mu.Unlock()

	var stats CacheEntryStats
	stats.OngoingCalls = len(c.ongoingCalls)
	for _, lst := range c.completedCalls {
		stats.CompletedCalls += lst.len()
	}
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
			constructor:        val.ID(),
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

	callKey := key.ID.CacheDigest().String()
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

		We always attempt to canonicalize the request ID into a term proto
		(self digest + input digests). If derivation fails, we fail fast because
		equivalence matching would be unsafe.
	*/
	lookupTerm, err := termProtoForID(key.ID)
	if err != nil {
		return nil, fmt.Errorf("derive call term: %w", err)
	}

	c.mu.Lock()
	if c.ongoingCalls == nil {
		c.ongoingCalls = make(map[callConcurrencyKeys]*sharedResult)
	}
	if c.completedCalls == nil {
		c.completedCalls = make(map[string]*resultList)
	}
	requestNow := int64(0)
	if key.TTL != 0 {
		requestNow = time.Now().Unix()
	}
	type lookupRejectSummary struct {
		ttlMismatchAllowed         int
		rejectedTTLStorageMismatch int
		rejectedOther              int
	}
	rejectSummary := lookupRejectSummary{}
	acceptLookupResult := func(res *sharedResult) bool {
		decision := evaluateCacheLookupCandidate(key.TTL, requestNow, storageKey, requestSessionID, res)
		if decision.ttlMismatchAllowed {
			rejectSummary.ttlMismatchAllowed++
			if shouldTraceCacheFlowVerbose(key.ID) {
				slog.Info("cache trace ttl-storage-key-mismatch-allowed",
					"requestPath", key.ID.Path(),
					"requestDigest", key.ID.Digest(),
					"requestCacheDigest", key.ID.CacheDigest(),
					"requestStorageKey", storageKey,
					"candidateStorageKey", res.storageKey,
				)
			}
		}
		if decision.accepted {
			return true
		}

		switch decision.rejectReason {
		case cacheLookupRejectReasonTTLStorageKeyMismatch:
			// TTL selects a specific storage key version. Equivalent hits found via
			// egraph evidence must not bypass that version boundary for persistable entries.
			rejectSummary.rejectedTTLStorageMismatch++
			if shouldTraceCacheFlowVerbose(key.ID) {
				slog.Info("cache trace reject",
					"reason", string(cacheLookupRejectReasonTTLStorageKeyMismatch),
					"requestPath", key.ID.Path(),
					"requestDigest", key.ID.Digest(),
					"requestCacheDigest", key.ID.CacheDigest(),
					"requestStorageKey", storageKey,
					"candidateStorageKey", res.storageKey,
				)
			}
		default:
			rejectSummary.rejectedOther++
			if shouldTraceCacheFlowVerbose(key.ID) {
				slog.Info("cache trace reject",
					"reason", "candidate-rejected",
					"requestPath", key.ID.Path(),
					"requestDigest", key.ID.Digest(),
					"requestCacheDigest", key.ID.CacheDigest(),
				)
			}
		}
		return false
	}

	if res := c.lookupResultByTermLocked(lookupTerm, storageKey, acceptLookupResult); res != nil {
		res.refCount++
		c.mu.Unlock()

		hitKind := cacheHitKindEquivalent
		if res.storageKey == storageKey {
			hitKind = cacheHitKindStorage
		}
		presentationID := presentationIDForResult(key.ID, res, hitKind)
		return materializeCacheHitResult(
			ctx,
			res,
			presentationID,
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
			return c.wait(ctx, res, false)
		}
	}

	// make a new call with ctx that's only canceled when all caller contexts are canceled
	callCtx := context.WithValue(ctx, cacheContextKey{storageKey, c}, struct{}{})
	callCtx, cancel := context.WithCancelCause(context.WithoutCancel(callCtx))
	res := &sharedResult{
		cache: c,

		storageKey:          storageKey,
		callConcurrencyKeys: callConcKeys,
		requestID:           key.ID,
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
			"requestCacheDigest", key.ID.CacheDigest(),
			"storageKey", storageKey,
			"concurrencyKey", key.ConcurrencyKey,
			"lookupInputCount", len(lookupTerm.inputDigests),
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
			res.constructor = val.ID()
			res.self = val.Unwrap()
			res.postCall = val.PostCall
			res.safeToPersistCache = val.IsSafeToPersistCache()
			res.onRelease = nil
			res.objType = nil
			res.hasValue = true
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
			if res.constructor != nil {
				indexID := cacheIndexIDForResult(res.requestID, res)
				if indexID == nil {
					indexID = res.constructor
				}
				res.resultCallKey = indexID.CacheDigest().String()
			}
		}
	}()

	c.mu.Unlock()
	return c.wait(ctx, res, true)
}

func (c *cache) wait(ctx context.Context, res *sharedResult, isFirstCaller bool) (AnyResult, error) {
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
		lst, ok := c.completedCalls[res.storageKey]
		if ok {
			if existing := lst.first(); existing != nil {
				res = existing
			} else {
				lst.add(res)
			}
		} else {
			lst = newResultList()
			lst.add(res)
			c.completedCalls[res.storageKey] = lst
		}

		if res.resultCallKey != "" && res.resultCallKey != res.storageKey {
			resultKey := res.resultCallKey
			lst := c.completedCalls[resultKey]
			if lst == nil {
				lst = newResultList()
				c.completedCalls[resultKey] = lst
			}
			lst.add(res)
		}

		c.indexResultInEgraphLocked(res)

		res.refCount++
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
		presentationID := presentationIDForResult(res.requestID, res, cacheHitKindStorage)
		return materializeCacheHitResult(
			ctx,
			res,
			presentationID,
			hitCache,
			false,
			"reconstruct object result from cache wait",
		)
	}

	if shouldTraceCacheFlow(res.requestID) {
		slog.Info("cache trace wait-error",
			"requestPath", func() string {
				if res.requestID == nil {
					return ""
				}
				return res.requestID.Path()
			}(),
			"requestDigest", func() digest.Digest {
				if res.requestID == nil {
					return ""
				}
				return res.requestID.Digest()
			}(),
			"requestCacheDigest", func() digest.Digest {
				if res.requestID == nil {
					return ""
				}
				return res.requestID.CacheDigest()
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
