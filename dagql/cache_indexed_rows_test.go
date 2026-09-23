package dagql

import (
	"context"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/dagger/dagger/dagql/cachefact"
	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// applyCloudFact applies one engine's fact to a cache of indexed rows, as the
// Cloud service does. State facts that the service keeps beside the cache
// (retention, parts, engine lifecycle) change nothing here.
func applyCloudFact(t *testing.T, cloud *Cache, engine string, f cachefact.Fact) {
	t.Helper()
	ctx := t.Context()
	switch body := f.Body.(type) {
	case cachefact.Result:
		_, err := cloud.UpsertIndexedRow(ctx, engine, body)
		require.NoError(t, err)
	case cachefact.Identity:
		require.NoError(t, cloud.TeachIndexedRowIdentity(ctx, engine, body))
	case cachefact.Deps:
		unknown, err := cloud.SetIndexedRowDeps(ctx, engine, body)
		require.NoError(t, err)
		require.Empty(t, unknown, "a deps fact names only announced rows")
	case cachefact.Class:
		require.NoError(t, cloud.UpsertIndexedClass(ctx, body))
	case cachefact.TermFact:
		require.NoError(t, cloud.UpsertIndexedTerm(ctx, body))
	case cachefact.Removed:
		for _, id := range body.IDs {
			require.NoError(t, cloud.RemoveIndexedRow(ctx, RowKey{Engine: engine, ID: id}))
		}
	}
}

func newCloudCache(t *testing.T) *Cache {
	t.Helper()
	cloud, err := NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cloud.CloseDiscardingPersistence()) })
	return cloud
}

func replayOntoCloud(t *testing.T, cloud *Cache, engine string, facts []cachefact.Fact) {
	t.Helper()
	for _, f := range facts {
		applyCloudFact(t, cloud, engine, f)
	}
}

// rowView is what the round trip compares for one row.
type rowView struct {
	classes      string // canonical set of output classes, as an opaque key
	classDigests []string
	postings     []string
	deps         []uint64
	// termSelfs are the self digests of the terms producing the row's
	// classes, one per term.
	termSelfs []string
}

// sourceRowViews describes an engine cache's own results by result number.
func sourceRowViews(c *Cache) map[uint64]rowView {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	out := map[uint64]rowView{}
	for id, res := range c.resultsByID {
		if res == nil {
			continue
		}
		view := rowViewLocked(c, id)
		for dep := range res.deps {
			view.deps = append(view.deps, uint64(dep))
		}
		slices.Sort(view.deps)
		out[uint64(id)] = view
	}
	return out
}

// cloudRowViews describes one engine's rows in a cache of indexed rows, by
// that engine's result numbers.
func cloudRowViews(c *Cache, engine string) map[uint64]rowView {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	out := map[uint64]rowView{}
	for key, id := range c.indexedRows {
		if key.Engine != engine {
			continue
		}
		res := c.resultsByID[id]
		view := rowViewLocked(c, id)
		for dep := range res.deps {
			if depRes := c.resultsByID[dep]; depRes != nil && depRes.indexed != nil {
				view.deps = append(view.deps, depRes.indexed.key.ID)
			}
		}
		slices.Sort(view.deps)
		out[key.ID] = view
	}
	return out
}

func rowViewLocked(c *Cache, id sharedResultID) rowView {
	var view rowView
	var roots []int
	digests := map[string]struct{}{}
	for classID := range c.outputEqClassesForResultLocked(id) {
		roots = append(roots, int(classID))
		for dig := range c.eqClassToDigests[classID] {
			digests[dig] = struct{}{}
		}
		for termID := range c.outputEqClassToTerms[classID] {
			if term := c.egraphTerms[termID]; term != nil {
				view.termSelfs = append(view.termSelfs, term.selfDigest.String())
			}
		}
	}
	slices.Sort(view.termSelfs)
	slices.Sort(roots)
	parts := make([]string, len(roots))
	for i, r := range roots {
		parts[i] = strconv.Itoa(r)
	}
	view.classes = strings.Join(parts, ",")
	view.classDigests = sortedKeys(digests)
	postings := map[string]struct{}{}
	for _, dig := range c.resultIndexedDigests[id] {
		postings[dig] = struct{}{}
	}
	if _, broad := c.broadlyIndexedResults[id]; broad {
		for dig := range digests {
			postings[dig] = struct{}{}
		}
	}
	view.postings = sortedKeys(postings)
	return view
}

