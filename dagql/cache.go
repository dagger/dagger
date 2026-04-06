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
	"time"

	telemetry "github.com/dagger/otel-go"
	set "github.com/hashicorp/go-set/v3"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
	_ "modernc.org/sqlite"

	"github.com/dagger/dagger/dagql/call"
	persistdb "github.com/dagger/dagger/dagql/persistdb"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
)

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

type persistedEdge struct {
	resultID          sharedResultID
	createdAtUnixNano int64
	expiresAtUnix     int64
	unpruneable       bool
}

const cachePersistenceSchemaVersion = "12"

var ErrCacheRecursiveCall = fmt.Errorf("recursive call detected")
var ErrPersistStateNotReady = errors.New("persist state not ready")

func NewCache(ctx context.Context, dbPath string, snapshotManager bkcache.SnapshotManager) (*Cache, error) {
	c := &Cache{
		traceBootID:     newTraceBootID(),
		snapshotManager: snapshotManager,
	}

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

func (c *Cache) trackSessionResult(ctx context.Context, sessionID string, res AnyResult, hitCache bool) {
	if c == nil || sessionID == "" || res == nil {
		return
	}
	shared := res.cacheSharedResult()
	if shared == nil || shared.id == 0 {
		return
	}

	acquired := false
	trackedCount := 0
	c.sessionMu.Lock()
	if c.sessionResultIDsBySession == nil {
		c.sessionResultIDsBySession = make(map[string]map[sharedResultID]struct{})
	}
	if c.sessionResultIDsBySession[sessionID] == nil {
		c.sessionResultIDsBySession[sessionID] = make(map[sharedResultID]struct{})
	}
	if _, found := c.sessionResultIDsBySession[sessionID][shared.id]; !found {
		c.sessionResultIDsBySession[sessionID][shared.id] = struct{}{}
		acquired = true
	}
	trackedCount = len(c.sessionResultIDsBySession[sessionID])
	c.sessionMu.Unlock()

	if acquired {
		c.egraphMu.Lock()
		if c.resultsByID[shared.id] == shared {
			c.incrementIncomingOwnershipLocked(ctx, shared)
		}
		c.egraphMu.Unlock()
	}

	if c.traceEnabled() {
		c.traceSessionResultTracked(ctx, sessionID, res, hitCache, trackedCount)
	}
}

func (c *Cache) recomputeRequiredSessionResourcesLocked(res *sharedResult) error {
	if res == nil {
		return nil
	}

	var reqs *set.TreeSet[SessionResourceHandle]
	if res.sessionResourceHandle != "" {
		reqs = set.NewTreeSet(compareSessionResourceHandles)
		reqs.Insert(res.sessionResourceHandle)
	}
	for depID := range res.deps {
		dep := c.resultsByID[depID]
		if dep == nil {
			return fmt.Errorf("recompute required session resources: missing dep result %d", depID)
		}
		if dep.requiredSessionResources == nil {
			continue
		}
		if reqs == nil {
			reqs = dep.requiredSessionResources.Copy()
		} else {
			reqs = reqs.Union(dep.requiredSessionResources).(*set.TreeSet[SessionResourceHandle])
		}
	}
	if reqs == nil || reqs.Empty() {
		res.requiredSessionResources = nil
		return nil
	}
	res.requiredSessionResources = reqs
	return nil
}

func (c *Cache) BindSessionResource(_ context.Context, sessionID string, clientID string, handle SessionResourceHandle, value any) error {
	if c == nil {
		return errors.New("bind session resource: nil cache")
	}
	if sessionID == "" {
		return errors.New("bind session resource: empty session ID")
	}
	if clientID == "" {
		return errors.New("bind session resource: empty client ID")
	}
	if handle == "" {
		return errors.New("bind session resource: empty handle")
	}
	if value == nil {
		return errors.New("bind session resource: nil concrete value")
	}

	c.sessionMu.Lock()
	if c.sessionResourcesBySession == nil {
		c.sessionResourcesBySession = make(map[string]map[SessionResourceHandle]*sessionResourceBindings)
	}
	if c.sessionResourcesBySession[sessionID] == nil {
		c.sessionResourcesBySession[sessionID] = make(map[SessionResourceHandle]*sessionResourceBindings)
	}
	sessionBindings := c.sessionResourcesBySession[sessionID]
	bindings := sessionBindings[handle]
	if bindings == nil {
		bindings = &sessionResourceBindings{
			byClientID: make(map[string]any),
		}
		sessionBindings[handle] = bindings
	}
	bindings.byClientID[clientID] = value
	bindings.latestClientID = clientID
	if c.sessionHandlesBySession == nil {
		c.sessionHandlesBySession = make(map[string]*set.TreeSet[SessionResourceHandle])
	}
	if c.sessionHandlesBySession[sessionID] == nil {
		c.sessionHandlesBySession[sessionID] = set.NewTreeSet(compareSessionResourceHandles)
	}
	c.sessionHandlesBySession[sessionID].Insert(handle)
	c.sessionMu.Unlock()

	return nil
}

func (c *Cache) ResolveSessionResource(
	_ context.Context,
	sessionID string,
	clientID string,
	handle SessionResourceHandle,
) (any, error) {
	if c == nil {
		return nil, errors.New("resolve session resource: nil cache")
	}
	if sessionID == "" {
		return nil, errors.New("resolve session resource: empty session ID")
	}
	if clientID == "" {
		return nil, errors.New("resolve session resource: empty client ID")
	}
	if handle == "" {
		return nil, errors.New("resolve session resource: empty handle")
	}

	c.sessionMu.Lock()
	sessionBindings := c.sessionResourcesBySession[sessionID]
	bindings := sessionBindings[handle]
	if bindings == nil || len(bindings.byClientID) == 0 {
		c.sessionMu.Unlock()
		return nil, fmt.Errorf("resolve session resource %q: no bound resource for session %q", handle, sessionID)
	}
	if value, ok := bindings.byClientID[clientID]; ok {
		c.sessionMu.Unlock()
		return value, nil
	}
	if bindings.latestClientID != "" {
		if value, ok := bindings.byClientID[bindings.latestClientID]; ok {
			c.sessionMu.Unlock()
			return value, nil
		}
	}
	c.sessionMu.Unlock()
	return nil, fmt.Errorf("resolve session resource %q: no binding for client %q in session %q", handle, clientID, sessionID)
}

func (c *Cache) captureSessionLazySpanContext(ctx context.Context, sessionID string, res AnyResult) {
	if c == nil || sessionID == "" || res == nil {
		return
	}
	shared := res.cacheSharedResult()
	if shared == nil || shared.id == 0 {
		return
	}
	spanCtx := trace.SpanContextFromContext(ctx)
	if !spanCtx.IsValid() {
		return
	}

	c.sessionMu.Lock()
	if c.sessionLazySpansBySession == nil {
		c.sessionLazySpansBySession = make(map[string]map[sharedResultID]trace.SpanContext)
	}
	if c.sessionLazySpansBySession[sessionID] == nil {
		c.sessionLazySpansBySession[sessionID] = make(map[sharedResultID]trace.SpanContext)
	}
	if _, exists := c.sessionLazySpansBySession[sessionID][shared.id]; !exists {
		c.sessionLazySpansBySession[sessionID][shared.id] = spanCtx
	}
	c.sessionMu.Unlock()
}

func (c *Cache) sessionLazySpanContext(sessionID string, resultID sharedResultID) (trace.SpanContext, bool) {
	if c == nil || sessionID == "" || resultID == 0 {
		return trace.SpanContext{}, false
	}

	c.sessionMu.Lock()
	spanCtx := c.sessionLazySpansBySession[sessionID][resultID]
	c.sessionMu.Unlock()
	if !spanCtx.IsValid() {
		return trace.SpanContext{}, false
	}
	return spanCtx, true
}

func HasPendingLazyEvaluation(res AnyResult) bool {
	if res == nil {
		return false
	}
	shared := res.cacheSharedResult()
	if shared == nil || shared.id == 0 {
		return false
	}

	shared.lazyMu.Lock()
	defer shared.lazyMu.Unlock()
	if shared.lazyEvalComplete {
		return false
	}
	if shared.lazyEval != nil {
		return true
	}
	return lazyEvalFuncOfResult(res) != nil
}

func (c *Cache) trackSessionArbitrary(sessionID string, res ArbitraryCachedResult) {
	if c == nil || sessionID == "" || res == nil {
		return
	}
	shared, ok := res.(arbitraryResult)
	if !ok || shared.shared == nil {
		return
	}

	acquired := false
	c.sessionMu.Lock()
	if c.sessionArbitraryCallKeysBySession == nil {
		c.sessionArbitraryCallKeysBySession = make(map[string]map[string]struct{})
	}
	if c.sessionArbitraryCallKeysBySession[sessionID] == nil {
		c.sessionArbitraryCallKeysBySession[sessionID] = make(map[string]struct{})
	}
	if _, found := c.sessionArbitraryCallKeysBySession[sessionID][shared.shared.callKey]; !found {
		c.sessionArbitraryCallKeysBySession[sessionID][shared.shared.callKey] = struct{}{}
		acquired = true
	}
	c.sessionMu.Unlock()

	if acquired {
		c.callsMu.Lock()
		shared.shared.ownerSessionCount++
		c.callsMu.Unlock()
	}
}

func (c *Cache) ReleaseSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("release session: empty session ID")
	}
	if c == nil {
		return nil
	}

	c.sessionMu.Lock()
	resultIDs := c.sessionResultIDsBySession[sessionID]
	arbitraryCallKeys := c.sessionArbitraryCallKeysBySession[sessionID]
	delete(c.sessionResultIDsBySession, sessionID)
	delete(c.sessionArbitraryCallKeysBySession, sessionID)
	delete(c.sessionLazySpansBySession, sessionID)
	delete(c.sessionResourcesBySession, sessionID)
	delete(c.sessionHandlesBySession, sessionID)
	c.sessionMu.Unlock()

	var (
		rerr       error
		onReleases []OnReleaseFunc
	)
	c.egraphMu.Lock()
	queue := make([]*sharedResult, 0, len(resultIDs))
	for resultID := range resultIDs {
		shared := c.resultsByID[resultID]
		if shared == nil {
			continue
		}
		res := Result[Typed]{shared: shared}
		if c.traceEnabled() {
			c.traceSessionResultReleasing(ctx, sessionID, res, "release_session", 1, len(resultIDs))
		}
		var err error
		queue, err = c.decrementIncomingOwnershipLocked(ctx, shared, queue)
		rerr = errors.Join(rerr, err)
	}
	collectReleases, collectErr := c.collectUnownedResultsLocked(context.WithoutCancel(ctx), queue)
	onReleases = append(onReleases, collectReleases...)
	rerr = errors.Join(rerr, collectErr)
	c.egraphMu.Unlock()

	rerr = errors.Join(rerr, runOnReleaseFuncs(context.WithoutCancel(ctx), onReleases))
	for callKey := range arbitraryCallKeys {
		var onRelease OnReleaseFunc
		c.callsMu.Lock()
		res := c.completedArbitraryCalls[callKey]
		if res == nil {
			res = c.ongoingArbitraryCalls[callKey]
		}
		if res != nil {
			res.ownerSessionCount--
			if res.ownerSessionCount < 0 {
				res.ownerSessionCount = 0
			}
			if res.ownerSessionCount == 0 && res.waiters == 0 {
				if existing := c.ongoingArbitraryCalls[callKey]; existing == res {
					delete(c.ongoingArbitraryCalls, callKey)
				}
				if existing := c.completedArbitraryCalls[callKey]; existing == res {
					delete(c.completedArbitraryCalls, callKey)
				}
				onRelease = res.onRelease
			}
		}
		c.callsMu.Unlock()
		if onRelease != nil {
			rerr = errors.Join(rerr, onRelease(context.WithoutCancel(ctx)))
		}
	}
	return rerr
}

