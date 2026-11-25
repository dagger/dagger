package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// decodeChainValue is a stored value whose payload names another result, its
// child, as a module's type definitions name theirs: its decode loads the
// child, so decoding the last of a chain decodes the whole chain. links are
// the snapshots it declares it holds, which only a test hook gives it.
type decodeChainValue struct {
	Text  string `json:"text"`
	Child uint64 `json:"child,omitempty"`
	child AnyResult
	links []PersistedSnapshotRefLink
}

func (*decodeChainValue) Type() *ast.Type {
	return &ast.Type{NamedType: "DecodeChainValue", NonNull: true}
}

func (v *decodeChainValue) EncodePersistedObject(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	raw, err := json.Marshal(v)
	return PersistedObjectEncoding{JSON: raw}, err
}

func (v *decodeChainValue) PersistedSnapshotRefLinks() []PersistedSnapshotRefLink {
	return cloneSnapshotRefLinks(v.links)
}

// decodeChainHook runs in a decodeChainValue's decode, by the value's text:
// before its decode loads its child, and after.
type decodeChainHook struct {
	before, after func(ctx context.Context, dec *PersistDecodeContext, value *decodeChainValue)
}

var decodeChainHooks sync.Map // text → decodeChainHook

func (*decodeChainValue) DecodePersistedObject(ctx context.Context, dec *PersistDecodeContext, raw json.RawMessage) (Typed, error) {
	value := new(decodeChainValue)
	if err := json.Unmarshal(raw, value); err != nil {
		return nil, err
	}
	var hook decodeChainHook
	if h, ok := decodeChainHooks.Load(value.Text); ok {
		hook = h.(decodeChainHook)
	}
	if hook.before != nil {
		hook.before(ctx, dec, value)
	}
	child, err := dec.ResultRef(ctx, value.Child)
	if err != nil {
		return nil, err
	}
	value.child = child
	if hook.after != nil {
		hook.after(ctx, dec, value)
	}
	return value, nil
}

func init() {
	// Its payload has a transferTestValue's shape: that codec visits its
	// child.
	RegisterPersistedObjectFamily(PersistedObjectFamily{Name: "dagql_test.DecodeChain", Typed: (*decodeChainValue)(nil), Visitor: transferTestCodec{}, Transfer: transferTestCodec{}})
}

// hitDecodeTestServer installs decodeChainValue on a test server for c.
func hitDecodeTestServer(t testing.TB, ctx context.Context, c *Cache) (context.Context, *Server) {
	t.Helper()
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*decodeChainValue]{}))
	return srvToContext(ContextWithCache(ctx, c), srv), srv
}

// hitDecodeTestChain has an engine compute a chain of n results, prefix-0 to
// prefix-(n-1), each but the first naming the one before it as its child and
// depending on it, and returns the last, whose payload names the whole chain
// through its children, and its field.
func hitDecodeTestChain(t testing.TB, ctx context.Context, c *Cache, srv *Server, prefix string, n int) (AnyResult, string) {
	t.Helper()
	var prev AnyResult
	var field string
	for i := range n {
		field = fmt.Sprintf("%s-%d", prefix, i)
		value := &decodeChainValue{Text: field}
		if prev != nil {
			value.Child = uint64(prev.cacheSharedResult().id)
		}
		res := persistedListTestResult(t, ctx, c, srv, field, value)
		if prev != nil {
			transferTestDependency(c, ctx, res, prev)
		}
		prev = res
	}
	return prev, field
}

// hitDecodeTestCloudChain is a chain of n results an engine computed and
// exported to the Cloud, merged into a fresh engine that keeps snapshot
// leases as every engine does: its records wait for their first use, their
// payloads undecoded. It returns the fresh engine, its context and server,
// the chain's last field, and the fresh engine's entries by field.
func hitDecodeTestCloudChain(t testing.TB, prefix string, n int) (context.Context, *Cache, *Server, string, map[string]*sharedResult) {
	t.Helper()
	actx, a, asrv := transferTestCache(t)
	asrv.InstallObject(NewClass(asrv, ClassOpts[*decodeChainValue]{}))
	root, field := hitDecodeTestChain(t, actx, a, asrv, prefix, n)
	exported := exportTestBundle(t, actx, a, root)

	cctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(cctx, "cache-a", exported)
	require.NoError(t, err)
	var rootNumber uint64
	for _, value := range reply.Values {
		for _, sent := range exported.Values {
			if sent.Ordinal == value.Ordinal && sent.SenderNumber == uint64(root.cacheSharedResult().id) {
				rootNumber = value.Number
			}
		}
	}
	require.NotZero(t, rootNumber)
	stored, err := cloud.StoredBundle(cctx, []uint64{rootNumber}, time.Now().Unix(), nil)
	require.NoError(t, err)
	bundle, err := cloneValueBundle(stored.Bundle)
	require.NoError(t, err)

	ctx := cacheTestContext(t.Context())
	engine, err := NewCache(ctx, "", &fakeSnapshotManager{}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.CloseDiscardingPersistence()) })
	ctx, srv := hitDecodeTestServer(t, ctx, engine)
	merged, err := engine.MergeValues(ctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.True(t, merged.Committed)
	return ctx, engine, srv, field, hitDecodeTestEntries(engine, prefix)
}

