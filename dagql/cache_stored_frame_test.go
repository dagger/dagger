package dagql

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	engineslog "github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// storedTestFrame is the entry's call frame, decoded: its own, or decoded
// from the stored body that holds it.
func storedTestFrame(t *testing.T, c *Cache, res *sharedResult) *ResultCall {
	t.Helper()
	c.egraphMu.RLock()
	held := res.entryFrameLocked()
	c.egraphMu.RUnlock()
	frame, err := held.decode()
	require.NoError(t, err)
	require.NotNil(t, frame, "entry %d has a call frame", res.id)
	return frame
}

// storedTestRecipe is the recipe digest of the entry's call frame.
func storedTestRecipe(t *testing.T, c *Cache, res *sharedResult) digest.Digest {
	t.Helper()
	c.egraphMu.RLock()
	held := res.entryFrameLocked()
	c.egraphMu.RUnlock()
	recipe, err := held.recipeDigest(c)
	require.NoError(t, err)
	return recipe
}

// storedTestCompressWithoutFrames stops the Cloud's body worker and
// compresses every stored record's envelope now, leaving its call frame
// decoded on the entry: the Cloud as it held its records before its bodies
// held their frames, which a test compares answers against.
func storedTestCompressWithoutFrames(c *Cache) {
	c.closeStoredBodies()
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	for _, res := range c.resultsByID {
		if res.stored != nil || res.persistedEnvelope == nil {
			continue
		}
		body, err := newStoredBody(res, res.persistedEnvelope, res.loadResultCall())
		if err == nil {
			c.setStoredBodyLocked(res, res.persistedEnvelope, body)
		}
	}
}

// requireFramesHeldCompressed checks that every entry with a stored record
// holds its call frame in its body, with the frame's recipe, and no decoded
// frame.
func requireFramesHeldCompressed(t *testing.T, c *Cache) {
	t.Helper()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	stored := 0
	for _, res := range c.resultsByID {
		if res.persistedEnvelope == nil {
			continue
		}
		stored++
		require.Nil(t, res.loadResultCall(), "entry %d keeps no decoded frame", res.id)
		require.NotNil(t, res.stored, "entry %d holds its record compressed", res.id)
		require.NotNil(t, res.stored.frame, "entry %d's body holds its frame", res.id)
		frame, err := res.stored.decodeFrame()
		require.NoError(t, err)
		recipe, err := frame.deriveRecipeDigest(nil)
		if err == nil {
			require.Equal(t, recipe, res.stored.recipe, "entry %d's body keeps its frame's recipe", res.id)
		}
		if len(res.recipeKeys) > 0 {
			require.Equal(t, res.recipeKeys[0], res.stored.recipe, "entry %d's body keeps the recipe it is indexed under", res.id)
		}
	}
	require.NotZero(t, stored)
}

// storedFrameTestPair is two Clouds that merged the same exports in the same
// order: whole keeps each stored record's call frame decoded, as before its
// bodies held frames, and held holds every frame in its body.
func storedFrameTestPair(t *testing.T, bundles ...ValueBundle) (context.Context, *Cache, *Cache) {
	t.Helper()
	ctx := cacheTestContext(t.Context())
	open := func() *Cache {
		c, err := NewCache(ctx, "", nil, nil, WithBlobStore())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
		c.closeStoredBodies()
		for _, bundle := range bundles {
			reply, err := c.MergeValues(ctx, "cache-a", bundle)
			require.NoError(t, err)
			require.True(t, reply.Committed)
		}
		return c
	}
	whole, held := open(), open()
	storedTestCompressWithoutFrames(whole)
	storedTestCompress(held)
	requireFramesHeldCompressed(t, held)
	return ctx, whole, held
}

// storedFrameTestChain exports a root R whose call takes P as its receiver,
// with a dependency Q of R's own, from an engine's cache: R's recipe derives
// from P's.
func storedFrameTestChain(t *testing.T, text string) (ValueBundle, ValueBundle) {
	t.Helper()
	actx, a, asrv := transferTestCache(t)
	p := persistedListTestResult(t, actx, a, asrv, "frame-receiver", &transferTestValue{Text: "receiver " + text})
	q := persistedListTestResult(t, actx, a, asrv, "frame-dep", &transferTestValue{Text: "dep " + text})
	frame := &ResultCall{Kind: ResultCallKindField, Field: "frame-chained", Type: NewResultCallType((&transferTestValue{}).Type()), Receiver: &ResultCallRef{ResultID: uint64(p.cacheSharedResult().id)}, Args: []*ResultCallArg{{Name: "text", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: strings.Repeat(text, 64)}}}}
	r, err := a.GetOrInitCall(actx, "test-session", asrv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(&transferTestValue{Text: "chained " + text}, frame)
	})
	require.NoError(t, err)
	transferTestDependency(a, actx, r, q)
	return exportTestBundle(t, actx, a, p), exportTestBundle(t, actx, a, r)
}