func (c *Cache) snapshotSessionResultIDs() map[sharedResultID]struct{} {
	if c == nil {
		return nil
	}
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	if len(c.sessionResultIDsBySession) == 0 {
		return nil
	}
	roots := make(map[sharedResultID]struct{})
	for _, resultIDs := range c.sessionResultIDsBySession {
		for resultID := range resultIDs {
			roots[resultID] = struct{}{}
		}
	}
	return roots
}

func (c *Cache) upsertPersistedEdgeLocked(ctx context.Context, res *sharedResult, expiresAtUnix int64, unpruneable bool) {
	if c == nil || res == nil || res.id == 0 {
		return
	}
	if c.persistedEdgesByResult == nil {
		c.persistedEdgesByResult = make(map[sharedResultID]persistedEdge)
	}
	edge, found := c.persistedEdgesByResult[res.id]
	if !found {
		createdAtUnixNano := res.loadPayloadState().createdAtUnixNano
		if createdAtUnixNano == 0 {
			createdAtUnixNano = time.Now().UnixNano()
		}
		edge = persistedEdge{
			resultID:          res.id,
			createdAtUnixNano: createdAtUnixNano,
		}
		c.incrementIncomingOwnershipLocked(ctx, res)
	}
	if unpruneable {
		edge.unpruneable = true
		edge.expiresAtUnix = 0
		res.expiresAtUnix = 0
	} else if !edge.unpruneable {
		edge.expiresAtUnix = mergeSharedResultExpiryUnix(edge.expiresAtUnix, expiresAtUnix)
	}
	c.persistedEdgesByResult[res.id] = edge
}

func (c *Cache) MakeResultUnpruneable(ctx context.Context, res AnyResult) error {
	if c == nil {
		return fmt.Errorf("make result unpruneable: nil cache")
	}
	if res == nil {
		return fmt.Errorf("make result unpruneable: nil result")
	}
	shared := res.cacheSharedResult()
	if shared == nil || shared.id == 0 {
		return fmt.Errorf("make result unpruneable: result is not cache-backed")
	}

	c.egraphMu.Lock()
	c.upsertPersistedEdgeLocked(ctx, shared, 0, true)
	c.egraphMu.Unlock()
	return nil
}

func (c *Cache) removePersistedEdge(ctx context.Context, resultID sharedResultID) (bool, error) {
	if c == nil || resultID == 0 {
		return false, nil
	}

	var (
		res        *sharedResult
		queue      []*sharedResult
		onReleases []OnReleaseFunc
		rerr       error
	)
	c.egraphMu.Lock()
	if _, found := c.persistedEdgesByResult[resultID]; !found {
		c.egraphMu.Unlock()
		return false, nil
	}
	delete(c.persistedEdgesByResult, resultID)
	res = c.resultsByID[resultID]
	if res != nil {
		var err error
		queue, err = c.decrementIncomingOwnershipLocked(ctx, res, queue)
		rerr = errors.Join(rerr, err)
	}
	collectReleases, collectErr := c.collectUnownedResultsLocked(ctx, queue)
	onReleases = append(onReleases, collectReleases...)
	rerr = errors.Join(rerr, collectErr)
	c.egraphMu.Unlock()

	return true, errors.Join(rerr, runOnReleaseFuncs(ctx, onReleases))
}

func (c *Cache) incrementIncomingOwnershipLocked(ctx context.Context, res *sharedResult) {
	if c == nil || res == nil {
		return
	}
	res.incomingOwnershipCount++
	c.traceRefAcquired(ctx, res, res.incomingOwnershipCount)
}

func (c *Cache) enqueueCollectibleResultLocked(queue []*sharedResult, res *sharedResult) []*sharedResult {
	if c == nil || res == nil || res.id == 0 {
		return queue
	}
	if c.resultsByID[res.id] != res {
		return queue
	}
	if res.incomingOwnershipCount != 0 {
		return queue
	}
	return append(queue, res)
}

func (c *Cache) decrementIncomingOwnershipLocked(ctx context.Context, res *sharedResult, queue []*sharedResult) ([]*sharedResult, error) {
	if c == nil || res == nil {
		return queue, nil
	}
	res.incomingOwnershipCount--
	c.traceRefReleased(ctx, res, res.incomingOwnershipCount)
	if res.incomingOwnershipCount < 0 {
		c.traceRefUnderflow(ctx, res, res.incomingOwnershipCount)
		return queue, fmt.Errorf("incoming ownership underflow for result %d", res.id)
	}
	return c.enqueueCollectibleResultLocked(queue, res), nil
}

func (c *Cache) collectUnownedResultsLocked(ctx context.Context, queue []*sharedResult) ([]OnReleaseFunc, error) {
	if c == nil {
		return nil, nil
	}

	var (
		rerr       error
		onReleases []OnReleaseFunc
	)

	for len(queue) > 0 {
		res := queue[len(queue)-1]
		queue = queue[:len(queue)-1]

		if c.resultsByID[res.id] != res {
			continue
		}
		if res.incomingOwnershipCount != 0 {
			continue
		}

		depIDs := make([]sharedResultID, 0, len(res.deps))
		for depID := range res.deps {
			depIDs = append(depIDs, depID)
		}

		if err := c.removeResultFromEgraphLocked(ctx, res); err != nil {
			rerr = errors.Join(rerr, err)
		} else if res.onRelease != nil {
			onReleases = append(onReleases, res.onRelease)
		}
		res.deps = nil

		for _, depID := range depIDs {
			depRes := c.resultsByID[depID]
			if depRes == nil {
				continue
			}
			c.traceDependencyRemoved(ctx, res.id, depID, "parent_collected")
			var err error
			queue, err = c.decrementIncomingOwnershipLocked(ctx, depRes, queue)
			rerr = errors.Join(rerr, err)
		}
	}

	return onReleases, rerr
}

func runOnReleaseFuncs(ctx context.Context, onReleases []OnReleaseFunc) error {
	var rerr error
	for _, onRelease := range onReleases {
		if onRelease == nil {
			continue
		}
		rerr = errors.Join(rerr, onRelease(ctx))
	}
	return rerr
}

func resultSnapshotLeaseID(resultID sharedResultID, role, slot string) string {
	if slot == "" {
		return fmt.Sprintf("dagql/result/%d/%s", resultID, url.PathEscape(role))
	}
	return fmt.Sprintf(
		"dagql/result/%d/%s/%s",
		resultID,
		url.PathEscape(role),
		url.PathEscape(slot),
	)
}

func joinOnRelease(a, b OnReleaseFunc) OnReleaseFunc {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	default:
		return func(ctx context.Context) error {
			return errors.Join(a(ctx), b(ctx))
		}
	}
}

type snapshotOwnerKey struct {
	Role string
	Slot string
}

func (c *Cache) authoritativeSnapshotLinksForResult(res *sharedResult) ([]PersistedSnapshotRefLink, bool) {
	if res == nil {
		return nil, false
	}

	state := res.loadPayloadState()
	if state.hasValue && state.self != nil {
		linker, ok := any(state.self).(PersistedSnapshotRefLinkProvider)
		if ok {
			links := linker.PersistedSnapshotRefLinks()
			if len(links) == 0 {
				return nil, true
			}
			cpy := make([]PersistedSnapshotRefLink, len(links))
			copy(cpy, links)
			return cpy, true
		}
	}

	res.payloadMu.RLock()
	defer res.payloadMu.RUnlock()
	if len(res.snapshotOwnerLinks) == 0 {
		return nil, false
	}
	links := make([]PersistedSnapshotRefLink, len(res.snapshotOwnerLinks))
	copy(links, res.snapshotOwnerLinks)
	return links, true
}

func (c *Cache) resultSnapshotLeaseCleanup(res *sharedResult) OnReleaseFunc {
	if c == nil || c.snapshotManager == nil || res == nil || res.id == 0 {
		return nil
	}

	return func(ctx context.Context) error {
		links, ok := c.authoritativeSnapshotLinksForResult(res)
		if !ok {
			return nil
		}

		seen := make(map[snapshotOwnerKey]struct{}, len(links))
		var rerr error
		for _, link := range links {
			key := snapshotOwnerKey{Role: link.Role, Slot: link.Slot}
			if _, alreadySeen := seen[key]; alreadySeen {
				continue
			}
			seen[key] = struct{}{}
			rerr = errors.Join(rerr, c.snapshotManager.RemoveLease(
				ctx,
				resultSnapshotLeaseID(res.id, link.Role, link.Slot),
			))
		}
		return rerr
	}
}