// hitDecodeTestEntries returns c's entries of the chain prefix, by field.
func hitDecodeTestEntries(c *Cache, prefix string) map[string]*sharedResult {
	entries := map[string]*sharedResult{}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	for _, res := range c.resultsByID {
		if call := res.loadResultCall(); call != nil && len(call.Field) > len(prefix) && call.Field[:len(prefix)+1] == prefix+"-" {
			entries[call.Field] = res
		}
	}
	return entries
}

// hitDecodeTestWriter keeps a writer queued for c's graph lock: it takes the
// lock for writing as a publication, holds it until another acquisition
// waits for it, holds it hold longer, lets it go, and takes it again, until
// stopped. Each time something waited for it counts once: how often work
// running meanwhile queued behind a writer. A load whose work takes the lock
// once for each entry it decodes queues once for each.
type hitDecodeTestWriter struct {
	queued atomic.Int64
	stop   chan struct{}
	done   chan struct{}
}

func startHitDecodeTestWriter(c *Cache, hold time.Duration) *hitDecodeTestWriter {
	w := &hitDecodeTestWriter{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		for {
			c.lockGraphFor(context.Background(), workPublication)
			for !c.egraphMu.Waiting() {
				select {
				case <-w.stop:
					c.egraphMu.Unlock()
					return
				case <-time.After(50 * time.Microsecond):
				}
			}
			w.queued.Add(1)
			time.Sleep(hold)
			c.egraphMu.Unlock()
		}
	}()
	return w
}

func (w *hitDecodeTestWriter) close() {
	close(w.stop)
	<-w.done
}

// hitDecodeTestHit hits field on c and returns what the hit's carrier says
// of its load, and the result.
func hitDecodeTestHit(t *testing.T, ctx context.Context, c *Cache, srv *Server, field string) (*CacheDecision, AnyResult) {
	t.Helper()
	frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType((&decodeChainValue{}).Type())}
	req := &CallRequest{ResultCall: frame, IsPersistable: true, CacheEvidence: NewCacheDecision()}
	res, err := c.GetOrInitCall(ctx, "test-session", srv, req, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("the call ran: it should hit")
	})
	require.NoError(t, err)
	require.Equal(t, CacheOutcomeHit, req.CacheEvidence.Outcome)
	return req.CacheEvidence, res
}

// hitDecodeTestLoad hits field on c, with a writer kept queued for the graph
// lock while it does, and returns what the hit's carrier says of its load,
// the result, and how many times the hit queued behind the writer.
func hitDecodeTestLoad(t *testing.T, ctx context.Context, c *Cache, srv *Server, field string) (*CacheDecision, AnyResult, int64) {
	t.Helper()
	w := startHitDecodeTestWriter(c, time.Millisecond)
	ev, res := hitDecodeTestHit(t, ctx, c, srv, field)
	w.close()
	return ev, res, w.queued.Load()
}

// hitDecodeTestTexts walks the decoded chain from its last value through the
// children its decode loaded, and returns their texts, last first.
func hitDecodeTestTexts(t *testing.T, res AnyResult) []string {
	t.Helper()
	var texts []string
	for res != nil {
		value, ok := res.Unwrap().(*decodeChainValue)
		require.True(t, ok, "a chain value is %T", res.Unwrap())
		texts = append(texts, value.Text)
		res = value.child
	}
	return texts
}

// hitDecodeTestHooks sets the decode hooks of the chain values of texts for
// the test's life.
func hitDecodeTestHooks(t *testing.T, hooks map[string]decodeChainHook) {
	t.Helper()
	for text, hook := range hooks {
		decodeChainHooks.Store(text, hook)
		t.Cleanup(func() { decodeChainHooks.Delete(text) })
	}
}

