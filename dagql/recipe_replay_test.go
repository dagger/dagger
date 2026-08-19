package dagql_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/internal/points"
	"github.com/dagger/dagger/engine"
)

const recipeClassificationReason = "test: value is bound to its producing session"

func newRecipeClassificationServer(t *testing.T) *dagql.Server {
	t.Helper()
	srv := newExternalDagqlServerForTest(t, Query{})
	points.Install[Query](srv)
	dagql.Fields[Query]{
		dagql.Func("unsafePoint", func(context.Context, Query, struct{}) (*points.Point, error) {
			t.Fatal("recipe classification must not evaluate fields")
			return nil, nil
		}).NotReplayable(recipeClassificationReason),
		dagql.Func("pointFromObject", func(context.Context, Query, struct {
			Object dagql.AnyID
		}) (*points.Point, error) {
			t.Fatal("recipe classification must not evaluate fields")
			return nil, nil
		}).Args(dagql.Arg("object")),
		dagql.Func("pointFromLazyObject", func(context.Context, Query, struct {
			Object dagql.AnyID
		}) (*points.Point, error) {
			t.Fatal("recipe classification must not evaluate fields")
			return nil, nil
		}).Args(dagql.Arg("object").LazyRef()),
	}.Install(srv)
	return srv
}

func requireNotReplayableCall(t *testing.T, got dagql.RecipeClassification, want *call.ID) {
	t.Helper()
	require.NotNil(t, got.NotReplayable)
	require.Equal(t, want.Field(), got.NotReplayable.Field)
	require.Equal(t, want.Digest(), got.NotReplayable.Digest)
	require.Equal(t, recipeClassificationReason, got.NotReplayable.Reason)
}

func TestClassifyRecipeNotReplayable(t *testing.T) {
	srv := newRecipeClassificationServer(t)
	pointType := (&points.Point{}).Type()
	unsafe := call.New().Append(pointType, "unsafePoint")

	t.Run("direct", func(t *testing.T) {
		requireNotReplayableCall(t, srv.ClassifyRecipe(unsafe), unsafe)
	})

	t.Run("receiver", func(t *testing.T) {
		recipe := unsafe.Append(pointType, "shiftLeft")
		requireNotReplayableCall(t, srv.ClassifyRecipe(recipe), unsafe)
	})

	t.Run("ID argument", func(t *testing.T) {
		recipe := call.New().Append(pointType, "pointFromObject",
			call.WithArgs(call.NewArgument("object", call.NewLiteralID(unsafe), false)),
		)
		requireNotReplayableCall(t, srv.ClassifyRecipe(recipe), unsafe)
	})
}

func TestClassifyRecipeReplayableAndLazyRefs(t *testing.T) {
	srv := newRecipeClassificationServer(t)
	pointType := (&points.Point{}).Type()
	replayable := call.New().Append(pointType, "point").Append(pointType, "shiftLeft")
	require.Nil(t, srv.ClassifyRecipe(replayable).NotReplayable)

	unsafe := call.New().Append(pointType, "unsafePoint")
	lazyRecipe := call.New().Append(pointType, "pointFromLazyObject",
		call.WithArgs(call.NewArgument("object", call.NewLiteralID(unsafe), false)),
	)
	require.Nil(t, srv.ClassifyRecipe(lazyRecipe).NotReplayable,
		"lazy-ref arguments are not evaluated during recipe loading")
}

func TestClassifyRecipeUnknownFieldsAreBestEffort(t *testing.T) {
	srv := newRecipeClassificationServer(t)
	pointType := (&points.Point{}).Type()

	unknown := call.New().Append(pointType, "fieldNotInSchema")
	require.Nil(t, srv.ClassifyRecipe(unknown).NotReplayable)
	require.Nil(t, srv.ClassifyRecipe(nil).NotReplayable)

	// An unresolved field cannot declare an argument lazy, so its ID arguments
	// are traversed as normal, just as they are by recipe loading.
	unsafe := call.New().Append(pointType, "unsafePoint")
	unknownWithArg := call.New().Append(pointType, "fieldNotInSchema",
		call.WithArgs(call.NewArgument("object", call.NewLiteralID(unsafe), false)),
	)
	requireNotReplayableCall(t, srv.ClassifyRecipe(unknownWithArg), unsafe)
}