// partition groups row numbers by their classes.
func partition(views map[uint64]rowView) [][]uint64 {
	groups := map[string][]uint64{}
	for id, view := range views {
		groups[view.classes] = append(groups[view.classes], id)
	}
	var out [][]uint64
	for _, ids := range groups {
		slices.Sort(ids)
		out = append(out, ids)
	}
	slices.SortFunc(out, func(a, b []uint64) int { return int(a[0]) - int(b[0]) })
	return out
}

// requireRoundTrip requires that the indexed rows built from one engine's
// facts alone match the engine's own cache: the same rows, the same
// partition of rows into classes, the same digests per class, the same terms
// per class, the same digest postings and the same dependency edges.
func requireRoundTrip(t *testing.T, source, cloud *Cache, engine string) {
	t.Helper()
	src := sourceRowViews(source)
	got := cloudRowViews(cloud, engine)
	require.ElementsMatch(t, keysOf(src), keysOf(got), "the same rows")
	require.Equal(t, partition(src), partition(got), "the same partition into classes")
	for id, want := range src {
		have := got[id]
		require.Equal(t, want.classDigests, have.classDigests, "class digests of row %d", id)
		require.Equal(t, want.postings, have.postings, "postings of row %d", id)
		require.Equal(t, want.deps, have.deps, "dependencies of row %d", id)
		require.Equal(t, want.termSelfs, have.termSelfs, "terms producing the classes of row %d", id)
	}
}

func keysOf[V any](m map[uint64]V) []uint64 {
	out := make([]uint64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

type factSource struct {
	ctx   context.Context
	cache *Cache
	rec   *factRecorder
}

func newFactSource(t *testing.T) *factSource {
	t.Helper()
	ctx := cacheTestContext(t.Context())
	rec := newFactRecorder(t)
	c, err := NewCache(ctx, "", nil, nil, WithFactSink(rec))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	return &factSource{ctx: ctx, cache: c, rec: rec}
}

func (s *factSource) publish(t *testing.T, session string, req *CallRequest, value AnyResult) AnyResult {
	t.Helper()
	res, err := s.cache.GetOrInitCall(s.ctx, session, noopTypeResolver{}, req, func(context.Context) (AnyResult, error) {
		return value, nil
	})
	require.NoError(t, err)
	return res
}

func (s *factSource) int(t *testing.T, field string, v int) AnyResult {
	t.Helper()
	frame := cacheTestIntCall(field)
	return s.publish(t, "s", &CallRequest{ResultCall: frame, IsPersistable: true}, cacheTestIntResult(frame, v))
}

func (s *factSource) recipe(t *testing.T, frame *ResultCall) string {
	t.Helper()
	dig, err := frame.deriveRecipeDigest(s.cache)
	require.NoError(t, err)
	return dig.String()
}

func receiverCall(field string, receiver AnyResult) *ResultCall {
	return &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(Int(0).Type()), Receiver: &ResultCallRef{ResultID: uint64(receiver.cacheSharedResult().id)}}
}

// The computed-result case: dependencies, hits that teach and lower expiry,
// extra digests, a content teach, an explicit dependency and removals.
func TestIndexedRowsRoundTripComputed(t *testing.T) {
	t.Parallel()
	s := newFactSource(t)
	leaf := s.int(t, "leaf", 1)
	rootFrame := receiverCall("root", leaf)
	root := s.publish(t, "s", &CallRequest{ResultCall: rootFrame, IsPersistable: true}, cacheTestIntResult(rootFrame, 2))
	s.publish(t, "s", &CallRequest{ResultCall: rootFrame.clone(), IsPersistable: true, TTL: 3600}, cacheTestIntResult(rootFrame, 99))
	extra := call.ExtraDigest{Digest: digest.FromString("round-trip-extra"), Label: call.ExtraDigestLabelRemoteCache}
	s.publish(t, "s", &CallRequest{ResultCall: cacheTestIntCall("leaf", extra)}, cacheTestIntResult(cacheTestIntCall("leaf"), 99))
	require.NoError(t, s.cache.TeachContentDigest(s.ctx, leaf, digest.FromString("round-trip-content"), call.ExtraDigestLabelRemoteCache))
	extraDep := s.int(t, "explicit-dep", 3)
	require.NoError(t, s.cache.AddExplicitDependency(s.ctx, root, extraDep, "test"))
	s.publish(t, "scratch", &CallRequest{ResultCall: cacheTestIntCall("scratch")}, cacheTestIntResult(cacheTestIntCall("scratch"), 4))
	require.NoError(t, s.cache.ReleaseSession(s.ctx, "scratch"))

	cloud := newCloudCache(t)
	replayOntoCloud(t, cloud, "engine-a", s.rec.all())
	requireRoundTrip(t, s.cache, cloud, "engine-a")

	rootKey := RowKey{Engine: "engine-a", ID: uint64(root.cacheSharedResult().id)}
	leafKey := RowKey{Engine: "engine-a", ID: uint64(leaf.cacheSharedResult().id)}
	depKey := RowKey{Engine: "engine-a", ID: uint64(extraDep.cacheSharedResult().id)}
	require.Equal(t, []RowKey{rootKey}, cloud.EquivalentRows(s.recipe(t, rootFrame)))
	require.Equal(t, []RowKey{leafKey}, cloud.EquivalentRows(extra.Digest.String()))
	require.ElementsMatch(t, []RowKey{rootKey, leafKey, depKey}, cloud.RowClosure(rootKey))
	info, ok := cloud.RowInfo(rootKey)
	require.True(t, ok)
	require.Equal(t, "root", info.Field)
	require.Equal(t, "Int", info.TypeName)
	require.ElementsMatch(t, []RowKey{leafKey, depKey}, info.Deps)
	require.NotZero(t, info.ExpiresAtUnix, "the TTL hit lowered the root's own expiry")
	require.Contains(t, info.Digests, s.recipe(t, rootFrame))

	// The indexed rows are never cache hits.
	res, err := cloud.GetOrInitCall(cacheTestContext(t.Context()), "cloud", noopTypeResolver{}, &CallRequest{ResultCall: cacheTestIntCall("leaf")}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(cacheTestIntCall("leaf"), 7), nil
	})
	require.NoError(t, err)
	require.False(t, res.HitCache())
	require.Equal(t, 7, cacheTestUnwrapInt(t, res))
}

