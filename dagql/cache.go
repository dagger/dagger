package dagql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"github.com/dagger/dagger/dagql/call"
	persistdb "github.com/dagger/dagger/dagql/persistdb"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
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

	// Returns a deterministic snapshot of cache usage entries used for pruning
	// policy/accounting.
	UsageEntries() []CacheUsageEntry

	// Returns a deterministic snapshot of cache usage entries with size
	// measurement for all cache entries (not only prune candidates). This is
	// intended for global cache reporting and policy threshold evaluation.
	UsageEntriesAll(context.Context) []CacheUsageEntry

	// Applies ordered prune policies and returns the set of pruned entries.
	Prune(context.Context, []CachePrunePolicy) (CachePruneReport, error)

	// Close flushes and closes cache-owned persistence resources.
	Close(context.Context) error

	// DebugEGraphSnapshot returns a deterministic point-in-time dump of the
	// current in-memory e-graph/cache state for debugging.
	DebugEGraphSnapshot() *EGraphDebugSnapshot
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

	// IsPersistable indicates whether this field call is eligible for persistent
	// cache storage.
	IsPersistable bool
}

type CacheEntryStats struct {
	OngoingCalls            int
	CompletedCalls          int
	RetainedCalls           int
	CompletedCallsByContent int
	OngoingArbitrary        int
	CompletedArbitrary      int
}

type CacheUsageEntry struct {
	ID                        string
	Description               string
	RecordType                string
	SizeBytes                 int64
	CreatedTimeUnixNano       int64
	MostRecentUseTimeUnixNano int64
	ActivelyUsed              bool
}

type CachePrunePolicy struct {
	All           bool
	Filters       []string
	KeepDuration  time.Duration
	ReservedSpace int64
	MaxUsedSpace  int64
	MinFreeSpace  int64
	TargetSpace   int64

	// CurrentFreeSpace is optional free-disk bytes at prune start used to
	// evaluate MinFreeSpace. When unset, MinFreeSpace behaves as if free space
	// were zero.
	CurrentFreeSpace int64
}

type CachePruneReport struct {
	Entries        []CacheUsageEntry
	ReclaimedBytes int64
}

const cachePersistenceSchemaVersion = "5"

var ErrCacheRecursiveCall = fmt.Errorf("recursive call detected")
var errPersistedHitNotDecodable = errors.New("persisted hit payload not decodable in current context")
var ErrPersistStateNotReady = errors.New("persist state not ready")

func NewCache(ctx context.Context, dbPath string) (Cache, error) {
	c := &cache{traceBootID: newTraceBootID()}

	if dbPath == "" {
		return c, nil
	}

	db, persistDB, err := prepareCacheDBs(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	c.sqlDB = db
	c.pdb = persistDB

	schemaVersionVal, found, err := c.pdb.SelectMetaValue(ctx, persistdb.MetaKeySchemaVersion)
	if err != nil {
		if closeErr := closeCacheDBs(db, c.pdb); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("read schema_version metadata: %w", err), closeErr)
		}
		return nil, fmt.Errorf("read schema_version metadata: %w", err)
	}
	if found && schemaVersionVal != cachePersistenceSchemaVersion {
		c.tracePersistStoreWipedSchemaMismatch(ctx, cachePersistenceSchemaVersion, schemaVersionVal)
		slog.Warn("dagql persistence store schema version mismatch; wiping and cold-starting", "expected", cachePersistenceSchemaVersion, "actual", schemaVersionVal)
		if closeErr := closeCacheDBs(db, c.pdb); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("close db before schema-version wipe"), closeErr)
		}
		if err := wipeSQLiteFiles(dbPath); err != nil {
			return nil, fmt.Errorf("wipe schema-mismatched persistence db: %w", err)
		}

		db, persistDB, err = prepareCacheDBs(ctx, dbPath)
		if err != nil {
			return nil, err
		}
		c.sqlDB = db
		c.pdb = persistDB
	}

	cleanShutdownVal, found, err := c.pdb.SelectMetaValue(ctx, persistdb.MetaKeyCleanShutdown)
	if err != nil {
		if closeErr := closeCacheDBs(db, c.pdb); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("read clean_shutdown metadata: %w", err), closeErr)
		}
		return nil, fmt.Errorf("read clean_shutdown metadata: %w", err)
	}
	if found && cleanShutdownVal != "1" {
		c.tracePersistStoreWipedUncleanShutdown(ctx, cleanShutdownVal)
		slog.Warn("dagql persistence store marked unclean; wiping and cold-starting", "cleanShutdown", cleanShutdownVal)
		if closeErr := closeCacheDBs(db, c.pdb); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("close db before wipe"), closeErr)
		}
		if err := wipeSQLiteFiles(dbPath); err != nil {
			return nil, fmt.Errorf("wipe unclean persistence db: %w", err)
		}

		db, persistDB, err = prepareCacheDBs(ctx, dbPath)
		if err != nil {
			return nil, err
		}
		c.sqlDB = db
		c.pdb = persistDB
	}
	if err := c.importPersistedState(ctx); err != nil {
		c.tracePersistStoreWipedImportFailure(ctx, err)
		slog.Warn("dagql persistence import failed; wiping and cold-starting", "err", err)
		if closeErr := closeCacheDBs(db, c.pdb); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("close db before import-wipe"), closeErr)
		}
		if err := wipeSQLiteFiles(dbPath); err != nil {
			return nil, fmt.Errorf("wipe persistence db after import failure: %w", err)
		}
		db, persistDB, err = prepareCacheDBs(ctx, dbPath)
		if err != nil {
			return nil, err
		}
		c.sqlDB = db
		c.pdb = persistDB
	}

	if err := c.pdb.UpsertMeta(ctx, persistdb.MetaKeySchemaVersion, cachePersistenceSchemaVersion); err != nil {
		if closeErr := closeCacheDBs(db, c.pdb); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("set persistence schema version: %w", err), closeErr)
		}
		return nil, fmt.Errorf("set persistence schema version: %w", err)
	}
	if err := c.pdb.UpsertMeta(ctx, persistdb.MetaKeyCleanShutdown, "0"); err != nil {
		if closeErr := closeCacheDBs(db, c.pdb); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("mark clean_shutdown=0 at startup: %w", err), closeErr)
		}
		return nil, fmt.Errorf("mark clean_shutdown=0 at startup: %w", err)
	}
	c.startPersistenceWorker()

	return c, nil
}