// This file also measures what the recipe loader actually does when it is forced
// to re-execute a recorded call, rather than what it is documented to do.
//
// Background (internal-docs/recipe_replay.md): a field marked
// [dagql.Field.NotReplayable] taints itself and every recorded call above it
// when the recipe is loaded by a session other than the one that recorded it.
// A tainted vertex skips BOTH of loadRecipeVertex's cache lookups
// (dagql/server.go ~1530-1610) and falls through to baseObj.Select — the
// ordinary call path.
//
// The part that is not documented, and is the subject of these tests: that
// fall-through re-resolves the field's implicit inputs from scratch
// (dagql/objects.go:566, FieldSpec.resolveImplicitInputCallArgs). The recorded
// implicit inputs are copied verbatim only into the *frame* used for the
// lookups that taint just skipped; they never reach the executed call. So a
// field carrying dagql.PerCallInput mints a brand new nonce on every load
// (dagql/cache_inputs.go:70-77), which means:
//
//   - the value the load returns has an identity nobody recorded, and
//   - the entry it caches is keyed under that fresh identity, so the NEXT load
//     cannot find it and executes again, forever.
//
// That is the shape of the reported bug: a module function declared
// @cache(policy: Never) — which is exactly dagql.PerCallInput and nothing else
// (core/modfunc.go:124-139) — spawned a live agent every time a saved session
// recipe was resumed, instead of once.
//
// The fixture below models the three field shapes that matter:
//
//	liveBase  NotReplayable + PerSessionInput   (host.directory / currentWorkspace)
//	pure      plain cacheable                   (withEnvVariable, withPrompt, ...)
//	nonced    PerCallInput                      (@cache(policy: Never) module function)
//	spawned   PerCallInput + DoNotCache         (core LLM.spawn)

type replayFixture struct {
	t     *testing.T
	srv   *dagql.Server
	cache *dagql.Cache

	// Every counter is bumped by its field's resolver, so it counts real
	// executions rather than cache hits.
	liveReads   atomic.Int64
	pureCalls   atomic.Int64
	noncedCalls atomic.Int64
	spawnCalls  atomic.Int64
}

func newReplayFixture(t *testing.T) *replayFixture {
	f := &replayFixture{
		t:     t,
		srv:   newExternalDagqlServerForTest(t, Query{}),
		cache: newCache(t),
	}
	points.Install[Query](f.srv)

	dagql.Fields[Query]{
		// liveBase stands in for Host.directory / Query.currentWorkspace: a
		// value that is only meaningful to the session that read it, so a
		// recorded call to it must be re-executed by any other session.
		dagql.Func("liveBase", func(ctx context.Context, _ Query, _ struct{}) (*points.Point, error) {
			return &points.Point{X: int(f.liveReads.Add(1))}, nil
		}).
			WithInput(dagql.PerSessionInput).
			NotReplayable("test: the recorded digest is a stable key for an unstable value"),
	}.Install(f.srv)

	dagql.Fields[*points.Point]{
		// pure stands in for the ordinary reproducible calls a recipe is
		// mostly made of. Replaying these from cache is correct.
		dagql.Func("pure", func(ctx context.Context, self *points.Point, _ struct{}) (*points.Point, error) {
			f.pureCalls.Add(1)
			return &points.Point{X: self.X, Y: self.Y + 1}, nil
		}),
		// nonced is precisely a module function declared @cache(policy:
		// Never): dagql.PerCallInput as its only implicit input, and no
		// DoNotCache (core/modfunc.go:132-133). Its result IS cached — under a
		// key containing the nonce minted for that one invocation.
		dagql.Func("nonced", func(ctx context.Context, self *points.Point, _ struct{}) (*points.Point, error) {
			return &points.Point{X: self.X, Y: int(f.noncedCalls.Add(1))}, nil
		}).
			WithInput(dagql.PerCallInput),
		// spawned is the core LLM.spawn shape: a per-call nonce AND
		// DoNotCache, so nothing is ever stored under the recorded digest.
		dagql.Func("spawned", func(ctx context.Context, self *points.Point, _ struct{}) (*points.Point, error) {
			return &points.Point{X: self.X, Y: int(f.spawnCalls.Add(1))}, nil
		}).
			WithInput(dagql.PerCallInput).
			DoNotCache("test: every spawn mints a distinct instance"),
	}.Install(f.srv)

	return f
}

