package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
	"gotest.tools/v3/assert"
)

const (
	egraphBenchmarkSetupLimit     = 30 * time.Second
	egraphBenchmarkOperationLimit = 20 * time.Second
	egraphBenchmarkMemoryLimit    = uint64(4 << 30)
)

type egraphBenchmarkPersistence string

const (
	egraphBenchmarkTransient      egraphBenchmarkPersistence = "fresh"
	egraphBenchmarkPersistedFresh egraphBenchmarkPersistence = "persisted-fresh"
	egraphBenchmarkImported       egraphBenchmarkPersistence = "imported"
)

type egraphBenchmarkFixture struct {
	cache              *Cache
	ctx                context.Context
	server             *Server
	dbPath             string
	setupSession       string
	allResultIDs       []sharedResultID
	results            []AnyResult
	outputResultIDs    []sharedResultID
	exactRequests      []*ResultCall
	structuralRequest  *ResultCall
	sharedOutputDigest digest.Digest
	aliases            []AnyResult
	aliasResultIDs     []sharedResultID
	popularInputID     sharedResultID
}

func (f *egraphBenchmarkFixture) close(tb testing.TB) {
	tb.Helper()
	if f == nil || f.cache == nil {
		return
	}
	if err := f.cache.CloseDiscardingPersistence(); err != nil {
		tb.Errorf("close benchmark cache: %v", err)
	}
	f.cache = nil
}

func egraphBenchmarkContext(tb testing.TB, sessionID string) context.Context {
	tb.Helper()
	ctx := slog.WithLogger(tb.Context(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "dagql-egraph-benchmark",
		SessionID: sessionID,
	})
}

func egraphBenchmarkSessionContext(ctx context.Context, sessionID string) context.Context {
	return engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "dagql-egraph-benchmark",
		SessionID: sessionID,
	})
}

func egraphBenchmarkNewCache(tb testing.TB, sessionID, dbPath string, srv *Server) (*Cache, context.Context) {
	tb.Helper()
	ctx := egraphBenchmarkContext(tb, sessionID)
	c, err := NewCache(ctx, dbPath, nil, nil)
	if err != nil {
		tb.Fatalf("new benchmark cache: %v", err)
	}
	ctx = ContextWithCache(ctx, c)
	if srv != nil {
		ctx = srvToContext(ctx, srv)
	}
	return c, ctx
}

func (f *egraphBenchmarkFixture) reopenImported(tb testing.TB) {
	tb.Helper()
	if f.dbPath == "" {
		tb.Fatal("reopen imported fixture without a database")
	}
	if err := f.cache.Close(tb.Context()); err != nil {
		tb.Fatalf("persist benchmark fixture: %v", err)
	}
	f.cache = nil
	f.cache, f.ctx = egraphBenchmarkNewCache(tb, f.setupSession, f.dbPath, f.server)
}

type egraphBenchmarkGuard struct {
	b       *testing.B
	started time.Time
}

func newEgraphBenchmarkGuard(b *testing.B) *egraphBenchmarkGuard {
	return &egraphBenchmarkGuard{b: b, started: time.Now()}
}