func (c *Cache) syncResultSnapshotLeases(ctx context.Context, res *sharedResult) error {
	if c == nil || c.snapshotManager == nil || res == nil || res.id == 0 {
		return nil
	}

	links, ok := c.authoritativeSnapshotLinksForResult(res)
	if !ok {
		return nil
	}

	res.payloadMu.RLock()
	oldLinks := append([]PersistedSnapshotRefLink(nil), res.snapshotOwnerLinks...)
	res.payloadMu.RUnlock()

	oldByKey := make(map[snapshotOwnerKey]PersistedSnapshotRefLink, len(oldLinks))
	newByKey := make(map[snapshotOwnerKey]PersistedSnapshotRefLink, len(links))

	for _, link := range oldLinks {
		oldByKey[snapshotOwnerKey{Role: link.Role, Slot: link.Slot}] = link
	}
	for _, link := range links {
		newByKey[snapshotOwnerKey{Role: link.Role, Slot: link.Slot}] = link
	}
	for key, oldLink := range oldByKey {
		newLink, ok := newByKey[key]
		if !ok || newLink.RefKey != oldLink.RefKey {
			if err := c.snapshotManager.RemoveLease(
				ctx,
				resultSnapshotLeaseID(res.id, key.Role, key.Slot),
			); err != nil {
				return err
			}
		}
	}

	for key, newLink := range newByKey {
		oldLink, ok := oldByKey[key]
		if !ok || oldLink.RefKey != newLink.RefKey {
			if err := c.snapshotManager.AttachLease(
				ctx,
				resultSnapshotLeaseID(res.id, key.Role, key.Slot),
				newLink.RefKey,
			); err != nil {
				return err
			}
		}
	}

	res.payloadMu.Lock()
	res.snapshotOwnerLinks = append([]PersistedSnapshotRefLink(nil), links...)
	res.payloadMu.Unlock()

	return nil
}

func (c *Cache) desiredImportedOwnerLeaseIDs(ctx context.Context) (map[string]struct{}, error) {
	if c == nil {
		return nil, nil
	}

	c.egraphMu.RLock()
	results := make([]*sharedResult, 0, len(c.resultsByID))
	for _, res := range c.resultsByID {
		if res != nil {
			results = append(results, res)
		}
	}
	c.egraphMu.RUnlock()

	desired := make(map[string]struct{})
	for _, res := range results {
		links, ok := c.authoritativeSnapshotLinksForResult(res)
		if !ok {
			continue
		}
		for _, link := range links {
			desired[resultSnapshotLeaseID(res.id, link.Role, link.Slot)] = struct{}{}
		}
	}

	return desired, nil
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

type Cache struct {
	// callsMu protects in-flight call bookkeeping and arbitrary in-memory call maps.
	callsMu sync.Mutex
	// sessionMu protects per-session tracked cache-backed results and arbitrary values.
	sessionMu sync.Mutex
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
	egraphTermsByTermDigest map[string]*set.TreeSet[egraphTermID]

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

	// explicit result<->term associations. These are distinct from output eq
	// class membership: multiple results can share an output eq class, but
	// cache lookup for a matched term should first prefer results that were
	// actually observed for that term before falling back to equivalent outputs.
	termResults map[egraphTermID]map[sharedResultID]egraphResultTermAssoc
	resultTerms map[sharedResultID]map[egraphTermID]struct{}

	// Reverse index from any known result-associated digest to materialized results.
	// This includes request recipe+extra digests and result recipe+extra digests.
	egraphResultsByDigest map[string]*set.TreeSet[sharedResultID]

	// Explicit retained-root edges for persisted results.
	persistedEdgesByResult map[sharedResultID]persistedEdge

	// per-term input provenance indicates whether each input slot was
	// result-backed or digest-only when the term was observed
	termInputProvenance map[egraphTermID][]egraphInputProvenanceKind

	// in-progress and completed opaque in-memory calls, keyed by call key
	ongoingArbitraryCalls   map[string]*sharedArbitraryResult
	completedArbitraryCalls map[string]*sharedArbitraryResult

	sessionResultIDsBySession         map[string]map[sharedResultID]struct{}
	sessionArbitraryCallKeysBySession map[string]map[string]struct{}
	sessionLazySpansBySession         map[string]map[sharedResultID]trace.SpanContext
	sessionResourcesBySession         map[string]map[SessionResourceHandle]*sessionResourceBindings
	sessionHandlesBySession           map[string]*set.TreeSet[SessionResourceHandle]

	sqlDB *sql.DB
	// persistent normalized cache store (disk persistence/import).
	pdb *persistdb.Queries

	traceBootID       string
	traceSeq          uint64
	tracePersistBatch uint64
	traceImportRuns   uint64

	snapshotManager bkcache.SnapshotManager

	closeOnce sync.Once
	closeErr  error
}

type callConcurrencyKeys struct {
	callKey        string
	concurrencyKey string
}

type OnReleaseFunc = func(context.Context) error

type sharedResultID uint64

type sessionResourceBindings struct {
	latestClientID string
	byClientID     map[string]any
}

const sharedResultSizeUnknown int64 = -1

func compareSessionResourceHandles(a, b SessionResourceHandle) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func compareSharedResults(a, b *sharedResult) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	case a.id < b.id:
		return -1
	case a.id > b.id:
		return 1
	default:
		return 0
	}
}

type cacheUsageSizer interface {
	// CacheUsageSize returns the concrete size of the cached payload when known.
	// ok=false means "size is currently unknown/not available".
	CacheUsageSize(context.Context, string) (sizeBytes int64, ok bool, err error)
}

type hasCacheUsageIdentity interface {
	// CacheUsageIdentities returns the stable identities for deduplicating
	// physical storage accounting across cache results that share snapshots.
	CacheUsageIdentities() []string
}

type cacheUsageMayChange interface {
	// CacheUsageMayChange reports whether usage size can change over time for the
	// same usage identity (for example mutable cache volume snapshots).
	CacheUsageMayChange() bool
}