func (f *replayFixture) ctxFor(sessionID string) context.Context {
	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID:  "same-client",
		SessionID: sessionID,
	})
	return dagql.ContextWithCache(ctx, f.cache)
}

// record builds a chain in the given session and returns its recipe-form ID,
// the way LLM.portableID hands one to a session save file.
func (f *replayFixture) record(ctx context.Context, sels ...dagql.Selector) *call.ID {
	f.t.Helper()
	var base dagql.ObjectResult[*points.Point]
	require.NoError(f.t, f.srv.Select(ctx, f.srv.Root(), &base, dagql.Selector{Field: "liveBase"}))
	var chain dagql.ObjectResult[*points.Point]
	require.NoError(f.t, f.srv.Select(ctx, base, &chain, sels...))
	id, err := chain.RecipeID(ctx)
	require.NoError(f.t, err)
	require.False(f.t, id.IsHandle(), "the saved ID must be recipe-form")
	return id
}

// loadObj replays a recorded ID the way Server.LoadType does for a saved
// session file.
func (f *replayFixture) loadObj(ctx context.Context, id *call.ID) dagql.AnyObjectResult {
	f.t.Helper()
	res, err := f.srv.LoadType(ctx, id)
	require.NoError(f.t, err)
	obj, ok := res.(dagql.AnyObjectResult)
	require.True(f.t, ok)
	return obj
}

// load replays a recorded ID and returns the recipe ID of what came back, so a
// caller can compare the loaded call's identity against the recorded one.
func (f *replayFixture) load(ctx context.Context, id *call.ID) *call.ID {
	f.t.Helper()
	got, err := f.loadObj(ctx, id).RecipeID(ctx)
	require.NoError(f.t, err)
	return got
}

// implicitInputValue reads a recorded implicit input off a call, the same way
// recipeLoadState.recordedSessionStamp does (dagql/server.go:1516).
func implicitInputValue(t *testing.T, id *call.ID, name string) string {
	t.Helper()
	for _, in := range id.ImplicitInputs() {
		if in == nil || in.Name() != name {
			continue
		}
		lit, ok := in.Value().(*call.LiteralString)
		require.True(t, ok, "implicit input %q is not a string literal", name)
		return lit.Value()
	}
	t.Fatalf("no implicit input %q on %s", name, id.Field())
	return ""
}

// TestRecipeLoadSameSessionServesTheRecordedCall pins claim 4: inside the
// session that recorded it, a recipe carrying a per-call nonce loads straight
// off the cache. Nothing re-executes and the loaded value's identity is bit
// for bit the recorded one — the recorded nonce is still a live cache key
// here, so neither lookup in loadRecipeVertex is skipped.
//
// This is correct behaviour, and it is the control for the cross-session
// measurements below: the difference they show is caused by the session
// boundary, not by the nonce alone.
func TestRecipeLoadSameSessionServesTheRecordedCall(t *testing.T) {
	f := newReplayFixture(t)
	ctxA := f.ctxFor("session-a")

	// liveBase -> nonced -> pure: a per-call-nonce call sitting above a
	// NotReplayable read, with an ordinary recorded call above it.
	recorded := f.record(ctxA,
		dagql.Selector{Field: "nonced"},
		dagql.Selector{Field: "pure"},
	)
	recordedNonce := implicitInputValue(t, recorded.Receiver(), dagql.PerCallInput.Name)

	const loads = 5
	for range loads {
		got := f.load(ctxA, recorded)
		assert.Check(t, cmp.Equal(got.Digest(), recorded.Digest()),
			"a same-session load must return the recorded call, not a new one")
		assert.Check(t, cmp.Equal(
			implicitInputValue(t, got.Receiver(), dagql.PerCallInput.Name), recordedNonce),
			"a same-session load must not re-mint the recorded nonce")
	}

	assert.Check(t, cmp.Equal(f.liveReads.Load(), int64(1)), "liveBase executions")
	assert.Check(t, cmp.Equal(f.noncedCalls.Load(), int64(1)), "nonced executions")
	assert.Check(t, cmp.Equal(f.pureCalls.Load(), int64(1)), "pure executions")
}