func (g *egraphBenchmarkGuard) checkpoint(index int) {
	if index&1023 != 0 {
		return
	}
	if elapsed := time.Since(g.started); elapsed > egraphBenchmarkSetupLimit {
		g.b.Skipf("EGRAPH_BENCH_STOP setup exceeded %s after item %d", egraphBenchmarkSetupLimit, index)
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	if mem.HeapAlloc > egraphBenchmarkMemoryLimit || mem.Sys > egraphBenchmarkMemoryLimit {
		g.b.Skipf("EGRAPH_BENCH_STOP setup memory exceeded %d bytes after item %d: HeapAlloc=%d Sys=%d",
			egraphBenchmarkMemoryLimit, index, mem.HeapAlloc, mem.Sys)
	}
}

func egraphBenchmarkFinishSetup(b *testing.B, guard *egraphBenchmarkGuard) {
	b.Helper()
	elapsed := time.Since(guard.started)
	if elapsed > egraphBenchmarkSetupLimit {
		b.Skipf("EGRAPH_BENCH_STOP setup exceeded %s", egraphBenchmarkSetupLimit)
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	if mem.HeapAlloc > egraphBenchmarkMemoryLimit || mem.Sys > egraphBenchmarkMemoryLimit {
		b.Skipf("EGRAPH_BENCH_STOP setup memory exceeded %d bytes: HeapAlloc=%d Sys=%d",
			egraphBenchmarkMemoryLimit, mem.HeapAlloc, mem.Sys)
	}
	b.StopTimer()
	b.ResetTimer()
	b.ReportMetric(float64(elapsed.Nanoseconds()), "setup-ns")
}

func egraphBenchmarkResult(tb testing.TB, c *Cache, ctx context.Context, sessionID string, requestFrame, responseFrame *ResultCall, value int, persist bool) AnyResult {
	tb.Helper()
	res, err := c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{
		ResultCall:    requestFrame,
		IsPersistable: persist,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(responseFrame, value), nil
	})
	if err != nil {
		tb.Fatalf("publish benchmark result %d: %v", value, err)
	}
	return res
}

func egraphBenchmarkAppendResult(f *egraphBenchmarkFixture, res AnyResult) {
	shared := res.cacheSharedResult()
	if shared == nil || shared.id == 0 {
		panic("benchmark result is not attached")
	}
	f.results = append(f.results, res)
	f.allResultIDs = append(f.allResultIDs, shared.id)
}

func egraphBenchmarkPersistenceEnabled(mode egraphBenchmarkPersistence) bool {
	return mode != egraphBenchmarkTransient
}

func egraphBenchmarkIndependentFixture(tb testing.TB, n int, mode egraphBenchmarkPersistence, guard *egraphBenchmarkGuard) *egraphBenchmarkFixture {
	tb.Helper()
	f := &egraphBenchmarkFixture{setupSession: "independent-setup"}
	if mode == egraphBenchmarkImported {
		f.dbPath = filepath.Join(tb.TempDir(), "cache.db")
	}
	f.cache, f.ctx = egraphBenchmarkNewCache(tb, f.setupSession, f.dbPath, nil)
	persist := egraphBenchmarkPersistenceEnabled(mode)
	for i := range n {
		if guard != nil {
			guard.checkpoint(i)
		}
		frame := cacheTestIntCall("egraph-independent-" + strconv.Itoa(i))
		res := egraphBenchmarkResult(tb, f.cache, f.ctx, f.setupSession, frame, frame, i, persist)
		egraphBenchmarkAppendResult(f, res)
	}
	if persist {
		if err := f.cache.ReleaseSession(f.ctx, f.setupSession); err != nil {
			tb.Fatalf("release independent setup session: %v", err)
		}
	}
	if mode == egraphBenchmarkImported {
		f.reopenImported(tb)
	}
	return f
}

type egraphBenchmarkOwnershipShape string

const (
	egraphBenchmarkChain      egraphBenchmarkOwnershipShape = "chain"
	egraphBenchmarkStarFanout egraphBenchmarkOwnershipShape = "star-fanout"
	egraphBenchmarkStarShared egraphBenchmarkOwnershipShape = "star-shared"
)

func egraphBenchmarkOwnershipFixture(tb testing.TB, n int, shape egraphBenchmarkOwnershipShape, guard *egraphBenchmarkGuard) *egraphBenchmarkFixture {
	tb.Helper()
	if n < 1 {
		tb.Fatal("ownership fixture requires at least one result")
	}
	f := &egraphBenchmarkFixture{setupSession: "ownership-seed"}
	f.cache, f.ctx = egraphBenchmarkNewCache(tb, f.setupSession, "", nil)
	for i := range n {
		if guard != nil {
			guard.checkpoint(i)
		}
		frame := cacheTestIntCall(fmt.Sprintf("egraph-%s-%d", shape, i))
		res := egraphBenchmarkResult(tb, f.cache, f.ctx, f.setupSession, frame, frame, i, false)
		egraphBenchmarkAppendResult(f, res)
	}

	measureSession := "ownership-measure"
	switch shape {
	case egraphBenchmarkChain:
		for i := 0; i+1 < len(f.results); i++ {
			if guard != nil {
				guard.checkpoint(n + i)
			}
			if err := f.cache.AddExplicitDependency(f.ctx, f.results[i], f.results[i+1], "egraph_benchmark_chain"); err != nil {
				tb.Fatalf("add chain edge %d: %v", i, err)
			}
		}
		egraphBenchmarkClaimExact(f.cache, f.ctx, measureSession, f.allResultIDs[:1])
	case egraphBenchmarkStarFanout:
		for i := 1; i < len(f.results); i++ {
			if guard != nil {
				guard.checkpoint(n + i)
			}
			if err := f.cache.AddExplicitDependency(f.ctx, f.results[0], f.results[i], "egraph_benchmark_star_fanout"); err != nil {
				tb.Fatalf("add fanout edge %d: %v", i, err)
			}
		}
		egraphBenchmarkClaimExact(f.cache, f.ctx, measureSession, f.allResultIDs[:1])
	case egraphBenchmarkStarShared:
		shared := f.results[len(f.results)-1]
		for i := 0; i+1 < len(f.results); i++ {
			if guard != nil {
				guard.checkpoint(n + i)
			}
			if err := f.cache.AddExplicitDependency(f.ctx, f.results[i], shared, "egraph_benchmark_star_shared"); err != nil {
				tb.Fatalf("add shared edge %d: %v", i, err)
			}
		}
		egraphBenchmarkClaimExact(f.cache, f.ctx, measureSession, f.allResultIDs[:len(f.allResultIDs)-1])
	default:
		tb.Fatalf("unknown ownership shape %q", shape)
	}
	if err := f.cache.ReleaseSession(f.ctx, f.setupSession); err != nil {
		tb.Fatalf("release ownership seed session: %v", err)
	}
	f.setupSession = measureSession
	return f
}

func egraphBenchmarkWideOutputFixture(tb testing.TB, width int, mode egraphBenchmarkPersistence, guard *egraphBenchmarkGuard) *egraphBenchmarkFixture {
	tb.Helper()
	f := &egraphBenchmarkFixture{
		setupSession:       "wide-output-setup",
		sharedOutputDigest: digest.FromString("egraph-wide-output-content"),
	}
	if mode == egraphBenchmarkImported {
		f.dbPath = filepath.Join(tb.TempDir(), "cache.db")
	}
	f.cache, f.ctx = egraphBenchmarkNewCache(tb, f.setupSession, f.dbPath, nil)
	persist := egraphBenchmarkPersistenceEnabled(mode)
	sharedInputDigest := digest.FromString("egraph-wide-input-content")
	parents := make([]AnyResult, 0, width+1)
	for i := 0; i < width+1; i++ {
		if guard != nil {
			guard.checkpoint(i)
		}
		request := cacheTestIntCall("egraph-wide-input-request-" + strconv.Itoa(i))
		response := cacheTestIntCall("egraph-wide-input-response-"+strconv.Itoa(i), call.ExtraDigest{
			Label:  call.ExtraDigestLabelContent,
			Digest: sharedInputDigest,
		})
		res := egraphBenchmarkResult(tb, f.cache, f.ctx, f.setupSession, request, response, i, persist)
		parents = append(parents, res)
		egraphBenchmarkAppendResult(f, res)
	}

	for i := range width {
		if guard != nil {
			guard.checkpoint(width + 1 + i)
		}
		publishRequest := cacheTestIntCall("egraph-wide-output-publish-" + strconv.Itoa(i))
		// Keep request and response recipe identity equal so this fixture has
		// exactly one publication recipe digest, one taught structural recipe
		// digest, and the shared content digest per output: D=2R+1.
		response := cacheTestIntCall(publishRequest.Field, call.ExtraDigest{
			Label:  call.ExtraDigestLabelContent,
			Digest: f.sharedOutputDigest,
		})
		res := egraphBenchmarkResult(tb, f.cache, f.ctx, f.setupSession, publishRequest, response, i, persist)
		egraphBenchmarkAppendResult(f, res)
		f.outputResultIDs = append(f.outputResultIDs, res.cacheSharedResult().id)

		structural := &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "egraph-wide-structural",
			Receiver: &ResultCallRef{ResultID: uint64(parents[i].cacheSharedResult().id)},
		}
		if err := f.cache.TeachCallEquivalentToResult(f.ctx, f.setupSession, structural, res); err != nil {
			tb.Fatalf("teach wide structural result %d: %v", i, err)
		}
		f.exactRequests = append(f.exactRequests, structural)
	}
	f.structuralRequest = &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "egraph-wide-structural",
		Receiver: &ResultCallRef{ResultID: uint64(parents[len(parents)-1].cacheSharedResult().id)},
	}

	if persist {
		if err := f.cache.ReleaseSession(f.ctx, f.setupSession); err != nil {
			tb.Fatalf("release wide setup session: %v", err)
		}
	}
	if mode == egraphBenchmarkImported {
		f.reopenImported(tb)
	}
	return f
}

