package dagql

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/cachefact"
	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// factRecorder is a FactSink that keeps every fact after a JSON round trip,
// so the tests see exactly what a consumer decodes.
type factRecorder struct {
	t     testing.TB
	mu    sync.Mutex
	facts []cachefact.Fact
}

func (r *factRecorder) Emit(f cachefact.Fact) {
	data, err := json.Marshal(f)
	if err != nil {
		r.t.Errorf("encode fact %d (%s): %v", f.Seq, f.Kind(), err)
		return
	}
	decoded, err := cachefact.Decode(data)
	if err != nil {
		r.t.Errorf("decode fact %s: %v", data, err)
		return
	}
	r.mu.Lock()
	r.facts = append(r.facts, decoded)
	r.mu.Unlock()
}

func (r *factRecorder) all() []cachefact.Fact {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.facts)
}

func (r *factRecorder) since(seq uint64) []cachefact.Fact {
	var out []cachefact.Fact
	for _, f := range r.all() {
		if f.Seq > seq {
			out = append(out, f)
		}
	}
	return out
}

func (r *factRecorder) lastSeq() uint64 {
	facts := r.all()
	if len(facts) == 0 {
		return 0
	}
	return facts[len(facts)-1].Seq
}

func newFactRecorder(t testing.TB) *factRecorder {
	return &factRecorder{t: t}
}

// attachFactRecorder attaches a recorder to a cache built by a shared test
// helper, before any result exists.
func attachFactRecorder(t *testing.T, c *Cache) *factRecorder {
	t.Helper()
	rec := newFactRecorder(t)
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	require.Empty(t, c.resultsByID, "attach the recorder before the first result")
	c.factSink = rec
	return rec
}

func factKinds(facts []cachefact.Fact) []cachefact.Kind {
	out := make([]cachefact.Kind, len(facts))
	for i, f := range facts {
		out[i] = f.Kind()
	}
	return out
}

// factModel is what a consumer knows from one engine's facts alone.
type factModel struct {
	lastSeq uint64
	classes map[string][]string // representative digest -> class digests
	rows    map[uint64]*factRow
}

type factRow struct {
	origin        cachefact.Origin
	digests       map[string]bool
	deps          []uint64
	expiresAtUnix int64
	retained      bool
	edgeExpires   int64
	unpruneable   bool
	parts         []string
}

func replayFacts(t *testing.T, facts []cachefact.Fact) *factModel {
	t.Helper()
	m := &factModel{classes: map[string][]string{}, rows: map[uint64]*factRow{}}
	row := func(f cachefact.Fact, id uint64) *factRow {
		t.Helper()
		r := m.rows[id]
		require.NotNil(t, r, "fact %d (%s) names result %d, which the stream has not announced", f.Seq, f.Kind(), id)
		return r
	}
	knownDeps := func(f cachefact.Fact, deps []uint64) {
		t.Helper()
		for _, dep := range deps {
			row(f, dep)
		}
	}
	for _, f := range facts {
		require.Equal(t, m.lastSeq+1, f.Seq, "fact sequence is dense")
		m.lastSeq = f.Seq
		switch body := f.Body.(type) {
		case cachefact.EngineStart, cachefact.EngineAlive, cachefact.EngineStop:
		case cachefact.Class:
			digests := make([]string, 0, len(body.Digests))
			for _, d := range body.Digests {
				digests = append(digests, d.Digest)
			}
			slices.Sort(digests)
			digests = slices.Compact(digests)
			require.NotEmpty(t, digests)
			m.classes[digests[0]] = digests
		case cachefact.TermFact:
			require.Contains(t, m.classes, body.Output, "term output names an announced class")
			for _, in := range body.Inputs {
				require.Contains(t, m.classes, in.Digest, "term input names an announced class")
			}
		case cachefact.Result:
			require.NotContains(t, m.rows, body.ID, "result %d announced twice", body.ID)
			r := &factRow{origin: body.Origin, digests: map[string]bool{}, deps: body.Deps, expiresAtUnix: body.ExpiresAtUnix, retained: body.Retained, edgeExpires: body.RetentionExpiresAtUnix, unpruneable: body.Unpruneable}
			for _, d := range body.Digests {
				if body.Origin == cachefact.OriginRestored {
					require.Contains(t, m.classes, d.Digest, "restored result names an announced class")
					for _, dig := range m.classes[d.Digest] {
						r.digests[dig] = true
					}
					continue
				}
				r.digests[d.Digest] = true
			}
			if body.Origin == cachefact.OriginComputed {
				require.NotEmpty(t, body.Terms, "a computed result names its terms")
			}
			knownDeps(f, body.Deps)
			m.rows[body.ID] = r
		case cachefact.Deps:
			r := row(f, body.ID)
			require.True(t, body.Complete)
			knownDeps(f, body.Deps)
			r.deps = body.Deps
		case cachefact.Identity:
			r := row(f, body.ID)
			for _, d := range body.Digests {
				r.digests[d.Digest] = true
			}
			r.expiresAtUnix = body.ExpiresAtUnix
		case cachefact.Retention:
			r := row(f, body.ID)
			r.retained = body.Retained
			r.edgeExpires = body.ExpiresAtUnix
			r.unpruneable = body.Unpruneable
			if body.Unpruneable {
				r.expiresAtUnix = 0
			}
		case cachefact.Part:
			r := row(f, body.ID)
			require.Equal(t, cachefact.PartStateCompleted, body.State)
			r.parts = append(r.parts, body.OutputPath+"/"+body.Part)
		case cachefact.Removed:
			require.NotEmpty(t, body.IDs)
			for _, id := range body.IDs {
				row(f, id)
				delete(m.rows, id)
			}
		default:
			t.Fatalf("unexpected fact body %T", f.Body)
		}
	}
	return m
}