// storedFrameTestNumber is the Cloud's number of the entry whose frame has
// the field.
func storedFrameTestNumber(t *testing.T, c *Cache, field string) uint64 {
	t.Helper()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	for id, res := range c.resultsByID {
		frame, err := res.entryFrameLocked().decode()
		require.NoError(t, err)
		if frame != nil && frame.Field == field {
			return uint64(id)
		}
	}
	require.FailNow(t, "no entry of the field", field)
	return 0
}

// The Cloud's body worker has each stored record's body hold its call frame,
// compressed, with the frame's recipe, and the entry then keeps no decoded
// frame. The wait is long because the race detector makes the worker's
// compression several times slower.
func TestCloudHoldsStoredFramesCompressed(t *testing.T) {
	t.Parallel()
	bundle := storedBodyTestExport(t)
	ctx := cacheTestContext(t.Context())
	cloud, err := NewCache(ctx, "", nil, nil, WithBlobStore())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cloud.CloseDiscardingPersistence()) })
	_, chain := storedFrameTestChain(t, "worker")
	storedPersistenceTestMerge(t, ctx, cloud, bundle)
	storedPersistenceTestMerge(t, ctx, cloud, chain)
	require.Eventually(t, func() bool {
		cloud.egraphMu.RLock()
		defer cloud.egraphMu.RUnlock()
		for _, res := range cloud.resultsByID {
			if res.persistedEnvelope != nil && res.loadResultCall() != nil {
				return false
			}
		}
		return true
	}, time.Minute, 50*time.Millisecond, "the body worker drops every stored record's decoded frame")
	requireFramesHeldCompressed(t, cloud)
}

// A Cloud whose bodies hold their frames answers exactly as one whose frames
// stay decoded: its bundles, with and without held rows, encode byte for
// byte the same, its sizes are the same, and the bundles merge into an
// engine as before.
func TestCloudAnswersTheSameWithFramesCompressed(t *testing.T) {
	t.Parallel()
	receiver, chain := storedFrameTestChain(t, "answers")
	ctx, whole, held := storedFrameTestPair(t, receiver, chain, storedBodyTestExport(t))
	r := storedFrameTestNumber(t, held, "frame-chained")
	p := storedFrameTestNumber(t, held, "frame-receiver")
	require.Equal(t, r, storedFrameTestNumber(t, whole, "frame-chained"), "both Clouds numbered the same")
	var roots []uint64
	held.egraphMu.RLock()
	for id := range held.persistedEdgesByResult {
		roots = append(roots, uint64(id))
	}
	held.egraphMu.RUnlock()
	require.Greater(t, len(roots), 1)
	now := time.Now().Unix()
	for _, c := range []*Cache{whole, held} {
		heldTestHold(t, ctx, c, "cache-b", 7, p, 0)
	}
	for name, by := range map[string]*HeldBy{"no recipient": nil, "a recipient that holds the receiver": {Cache: "cache-b"}} {
		for _, rs := range [][]uint64{{r}, roots} {
			want, err := whole.StoredBundle(ctx, rs, now, by)
			require.NoError(t, err, name)
			got, err := held.StoredBundle(ctx, rs, now, by)
			require.NoError(t, err, name)
			require.Equal(t, want.Held, got.Held, name)
			require.Equal(t, string(mustTransferJSON(t, want.Bundle)), string(mustTransferJSON(t, got.Bundle)), "%s: the bundle encodes the same", name)
			wantSizes, err := whole.StoredSizes(ctx, rs, now, by)
			require.NoError(t, err, name)
			gotSizes, err := held.StoredSizes(ctx, rs, now, by)
			require.NoError(t, err, name)
			require.Equal(t, wantSizes, gotSizes, "%s: the sizes are the same", name)
		}
	}
	requireFramesHeldCompressed(t, held)

	// The bundle merges into an engine as the bundle with decoded frames
	// does: it carries a decoded copy of each frame in process, too.
	got, err := held.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	want, err := whole.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	for _, bundle := range []ValueBundle{want.Bundle, got.Bundle} {
		bctx, b, _ := transferTestCache(t)
		reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
		require.NoError(t, err)
		require.True(t, reply.Committed)
		imported := reply.Imported()
		require.Len(t, imported, 1)
		frame := storedTestFrame(t, b, storedBundleTestEntry(b, imported[0].ResultID))
		require.Equal(t, "frame-chained", frame.Field)
		require.Equal(t, storedTestRecipe(t, whole, storedBundleTestEntry(whole, r)), storedTestRecipe(t, b, storedBundleTestEntry(b, imported[0].ResultID)), "the engine derives the Cloud's recipe")
	}
}