func egraphBenchmarkWideDigestFixture(tb testing.TB, width int, guard *egraphBenchmarkGuard) *egraphBenchmarkFixture {
	tb.Helper()
	f := &egraphBenchmarkFixture{setupSession: "wide-digest-measure"}
	f.cache, f.ctx = egraphBenchmarkNewCache(tb, f.setupSession, "", nil)
	extras := make([]call.ExtraDigest, width)
	for i := range width {
		if guard != nil {
			guard.checkpoint(i)
		}
		extras[i] = call.ExtraDigest{Digest: digest.FromString("egraph-wide-single-" + strconv.Itoa(i))}
	}
	request := cacheTestIntCall("egraph-wide-single-request")
	response := cacheTestIntCall("egraph-wide-single-response", extras...)
	res := egraphBenchmarkResult(tb, f.cache, f.ctx, f.setupSession, request, response, 1, false)
	egraphBenchmarkAppendResult(f, res)
	f.outputResultIDs = append(f.outputResultIDs, res.cacheSharedResult().id)
	return f
}

func egraphBenchmarkPopularInputFixture(tb testing.TB, termCount, mergeCount int, mode egraphBenchmarkPersistence, guard *egraphBenchmarkGuard) *egraphBenchmarkFixture {
	tb.Helper()
	f := &egraphBenchmarkFixture{setupSession: "popular-input-setup"}
	if mode == egraphBenchmarkImported {
		f.dbPath = filepath.Join(tb.TempDir(), "cache.db")
	}
	f.cache, f.ctx = egraphBenchmarkNewCache(tb, f.setupSession, f.dbPath, nil)
	persist := egraphBenchmarkPersistenceEnabled(mode)
	sharedInputDigest := digest.FromString("egraph-popular-input-content")
	baseRequest := cacheTestIntCall("egraph-popular-input-base-request")
	baseResponse := cacheTestIntCall("egraph-popular-input-base-response", call.ExtraDigest{
		Label:  call.ExtraDigestLabelContent,
		Digest: sharedInputDigest,
	})
	base := egraphBenchmarkResult(tb, f.cache, f.ctx, f.setupSession, baseRequest, baseResponse, 0, persist)
	egraphBenchmarkAppendResult(f, base)
	f.popularInputID = base.cacheSharedResult().id

	for i := range termCount {
		if guard != nil {
			guard.checkpoint(i)
		}
		publish := cacheTestIntCall("egraph-popular-output-" + strconv.Itoa(i))
		res := egraphBenchmarkResult(tb, f.cache, f.ctx, f.setupSession, publish, publish, i, persist)
		egraphBenchmarkAppendResult(f, res)
		structural := &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "egraph-popular-term-" + strconv.Itoa(i),
			Receiver: &ResultCallRef{ResultID: uint64(base.cacheSharedResult().id)},
		}
		if err := f.cache.TeachCallEquivalentToResult(f.ctx, f.setupSession, structural, res); err != nil {
			tb.Fatalf("teach popular term %d: %v", i, err)
		}
	}
	for i := range mergeCount {
		if guard != nil {
			guard.checkpoint(termCount + i)
		}
		frame := cacheTestIntCall("egraph-popular-alias-" + strconv.Itoa(i))
		alias := egraphBenchmarkResult(tb, f.cache, f.ctx, f.setupSession, frame, frame, termCount+i, persist)
		egraphBenchmarkAppendResult(f, alias)
		f.aliases = append(f.aliases, alias)
		f.aliasResultIDs = append(f.aliasResultIDs, alias.cacheSharedResult().id)
	}
	if persist {
		if err := f.cache.ReleaseSession(f.ctx, f.setupSession); err != nil {
			tb.Fatalf("release popular setup session: %v", err)
		}
	}
	if mode == egraphBenchmarkImported {
		f.reopenImported(tb)
		f.aliases = f.aliases[:0]
		for _, resultID := range f.aliasResultIDs {
			alias, err := f.cache.loadResultByResultID(f.ctx, "", f.server, uint64(resultID))
			if err != nil {
				tb.Fatalf("load imported popular alias %d: %v", resultID, err)
			}
			f.aliases = append(f.aliases, alias)
		}
	}
	return f
}