// TestRecipeLoadCrossSessionCachesTheTaintForcedExecution pins claim 1 for
// ordinary, reproducible calls: the taint costs exactly one re-execution per
// loading session, not one per load.
//
// The first load in a new session re-executes liveBase (its recorded
// cachePerSession stamp names another session) and re-executes the pure call
// above it, because that call's receiver is now a different value. Both
// results are then cached under the new session's digests, and the four
// remaining loads are served from them even though the vertices are still
// tainted — taint only skips the *recipe loader's* lookups; the re-issued call
// still consults the ordinary cache, and for a field with no per-call input it
// hits.
//
// This is correct behaviour, and it is what makes the failure in
// TestRecipeLoadTaintedNonceReExecutesOnEveryLoad specific to the nonce rather
// than inherent to the taint path.
func TestRecipeLoadCrossSessionCachesTheTaintForcedExecution(t *testing.T) {
	f := newReplayFixture(t)

	recorded := f.record(f.ctxFor("session-a"), dagql.Selector{Field: "pure"})

	ctxB := f.ctxFor("session-b")
	var first *call.ID
	const loads = 5
	for i := range loads {
		got := f.load(ctxB, recorded)
		if i == 0 {
			first = got
			assert.Check(t, got.Digest() != recorded.Digest(),
				"the re-executed chain is re-recorded under the loading session")
			continue
		}
		assert.Check(t, cmp.Equal(got.Digest(), first.Digest()),
			"repeated loads in one session must converge on a single identity")
	}

	assert.Check(t, cmp.Equal(f.liveReads.Load(), int64(2)),
		"liveBase: once when recorded, once when the new session first loads it")
	assert.Check(t, cmp.Equal(f.pureCalls.Load(), int64(2)),
		"pure: once when recorded, once on top of the re-read base")
}