// An engine's bundle that sends, as a held row, a row whose Cloud entry
// holds its frame compressed merges as it does against a decoded frame: the
// row above it, whose call takes the held row as its receiver, derives its
// recipe from the entry's kept recipe and lands on the same entries.
func TestCloudMergesHeldRowsOnCompressedFrames(t *testing.T) {
	t.Parallel()
	ctx, engine, srv := transferTestCache(t)
	p := persistedListTestResult(t, ctx, engine, srv, "frame-held-receiver", String("receiver"))
	frame := &ResultCall{Kind: ResultCallKindField, Field: "frame-held-chained", Type: NewResultCallType(String("").Type()), Receiver: &ResultCallRef{ResultID: uint64(p.cacheSharedResult().id)}}
	r, err := engine.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(String("chained"), frame)
	})
	require.NoError(t, err)
	receiverOnly := exportTestBundle(t, ctx, engine, p)
	full := exportTestBundle(t, ctx, engine, r)
	heldBundle := engineHeldTestBundle(t, engine, full, p)

	var fromBody atomic.Int32
	_, whole, held := storedFrameTestPair(t, receiverOnly)
	held.testRecipeFromBody = func() { fromBody.Add(1) }
	for _, c := range []*Cache{whole, held} {
		reply, err := c.MergeValues(cacheTestContext(t.Context()), "cache-a", heldBundle)
		require.NoError(t, err)
		require.True(t, reply.Committed)
		require.Empty(t, reply.Stale)
	}
	require.NotZero(t, fromBody.Load(), "the held row's recipe came from the body")
	wantEntry, wantDeps := cloudEntryOfRecipe(t, whole, engine, r)
	gotEntry, gotDeps := cloudEntryOfRecipe(t, held, engine, r)
	require.NotNil(t, wantEntry)
	require.NotNil(t, gotEntry, "the row above the held row lands under its recipe")
	require.Equal(t, wantEntry.id, gotEntry.id)
	require.Equal(t, wantDeps, gotDeps)

	// The merge left the receiver's frame in its body, and the body worker
	// is stopped: the new entry keeps its decoded frame until compressed.
	pEntry, _ := cloudEntryOfRecipe(t, held, engine, p)
	held.egraphMu.RLock()
	require.Nil(t, pEntry.loadResultCall(), "the held row's entry keeps its frame compressed")
	held.egraphMu.RUnlock()
}

// A merge landing on entries whose bodies hold their frames, a record the
// Cloud already has, keeps them so: no decoded frame comes back, and the
// bodies are the same.
func TestCloudMergeLandingOnCompressedFramesKeepsThem(t *testing.T) {
	t.Parallel()
	receiver, chain := storedFrameTestChain(t, "landing")
	ctx, _, held := storedFrameTestPair(t, receiver, chain)
	held.egraphMu.RLock()
	bodies := map[sharedResultID]*storedBody{}
	for id, res := range held.resultsByID {
		bodies[id] = res.stored
	}
	held.egraphMu.RUnlock()
	for _, bundle := range []ValueBundle{receiver, chain} {
		reply, err := held.MergeValues(ctx, "cache-a", bundle)
		require.NoError(t, err)
		require.True(t, reply.Committed)
	}
	requireFramesHeldCompressed(t, held)
	held.egraphMu.RLock()
	defer held.egraphMu.RUnlock()
	for id, body := range bodies {
		require.Same(t, body, held.resultsByID[id].stored, "entry %d keeps its body", id)
	}
}