// The first hit on a stored value decodes the whole graph its payload names:
// a module load on a fresh engine decodes about 1,400 entries. While writers
// keep coming, as other calls' publications and merges do on a busy engine,
// that load queues behind them a few times in all, not once or more for
// every entry it decodes: the work it needs of the cache's graph, finding
// each entry, reading its offers and dependencies and sizing what it
// decoded, doesn't take the graph lock entry by entry. The hit still decodes
// every entry of the chain, each with its own value, and the cache's count
// of payload bytes still follows its entries. It holds for a graph merged
// from the Cloud, whose records are prepared at their first use, on an
// engine that keeps snapshot leases, and for one restored from the engine's
// own saved cache.
func TestHitDecodingAStoredGraphQueuesBehindWritersAFewTimes(t *testing.T) {
	t.Parallel()
	const n = 64
	// The lookup, the decode's own reads of the graph before and after it,
	// and the persisted edge the hit refreshes: a load that takes the lock
	// for each entry queues about 2n to 4n times.
	const few = 8
	want := func(prefix string) []string {
		texts := make([]string, n)
		for i := range n {
			texts[i] = fmt.Sprintf("%s-%d", prefix, n-1-i)
		}
		return texts
	}

	t.Run("merged from the Cloud", func(t *testing.T) {
		t.Parallel()
		ctx, engine, srv, field, _ := hitDecodeTestCloudChain(t, "queue-cloud", n)
		ev, res, queued := hitDecodeTestLoad(t, ctx, engine, srv, field)
		t.Logf("the load queued %d times behind the writer, for %d entries", queued, n)
		require.Equal(t, int64(n), ev.HitLoad().Decoded, "the hit decodes every entry of the chain")
		require.LessOrEqual(t, queued, int64(few), "times the load queued behind a writer, for %d entries", n)
		require.Equal(t, want("queue-cloud"), hitDecodeTestTexts(t, res))
		total, sum := cacheTestPayloadTotalConsistent(engine)
		require.Equal(t, sum, total, "the payload byte count follows its entries")
	})

	t.Run("restored from the engine's own saved cache", func(t *testing.T) {
		t.Parallel()
		dbPath := filepath.Join(t.TempDir(), "cache.db")
		actx := cacheTestContext(t.Context())
		a, err := NewCache(actx, dbPath, nil, nil)
		require.NoError(t, err)
		actx, asrv := hitDecodeTestServer(t, actx, a)
		_, field := hitDecodeTestChain(t, actx, a, asrv, "queue-restart", n)
		cacheTestReleaseSession(t, a, actx)
		require.NoError(t, a.persistCurrentState(actx))
		require.NoError(t, a.Close(context.Background()))

		ctx := cacheTestContext(t.Context())
		engine, err := NewCache(ctx, dbPath, nil, nil)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, engine.CloseDiscardingPersistence()) })
		ctx, srv := hitDecodeTestServer(t, ctx, engine)

		ev, res, queued := hitDecodeTestLoad(t, ctx, engine, srv, field)
		t.Logf("the load queued %d times behind the writer, for %d entries", queued, n)
		require.Equal(t, int64(n), ev.HitLoad().Decoded, "the hit decodes every entry of the chain")
		require.LessOrEqual(t, queued, int64(few), "times the load queued behind a writer, for %d entries", n)
		require.Equal(t, want("queue-restart"), hitDecodeTestTexts(t, res))
		total, sum := cacheTestPayloadTotalConsistent(engine)
		require.Equal(t, sum, total, "the payload byte count follows its entries")
	})
}