func cacheFactsDebugSnapshot(t *testing.T, c *Cache) CacheDebugSnapshot {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, c.WriteDebugCacheSnapshot(&buf))
	var snap CacheDebugSnapshot
	require.NoError(t, json.Unmarshal(buf.Bytes(), &snap))
	return snap
}

// requireFactsMatchSnapshot replays every fact the recorder saw and requires
// that the replayed rows are exactly the engine's own statement of its cache:
// the same results, digest postings, dependencies, retention and expiries,
// with the snapshot's fact_seq equal to the last fact.
func requireFactsMatchSnapshot(t *testing.T, c *Cache, rec *factRecorder) *factModel {
	t.Helper()
	snap := cacheFactsDebugSnapshot(t, c)
	facts := rec.all()
	model := replayFacts(t, facts)
	require.Equal(t, model.lastSeq, snap.FactSeq, "snapshot fact_seq bounds the facts")
	require.Equal(t, c.engineInstanceID, snap.EngineInstance)

	snapIDs := map[uint64]bool{}
	for _, res := range snap.Results {
		snapIDs[res.SharedResultID] = true
		row := model.rows[res.SharedResultID]
		require.NotNil(t, row, "result %d (%s) is in the snapshot but was never announced", res.SharedResultID, res.Description)
		want := map[string]bool{}
		for _, dig := range res.IndexedDigests {
			want[dig] = true
		}
		require.Equal(t, want, row.digests, "digest postings of result %d (%s)", res.SharedResultID, res.Description)
		deps := slices.Clone(res.ExplicitDeps)
		if deps == nil {
			deps = []uint64{}
		}
		rowDeps := slices.Clone(row.deps)
		if rowDeps == nil {
			rowDeps = []uint64{}
		}
		require.Equal(t, deps, rowDeps, "dependencies of result %d", res.SharedResultID)
		require.Equal(t, res.HasPersistedEdge, row.retained, "retention of result %d", res.SharedResultID)
		require.Equal(t, res.PersistedEdgeUnpruneable, row.unpruneable, "unpruneable of result %d", res.SharedResultID)
		require.Equal(t, res.PersistedEdgeExpiresAtUnix, row.edgeExpires, "retention expiry of result %d", res.SharedResultID)
		require.Equal(t, res.ExpiresAtUnix, row.expiresAtUnix, "own expiry of result %d", res.SharedResultID)
	}
	require.ElementsMatch(t, slices.Collect(maps.Keys(snapIDs)), slices.Collect(maps.Keys(model.rows)), "announced results are exactly the cached results")
	return model
}