// A save writes each frame a body holds as the frame itself encodes: the
// rows are the same, column for column, as a Cloud's whose frames stay
// decoded. Restored, the Cloud holds its frames in their bodies again and
// sends the records it sent before.
func TestCloudSavesAndRestoresCompressedFrames(t *testing.T) {
	t.Parallel()
	receiver, chain := storedFrameTestChain(t, "save")
	pool := storedBodyTestExport(t)
	dir := t.TempDir()
	save := func(path string, compress func(*Cache)) (*Cache, map[int64][2]string) {
		ctx, c := storedPersistenceTestCloud(t, path)
		c.closeStoredBodies()
		for _, bundle := range []ValueBundle{receiver, chain, pool} {
			storedPersistenceTestMerge(t, ctx, c, bundle)
		}
		compress(c)
		require.NoError(t, c.Checkpoint(ctx))
		rows := map[int64][2]string{}
		q, err := c.sqlDB.QueryContext(ctx, `SELECT id, call_frame_json, self_payload FROM results`)
		require.NoError(t, err)
		for q.Next() {
			var id int64
			var call string
			var payload []byte
			require.NoError(t, q.Scan(&id, &call, &payload))
			rows[id] = [2]string{call, string(payload)}
		}
		require.NoError(t, q.Err())
		require.NoError(t, q.Close())
		return c, rows
	}
	whole, wantRows := save(filepath.Join(dir, "whole.db"), storedTestCompressWithoutFrames)
	held, gotRows := save(filepath.Join(dir, "held.db"), storedTestCompress)
	requireFramesHeldCompressed(t, held)
	require.NotEmpty(t, wantRows)
	require.Equal(t, wantRows, gotRows, "the saved rows are the same")

	ctx := cacheTestContext(t.Context())
	r := storedFrameTestNumber(t, held, "frame-chained")
	want, err := whole.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.NoError(t, whole.Close(ctx))
	require.NoError(t, held.Close(ctx))

	ctx, restored := storedPersistenceTestCloud(t, filepath.Join(dir, "held.db"))
	defer func() { require.NoError(t, restored.Close(ctx)) }()
	require.Equal(t, CachePersistenceResetNone, restored.PersistenceResetReason())
	requireFramesHeldCompressed(t, restored)
	got, err := restored.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, string(mustTransferJSON(t, want.Bundle)), string(mustTransferJSON(t, got.Bundle)), "the restored Cloud sends the records it sent before")
}

// Teaching a content digest to an entry whose body holds its frame replaces
// the frame with the body: the bundle carries the taught digest as it does
// from an entry whose frame stays decoded, and the entry keeps its frame
// compressed.
func TestTeachContentDigestToACompressedFrame(t *testing.T) {
	t.Parallel()
	receiver, chain := storedFrameTestChain(t, "teach")
	ctx, whole, held := storedFrameTestPair(t, receiver, chain)
	r := storedFrameTestNumber(t, held, "frame-chained")
	for _, c := range []*Cache{whole, held} {
		require.NoError(t, c.TeachContentDigest(ctx, Result[Typed]{shared: storedBundleTestEntry(c, r)}, testDigest("taught-content")))
	}
	requireFramesHeldCompressed(t, held)
	want, err := whole.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	got, err := held.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Contains(t, string(mustTransferJSON(t, want.Bundle)), testDigest("taught-content").String())
	require.Equal(t, string(mustTransferJSON(t, want.Bundle)), string(mustTransferJSON(t, got.Bundle)))
	wantSizes, err := whole.StoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	gotSizes, err := held.StoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, wantSizes, gotSizes)
}

// An engine's cache never reads a recipe from a stored body: its entries keep
// their frames decoded. Merging the Cloud's bundles, with held rows, saving,
// restoring and pruning all derive recipes from decoded frames. If a change
// ever made an engine take that path, this fails.
func TestEngineNeverReadsARecipeFromAStoredBody(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := heldTestChainedCloud(t)
	storedTestCompress(cloud)
	var fromBody atomic.Int32
	hook := func() { fromBody.Add(1) }

	alone, err := cloud.StoredBundle(ctx, []uint64{p}, time.Now().Unix(), nil)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "engine.db")
	bctx := cacheTestContext(t.Context())
	b, err := NewCache(bctx, path, nil, nil)
	require.NoError(t, err)
	b.testRecipeFromBody = hook
	reply, err := b.MergeValues(bctx, cloudCacheID, alone.Bundle)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	pb := reply.Imported()[0].ResultID
	heldTestHold(t, ctx, cloud, "cache-b", pb, p, 0)
	got, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), &HeldBy{Cache: "cache-b"})
	require.NoError(t, err)
	require.Equal(t, []uint64{p}, got.Held)
	reply, err = b.MergeValues(bctx, cloudCacheID, got.Bundle)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	require.Empty(t, reply.Stale)
	require.NoError(t, b.Close(bctx))

	b, err = NewCache(bctx, path, nil, nil)
	require.NoError(t, err)
	b.testRecipeFromBody = hook
	defer func() { require.NoError(t, b.Close(bctx)) }()
	require.Equal(t, CachePersistenceResetNone, b.PersistenceResetReason())
	b.egraphMu.RLock()
	for _, res := range b.resultsByID {
		require.Nil(t, res.stored, "an engine holds no stored body")
	}
	b.egraphMu.RUnlock()
	_, err = b.Prune(bctx, []CachePrunePolicy{{All: true}})
	require.NoError(t, err)
	require.Zero(t, fromBody.Load(), "the engine read no recipe from a stored body")
}

