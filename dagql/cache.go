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

func ArbitraryValueFunc(v any) func(context.Context) (any, error) {
	return func(context.Context) (any, error) {
		return v, nil
	}
}

type ArbitraryCachedResult interface {
	Value() any
	HitCache() bool
	Release(context.Context) error
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

// preserveIDWithResultDigests keeps the caller's recipe ID while carrying over
// known digest annotations from the cached constructor. This allows downstream
// calls to still participate in equivalence/content-based cache hits.
func preserveIDWithResultDigests(requestID *call.ID, cachedRes *sharedResult) *call.ID {
	if requestID == nil {
		return nil
	}
	if cachedRes == nil || cachedRes.constructor == nil {
		return requestID
	}
	id := requestID
	for _, extra := range cachedRes.constructor.ExtraDigests() {
		if extra.Digest == "" {
			continue
		}
		// Custom digests are still used as call-key scoping in parts of the
		// engine. Do not copy them across equivalent/content hits, or we can
		// leak one caller's scope into another caller's recipe ID.
		if extra.Label == "custom" {
			continue
		}
		id = id.With(call.WithExtraDigest(extra))
	}
	if dig := cachedRes.constructor.ContentDigest(); dig != "" {
		id = id.With(call.WithContentDigest(dig))
	}
	return id
}

func shouldReuseEquivalentObjectPayload(res *sharedResult) bool {
	if res == nil || res.objType == nil {
		return true
	}
	// Some objects carry caller-local/provenance metadata in payload fields
	// (e.g. module source paths). Reusing an equivalent-hit payload for these
	// types can return values from a different caller recipe even if IDs are
	// overridden.
	switch res.objType.TypeName() {
	case "Module", "ModuleSource":
		return false
	default:
		return true
	}
}

func shouldReuseExactObjectPayload(res *sharedResult) bool {
	if res == nil || res.objType == nil {
		return true
	}
	// Container payloads can contain mutable buildkit refs. Reusing the exact
	// payload across cache hits can cause multiple callers to race finalization
	// of the same active snapshot.
	return res.objType.TypeName() != "Container"
}

func shouldReuseEquivalentHitForCall(keyID *call.ID, res *sharedResult) bool {
	if !shouldReuseEquivalentObjectPayload(res) {
		return false
	}
	if keyID == nil || res == nil || res.constructor == nil {
		return true
	}
	// Equivalent/content-digest reuse must not cross call paths. Path remapping
	// can leak wrong contextual/provenance payloads (e.g. moduleSource context).
	if keyID.Path() != res.constructor.Path() {
		isContainer := false
		if res.objType != nil && res.objType.TypeName() == "Container" {
			isContainer = true
		}
		if res.constructor != nil && res.constructor.Type() != nil && res.constructor.Type().NamedType() == "Container" {
			isContainer = true
		}
		if isContainer {
			// DagOp and non-DagOp container calls are not interchangeable even
			// when they converge to the same known digest. Reusing across this
			// boundary can break nested execution lifecycles.
			if isDagOpCall(keyID) != isDagOpCall(res.constructor) {
				return false
			}
			// Container results can be reached through equivalent recipes with
			// different call paths (e.g. module function wrappers vs underlying
			// container builder calls). Allow cross-path reuse.
		} else {
			return false
		}
	}
	// Avoid reusing constructor payloads for non-core object instances.
	// These values can embed contextual defaults (e.g. _contextDirectory IDs)
	// that are session-specific and become stale across sessions.
	if res.objType != nil {
		typeName := res.objType.TypeName()
		if typeName != "" && keyID.Type() != nil && keyID.Type().NamedType() == typeName {
			if keyID.Field() == lowerFirstASCII(typeName) && !isCoreConstructorType(typeName) {
				return false
			}
		}
	}
	// Identity/provenance introspection fields must preserve exact recipe-bound
	// payload values and should not reuse equivalent-hit payloads.
	switch keyID.Field() {
	case "id", "asString":
		return false
	default:
		return true
	}
}

func shouldPreserveRequestIDOnEquivalentHit(keyID *call.ID, res *sharedResult) bool {
	if keyID == nil {
		return false
	}
	if res != nil && res.objType != nil && res.objType.TypeName() == "Container" {
		return false
	}
	// Module-provided calls can include client-scoped receiver digests in the
	// recipe path. Preserving request IDs for equivalent hits there causes
	// stable cached payloads to appear as different IDs across callers.
	return keyID.Module() == nil
}

func lowerFirstASCII(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] >= 'A' && b[0] <= 'Z' {
		b[0] = b[0] - 'A' + 'a'
	}
	return string(b)
}