// A result posted under its request's and a different response's recipe
// digests, with both terms.
func TestIndexedRowsRoundTripAliases(t *testing.T) {
	t.Parallel()
	s := newFactSource(t)
	request := cacheTestIntCall("alias-request")
	response := cacheTestIntCall("alias-response")
	s.publish(t, "s", &CallRequest{ResultCall: request, IsPersistable: true}, cacheTestIntResult(response, 5))
	cloud := newCloudCache(t)
	replayOntoCloud(t, cloud, "engine-a", s.rec.all())
	requireRoundTrip(t, s.cache, cloud, "engine-a")
	require.Equal(t, cloud.EquivalentRows(cacheTestCallDigest(request).String()), cloud.EquivalentRows(cacheTestCallDigest(response).String()))
}

// A content teach reuses the result's own term; teaching another result's
// call onto a result associates it with that existing congruent term and
// merges the two classes. Replaying the identity facts reproduces both.
func TestIndexedRowsRoundTripTeaches(t *testing.T) {
	t.Parallel()
	s := newFactSource(t)
	leaf := s.int(t, "teach-leaf", 1)
	require.NoError(t, s.cache.TeachContentDigest(s.ctx, leaf, digest.FromString("teach-content")))
	first := s.int(t, "first", 2)
	second := s.int(t, "second", 3)
	require.NoError(t, s.cache.TeachCallEquivalentToResult(s.ctx, "s", cacheTestIntCall("first"), second))

	uses := map[cachefact.TermUse]int{}
	for _, identity := range factsOfKind[cachefact.Identity](s.rec.all()) {
		uses[identity.TermUse]++
	}
	require.Positive(t, uses[cachefact.TermUseReused], "the content teach reused its term")
	require.Positive(t, uses[cachefact.TermUseAssociated], "the call teach associated an existing term")

	cloud := newCloudCache(t)
	replayOntoCloud(t, cloud, "engine-a", s.rec.all())
	requireRoundTrip(t, s.cache, cloud, "engine-a")
	firstKey := RowKey{Engine: "engine-a", ID: uint64(first.cacheSharedResult().id)}
	secondKey := RowKey{Engine: "engine-a", ID: uint64(second.cacheSharedResult().id)}
	require.ElementsMatch(t, []RowKey{firstKey, secondKey}, cloud.EquivalentRows(cacheTestCallDigest(cacheTestIntCall("first")).String()))
}