func factsOfKind[T cachefact.Body](facts []cachefact.Fact) []T {
	var out []T
	for _, f := range facts {
		if body, ok := f.Body.(T); ok {
			out = append(out, body)
		}
	}
	return out
}

func TestCacheFactsComputedResults(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	rec := newFactRecorder(t)
	c, err := NewCache(ctx, "", nil, nil, WithFactSink(rec), WithEngineInstanceID("engine-instance-a"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	const session = "facts-session"
	publish := func(session string, req *CallRequest, v int) AnyResult {
		t.Helper()
		res, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, req, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(req.ResultCall, v), nil
		})
		require.NoError(t, err)
		return res
	}
	id := func(res AnyResult) uint64 {
		n, ok := CacheResultNumber(res)
		require.True(t, ok)
		return n
	}

	// A persistable leaf, and a root whose frame names the leaf as receiver.
	leaf := publish(session, &CallRequest{ResultCall: cacheTestIntCall("leaf"), IsPersistable: true}, 1)
	rootFrame := &ResultCall{Kind: ResultCallKindField, Field: "root", Type: NewResultCallType(Int(0).Type()), Receiver: &ResultCallRef{ResultID: id(leaf)}}
	root := publish(session, &CallRequest{ResultCall: rootFrame, IsPersistable: true}, 2)
	requireFactsMatchSnapshot(t, c, rec)

	facts := rec.all()
	require.Equal(t, []cachefact.Kind{
		cachefact.KindResult, cachefact.KindDeps, cachefact.KindRetention,
		cachefact.KindResult, cachefact.KindDeps, cachefact.KindRetention,
	}, factKinds(facts), "a computed result is announced, then its dependency set, then its retention")
	results := factsOfKind[cachefact.Result](facts)
	require.Equal(t, cachefact.OriginComputed, results[1].Origin)
	require.Equal(t, "root", results[1].Field)
	require.Equal(t, "Int", results[1].TypeName)
	require.Len(t, results[1].Terms, 1)
	require.Equal(t, cacheTestCallDigest(rootFrame).String(), results[1].Digests[0].Digest)
	require.Equal(t, cachefact.LabelRecipe, results[1].Digests[0].Label)
	require.Equal(t, []uint64{id(leaf)}, factsOfKind[cachefact.Deps](facts)[1].Deps)

	// A TTL-bearing hit lowers the root's own expiry and the edge's expiry.
	before := rec.lastSeq()
	ttlHit := publish(session, &CallRequest{ResultCall: rootFrame.clone(), IsPersistable: true, TTL: 3600}, 99)
	require.True(t, ttlHit.HitCache())
	hitFacts := rec.since(before)
	identities := factsOfKind[cachefact.Identity](hitFacts)
	require.Len(t, identities, 1)
	require.Equal(t, id(root), identities[0].ID)
	require.NotZero(t, identities[0].ExpiresAtUnix)
	require.Equal(t, cachefact.TermUseReused, identities[0].TermUse)
	require.Len(t, factsOfKind[cachefact.Retention](hitFacts), 1)
	requireFactsMatchSnapshot(t, c, rec)

	// A request carrying an extra digest teaches it onto the leaf.
	extra := call.ExtraDigest{Digest: digest.FromString("facts-extra"), Label: call.ExtraDigestLabelRemoteCache}
	before = rec.lastSeq()
	extraHit := publish(session, &CallRequest{ResultCall: cacheTestIntCall("leaf", extra)}, 99)
	require.True(t, extraHit.HitCache())
	identities = factsOfKind[cachefact.Identity](rec.since(before))
	require.Len(t, identities, 1)
	require.Contains(t, identities[0].Digests, cachefact.Digest{Digest: extra.Digest.String(), Label: call.ExtraDigestLabelRemoteCache})
	requireFactsMatchSnapshot(t, c, rec)

	// A plain recipe hit takes the fast path and emits nothing.
	before = rec.lastSeq()
	require.True(t, publish(session, &CallRequest{ResultCall: cacheTestIntCall("leaf")}, 99).HitCache())
	require.Empty(t, rec.since(before))

	// A content digest learned later.
	content := digest.FromString("facts-content")
	before = rec.lastSeq()
	require.NoError(t, c.TeachContentDigest(ctx, leaf, content))
	identities = factsOfKind[cachefact.Identity](rec.since(before))
	require.Len(t, identities, 1)
	require.Contains(t, identities[0].Digests, cachefact.Digest{Digest: content.String(), Label: call.ExtraDigestLabelContent})
	requireFactsMatchSnapshot(t, c, rec)

	// An explicit dependency after publication announces the grown set.
	extraDep := publish(session, &CallRequest{ResultCall: cacheTestIntCall("explicit-dep"), IsPersistable: true}, 3)
	before = rec.lastSeq()
	require.NoError(t, c.AddExplicitDependency(ctx, root, extraDep, "test"))
	deps := factsOfKind[cachefact.Deps](rec.since(before))
	require.Len(t, deps, 1)
	require.Equal(t, id(root), deps[0].ID)
	require.ElementsMatch(t, []uint64{id(leaf), id(extraDep)}, deps[0].Deps)
	requireFactsMatchSnapshot(t, c, rec)

	// A result only a session owns leaves with the session.
	scratch := publish("scratch-session", &CallRequest{ResultCall: cacheTestIntCall("scratch")}, 4)
	before = rec.lastSeq()
	require.NoError(t, c.ReleaseSession(ctx, "scratch-session"))
	require.Equal(t, []cachefact.Removed{{IDs: []uint64{id(scratch)}, Reason: cachefact.RemovedSessionRelease}}, factsOfKind[cachefact.Removed](rec.since(before)))
	requireFactsMatchSnapshot(t, c, rec)

	// A prune drops the root's edge; the session still holds the root.
	before = rec.lastSeq()
	removed, err := c.removePersistedEdge(ctx, sharedResultID(id(root)))
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, []cachefact.Kind{cachefact.KindRetention}, factKinds(rec.since(before)))
	require.False(t, factsOfKind[cachefact.Retention](rec.since(before))[0].Retained)
	requireFactsMatchSnapshot(t, c, rec)

	// Releasing the session now collects the root, and nothing retained.
	before = rec.lastSeq()
	require.NoError(t, c.ReleaseSession(ctx, session))
	require.Equal(t, []cachefact.Removed{{IDs: []uint64{id(root)}, Reason: cachefact.RemovedSessionRelease}}, factsOfKind[cachefact.Removed](rec.since(before)))
	model := requireFactsMatchSnapshot(t, c, rec)
	require.Len(t, model.rows, 2, "the retained leaf and explicit dependency remain")

	// A prune of an edge that is the last owner removes the result.
	before = rec.lastSeq()
	removed, err = c.removePersistedEdge(ctx, sharedResultID(id(leaf)))
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, []cachefact.Kind{cachefact.KindRetention, cachefact.KindRemoved}, factKinds(rec.since(before)))
	require.Equal(t, cachefact.Removed{IDs: []uint64{id(leaf)}, Reason: cachefact.RemovedPrune}, factsOfKind[cachefact.Removed](rec.since(before))[0])
	requireFactsMatchSnapshot(t, c, rec)
}