// TestRecipeLoadTaintedNonceReExecutesOnEveryLoad pins the hazard that makes
// "an effectful call must never enter a persisted recipe" a RULE rather than a
// preference. It characterizes the loader as it stands; it is not a wish.
//
// Setup mirrors the reported failure: a saved recipe whose chain contains a
// module function declared @cache(policy: Never) (here `nonced`, carrying only
// dagql.PerCallInput) that transitively depends on a NotReplayable read, then
// resumed in a different session. Loading it five times in one new session:
//
//	liveBase executions   2  (1 recorded + 1 forced)
//	nonced   executions   6  (1 recorded + 5 forced)
//	pure     executions   6  (1 recorded + 5 forced)
//	distinct loaded IDs   5
//
// Every load re-enters loadRecipeVertex with the taint set, skips both
// lookups, and re-issues the call — at which point
// FieldSpec.resolveImplicitInputCallArgs mints a fresh cachePerCall nonce
// (dagql/cache_inputs.go:72-77). The executed call therefore has an identity
// no load can predict, so the result it caches is unreachable to the next
// load, which executes again. Three recorded spawns replayed across eleven
// loads is thirty-three live agents.
//
// Note the cascade: `pure` sits ABOVE `nonced` and is an ordinary cacheable
// field, yet it also re-executes on every load, because its receiver is a
// different value each time. A single per-call nonce buried in a recipe
// invalidates every recorded call above it, on every load.
//
// THE FIX IS NOT HERE, deliberately. Making the loader identity-stable (replay
// the recorded cachePerCall inputs, or memoize the forced execution per
// loading session) would turn 33 agents into 3 — quieter, not smaller in kind,
// and three agents wearing the user's workers' names with none of their
// history is the failure this defect was already misdiagnosed as once. The fix
// is at the API layer: keep effectful calls out of persisted recipes, so a
// resumed chain re-executes nothing worth re-executing. This test exists so
// that the cost of breaking that rule stays measured and visible, and it
// should be UPDATED, not deleted, if the loader ever does change.
func TestRecipeLoadTaintedNonceReExecutesOnEveryLoad(t *testing.T) {
	f := newReplayFixture(t)

	recorded := f.record(f.ctxFor("session-a"),
		dagql.Selector{Field: "nonced"},
		dagql.Selector{Field: "pure"},
	)
	recordedNonce := implicitInputValue(t, recorded.Receiver(), dagql.PerCallInput.Name)

	ctxB := f.ctxFor("session-b")
	const loads = 5
	seen := map[string]int{}
	for range loads {
		got := f.load(ctxB, recorded)
		seen[got.Digest().String()]++

		// Measurement, not a wish: the re-executed call is minted with a new
		// nonce rather than the recorded one. A fix may legitimately either
		// replay the recorded nonce or mint one nonce per loading session —
		// what it must not do is mint a new one per load, which is what the
		// identity-stability assertion below pins.
		assert.Check(t,
			implicitInputValue(t, got.Receiver(), dagql.PerCallInput.Name) != recordedNonce,
			"the taint path re-resolves implicit inputs instead of replaying them")
	}

	// The cost of one nonce in a recipe: the effectful call runs once per
	// LOAD, not once per loading session. This is the number that turned 3
	// recorded spawns into 33 live agents.
	assert.Check(t, cmp.Equal(f.noncedCalls.Load(), int64(1+loads)),
		"a nonce-bearing call under a taint re-executes on every load")

	// The same cause, one level up: every recorded call above the nonce
	// loses its cache too, because its receiver has a fresh identity on
	// each load. The blast radius of one effectful call is the whole chain
	// recorded above it.
	assert.Check(t, cmp.Equal(f.pureCalls.Load(), int64(1+loads)),
		"an ordinary cacheable call above the nonce is dragged into "+
			"re-executing on every load")

	// Identity never converges: each load produces a distinct ID, so a
	// caller that saves the loaded ID saves a value nothing else can
	// address — which is also why the next load cannot reuse the last
	// load's cached result.
	assert.Check(t, cmp.Equal(len(seen), loads),
		"repeated loads of one recorded ID produce one identity each")

	// The contrast that localizes the cause to the nonce and not the taint:
	// the NotReplayable read is re-executed once for the new session and
	// then served from cache, exactly as
	// TestRecipeLoadCrossSessionCachesTheTaintForcedExecution shows for a
	// nonce-free chain.
	assert.Check(t, cmp.Equal(f.liveReads.Load(), int64(2)),
		"liveBase: once when recorded, once when the new session first loads it")
}