func prepareCacheDBs(ctx context.Context, dbPath string) (*sql.DB, *persistdb.Queries, error) {
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
		return nil, nil, fmt.Errorf("open %s: %w", connURL, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("ping %s: %w", connURL, err)
	}
	if _, err := db.Exec(persistdb.Schema); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("migrate persistence schema: %w", err)
	}
	persistDB, err := persistdb.Prepare(ctx, db)
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("prepare persistence queries: %w", err)
	}

	return db, persistDB, nil
}

func closeCacheDBs(db *sql.DB, persistDB *persistdb.Queries) error {
	var err error
	if persistDB != nil {
		err = errors.Join(err, persistDB.Close())
	}
	if db != nil {
		err = errors.Join(err, db.Close())
	}
	return err
}

func wipeSQLiteFiles(dbPath string) error {
	removeIfExists := func(path string) error {
		err := os.Remove(path)
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := removeIfExists(dbPath); err != nil {
		return err
	}
	if err := removeIfExists(dbPath + "-wal"); err != nil {
		return err
	}
	if err := removeIfExists(dbPath + "-shm"); err != nil {
		return err
	}
	return nil
}

type cache struct {
	// callsMu protects in-flight call bookkeeping and arbitrary in-memory call maps.
	callsMu sync.Mutex
	// egraphMu protects all e-graph state and indexes.
	egraphMu sync.RWMutex

	// calls that are in progress, keyed by a combination of the call key and the concurrency key
	// two calls with the same call+concurrency key will be "single-flighted" (only one will actually run)
	ongoingCalls map[callConcurrencyKeys]*ongoingCall

	//
	// indexes for eq classes, which are disjoint sets of digests considered equivalent and interchangeable
	//

	nextEgraphClassID eqClassID

	// map of eqClassID -> all digests in that class
	eqClassToDigests map[eqClassID]map[string]struct{}

	// map of eqClassID -> all labeled extra digests known to belong to that class
	eqClassExtraDigests map[eqClassID]map[call.ExtraDigest]struct{}

	// map of digest -> eqClassID for the class that digest is in, if any
	// due to the sets being disjoint, a digest is enforced to only be in one
	// set at a time (any overlap results in union of the sets)
	egraphDigestToClass map[string]eqClassID

	// the parent of the given eqClassID, slice is index by eqClassID so it's
	// conceptually a map of eqClassID->parent eqClassID
	egraphParents []eqClassID

	// the rand of the given eqClassID, slice is index by eqClassID so it's
	// conceptually a map of eqClassID->rank
	egraphRanks []uint8

	//
	// indexes for terms
	//

	nextEgraphTermID egraphTermID

	// term ID -> term
	egraphTerms map[egraphTermID]*egraphTerm

	// term digest -> all terms with that digest
	egraphTermsByTermDigest map[string]map[egraphTermID]struct{}

	//
	// indexes for results
	//

	nextSharedResultID sharedResultID

	// result id -> result
	resultsByID map[sharedResultID]*sharedResult

	// one canonical caller-facing ID per materialized result, used for
	// persistence payload encoding and lazy rehydration after import
	resultCanonicalIDs map[sharedResultID]*call.ID

	//
	// other indexes
	//

	// map of eq class -> all terms that have it as an input, needed during repair to
	// figure out all the terms that need repair after eq class union
	inputEqClassToTerms map[eqClassID]map[egraphTermID]struct{}

	// reverse index from canonical output eq class to all terms whose outputs are
	// currently represented by that class
	outputEqClassToTerms map[eqClassID]map[egraphTermID]struct{}

	// reverse index from materialized result to all output eq classes it is
	// currently associated with
	resultOutputEqClasses map[sharedResultID]map[eqClassID]struct{}

	// Reverse index from any known result-associated digest to materialized results.
	// This includes request recipe+extra digests and result recipe+extra digests.
	egraphResultsByDigest map[string]map[sharedResultID]struct{}

	// per-term input provenance indicates whether each input slot was
	// result-backed or digest-only when the term was observed
	termInputProvenance map[egraphTermID][]egraphInputProvenanceKind

	// in-progress and completed opaque in-memory calls, keyed by call key
	ongoingArbitraryCalls   map[string]*sharedArbitraryResult
	completedArbitraryCalls map[string]*sharedArbitraryResult

	sqlDB *sql.DB
	// persistent normalized cache store (disk persistence/import).
	pdb *persistdb.Queries

	persistMu            sync.Mutex
	persistDirty         bool
	persistNotify        chan struct{}
	persistFlushRequests chan chan error
	persistStop          chan struct{}
	persistDone          chan struct{}
	persistClosed        bool
	persistErr           error
	persistWatchResults  map[sharedResultID]struct{}

	traceBootID       string
	traceSeq          uint64
	tracePersistBatch uint64
	traceImportRuns   uint64

	closeOnce sync.Once
	closeErr  error
}

type callConcurrencyKeys struct {
	callKey        string
	concurrencyKey string
}

var _ Cache = &cache{}

type PostCallFunc = func(context.Context) error

type OnReleaseFunc = func(context.Context) error

type sharedResultID uint64

const sharedResultSizeUnknown int64 = -1

type cacheUsageSizer interface {
	// CacheUsageSize returns the concrete size of the cached payload when known.
	// ok=false means "size is currently unknown/not available".
	CacheUsageSize(context.Context) (sizeBytes int64, ok bool, err error)
}

type hasCacheUsageIdentity interface {
	// CacheUsageIdentity returns a stable identity for deduplicating physical
	// storage accounting across multiple cache results that share one snapshot.
	CacheUsageIdentity() (identity string, ok bool)
}

type cacheUsageMayChange interface {
	// CacheUsageMayChange reports whether usage size can change over time for the
	// same usage identity (for example mutable cache volume snapshots).
	CacheUsageMayChange() bool
}

// sharedResult holds cache-entry state and immutable payload shared by per-call Result values.
type sharedResult struct {
	cache *cache

	// id is the stable cache-local identity for this materialized result.
	id sharedResultID

	// Immutable payload shared by all per-call Result values.
	self    Typed
	objType ObjectType
	// resultCallFrame is the non-lossy presentational call-node metadata for
	// this materialized result. It is used for caller-facing ID reconstruction
	// and telemetry hierarchy reconstruction, not execution or liveness.
	resultCallFrame *ResultCallFrame
	// hasValue distinguishes "initialized with a nil value" from "not initialized".
	hasValue bool
	postCall PostCallFunc

	safeToPersistCache bool
	onRelease          OnReleaseFunc
	// depOfPersistedResult marks results that must remain live because they are
	// dependencies of a persisted/retained result.
	depOfPersistedResult bool
	// deps tracks explicit non-structural owned child-result dependencies used
	// for release/liveness propagation and persistence closure.
	deps map[sharedResultID]struct{}
	// heldDependencyResults are explicit owned child results whose refs are held
	// while this result has active refs. They are released when this result's
	// refcount drains to zero.
	heldDependencyResults []AnyResult
	// persistedSnapshotLinks are imported snapshot-link associations used before
	// typed self payload is decoded/rehydrated. They are not child-result deps.
	persistedSnapshotLinks []PersistedSnapshotRefLink

	outputEffectIDs []string
	// expiresAtUnix is the in-memory TTL deadline for cache-hit eligibility.
	// 0 means "never expires".
	expiresAtUnix int64
	// persistedEnvelope is populated for imported rows and decoded lazily on
	// first cache-hit use in a server-aware context.
	persistedEnvelope *PersistedResultEnvelope

	// Prune-accounting metadata. Size is unknown until explicitly measured.
	createdAtUnixNano  int64
	lastUsedAtUnixNano int64
	sizeEstimateBytes  int64
	usageIdentity      string
	description        string
	recordType         string

	refCount int64
}

// ongoingCall tracks one in-flight GetOrInitCall execution and points at the
// shared result payload that will be returned to waiters.
type ongoingCall struct {
	callConcurrencyKeys     callConcurrencyKeys
	isPersistable           bool
	ttlSeconds              int64
	initCompletedResultOnce sync.Once
	initCompletedResultErr  error

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
			self:               self,
			hasValue:           true,
			safeToPersistCache: true,
		},
		id: resultID,
	}
}