// A consumer built from a restarted engine's boot facts alone matches the
// restored engine.
func TestIndexedRowsRoundTripBoot(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cache.db")
	open := func(rec *factRecorder) (context.Context, *Cache, *Server) {
		t.Helper()
		ctx := cacheTestContext(t.Context())
		c, err := NewCache(ctx, path, nil, nil, WithFactSink(rec))
		require.NoError(t, err)
		srv := newDagqlServerForTest(t, &persistCodecRoot{})
		return srvToContext(ContextWithCache(ctx, c), srv), c, srv
	}
	ctx, first, srv := open(newFactRecorder(t))
	leaf := persistedListTestResult(t, ctx, first, srv, "leaf", String("saved"))
	persistedListTestResult(t, ctx, first, srv, "root", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{leaf}})
	require.NoError(t, first.TeachContentDigest(ctx, leaf, digest.FromString("boot-content"), call.ExtraDigestLabelRemoteCache))
	require.NoError(t, first.ReleaseSession(ctx, "test-session"))
	require.NoError(t, first.Close(ctx))

	rec := newFactRecorder(t)
	_, restored, _ := open(rec)
	t.Cleanup(func() { require.NoError(t, restored.CloseDiscardingPersistence()) })
	require.Positive(t, restored.BootRestoredResults())

	cloud := newCloudCache(t)
	replayOntoCloud(t, cloud, "engine-restarted", rec.all())
	requireRoundTrip(t, restored, cloud, "engine-restarted")
}

// Two engines' facts, interleaved in any order across engines (each engine's
// own facts stay in sequence order) and with duplicates, give the same rows
// and the same partition.
func TestIndexedRowsOrderIndependence(t *testing.T) {
	t.Parallel()
	a, b, _ := refinementSources(t)
	factsA, factsB := a.rec.all(), b.rec.all()

	reference := newCloudCache(t)
	replayOntoCloud(t, reference, "engine-a", factsA)
	replayOntoCloud(t, reference, "engine-b", factsB)
	want := map[string][][]uint64{"engine-a": partition(cloudRowViews(reference, "engine-a")), "engine-b": partition(cloudRowViews(reference, "engine-b"))}
	wantCross := crossEnginePartition(reference)

	for seed := range uint64(8) {
		rng := rand.New(rand.NewPCG(seed, seed+1))
		cloud := newCloudCache(t)
		ia, ib := 0, 0
		for ia < len(factsA) || ib < len(factsB) {
			engine, facts, i := "engine-a", factsA, &ia
			if ia == len(factsA) || (ib < len(factsB) && rng.IntN(2) == 1) {
				engine, facts, i = "engine-b", factsB, &ib
			}
			applyCloudFact(t, cloud, engine, facts[*i])
			if rng.IntN(3) == 0 {
				applyCloudFact(t, cloud, engine, facts[*i]) // a duplicate
			}
			*i++
		}
		for engine, partitionWant := range want {
			require.Equal(t, partitionWant, partition(cloudRowViews(cloud, engine)), "seed %d, %s", seed, engine)
		}
		require.Equal(t, wantCross, crossEnginePartition(cloud), "seed %d", seed)
	}
}

// crossEnginePartition groups every row of every engine by class.
func crossEnginePartition(c *Cache) [][]string {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	groups := map[string][]string{}
	for key, id := range c.indexedRows {
		view := rowViewLocked(c, id)
		groups[view.classes] = append(groups[view.classes], key.Engine+"/"+strconv.FormatUint(key.ID, 10))
	}
	var out [][]string
	for _, g := range groups {
		slices.Sort(g)
		out = append(out, g)
	}
	slices.SortFunc(out, func(a, b []string) int { return strings.Compare(a[0], b[0]) })
	return out
}