// A call whose value carries its own, different frame is posted under both
// the request's and the response's digests, with both terms.
func TestCacheFactsRequestAndResponseAliases(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	rec := newFactRecorder(t)
	c, err := NewCache(ctx, "", nil, nil, WithFactSink(rec))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })

	request := cacheTestIntCall("alias-request")
	response := cacheTestIntCall("alias-response")
	res, err := c.GetOrInitCall(ctx, "s", noopTypeResolver{}, &CallRequest{ResultCall: request, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(response, 5), nil
	})
	require.NoError(t, err)
	results := factsOfKind[cachefact.Result](rec.all())
	require.Len(t, results, 1)
	require.Equal(t, uint64(res.cacheSharedResult().id), results[0].ID)
	require.ElementsMatch(t, []cachefact.Digest{
		{Digest: cacheTestCallDigest(request).String(), Label: cachefact.LabelRecipe},
		{Digest: cacheTestCallDigest(response).String(), Label: cachefact.LabelRecipe},
	}, results[0].Digests)
	require.Len(t, results[0].Terms, 2)
	requireFactsMatchSnapshot(t, c, rec)
}

func TestCacheFactsFailedPublication(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	rec := newFactRecorder(t)
	c, err := NewCache(ctx, "", nil, nil, WithFactSink(rec))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })

	frame := cacheTestIntCall("attachment-fails")
	attachErr := errors.New("attachment failed")
	_, err = c.GetOrInitCall(ctx, "failing", noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return cacheTestDetachedResult(frame, cacheTestLeaseCheckedInt{Int: NewInt(1), onAttach: func(context.Context) error { return attachErr }}), nil
	})
	require.ErrorIs(t, err, attachErr)

	facts := rec.all()
	require.Equal(t, []cachefact.Kind{cachefact.KindResult, cachefact.KindRemoved}, factKinds(facts), "a failed publication is retracted and never announces dependencies")
	removed := factsOfKind[cachefact.Removed](facts)[0]
	require.Equal(t, cachefact.RemovedRollback, removed.Reason)
	require.Equal(t, []uint64{factsOfKind[cachefact.Result](facts)[0].ID}, removed.IDs)
	requireFactsMatchSnapshot(t, c, rec)
}