func egraphBenchmarkReceiverFixture(tb testing.TB, width int, mode egraphBenchmarkPersistence, guard *egraphBenchmarkGuard) (*egraphBenchmarkFixture, ObjectResult[*persistCodecObj]) {
	tb.Helper()
	srv := newPersistCodecImportTestServer()
	f := &egraphBenchmarkFixture{
		setupSession: "receiver-setup",
		server:       srv,
	}
	if mode == egraphBenchmarkImported {
		f.dbPath = filepath.Join(tb.TempDir(), "cache.db")
	}
	f.cache, f.ctx = egraphBenchmarkNewCache(tb, f.setupSession, f.dbPath, srv)
	persist := egraphBenchmarkPersistenceEnabled(mode)
	sharedContent := digest.FromString("egraph-receiver-wide-content")
	parents := make([]AnyResult, 0, width)
	for i := range width {
		if guard != nil {
			guard.checkpoint(i)
		}
		request := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType((&persistCodecObj{}).Type()), Field: "egraph-receiver-request-" + strconv.Itoa(i)}
		response := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType((&persistCodecObj{}).Type()),
			Field: "egraph-receiver-response-" + strconv.Itoa(i),
			ExtraDigests: []call.ExtraDigest{{
				Label:  call.ExtraDigestLabelContent,
				Digest: sharedContent,
			}},
		}
		res, err := f.cache.GetOrInitCall(f.ctx, f.setupSession, srv, &CallRequest{ResultCall: request, IsPersistable: persist}, func(context.Context) (AnyResult, error) {
			return NewObjectResultForCall(&persistCodecObj{Name: strconv.Itoa(i)}, srv, response)
		})
		if err != nil {
			tb.Fatalf("publish receiver parent %d: %v", i, err)
		}
		parents = append(parents, res)
		egraphBenchmarkAppendResult(f, res)
		f.outputResultIDs = append(f.outputResultIDs, res.cacheSharedResult().id)
	}
	parentID := parents[len(parents)-1].cacheSharedResult().id
	childFrame := &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType((&persistCodecObj{}).Type()),
		Field:    "egraph-receiver-child",
		Receiver: &ResultCallRef{ResultID: uint64(parentID)},
	}
	childAny, err := f.cache.GetOrInitCall(f.ctx, f.setupSession, srv, &CallRequest{ResultCall: childFrame, IsPersistable: persist}, func(context.Context) (AnyResult, error) {
		return NewObjectResultForCall(&persistCodecObj{Name: "child"}, srv, childFrame)
	})
	if err != nil {
		tb.Fatalf("publish receiver child: %v", err)
	}
	egraphBenchmarkAppendResult(f, childAny)
	childID := childAny.cacheSharedResult().id
	if persist {
		if err := f.cache.ReleaseSession(f.ctx, f.setupSession); err != nil {
			tb.Fatalf("release receiver setup session: %v", err)
		}
	}
	if mode == egraphBenchmarkImported {
		f.reopenImported(tb)
		childAny, err = f.cache.loadResultByResultID(f.ctx, "", srv, uint64(childID))
		if err != nil {
			tb.Fatalf("load imported receiver child: %v", err)
		}
	}
	child, ok := childAny.(ObjectResult[*persistCodecObj])
	if !ok {
		tb.Fatalf("receiver child has type %T", childAny)
	}
	return f, child
}

func egraphBenchmarkClaimExact(c *Cache, ctx context.Context, sessionID string, ids []sharedResultID) {
	c.sessionMu.Lock()
	if c.sessionResultIDsBySession == nil {
		c.sessionResultIDsBySession = make(map[string]map[sharedResultID]struct{})
	}
	if c.sessionResultIDsBySession[sessionID] == nil {
		c.sessionResultIDsBySession[sessionID] = make(map[sharedResultID]struct{}, len(ids))
	}
	c.sessionMu.Unlock()

	c.egraphMu.Lock()
	c.sessionMu.Lock()
	for _, id := range ids {
		res := c.resultsByID[id]
		if res == nil {
			continue
		}
		if _, ok := c.sessionResultIDsBySession[sessionID][id]; ok {
			continue
		}
		c.sessionResultIDsBySession[sessionID][id] = struct{}{}
		c.incrementIncomingOwnershipLocked(ctx, res)
	}
	c.sessionMu.Unlock()
	c.egraphMu.Unlock()
}

func egraphBenchmarkPreparePersistedRelease(tb testing.TB, f *egraphBenchmarkFixture, sessionID string) {
	tb.Helper()
	egraphBenchmarkClaimExact(f.cache, f.ctx, sessionID, f.allResultIDs)
	for _, id := range f.allResultIDs {
		if _, ok := f.cache.persistedEdgesByResult[id]; !ok {
			continue
		}
		removed, err := f.cache.removePersistedEdge(f.ctx, id)
		if err != nil {
			tb.Fatalf("remove persisted edge %d for release fixture: %v", id, err)
		}
		if !removed {
			tb.Fatalf("persisted edge %d was not removed", id)
		}
	}
	f.setupSession = sessionID
}

func egraphBenchmarkPersistOwnershipRoots(tb testing.TB, f *egraphBenchmarkFixture, shape egraphBenchmarkOwnershipShape) {
	tb.Helper()
	var rootIDs []sharedResultID
	switch shape {
	case egraphBenchmarkChain, egraphBenchmarkStarFanout:
		rootIDs = f.allResultIDs[:1]
	case egraphBenchmarkStarShared:
		rootIDs = f.allResultIDs[:len(f.allResultIDs)-1]
	default:
		tb.Fatalf("unknown ownership shape %q", shape)
	}
	f.cache.egraphMu.Lock()
	for _, resultID := range rootIDs {
		res := f.cache.resultsByID[resultID]
		if res == nil {
			tb.Fatalf("persist ownership root %d: missing result", resultID)
		}
		f.cache.upsertPersistedEdgeLocked(f.ctx, res, 0, false)
	}
	f.cache.egraphMu.Unlock()
	if err := f.cache.ReleaseSession(f.ctx, f.setupSession); err != nil {
		tb.Fatalf("release ownership measurement session: %v", err)
	}
}

type egraphBenchmarkDistribution struct {
	Count int `json:"count"`
	P50   int `json:"p50"`
	P95   int `json:"p95"`
	P99   int `json:"p99"`
	Max   int `json:"max"`
}

type egraphBenchmarkWidths struct {
	Results                  int
	Terms                    int
	Classes                  int
	ClassDigestWidth         egraphBenchmarkDistribution
	ClassResultWidth         egraphBenchmarkDistribution
	PostingWidth             egraphBenchmarkDistribution
	ResultIndexedDigestWidth egraphBenchmarkDistribution
	InputTermFanout          egraphBenchmarkDistribution
	TotalPostings            int
	AssociatedResults        int
	ResultTermCoverage       float64
}

func egraphBenchmarkDistributionFor(values []int) egraphBenchmarkDistribution {
	if len(values) == 0 {
		return egraphBenchmarkDistribution{}
	}
	sort.Ints(values)
	pick := func(percent int) int {
		idx := (len(values)*percent + 99) / 100
		if idx < 1 {
			idx = 1
		}
		if idx > len(values) {
			idx = len(values)
		}
		return values[idx-1]
	}
	return egraphBenchmarkDistribution{
		Count: len(values),
		P50:   pick(50),
		P95:   pick(95),
		P99:   pick(99),
		Max:   values[len(values)-1],
	}
}

func egraphBenchmarkFindRoot(c *Cache, id eqClassID) eqClassID {
	if id == 0 || int(id) >= len(c.egraphParents) {
		return 0
	}
	for c.egraphParents[id] != id {
		id = c.egraphParents[id]
	}
	return id
}