// refinementSources builds engine A with f(x) and f(y) over distinct inputs
// x and y, and engine B, which learns that x and y are the same result.
func refinementSources(t *testing.T) (*factSource, *factSource, string) {
	t.Helper()
	a := newFactSource(t)
	x := a.int(t, "x", 1)
	y := a.int(t, "y", 2)
	fx := receiverCall("f", x)
	fy := receiverCall("f", y)
	a.publish(t, "s", &CallRequest{ResultCall: fx, IsPersistable: true}, cacheTestIntResult(fx, 3))
	a.publish(t, "s", &CallRequest{ResultCall: fy, IsPersistable: true}, cacheTestIntResult(fy, 4))

	b := newFactSource(t)
	b.publish(t, "s", &CallRequest{ResultCall: cacheTestIntCall("x"), IsPersistable: true}, cacheTestIntResult(cacheTestIntCall("y"), 5))
	return a, b, a.recipe(t, fx)
}

// Engine B's equality of x and y merges engine A's f(x) and f(y) in the
// combined cache, with no B row in that class; every class of A alone is
// contained in one combined class.
func TestIndexedRowsRefinementAcrossEngines(t *testing.T) {
	t.Parallel()
	a, b, fxDigest := refinementSources(t)

	alone := newCloudCache(t)
	replayOntoCloud(t, alone, "engine-a", a.rec.all())
	requireRoundTrip(t, a.cache, alone, "engine-a")

	combined := newCloudCache(t)
	replayOntoCloud(t, combined, "engine-a", a.rec.all())
	replayOntoCloud(t, combined, "engine-b", b.rec.all())

	aloneViews, combinedViews := cloudRowViews(alone, "engine-a"), cloudRowViews(combined, "engine-a")
	classOf := func(views map[uint64]rowView) map[string][]uint64 {
		out := map[string][]uint64{}
		for id, v := range views {
			out[v.classes] = append(out[v.classes], id)
		}
		return out
	}
	for _, ids := range classOf(aloneViews) {
		first := combinedViews[ids[0]].classes
		for _, id := range ids {
			require.Equal(t, first, combinedViews[id].classes, "an engine A class stays in one combined class")
		}
	}
	require.Less(t, len(classOf(combinedViews)), len(classOf(aloneViews)), "engine B's equality merged engine A's classes")

	merged := combined.EquivalentRows(fxDigest)
	require.Len(t, merged, 2, "f(x) and f(y) are one class")
	for _, key := range merged {
		require.Equal(t, "engine-a", key.Engine, "with no engine B row in it")
	}
}

// A removed row leaves when nothing depends on it; a row a dependent still
// holds stays, marked removed, until the dependent goes.
func TestIndexedRowsRemovalFollowsOwnership(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	ctx := t.Context()
	leaf := cachefact.Result{ID: 1, Origin: cachefact.OriginComputed, Field: "leaf", Digests: []cachefact.Digest{{Digest: "xxh3:leaf", Label: cachefact.LabelRecipe}}, Terms: []cachefact.Term{{Self: "xxh3:leaf-self"}}}
	root := cachefact.Result{ID: 2, Origin: cachefact.OriginComputed, Field: "root", Digests: []cachefact.Digest{{Digest: "xxh3:root", Label: cachefact.LabelRecipe}}, Terms: []cachefact.Term{{Self: "xxh3:root-self", Inputs: []cachefact.TermInput{{Digest: "xxh3:leaf", Provenance: cachefact.ProvenanceResult}}}}}
	for _, fact := range []cachefact.Result{leaf, root} {
		_, err := cloud.UpsertIndexedRow(ctx, "e", fact)
		require.NoError(t, err)
	}
	unknown, err := cloud.SetIndexedRowDeps(ctx, "e", cachefact.Deps{ID: 2, Deps: []uint64{1, 99}, Complete: true})
	require.NoError(t, err)
	require.Equal(t, []uint64{99}, unknown, "a dependency the cache does not hold is reported")

	require.NoError(t, cloud.RemoveIndexedRow(ctx, RowKey{Engine: "e", ID: 1}))
	info, ok := cloud.RowInfo(RowKey{Engine: "e", ID: 1})
	require.True(t, ok, "the root still holds the leaf")
	require.True(t, info.Removed)
	require.NoError(t, cloud.RemoveIndexedRow(ctx, RowKey{Engine: "e", ID: 1}), "removing twice is a no-op")

	require.NoError(t, cloud.RemoveIndexedRow(ctx, RowKey{Engine: "e", ID: 2}))
	for _, id := range []uint64{1, 2} {
		_, ok := cloud.RowInfo(RowKey{Engine: "e", ID: id})
		require.False(t, ok, "row %d is collected", id)
	}
	require.Empty(t, cloud.EquivalentRows("xxh3:root"))
	require.ErrorIs(t, cloud.TeachIndexedRowIdentity(ctx, "e", cachefact.Identity{ID: 2}), ErrUnknownIndexedRow)
}