// TestRecipeLoadTaintIsScopedNotPermanent pins claim 2: the taint is recomputed
// per load against the loading session, so it does not accumulate.
//
// A chain re-recorded by the new session carries that session's cachePerSession
// stamp, and fieldNotReplayable compares the recorded stamp to the loading
// session (dagql/server.go:1506-1511). Loading it — or a longer chain built on
// top of it — inside that same session is therefore clean: nothing re-executes,
// including the per-call-nonce call underneath, whose recorded nonce is a live
// cache key again. A previous session's input does not confer permanent taint.
//
// Crossing another boundary re-taints, exactly once for the NotReplayable read.
func TestRecipeLoadTaintIsScopedNotPermanent(t *testing.T) {
	f := newReplayFixture(t)

	recorded := f.record(f.ctxFor("session-a"), dagql.Selector{Field: "nonced"})

	// One tainted load. Everything below is measured against the chain this
	// produces, which session B recorded itself.
	ctxB := f.ctxFor("session-b")
	loadedInB := f.load(ctxB, recorded)
	assert.Check(t, cmp.Equal(f.liveReads.Load(), int64(2)))
	assert.Check(t, cmp.Equal(f.noncedCalls.Load(), int64(2)))

	// Extend it, the way a resumed conversation keeps making calls.
	obj := f.loadObj(ctxB, loadedInB)
	var extended dagql.ObjectResult[*points.Point]
	require.NoError(t, f.srv.Select(ctxB, obj, &extended, dagql.Selector{Field: "pure"}))
	extendedID, err := extended.RecipeID(ctxB)
	require.NoError(t, err)

	before := [3]int64{f.liveReads.Load(), f.pureCalls.Load(), f.noncedCalls.Load()}
	for range 3 {
		got := f.load(ctxB, extendedID)
		assert.Check(t, cmp.Equal(got.Digest(), extendedID.Digest()),
			"a chain recorded by this session loads as itself")
	}
	after := [3]int64{f.liveReads.Load(), f.pureCalls.Load(), f.noncedCalls.Load()}
	assert.Check(t, cmp.Equal(after, before),
		"a chain recorded in the loading session must not inherit taint from "+
			"the previous-session input it was derived from")

	// A third session re-taints the same chain — once for the NotReplayable
	// read, which is the scoping working as intended. (The calls above it do
	// re-execute per load, for the reason
	// TestRecipeLoadTaintedNonceReExecutesOnEveryLoad pins.)
	ctxC := f.ctxFor("session-c")
	for range 3 {
		f.load(ctxC, extendedID)
	}
	assert.Check(t, cmp.Equal(f.liveReads.Load(), int64(3)),
		"the taint is evaluated against the loading session, not remembered")
}

// TestRecipeLoadDoNotCacheReExecutesOnEveryLoad records the neighbouring
// behaviour of the core LLM.spawn shape — dagql.PerCallInput plus DoNotCache —
// which needs no taint at all to re-execute.
//
// Because nothing is stored under the recorded digest, both of
// loadRecipeVertex's lookups miss even inside the recording session, and the
// call is re-issued with a fresh nonce. So a recipe whose top call is
// DoNotCache re-executes on EVERY load, in EVERY session, forever.
//
// Asserted as current behaviour rather than as a defect: DoNotCache does say
// "never serve this from cache". core/schema/llm.go avoids the consequence by
// pinning spawn's result identity through the cached `agent(id:)` lookup, so a
// saved conversation's recipe never contains a bare spawn. A module function
// has no such mitigation, which is why the defect above bites at
// @cache(policy: Never).
func TestRecipeLoadDoNotCacheReExecutesOnEveryLoad(t *testing.T) {
	f := newReplayFixture(t)
	ctxA := f.ctxFor("session-a")

	recorded := f.record(ctxA, dagql.Selector{Field: "spawned"})
	assert.Check(t, cmp.Equal(f.spawnCalls.Load(), int64(1)))

	const loads = 5
	for range loads {
		f.load(ctxA, recorded)
	}
	assert.Check(t, cmp.Equal(f.spawnCalls.Load(), int64(1+loads)),
		"a DoNotCache call is re-executed by every load, even in its own session")
	assert.Check(t, cmp.Equal(f.liveReads.Load(), int64(1)),
		"the untainted NotReplayable read below it stays on the cache path")

	ctxB := f.ctxFor("session-b")
	for range loads {
		f.load(ctxB, recorded)
	}
	assert.Check(t, cmp.Equal(f.spawnCalls.Load(), int64(1+2*loads)),
		"crossing the session boundary adds taint but changes nothing here")
	assert.Check(t, cmp.Equal(f.liveReads.Load(), int64(2)),
		"the NotReplayable read is still re-executed exactly once per session")
}