func egraphBenchmarkWidthsForCache(c *Cache) egraphBenchmarkWidths {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()

	widths := egraphBenchmarkWidths{Results: len(c.resultsByID), Terms: len(c.egraphTerms)}
	classDigests := make([]int, 0, len(c.eqClassToDigests))
	classResults := make(map[eqClassID]map[sharedResultID]struct{})
	for eqID, digests := range c.eqClassToDigests {
		root := egraphBenchmarkFindRoot(c, eqID)
		if root != eqID || len(digests) == 0 {
			continue
		}
		classDigests = append(classDigests, len(digests))
	}
	widths.Classes = len(classDigests)
	for resultID, classes := range c.resultOutputEqClasses {
		for eqID := range classes {
			root := egraphBenchmarkFindRoot(c, eqID)
			if root == 0 {
				continue
			}
			if classResults[root] == nil {
				classResults[root] = make(map[sharedResultID]struct{})
			}
			classResults[root][resultID] = struct{}{}
		}
	}
	classResultWidths := make([]int, 0, len(classResults))
	for _, results := range classResults {
		classResultWidths = append(classResultWidths, len(results))
	}
	postingWidths := make([]int, 0, len(c.egraphResultsByDigest))
	indexedByResult := make(map[sharedResultID]int, len(c.resultsByID))
	for _, results := range c.egraphResultsByDigest {
		if results == nil || results.Empty() {
			continue
		}
		postingWidths = append(postingWidths, results.Size())
		widths.TotalPostings += results.Size()
		for resultID := range results.Items() {
			indexedByResult[resultID]++
		}
	}
	indexedWidths := make([]int, 0, len(c.resultsByID))
	for resultID := range c.resultsByID {
		indexedWidths = append(indexedWidths, indexedByResult[resultID])
		if len(c.resultTerms[resultID]) > 0 {
			widths.AssociatedResults++
		}
	}
	inputTermFanouts := make([]int, 0, len(c.inputEqClassToTerms))
	for _, terms := range c.inputEqClassToTerms {
		if len(terms) > 0 {
			inputTermFanouts = append(inputTermFanouts, len(terms))
		}
	}
	if widths.Results > 0 {
		widths.ResultTermCoverage = float64(widths.AssociatedResults) / float64(widths.Results)
	}
	widths.ClassDigestWidth = egraphBenchmarkDistributionFor(classDigests)
	widths.ClassResultWidth = egraphBenchmarkDistributionFor(classResultWidths)
	widths.PostingWidth = egraphBenchmarkDistributionFor(postingWidths)
	widths.ResultIndexedDigestWidth = egraphBenchmarkDistributionFor(indexedWidths)
	widths.InputTermFanout = egraphBenchmarkDistributionFor(inputTermFanouts)
	return widths
}

func egraphBenchmarkOutputClassShape(c *Cache, resultID sharedResultID) (digests, results int) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	for eqID := range c.resultOutputEqClasses[resultID] {
		root := egraphBenchmarkFindRoot(c, eqID)
		if root == 0 {
			continue
		}
		digests = len(c.eqClassToDigests[root])
		seen := make(map[sharedResultID]struct{})
		for dig := range c.eqClassToDigests[root] {
			posting := c.egraphResultsByDigest[dig]
			if posting == nil {
				continue
			}
			for id := range posting.Items() {
				seen[id] = struct{}{}
			}
		}
		results = len(seen)
		return digests, results
	}
	return 0, 0
}

type egraphBenchmarkTargetOutputShape struct {
	Digests int
	Results int
	Valid   bool
}

func egraphBenchmarkTargetOutputShapeForFixture(f *egraphBenchmarkFixture) egraphBenchmarkTargetOutputShape {
	if f == nil || f.cache == nil || len(f.outputResultIDs) == 0 {
		return egraphBenchmarkTargetOutputShape{}
	}
	digests, results := egraphBenchmarkOutputClassShape(f.cache, f.outputResultIDs[0])
	return egraphBenchmarkTargetOutputShape{Digests: digests, Results: results, Valid: true}
}

func egraphBenchmarkReportTargetOutputShape(b *testing.B, shape egraphBenchmarkTargetOutputShape) {
	if !shape.Valid {
		return
	}
	b.ReportMetric(float64(shape.Digests), "target-output-class-digests")
	b.ReportMetric(float64(shape.Results), "target-output-class-results")
}

func egraphBenchmarkReportDistribution(b *testing.B, prefix string, dist egraphBenchmarkDistribution) {
	b.ReportMetric(float64(dist.Count), prefix+"-count")
	b.ReportMetric(float64(dist.P50), prefix+"-p50")
	b.ReportMetric(float64(dist.P95), prefix+"-p95")
	b.ReportMetric(float64(dist.P99), prefix+"-p99")
	b.ReportMetric(float64(dist.Max), prefix+"-max")
}

func egraphBenchmarkReportWidths(b *testing.B, widths egraphBenchmarkWidths) {
	b.ReportMetric(float64(widths.Results), "shape-results")
	b.ReportMetric(float64(widths.Terms), "shape-terms")
	b.ReportMetric(float64(widths.Classes), "shape-classes")
	b.ReportMetric(float64(widths.TotalPostings), "postings-total")
	b.ReportMetric(float64(widths.AssociatedResults), "associated-results")
	b.ReportMetric(widths.ResultTermCoverage, "result-term-coverage")
	egraphBenchmarkReportDistribution(b, "class-digests", widths.ClassDigestWidth)
	egraphBenchmarkReportDistribution(b, "class-results", widths.ClassResultWidth)
	egraphBenchmarkReportDistribution(b, "posting", widths.PostingWidth)
	egraphBenchmarkReportDistribution(b, "result-indexed-digests", widths.ResultIndexedDigestWidth)
	egraphBenchmarkReportDistribution(b, "input-term-fanout", widths.InputTermFanout)
}

type egraphBenchmarkLockRecorder struct {
	next                    atomic.Uint64
	dropped                 atomic.Uint64
	observations            []cacheEgraphLockObservation
	measurementNext         atomic.Uint64
	measurementDropped      atomic.Uint64
	measurementObservations []cacheEgraphMeasurementObservation
	recordDetails           bool
	sampleNext              atomic.Uint64
	sampleEvery             uint64
	signalOp                string
	signal                  chan struct{}
	signalOnce              sync.Once
}

