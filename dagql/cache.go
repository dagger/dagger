package dagql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// GetOrInitCall caches dagql call results keyed by the request's canonical
	// recipe identity. It returns the call result for that request,
	// initializing it with fn when needed.
	GetOrInitCall(
		context.Context,
		TypeResolver,
		*CallRequest,
		func(context.Context) (AnyResult, error),
	) (AnyResult, error)

	// LookupCacheForDigests performs only the direct digest/extra-digest cache-hit
	// check and returns an already-cached result when one exists.
	LookupCacheForDigests(
		context.Context,
		TypeResolver,
		digest.Digest,
		[]call.ExtraDigest,
	) (AnyResult, bool, error)

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

	// WriteDebugCacheSnapshot streams a broader deterministic point-in-time dump
	// of in-memory cache state, including cache entry metadata and call-frame
	// provenance, for debugging retained-memory and liveness issues.
	WriteDebugCacheSnapshot(io.Writer) error

	// LoadResultByResultID loads a cache-backed result by its stable shared
	// result handle.
	LoadResultByResultID(context.Context, *Server, uint64) (AnyResult, error)

	// AttachResult promotes a detached result into the cache so subsequent
	// selections can refer to it by stable result handle.
	AttachResult(context.Context, TypeResolver, AnyResult) (AnyResult, error)

	// AddExplicitDependency records that parent must retain dep until parent is
	// released, without changing either result's structural call identity.
	AddExplicitDependency(context.Context, AnyResult, AnyResult, string) error

	// RecipeIDForCall derives the semantic recipe ID for a result call using
	// the cache to resolve any cached result refs it depends on.
	RecipeIDForCall(*ResultCall) (*call.ID, error)

	// RecipeDigestForCall derives the semantic recipe digest for a result call
	// using the cache to resolve any cached result refs it depends on.
	RecipeDigestForCall(*ResultCall) (digest.Digest, error)

	// TeachCallEquivalentToResult records that the given call is equivalent to
	// the provided existing result, updating e-graph/cache identity state
	// without re-executing or synthetic loading.
	TeachCallEquivalentToResult(context.Context, *ResultCall, AnyResult) error

	// TeachContentDigest records a content digest for an already-attached
	// result, updating both the stored result call metadata and the e-graph's
	// digest-equivalence knowledge without detaching or cloning the result.
	TeachContentDigest(context.Context, AnyResult, digest.Digest) error
}