func setTypedPersistedResultID(val Typed, resultID sharedResultID) {
	if resultID == 0 || val == nil {
		return
	}
	setter, ok := val.(PersistedResultIDSetter)
	if !ok {
		return
	}
	setter.SetPersistedResultID(uint64(resultID))
}

func (c *cache) attachResult(ctx context.Context, res AnyResult) (AnyResult, error) {
	if res == nil {
		return nil, nil
	}
	if res.ID() == nil {
		return nil, fmt.Errorf("attach owned result: nil ID")
	}
	if shared := res.cacheSharedResult(); shared != nil && shared.id != 0 {
		return res, nil
	}
	attached, err := c.GetOrInitCall(ctx, CacheKey{ID: res.ID()}, ValueFunc(res))
	if err != nil {
		return nil, fmt.Errorf("attach owned result %q: %w", res.ID().Digest(), err)
	}
	if attached == nil {
		return nil, fmt.Errorf("attach owned result %q: nil result", res.ID().Digest())
	}
	shared := attached.cacheSharedResult()
	if shared == nil || shared.id == 0 {
		return nil, fmt.Errorf("attach owned result %q: attached result missing shared result ID", res.ID().Digest())
	}
	return attached, nil
}

func (res *sharedResult) release(ctx context.Context) error {
	if res == nil || res.cache == nil {
		// wasn't cached, nothing to do
		return nil
	}

	var onRelease OnReleaseFunc
	var heldDependencyResults []AnyResult
	var removeErr error
	newRefCount := atomic.AddInt64(&res.refCount, -1)
	if res.cache != nil {
		res.cache.traceRefReleased(ctx, res, newRefCount)
	}
	if newRefCount == 0 {
		res.cache.egraphMu.Lock()
		heldDependencyResults = res.heldDependencyResults
		res.heldDependencyResults = nil
		if !res.depOfPersistedResult {
			// Non-retained entries keep current behavior: once refs drain, drop the
			// result from the in-memory e-graph and release associated resources.
			removeErr = res.cache.removeResultFromEgraphLocked(ctx, res)
			if removeErr == nil {
				onRelease = res.onRelease
			}
		}
		res.cache.egraphMu.Unlock()
	}

	rerr := removeErr
	for _, dep := range heldDependencyResults {
		if dep == nil {
			continue
		}
		rerr = errors.Join(rerr, dep.Release(ctx))
	}
	if onRelease != nil {
		rerr = errors.Join(rerr, onRelease(ctx))
	}
	return rerr
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
			self:                   r.shared.self,
			objType:                r.shared.objType,
			resultCallFrame:        r.shared.resultCallFrame.clone(),
			hasValue:               r.shared.hasValue,
			postCall:               r.shared.postCall,
			safeToPersistCache:     r.shared.safeToPersistCache,
			persistedEnvelope:      r.shared.persistedEnvelope,
			persistedSnapshotLinks: slices.Clone(r.shared.persistedSnapshotLinks),
			outputEffectIDs:        slices.Clone(r.shared.outputEffectIDs),
			createdAtUnixNano:      r.shared.createdAtUnixNano,
			lastUsedAtUnixNano:     r.shared.lastUsedAtUnixNano,
			sizeEstimateBytes:      r.shared.sizeEstimateBytes,
			usageIdentity:          r.shared.usageIdentity,
			description:            r.shared.description,
			recordType:             r.shared.recordType,
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

func (r Result[T]) ResultWithCallFrame(frame *ResultCallFrame) Result[T] {
	r = r.withDetachedPayload()
	r.shared.resultCallFrame = frame.clone()
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

func (r ObjectResult[T]) ObjectResultWithCallFrame(frame *ResultCallFrame) ObjectResult[T] {
	r.Result = r.Result.ResultWithCallFrame(frame)
	return r
}

func (r ObjectResult[T]) cacheSharedResult() *sharedResult {
	return r.shared
}

type cacheContextKey struct {
	key   string
	cache *cache
}

func (c *cache) Close(ctx context.Context) error {
	c.closeOnce.Do(func() {
		if err := c.flushAndStopPersistenceWorker(ctx); err != nil {
			c.closeErr = errors.Join(c.closeErr, err)
		}
		if c.closeErr != nil {
			c.closeErr = errors.Join(c.closeErr, closeCacheDBs(c.sqlDB, c.pdb))
			c.sqlDB = nil
			c.pdb = nil
			return
		}
		if c.pdb != nil {
			if err := c.pdb.UpsertMeta(ctx, persistdb.MetaKeyCleanShutdown, "1"); err != nil {
				slog.Warn("failed to mark clean shutdown in persistence metadata", "err", err)
			}
			slog.Warn("successfully marked clean shutdown in persistence metadata")
		}
		c.closeErr = closeCacheDBs(c.sqlDB, c.pdb)
		c.sqlDB = nil
		c.pdb = nil
	})
	return c.closeErr
}

func (c *cache) Size() int {
	c.callsMu.Lock()
	defer c.callsMu.Unlock()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()

	// TODO: Re-implement size accounting directly from egraph state instead of
	// relying on mixed index-oriented counters.
	total := len(c.ongoingCalls)
	total += len(c.resultOutputEqClasses)
	total += len(c.ongoingArbitraryCalls)
	total += len(c.completedArbitraryCalls)
	return total
}

func (c *cache) EntryStats() CacheEntryStats {
	c.callsMu.Lock()
	defer c.callsMu.Unlock()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()

	var stats CacheEntryStats
	stats.OngoingCalls = len(c.ongoingCalls)
	stats.CompletedCalls = len(c.resultOutputEqClasses)
	for resID := range c.resultOutputEqClasses {
		res := c.resultsByID[resID]
		if res != nil && res.depOfPersistedResult {
			stats.RetainedCalls++
		}
	}
	stats.OngoingArbitrary = len(c.ongoingArbitraryCalls)
	stats.CompletedArbitrary = len(c.completedArbitraryCalls)

	return stats
}

func (c *cache) UsageEntries() []CacheUsageEntry {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()

	// Intentionally size only prune-relevant candidates. This avoids forcing
	// expensive snapshot size calls for active/non-prunable results while still
	// providing accurate byte accounting for prune policy decisions.
	c.measurePruneCandidateSizesLocked(context.Background())
	return c.usageEntriesLocked()
}

func (c *cache) UsageEntriesAll(ctx context.Context) []CacheUsageEntry {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()

	c.measureAllResultSizesLocked(ctx)
	return c.usageEntriesLocked()
}

func (c *cache) usageEntriesLocked() []CacheUsageEntry {
	// Snapshot-identity dedupe accounting: a single physical snapshot can be
	// referenced by multiple logical results. We deterministically assign bytes
	// to one owner (smallest sharedResultID) and report zero for siblings.
	ownerByUsageIdentity := make(map[string]sharedResultID, len(c.resultsByID))
	for resID, res := range c.resultsByID {
		if res == nil {
			continue
		}
		if res.usageIdentity == "" || res.sizeEstimateBytes == sharedResultSizeUnknown {
			continue
		}
		curOwner := ownerByUsageIdentity[res.usageIdentity]
		if curOwner == 0 || resID < curOwner {
			ownerByUsageIdentity[res.usageIdentity] = resID
		}
	}

	entries := make([]CacheUsageEntry, 0, len(c.resultsByID))
	for resID, res := range c.resultsByID {
		if res == nil {
			continue
		}
		createdAt := res.createdAtUnixNano
		lastUsedAt := res.lastUsedAtUnixNano
		if createdAt == 0 {
			createdAt = lastUsedAt
		}
		if lastUsedAt == 0 {
			lastUsedAt = createdAt
		}
		recordType := res.recordType
		if recordType == "" {
			recordType = "dagql.unknown"
		}
		description := res.description
		if description == "" {
			description = fmt.Sprintf("dagql cache result %d", resID)
		}
		sizeBytes := res.sizeEstimateBytes
		if sizeBytes < 0 {
			sizeBytes = 0
		}
		if res.usageIdentity != "" && ownerByUsageIdentity[res.usageIdentity] != resID {
			sizeBytes = 0
		}
		entries = append(entries, CacheUsageEntry{
			ID:                        fmt.Sprintf("dagql.result.%d", resID),
			Description:               description,
			RecordType:                recordType,
			SizeBytes:                 sizeBytes,
			CreatedTimeUnixNano:       createdAt,
			MostRecentUseTimeUnixNano: lastUsedAt,
			ActivelyUsed:              atomic.LoadInt64(&res.refCount) > 0,
		})
	}

	slices.SortFunc(entries, func(a, b CacheUsageEntry) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return entries
}

func (c *cache) measurePruneCandidateSizesLocked(ctx context.Context) {
	activeDependencyClosure := c.activeDependencyClosureLocked()
	candidatesByIdentity := make(map[string][]sharedResultID)
	for resID, res := range c.resultsByID {
		if res == nil {
			continue
		}
		// Gate size work to prune-relevant candidates only.
		if !res.depOfPersistedResult {
			continue
		}
		if atomic.LoadInt64(&res.refCount) > 0 {
			continue
		}
		if _, blocked := activeDependencyClosure[resID]; blocked {
			continue
		}
		if res.sizeEstimateBytes != sharedResultSizeUnknown && !cacheUsageSizeMayChange(res) {
			continue
		}

		usageIdentity, ok := cacheUsageIdentity(res)
		if !ok || usageIdentity == "" {
			usageIdentity = fmt.Sprintf("dagql.result.%d", resID)
		}
		res.usageIdentity = usageIdentity
		candidatesByIdentity[usageIdentity] = append(candidatesByIdentity[usageIdentity], resID)
	}

	if len(candidatesByIdentity) == 0 {
		return
	}

	usageIdentities := make([]string, 0, len(candidatesByIdentity))
	for usageIdentity := range candidatesByIdentity {
		usageIdentities = append(usageIdentities, usageIdentity)
	}
	slices.Sort(usageIdentities)

	for _, usageIdentity := range usageIdentities {
		candidateIDs := candidatesByIdentity[usageIdentity]
		if len(candidateIDs) == 0 {
			continue
		}
		// Deterministic owner tie-break for this snapshot identity.
		slices.Sort(candidateIDs)
		ownerID := candidateIDs[0]
		ownerRes := c.resultsByID[ownerID]
		if ownerRes == nil {
			continue
		}

		sizeBytes, ok, err := cacheUsageSizeBytes(ctx, ownerRes)
		if err != nil {
			slog.Warn("failed to determine cache usage size",
				"resultID", ownerID,
				"usageIdentity", usageIdentity,
				"err", err)
			continue
		}
		if !ok {
			continue
		}
		if sizeBytes < 0 {
			sizeBytes = 0
		}

		for _, candidateID := range candidateIDs {
			candidateRes := c.resultsByID[candidateID]
			if candidateRes == nil {
				continue
			}
			candidateRes.usageIdentity = usageIdentity
			candidateRes.sizeEstimateBytes = sizeBytes
		}
	}
}

func (c *cache) measureAllResultSizesLocked(ctx context.Context) {
	candidatesByIdentity := make(map[string][]sharedResultID)
	for resID, res := range c.resultsByID {
		if res == nil {
			continue
		}
		if res.sizeEstimateBytes != sharedResultSizeUnknown && !cacheUsageSizeMayChange(res) {
			continue
		}

		usageIdentity, ok := cacheUsageIdentity(res)
		if !ok || usageIdentity == "" {
			usageIdentity = fmt.Sprintf("dagql.result.%d", resID)
		}
		res.usageIdentity = usageIdentity
		candidatesByIdentity[usageIdentity] = append(candidatesByIdentity[usageIdentity], resID)
	}

	if len(candidatesByIdentity) == 0 {
		return
	}

	usageIdentities := make([]string, 0, len(candidatesByIdentity))
	for usageIdentity := range candidatesByIdentity {
		usageIdentities = append(usageIdentities, usageIdentity)
	}
	slices.Sort(usageIdentities)

	for _, usageIdentity := range usageIdentities {
		candidateIDs := candidatesByIdentity[usageIdentity]
		if len(candidateIDs) == 0 {
			continue
		}
		slices.Sort(candidateIDs)
		ownerID := candidateIDs[0]
		ownerRes := c.resultsByID[ownerID]
		if ownerRes == nil {
			continue
		}

		sizeBytes, ok, err := cacheUsageSizeBytes(ctx, ownerRes)
		if err != nil {
			slog.Warn("failed to determine cache usage size",
				"resultID", ownerID,
				"usageIdentity", usageIdentity,
				"err", err)
			continue
		}
		if !ok {
			continue
		}
		if sizeBytes < 0 {
			sizeBytes = 0
		}

		for _, candidateID := range candidateIDs {
			candidateRes := c.resultsByID[candidateID]
			if candidateRes == nil {
				continue
			}
			candidateRes.usageIdentity = usageIdentity
			candidateRes.sizeEstimateBytes = sizeBytes
		}
	}
}

func (c *cache) activeDependencyClosureLocked() map[sharedResultID]struct{} {
	closure := make(map[sharedResultID]struct{})
	stack := make([]sharedResultID, 0, len(c.resultsByID))

	for resID, res := range c.resultsByID {
		if res == nil {
			continue
		}
		if atomic.LoadInt64(&res.refCount) > 0 {
			stack = append(stack, resID)
		}
	}

	for len(stack) > 0 {
		n := len(stack) - 1
		curID := stack[n]
		stack = stack[:n]
		if _, seen := closure[curID]; seen {
			continue
		}
		closure[curID] = struct{}{}
		cur := c.resultsByID[curID]
		if cur == nil {
			continue
		}
		for depID := range cur.deps {
			stack = append(stack, depID)
		}
		for termID := range c.termIDsForResultLocked(curID) {
			term := c.egraphTerms[termID]
			if term == nil {
				continue
			}
			inputProvenance := c.termInputProvenance[termID]
			for i, provenance := range inputProvenance {
				if provenance != egraphInputProvenanceKindResult || i >= len(term.inputEqIDs) {
					continue
				}
				dep := c.firstResultForOutputEqClassAnyAtLocked(term.inputEqIDs[i])
				if dep == nil || dep.id == curID {
					continue
				}
				stack = append(stack, dep.id)
			}
		}
	}

	return closure
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

		val, err := fn(ctx)
		if err != nil {
			return nil, err
		}
		if val == nil {
			return nil, nil
		}

		detached := &sharedResult{
			self:               val.Unwrap(),
			resultCallFrame:    val.cacheSharedResult().resultCallFrame.clone(),
			hasValue:           true,
			postCall:           val.PostCall,
			safeToPersistCache: val.IsSafeToPersistCache(),
			outputEffectIDs:    val.ID().AllEffectIDs(),
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

	// Call identity is recipe-based; extra digests are output-equivalence digests.
	callKey := key.ID.Digest().String()
	if ctx.Value(cacheContextKey{callKey, c}) != nil {
		return nil, ErrCacheRecursiveCall
	}
	callConcKeys := callConcurrencyKeys{
		callKey:        callKey,
		concurrencyKey: key.ConcurrencyKey,
	}

	c.egraphMu.Lock()
	hitRes, hit, err := c.lookupCacheForID(ctx, key.ID, key.IsPersistable, key.TTL)
	c.egraphMu.Unlock()
	if err != nil {
		return nil, err
	}
	if hit {
		loadedHit, loadErr := c.ensurePersistedHitValueLoaded(ctx, CurrentDagqlServer(ctx), hitRes)
		if loadErr == nil {
			return loadedHit, nil
		}
		if !errors.Is(loadErr, errPersistedHitNotDecodable) {
			return nil, loadErr
		}
	}

	c.callsMu.Lock()
	if c.ongoingCalls == nil {
		c.ongoingCalls = make(map[callConcurrencyKeys]*ongoingCall)
	}

	if key.ConcurrencyKey != "" {
		if oc := c.ongoingCalls[callConcKeys]; oc != nil {
			if key.IsPersistable {
				oc.isPersistable = true
			}
			// already an ongoing call
			oc.waiters++
			c.callsMu.Unlock()
			return c.wait(ctx, oc, key.ID)
		}
	}

	// Intentional tradeoff: we do not perform a second e-graph lookup while
	// holding callsMu. A concurrent completion can index and drop its
	// singleflight entry between the first lookup and this point, which may lead
	// to occasional redundant execution instead of a late cache hit. We accept
	// that waste to avoid paying an extra lookup on this miss path.

	// make a new call with ctx that's only canceled when all caller contexts are canceled
	callCtx := context.WithValue(ctx, cacheContextKey{callKey, c}, struct{}{})
	callCtx, cancel := context.WithCancelCause(context.WithoutCancel(callCtx))
	oc := &ongoingCall{
		callConcurrencyKeys: callConcKeys,
		isPersistable:       key.IsPersistable,
		ttlSeconds:          key.TTL,
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

	c.callsMu.Unlock()
	return c.wait(ctx, oc, key.ID)
}

func (c *cache) wait(
	ctx context.Context,
	oc *ongoingCall,
	requestID *call.ID,
) (AnyResult, error) {
	var waitErr error
	sessionID := ""
	if md, mdErr := engine.ClientMetadataFromContext(ctx); mdErr == nil {
		sessionID = md.SessionID
	}

	// wait for completion or caller cancellation.
	select {
	case <-oc.waitCh:
		waitErr = oc.err
	case <-ctx.Done():
		waitErr = context.Cause(ctx)
	}

	c.callsMu.Lock()
	oc.waiters--
	lastWaiter := oc.waiters == 0
	if lastWaiter {
		delete(c.ongoingCalls, oc.callConcurrencyKeys)
		oc.cancel(waitErr)
	}
	c.callsMu.Unlock()
	if waitErr != nil {
		return nil, waitErr
	}

	oc.initCompletedResultOnce.Do(func() {
		oc.initCompletedResultErr = c.initCompletedResult(ctx, oc, requestID, sessionID)
	})
	if oc.initCompletedResultErr != nil {
		return nil, oc.initCompletedResultErr
	}
	if oc.res == nil {
		return nil, fmt.Errorf("cache wait completed without initialized result")
	}

	atomic.AddInt64(&oc.res.refCount, 1)
	c.traceRefAcquired(ctx, oc.res, atomic.LoadInt64(&oc.res.refCount))
	c.egraphMu.Lock()
	touchSharedResultLastUsed(oc.res, time.Now().UnixNano())
	var contentDigest call.ExtraDigest
	for outputEqID := range c.outputEqClassesForResultLocked(oc.res.id) {
		// NOTE: if multiple content-labeled digests end up in one eq class, we
		// intentionally tolerate that for now and just use the first one we
		// encounter.
		for extra := range c.eqClassExtraDigests[outputEqID] {
			if extra.Label != call.ExtraDigestLabelContent || extra.Digest == "" {
				continue
			}
			contentDigest = extra
			break
		}
		if contentDigest.Digest != "" {
			break
		}
	}
	c.egraphMu.Unlock()

	retID := requestID
	if contentDigest.Digest != "" {
		retID = retID.With(call.WithExtraDigest(contentDigest))
	}
	retID = retID.AppendEffectIDs(oc.res.outputEffectIDs...)

	// TTL-bounded unsafe values must be session-scoped to avoid cross-session reuse.
	if oc.ttlSeconds > 0 && !oc.res.safeToPersistCache {
		retID = retID.With(call.WithAppendedImplicitInputs(call.NewArgument(
			"sessionID",
			call.NewLiteralString(sessionID),
			false,
		)))
	}

	retRes := Result[Typed]{
		shared:   oc.res,
		id:       retID,
		hitCache: false,
	}

	if !retRes.shared.hasValue {
		return retRes, nil
	}
	if retRes.shared.objType == nil {
		return retRes, nil
	}
	retObjRes, constructErr := retRes.shared.objType.New(retRes)
	if constructErr != nil {
		return retRes, nil
	}
	return retObjRes, nil
}

func (c *cache) initCompletedResult(ctx context.Context, oc *ongoingCall, requestID *call.ID, sessionID string) error {
	resWasCacheBacked := false
	now := time.Now()
	var (
		resultID         *call.ID
		resultTermSelf   digest.Digest
		resultTermInputs []digest.Digest
		resultTermRefs   []call.StructuralInputRef
		hasResultTerm    bool
	)

	// Materialize shared result for this completed call.
	oc.res = &sharedResult{
		cache:             c,
		sizeEstimateBytes: sharedResultSizeUnknown,
	}
	if oc.val != nil {
		if existingRes := oc.val.cacheSharedResult(); existingRes != nil && existingRes.cache != nil {
			oc.res = existingRes
			resWasCacheBacked = true
		} else {
			oc.res.self = oc.val.Unwrap()
			if shared := oc.val.cacheSharedResult(); shared != nil {
				oc.res.resultCallFrame = shared.resultCallFrame.clone()
			}
			oc.res.hasValue = true
			oc.res.postCall = oc.val.PostCall
			oc.res.safeToPersistCache = oc.val.IsSafeToPersistCache()

			if onReleaser, ok := UnwrapAs[OnReleaser](oc.val); ok {
				oc.res.onRelease = onReleaser.OnRelease
			}
			if obj, ok := oc.val.(AnyObjectResult); ok {
				oc.res.objType = obj.ObjectType()
			} else if srv := CurrentDagqlServer(ctx); srv != nil && oc.val.Type() != nil && oc.val.Type().Elem == nil {
				if objType, ok := srv.ObjectType(oc.val.Type().Name()); ok {
					oc.res.objType = objType
				}
			}
		}
	}
	requestIDForIndex := requestID
	// TTL-bounded unsafe values must be session-scoped to avoid cross-session reuse.
	if oc.ttlSeconds > 0 && !oc.res.safeToPersistCache {
		requestIDForIndex = requestID.With(call.WithAppendedImplicitInputs(call.NewArgument(
			"sessionID",
			call.NewLiteralString(sessionID),
			false,
		)))
	}

	if oc.res.createdAtUnixNano == 0 {
		oc.res.createdAtUnixNano = now.UnixNano()
	}
	touchSharedResultLastUsed(oc.res, now.UnixNano())
	if oc.res.recordType == "" {
		oc.res.recordType = requestIDForIndex.Name()
	}
	if oc.res.recordType == "" {
		oc.res.recordType = "dagql.unknown"
	}
	if oc.res.description == "" {
		oc.res.description = requestIDForIndex.Field()
	}
	if oc.res.description == "" {
		oc.res.description = requestIDForIndex.Digest().String()
	}
	if oc.res.usageIdentity == "" {
		if usageIdentity, ok := cacheUsageIdentity(oc.res); ok {
			oc.res.usageIdentity = usageIdentity
		}
	}

	// TTL merge policy for shared results:
	// - 0 means "no TTL for this writer", not necessarily "never expire globally".
	// - if any writer provides TTL, we keep the earliest non-zero expiry.
	// - 0 only remains when all writers are 0.
	oc.res.expiresAtUnix = mergeSharedResultExpiryUnix(
		oc.res.expiresAtUnix,
		candidateSharedResultExpiryUnix(now.Unix(), oc.ttlSeconds),
	)
	if oc.val != nil {
		resultID = oc.val.ID()
		if !resWasCacheBacked {
			oc.res.outputEffectIDs = resultID.AllEffectIDs()
		}
	}
	if resultID != nil && !resWasCacheBacked {
		selfDigest, inputRefs, deriveErr := resultID.SelfDigestAndInputRefs()
		if deriveErr != nil {
			return fmt.Errorf("derive result term digests: %w", deriveErr)
		}
		inputDigests := make([]digest.Digest, 0, len(inputRefs))
		for _, ref := range inputRefs {
			dig, err := ref.InputDigest()
			if err != nil {
				return fmt.Errorf("derive result term input digest: %w", err)
			}
			inputDigests = append(inputDigests, dig)
		}
		resultTermSelf = selfDigest
		resultTermInputs = inputDigests
		resultTermRefs = inputRefs
		hasResultTerm = true
	}

	requestSelf, requestInputRefs, err := requestIDForIndex.SelfDigestAndInputRefs()
	if err != nil {
		return fmt.Errorf("derive request term digests: %w", err)
	}
	requestInputs := make([]digest.Digest, 0, len(requestInputRefs))
	for _, ref := range requestInputRefs {
		dig, err := ref.InputDigest()
		if err != nil {
			return fmt.Errorf("derive request term input digest: %w", err)
		}
		requestInputs = append(requestInputs, dig)
	}

	c.egraphMu.Lock()
	indexErr := c.indexWaitResultInEgraphLocked(
		ctx,
		requestIDForIndex,
		resultID,
		requestSelf,
		requestInputs,
		requestInputRefs,
		resultTermSelf,
		resultTermInputs,
		resultTermRefs,
		hasResultTerm,
		oc.res,
	)
	if indexErr != nil {
		c.egraphMu.Unlock()
		return indexErr
	}
	if oc.isPersistable {
		c.markResultAsDepOfPersistedLocked(ctx, oc.res)
	}
	c.egraphMu.Unlock()

	if err := c.attachOwnedResults(ctx, oc.res, oc.val); err != nil {
		return err
	}
	if oc.isPersistable {
		c.egraphMu.Lock()
		c.markPersistenceDirty()
		c.egraphMu.Unlock()
	} else {
		c.markPersistenceDirty()
	}

	return nil
}

func (c *cache) attachOwnedResults(ctx context.Context, parent *sharedResult, val AnyResult) error {
	if parent == nil || val == nil {
		return nil
	}
	withOwned, ok := UnwrapAs[HasOwnedResults](val)
	if !ok {
		return nil
	}
	deps, err := withOwned.AttachOwnedResults(ctx, func(child AnyResult) (AnyResult, error) {
		return c.attachResult(ctx, child)
	})
	if err != nil {
		return err
	}
	if len(deps) == 0 {
		return nil
	}

	attachedDepIDs := make([]sharedResultID, 0, len(deps))
	attachedDepRefs := make([]AnyResult, 0, len(deps))
	seen := make(map[sharedResultID]struct{}, len(deps))
	addDepID := func(depID sharedResultID) {
		if depID == 0 {
			return
		}
		if _, ok := seen[depID]; ok {
			return
		}
		seen[depID] = struct{}{}
		attachedDepIDs = append(attachedDepIDs, depID)
	}

	for _, dep := range deps {
		if dep == nil || dep.ID() == nil {
			continue
		}
		attachedDepRes := dep.cacheSharedResult()
		if attachedDepRes == nil || attachedDepRes.id == 0 {
			return fmt.Errorf("attach owned result %q: unexpected detached result", dep.ID().Digest())
		}
		attachedDepRefs = append(attachedDepRefs, dep)
		addDepID(attachedDepRes.id)
	}

	if parent.id == 0 || len(attachedDepIDs) == 0 {
		for _, dep := range attachedDepRefs {
			if dep == nil {
				continue
			}
			if err := dep.Release(ctx); err != nil {
				return err
			}
		}
		return nil
	}

	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()

	parentRes := c.resultsByID[parent.id]
	if parentRes == nil {
		return nil
	}
	if parentRes.deps == nil {
		parentRes.deps = make(map[sharedResultID]struct{})
	}

	for _, depID := range attachedDepIDs {
		if depID == parentRes.id {
			continue
		}
		parentRes.deps[depID] = struct{}{}
		c.traceExplicitDepAdded(ctx, parentRes.id, depID, "attached_owned_result")
		if parentRes.depOfPersistedResult {
			if depRes := c.resultsByID[depID]; depRes != nil {
				c.markResultAsDepOfPersistedLocked(ctx, depRes)
			}
		}
	}
	parentRes.heldDependencyResults = append(parentRes.heldDependencyResults, attachedDepRefs...)

	return nil
}

func candidateSharedResultExpiryUnix(nowUnix, ttlSeconds int64) int64 {
	if ttlSeconds <= 0 {
		return 0
	}
	return nowUnix + ttlSeconds
}

func mergeSharedResultExpiryUnix(existingExpiresAtUnix, candidateExpiresAtUnix int64) int64 {
	switch {
	case existingExpiresAtUnix == 0 && candidateExpiresAtUnix == 0:
		return 0
	case existingExpiresAtUnix == 0:
		return candidateExpiresAtUnix
	case candidateExpiresAtUnix == 0:
		return existingExpiresAtUnix
	case candidateExpiresAtUnix < existingExpiresAtUnix:
		return candidateExpiresAtUnix
	default:
		return existingExpiresAtUnix
	}
}

func cacheUsageSizeBytes(ctx context.Context, res *sharedResult) (int64, bool, error) {
	if res == nil || !res.hasValue || res.self == nil {
		return 0, false, nil
	}
	sizer, ok := any(res.self).(cacheUsageSizer)
	if !ok {
		return 0, false, nil
	}
	return sizer.CacheUsageSize(ctx)
}

func cacheUsageIdentity(res *sharedResult) (string, bool) {
	if res == nil || !res.hasValue || res.self == nil {
		return "", false
	}
	identityer, ok := any(res.self).(hasCacheUsageIdentity)
	if !ok {
		return "", false
	}
	return identityer.CacheUsageIdentity()
}

func cacheUsageSizeMayChange(res *sharedResult) bool {
	if res == nil || !res.hasValue || res.self == nil {
		return false
	}
	mutableSizer, ok := any(res.self).(cacheUsageMayChange)
	if !ok {
		return false
	}
	return mutableSizer.CacheUsageMayChange()
}