func newEgraphBenchmarkLockRecorder(capacity int) *egraphBenchmarkLockRecorder {
	if capacity < 64 {
		capacity = 64
	}
	return &egraphBenchmarkLockRecorder{
		observations:            make([]cacheEgraphLockObservation, capacity),
		measurementObservations: make([]cacheEgraphMeasurementObservation, capacity),
		recordDetails:           true,
		sampleEvery:             1,
	}
}

func newEgraphBenchmarkLockOnlyRecorder(capacity int) *egraphBenchmarkLockRecorder {
	r := newEgraphBenchmarkLockRecorder(capacity)
	r.recordDetails = false
	return r
}

func newEgraphBenchmarkSampledLockRecorder(capacity int, sampleEvery uint64) *egraphBenchmarkLockRecorder {
	r := newEgraphBenchmarkLockOnlyRecorder(capacity)
	if sampleEvery == 0 {
		sampleEvery = 1
	}
	r.sampleEvery = sampleEvery
	return r
}

func newEgraphBenchmarkSignalingRecorder(capacity int, operation string) *egraphBenchmarkLockRecorder {
	r := newEgraphBenchmarkLockRecorder(capacity)
	r.signalOp = operation
	r.signal = make(chan struct{})
	return r
}

func (r *egraphBenchmarkLockRecorder) cacheEgraphLockShouldMeasure(_ string, _ cacheEgraphLockMode) bool {
	sequence := r.sampleNext.Add(1) - 1
	return sequence%r.sampleEvery == 0
}

func (r *egraphBenchmarkLockRecorder) cacheEgraphLockAcquired(operation string, _ cacheEgraphLockMode) {
	if r.signal != nil && operation == r.signalOp {
		r.signalOnce.Do(func() { close(r.signal) })
	}
}

func (r *egraphBenchmarkLockRecorder) cacheEgraphLockObserved(obs cacheEgraphLockObservation) {
	idx := r.next.Add(1) - 1
	if idx >= uint64(len(r.observations)) {
		r.dropped.Add(1)
		return
	}
	r.observations[idx] = obs
}

func (r *egraphBenchmarkLockRecorder) cacheEgraphMeasurementObserved(obs cacheEgraphMeasurementObservation) {
	if !r.recordDetails {
		return
	}
	idx := r.measurementNext.Add(1) - 1
	if idx >= uint64(len(r.measurementObservations)) {
		r.measurementDropped.Add(1)
		return
	}
	r.measurementObservations[idx] = obs
}

func (r *egraphBenchmarkLockRecorder) snapshot() []cacheEgraphLockObservation {
	n := r.next.Load()
	if n > uint64(len(r.observations)) {
		n = uint64(len(r.observations))
	}
	return slices.Clone(r.observations[:n])
}

func (r *egraphBenchmarkLockRecorder) measurementSnapshot() []cacheEgraphMeasurementObservation {
	n := r.measurementNext.Load()
	if n > uint64(len(r.measurementObservations)) {
		n = uint64(len(r.measurementObservations))
	}
	return slices.Clone(r.measurementObservations[:n])
}

type egraphBenchmarkDurationDistribution struct {
	Count int64 `json:"count"`
	P50NS int64 `json:"p50_ns"`
	P95NS int64 `json:"p95_ns"`
	P99NS int64 `json:"p99_ns"`
	MaxNS int64 `json:"max_ns"`
	SumNS int64 `json:"sum_ns"`
}

type egraphBenchmarkLockSummary struct {
	Operation string                              `json:"operation"`
	Mode      string                              `json:"mode"`
	Wait      egraphBenchmarkDurationDistribution `json:"wait"`
	Hold      egraphBenchmarkDurationDistribution `json:"hold"`
}

type egraphBenchmarkMeasurementSummary struct {
	Operation string                              `json:"operation"`
	Duration  egraphBenchmarkDurationDistribution `json:"duration"`
	Count     egraphBenchmarkDistribution         `json:"count"`
	Maximum   egraphBenchmarkDistribution         `json:"maximum"`
}

func egraphBenchmarkDurationDistributionFor(values []time.Duration) egraphBenchmarkDurationDistribution {
	if len(values) == 0 {
		return egraphBenchmarkDurationDistribution{}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	pick := func(percent int) int64 {
		idx := (len(values)*percent + 99) / 100
		if idx < 1 {
			idx = 1
		}
		if idx > len(values) {
			idx = len(values)
		}
		return values[idx-1].Nanoseconds()
	}
	var sum int64
	for _, value := range values {
		sum += value.Nanoseconds()
	}
	return egraphBenchmarkDurationDistribution{
		Count: int64(len(values)),
		P50NS: pick(50),
		P95NS: pick(95),
		P99NS: pick(99),
		MaxNS: values[len(values)-1].Nanoseconds(),
		SumNS: sum,
	}
}

func egraphBenchmarkSummarizeLocks(observations []cacheEgraphLockObservation) []egraphBenchmarkLockSummary {
	type group struct {
		mode cacheEgraphLockMode
		wait []time.Duration
		hold []time.Duration
	}
	groups := make(map[string]*group)
	for _, obs := range observations {
		key := fmt.Sprintf("%s/%d", obs.Operation, obs.Mode)
		g := groups[key]
		if g == nil {
			g = &group{mode: obs.Mode}
			groups[key] = g
		}
		g.wait = append(g.wait, obs.Wait)
		g.hold = append(g.hold, obs.Hold)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]egraphBenchmarkLockSummary, 0, len(keys))
	for _, key := range keys {
		g := groups[key]
		operation := key[:len(key)-2]
		mode := "read"
		if g.mode == cacheEgraphLockWrite {
			mode = "write"
		}
		out = append(out, egraphBenchmarkLockSummary{
			Operation: operation,
			Mode:      mode,
			Wait:      egraphBenchmarkDurationDistributionFor(g.wait),
			Hold:      egraphBenchmarkDurationDistributionFor(g.hold),
		})
	}
	return out
}