func ValueFunc(v AnyResult) func(context.Context) (AnyResult, error) {
	return func(context.Context) (AnyResult, error) {
		return v, nil
	}
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

const cachePersistenceSchemaVersion = "7"

var ErrCacheRecursiveCall = fmt.Errorf("recursive call detected")
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

	// the rank of the given eqClassID, slice is index by eqClassID so it's
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

// sharedResult holds cache-entry state and shared payload published to per-call Result values.
type sharedResult struct {
	cache *cache

	// id is the stable cache-local identity for this materialized result.
	id sharedResultID

	// Immutable payload shared by all per-call Result values.
	self     Typed
	isObject bool
	// resultCall is the non-lossy semantic/provenance call-node metadata
	// for this materialized result. It is used for canonical recipe
	// reconstruction and telemetry hierarchy reconstruction, not execution or
	// liveness.
	//
	// Cache-owned frames remain immutable once published. The mutable part is
	// which frame is currently published for this shared result.
	resultCallMu sync.RWMutex
	resultCall   *ResultCall
	// payloadMu guards lazy payload publication for imported persisted hits and
	// prune-accounting timestamps that can change after initial publication.
	payloadMu sync.RWMutex
	// hasValue distinguishes "initialized with a nil value" from "not initialized".
	hasValue bool
	postCall PostCallFunc

	safeToPersistCache bool
	onRelease          OnReleaseFunc
	// depOfPersistedResult marks results that must remain live because they are
	// dependencies of a persisted/retained result.
	depOfPersistedResult bool
	// deps tracks exact materialized child-result dependencies used for
	// release/liveness propagation and persistence closure. This includes
	// explicit out-of-band deps and exact resultCall refs mirrored into deps
	// during materialization.
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

type sharedResultPayloadState struct {
	self               Typed
	isObject           bool
	hasValue           bool
	persistedEnvelope  *PersistedResultEnvelope
	createdAtUnixNano  int64
	lastUsedAtUnixNano int64
}

func (res *sharedResult) loadResultCall() *ResultCall {
	if res == nil {
		return nil
	}
	res.resultCallMu.RLock()
	frame := res.resultCall
	res.resultCallMu.RUnlock()
	return frame
}

func (res *sharedResult) storeResultCall(frame *ResultCall) {
	if res == nil {
		return
	}
	res.resultCallMu.Lock()
	res.resultCall = frame
	res.resultCallMu.Unlock()
}

func (res *sharedResult) loadPayloadState() sharedResultPayloadState {
	if res == nil {
		return sharedResultPayloadState{}
	}
	res.payloadMu.RLock()
	state := sharedResultPayloadState{
		self:               res.self,
		isObject:           res.isObject,
		hasValue:           res.hasValue,
		persistedEnvelope:  res.persistedEnvelope,
		createdAtUnixNano:  res.createdAtUnixNano,
		lastUsedAtUnixNano: res.lastUsedAtUnixNano,
	}
	res.payloadMu.RUnlock()
	return state
}

func resultIsObject(val AnyResult, resolver TypeResolver) (bool, error) {
	if resolver == nil {
		return false, errors.New("type resolver is nil")
	}
	if val == nil {
		return false, nil
	}
	if _, ok := val.(AnyObjectResult); ok {
		return true, nil
	}
	typ := val.Type()
	if typ == nil || typ.Elem != nil || typ.Name() == "" {
		return false, nil
	}
	objType, ok := resolver.ObjectType(typ.Name())
	if !ok {
		return false, nil
	}
	if _, err := objType.New(val); err != nil {
		return false, nil
	}
	return true, nil
}

func sharedResultObjectTypeName(res *sharedResult, state sharedResultPayloadState) string {
	if res == nil || !state.isObject {
		return ""
	}
	if frame := res.loadResultCall(); frame != nil && frame.Type != nil && frame.Type.NamedType != "" {
		return frame.Type.NamedType
	}
	if state.persistedEnvelope != nil && state.persistedEnvelope.TypeName != "" {
		return state.persistedEnvelope.TypeName
	}
	if state.self != nil && state.self.Type() != nil {
		return state.self.Type().Name()
	}
	return ""
}

func wrapSharedResultWithResolver(res *sharedResult, hitCache bool, resolver TypeResolver) (AnyResult, error) {
	ret := Result[Typed]{
		shared:   res,
		hitCache: hitCache,
	}
	if res == nil {
		return ret, nil
	}
	state := res.loadPayloadState()
	if !state.isObject {
		return ret, nil
	}
	typeName := sharedResultObjectTypeName(res, state)
	if typeName == "" {
		return nil, fmt.Errorf("reconstruct object result: missing type name")
	}
	if resolver == nil {
		return nil, fmt.Errorf("reconstruct object result %q: missing type resolver", typeName)
	}
	objType, ok := resolver.ObjectType(typeName)
	if !ok {
		return nil, fmt.Errorf("reconstruct object result %q: unknown object type", typeName)
	}
	objRes, err := objType.New(ret)
	if err != nil {
		return nil, fmt.Errorf("reconstruct object result %q: %w", typeName, err)
	}
	return objRes, nil
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

// newDetachedResult creates a non-cache-backed Result from an explicit call frame and value.
func newDetachedResult[T Typed](call *ResultCall, self T) Result[T] {
	var resultCall *ResultCall
	if call != nil {
		resultCall = call.clone()
	}
	return Result[T]{
		shared: &sharedResult{
			self:               self,
			resultCall:         resultCall,
			hasValue:           true,
			safeToPersistCache: true,
		},
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

func (c *cache) normalizePendingResultCallRefs(ctx context.Context, frame *ResultCall) error {
	return c.normalizePendingResultCallRefsWithSeen(ctx, frame, map[*ResultCall]struct{}{})
}

func (c *cache) normalizePendingResultCallRefsWithSeen(ctx context.Context, frame *ResultCall, seen map[*ResultCall]struct{}) error {
	if frame == nil {
		return nil
	}
	if _, ok := seen[frame]; ok {
		return fmt.Errorf("cycle while normalizing pending call refs")
	}
	seen[frame] = struct{}{}
	defer delete(seen, frame)

	frame.bindCache(c)
	if err := c.normalizePendingResultCallRefWithSeen(ctx, frame.Receiver, seen); err != nil {
		return fmt.Errorf("receiver: %w", err)
	}
	if frame.Module != nil {
		if err := c.normalizePendingResultCallRefWithSeen(ctx, frame.Module.ResultRef, seen); err != nil {
			return fmt.Errorf("module: %w", err)
		}
	}
	for _, arg := range frame.Args {
		if arg == nil {
			continue
		}
		if err := c.normalizePendingResultCallLiteralWithSeen(ctx, arg.Value, seen); err != nil {
			return fmt.Errorf("arg %q: %w", arg.Name, err)
		}
	}
	for _, input := range frame.ImplicitInputs {
		if input == nil {
			continue
		}
		if err := c.normalizePendingResultCallLiteralWithSeen(ctx, input.Value, seen); err != nil {
			return fmt.Errorf("implicit input %q: %w", input.Name, err)
		}
	}
	return nil
}

func (c *cache) normalizePendingResultCallRefWithSeen(ctx context.Context, ref *ResultCallRef, seen map[*ResultCall]struct{}) error {
	if ref == nil {
		return nil
	}
	if err := ref.Validate(); err != nil {
		return err
	}
	if ref.Call == nil {
		return nil
	}
	if err := c.normalizePendingResultCallRefsWithSeen(ctx, ref.Call, seen); err != nil {
		return err
	}
	resultID, err := c.resultIDForCall(ctx, ref.Call)
	if err != nil {
		return err
	}
	ref.ResultID = uint64(resultID)
	if shared, err := c.sharedResultByResultID(resultID); err == nil {
		ref.shared = shared
	}
	ref.Call = nil
	return nil
}

func (c *cache) normalizePendingResultCallLiteralWithSeen(ctx context.Context, lit *ResultCallLiteral, seen map[*ResultCall]struct{}) error {
	if lit == nil {
		return nil
	}
	switch lit.Kind {
	case ResultCallLiteralKindResultRef:
		return c.normalizePendingResultCallRefWithSeen(ctx, lit.ResultRef, seen)
	case ResultCallLiteralKindList:
		for _, item := range lit.ListItems {
			if err := c.normalizePendingResultCallLiteralWithSeen(ctx, item, seen); err != nil {
				return err
			}
		}
	case ResultCallLiteralKindObject:
		for _, field := range lit.ObjectFields {
			if field == nil {
				continue
			}
			if err := c.normalizePendingResultCallLiteralWithSeen(ctx, field.Value, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *cache) AttachResult(ctx context.Context, resolver TypeResolver, res AnyResult) (AnyResult, error) {
	if resolver == nil {
		return nil, errors.New("attach result: type resolver is nil")
	}
	if res == nil {
		return nil, nil
	}
	shared := res.cacheSharedResult()
	frame := shared.loadResultCall()
	if shared == nil || frame == nil {
		return nil, fmt.Errorf("attach owned result: missing result call frame")
	}
	if shared.id != 0 {
		return res, nil
	}
	req := &CallRequest{
		ResultCall: frame.clone(),
	}
	req.ResultCall.bindCache(c)
	if err := c.normalizePendingResultCallRefs(ctx, req.ResultCall); err != nil {
		return nil, fmt.Errorf("attach owned result: normalize pending result call refs: %w", err)
	}
	shared.storeResultCall(req.ResultCall)
	req.ResultCall.bindCache(c)
	attached, err := c.GetOrInitCall(ctx, resolver, req, ValueFunc(res))
	if err != nil {
		return nil, fmt.Errorf("attach owned result: %w", err)
	}
	if attached == nil {
		return nil, fmt.Errorf("attach owned result: nil result")
	}
	attachedShared := attached.cacheSharedResult()
	if attachedShared == nil || attachedShared.id == 0 {
		return nil, fmt.Errorf("attach owned result: attached result missing shared result ID")
	}
	return attached, nil
}

func (c *cache) AddExplicitDependency(ctx context.Context, parent AnyResult, dep AnyResult, reason string) error {
	if parent == nil || dep == nil {
		return nil
	}

	parentShared := parent.cacheSharedResult()
	if parentShared == nil || parentShared.id == 0 || parentShared.cache != c {
		return fmt.Errorf("add explicit dependency: parent %T is not an attached result in this cache", parent)
	}
	depShared := dep.cacheSharedResult()
	if depShared == nil || depShared.id == 0 || depShared.cache != c {
		return fmt.Errorf("add explicit dependency: dep %T is not an attached result in this cache", dep)
	}
	if parentShared.id == depShared.id {
		return nil
	}

	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()

	parentRes := c.resultsByID[parentShared.id]
	if parentRes == nil {
		return fmt.Errorf("add explicit dependency: parent result %d missing from cache", parentShared.id)
	}
	depRes := c.resultsByID[depShared.id]
	if depRes == nil {
		return fmt.Errorf("add explicit dependency: dep result %d missing from cache", depShared.id)
	}
	return c.addExplicitDependencyLocked(ctx, parentRes, depRes, dep, reason)
}

func (c *cache) addExplicitDependencyLocked(
	ctx context.Context,
	parentRes *sharedResult,
	depRes *sharedResult,
	dep AnyResult,
	reason string,
) error {
	if parentRes == nil || depRes == nil {
		return nil
	}
	if parentRes.id == depRes.id {
		return nil
	}
	if parentRes.deps == nil {
		parentRes.deps = make(map[sharedResultID]struct{})
	}
	if _, ok := parentRes.deps[depRes.id]; ok {
		return nil
	}

	atomic.AddInt64(&depRes.refCount, 1)
	c.traceRefAcquired(ctx, depRes, atomic.LoadInt64(&depRes.refCount))

	parentRes.deps[depRes.id] = struct{}{}
	parentRes.heldDependencyResults = append(parentRes.heldDependencyResults, dep)
	c.traceExplicitDepAdded(ctx, parentRes.id, depRes.id, reason)
	if parentRes.depOfPersistedResult {
		if _, err := c.markResultAsDepOfPersistedLocked(ctx, depRes); err != nil {
			return err
		}
	}

	return nil
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
		if newRefCount < 0 {
			res.cache.traceRefUnderflow(ctx, res, newRefCount)
		}
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
	for i, dep := range heldDependencyResults {
		if dep == nil {
			continue
		}
		if res.cache != nil {
			res.cache.traceHeldDependencyReleasing(ctx, res, dep, newRefCount, i+1, len(heldDependencyResults))
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

	// per-call cache-hit signal for callers/tests.
	hitCache bool

	// derefView means the result should present the dereferenced view of a
	// nullable/shared wrapper payload while keeping the same sharedResult.
	derefView bool

	// nullableWrapped means the result should present the same shared payload as
	// a nullable wrapper view while keeping the same sharedResult.
	nullableWrapped bool
}

var _ AnyResult = Result[Typed]{}

func (r Result[T]) Type() *ast.Type {
	state := r.shared.loadPayloadState()
	if r.shared == nil || state.self == nil {
		var zero T
		return zero.Type()
	}
	if r.nullableWrapped {
		var innerType *ast.Type
		if r.derefView {
			if inner, ok := derefTyped(state.self); ok && inner != nil {
				innerType = inner.Type()
			}
		} else {
			innerType = state.self.Type()
		}
		if innerType != nil {
			cp := *innerType
			cp.NonNull = false
			return &cp
		}
	}
	if r.derefView {
		if inner, ok := derefTyped(state.self); ok && inner != nil && inner.Type() != nil {
			cp := *inner.Type()
			cp.NonNull = true
			return &cp
		}
	}
	return state.self.Type()
}

// ID returns the runtime handle ID of the instance.
func (r Result[T]) ID() (*call.ID, error) {
	if r.shared == nil {
		return nil, fmt.Errorf("result has no shared payload")
	}
	if r.shared.id == 0 {
		return nil, fmt.Errorf("result %T is detached", r.Self())
	}
	typ := r.Type()
	if typ == nil {
		return nil, fmt.Errorf("result %T has no type", r.Self())
	}
	return call.NewEngineResultID(uint64(r.shared.id), call.NewType(typ)), nil
}

func (r Result[T]) RecipeID() (*call.ID, error) {
	call := r.shared.loadResultCall()
	if r.shared == nil || call == nil {
		return nil, fmt.Errorf("result %T has no call frame", r.Self())
	}
	if r.shared.cache != nil {
		call.bindCache(r.shared.cache)
	}
	return call.RecipeID()
}

func (r Result[T]) RecipeDigest() (digest.Digest, error) {
	call := r.shared.loadResultCall()
	if r.shared == nil || call == nil {
		return "", fmt.Errorf("result %T has no call frame", r.Self())
	}
	if r.shared.cache != nil {
		call.bindCache(r.shared.cache)
	}
	return call.RecipeDigest()
}

func (r Result[T]) AllEffectIDs() ([]string, error) {
	call := r.shared.loadResultCall()
	if r.shared == nil || call == nil {
		return nil, fmt.Errorf("result %T has no call frame", r.Self())
	}
	if r.shared.cache != nil {
		call.bindCache(r.shared.cache)
	}
	return call.AllEffectIDs()
}

func (r Result[T]) ContentPreferredDigest() (digest.Digest, error) {
	call := r.shared.loadResultCall()
	if r.shared == nil || call == nil {
		return "", fmt.Errorf("result %T has no call frame", r.Self())
	}
	if r.shared.cache != nil {
		call.bindCache(r.shared.cache)
	}
	return call.ContentPreferredDigest()
}

func (r Result[T]) ResultCall() (*ResultCall, error) {
	call := r.shared.loadResultCall()
	if r.shared == nil || call == nil {
		return nil, fmt.Errorf("result %T has no call frame", r.Self())
	}
	call = call.clone()
	if r.shared.cache != nil {
		call.bindCache(r.shared.cache)
	}
	return call, nil
}

func (r Result[T]) Self() T {
	self, ok := UnwrapAs[T](r.Unwrap())
	if !ok {
		var zero T
		return zero
	}
	return self
}

func (r Result[T]) SetField(field reflect.Value) error {
	return assign(field, r.Self())
}

// Unwrap returns the inner value of the instance.
func (r Result[T]) Unwrap() Typed {
	state := r.shared.loadPayloadState()
	if r.shared == nil {
		var zero T
		return zero
	}
	if state.self == nil {
		var zero T
		return zero
	}
	if r.nullableWrapped {
		wrapped := state.self
		if r.derefView {
			if inner, ok := derefTyped(state.self); ok && inner != nil {
				wrapped = inner
			}
		}
		return DynamicNullable{
			Elem:  wrapped,
			Value: wrapped,
			Valid: true,
		}
	}
	if r.derefView {
		if inner, ok := derefTyped(state.self); ok && inner != nil {
			return inner
		}
	}
	return state.self
}

func (r Result[T]) DerefValue() (AnyResult, bool) {
	state := r.shared.loadPayloadState()
	if r.derefView {
		return r, true
	}
	if r.nullableWrapped {
		r.nullableWrapped = false
		return r, true
	}
	if r.shared == nil || state.self == nil {
		return r, true
	}
	inner, valid := derefTyped(state.self)
	if !valid {
		if _, ok := any(state.self).(Derefable); ok {
			return nil, false
		}
		return r, true
	}
	if anyRes, ok := inner.(AnyResult); ok {
		// Suspicious: WithSafeToPersistCache currently clones into a detached
		// payload. If we hit weird secret/function caching bugs, check whether
		// this propagation is incorrectly stripping attachment from an already-
		// attached inner result.
		return anyRes.WithSafeToPersistCache(r.IsSafeToPersistCache()), true
	}
	return r.resultWithDerefView(), true
}

func (r Result[T]) NthValue(ctx context.Context, nth int) (AnyResult, error) {
	self := r.Self()
	enumerableSelf, ok := any(self).(Enumerable)
	if !ok {
		return nil, fmt.Errorf("cannot get %dth value from %T", nth, self)
	}
	parentCall := r.shared.loadResultCall()
	if r.shared == nil || parentCall == nil {
		return nil, fmt.Errorf("cannot get %dth value from %T without call frame", nth, self)
	}
	detached, err := enumerableSelf.NthValue(nth, parentCall)
	if err != nil || detached == nil {
		return detached, err
	}
	if r.shared.id == 0 {
		return detached, nil
	}
	srv := CurrentDagqlServer(ctx)
	if srv == nil {
		return detached, nil
	}
	if parentCall.Type == nil || parentCall.Type.Elem == nil {
		return nil, fmt.Errorf("cannot get %dth value from %T without element type", nth, self)
	}
	req := &CallRequest{
		ResultCall: parentCall.fork(),
	}
	req.Type = req.Type.Elem.clone()
	req.Receiver = &ResultCallRef{ResultID: uint64(r.shared.id), shared: r.shared}
	req.Nth = int64(nth)
	callCtx := srvToContext(ctx, srv)
	if shared := detached.cacheSharedResult(); shared != nil && shared.id == 0 {
		shared.storeResultCall(req.ResultCall.clone())
		if r.shared.cache != nil {
			shared.loadResultCall().bindCache(r.shared.cache)
		}
	}
	if res, err := srv.Cache.GetOrInitCall(callCtx, srv, req, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("cache miss")
	}); err == nil {
		return res, nil
	}
	return srv.Cache.GetOrInitCall(callCtx, srv, req, func(context.Context) (AnyResult, error) {
		return detached, nil
	})
}

// withDetachedPayload clones shared payload so per-result metadata changes do
// not affect other Results. It intentionally reuses the authoritative call
// frame; call-frame mutations must fork explicitly.
func (r Result[T]) withDetachedPayload() Result[T] {
	if r.shared != nil {
		state := r.shared.loadPayloadState()
		r.shared = &sharedResult{
			self:                   state.self,
			isObject:               state.isObject,
			resultCall:             r.shared.loadResultCall(),
			hasValue:               state.hasValue,
			postCall:               r.shared.postCall,
			safeToPersistCache:     r.shared.safeToPersistCache,
			persistedEnvelope:      state.persistedEnvelope,
			persistedSnapshotLinks: slices.Clone(r.shared.persistedSnapshotLinks),
			outputEffectIDs:        slices.Clone(r.shared.outputEffectIDs),
			createdAtUnixNano:      state.createdAtUnixNano,
			lastUsedAtUnixNano:     state.lastUsedAtUnixNano,
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

func (r Result[T]) resultWithDerefView() Result[T] {
	r.derefView = true
	r.nullableWrapped = false
	return r
}

func (r Result[T]) withDerefViewAny() AnyResult {
	return r.resultWithDerefView()
}

func (r Result[T]) resultNullableWrapped() Result[T] {
	r.nullableWrapped = true
	return r
}

func (r Result[T]) NullableWrapped() AnyResult {
	return r.resultNullableWrapped()
}

func derefTyped(val Typed) (Typed, bool) {
	derefable, ok := any(val).(Derefable)
	if !ok {
		return nil, false
	}
	return derefable.Deref()
}

func (r Result[T]) withDetachedCallPayload() Result[T] {
	r = r.withDetachedPayload()
	if r.shared != nil {
		if frame := r.shared.loadResultCall(); frame != nil {
			r.shared.storeResultCall(frame.fork())
		}
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

func (r Result[T]) ResultWithCall(frame *ResultCall) Result[T] {
	r = r.withDetachedPayload()
	r.shared.storeResultCall(frame.clone())
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
	r = r.withDetachedCallPayload()
	frame := r.shared.loadResultCall()
	if r.shared == nil || frame == nil {
		panic(fmt.Sprintf("set content digest on %T: missing call frame", r.Self()))
	}
	replaced := false
	for i, extra := range frame.ExtraDigests {
		if extra.Label != call.ExtraDigestLabelContent {
			continue
		}
		frame.ExtraDigests[i].Digest = contentDigest
		replaced = true
		break
	}
	if !replaced {
		frame.ExtraDigests = append(frame.ExtraDigests, call.ExtraDigest{
			Label:  call.ExtraDigestLabelContent,
			Digest: contentDigest,
		})
	}
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
	id, err := r.ID()
	if err != nil {
		return fmt.Sprintf("%s@<detached>", typ.Name())
	}
	enc, err := id.Encode()
	if err != nil {
		return fmt.Sprintf("%s@<encode-error>", typ.Name())
	}
	return fmt.Sprintf("%s@%s", typ.Name(), enc)
}

func (r Result[T]) PostCall(ctx context.Context) error {
	if r.shared != nil && r.shared.postCall != nil {
		return r.shared.postCall(ctx)
	}
	return nil
}

func (r Result[T]) MarshalJSON() ([]byte, error) {
	id, err := r.ID()
	if err != nil {
		return nil, err
	}
	return json.Marshal(id)
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
	state := r.shared.loadPayloadState()
	if r.derefView {
		return r, true
	}
	if r.shared == nil || state.self == nil {
		return r, true
	}
	inner, valid := derefTyped(state.self)
	if !valid {
		if _, ok := any(state.self).(Derefable); ok {
			return nil, false
		}
		return r, true
	}
	if anyRes, ok := inner.(AnyResult); ok {
		// Suspicious: WithSafeToPersistCache currently clones into a detached
		// payload. If we hit weird secret/function caching bugs, check whether
		// this propagation is incorrectly stripping attachment from an already-
		// attached inner result.
		return anyRes.WithSafeToPersistCache(r.IsSafeToPersistCache()), true
	}
	r.Result = r.Result.resultWithDerefView()
	return r, true
}

func (r ObjectResult[T]) SetField(field reflect.Value) error {
	return assign(field, r.Result)
}

// ObjectType returns the ObjectType of the instance.
func (r ObjectResult[T]) ObjectType() ObjectType {
	return r.class
}

func (r ObjectResult[T]) Receiver(ctx context.Context, srv *Server) (AnyObjectResult, error) {
	if srv == nil {
		return nil, fmt.Errorf("receiver: server is nil")
	}
	call, err := r.ResultCall()
	if err != nil {
		return nil, err
	}
	if call.Receiver == nil {
		return nil, nil
	}
	if call.Receiver.ResultID == 0 {
		return nil, fmt.Errorf("receiver: result is detached")
	}
	res, err := srv.Cache.LoadResultByResultID(srvToContext(ctx, srv), srv, call.Receiver.ResultID)
	if err != nil {
		return nil, fmt.Errorf("receiver: load result %d: %w", call.Receiver.ResultID, err)
	}
	obj, ok := res.(AnyObjectResult)
	if !ok {
		return nil, fmt.Errorf("receiver: result %d is %T, not object result", call.Receiver.ResultID, res)
	}
	return obj, nil
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

func (r ObjectResult[T]) ObjectResultWithCall(frame *ResultCall) ObjectResult[T] {
	r.Result = r.Result.ResultWithCall(frame)
	return r
}

func (r ObjectResult[T]) objectResultWithDerefView() AnyResult {
	r.Result = r.Result.resultWithDerefView()
	return r
}

func (r ObjectResult[T]) withDerefViewAny() AnyResult {
	return r.objectResultWithDerefView()
}

func (r ObjectResult[T]) NullableWrapped() AnyResult {
	return r.Result.resultNullableWrapped()
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
		if err := c.persistCurrentState(ctx); err != nil {
			slog.Error("failed to persist dagql cache during close", "err", err)
			c.closeErr = errors.Join(c.closeErr, err)
		}
		if c.closeErr != nil {
			if closeErr := closeCacheDBs(c.sqlDB, c.pdb); closeErr != nil {
				slog.Error("failed to close dagql persistence databases after cache close error", "err", closeErr)
				c.closeErr = errors.Join(c.closeErr, closeErr)
			}
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
		if closeErr := closeCacheDBs(c.sqlDB, c.pdb); closeErr != nil {
			slog.Error("failed to close dagql persistence databases", "err", closeErr)
			c.closeErr = closeErr
		}
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
		state := res.loadPayloadState()
		createdAt := state.createdAtUnixNano
		lastUsedAt := state.lastUsedAtUnixNano
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
		// TODO: Investigate whether this gate is inverted. Prune selection skips
		// depOfPersistedResult entries, but this size pass only measures
		// depOfPersistedResult entries.
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
	}

	return closure
}

//nolint:gocyclo // Core cache lookup/insert flow is intentionally centralized here.
func (c *cache) GetOrInitCall(
	ctx context.Context,
	resolver TypeResolver,
	req *CallRequest,
	fn func(context.Context) (AnyResult, error),
) (AnyResult, error) {
	if resolver == nil {
		return nil, errors.New("get or init call: type resolver is nil")
	}
	if req == nil || req.ResultCall == nil {
		return nil, fmt.Errorf("call request is nil")
	}
	req.ResultCall.bindCache(c)
	ctx = ContextWithCall(ctx, req.ResultCall)

	if req.DoNotCache {
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
			resultCall:         req.ResultCall.clone(),
			hasValue:           true,
			postCall:           val.PostCall,
			safeToPersistCache: val.IsSafeToPersistCache(),
		}
		detached.resultCall.bindCache(c)
		outputEffects, err := detached.resultCall.AllEffectIDs()
		if err != nil {
			return nil, fmt.Errorf("derive do-not-cache output effects: %w", err)
		}
		detached.outputEffectIDs = outputEffects
		if onReleaser, ok := UnwrapAs[OnReleaser](val); ok {
			detached.onRelease = onReleaser.OnRelease
		}
		detached.isObject, err = resultIsObject(val, resolver)
		if err != nil {
			return nil, fmt.Errorf("classify do-not-cache result: %w", err)
		}
		if detached.isObject {
			normalized, err := wrapSharedResultWithResolver(detached, false, resolver)
			if err != nil {
				return nil, fmt.Errorf("normalize do-not-cache object result: %w", err)
			}
			return normalized, nil
		}
		return Result[Typed]{shared: detached}, nil
	}

	callDigest, err := req.RecipeDigest()
	if err != nil {
		return nil, fmt.Errorf("derive request digest: %w", err)
	}
	requestSelf, requestInputRefs, err := req.SelfDigestAndInputRefs()
	if err != nil {
		return nil, fmt.Errorf("derive request term digests: %w", err)
	}
	requestInputs := make([]digest.Digest, 0, len(requestInputRefs))
	for _, ref := range requestInputRefs {
		dig, err := ref.InputDigest()
		if err != nil {
			return nil, fmt.Errorf("derive request term input digest: %w", err)
		}
		requestInputs = append(requestInputs, dig)
	}
	callKey := callDigest.String()
	if ctx.Value(cacheContextKey{callKey, c}) != nil {
		return nil, ErrCacheRecursiveCall
	}
	callConcKeys := callConcurrencyKeys{
		callKey:        callKey,
		concurrencyKey: req.ConcurrencyKey,
	}

	c.egraphMu.Lock()
	hitRes, hit, err := c.lookupCacheForRequest(ctx, req, callDigest, requestSelf, requestInputs, requestInputRefs)
	c.egraphMu.Unlock()
	if err != nil {
		return nil, err
	}
	if hit {
		return c.ensurePersistedHitValueLoaded(ctx, resolver, hitRes)
	}

	c.callsMu.Lock()
	if c.ongoingCalls == nil {
		c.ongoingCalls = make(map[callConcurrencyKeys]*ongoingCall)
	}

	if req.ConcurrencyKey != "" {
		if oc := c.ongoingCalls[callConcKeys]; oc != nil {
			if req.IsPersistable {
				oc.isPersistable = true
			}
			// already an ongoing call
			oc.waiters++
			c.callsMu.Unlock()
			return c.wait(ctx, resolver, oc, req)
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
		isPersistable:       req.IsPersistable,
		ttlSeconds:          req.TTL,
		waitCh:              make(chan struct{}),
		cancel:              cancel,
		waiters:             1,
	}

	if req.ConcurrencyKey != "" {
		c.ongoingCalls[callConcKeys] = oc
	}

	go func() {
		defer close(oc.waitCh)
		val, err := fn(callCtx)
		oc.err = err
		oc.val = val
	}()

	c.callsMu.Unlock()
	return c.wait(ctx, resolver, oc, req)
}

func (c *cache) lookupCallRequest(
	ctx context.Context,
	resolver TypeResolver,
	req *CallRequest,
) (AnyResult, bool, error) {
	if resolver == nil {
		return nil, false, errors.New("lookup call request: type resolver is nil")
	}
	if req == nil || req.ResultCall == nil {
		return nil, false, fmt.Errorf("call request is nil")
	}
	req.ResultCall.bindCache(c)

	callDigest, err := req.RecipeDigest()
	if err != nil {
		return nil, false, fmt.Errorf("derive request digest: %w", err)
	}
	requestSelf, requestInputRefs, err := req.SelfDigestAndInputRefs()
	if err != nil {
		return nil, false, fmt.Errorf("derive request term digests: %w", err)
	}
	requestInputs := make([]digest.Digest, 0, len(requestInputRefs))
	for _, ref := range requestInputRefs {
		dig, err := ref.InputDigest()
		if err != nil {
			return nil, false, fmt.Errorf("derive request term input digest: %w", err)
		}
		requestInputs = append(requestInputs, dig)
	}

	c.egraphMu.Lock()
	hitRes, hit, err := c.lookupCacheForRequest(ctx, req, callDigest, requestSelf, requestInputs, requestInputRefs)
	c.egraphMu.Unlock()
	if err != nil {
		return nil, false, err
	}
	if !hit {
		return nil, false, nil
	}

	loadedHit, loadErr := c.ensurePersistedHitValueLoaded(ctx, resolver, hitRes)
	if loadErr != nil {
		return nil, false, loadErr
	}
	return loadedHit, true, nil
}

func (c *cache) LookupCacheForDigests(
	ctx context.Context,
	resolver TypeResolver,
	recipeDigest digest.Digest,
	extraDigests []call.ExtraDigest,
) (AnyResult, bool, error) {
	if resolver == nil {
		return nil, false, errors.New("lookup cache for digests: type resolver is nil")
	}
	if recipeDigest == "" {
		return nil, false, nil
	}

	c.egraphMu.Lock()
	match := c.lookupMatchForDigestsLocked(recipeDigest, extraDigests)
	c.traceLookupAttempt(ctx, recipeDigest.String(), "", nil, false)
	hitRes := match.hitRes
	if hitRes == nil {
		c.traceLookupMissNoMatch(ctx, recipeDigest.String(), false, -1, "", 0)
		c.egraphMu.Unlock()
		return nil, false, nil
	}

	now := time.Now()
	nowUnix := now.Unix()
	hitRes.expiresAtUnix = mergeSharedResultExpiryUnix(
		hitRes.expiresAtUnix,
		candidateSharedResultExpiryUnix(nowUnix, 0),
	)
	touchSharedResultLastUsed(hitRes, now.UnixNano())
	newRefCount := atomic.AddInt64(&hitRes.refCount, 1)
	c.traceRefAcquired(ctx, hitRes, newRefCount)
	retRes := Result[Typed]{
		shared:   hitRes,
		hitCache: true,
	}
	c.traceLookupHit(ctx, recipeDigest.String(), hitRes, match.hitTerm, match.termDigest)
	c.egraphMu.Unlock()
	loadedHit, err := c.ensurePersistedHitValueLoaded(ctx, resolver, retRes)
	if err != nil {
		return nil, false, err
	}
	return loadedHit, true, nil
}

func (c *cache) wait(
	ctx context.Context,
	resolver TypeResolver,
	oc *ongoingCall,
	req *CallRequest,
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
		oc.initCompletedResultErr = c.initCompletedResult(ctx, resolver, oc, req, sessionID)
	})
	if oc.initCompletedResultErr != nil {
		return nil, oc.initCompletedResultErr
	}
	if oc.res == nil {
		return nil, fmt.Errorf("cache wait completed without initialized result")
	}

	atomic.AddInt64(&oc.res.refCount, 1)
	c.traceRefAcquired(ctx, oc.res, atomic.LoadInt64(&oc.res.refCount))
	touchSharedResultLastUsed(oc.res, time.Now().UnixNano())

	retRes := Result[Typed]{
		shared:   oc.res,
		hitCache: false,
	}

	if !retRes.shared.loadPayloadState().hasValue {
		return retRes, nil
	}
	retResAny, err := wrapSharedResultWithResolver(oc.res, false, resolver)
	if err != nil {
		return nil, fmt.Errorf("wait: reconstruct result: %w", err)
	}
	return retResAny, nil
}

func (c *cache) initCompletedResult(ctx context.Context, resolver TypeResolver, oc *ongoingCall, req *CallRequest, sessionID string) error {
	resWasCacheBacked := false
	now := time.Now()
	var (
		resultTermSelf   digest.Digest
		resultTermInputs []digest.Digest
		resultTermRefs   []ResultCallStructuralInputRef
		hasResultTerm    bool
	)
	if req == nil || req.ResultCall == nil {
		return fmt.Errorf("call request is nil")
	}
	req.ResultCall.bindCache(c)

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
				if frame := shared.loadResultCall(); frame != nil {
					oc.res.storeResultCall(frame.clone())
					oc.res.loadResultCall().bindCache(c)
				}
			}
			if oc.res.loadResultCall() == nil {
				oc.res.storeResultCall(req.ResultCall.clone())
				oc.res.loadResultCall().bindCache(c)
			}
			oc.res.hasValue = true
			oc.res.postCall = oc.val.PostCall
			oc.res.safeToPersistCache = oc.val.IsSafeToPersistCache()

			if onReleaser, ok := UnwrapAs[OnReleaser](oc.val); ok {
				oc.res.onRelease = onReleaser.OnRelease
			}
			isObject, err := resultIsObject(oc.val, resolver)
			if err != nil {
				return fmt.Errorf("classify completed result: %w", err)
			}
			oc.res.isObject = isObject
		}
	}
	requestForIndex := req
	// TTL-bounded unsafe values must be session-scoped to avoid cross-session reuse.
	if oc.ttlSeconds > 0 && !oc.res.safeToPersistCache {
		requestForIndex = &CallRequest{
			ResultCall:     req.ResultCall.fork(),
			ConcurrencyKey: req.ConcurrencyKey,
			TTL:            req.TTL,
			DoNotCache:     req.DoNotCache,
			IsPersistable:  req.IsPersistable,
		}
		requestForIndex.ImplicitInputs = append(requestForIndex.ImplicitInputs, &ResultCallArg{
			Name: "sessionID",
			Value: &ResultCallLiteral{
				Kind:        ResultCallLiteralKindString,
				StringValue: sessionID,
			},
		})
	}
	requestForIndex.ResultCall.bindCache(c)

	if oc.res.createdAtUnixNano == 0 {
		oc.res.createdAtUnixNano = now.UnixNano()
	}
	touchSharedResultLastUsed(oc.res, now.UnixNano())
	if oc.res.recordType == "" {
		oc.res.recordType = requestForIndex.Field
	}
	if oc.res.recordType == "" {
		oc.res.recordType = "dagql.unknown"
	}
	if oc.res.description == "" {
		oc.res.description = requestForIndex.Field
	}
	if oc.res.description == "" {
		if reqDig, err := requestForIndex.RecipeDigest(); err == nil {
			oc.res.description = reqDig.String()
		}
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
	if !resWasCacheBacked {
		if resultCall := oc.res.loadResultCall(); resultCall != nil {
			outputEffects, err := resultCall.AllEffectIDs()
			if err != nil {
				return fmt.Errorf("derive result output effects: %w", err)
			}
			oc.res.outputEffectIDs = outputEffects

			selfDigest, inputRefs, deriveErr := resultCall.SelfDigestAndInputRefs()
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
	}

	requestDigest, err := requestForIndex.RecipeDigest()
	if err != nil {
		return fmt.Errorf("derive request digest: %w", err)
	}
	requestSelf, requestInputRefs, err := requestForIndex.SelfDigestAndInputRefs()
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
	var responseDigest digest.Digest
	if resultCall := oc.res.loadResultCall(); resultCall != nil {
		responseDigest, err = resultCall.RecipeDigest()
		if err != nil {
			return fmt.Errorf("derive result digest: %w", err)
		}
	}
	var resultCallDepIDs []sharedResultID
	if !resWasCacheBacked {
		if resultCall := oc.res.loadResultCall(); resultCall != nil {
			seenResults := map[sharedResultID]struct{}{}
			seenCalls := map[*ResultCall]struct{}{}

			var walkFrame func(*ResultCall) error
			var walkRef func(*ResultCallRef) error
			var walkLiteral func(*ResultCallLiteral) error

			walkRef = func(ref *ResultCallRef) error {
				if ref == nil {
					return nil
				}
				if ref.Call != nil {
					return walkFrame(ref.Call)
				}
				if ref.ResultID == 0 {
					return nil
				}
				resultID := sharedResultID(ref.ResultID)
				if resultID == oc.res.id {
					return nil
				}
				if _, seen := seenResults[resultID]; seen {
					return nil
				}
				seenResults[resultID] = struct{}{}
				resultCallDepIDs = append(resultCallDepIDs, resultID)
				return nil
			}

			walkLiteral = func(lit *ResultCallLiteral) error {
				if lit == nil {
					return nil
				}
				switch lit.Kind {
				case ResultCallLiteralKindResultRef:
					return walkRef(lit.ResultRef)
				case ResultCallLiteralKindList:
					for _, item := range lit.ListItems {
						if err := walkLiteral(item); err != nil {
							return err
						}
					}
				case ResultCallLiteralKindObject:
					for _, field := range lit.ObjectFields {
						if field == nil {
							continue
						}
						if err := walkLiteral(field.Value); err != nil {
							return err
						}
					}
				}
				return nil
			}

			walkFrame = func(frame *ResultCall) error {
				if frame == nil {
					return nil
				}
				if _, seen := seenCalls[frame]; seen {
					return nil
				}
				seenCalls[frame] = struct{}{}

				if err := walkRef(frame.Receiver); err != nil {
					return fmt.Errorf("receiver: %w", err)
				}
				if frame.Module != nil {
					if err := walkRef(frame.Module.ResultRef); err != nil {
						return fmt.Errorf("module: %w", err)
					}
				}
				for _, arg := range frame.Args {
					if arg == nil {
						continue
					}
					if err := walkLiteral(arg.Value); err != nil {
						return fmt.Errorf("arg %q: %w", arg.Name, err)
					}
				}
				for _, input := range frame.ImplicitInputs {
					if input == nil {
						continue
					}
					if err := walkLiteral(input.Value); err != nil {
						return fmt.Errorf("implicit input %q: %w", input.Name, err)
					}
				}
				return nil
			}

			if err := walkFrame(resultCall); err != nil {
				return fmt.Errorf("collect result call dependencies: %w", err)
			}
		}
	}

	c.egraphMu.Lock()
	resultCall := oc.res.loadResultCall()
	indexErr := c.indexWaitResultInEgraphLocked(
		ctx,
		requestForIndex.ResultCall,
		resultCall,
		requestDigest,
		responseDigest,
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
	for _, depID := range resultCallDepIDs {
		depRes := c.resultsByID[depID]
		if depRes == nil {
			c.egraphMu.Unlock()
			return fmt.Errorf("retain result call ref %d: missing cached result", depID)
		}
		if oc.res.deps == nil {
			oc.res.deps = make(map[sharedResultID]struct{})
		}
		if _, alreadyHeld := oc.res.deps[depID]; alreadyHeld {
			continue
		}
		newRefCount := atomic.AddInt64(&depRes.refCount, 1)
		c.traceRefAcquired(ctx, depRes, newRefCount)
		oc.res.deps[depID] = struct{}{}
		oc.res.heldDependencyResults = append(oc.res.heldDependencyResults, Result[Typed]{shared: depRes})
	}
	if oc.isPersistable {
		if _, err := c.markResultAsDepOfPersistedLocked(ctx, oc.res); err != nil {
			c.egraphMu.Unlock()
			return err
		}
	}
	c.egraphMu.Unlock()

	if err := c.attachOwnedResults(ctx, resolver, oc.res, oc.val); err != nil {
		return err
	}

	return nil
}

func (c *cache) attachOwnedResults(ctx context.Context, resolver TypeResolver, parent *sharedResult, val AnyResult) (rerr error) {
	if parent == nil || val == nil {
		return nil
	}
	withOwned, ok := UnwrapAs[HasOwnedResults](val)
	if !ok {
		return nil
	}
	self := Result[Typed]{shared: parent}
	var attachedSelf AnyResult = self
	parentState := parent.loadPayloadState()
	if parentState.hasValue && parentState.isObject {
		objSelf, err := wrapSharedResultWithResolver(parent, false, resolver)
		if err != nil {
			return fmt.Errorf("attach owned results: reconstruct attached self: %w", err)
		}
		attachedSelf = objSelf
	}
	var temporarilyAttachedDeps []AnyResult
	deps, err := withOwned.AttachOwnedResults(ctx, attachedSelf, func(child AnyResult) (AnyResult, error) {
		wasDetached := false
		if shared := child.cacheSharedResult(); shared == nil || shared.id == 0 {
			wasDetached = true
		}
		attached, err := c.AttachResult(ctx, resolver, child)
		if err != nil {
			return nil, err
		}
		if wasDetached && attached != nil {
			temporarilyAttachedDeps = append(temporarilyAttachedDeps, attached)
		}
		return attached, nil
	})
	if err != nil {
		return err
	}
	defer func() {
		for _, dep := range temporarilyAttachedDeps {
			if dep == nil {
				continue
			}
			rerr = errors.Join(rerr, dep.Release(ctx))
		}
	}()
	if len(deps) == 0 || parent.id == 0 {
		return nil
	}

	seen := make(map[sharedResultID]struct{}, len(deps))
	for _, dep := range deps {
		if dep == nil {
			continue
		}
		attachedDepRes := dep.cacheSharedResult()
		if attachedDepRes == nil || attachedDepRes.id == 0 {
			return fmt.Errorf("attach owned result %T: unexpected detached result", dep)
		}
		if attachedDepRes.id == parent.id {
			continue
		}
		if _, ok := seen[attachedDepRes.id]; ok {
			continue
		}
		seen[attachedDepRes.id] = struct{}{}
		if err := c.AddExplicitDependency(ctx, attachedSelf, dep, "attached_owned_result"); err != nil {
			return err
		}
	}

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
	state := res.loadPayloadState()
	if res == nil || !state.hasValue || state.self == nil {
		return 0, false, nil
	}
	sizer, ok := any(state.self).(cacheUsageSizer)
	if !ok {
		return 0, false, nil
	}
	return sizer.CacheUsageSize(ctx)
}

func cacheUsageIdentity(res *sharedResult) (string, bool) {
	state := res.loadPayloadState()
	if res == nil || !state.hasValue || state.self == nil {
		return "", false
	}
	identityer, ok := any(state.self).(hasCacheUsageIdentity)
	if !ok {
		return "", false
	}
	return identityer.CacheUsageIdentity()
}

func cacheUsageSizeMayChange(res *sharedResult) bool {
	state := res.loadPayloadState()
	if res == nil || !state.hasValue || state.self == nil {
		return false
	}
	mutableSizer, ok := any(state.self).(cacheUsageMayChange)
	if !ok {
		return false
	}
	return mutableSizer.CacheUsageMayChange()
}