func rowFact(id uint64, recipe string, expiresAtUnix int64) cachefact.Result {
	return cachefact.Result{
		ID: id, Origin: cachefact.OriginComputed, Field: "f", ExpiresAtUnix: expiresAtUnix,
		Digests: []cachefact.Digest{{Digest: recipe, Label: cachefact.LabelRecipe}},
		Terms:   []cachefact.Term{{Self: recipe + "-self"}},
	}
}

// A row stays until its own engine removes it: another engine's removal
// never takes an expired row or a restored row without terms with it, even
// when it removes the cache's last live term.
func TestIndexedRowsSurviveOtherEnginesRemovals(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cloud := newCloudCache(t)
	expired := rowFact(1, "xxh3:shared", 1) // expired long ago
	_, err := cloud.UpsertIndexedRow(ctx, "engine-a", expired)
	require.NoError(t, err)
	_, err = cloud.UpsertIndexedRow(ctx, "engine-b", rowFact(1, "xxh3:shared", 0))
	require.NoError(t, err)
	_, err = cloud.UpsertIndexedRow(ctx, "engine-r", cachefact.Result{
		ID: 7, Origin: cachefact.OriginRestored, Field: "restored",
		Digests: []cachefact.Digest{{Digest: "xxh3:restored"}},
	})
	require.NoError(t, err)

	a := RowKey{Engine: "engine-a", ID: 1}
	r := RowKey{Engine: "engine-r", ID: 7}
	require.NoError(t, cloud.RemoveIndexedRow(ctx, RowKey{Engine: "engine-b", ID: 1}))
	info, ok := cloud.RowInfo(a)
	require.True(t, ok, "engine B's removal leaves engine A's expired row")
	require.False(t, info.Removed)
	require.Equal(t, []string{"xxh3:shared"}, info.ClassDigests)
	require.Equal(t, []RowKey{a}, cloud.EquivalentRows("xxh3:shared"))
	_, ok = cloud.RowInfo(r)
	require.True(t, ok, "and the restored row")

	require.NoError(t, cloud.RemoveIndexedRow(ctx, a))
	_, ok = cloud.RowInfo(a)
	require.False(t, ok, "engine A's own removal takes it")
	require.Empty(t, cloud.EquivalentRows("xxh3:shared"))
	info, ok = cloud.RowInfo(r)
	require.True(t, ok, "the restored row survives the last term's removal")
	require.Equal(t, []string{"xxh3:restored"}, info.ClassDigests)
	require.Equal(t, []RowKey{r}, cloud.EquivalentRows("xxh3:restored"))

	require.NoError(t, cloud.RemoveIndexedRow(ctx, r))
	_, ok = cloud.RowInfo(r)
	require.False(t, ok)
	_, err = cloud.UpsertIndexedRow(ctx, "engine-a", rowFact(2, "xxh3:after", 0))
	require.NoError(t, err, "the emptied cache takes new rows")
	require.Equal(t, []RowKey{{Engine: "engine-a", ID: 2}}, cloud.EquivalentRows("xxh3:after"))
}