func isDagOpCall(id *call.ID) bool {
	if id == nil {
		return false
	}
	arg := id.Arg("isDagOp")
	if arg == nil {
		return false
	}
	lit, ok := arg.Value().(*call.LiteralBool)
	if !ok {
		return false
	}
	return lit.Value()
}

func isCoreConstructorType(typeName string) bool {
	switch typeName {
	case "Address",
		"CacheVolume",
		"Container",
		"CurrentModule",
		"Directory",
		"Engine",
		"EnvFile",
		"File",
		"Function",
		"FunctionCall",
		"GeneratedCode",
		"GitRef",
		"GitRepository",
		"Host",
		"HTTP",
		"LLM",
		"Module",
		"ModuleSource",
		"PortForward",
		"Query",
		"Secret",
		"Service",
		"Socket",
		"SourceMap",
		"TypeDef":
		return true
	default:
		return false
	}
}

func persistentKnownDigestCallKey(knownDigest string) string {
	return hashutil.HashStrings("known-digest", knownDigest).String()
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

	// calls that have completed successfully and are cached, keyed by known digest key
	// (e.g. content digests and additional digests attached to IDs/results).
	completedCallsByKnownDigest map[string]*resultList

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

	// Immutable payload shared by all per-call Result values.
	constructor *call.ID
	self        Typed
	objType     ObjectType
	// hasValue distinguishes "initialized with a nil value" from "not initialized".
	hasValue bool
	postCall PostCallFunc

	err error

	safeToPersistCache bool
	contentDigestKey   string
	knownDigestKeys    []string
	resultCallKey      string
	onRelease          OnReleaseFunc

	persistToDB func(context.Context) error

	waitCh  chan struct{}
	cancel  context.CancelCauseFunc
	waiters int

	refCount int
}

// sharedArbitraryResult is the in-memory-only cache entry for GetOrInitArbitrary values.
type sharedArbitraryResult struct {
	cache *cache

	callKey string

	value any
	err   error

	onRelease OnReleaseFunc

	waitCh  chan struct{}
	cancel  context.CancelCauseFunc
	waiters int

	refCount int
}

func (res *sharedArbitraryResult) release(ctx context.Context) error {
	if res == nil || res.cache == nil {
		return nil
	}

	res.cache.mu.Lock()
	res.refCount--
	var onRelease OnReleaseFunc
	if res.refCount == 0 && res.waiters == 0 {
		delete(res.cache.ongoingArbitraryCalls, res.callKey)
		if existing := res.cache.completedArbitraryCalls[res.callKey]; existing == res {
			delete(res.cache.completedArbitraryCalls, res.callKey)
		}
		onRelease = res.onRelease
	}
	res.cache.mu.Unlock()

	if onRelease != nil {
		return onRelease(ctx)
	}
	return nil
}

type arbitraryResult struct {
	shared   *sharedArbitraryResult
	hitCache bool
}

var _ ArbitraryCachedResult = arbitraryResult{}

func (r arbitraryResult) Value() any {
	if r.shared == nil {
		return nil
	}
	return r.shared.value
}

func (r arbitraryResult) HitCache() bool {
	return r.hitCache
}

