package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// decodeChainValue is a stored value whose payload names another result, its
// child, as a module's type definitions name theirs: its decode loads the
// child, so decoding the last of a chain decodes the whole chain.
type decodeChainValue struct {
	Text  string `json:"text"`
	Child uint64 `json:"child,omitempty"`
	child AnyResult
}

func (*decodeChainValue) Type() *ast.Type {
	return &ast.Type{NamedType: "DecodeChainValue", NonNull: true}
}

func (v *decodeChainValue) EncodePersistedObject(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	raw, err := json.Marshal(v)
	return PersistedObjectEncoding{JSON: raw}, err
}

func (*decodeChainValue) DecodePersistedObject(ctx context.Context, dec *PersistDecodeContext, raw json.RawMessage) (Typed, error) {
	value := new(decodeChainValue)
	if err := json.Unmarshal(raw, value); err != nil {
		return nil, err
	}
	child, err := dec.ResultRef(ctx, value.Child)
	if err != nil {
		return nil, err
	}
	value.child = child
	return value, nil
}

func init() {
	// Its payload has a transferTestValue's shape: that codec visits its
	// child.
	RegisterPersistedObjectFamily(PersistedObjectFamily{Name: "dagql_test.DecodeChain", Typed: (*decodeChainValue)(nil), Visitor: transferTestCodec{}, Transfer: transferTestCodec{}})
}

// hitDecodeTestServer installs decodeChainValue on a test server for c.
func hitDecodeTestServer(t *testing.T, ctx context.Context, c *Cache) (context.Context, *Server) {
	t.Helper()
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*decodeChainValue]{}))
	return srvToContext(ContextWithCache(ctx, c), srv), srv
}

// hitDecodeTestChain has an engine compute a chain of n results, each but
// the first naming the one before it as its child and depending on it, and
// returns the engine's cache, its context and server, and the field of the
// chain's last result, whose payload names the whole chain through its
// children.
func hitDecodeTestChain(t *testing.T, ctx context.Context, c *Cache, srv *Server, n int) (AnyResult, string) {
	t.Helper()
	var prev AnyResult
	var field string
	for i := range n {
		field = fmt.Sprintf("decode-chain-%d", i)
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

// hitDecodeTestLoad hits field on c, with a writer kept queued for the graph
// lock while it does, and returns what the hit's carrier says of its load,
// and how many times the hit queued behind the writer.
func hitDecodeTestLoad(t *testing.T, ctx context.Context, c *Cache, srv *Server, field string) (*CacheDecision, AnyResult, int64) {
	t.Helper()
	const hold = time.Millisecond
	w := startHitDecodeTestWriter(c, hold)
	frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType((&decodeChainValue{}).Type())}
	req := &CallRequest{ResultCall: frame, IsPersistable: true, CacheEvidence: NewCacheDecision()}
	res, err := c.GetOrInitCall(ctx, "test-session", srv, req, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("the call ran: it should hit")
	})
	w.close()
	require.NoError(t, err)
	require.Equal(t, CacheOutcomeHit, req.CacheEvidence.Outcome)
	return req.CacheEvidence, res, w.queued.Load()
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
	want := make([]string, n)
	for i := range n {
		want[i] = fmt.Sprintf("decode-chain-%d", n-1-i)
	}

	t.Run("merged from the Cloud", func(t *testing.T) {
		t.Parallel()
		actx, a, asrv := transferTestCache(t)
		asrv.InstallObject(NewClass(asrv, ClassOpts[*decodeChainValue]{}))
		root, field := hitDecodeTestChain(t, actx, a, asrv, n)
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

		ev, res, queued := hitDecodeTestLoad(t, ctx, engine, srv, field)
		require.Equal(t, int64(n), ev.HitLoad().Decoded, "the hit decodes every entry of the chain")
		require.LessOrEqual(t, queued, int64(few), "times the load queued behind a writer, for %d entries", n)
		require.Equal(t, want, hitDecodeTestTexts(t, res))
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
		_, field := hitDecodeTestChain(t, actx, a, asrv, n)
		cacheTestReleaseSession(t, a, actx)
		require.NoError(t, a.persistCurrentState(actx))
		require.NoError(t, a.Close(context.Background()))

		ctx := cacheTestContext(t.Context())
		engine, err := NewCache(ctx, dbPath, nil, nil)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, engine.CloseDiscardingPersistence()) })
		ctx, srv := hitDecodeTestServer(t, ctx, engine)

		ev, res, queued := hitDecodeTestLoad(t, ctx, engine, srv, field)
		require.Equal(t, int64(n), ev.HitLoad().Decoded, "the hit decodes every entry of the chain")
		require.LessOrEqual(t, queued, int64(few), "times the load queued behind a writer, for %d entries", n)
		require.Equal(t, want, hitDecodeTestTexts(t, res))
		total, sum := cacheTestPayloadTotalConsistent(engine)
		require.Equal(t, sum, total, "the payload byte count follows its entries")
	})
}