// Removals of two engines' rows, interleaved in any order, leave exactly the
// rows not removed, each still in its class.
func TestIndexedRowsRemovalInterleavings(t *testing.T) {
	t.Parallel()
	a, b, _ := refinementSources(t)
	factsA, factsB := a.rec.all(), b.rec.all()
	var removals []RowKey
	survivors := map[RowKey]bool{}
	for i, res := range factsOfKind[cachefact.Result](factsA) {
		key := RowKey{Engine: "engine-a", ID: res.ID}
		if i%2 == 0 {
			removals = append(removals, key)
		} else {
			survivors[key] = true
		}
	}
	for _, res := range factsOfKind[cachefact.Result](factsB) {
		removals = append(removals, RowKey{Engine: "engine-b", ID: res.ID})
	}
	require.NotEmpty(t, survivors)

	for seed := range uint64(8) {
		cloud := newCloudCache(t)
		replayOntoCloud(t, cloud, "engine-a", factsA)
		replayOntoCloud(t, cloud, "engine-b", factsB)
		order := slices.Clone(removals)
		rand.New(rand.NewPCG(seed, seed+7)).Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		for _, key := range order {
			require.NoError(t, cloud.RemoveIndexedRow(t.Context(), key))
		}
		cloud.egraphMu.Lock()
		held := map[RowKey]bool{}
		for key, id := range cloud.indexedRows {
			if res := cloud.resultsByID[id]; res != nil && !res.indexed.removed {
				held[key] = true
			}
		}
		cloud.egraphMu.Unlock()
		require.Equal(t, survivors, held, "seed %d", seed)
		for key := range survivors {
			info, ok := cloud.RowInfo(key)
			require.True(t, ok)
			require.NotEmpty(t, info.ClassDigests, "seed %d: %v keeps its class", seed, key)
			for _, dig := range info.Digests {
				require.Contains(t, cloud.EquivalentRows(dig), key)
			}
		}
	}
}

// An indexed row's number never loads a value, a call frame or a schema
// module, with or without a session.
func TestIndexedRowsNeverLoadByResultID(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cloud := newCloudCache(t)
	_, err := cloud.UpsertIndexedRow(ctx, "engine-a", rowFact(1, "xxh3:load", 0))
	require.NoError(t, err)
	cloud.egraphMu.RLock()
	id := uint64(cloud.indexedRows[RowKey{Engine: "engine-a", ID: 1}])
	cloud.egraphMu.RUnlock()
	require.NotZero(t, id)

	for _, session := range []string{"", "s"} {
		_, err := cloud.LoadResultByResultID(ctx, session, nil, id)
		require.ErrorIs(t, err, errIndexedRowHasNoValue, "session %q", session)
		_, err = cloud.ResultCallByResultID(ctx, session, id)
		require.ErrorIs(t, err, errIndexedRowHasNoValue, "session %q", session)
	}
	_, err = cloud.LoadResultByResultIDForSchema(ctx, "s", nil, id, nil)
	require.ErrorIs(t, err, errIndexedRowHasNoValue)
}

// Readers of a merged-away digest run concurrently with each other and with
// writers. Run with -race.
func TestIndexedRowsConcurrentReadsOfMergedDigest(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cloud := newCloudCache(t)
	const n = 8
	for i := range n {
		_, err := cloud.UpsertIndexedRow(ctx, "engine-a", rowFact(uint64(i+1), "xxh3:merge-"+strconv.Itoa(i), 0))
		require.NoError(t, err)
	}
	// Chain the classes so that finding a digest's root walks a path.
	for i := range n - 1 {
		require.NoError(t, cloud.UpsertIndexedClass(ctx, cachefact.Class{Digests: []cachefact.Digest{
			{Digest: "xxh3:merge-" + strconv.Itoa(i)}, {Digest: "xxh3:merge-" + strconv.Itoa(i+1)},
		}}))
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 50 {
				dig := "xxh3:merge-" + strconv.Itoa((g+i)%n)
				require.Len(t, cloud.EquivalentRows(dig), n)
				info, ok := cloud.RowInfo(RowKey{Engine: "engine-a", ID: uint64((g+i)%n + 1)})
				require.True(t, ok)
				require.Len(t, info.ClassDigests, n)
			}
		})
	}
	wg.Go(func() {
		for i := range 20 {
			_, err := cloud.UpsertIndexedRow(ctx, "engine-w", rowFact(uint64(i+1), "xxh3:writer-"+strconv.Itoa(i), 0))
			require.NoError(t, err)
		}
	})
	wg.Wait()
}