func TestCacheFactsImport(t *testing.T) {
	t.Parallel()
	ctx, a, srv := transferTestCache(t)
	leaf := persistedListTestResult(t, ctx, a, srv, "leaf", String("value"))
	root := persistedListTestResult(t, ctx, a, srv, "root", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{leaf}})
	bundle := exportTestBundle(t, ctx, a, root)

	ctx, b, _ := transferTestCache(t)
	rec := attachFactRecorder(t, b)
	mapping, err := b.ImportValues(ctx, bundle)
	require.NoError(t, err)
	require.Len(t, mapping, 1)

	facts := rec.all()
	require.Equal(t, []cachefact.Kind{cachefact.KindResult, cachefact.KindResult}, factKinds(facts), "one complete fact per imported row and no intermediate facts")
	results := factsOfKind[cachefact.Result](facts)
	for _, res := range results {
		require.Equal(t, cachefact.OriginImported, res.Origin)
		require.Len(t, res.Terms, 1)
		require.NotEmpty(t, res.Digests)
	}
	rootFact := results[1]
	require.Equal(t, mapping[0].ResultID, rootFact.ID, "dependencies are announced first")
	require.True(t, rootFact.Retained)
	require.Equal(t, []uint64{results[0].ID}, rootFact.Deps)
	require.False(t, results[0].Retained)
	requireFactsMatchSnapshot(t, b, rec)
}

func TestCacheFactsBootRestore(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cache.db")
	open := func(rec *factRecorder) (context.Context, *Cache, *Server) {
		t.Helper()
		ctx := cacheTestContext(t.Context())
		c, err := NewCache(ctx, path, nil, nil, WithFactSink(rec), WithEngineInstanceID("instance-"+t.Name()))
		require.NoError(t, err)
		srv := newDagqlServerForTest(t, &persistCodecRoot{})
		return srvToContext(ContextWithCache(ctx, c), srv), c, srv
	}

	firstRec := newFactRecorder(t)
	ctx, first, srv := open(firstRec)
	require.Empty(t, firstRec.all(), "an empty store restores nothing")
	require.Zero(t, first.BootRestoredResults())
	leaf := persistedListTestResult(t, ctx, first, srv, "leaf", String("saved"))
	persistedListTestResult(t, ctx, first, srv, "root", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{leaf}})
	require.NoError(t, first.TeachContentDigest(ctx, leaf, digest.FromString("restored-content")))
	requireFactsMatchSnapshot(t, first, firstRec)
	require.NoError(t, first.ReleaseSession(ctx, "test-session"))
	firstSnap := cacheFactsDebugSnapshot(t, first)
	require.NoError(t, first.Close(ctx))

	rec := newFactRecorder(t)
	_, restored, _ := open(rec)
	t.Cleanup(func() { require.NoError(t, restored.CloseDiscardingPersistence()) })
	require.Equal(t, CachePersistenceResetNone, restored.PersistenceResetReason())
	require.Equal(t, len(firstSnap.Results), restored.BootRestoredResults())

	facts := rec.all()
	kinds := factKinds(facts)
	firstTerm := slices.Index(kinds, cachefact.KindTerm)
	firstResult := slices.Index(kinds, cachefact.KindResult)
	require.Positive(t, firstTerm, "classes precede terms")
	require.Greater(t, firstResult, firstTerm, "terms precede results")
	require.Equal(t, cachefact.KindClass, kinds[0])
	for i, kind := range kinds {
		switch {
		case i < firstTerm:
			require.Equal(t, cachefact.KindClass, kind)
		case i < firstResult:
			require.Equal(t, cachefact.KindTerm, kind)
		default:
			require.Equal(t, cachefact.KindResult, kind)
		}
	}
	results := factsOfKind[cachefact.Result](facts)
	require.Len(t, results, len(firstSnap.Results))
	var restoredIDs, firstIDs []uint64
	for _, res := range results {
		require.Equal(t, cachefact.OriginRestored, res.Origin)
		require.Empty(t, res.Terms)
		restoredIDs = append(restoredIDs, res.ID)
	}
	for _, res := range firstSnap.Results {
		firstIDs = append(firstIDs, res.SharedResultID)
	}
	require.ElementsMatch(t, firstIDs, restoredIDs, "restored rows keep their numbers")
	requireFactsMatchSnapshot(t, restored, rec)
}