func egraphBenchmarkSummarizeMeasurements(observations []cacheEgraphMeasurementObservation) []egraphBenchmarkMeasurementSummary {
	type group struct {
		duration []time.Duration
		count    []int
		maximum  []int
	}
	groups := make(map[string]*group)
	for _, obs := range observations {
		g := groups[obs.Operation]
		if g == nil {
			g = &group{}
			groups[obs.Operation] = g
		}
		g.duration = append(g.duration, obs.Duration)
		g.count = append(g.count, int(obs.Count))
		g.maximum = append(g.maximum, int(obs.Maximum))
	}
	operations := make([]string, 0, len(groups))
	for operation := range groups {
		operations = append(operations, operation)
	}
	sort.Strings(operations)
	out := make([]egraphBenchmarkMeasurementSummary, 0, len(operations))
	for _, operation := range operations {
		g := groups[operation]
		out = append(out, egraphBenchmarkMeasurementSummary{
			Operation: operation,
			Duration:  egraphBenchmarkDurationDistributionFor(g.duration),
			Count:     egraphBenchmarkDistributionFor(g.count),
			Maximum:   egraphBenchmarkDistributionFor(g.maximum),
		})
	}
	return out
}

func egraphBenchmarkReportLocks(b *testing.B, recorder *egraphBenchmarkLockRecorder) {
	b.Helper()
	observations := recorder.snapshot()
	waits := make([]time.Duration, 0, len(observations))
	holds := make([]time.Duration, 0, len(observations))
	for _, obs := range observations {
		waits = append(waits, obs.Wait)
		holds = append(holds, obs.Hold)
	}
	waitDist := egraphBenchmarkDurationDistributionFor(waits)
	holdDist := egraphBenchmarkDurationDistributionFor(holds)
	b.ReportMetric(float64(len(observations)), "egraph-lock-acquires")
	b.ReportMetric(float64(recorder.sampleNext.Load()), "egraph-lock-attempts")
	b.ReportMetric(float64(recorder.sampleEvery), "lock-sample-every")
	b.ReportMetric(float64(waitDist.P50NS), "lock-wait-p50-ns")
	b.ReportMetric(float64(waitDist.P95NS), "lock-wait-p95-ns")
	b.ReportMetric(float64(waitDist.P99NS), "lock-wait-p99-ns")
	b.ReportMetric(float64(waitDist.MaxNS), "lock-wait-max-ns")
	b.ReportMetric(float64(holdDist.P50NS), "lock-hold-p50-ns")
	b.ReportMetric(float64(holdDist.P95NS), "lock-hold-p95-ns")
	b.ReportMetric(float64(holdDist.P99NS), "lock-hold-p99-ns")
	b.ReportMetric(float64(holdDist.MaxNS), "lock-hold-max-ns")
	b.ReportMetric(float64(recorder.dropped.Load()), "lock-observations-dropped")
	for _, summary := range egraphBenchmarkSummarizeLocks(observations) {
		payload, err := json.Marshal(summary)
		if err != nil {
			b.Fatalf("marshal lock summary: %v", err)
		}
		b.Logf("EGRAPH_LOCK_METRIC %s", payload)
	}
	measurementObservations := recorder.measurementSnapshot()
	b.ReportMetric(float64(len(measurementObservations)), "egraph-detail-observations")
	b.ReportMetric(float64(recorder.measurementDropped.Load()), "detail-observations-dropped")
	for _, summary := range egraphBenchmarkSummarizeMeasurements(measurementObservations) {
		payload, err := json.Marshal(summary)
		if err != nil {
			b.Fatalf("marshal detailed measurement summary: %v", err)
		}
		b.Logf("EGRAPH_DETAIL_METRIC %s", payload)
	}
}

func egraphBenchmarkRunMeasured(b *testing.B, f *egraphBenchmarkFixture, recorderCapacity int, fn func() error) time.Duration {
	b.Helper()
	widths := egraphBenchmarkWidthsForCache(f.cache)
	targetOutput := egraphBenchmarkTargetOutputShapeForFixture(f)
	recorder := newEgraphBenchmarkLockRecorder(recorderCapacity)
	f.cache.egraphLockObserver = recorder
	b.ReportAllocs()
	b.StartTimer()
	started := time.Now()
	err := fn()
	duration := time.Since(started)
	b.StopTimer()
	f.cache.egraphLockObserver = nil
	if err != nil {
		b.Fatal(err)
	}
	egraphBenchmarkReportWidths(b, widths)
	egraphBenchmarkReportTargetOutputShape(b, targetOutput)
	egraphBenchmarkReportLocks(b, recorder)
	b.ReportMetric(float64(duration.Nanoseconds()), "operation-wall-ns")
	if duration > egraphBenchmarkOperationLimit {
		b.ReportMetric(1, "stop-limit-exceeded")
		b.Logf("EGRAPH_BENCH_STOP operation exceeded %s: %s", egraphBenchmarkOperationLimit, duration)
	}
	return duration
}

func TestCacheEGraphBenchmarkLockRecorder(t *testing.T) {
	f := egraphBenchmarkIndependentFixture(t, 2, egraphBenchmarkTransient, nil)
	defer f.close(t)
	recorder := newEgraphBenchmarkLockRecorder(16)
	f.cache.egraphLockObserver = recorder
	request := cacheTestIntCall("egraph-independent-0")
	_, err := f.cache.GetOrInitCall(f.ctx, "recorder-session", noopTypeResolver{}, &CallRequest{ResultCall: request}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("exact benchmark hit unexpectedly executed")
	})
	f.cache.egraphLockObserver = nil
	assert.NilError(t, err)
	observations := recorder.snapshot()
	assert.Assert(t, len(observations) > 0)
	var sawLookup bool
	for _, obs := range observations {
		assert.Assert(t, obs.Wait >= 0)
		assert.Assert(t, obs.Hold >= 0)
		if obs.Operation == "lookup-request" && obs.Mode == cacheEgraphLockWrite {
			sawLookup = true
		}
	}
	assert.Assert(t, sawLookup, "expected a measured lookup-request write acquisition")
	measurements := recorder.measurementSnapshot()
	assert.Assert(t, len(measurements) > 0)
	var sawCandidates bool
	for _, obs := range measurements {
		assert.Assert(t, obs.Duration >= 0)
		if obs.Operation == "request-lookup-candidates" {
			sawCandidates = true
			assert.Equal(t, obs.Count, int64(1))
		}
	}
	assert.Assert(t, sawCandidates, "expected a candidate materialization observation")
}