// hitDecodeTestReplace replaces res's value in place with a stored payload
// of text naming child, as a merge does when the Cloud sends a newer record
// for an entry the engine holds (cache_value_merge.go): its payload is
// undecoded again, at a new revision, and sized as the payload it stores.
func hitDecodeTestReplace(t *testing.T, c *Cache, res *sharedResult, text string, child *sharedResult) {
	t.Helper()
	raw, err := json.Marshal(&decodeChainValue{Text: text, Child: uint64(child.id)})
	require.NoError(t, err)
	env := PersistedResultEnvelope{
		Version:     persistedResultEnvelopeVersion,
		Kind:        persistedResultKindObject,
		TypeName:    (&decodeChainValue{}).Type().NamedType,
		ObjectCodec: "dagql_test.DecodeChain",
		ResultID:    uint64(res.id),
		Imported:    true,
		ObjectJSON:  raw,
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	res.payloadMu.Lock()
	defer res.payloadMu.Unlock()
	res.hasValue = false
	res.self = nil
	res.persistedEnvelope = &env
	res.firstUse = nil
	res.payloadRevision++
	c.setResultPayloadBytesLocked(res, persistedEnvelopePayloadBytes(&env))
}

// A load reads the graph its decode reaches once, before it decodes, so a
// change to an entry after that read and before the entry's decode reaches
// the load as a change just after the entry's own read did when each entry
// was read as it was decoded. Each case changes the middle entry of a chain
// of three merged from the Cloud, from the decode of the last, which runs
// after the read and before the others decode.
func TestHitDecodeMeetsChangesAfterItsGraphRead(t *testing.T) {
	t.Parallel()

	// An offer that arrives after the read doesn't arm the entry's decode
	// for part acquisition, as it didn't when it arrived just after the
	// entry's own read: the decode runs the recipe, and the entry's next
	// demand finds the offer (usesPartAcquisition). One that arrived before
	// the read does.
	t.Run("an offer that arrives", func(t *testing.T) {
		t.Parallel()
		for _, before := range []bool{false, true} {
			prefix := fmt.Sprintf("offer-%v", before)
			ctx, engine, srv, field, entries := hitDecodeTestCloudChain(t, prefix, 3)
			middle, leaf := entries[prefix+"-1"], entries[prefix+"-0"]
			offer := func() {
				transferTestOffer(t, engine, ctx, Result[Typed]{shared: middle}, Result[Typed]{shared: leaf})
			}
			if before {
				offer()
			}
			var armed atomic.Pointer[bool]
			hitDecodeTestHooks(t, map[string]decodeChainHook{
				prefix + "-2": {before: func(context.Context, *PersistDecodeContext, *decodeChainValue) {
					if !before {
						offer()
					}
				}},
				prefix + "-1": {before: func(_ context.Context, dec *PersistDecodeContext, _ *decodeChainValue) {
					arm := dec.ArmRecipe()
					armed.Store(&arm)
				}},
			})
			ev, _ := hitDecodeTestHit(t, ctx, engine, srv, field)
			require.Equal(t, int64(3), ev.HitLoad().Decoded)
			require.NotNil(t, armed.Load())
			require.Equal(t, !before, *armed.Load(), "the middle entry's decode runs its recipe only when its offer came after the read")
			require.True(t, engine.HasPartOffer(Result[Typed]{shared: middle}, "snapshot"), "its next demand finds the offer")
		}
	})

	// A payload replaced in place after the read and before its entry's
	// decode is decoded as it stands then, as it was when it came just
	// after the entry's own read. One replaced after its entry's decode
	// waits for its own first use, and keeps the replacement's size, not
	// the size of the value the load decoded, which the load records only
	// at its end: its revision changed.
	t.Run("a value that goes", func(t *testing.T) {
		t.Parallel()
		ctx, engine, srv, field, entries := hitDecodeTestCloudChain(t, "replace-before", 3)
		middle, leaf := entries["replace-before-1"], entries["replace-before-0"]
		hitDecodeTestHooks(t, map[string]decodeChainHook{
			"replace-before-2": {before: func(context.Context, *PersistDecodeContext, *decodeChainValue) {
				hitDecodeTestReplace(t, engine, middle, "replaced", leaf)
			}},
		})
		_, res := hitDecodeTestHit(t, ctx, engine, srv, field)
		require.Equal(t, []string{"replace-before-2", "replaced", "replace-before-0"}, hitDecodeTestTexts(t, res))
		total, sum := cacheTestPayloadTotalConsistent(engine)
		require.Equal(t, sum, total, "the payload byte count follows its entries")

		ctx, engine, srv, field, entries = hitDecodeTestCloudChain(t, "replace-after", 3)
		middle, leaf = entries["replace-after-1"], entries["replace-after-0"]
		var replacement int64
		hitDecodeTestHooks(t, map[string]decodeChainHook{
			"replace-after-2": {after: func(context.Context, *PersistDecodeContext, *decodeChainValue) {
				hitDecodeTestReplace(t, engine, middle, "replaced", leaf)
				replacement = middle.payloadBytes.Load()
			}},
		})
		ev, _ := hitDecodeTestHit(t, ctx, engine, srv, field)
		require.Equal(t, int64(3), ev.HitLoad().Decoded)
		require.True(t, middle.holdsPayloadToDecode(), "the replacement waits for its own first use")
		require.Equal(t, replacement, middle.payloadBytes.Load(), "the entry keeps the replacement's size")
		total, sum = cacheTestPayloadTotalConsistent(engine)
		require.Equal(t, sum, total, "the payload byte count follows its entries")
	})

	// A dependency added after the read isn't among those the entry's lease
	// sync reads, as it wasn't when it was added just after the sync's own
	// read: a snapshot the new dependency holds gets the entry's own lease
	// rather than being borrowed, and the entry's next sync borrows it. One
	// added before the read is borrowed at once.
	t.Run("a dependency that changes", func(t *testing.T) {
		t.Parallel()
		for _, before := range []bool{false, true} {
			prefix := fmt.Sprintf("dep-%v", before)
			ctx, engine, srv, field, entries := hitDecodeTestCloudChain(t, prefix, 3)
			middle := entries[prefix+"-1"]
			link := PersistedSnapshotRefLink{RefKey: prefix + "-snapshot", Role: "snapshot"}
			holderFrame := &ResultCall{Kind: ResultCallKindField, Field: prefix + "-holder", Type: NewResultCallType((&decodeChainValue{}).Type())}
			holder, err := engine.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: holderFrame}, func(context.Context) (AnyResult, error) {
				return NewResultForCall(&decodeChainValue{Text: prefix + "-holder", links: []PersistedSnapshotRefLink{link}}, holderFrame)
			})
			require.NoError(t, err)
			require.Len(t, holder.cacheSharedResult().loadSnapshotOwnerLinks(), 1, "the holder holds the snapshot")
			depend := func() {
				transferTestDependency(engine, ctx, Result[Typed]{shared: middle}, holder)
			}
			if before {
				depend()
			}
			hitDecodeTestHooks(t, map[string]decodeChainHook{
				prefix + "-2": {before: func(context.Context, *PersistDecodeContext, *decodeChainValue) {
					if !before {
						depend()
					}
				}},
				prefix + "-1": {before: func(_ context.Context, _ *PersistDecodeContext, value *decodeChainValue) {
					value.links = []PersistedSnapshotRefLink{link}
				}},
			})
			ev, _ := hitDecodeTestHit(t, ctx, engine, srv, field)
			require.Equal(t, int64(3), ev.HitLoad().Decoded)
			borrowed := func() bool {
				links := middle.loadSnapshotOwnerLinks()
				require.Len(t, links, 1)
				return links[0].Borrowed
			}
			require.Equal(t, before, borrowed(), "the decode's sync borrows the snapshot only from a dependency the read found")
			require.NoError(t, engine.SyncResultSnapshotOwnerLeases(ctx, Result[Typed]{shared: middle}))
			require.True(t, borrowed(), "the entry's next sync borrows it")
		}
	})

	// A prune after the read collects nothing the load reaches, as it
	// collected nothing just after an entry's own read: the hit's entry is
	// held by the session that hit it, and owns the chain through its
	// dependencies.
	t.Run("an entry that is pruned", func(t *testing.T) {
		t.Parallel()
		ctx, engine, srv, field, entries := hitDecodeTestCloudChain(t, "prune", 3)
		hitDecodeTestHooks(t, map[string]decodeChainHook{
			"prune-2": {before: func(ctx context.Context, _ *PersistDecodeContext, _ *decodeChainValue) {
				_, err := engine.Prune(context.WithoutCancel(ctx), []CachePrunePolicy{{All: true}})
				require.NoError(t, err)
			}},
		})
		ev, res := hitDecodeTestHit(t, ctx, engine, srv, field)
		require.Equal(t, int64(3), ev.HitLoad().Decoded)
		require.Equal(t, []string{"prune-2", "prune-1", "prune-0"}, hitDecodeTestTexts(t, res))
		require.Equal(t, entries, hitDecodeTestEntries(engine, "prune"), "every entry of the chain is still in the cache")
		total, sum := cacheTestPayloadTotalConsistent(engine)
		require.Equal(t, sum, total, "the payload byte count follows its entries")
	})
}

// BenchmarkHitDecodeGraphRead is a load's read of the graph its decode
// reaches, under one hold of the graph lock: a writer waits at most this
// long behind a load. The closures are a module's, about 1,400 entries, the
// largest seen on a pull request's checks, about 2,000, and ten times that.
func BenchmarkHitDecodeGraphRead(b *testing.B) {
	for _, n := range []int{1400, 2000, 20000} {
		b.Run(fmt.Sprintf("%d entries", n), func(b *testing.B) {
			_, engine, _, field, entries := hitDecodeTestCloudChain(b, fmt.Sprintf("bench-%d", n), n)
			root := entries[field]
			require.NotNil(b, root)
			ctx := context.Background()
			b.ResetTimer()
			for range b.N {
				reads := engine.readGraphForDecode(ctx, root)
				if len(reads.rows) != n {
					b.Fatalf("read %d entries of %d", len(reads.rows), n)
				}
			}
		})
	}
}