// sharedResult holds cache-entry state and shared payload published to per-call Result values.
type sharedResult struct {
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
	hasValue  bool
	onRelease OnReleaseFunc
	// deps tracks exact materialized child-result dependencies used for
	// release/liveness propagation and persistence closure. This includes
	// explicit out-of-band deps and exact resultCall refs mirrored into deps
	// during materialization.
	deps map[sharedResultID]struct{}
	// sessionResourceHandle is set when this result is itself an attached
	// session-resource handle leaf. requiredSessionResources is the flattened
	// transitive set of handle requirements for cache-hit validation.
	sessionResourceHandle    SessionResourceHandle
	requiredSessionResources *set.TreeSet[SessionResourceHandle]
	// snapshotOwnerLinks are the current authoritative direct snapshot-owner
	// links for this result. Imported rows seed them during startup, and typed
	// self payload can replace them after decode/materialization. They are not
	// child-result deps.
	snapshotOwnerLinks []PersistedSnapshotRefLink

	// expiresAtUnix is the in-memory TTL deadline for cache-hit eligibility.
	// 0 means "never expires".
	expiresAtUnix int64
	// persistedEnvelope is populated for imported rows and decoded lazily on
	// first cache-hit use in a server-aware context.
	persistedEnvelope *PersistedResultEnvelope

	// Prune-accounting metadata. Sizes are unknown until explicitly measured.
	createdAtUnixNano        int64
	lastUsedAtUnixNano       int64
	cacheUsageSizeByIdentity map[string]int64
	description              string
	recordType               string

	// incomingOwnershipCount is the authoritative liveness count derived from
	// session edges, persisted edges, and result dependency edges.
	incomingOwnershipCount int64

	lazyMu           sync.Mutex
	lazyEval         LazyEvalFunc
	lazyEvalComplete bool
	lazyEvalWaitCh   chan struct{}
	lazyEvalCancel   context.CancelCauseFunc
	lazyEvalWaiters  int
	lazyEvalErr      error
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
	handoffHoldActive       bool
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
			self:       self,
			resultCall: resultCall,
			hasValue:   true,
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

func (c *Cache) normalizePendingResultCallRefs(ctx context.Context, frame *ResultCall) error {
	return c.normalizePendingResultCallRefsWithSeen(ctx, frame, map[*ResultCall]struct{}{})
}

func (c *Cache) canonicalEquivalentSharedResultLocked(sessionID string, res *sharedResult, nowUnix int64) *sharedResult {
	if res == nil || res.id == 0 {
		return nil
	}

	candidates := newSharedResultSet()
	for outputEqID := range c.outputEqClassesForResultLocked(res.id) {
		outputEqID = c.findEqClassLocked(outputEqID)
		if outputEqID == 0 {
			continue
		}
		for dig := range c.eqClassToDigests[outputEqID] {
			c.appendDigestResultsLocked(candidates, digest.Digest(dig), nowUnix)
		}
	}

	if candidates.Empty() {
		return res
	}
	if canonical := c.selectLookupCandidateForSessionLocked(sessionID, candidates); canonical != nil {
		return canonical
	}
	return res
}

func (c *Cache) normalizePendingResultCallRefsWithSeen(ctx context.Context, frame *ResultCall, seen map[*ResultCall]struct{}) error {
	if frame == nil {
		return nil
	}
	if _, ok := seen[frame]; ok {
		return fmt.Errorf("cycle while normalizing pending call refs")
	}
	seen[frame] = struct{}{}
	defer delete(seen, frame)

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

func (c *Cache) normalizePendingResultCallRefWithSeen(ctx context.Context, ref *ResultCallRef, seen map[*ResultCall]struct{}) error {
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
	if shared, _, _, err := c.sharedResultByResultID(ctx, "", resultID, sharedResultLookupExact); err == nil {
		ref.shared = shared
	}
	ref.Call = nil
	return nil
}

func (c *Cache) normalizePendingResultCallLiteralWithSeen(ctx context.Context, lit *ResultCallLiteral, seen map[*ResultCall]struct{}) error {
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

func (c *Cache) AttachResult(ctx context.Context, sessionID string, resolver TypeResolver, res AnyResult) (AnyResult, error) {
	if sessionID == "" {
		return nil, errors.New("attach result: empty session ID")
	}
	return c.attachResult(ctx, sessionID, resolver, res)
}

func (c *Cache) attachResult(ctx context.Context, sessionID string, resolver TypeResolver, res AnyResult) (AnyResult, error) {
	if sessionID == "" {
		return nil, errors.New("attach result: empty session ID")
	}
	if resolver == nil {
		return nil, errors.New("attach result: type resolver is nil")
	}
	if res == nil {
		return nil, nil
	}
	shared := res.cacheSharedResult()
	if shared == nil {
		return nil, fmt.Errorf("attach dependency result: missing shared result")
	}
	if shared.id != 0 {
		c.registerLazyEvaluation(shared, res)
		touchSharedResultLastUsed(shared, time.Now().UnixNano())
		c.traceAttachResultReusedCacheBacked(ctx, sessionID, shared)
		c.trackSessionResult(ctx, sessionID, res, true)
		return res, nil
	}
	frame := shared.loadResultCall()
	if frame == nil {
		return nil, fmt.Errorf("attach dependency result: missing result call frame")
	}
	req := &CallRequest{
		ResultCall: frame.clone(),
	}
	if err := c.normalizePendingResultCallRefs(ctx, req.ResultCall); err != nil {
		return nil, fmt.Errorf("attach dependency result: normalize pending result call refs: %w", err)
	}
	shared.storeResultCall(req.ResultCall)
	c.traceResultCallFrameUpdated(ctx, shared, "attach_result_normalized", frame, req.ResultCall)

	callDigest, err := req.deriveRecipeDigest(c)
	if err != nil {
		return nil, fmt.Errorf("attach dependency result: derive request digest: %w", err)
	}
	requestSelf, requestInputRefs, err := req.selfDigestAndInputRefs(c)
	if err != nil {
		return nil, fmt.Errorf("attach dependency result: derive request term digests: %w", err)
	}
	requestInputs := make([]digest.Digest, 0, len(requestInputRefs))
	for _, ref := range requestInputRefs {
		dig, err := ref.inputDigest(c)
		if err != nil {
			return nil, fmt.Errorf("attach dependency result: derive request term input digest: %w", err)
		}
		requestInputs = append(requestInputs, dig)
	}

	hitRes, hit, err := c.lookupCacheForRequest(ctx, sessionID, resolver, req, callDigest, requestSelf, requestInputs, requestInputRefs)
	if err != nil {
		return nil, fmt.Errorf("attach dependency result: %w", err)
	}
	if hit {
		c.registerLazyEvaluation(hitRes.cacheSharedResult(), hitRes)
		return hitRes, nil
	}

	oc := &ongoingCall{
		val: res,
	}
	if err := c.initCompletedResult(ctx, resolver, oc, req, sessionID); err != nil {
		return nil, fmt.Errorf("attach dependency result: %w", err)
	}
	if oc.res == nil {
		return nil, fmt.Errorf("attach dependency result: completed without initialized result")
	}
	c.trackSessionResult(ctx, sessionID, Result[Typed]{shared: oc.res}, false)
	if oc.handoffHoldActive {
		c.egraphMu.Lock()
		queue, decErr := c.decrementIncomingOwnershipLocked(ctx, oc.res, nil)
		collectReleases, collectErr := c.collectUnownedResultsLocked(context.WithoutCancel(ctx), queue)
		c.egraphMu.Unlock()
		oc.handoffHoldActive = false
		if relErr := errors.Join(decErr, collectErr, runOnReleaseFuncs(context.WithoutCancel(ctx), collectReleases)); relErr != nil {
			return nil, fmt.Errorf("attach dependency result: release publication hold: %w", relErr)
		}
	}
	touchSharedResultLastUsed(oc.res, time.Now().UnixNano())

	if !oc.res.loadPayloadState().hasValue {
		return Result[Typed]{shared: oc.res}, nil
	}

	attached, err := wrapSharedResultWithResolver(oc.res, false, resolver)
	if err != nil {
		return nil, fmt.Errorf("attach dependency result: %w", err)
	}
	attachedShared := attached.cacheSharedResult()
	if attachedShared == nil || attachedShared.id == 0 {
		return nil, fmt.Errorf("attach dependency result: attached result missing shared result ID")
	}
	return attached, nil
}

func (c *Cache) AddExplicitDependency(ctx context.Context, parent AnyResult, dep AnyResult, reason string) error {
	if parent == nil || dep == nil {
		return nil
	}

	parentShared := parent.cacheSharedResult()
	if parentShared == nil || parentShared.id == 0 {
		return fmt.Errorf("add explicit dependency: parent %T is not an attached result in this cache", parent)
	}
	depShared := dep.cacheSharedResult()
	if depShared == nil || depShared.id == 0 {
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

func (c *Cache) addExplicitDependencyLocked(
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

	parentRes.deps[depRes.id] = struct{}{}
	c.incrementIncomingOwnershipLocked(ctx, depRes)
	c.traceExplicitDepAdded(ctx, parentRes.id, depRes.id, reason)
	if err := c.recomputeRequiredSessionResourcesLocked(parentRes); err != nil {
		return err
	}

	return nil
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

func (r Result[T]) RecipeID(ctx context.Context) (*call.ID, error) {
	call := r.shared.loadResultCall()
	if r.shared == nil || call == nil {
		return nil, fmt.Errorf("result %T has no call frame", r.Self())
	}
	c, err := EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	return call.recipeID(c)
}

func (r Result[T]) RecipeDigest(ctx context.Context) (digest.Digest, error) {
	call := r.shared.loadResultCall()
	if r.shared == nil || call == nil {
		return "", fmt.Errorf("result %T has no call frame", r.Self())
	}
	c, err := EngineCache(ctx)
	if err != nil {
		return "", err
	}
	return call.deriveRecipeDigest(c)
}

func (r Result[T]) ContentPreferredDigest(ctx context.Context) (digest.Digest, error) {
	call := r.shared.loadResultCall()
	if r.shared == nil || call == nil {
		return "", fmt.Errorf("result %T has no call frame", r.Self())
	}
	c, err := EngineCache(ctx)
	if err != nil {
		return "", err
	}
	return call.deriveContentPreferredDigest(c)
}

func (r Result[T]) ResultCall() (*ResultCall, error) {
	call := r.shared.loadResultCall()
	if r.shared == nil || call == nil {
		return nil, fmt.Errorf("result %T has no call frame", r.Self())
	}
	return call.clone(), nil
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
		return anyRes, true
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

	childShared := detached.cacheSharedResult()
	if childShared != nil && childShared.id != 0 {
		srv := CurrentDagqlServer(ctx)
		if srv == nil {
			return nil, fmt.Errorf("load %dth value from %T: missing dagql server in context", nth, self)
		}
		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("load %dth value from %T: current client metadata: %w", nth, self, err)
		}
		if clientMetadata.SessionID == "" {
			return nil, fmt.Errorf("load %dth value from %T: empty session ID", nth, self)
		}
		cache, err := EngineCache(ctx)
		if err != nil {
			return nil, fmt.Errorf("load %dth value from %T: current dagql cache: %w", nth, self, err)
		}
		touchSharedResultLastUsed(childShared, time.Now().UnixNano())
		retResAny, err := wrapSharedResultWithResolver(childShared, true, srv)
		if err != nil {
			return nil, fmt.Errorf("load %dth value from %T: reconstruct result: %w", nth, self, err)
		}
		cache.trackSessionResult(ctx, clientMetadata.SessionID, retResAny, true)
		return retResAny, nil
	}

	srv := CurrentDagqlServer(ctx)
	if srv == nil {
		return nil, fmt.Errorf("load %dth value from %T: missing dagql server in context", nth, self)
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
	if shared := detached.cacheSharedResult(); shared != nil && shared.id == 0 {
		shared.storeResultCall(req.ResultCall.clone())
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("load %dth value from %T: current client metadata: %w", nth, self, err)
	}
	if clientMetadata.SessionID == "" {
		return nil, fmt.Errorf("load %dth value from %T: empty session ID", nth, self)
	}
	cache, err := EngineCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("load %dth value from %T: current dagql cache: %w", nth, self, err)
	}
	return cache.GetOrInitCall(ctx, clientMetadata.SessionID, srv, req, func(context.Context) (AnyResult, error) {
		return detached, nil
	})
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

func (r Result[T]) WithContentDigest(ctx context.Context, contentDigest digest.Digest) (Result[T], error) {
	if contentDigest == "" {
		return r, fmt.Errorf("set content digest on %T: empty digest", r.Self())
	}
	if r.shared == nil {
		return r, fmt.Errorf("set content digest on %T: missing shared result", r.Self())
	}
	if r.shared.id != 0 {
		cache, err := EngineCache(ctx)
		if err != nil {
			return r, fmt.Errorf("set content digest on %T: current dagql cache: %w", r.Self(), err)
		}
		if err := cache.TeachContentDigest(ctx, r, contentDigest); err != nil {
			return r, err
		}
		return r, nil
	}

	state := r.shared.loadPayloadState()
	frame := r.shared.loadResultCall()
	if frame == nil {
		return r, fmt.Errorf("set content digest on %T: missing call frame", r.Self())
	}
	var deps map[sharedResultID]struct{}
	if len(r.shared.deps) > 0 {
		deps = make(map[sharedResultID]struct{}, len(r.shared.deps))
		for depID := range r.shared.deps {
			deps[depID] = struct{}{}
		}
	}
	r.shared = &sharedResult{
		self:                  state.self,
		isObject:              state.isObject,
		resultCall:            frame.fork(),
		hasValue:              state.hasValue,
		deps:                  deps,
		sessionResourceHandle: r.shared.sessionResourceHandle,
		requiredSessionResources: func() *set.TreeSet[SessionResourceHandle] {
			if r.shared.requiredSessionResources == nil {
				return nil
			}
			return r.shared.requiredSessionResources.Copy()
		}(),
		persistedEnvelope:  state.persistedEnvelope,
		snapshotOwnerLinks: slices.Clone(r.shared.snapshotOwnerLinks),
		createdAtUnixNano:  state.createdAtUnixNano,
		lastUsedAtUnixNano: state.lastUsedAtUnixNano,
		cacheUsageSizeByIdentity: func() map[string]int64 {
			if len(r.shared.cacheUsageSizeByIdentity) == 0 {
				return nil
			}
			cp := make(map[string]int64, len(r.shared.cacheUsageSizeByIdentity))
			for id, sz := range r.shared.cacheUsageSizeByIdentity {
				cp[id] = sz
			}
			return cp
		}(),
		description: r.shared.description,
		recordType:  r.shared.recordType,
	}
	frame = r.shared.loadResultCall()
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
	return r, nil
}

func (r Result[T]) WithSessionResourceHandle(ctx context.Context, handle SessionResourceHandle) (Result[T], error) {
	if handle == "" {
		return r, fmt.Errorf("set session resource handle on %T: empty handle", r.Self())
	}
	if r.shared == nil {
		return r, fmt.Errorf("set session resource handle on %T: missing shared result", r.Self())
	}
	if r.shared.id != 0 {
		cache, err := EngineCache(ctx)
		if err != nil {
			return r, fmt.Errorf("set session resource handle on %T: current dagql cache: %w", r.Self(), err)
		}
		cache.egraphMu.Lock()
		defer cache.egraphMu.Unlock()

		cached := cache.resultsByID[r.shared.id]
		if cached == nil {
			return r, fmt.Errorf("set session resource handle on %T: result %d missing from cache", r.Self(), r.shared.id)
		}
		cached.sessionResourceHandle = handle
		if err := cache.recomputeRequiredSessionResourcesLocked(cached); err != nil {
			return r, err
		}
		return r, nil
	}

	state := r.shared.loadPayloadState()
	frame := r.shared.loadResultCall()
	var deps map[sharedResultID]struct{}
	if len(r.shared.deps) > 0 {
		deps = make(map[sharedResultID]struct{}, len(r.shared.deps))
		for depID := range r.shared.deps {
			deps[depID] = struct{}{}
		}
	}
	reqs := set.NewTreeSet(compareSessionResourceHandles)
	if r.shared.requiredSessionResources != nil {
		reqs = r.shared.requiredSessionResources.Copy()
	}
	reqs.Insert(handle)
	r.shared = &sharedResult{
		self:                     state.self,
		isObject:                 state.isObject,
		resultCall:               frame,
		hasValue:                 state.hasValue,
		deps:                     deps,
		sessionResourceHandle:    handle,
		requiredSessionResources: reqs,
		persistedEnvelope:        state.persistedEnvelope,
		snapshotOwnerLinks:       slices.Clone(r.shared.snapshotOwnerLinks),
		createdAtUnixNano:        state.createdAtUnixNano,
		lastUsedAtUnixNano:       state.lastUsedAtUnixNano,
		cacheUsageSizeByIdentity: func() map[string]int64 {
			if len(r.shared.cacheUsageSizeByIdentity) == 0 {
				return nil
			}
			cp := make(map[string]int64, len(r.shared.cacheUsageSizeByIdentity))
			for id, sz := range r.shared.cacheUsageSizeByIdentity {
				cp[id] = sz
			}
			return cp
		}(),
		description: r.shared.description,
		recordType:  r.shared.recordType,
	}
	if frame != nil {
		r.shared.storeResultCall(frame.fork())
	}
	return r, nil
}

// WithContentDigestAny is WithContentDigest but returns an AnyResult, required
// for polymorphic code paths like module function call plumbing.
func (r Result[T]) WithContentDigestAny(ctx context.Context, customDigest digest.Digest) (AnyResult, error) {
	return r.WithContentDigest(ctx, customDigest)
}

func (r Result[T]) WithSessionResourceHandleAny(ctx context.Context, handle SessionResourceHandle) (AnyResult, error) {
	return r.WithSessionResourceHandle(ctx, handle)
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
		return anyRes, true
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
	ctx = srvToContext(ctx, srv)
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
	cache, err := EngineCache(srvToContext(ctx, srv))
	if err != nil {
		return nil, fmt.Errorf("receiver: current dagql cache: %w", err)
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("receiver: current client metadata: %w", err)
	}
	if clientMetadata.SessionID == "" {
		return nil, fmt.Errorf("receiver: empty session ID")
	}
	res, err := cache.loadResultByResultID(ctx, clientMetadata.SessionID, srv, call.Receiver.ResultID)
	if err != nil {
		return nil, fmt.Errorf("receiver: load result %d: %w", call.Receiver.ResultID, err)
	}
	obj, ok := res.(AnyObjectResult)
	if !ok {
		return nil, fmt.Errorf("receiver: result %d is %T, not object result", call.Receiver.ResultID, res)
	}
	return obj, nil
}

func (r ObjectResult[T]) WithContentDigest(ctx context.Context, contentDigest digest.Digest) (ObjectResult[T], error) {
	res, err := r.Result.WithContentDigest(ctx, contentDigest)
	if err != nil {
		return ObjectResult[T]{}, err
	}
	return ObjectResult[T]{
		Result: res,
		class:  r.class,
	}, nil
}

func (r ObjectResult[T]) WithSessionResourceHandle(ctx context.Context, handle SessionResourceHandle) (ObjectResult[T], error) {
	res, err := r.Result.WithSessionResourceHandle(ctx, handle)
	if err != nil {
		return ObjectResult[T]{}, err
	}
	return ObjectResult[T]{
		Result: res,
		class:  r.class,
	}, nil
}

// WithContentDigestAny is WithContentDigest but returns an AnyResult, required
// for polymorphic code paths like module function call plumbing.
func (r ObjectResult[T]) WithContentDigestAny(ctx context.Context, customDigest digest.Digest) (AnyResult, error) {
	res, err := r.Result.WithContentDigest(ctx, customDigest)
	if err != nil {
		return nil, err
	}
	return ObjectResult[T]{
		Result: res,
		class:  r.class,
	}, nil
}

func (r ObjectResult[T]) WithSessionResourceHandleAny(ctx context.Context, handle SessionResourceHandle) (AnyResult, error) {
	res, err := r.Result.WithSessionResourceHandle(ctx, handle)
	if err != nil {
		return nil, err
	}
	return ObjectResult[T]{
		Result: res,
		class:  r.class,
	}, nil
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
	key string
}

type lazyEvalStackCtxKey struct{}
type lazyEvalStackNode struct {
	id     sharedResultID
	parent *lazyEvalStackNode
}

func lazyEvalFuncOfResult(val AnyResult) LazyEvalFunc {
	if val == nil {
		return nil
	}
	lazy, ok := UnwrapAs[HasLazyEvaluation](val)
	if !ok {
		return nil
	}
	return lazy.LazyEvalFunc()
}

func (c *Cache) registerLazyEvaluation(shared *sharedResult, val AnyResult) {
	if shared == nil || val == nil {
		return
	}
	lazyEval := lazyEvalFuncOfResult(val)
	if lazyEval == nil {
		return
	}

	shared.lazyMu.Lock()
	if shared.lazyEval == nil && !shared.lazyEvalComplete {
		shared.lazyEval = lazyEval
	}
	shared.lazyMu.Unlock()
}

func lazyEvalStackFromContext(ctx context.Context) *lazyEvalStackNode {
	stack, _ := ctx.Value(lazyEvalStackCtxKey{}).(*lazyEvalStackNode)
	return stack
}

func lazyEvalStackContains(stack *lazyEvalStackNode, id sharedResultID) bool {
	for cur := stack; cur != nil; cur = cur.parent {
		if cur.id == id {
			return true
		}
	}
	return false
}

type resumedCallbackSpan struct {
	trace.Span
	sc trace.SpanContext
	tp trace.TracerProvider
}

func (s resumedCallbackSpan) SpanContext() trace.SpanContext {
	return s.sc
}

func (s resumedCallbackSpan) TracerProvider() trace.TracerProvider {
	return s.tp
}

func (c *Cache) waitForLazyEvaluation(ctx context.Context, shared *sharedResult, waitCh chan struct{}) error {
	var waitErr error
	select {
	case <-waitCh:
		shared.lazyMu.Lock()
		waitErr = shared.lazyEvalErr
		shared.lazyEvalWaiters--
		if shared.lazyEvalWaiters == 0 && shared.lazyEvalWaitCh == waitCh {
			shared.lazyEvalWaitCh = nil
			shared.lazyEvalCancel = nil
			shared.lazyEvalErr = nil
		}
		shared.lazyMu.Unlock()
	case <-ctx.Done():
		waitErr = context.Cause(ctx)
		shared.lazyMu.Lock()
		shared.lazyEvalWaiters--
		lastWaiter := shared.lazyEvalWaiters == 0
		cancel := shared.lazyEvalCancel
		shared.lazyMu.Unlock()
		if lastWaiter && cancel != nil {
			cancel(waitErr)
		}
	}
	return waitErr
}

func (c *Cache) Evaluate(ctx context.Context, results ...AnyResult) error {
	switch len(results) {
	case 0:
		return nil
	case 1:
		return c.evaluateOne(ctx, results[0])
	}

	eg, egCtx := errgroup.WithContext(ctx)
	for _, res := range results {
		res := res
		eg.Go(func() error {
			return c.evaluateOne(egCtx, res)
		})
	}
	return eg.Wait()
}

func (c *Cache) evaluateOne(ctx context.Context, res AnyResult) error {
	if c == nil {
		return errors.New("evaluate: nil cache")
	}
	if res == nil {
		return nil
	}
	shared := res.cacheSharedResult()
	if shared == nil || shared.id == 0 {
		return fmt.Errorf("evaluate %T: detached result", res)
	}

	stack := lazyEvalStackFromContext(ctx)
	if stack != nil {
		if lazyEvalStackContains(stack, shared.id) {
			return fmt.Errorf("recursive lazy evaluation detected")
		}
	}

	stackCtx := context.WithValue(ctx, lazyEvalStackCtxKey{}, &lazyEvalStackNode{
		id:     shared.id,
		parent: stack,
	})

	shared.lazyMu.Lock()
	currentLazyEval := lazyEvalFuncOfResult(res)
	if currentLazyEval == nil {
		shared.lazyEval = nil
		shared.lazyEvalComplete = true
		shared.lazyMu.Unlock()
		return nil
	}
	if shared.lazyEvalComplete {
		shared.lazyMu.Unlock()
		return nil
	}
	shared.lazyEval = currentLazyEval
	if shared.lazyEval == nil {
		shared.lazyMu.Unlock()
		return nil
	}
	if shared.lazyEvalWaitCh != nil {
		waitCh := shared.lazyEvalWaitCh
		shared.lazyEvalWaiters++
		shared.lazyMu.Unlock()
		return c.waitForLazyEvaluation(stackCtx, shared, waitCh)
	}

	waitCh := make(chan struct{})
	evalCtx, cancel := context.WithCancelCause(context.WithoutCancel(stackCtx))
	lazyEval := shared.lazyEval
	resultCall := shared.loadResultCall()
	if resultCall != nil {
		evalCtx = ContextWithCall(evalCtx, resultCall)
	}
	shared.lazyEvalWaitCh = waitCh
	shared.lazyEvalCancel = cancel
	shared.lazyEvalWaiters = 1
	shared.lazyEvalErr = nil
	shared.lazyMu.Unlock()

	go func() {
		callbackCtx := evalCtx
		var resumeSpan trace.Span
		if clientMD, err := engine.ClientMetadataFromContext(evalCtx); err == nil && clientMD.SessionID != "" {
			if originalSpanCtx, ok := c.sessionLazySpanContext(clientMD.SessionID, shared.id); ok {
				spanName := "resume lazy evaluation"
				if resultCall != nil && resultCall.Field != "" {
					spanName = "resume " + resultCall.Field
				}
				var resumeCtx context.Context
				resumeCtx, resumeSpan = Tracer(evalCtx).Start(
					evalCtx,
					spanName,
					telemetry.Resume(trace.ContextWithSpanContext(evalCtx, originalSpanCtx)),
					telemetry.Passthrough(),
				)
				callbackCtx = trace.ContextWithSpan(resumeCtx, resumedCallbackSpan{
					Span: resumeSpan,
					sc:   originalSpanCtx,
					tp:   resumeSpan.TracerProvider(),
				})
			}
		}

		var err error
		if resumeSpan != nil {
			defer telemetry.EndWithCause(resumeSpan, &err)
		}
		err = lazyEval(callbackCtx)
		if err == nil {
			err = c.syncResultSnapshotLeases(callbackCtx, shared)
		}

		shared.lazyMu.Lock()
		shared.lazyEvalErr = err
		if err == nil {
			shared.lazyEvalComplete = true
			shared.lazyEval = nil
		}
		clearState := shared.lazyEvalWaiters == 0 && shared.lazyEvalWaitCh == waitCh
		if clearState {
			shared.lazyEvalWaitCh = nil
			shared.lazyEvalCancel = nil
			shared.lazyEvalErr = nil
		}
		shared.lazyMu.Unlock()

		close(waitCh)
	}()

	return c.waitForLazyEvaluation(stackCtx, shared, waitCh)
}

func (c *Cache) Close(ctx context.Context) error {
	c.closeOnce.Do(func() {
		slog.Info(
			"starting dagql cache close",
			"hasSQLDB", c.sqlDB != nil,
			"hasPersistDB", c.pdb != nil,
		)
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
			slog.Error("dagql cache close exiting with error", "err", c.closeErr)
			return
		}
		if c.pdb != nil {
			slog.Info("marking dagql cache clean shutdown")
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
		slog.Info("completed dagql cache close successfully")
	})
	return c.closeErr
}

func (c *Cache) Size() int {
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

func (c *Cache) EntryStats() CacheEntryStats {
	c.callsMu.Lock()
	defer c.callsMu.Unlock()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()

	var stats CacheEntryStats
	stats.OngoingCalls = len(c.ongoingCalls)
	stats.CompletedCalls = len(c.resultOutputEqClasses)
	stats.RetainedCalls = len(c.persistedEdgesByResult)
	stats.OngoingArbitrary = len(c.ongoingArbitraryCalls)
	stats.CompletedArbitrary = len(c.completedArbitraryCalls)

	return stats
}

func (c *Cache) UsageEntriesAll(ctx context.Context) []CacheUsageEntry {
	activeRoots := c.snapshotSessionResultIDs()
	c.measureAllResultSizes(ctx)
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	entries := c.usageEntriesLocked(activeRoots)
	return entries
}

func (c *Cache) usageEntriesLocked(activeRoots map[sharedResultID]struct{}) []CacheUsageEntry {
	entries := make([]CacheUsageEntry, 0, len(c.resultsByID))
	for resID, res := range c.resultsByID {
		if res == nil {
			continue
		}
		_, activelyUsed := activeRoots[resID]
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
		sizeBytes := int64(0)
		for _, sz := range res.cacheUsageSizeByIdentity {
			if sz > 0 {
				sizeBytes += sz
			}
		}
		entries = append(entries, CacheUsageEntry{
			ID:                        fmt.Sprintf("dagql.result.%d", resID),
			Description:               description,
			RecordType:                recordType,
			SizeBytes:                 sizeBytes,
			CreatedTimeUnixNano:       createdAt,
			MostRecentUseTimeUnixNano: lastUsedAt,
			ActivelyUsed:              activelyUsed,
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

type cacheUsageMeasurementInput struct {
	resultID         sharedResultID
	self             Typed
	identities       []string
	existingSizeByID map[string]int64
	sizeMayChange    bool
}

func (c *Cache) measureAllResultSizes(ctx context.Context) {
	inputs := c.collectUsageMeasurementInputs()
	if len(inputs) == 0 {
		return
	}
	sizes := buildCacheUsageMeasurements(ctx, inputs)
	c.publishUsageMeasurements(sizes)
}

func (c *Cache) collectUsageMeasurementInputs() []cacheUsageMeasurementInput {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	inputs := make([]cacheUsageMeasurementInput, 0, len(c.resultsByID))
	for resID, res := range c.resultsByID {
		if res == nil {
			continue
		}
		state := res.loadPayloadState()
		if !state.hasValue || state.self == nil {
			continue
		}
		identities := cacheUsageIdentitiesFromSelf(state.self)
		existing := make(map[string]int64, len(res.cacheUsageSizeByIdentity))
		for identity, sizeBytes := range res.cacheUsageSizeByIdentity {
			existing[identity] = sizeBytes
		}
		inputs = append(inputs, cacheUsageMeasurementInput{
			resultID:         resID,
			self:             state.self,
			identities:       identities,
			existingSizeByID: existing,
			sizeMayChange:    cacheUsageSizeMayChangeFromSelf(state.self),
		})
	}
	return inputs
}

func buildCacheUsageMeasurements(ctx context.Context, inputs []cacheUsageMeasurementInput) map[sharedResultID]map[string]int64 {
	if len(inputs) == 0 {
		return nil
	}

	inputByResultID := make(map[sharedResultID]cacheUsageMeasurementInput, len(inputs))
	ownerByIdentity := make(map[string]sharedResultID)
	for _, input := range inputs {
		inputByResultID[input.resultID] = input
		for _, identity := range input.identities {
			cur := ownerByIdentity[identity]
			if cur == 0 || input.resultID < cur {
				ownerByIdentity[identity] = input.resultID
			}
		}
	}

	identities := make([]string, 0, len(ownerByIdentity))
	for identity := range ownerByIdentity {
		identities = append(identities, identity)
	}
	slices.Sort(identities)

	sizeByIdentity := make(map[string]int64, len(ownerByIdentity))
	for _, identity := range identities {
		ownerID := ownerByIdentity[identity]
		input := inputByResultID[ownerID]
		if !input.sizeMayChange {
			if sizeBytes, ok := input.existingSizeByID[identity]; ok {
				sizeByIdentity[identity] = sizeBytes
				continue
			}
		}

		sizeBytes, ok, err := cacheUsageSizeBytesFromSelf(ctx, input.self, identity)
		if err != nil {
			slog.Warn("failed to determine cache usage size",
				"resultID", ownerID,
				"usageIdentity", identity,
				"err", err)
			continue
		}
		if !ok {
			continue
		}
		if sizeBytes < 0 {
			sizeBytes = 0
		}
		sizeByIdentity[identity] = sizeBytes
	}

	published := make(map[sharedResultID]map[string]int64, len(inputs))
	for _, input := range inputs {
		resultSizes := make(map[string]int64)
		for _, identity := range input.identities {
			if ownerByIdentity[identity] != input.resultID {
				continue
			}
			sizeBytes, ok := sizeByIdentity[identity]
			if !ok {
				continue
			}
			resultSizes[identity] = sizeBytes
		}
		if len(resultSizes) == 0 {
			continue
		}
		published[input.resultID] = resultSizes
	}

	return published
}

func (c *Cache) publishUsageMeasurements(sizes map[sharedResultID]map[string]int64) {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	for resultID, res := range c.resultsByID {
		if res == nil {
			continue
		}
		resultSizes, ok := sizes[resultID]
		if !ok {
			res.cacheUsageSizeByIdentity = nil
			continue
		}
		cp := make(map[string]int64, len(resultSizes))
		for identity, sizeBytes := range resultSizes {
			cp[identity] = sizeBytes
		}
		res.cacheUsageSizeByIdentity = cp
	}
}

//nolint:gocyclo // Core cache lookup/insert flow is intentionally centralized here.
func (c *Cache) GetOrInitCall(
	ctx context.Context,
	sessionID string,
	resolver TypeResolver,
	req *CallRequest,
	fn func(context.Context) (AnyResult, error),
) (AnyResult, error) {
	if sessionID == "" {
		return nil, errors.New("get or init call: empty session ID")
	}
	return c.getOrInitCall(ctx, sessionID, resolver, req, fn)
}

//nolint:gocyclo // Core cache lookup/insert flow is intentionally centralized here.
func (c *Cache) getOrInitCall(
	ctx context.Context,
	sessionID string,
	resolver TypeResolver,
	req *CallRequest,
	fn func(context.Context) (AnyResult, error),
) (AnyResult, error) {
	if sessionID == "" {
		return nil, errors.New("get or init call: empty session ID")
	}
	if resolver == nil {
		return nil, errors.New("get or init call: type resolver is nil")
	}
	if req == nil || req.ResultCall == nil {
		return nil, fmt.Errorf("call request is nil")
	}
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
		if shared := val.cacheSharedResult(); shared != nil && shared.id != 0 {
			touchSharedResultLastUsed(shared, time.Now().UnixNano())
			normalized, err := wrapSharedResultWithResolver(shared, false, resolver)
			if err != nil {
				return nil, fmt.Errorf("normalize do-not-cache attached result: %w", err)
			}
			c.trackSessionResult(ctx, sessionID, normalized, false)
			return normalized, nil
		}
		if lazyEval := lazyEvalFuncOfResult(val); lazyEval != nil {
			return nil, fmt.Errorf("do-not-cache result %T cannot be lazy", val.Unwrap())
		}

		detached := &sharedResult{
			self:       val.Unwrap(),
			resultCall: req.ResultCall.clone(),
			hasValue:   true,
		}
		if shared := val.cacheSharedResult(); shared != nil {
			detached.sessionResourceHandle = shared.sessionResourceHandle
			if shared.requiredSessionResources != nil {
				detached.requiredSessionResources = shared.requiredSessionResources.Copy()
			}
		}
		if onReleaser, ok := UnwrapAs[OnReleaser](val); ok {
			return nil, fmt.Errorf("do-not-cache result %T cannot implement OnReleaser", onReleaser)
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

	callDigest, err := req.deriveRecipeDigest(c)
	if err != nil {
		return nil, fmt.Errorf("derive request digest: %w", err)
	}
	requestSelf, requestInputRefs, err := req.selfDigestAndInputRefs(c)
	if err != nil {
		return nil, fmt.Errorf("derive request term digests: %w", err)
	}
	requestInputs := make([]digest.Digest, 0, len(requestInputRefs))
	for _, ref := range requestInputRefs {
		dig, err := ref.inputDigest(c)
		if err != nil {
			return nil, fmt.Errorf("derive request term input digest: %w", err)
		}
		requestInputs = append(requestInputs, dig)
	}
	callKey := callDigest.String()
	if ctx.Value(cacheContextKey{callKey}) != nil {
		return nil, ErrCacheRecursiveCall
	}
	callConcKeys := callConcurrencyKeys{
		callKey:        callKey,
		concurrencyKey: req.ConcurrencyKey,
	}

	hitRes, hit, err := c.lookupCacheForRequest(ctx, sessionID, resolver, req, callDigest, requestSelf, requestInputs, requestInputRefs)
	if err != nil {
		return nil, err
	}
	if hit {
		c.captureSessionLazySpanContext(ctx, sessionID, hitRes)
		return hitRes, nil
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
			return c.wait(ctx, sessionID, resolver, oc, req)
		}
	}

	// Intentional tradeoff: we do not perform a second e-graph lookup while
	// holding callsMu. A concurrent completion can index and drop its
	// singleflight entry between the first lookup and this point, which may lead
	// to occasional redundant execution instead of a late cache hit. We accept
	// that waste to avoid paying an extra lookup on this miss path.

	// make a new call with ctx that's only canceled when all caller contexts are canceled
	callCtx := context.WithValue(ctx, cacheContextKey{callKey}, struct{}{})
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
	return c.wait(ctx, sessionID, resolver, oc, req)
}

func (c *Cache) lookupCallRequest(
	ctx context.Context,
	sessionID string,
	resolver TypeResolver,
	req *CallRequest,
) (AnyResult, bool, error) {
	if sessionID == "" {
		return nil, false, errors.New("lookup call request: empty session ID")
	}
	if resolver == nil {
		return nil, false, errors.New("lookup call request: type resolver is nil")
	}
	if req == nil || req.ResultCall == nil {
		return nil, false, fmt.Errorf("call request is nil")
	}

	callDigest, err := req.deriveRecipeDigest(c)
	if err != nil {
		return nil, false, fmt.Errorf("derive request digest: %w", err)
	}
	requestSelf, requestInputRefs, err := req.selfDigestAndInputRefs(c)
	if err != nil {
		return nil, false, fmt.Errorf("derive request term digests: %w", err)
	}
	requestInputs := make([]digest.Digest, 0, len(requestInputRefs))
	for _, ref := range requestInputRefs {
		dig, err := ref.inputDigest(c)
		if err != nil {
			return nil, false, fmt.Errorf("derive request term input digest: %w", err)
		}
		requestInputs = append(requestInputs, dig)
	}

	hitRes, hit, err := c.lookupCacheForRequest(ctx, sessionID, resolver, req, callDigest, requestSelf, requestInputs, requestInputRefs)
	if err != nil {
		return nil, false, err
	}
	if !hit {
		return nil, false, nil
	}
	return hitRes, true, nil
}

func (c *Cache) lookupCacheForDigests(
	ctx context.Context,
	sessionID string,
	resolver TypeResolver,
	recipeDigest digest.Digest,
	extraDigests []call.ExtraDigest,
) (AnyResult, bool, error) {
	if sessionID == "" {
		return nil, false, errors.New("lookup cache for digests: empty session ID")
	}
	if resolver == nil {
		return nil, false, errors.New("lookup cache for digests: type resolver is nil")
	}
	if recipeDigest == "" {
		return nil, false, nil
	}

	c.egraphMu.Lock()
	now := time.Now()
	nowUnix := now.Unix()
	match := c.lookupMatchForDigestsLocked(recipeDigest, extraDigests, nowUnix)
	c.traceLookupAttempt(ctx, recipeDigest.String(), "", nil, false)
	hitRes := c.selectLookupCandidateForSessionLocked(sessionID, match.candidates)
	if hitRes == nil {
		c.traceLookupMissNoMatch(ctx, recipeDigest.String(), false, -1, "", 0)
		c.egraphMu.Unlock()
		return nil, false, nil
	}

	hitRes.expiresAtUnix = mergeSharedResultExpiryUnix(
		hitRes.expiresAtUnix,
		candidateSharedResultExpiryUnix(nowUnix, 0),
	)
	touchSharedResultLastUsed(hitRes, now.UnixNano())
	retRes := Result[Typed]{
		shared:   hitRes,
		hitCache: true,
	}
	c.traceLookupHit(ctx, recipeDigest.String(), hitRes, match.termDigest)
	hitShared := retRes.cacheSharedResult()
	if hitShared == nil || hitShared.id == 0 {
		c.egraphMu.Unlock()
		return nil, false, fmt.Errorf("lookup cache for digests: hit missing shared result ID")
	}

	trackedCount := 0
	alreadyTracked := false
	c.sessionMu.Lock()
	if c.sessionResultIDsBySession == nil {
		c.sessionResultIDsBySession = make(map[string]map[sharedResultID]struct{})
	}
	if c.sessionResultIDsBySession[sessionID] == nil {
		c.sessionResultIDsBySession[sessionID] = make(map[sharedResultID]struct{})
	}
	if _, found := c.sessionResultIDsBySession[sessionID][hitShared.id]; found {
		alreadyTracked = true
	} else {
		c.sessionResultIDsBySession[sessionID][hitShared.id] = struct{}{}
		c.incrementIncomingOwnershipLocked(ctx, hitShared)
	}
	trackedCount = len(c.sessionResultIDsBySession[sessionID])
	c.sessionMu.Unlock()
	c.egraphMu.Unlock()

	loadedHit, err := c.ensurePersistedHitValueLoaded(ctx, resolver, retRes)
	if err != nil {
		c.egraphMu.Lock()
		c.sessionMu.Lock()
		if resultIDs := c.sessionResultIDsBySession[sessionID]; resultIDs != nil {
			delete(resultIDs, hitShared.id)
			if len(resultIDs) == 0 {
				delete(c.sessionResultIDsBySession, sessionID)
			}
		}
		c.sessionMu.Unlock()
		queue := []*sharedResult(nil)
		var decErr error
		if !alreadyTracked {
			queue, decErr = c.decrementIncomingOwnershipLocked(ctx, hitShared, nil)
		}
		collectReleases, collectErr := c.collectUnownedResultsLocked(context.WithoutCancel(ctx), queue)
		c.egraphMu.Unlock()
		return nil, false, errors.Join(err, decErr, collectErr, runOnReleaseFuncs(context.WithoutCancel(ctx), collectReleases))
	}
	if c.traceEnabled() {
		c.traceSessionResultTracked(ctx, sessionID, loadedHit, true, trackedCount)
	}
	return loadedHit, true, nil
}

func (c *Cache) wait(
	ctx context.Context,
	sessionID string,
	resolver TypeResolver,
	oc *ongoingCall,
	req *CallRequest,
) (AnyResult, error) {
	var (
		completionErr error
		canceledErr   error
		completed     bool
	)

	select {
	case <-oc.waitCh:
		completed = true
	case <-ctx.Done():
		canceledErr = context.Cause(ctx)
	}

	if completed {
		completionErr = oc.err
	}

	if !completed {
		c.callsMu.Lock()
		oc.waiters--
		lastWaiter := oc.waiters == 0
		if lastWaiter {
			delete(c.ongoingCalls, oc.callConcurrencyKeys)
			oc.cancel(canceledErr)
		}
		c.callsMu.Unlock()
		return nil, canceledErr
	}

	if completionErr != nil {
		c.callsMu.Lock()
		oc.waiters--
		lastWaiter := oc.waiters == 0
		if lastWaiter {
			delete(c.ongoingCalls, oc.callConcurrencyKeys)
			oc.cancel(completionErr)
		}
		c.callsMu.Unlock()
		return nil, completionErr
	}

	oc.initCompletedResultOnce.Do(func() {
		oc.initCompletedResultErr = c.initCompletedResult(context.WithoutCancel(ctx), resolver, oc, req, sessionID)
		c.callsMu.Lock()
		delete(c.ongoingCalls, oc.callConcurrencyKeys)
		c.callsMu.Unlock()
	})
	if oc.initCompletedResultErr != nil {
		c.callsMu.Lock()
		oc.waiters--
		lastWaiter := oc.waiters == 0
		c.callsMu.Unlock()
		if lastWaiter && oc.handoffHoldActive {
			c.egraphMu.Lock()
			queue, decErr := c.decrementIncomingOwnershipLocked(ctx, oc.res, nil)
			collectReleases, collectErr := c.collectUnownedResultsLocked(context.WithoutCancel(ctx), queue)
			c.egraphMu.Unlock()
			oc.handoffHoldActive = false
			if relErr := errors.Join(decErr, collectErr, runOnReleaseFuncs(context.WithoutCancel(ctx), collectReleases)); relErr != nil {
				return nil, relErr
			}
		}
		return nil, oc.initCompletedResultErr
	}
	if oc.res == nil {
		c.callsMu.Lock()
		oc.waiters--
		lastWaiter := oc.waiters == 0
		c.callsMu.Unlock()
		if lastWaiter && oc.handoffHoldActive {
			c.egraphMu.Lock()
			queue, decErr := c.decrementIncomingOwnershipLocked(ctx, oc.res, nil)
			collectReleases, collectErr := c.collectUnownedResultsLocked(context.WithoutCancel(ctx), queue)
			c.egraphMu.Unlock()
			oc.handoffHoldActive = false
			if relErr := errors.Join(decErr, collectErr, runOnReleaseFuncs(context.WithoutCancel(ctx), collectReleases)); relErr != nil {
				return nil, relErr
			}
		}
		return nil, fmt.Errorf("cache wait completed without initialized result")
	}

	touchSharedResultLastUsed(oc.res, time.Now().UnixNano())

	retRes := Result[Typed]{
		shared:   oc.res,
		hitCache: false,
	}
	c.trackSessionResult(ctx, sessionID, retRes, false)
	c.captureSessionLazySpanContext(ctx, sessionID, retRes)
	c.callsMu.Lock()
	oc.waiters--
	lastWaiter := oc.waiters == 0
	c.callsMu.Unlock()
	if lastWaiter && oc.handoffHoldActive {
		c.egraphMu.Lock()
		queue, decErr := c.decrementIncomingOwnershipLocked(ctx, oc.res, nil)
		collectReleases, collectErr := c.collectUnownedResultsLocked(context.WithoutCancel(ctx), queue)
		c.egraphMu.Unlock()
		oc.handoffHoldActive = false
		if relErr := errors.Join(decErr, collectErr, runOnReleaseFuncs(context.WithoutCancel(ctx), collectReleases)); relErr != nil {
			return nil, relErr
		}
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

func (c *Cache) initCompletedResult(ctx context.Context, resolver TypeResolver, oc *ongoingCall, req *CallRequest, sessionID string) error {
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

	// Materialize shared result for this completed call.
	oc.res = &sharedResult{}
	if oc.val != nil {
		if existingRes := oc.val.cacheSharedResult(); existingRes != nil && existingRes.id != 0 {
			oc.res = existingRes
			resWasCacheBacked = true
		} else {
			oc.res.self = oc.val.Unwrap()
			if shared := oc.val.cacheSharedResult(); shared != nil {
				if frame := shared.loadResultCall(); frame != nil {
					oc.res.storeResultCall(frame.clone())
					c.traceResultCallFrameUpdated(ctx, oc.res, "init_completed_result_existing_value_frame", nil, oc.res.loadResultCall())
				}
				oc.res.sessionResourceHandle = shared.sessionResourceHandle
				if shared.requiredSessionResources != nil {
					oc.res.requiredSessionResources = shared.requiredSessionResources.Copy()
				}
			}
			if oc.res.loadResultCall() == nil {
				oc.res.storeResultCall(req.ResultCall.clone())
				c.traceResultCallFrameUpdated(ctx, oc.res, "init_completed_result_request_frame", nil, oc.res.loadResultCall())
			}
			oc.res.hasValue = true

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
	if !resWasCacheBacked {
		oc.res.onRelease = joinOnRelease(c.resultSnapshotLeaseCleanup(oc.res), oc.res.onRelease)
	}
	requestForIndex := req
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
		if reqDig, err := requestForIndex.deriveRecipeDigest(c); err == nil {
			oc.res.description = reqDig.String()
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
			selfDigest, inputRefs, deriveErr := resultCall.selfDigestAndInputRefs(c)
			if deriveErr != nil {
				return fmt.Errorf("derive result term digests: %w", deriveErr)
			}
			inputDigests := make([]digest.Digest, 0, len(inputRefs))
			for _, ref := range inputRefs {
				dig, err := ref.inputDigest(c)
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

	requestDigest, err := requestForIndex.deriveRecipeDigest(c)
	if err != nil {
		return fmt.Errorf("derive request digest: %w", err)
	}
	requestSelf, requestInputRefs, err := requestForIndex.selfDigestAndInputRefs(c)
	if err != nil {
		return fmt.Errorf("derive request term digests: %w", err)
	}
	requestInputs := make([]digest.Digest, 0, len(requestInputRefs))
	for _, ref := range requestInputRefs {
		dig, err := ref.inputDigest(c)
		if err != nil {
			return fmt.Errorf("derive request term input digest: %w", err)
		}
		requestInputs = append(requestInputs, dig)
	}
	var responseDigest digest.Digest
	if resultCall := oc.res.loadResultCall(); resultCall != nil {
		responseDigest, err = resultCall.deriveRecipeDigest(c)
		if err != nil {
			return fmt.Errorf("derive result digest: %w", err)
		}
	}
	type resultCallDep struct {
		resultID sharedResultID
		path     string
	}
	var resultCallDeps []resultCallDep
	if !resWasCacheBacked {
		if resultCall := oc.res.loadResultCall(); resultCall != nil {
			seenResults := map[sharedResultID]struct{}{}
			seenCalls := map[*ResultCall]struct{}{}

			var joinPath func(string, string) string
			var walkFrame func(string, *ResultCall) error
			var walkRef func(string, *ResultCallRef) error
			var walkLiteral func(string, *ResultCallLiteral) error

			joinPath = func(prefix string, segment string) string {
				switch {
				case prefix == "":
					return segment
				case segment == "":
					return prefix
				default:
					return prefix + "." + segment
				}
			}

			walkRef = func(path string, ref *ResultCallRef) error {
				if ref == nil {
					return nil
				}
				if ref.Call != nil {
					return walkFrame(path, ref.Call)
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
				resultCallDeps = append(resultCallDeps, resultCallDep{
					resultID: resultID,
					path:     path,
				})
				return nil
			}

			walkLiteral = func(path string, lit *ResultCallLiteral) error {
				if lit == nil {
					return nil
				}
				switch lit.Kind {
				case ResultCallLiteralKindResultRef:
					return walkRef(path, lit.ResultRef)
				case ResultCallLiteralKindList:
					for i, item := range lit.ListItems {
						if err := walkLiteral(fmt.Sprintf("%s[%d]", path, i), item); err != nil {
							return err
						}
					}
				case ResultCallLiteralKindObject:
					for _, field := range lit.ObjectFields {
						if field == nil {
							continue
						}
						if err := walkLiteral(joinPath(path, field.Name), field.Value); err != nil {
							return err
						}
					}
				}
				return nil
			}

			walkFrame = func(path string, frame *ResultCall) error {
				if frame == nil {
					return nil
				}
				if _, seen := seenCalls[frame]; seen {
					return nil
				}
				seenCalls[frame] = struct{}{}

				if err := walkRef(joinPath(path, "receiver"), frame.Receiver); err != nil {
					return fmt.Errorf("receiver: %w", err)
				}
				if frame.Module != nil {
					if err := walkRef(joinPath(path, "module"), frame.Module.ResultRef); err != nil {
						return fmt.Errorf("module: %w", err)
					}
				}
				for _, arg := range frame.Args {
					if arg == nil {
						continue
					}
					if err := walkLiteral(joinPath(path, "arg:"+arg.Name), arg.Value); err != nil {
						return fmt.Errorf("arg %q: %w", arg.Name, err)
					}
				}
				for _, input := range frame.ImplicitInputs {
					if input == nil {
						continue
					}
					if err := walkLiteral(joinPath(path, "implicit_input:"+input.Name), input.Value); err != nil {
						return fmt.Errorf("implicit input %q: %w", input.Name, err)
					}
				}
				return nil
			}

			if err := walkFrame("", resultCall); err != nil {
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
	for _, dep := range resultCallDeps {
		depID := dep.resultID
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
		oc.res.deps[depID] = struct{}{}
		c.incrementIncomingOwnershipLocked(ctx, depRes)
		c.traceResultCallDepAdded(ctx, oc.res.id, depID, dep.path)
	}
	if err := c.recomputeRequiredSessionResourcesLocked(oc.res); err != nil {
		c.egraphMu.Unlock()
		return err
	}
	if oc.isPersistable {
		c.upsertPersistedEdgeLocked(ctx, oc.res, candidateSharedResultExpiryUnix(now.Unix(), oc.ttlSeconds), false)
	}
	c.incrementIncomingOwnershipLocked(ctx, oc.res)
	oc.handoffHoldActive = true
	c.egraphMu.Unlock()

	if err := c.attachDependencyResults(ctx, sessionID, resolver, oc.res, oc.val); err != nil {
		c.egraphMu.Lock()
		queue, decErr := c.decrementIncomingOwnershipLocked(ctx, oc.res, nil)
		collectReleases, collectErr := c.collectUnownedResultsLocked(context.WithoutCancel(ctx), queue)
		c.egraphMu.Unlock()
		oc.handoffHoldActive = false
		return errors.Join(err, decErr, collectErr, runOnReleaseFuncs(context.WithoutCancel(ctx), collectReleases))
	}
	if err := c.syncResultSnapshotLeases(ctx, oc.res); err != nil {
		c.egraphMu.Lock()
		queue, decErr := c.decrementIncomingOwnershipLocked(ctx, oc.res, nil)
		collectReleases, collectErr := c.collectUnownedResultsLocked(context.WithoutCancel(ctx), queue)
		c.egraphMu.Unlock()
		oc.handoffHoldActive = false
		return errors.Join(err, decErr, collectErr, runOnReleaseFuncs(context.WithoutCancel(ctx), collectReleases))
	}
	c.registerLazyEvaluation(oc.res, oc.val)

	return nil
}

func (c *Cache) attachDependencyResults(ctx context.Context, sessionID string, resolver TypeResolver, parent *sharedResult, val AnyResult) error {
	if parent == nil || val == nil {
		return nil
	}
	withDeps, ok := UnwrapAs[HasDependencyResults](val)
	if !ok {
		return nil
	}
	self := Result[Typed]{shared: parent}
	var attachedSelf AnyResult = self
	parentState := parent.loadPayloadState()
	if parentState.hasValue && parentState.isObject {
		objSelf, err := wrapSharedResultWithResolver(parent, false, resolver)
		if err != nil {
			return fmt.Errorf("attach dependency results: reconstruct attached self: %w", err)
		}
		attachedSelf = objSelf
	}
	deps, err := withDeps.AttachDependencyResults(ctx, attachedSelf, func(child AnyResult) (AnyResult, error) {
		attached, err := c.attachResult(ctx, sessionID, resolver, child)
		if err != nil {
			return nil, err
		}
		return attached, nil
	})
	if err != nil {
		return err
	}
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
			return fmt.Errorf("attach dependency result %T: unexpected detached result", dep)
		}
		if attachedDepRes.id == parent.id {
			continue
		}
		if _, ok := seen[attachedDepRes.id]; ok {
			continue
		}
		seen[attachedDepRes.id] = struct{}{}
		if err := c.AddExplicitDependency(ctx, attachedSelf, dep, "attached_dependency_result"); err != nil {
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

func cacheUsageSizeBytesFromSelf(ctx context.Context, self Typed, identity string) (int64, bool, error) {
	if self == nil {
		return 0, false, nil
	}
	sizer, ok := any(self).(cacheUsageSizer)
	if !ok {
		return 0, false, nil
	}
	return sizer.CacheUsageSize(ctx, identity)
}

func cacheUsageSizeBytes(ctx context.Context, res *sharedResult, identity string) (int64, bool, error) {
	state := res.loadPayloadState()
	if res == nil || !state.hasValue || state.self == nil {
		return 0, false, nil
	}
	return cacheUsageSizeBytesFromSelf(ctx, state.self, identity)
}

func cacheUsageIdentitiesFromSelf(self Typed) []string {
	if self == nil {
		return nil
	}
	identityer, ok := any(self).(hasCacheUsageIdentity)
	if !ok {
		return nil
	}
	ids := append([]string(nil), identityer.CacheUsageIdentities()...)
	slices.Sort(ids)
	return slices.Compact(ids)
}

func cacheUsageIdentities(res *sharedResult) []string {
	state := res.loadPayloadState()
	if res == nil || !state.hasValue || state.self == nil {
		return nil
	}
	return cacheUsageIdentitiesFromSelf(state.self)
}

func cacheUsageSizeMayChangeFromSelf(self Typed) bool {
	if self == nil {
		return false
	}
	mutableSizer, ok := any(self).(cacheUsageMayChange)
	if !ok {
		return false
	}
	return mutableSizer.CacheUsageMayChange()
}

func cacheUsageSizeMayChange(res *sharedResult) bool {
	state := res.loadPayloadState()
	if res == nil || !state.hasValue || state.self == nil {
		return false
	}
	return cacheUsageSizeMayChangeFromSelf(state.self)
}