func (r arbitraryResult) Release(ctx context.Context) error {
	if r.shared == nil {
		return nil
	}
	return r.shared.release(ctx)
}

// TODO: Drop detached-result cloning once the cache uses an equivalence graph.
// At that point we should just attach newly discovered cache keys/IDs to the same result set.
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
		// no refs left and no one waiting on it, delete from cache
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
		for _, key := range res.knownDigestKeys {
			if lst := res.cache.completedCallsByKnownDigest[key]; lst != nil {
				lst.remove(res)
				if lst.empty() {
					delete(res.cache.completedCallsByKnownDigest, key)
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

	// TODO: Remove idOverride once equivalence-graph cache identity lands.
	// We should record equivalent IDs instead of per-result ID overrides.
	// If set, overrides shared.constructor for this one call result.
	idOverride *call.ID

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
	if r.idOverride != nil {
		return r.idOverride
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
	r.idOverride = nil
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

// WithDigest returns an updated instance with the given metadata set.
// customDigest overrides the default digest of the instance to the provided value.
// NOTE: customDigest must be used with care as any instances with the same digest
// will be considered equivalent and can thus replace each other in the cache.
// Generally, customDigest should be used when there's a content-based digest available
// that won't be caputured by the default, call-chain derived digest.
func (r Result[T]) WithDigest(customDigest digest.Digest) Result[T] {
	id := r.ID()
	if id == nil {
		return r
	}
	r = r.withDetachedPayload()
	r.shared.constructor = id.WithDigest(customDigest)
	return r
}

func (r Result[T]) WithContentDigest(customDigest digest.Digest) Result[T] {
	id := r.ID()
	if id == nil {
		return r
	}
	r = r.withDetachedPayload()
	r.shared.constructor = id.With(call.WithContentDigest(customDigest))
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

func (r ObjectResult[T]) WithObjectDigest(customDigest digest.Digest) ObjectResult[T] {
	return ObjectResult[T]{
		Result: r.Result.WithDigest(customDigest),
		class:  r.class,
	}
}

func (r ObjectResult[T]) WithContentDigest(customDigest digest.Digest) ObjectResult[T] {
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

type arbitraryCacheContextKey struct {
	callKey string
	cache   *cache
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
	for _, lst := range c.completedCallsByKnownDigest {
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
	for _, lst := range c.completedCallsByKnownDigest {
		stats.CompletedCallsByContent += lst.len()
	}
	stats.OngoingArbitrary = len(c.ongoingArbitraryCalls)
	stats.CompletedArbitrary = len(c.completedArbitraryCalls)
	return stats
}

func (c *cache) GetOrInitArbitrary(
	ctx context.Context,
	callKey string,
	fn func(context.Context) (any, error),
) (ArbitraryCachedResult, error) {
	if callKey == "" {
		return nil, fmt.Errorf("cache call key is empty")
	}

	if ctx.Value(arbitraryCacheContextKey{callKey: callKey, cache: c}) != nil {
		return nil, ErrCacheRecursiveCall
	}

	c.mu.Lock()
	if c.ongoingArbitraryCalls == nil {
		c.ongoingArbitraryCalls = make(map[string]*sharedArbitraryResult)
	}
	if c.completedArbitraryCalls == nil {
		c.completedArbitraryCalls = make(map[string]*sharedArbitraryResult)
	}

	if res := c.completedArbitraryCalls[callKey]; res != nil {
		res.refCount++
		c.mu.Unlock()
		return arbitraryResult{
			shared:   res,
			hitCache: true,
		}, nil
	}

	if res := c.ongoingArbitraryCalls[callKey]; res != nil {
		res.waiters++
		c.mu.Unlock()
		return c.waitArbitrary(ctx, res, false)
	}

	callCtx := context.WithValue(ctx, arbitraryCacheContextKey{callKey: callKey, cache: c}, struct{}{})
	callCtx, cancel := context.WithCancelCause(context.WithoutCancel(callCtx))
	res := &sharedArbitraryResult{
		cache: c,

		callKey: callKey,

		waitCh:  make(chan struct{}),
		cancel:  cancel,
		waiters: 1,
	}
	c.ongoingArbitraryCalls[callKey] = res

	go func() {
		defer close(res.waitCh)
		val, err := fn(callCtx)
		res.err = err
		if err == nil {
			res.value = val
			if onReleaser, ok := val.(OnReleaser); ok {
				res.onRelease = onReleaser.OnRelease
			}
		}
	}()

	c.mu.Unlock()
	return c.waitArbitrary(ctx, res, true)
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

	knownLookupKeys := make([]string, 0, 1+len(key.ID.ExtraDigests()))
	knownLookupSet := make(map[string]struct{}, cap(knownLookupKeys))
	addKnownLookupKey := func(k string) {
		if k == "" {
			return
		}
		if _, ok := knownLookupSet[k]; ok {
			return
		}
		knownLookupSet[k] = struct{}{}
		knownLookupKeys = append(knownLookupKeys, k)
	}
	addKnownLookupKey(key.ID.ContentDigest().String())
	for _, extra := range key.ID.ExtraDigests() {
		addKnownLookupKey(extra.Digest.String())
	}
	persistCallKeys := make([]string, 0, 1+len(knownLookupKeys))
	persistCallKeySet := make(map[string]struct{}, cap(persistCallKeys))
	addPersistCallKey := func(k string) {
		if k == "" {
			return
		}
		if _, ok := persistCallKeySet[k]; ok {
			return
		}
		persistCallKeySet[k] = struct{}{}
		persistCallKeys = append(persistCallKeys, k)
	}
	addPersistCallKey(callKey)
	for _, knownLookupKey := range knownLookupKeys {
		addPersistCallKey(persistentKnownDigestCallKey(knownLookupKey))
	}

	// The storage key is the key for what's actually stored on disk.
	// By default it's just the call key, but if we have a TTL then there
	// can be different results stored on disk for a single call key, necessitating
	// this separate storage key.
	storageKey := callKey

	var persistToDB func(context.Context) error
	if key.TTL != 0 && c.db != nil {
		var cachedCall *cachedb.Call
		for _, persistCallKey := range persistCallKeys {
			candidateCall, err := c.db.SelectCall(ctx, persistCallKey)
			if err == nil {
				cachedCall = candidateCall
				break
			}
			if !errors.Is(err, sql.ErrNoRows) {
				slog.Error("failed to select call from cache", "callKey", persistCallKey, "err", err)
				break
			}
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
				for _, persistCallKey := range persistCallKeys {
					err := c.db.SetExpiration(ctx, cachedb.SetExpirationParams{
						CallKey:        persistCallKey,
						StorageKey:     storageKey,
						Expiration:     expiration,
						PrevStorageKey: "",
					})
					if err != nil {
						return err
					}
				}
				return nil
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
				for _, persistCallKey := range persistCallKeys {
					err := c.db.SetExpiration(ctx, cachedb.SetExpirationParams{
						CallKey:        persistCallKey,
						StorageKey:     storageKey,
						Expiration:     expiration,
						PrevStorageKey: cachedCall.StorageKey,
					})
					if err != nil {
						return err
					}
				}
				return nil
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

	var lookupTerm *callTermProto
	term, err := termProtoForID(key.ID)
	if err != nil {
		return nil, fmt.Errorf("derive call term: %w", err)
	}
	if len(term.inputDigests) > 0 {
		lookupTerm = &term
	}

	c.mu.Lock()
	if c.ongoingCalls == nil {
		c.ongoingCalls = make(map[callConcurrencyKeys]*sharedResult)
	}
	if c.completedCalls == nil {
		c.completedCalls = make(map[string]*resultList)
	}
	if c.completedCallsByKnownDigest == nil {
		c.completedCallsByKnownDigest = make(map[string]*resultList)
	}

	if lst, ok := c.completedCalls[storageKey]; ok {
		if res := lst.first(); res != nil {
			if shouldReuseExactObjectPayload(res) {
				res.refCount++
				c.mu.Unlock()
				if !res.hasValue {
					if shouldTrackNilResult(ctx) {
						return Result[Typed]{
							shared:   res,
							hitCache: true,
						}, nil
					}
					return nil, nil
				}
				retRes := Result[Typed]{
					shared:   res,
					hitCache: true,
				}
				if res.objType != nil {
					retObjRes, err := res.objType.New(retRes)
					if err != nil {
						return nil, fmt.Errorf("reconstruct object result from cache: %w", err)
					}
					return retObjRes, nil
				}
				return retRes, nil
			}
		}
	}

	if lookupTerm != nil {
		res := c.lookupEquivalentResultLocked(*lookupTerm)
		if res != nil {
			if !shouldReuseEquivalentHitForCall(key.ID, res) {
				goto knownDigestLookup
			}
			res.refCount++
			c.mu.Unlock()

			hitEquivalent := res.constructor != nil && res.constructor.CacheDigest() != key.ID.CacheDigest()
			var idOverride *call.ID
			if hitEquivalent && shouldPreserveRequestIDOnEquivalentHit(key.ID, res) {
				idOverride = preserveIDWithResultDigests(key.ID, res)
				if key.ID != nil && res.constructor != nil && key.ID.Path() != res.constructor.Path() {
					slog.Info("cache equivalent hit remapped call",
						"requestPath", key.ID.Path(),
						"requestDigest", key.ID.Digest(),
						"resultPath", res.constructor.Path(),
						"resultDigest", res.constructor.Digest(),
						"requestType", key.ID.Type().NamedType(),
						"resultType", res.constructor.Type().NamedType(),
					)
				}
			}

			if !res.hasValue {
				if shouldTrackNilResult(ctx) {
					return Result[Typed]{
						shared:                res,
						idOverride:            idOverride,
						hitCache:              true,
						hitContentDigestCache: hitEquivalent,
					}, nil
				}
				return nil, nil
			}
			retRes := Result[Typed]{
				shared:                res,
				idOverride:            idOverride,
				hitCache:              true,
				hitContentDigestCache: hitEquivalent,
			}
			if res.objType != nil {
				retObjRes, err := res.objType.New(retRes)
				if err != nil {
					return nil, fmt.Errorf("reconstruct equivalent-hit object result from cache: %w", err)
				}
				return retObjRes, nil
			}
			return retRes, nil
		}
	}

knownDigestLookup:
	for _, knownKey := range knownLookupKeys {
		if lst, ok := c.completedCallsByKnownDigest[knownKey]; ok {
			if res := lst.first(); res != nil {
				if !shouldReuseEquivalentHitForCall(key.ID, res) {
					continue
				}
				res.refCount++
				c.aliasRequestIDToResultLocked(key.ID, res)
				c.mu.Unlock()
				var idOverride *call.ID
				if shouldPreserveRequestIDOnEquivalentHit(key.ID, res) {
					idOverride = preserveIDWithResultDigests(key.ID, res)
				}
				if key.ID != nil && res.constructor != nil && key.ID.Path() != res.constructor.Path() {
					slog.Info("cache known-digest hit remapped call",
						"knownKey", knownKey,
						"requestPath", key.ID.Path(),
						"requestDigest", key.ID.Digest(),
						"resultPath", res.constructor.Path(),
						"resultDigest", res.constructor.Digest(),
						"requestType", key.ID.Type().NamedType(),
						"resultType", res.constructor.Type().NamedType(),
					)
				}
				if !res.hasValue {
					if shouldTrackNilResult(ctx) {
						return Result[Typed]{
							shared:                res,
							idOverride:            idOverride,
							hitCache:              true,
							hitContentDigestCache: true,
						}, nil
					}
					return nil, nil
				}
				// if the cache hit was only due to a matching content digest, rather than recipe,
				// keep using the same ID as the input. This ensures that the client keeps seeing
				// the recipe they expected rather than a different random one that happened to
				// have the same content, while still allowing the underlying result be re-used.
				retRes := Result[Typed]{
					shared:                res,
					idOverride:            idOverride,
					hitCache:              true,
					hitContentDigestCache: true,
				}
				if res.objType != nil {
					retObjRes, err := res.objType.New(retRes)
					if err != nil {
						return nil, fmt.Errorf("reconstruct content-hit object result from cache: %w", err)
					}
					return retObjRes, nil
				}
				return retRes, nil
			}
		}
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

		persistToDB: persistToDB,

		waitCh:  make(chan struct{}),
		cancel:  cancel,
		waiters: 1,
	}

	if key.ConcurrencyKey != "" {
		c.ongoingCalls[callConcKeys] = res
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
				if res.requestID != nil {
					for _, extra := range res.requestID.ExtraDigests() {
						res.constructor = res.constructor.With(call.WithExtraDigest(extra))
					}
				}
				res.contentDigestKey = res.constructor.ContentDigest().String()
				knownDigestSet := map[string]struct{}{}
				if res.contentDigestKey != "" {
					knownDigestSet[res.contentDigestKey] = struct{}{}
				}
				for _, extra := range res.constructor.ExtraDigests() {
					if d := extra.Digest.String(); d != "" {
						knownDigestSet[d] = struct{}{}
					}
				}
				res.knownDigestKeys = make([]string, 0, len(knownDigestSet))
				for knownDig := range knownDigestSet {
					res.knownDigestKeys = append(res.knownDigestKeys, knownDig)
				}
				res.resultCallKey = res.constructor.CacheDigest().String()
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

		for _, knownDigestKey := range res.knownDigestKeys {
			lst := c.completedCallsByKnownDigest[knownDigestKey]
			if lst == nil {
				lst = newResultList()
				c.completedCallsByKnownDigest[knownDigestKey] = lst
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
		if !res.hasValue {
			if shouldTrackNilResult(ctx) {
				return Result[Typed]{
					shared:   res,
					hitCache: hitCache,
				}, nil
			}
			return nil, nil
		}
		retRes := Result[Typed]{
			shared:   res,
			hitCache: hitCache,
		}
		if res.objType != nil {
			retObjRes, err := res.objType.New(retRes)
			if err != nil {
				return nil, fmt.Errorf("reconstruct object result from cache wait: %w", err)
			}
			return retObjRes, nil
		}
		return retRes, nil
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

func (c *cache) waitArbitrary(ctx context.Context, res *sharedArbitraryResult, isFirstCaller bool) (ArbitraryCachedResult, error) {
	var hitCache bool
	var err error

	select {
	case <-res.waitCh:
		hitCache = true
		err = res.err
	default:
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
		res.cancel(err)
	}

	if err == nil {
		delete(c.ongoingArbitraryCalls, res.callKey)
		if existing := c.completedArbitraryCalls[res.callKey]; existing != nil {
			res = existing
		} else {
			c.completedArbitraryCalls[res.callKey] = res
		}
		res.refCount++
		c.mu.Unlock()

		if isFirstCaller {
			hitCache = false
		}
		return arbitraryResult{
			shared:   res,
			hitCache: hitCache,
		}, nil
	}

	if res.refCount == 0 && res.waiters == 0 {
		if existing := c.ongoingArbitraryCalls[res.callKey]; existing == res {
			delete(c.ongoingArbitraryCalls, res.callKey)
		}
		if existing := c.completedArbitraryCalls[res.callKey]; existing == res {
			delete(c.completedArbitraryCalls, res.callKey)
		}
	}

	c.mu.Unlock()
	return nil, err
}