// A Cloud whose bodies hold their frames names the calls a prune removes as
// one whose frames stay decoded, in the report and the log, also for a
// removed entry whose receiver the same pass removes.
//
// It replaces the default logger to read the log, so it runs alone.
func TestCloudPruneNamesCompressedFramesAsDecoded(t *testing.T) {
	receiver, chain := storedFrameTestChain(t, "prune")
	ctx, whole, held := storedFrameTestPair(t, receiver, chain)
	prune := func(c *Cache) ([]CacheUsageEntry, string) {
		var logs bytes.Buffer
		prev := engineslog.Default()
		engineslog.SetDefault(engineslog.New(slog.NewTextHandler(&logs, nil)))
		report, err := c.Prune(ctx, []CachePrunePolicy{{All: true}})
		engineslog.SetDefault(prev)
		require.NoError(t, err)
		var lines []string
		for _, line := range strings.Split(logs.String(), "\n") {
			if strings.Contains(line, "dagql pruned result") {
				// The log's own time differs from one prune to the next.
				_, rest, _ := strings.Cut(line, " level=")
				lines = append(lines, rest)
			}
		}
		slices.Sort(lines)
		return report.Entries, strings.Join(lines, "\n")
	}
	wantEntries, wantLog := prune(whole)
	gotEntries, gotLog := prune(held)
	labels := map[string]bool{}
	for _, entry := range wantEntries {
		labels[entry.DagqlCall] = true
	}
	require.True(t, labels["TransferTestValue.frame-chained"], "the removed chained call is named with its removed receiver's type: %v", labels)
	named := func(entries []CacheUsageEntry) []string {
		// Each Cloud has its own creation and use times.
		var out []string
		for _, entry := range entries {
			out = append(out, entry.ID+" "+entry.DagqlCall+" "+entry.Description)
		}
		slices.Sort(out)
		return out
	}
	require.Equal(t, named(wantEntries), named(gotEntries), "the report names the same calls")
	require.NotEmpty(t, wantLog)
	require.Equal(t, wantLog, gotLog, "the log names the same calls, digests and arguments")
}

// A body holds a frame's JSON only where it is the frame's transfer form
// too, as a bundle carries it: the frame's own encoding, which a save
// writes. A frame with an extra digest the transfer form drops stays
// decoded, whichever way the check finds it.
func TestStoredFrameJSONIsItsTransferForm(t *testing.T) {
	t.Parallel()
	typ := NewResultCallType((&transferTestValue{}).Type())
	for name, tc := range map[string]struct {
		frame *ResultCall
		held  bool
	}{
		"plain":                 {frame: &ResultCall{Kind: ResultCallKindField, Field: "plain", Type: typ, Receiver: &ResultCallRef{ResultID: 3}}, held: true},
		"extra digests":         {frame: &ResultCall{Kind: ResultCallKindField, Field: "extras", Type: typ, ExtraDigests: []call.ExtraDigest{{Digest: testDigest("content"), Label: call.ExtraDigestLabelContent}}}, held: true},
		"no extra digests yet":  {frame: &ResultCall{Kind: ResultCallKindField, Field: "empty", Type: typ, ExtraDigests: []call.ExtraDigest{}}, held: true},
		"an empty extra digest": {frame: &ResultCall{Kind: ResultCallKindField, Field: "dropped", Type: typ, ExtraDigests: []call.ExtraDigest{{Label: "dropped"}}}, held: false},
	} {
		raw, ok := storedFrameJSON(tc.frame)
		require.Equal(t, tc.held, ok, name)
		transfer, err := normalizeTransferCall(tc.frame)
		require.NoError(t, err, name)
		sent := mustTransferJSON(t, transfer)
		own := mustTransferJSON(t, tc.frame)
		require.Equal(t, tc.held, string(sent) == string(own), "%s: the check agrees with the encodings", name)
		if ok {
			require.Equal(t, string(own), string(raw), name)
		}
	}
}