func TestCacheFactsPartCompletion(t *testing.T) {
	t.Parallel()
	ctx, c, srv, _ := shareTestCache(t)
	rec := attachFactRecorder(t, c)
	barrier := newSharePassBarrier(c)
	donor := persistedListTestResult(t, ctx, c, srv, "donor", &transferTestValue{Text: "snapshot", links: []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "donor-snapshot"}}})
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	shareTestEncodedReceiver(t, ctx, c, receiver)
	shareTestUnite(t, ctx, c, "facts-part", donor, receiver)
	require.Equal(t, 1, barrier.awaitPass(t))
	require.True(t, shareTestHasLink(receiver, "donor-snapshot"))

	snap := cacheFactsDebugSnapshot(t, c)
	facts := rec.all()
	parts := factsOfKind[cachefact.Part](facts)
	require.Equal(t, []cachefact.Part{{ID: uint64(receiver.cacheSharedResult().id), OutputPath: "", Part: "snapshot", State: cachefact.PartStateCompleted}}, parts)
	model := replayFacts(t, facts)
	require.LessOrEqual(t, model.lastSeq, snap.FactSeq)
	require.Equal(t, []string{"/snapshot"}, model.rows[uint64(receiver.cacheSharedResult().id)].parts)
}

func TestCacheFactsDisabledCostsNothing(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	frame := cacheTestIntCall("no-sink")
	res, err := c.GetOrInitCall(ctx, "s", noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frame, 1), nil
	})
	require.NoError(t, err)
	require.False(t, res.cacheSharedResult().factAnnounced)
	snap := cacheFactsDebugSnapshot(t, c)
	require.Zero(t, snap.FactSeq)
	require.Empty(t, snap.EngineInstance)
}

type discardFactSink struct{}

func (discardFactSink) Emit(cachefact.Fact) {}

// BenchmarkCacheFactsRegister measures registering one computed persistable
// result, with and without a fact sink.
func BenchmarkCacheFactsRegister(b *testing.B) {
	for _, withSink := range []bool{false, true} {
		name := "no-sink"
		if withSink {
			name = "sink"
		}
		b.Run(name, func(b *testing.B) {
			ctx := cacheTestContext(b.Context())
			var opts []CacheOption
			if withSink {
				opts = append(opts, WithFactSink(discardFactSink{}))
			}
			c, err := NewCache(ctx, "", nil, nil, opts...)
			require.NoError(b, err)
			b.Cleanup(func() { require.NoError(b, c.CloseDiscardingPersistence()) })
			start := time.Now()
			b.ResetTimer()
			for i := range b.N {
				frame := cacheTestIntCall("bench-" + start.Format(time.RFC3339Nano) + "-" + string(rune('a'+i%26)) + "-" + time.Duration(i).String())
				if _, err := c.GetOrInitCall(ctx, "bench", noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
					return cacheTestIntResult(frame, i), nil
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