func TestCacheEGraphBenchmarkCollectorMeasurement(t *testing.T) {
	const resultCount = 8
	f := egraphBenchmarkOwnershipFixture(t, resultCount, egraphBenchmarkStarFanout, nil)
	defer f.close(t)
	recorder := newEgraphBenchmarkLockRecorder(64)
	f.cache.egraphLockObserver = recorder
	assert.NilError(t, f.cache.ReleaseSession(f.ctx, f.setupSession))
	f.cache.egraphLockObserver = nil

	var collector *cacheEgraphMeasurementObservation
	for _, obs := range recorder.measurementSnapshot() {
		if obs.Operation == "collect-unowned" {
			copy := obs
			collector = &copy
			break
		}
	}
	assert.Assert(t, collector != nil, "expected a collector observation")
	assert.Equal(t, collector.Count, int64(resultCount))
	assert.Equal(t, collector.Maximum, int64(resultCount-1))
	assert.Equal(t, len(f.cache.resultsByID), 0)
}

func TestCacheEGraphBenchmarkFixtureShapes(t *testing.T) {
	t.Run("ownership", func(t *testing.T) {
		for _, shape := range []egraphBenchmarkOwnershipShape{egraphBenchmarkChain, egraphBenchmarkStarFanout, egraphBenchmarkStarShared} {
			t.Run(string(shape), func(t *testing.T) {
				f := egraphBenchmarkOwnershipFixture(t, 8, shape, nil)
				defer f.close(t)
				assert.Equal(t, len(f.cache.resultsByID), 8)
				assert.NilError(t, f.cache.ReleaseSession(f.ctx, f.setupSession))
				assert.Equal(t, len(f.cache.resultsByID), 0)
			})
		}
	})

	t.Run("wide-output", func(t *testing.T) {
		const width = 4
		f := egraphBenchmarkWideOutputFixture(t, width, egraphBenchmarkTransient, nil)
		defer f.close(t)
		digests, results := egraphBenchmarkOutputClassShape(f.cache, f.outputResultIDs[0])
		assert.Equal(t, results, width)
		assert.Equal(t, digests, 2*width+1)
		assert.NilError(t, egraphBenchmarkRunLookup(f, egraphBenchmarkLookupExact, "wide-route-check"))
		assert.NilError(t, egraphBenchmarkRunLookup(f, egraphBenchmarkLookupStructural, "wide-route-check"))
	})

	t.Run("wide-digest", func(t *testing.T) {
		const width = 16
		f := egraphBenchmarkWideDigestFixture(t, width, nil)
		defer f.close(t)
		digests, results := egraphBenchmarkOutputClassShape(f.cache, f.outputResultIDs[0])
		assert.Equal(t, results, 1)
		assert.Equal(t, digests, width+2)
	})

	t.Run("popular-input", func(t *testing.T) {
		const termCount = 8
		f := egraphBenchmarkPopularInputFixture(t, termCount, 1, egraphBenchmarkTransient, nil)
		defer f.close(t)
		widths := egraphBenchmarkWidthsForCache(f.cache)
		assert.Assert(t, widths.InputTermFanout.Max >= termCount)
	})
}

func TestCacheEGraphBenchmarkImportedShape(t *testing.T) {
	const width = 4
	fresh := egraphBenchmarkWideOutputFixture(t, width, egraphBenchmarkPersistedFresh, nil)
	freshWidths := egraphBenchmarkWidthsForCache(fresh.cache)
	fresh.close(t)

	imported := egraphBenchmarkWideOutputFixture(t, width, egraphBenchmarkImported, nil)
	defer imported.close(t)
	importedWidths := egraphBenchmarkWidthsForCache(imported.cache)
	assert.Equal(t, importedWidths.AssociatedResults, 0)
	assert.Equal(t, len(imported.cache.termResults), 0)
	assert.Equal(t, len(imported.cache.resultTerms), 0)
	assert.Assert(t, importedWidths.TotalPostings > freshWidths.TotalPostings,
		"import should broaden postings: fresh=%d imported=%d", freshWidths.TotalPostings, importedWidths.TotalPostings)
	digests, results := egraphBenchmarkOutputClassShape(imported.cache, imported.outputResultIDs[0])
	assert.Equal(t, results, width)
	assert.Equal(t, digests, 2*width+1)
	assert.NilError(t, egraphBenchmarkRunLookup(imported, egraphBenchmarkLookupExact, "imported-route-check"))
	assert.NilError(t, egraphBenchmarkRunLookup(imported, egraphBenchmarkLookupStructural, "imported-route-check"))
}

func TestEGraphBenchmarkDistributions(t *testing.T) {
	got := egraphBenchmarkDistributionFor([]int{100, 1, 5, 50, 10})
	assert.DeepEqual(t, got, egraphBenchmarkDistribution{Count: 5, P50: 10, P95: 100, P99: 100, Max: 100})
	durations := egraphBenchmarkDurationDistributionFor([]time.Duration{100, 1, 5, 50, 10})
	assert.DeepEqual(t, durations, egraphBenchmarkDurationDistribution{Count: 5, P50NS: 10, P95NS: 100, P99NS: 100, MaxNS: 100, SumNS: 166})
	measurements := egraphBenchmarkSummarizeMeasurements([]cacheEgraphMeasurementObservation{
		{Operation: "phase", Duration: 10, Count: 2, Maximum: 4},
		{Operation: "phase", Duration: 20, Count: 3, Maximum: 8},
	})
	assert.Equal(t, len(measurements), 1)
	assert.Equal(t, measurements[0].Duration.SumNS, int64(30))
	assert.Equal(t, measurements[0].Count.Max, 3)
	assert.Equal(t, measurements[0].Maximum.Max, 8)

	recorder := newEgraphBenchmarkSampledLockRecorder(64, 8)
	measured := 0
	for range 17 {
		if recorder.cacheEgraphLockShouldMeasure("sample", cacheEgraphLockWrite) {
			measured++
		}
	}
	assert.Equal(t, measured, 3)
	assert.Equal(t, recorder.sampleNext.Load(), uint64(17))
}

func egraphBenchmarkSortedIDs(ids []sharedResultID) []sharedResultID {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}
