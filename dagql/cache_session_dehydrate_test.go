package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// Spike for freeing cold results while their session is still alive (see
// TestCacheSessionRetainsEveryResult): it proves that a live, session-owned
// result can trade its decoded value for the persisted envelope form that
// boot restore already produces, keeping its number, call frame, e-graph
// identity and session/dependency edges, and that the existing decode-on-hit
// path (ensurePersistedHitValueLoaded) brings it back transparently on the
// next reference: by handle ID, by recipe lookup, and through a dependent's
// decode. Nothing runs the resolver again.

// dehydrateBulkObj stands for an object whose decoded Go form is larger than
// its encoding: Index is derived state rebuilt on decode, as decoded core
// objects rebuild theirs.
type dehydrateBulkObj struct {
	Name  string
	Lines []string
	Index map[string]int
}

type persistedDehydrateBulkObj struct {
	Name  string   `json:"name"`
	Lines []string `json:"lines"`
}

func (*dehydrateBulkObj) Type() *ast.Type {
	return &ast.Type{NamedType: "DehydrateBulkObj", NonNull: true}
}

func newDehydrateBulkObj(name string, lines []string) *dehydrateBulkObj {
	index := make(map[string]int, len(lines))
	for i, line := range lines {
		index[line] = i
	}
	return &dehydrateBulkObj{Name: name, Lines: lines, Index: index}
}

func (obj *dehydrateBulkObj) EncodePersistedObject(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	payload, err := json.Marshal(persistedDehydrateBulkObj{Name: obj.Name, Lines: obj.Lines})
	if err != nil {
		return PersistedObjectEncoding{}, err
	}
	return PersistedObjectEncoding{JSON: payload}, nil
}

func (*dehydrateBulkObj) DecodePersistedObject(_ context.Context, _ *PersistDecodeContext, payload json.RawMessage) (Typed, error) {
	var persisted persistedDehydrateBulkObj
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, err
	}
	return newDehydrateBulkObj(persisted.Name, persisted.Lines), nil
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{Name: "dagql_test.DehydrateBulkObj", Typed: (*dehydrateBulkObj)(nil), Visitor: PersistedNoReferences{}})
}

var errDependentHydrated = errors.New("a dependent still holds the decoded value")

// testDehydrate is the spike's value -> envelope transition, the inverse of
// ensurePersistedHitValueLoaded's install. It encodes outside the graph lock
// with the codec persistence uses, then installs the envelope only if the
// payload revision is unchanged, as the decode install does. A result is
// eligible only when every dependent is itself undecoded: a decoded dependent
// may hold the Go value through a field (persistChildLoadObj.Child).
func (c *Cache) testDehydrate(ctx context.Context, res *sharedResult) error {
	c.egraphMu.RLock()
	if c.resultsByID[res.id] != res {
		c.egraphMu.RUnlock()
		return fmt.Errorf("result %d is not registered", res.id)
	}
	if res.attachmentState() != resultAttachmentClean {
		c.egraphMu.RUnlock()
		return fmt.Errorf("result %d attachment is not clean", res.id)
	}
	if res.depParents != nil {
		for parentID := range res.depParents {
			if parent := c.resultsByID[parentID]; parent != nil && parent.loadPayloadState().hasValue {
				c.egraphMu.RUnlock()
				return fmt.Errorf("result %d: dependent %d: %w", res.id, parentID, errDependentHydrated)
			}
		}
	}
	c.egraphMu.RUnlock()

	state := res.loadPayloadState()
	if !state.hasValue || state.persistedEnvelope != nil {
		return nil
	}
	frame := res.loadResultCall()
	encoding, err := encodePersistedResult(ContextWithCall(ctx, frame), c, Result[Typed]{shared: res}, nil)
	if err != nil {
		return fmt.Errorf("encode result %d: %w", res.id, err)
	}

	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	res.payloadMu.Lock()
	if res.payloadRevision != state.payloadRevision {
		res.payloadMu.Unlock()
		return fmt.Errorf("result %d changed while encoding", res.id)
	}
	env := encoding.Envelope
	res.persistedEnvelope = &env
	res.self = nil
	res.hasValue = false
	res.payloadRevision++
	res.payloadMu.Unlock()
	c.setResultPayloadBytesLocked(res, persistedEnvelopePayloadBytes(&env))
	return nil
}

func dehydrateBulkLines(i int) []string {
	lines := make([]string, 32)
	for j := range lines {
		lines[j] = fmt.Sprintf("result-%06d-line-%02d-%s", i, j, strings.Repeat("y", 8))
	}
	return lines
}

func TestCacheSessionDehydrateColdResults(t *testing.T) {
	const calls = 5000
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := persistedListTestCache(t, path)
	srv.InstallObject(NewClass(srv, ClassOpts[*dehydrateBulkObj]{}))
	srv.InstallObject(NewClass(srv, ClassOpts[*persistChildLoadObj]{}))

	var executed int
	frames := make([]*ResultCall, calls)
	results := make([]AnyResult, calls)
	base := cacheGrowthHeap()
	for i := range calls {
		frame := persistCodecFrame(fmt.Sprintf("bulk-%d", i), &dehydrateBulkObj{})
		frames[i] = frame
		res, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
			executed++
			return NewObjectResultForCall(newDehydrateBulkObj(fmt.Sprintf("bulk-%d", i), dehydrateBulkLines(i)), srv, frame)
		})
		require.NoError(t, err)
		results[i] = res
	}
	require.Equal(t, calls, executed)
	hydrated := cacheGrowthHeap()

	// A dependent (persistChildLoadObj) holds result 0's value through a Go
	// field: result 0 may only go after its dependent has.
	childShared := results[0].cacheSharedResult()
	parentFrame := persistCodecFrame("bulk-parent", &persistChildLoadObj{})
	parent, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: parentFrame}, func(context.Context) (AnyResult, error) {
		executed++
		return NewObjectResultForCall(&persistChildLoadObj{Child: results[0]}, srv, parentFrame)
	})
	require.NoError(t, err)
	parentShared := parent.cacheSharedResult()
	require.ErrorIs(t, cache.testDehydrate(ctx, childShared), errDependentHydrated)
	require.NoError(t, cache.testDehydrate(ctx, parentShared))
	require.NoError(t, cache.testDehydrate(ctx, childShared))

	// The client keeps its handle IDs; this test keeps only them.
	handles := make([]uint64, calls)
	for i, res := range results {
		handles[i] = uint64(res.cacheSharedResult().id)
	}
	parentHandle := uint64(parentShared.id)
	resultsBefore := len(cache.resultsByID)
	estimateBefore := cache.MetadataEstimate()
	for _, id := range handles[1:] {
		require.NoError(t, cache.testDehydrate(ctx, cache.resultsByID[sharedResultID(id)]))
	}
	results = nil
	parent = nil
	dehydrated := cacheGrowthHeap()

	// Identity is untouched: same rows, still owned by the session.
	require.Equal(t, resultsBefore, len(cache.resultsByID))
	cache.sessionMu.Lock()
	require.Len(t, cache.sessionResultIDsBySession["test-session"], calls+1)
	cache.sessionMu.Unlock()
	t.Logf("%d results: hydrated +%.1f MB (%d B/result); dehydrated +%.1f MB (%d B/result), %d B/result freed",
		calls, float64(hydrated-base)/1e6, int64(hydrated-base)/calls,
		float64(dehydrated-base)/1e6, int64(dehydrated-base)/calls, (int64(hydrated)-int64(dehydrated))/calls)
	t.Logf("metadata estimate payload bytes: %d before, %d after (envelopes counted)", estimateBefore.PayloadBytes, cache.MetadataEstimate().PayloadBytes)

	// The dependent's handle decodes it, and its decode loads the child row
	// by number, which decodes the child in turn.
	loadedParent, err := cache.LoadResultByResultID(ctx, "test-session", srv, parentHandle)
	require.NoError(t, err)
	child := loadedParent.Unwrap().(*persistChildLoadObj).Child
	require.Equal(t, handles[0], uint64(child.cacheSharedResult().id))
	require.Equal(t, "bulk-0", child.Unwrap().(*dehydrateBulkObj).Name)

	// Every handle loads the same row with the same value, and every recipe
	// lookup hits it: nothing executes again.
	for i, id := range handles {
		loaded, err := cache.LoadResultByResultID(ctx, "test-session", srv, id)
		require.NoError(t, err)
		require.Equal(t, id, uint64(loaded.cacheSharedResult().id))
		obj := loaded.Unwrap().(*dehydrateBulkObj)
		require.Equal(t, fmt.Sprintf("bulk-%d", i), obj.Name)
		require.Equal(t, dehydrateBulkLines(i), obj.Lines)
		require.Equal(t, 7, obj.Index[obj.Lines[7]])
	}
	for i := 0; i < calls; i += 97 {
		frame := persistCodecFrame(fmt.Sprintf("bulk-%d", i), &dehydrateBulkObj{})
		hit, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
			return nil, errors.New("recomputed a dehydrated result")
		})
		require.NoError(t, err)
		require.True(t, hit.HitCache())
		require.Equal(t, handles[i], uint64(hit.cacheSharedResult().id))
	}
	require.Equal(t, calls+1, executed)

	// A second cycle works the same, from the reloaded value.
	require.NoError(t, cache.testDehydrate(ctx, cache.resultsByID[sharedResultID(handles[5])]))
	again, err := cache.LoadResultByResultID(ctx, "test-session", srv, handles[5])
	require.NoError(t, err)
	require.Equal(t, "bulk-5", again.Unwrap().(*dehydrateBulkObj).Name)

	require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
	require.NoError(t, cache.WaitSessionRelease(ctx, "test-session"))
	require.Empty(t, cache.resultsByID)
}

// TestCacheDehydrateColdResultsPass drives the measurement pass
// (DehydrateColdResults): it dehydrates a dependent before the result it holds
// through a field, leaves recently used results alone, and every handle and
// recipe lookup loads the same row with an equal value afterwards, with no
// resolver run again, through a second cycle too.
func TestCacheDehydrateColdResultsPass(t *testing.T) {
	const calls = 200
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := persistedListTestCache(t, path)
	srv.InstallObject(NewClass(srv, ClassOpts[*dehydrateBulkObj]{}))
	srv.InstallObject(NewClass(srv, ClassOpts[*persistChildLoadObj]{}))

	var executed int
	handles := make([]uint64, calls)
	var child AnyResult
	for i := range calls {
		frame := persistCodecFrame(fmt.Sprintf("pass-%d", i), &dehydrateBulkObj{})
		res, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
			executed++
			return NewObjectResultForCall(newDehydrateBulkObj(fmt.Sprintf("pass-%d", i), dehydrateBulkLines(i)), srv, frame)
		})
		require.NoError(t, err)
		handles[i] = uint64(res.cacheSharedResult().id)
		if i == 0 {
			child = res
		}
	}
	parentFrame := persistCodecFrame("pass-parent", &persistChildLoadObj{})
	parent, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: parentFrame}, func(context.Context) (AnyResult, error) {
		executed++
		return NewObjectResultForCall(&persistChildLoadObj{Child: child}, srv, parentFrame)
	})
	require.NoError(t, err)
	parentHandle := uint64(parent.cacheSharedResult().id)
	child, parent = nil, nil

	// Everything was just used: an hour's threshold leaves it all.
	report, err := cache.DehydrateColdResults(ctx, DehydrateOptions{UnusedFor: time.Hour})
	require.NoError(t, err)
	require.Zero(t, report.Dehydrated)
	require.Equal(t, calls+1, report.Skipped[dehydrateSkipRecentlyUsed])

	report, err = cache.DehydrateColdResults(ctx, DehydrateOptions{})
	require.NoError(t, err)
	require.Equal(t, calls+1, report.Dehydrated, "the child goes once its dependent has: %+v", report)
	require.Equal(t, calls, report.DehydratedByType["DehydrateBulkObj"])

	for cycle := range 2 {
		loadedParent, err := cache.LoadResultByResultID(ctx, "test-session", srv, parentHandle)
		require.NoError(t, err)
		require.Equal(t, handles[0], uint64(loadedParent.Unwrap().(*persistChildLoadObj).Child.cacheSharedResult().id))
		for i, id := range handles {
			loaded, err := cache.LoadResultByResultID(ctx, "test-session", srv, id)
			require.NoError(t, err)
			require.Equal(t, id, uint64(loaded.cacheSharedResult().id))
			obj := loaded.Unwrap().(*dehydrateBulkObj)
			require.Equal(t, fmt.Sprintf("pass-%d", i), obj.Name)
			require.Equal(t, dehydrateBulkLines(i), obj.Lines)
		}
		for i := 0; i < calls; i += 17 {
			frame := persistCodecFrame(fmt.Sprintf("pass-%d", i), &dehydrateBulkObj{})
			hit, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
				return nil, errors.New("recomputed a dehydrated result")
			})
			require.NoError(t, err)
			require.Equal(t, handles[i], uint64(hit.cacheSharedResult().id))
		}
		require.Equal(t, calls+1, executed, "cycle %d", cycle)
		if cycle == 0 {
			report, err = cache.DehydrateColdResults(ctx, DehydrateOptions{})
			require.NoError(t, err)
			require.Equal(t, calls+1, report.Dehydrated)
		}
	}

	require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
	require.NoError(t, cache.WaitSessionRelease(ctx, "test-session"))
	require.Empty(t, cache.resultsByID)
}

// TestCacheSessionDroppedValueIsNotRecomputed documents why the value may not
// simply be dropped on an engine (only a blob-backed cache's holdings mark an
// entry as having no value, noValueLocked): an object entry with neither a
// value nor an envelope fails every handle load and every recipe lookup
// ("missing typed payload") instead of running the call again; a scalar one
// would be served as a cached null. Recompute-in-place is not a path that
// exists for handle loads today.
func TestCacheSessionDroppedValueIsNotRecomputed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := persistedListTestCache(t, path)
	srv.InstallObject(NewClass(srv, ClassOpts[*dehydrateBulkObj]{}))

	var executed int
	frame := persistCodecFrame("dropped", &dehydrateBulkObj{})
	res, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		executed++
		return NewObjectResultForCall(newDehydrateBulkObj("dropped", dehydrateBulkLines(0)), srv, frame)
	})
	require.NoError(t, err)
	shared := res.cacheSharedResult()
	shared.payloadMu.Lock()
	shared.self = nil
	shared.hasValue = false
	shared.payloadRevision++
	shared.payloadMu.Unlock()

	_, err = cache.LoadResultByResultID(ctx, "test-session", srv, uint64(shared.id))
	require.ErrorContains(t, err, "missing typed payload")
	_, err = cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: persistCodecFrame("dropped", &dehydrateBulkObj{})}, func(context.Context) (AnyResult, error) {
		executed++
		return NewObjectResultForCall(newDehydrateBulkObj("dropped", dehydrateBulkLines(0)), srv, frame)
	})
	require.ErrorContains(t, err, "missing typed payload")
	require.Equal(t, 1, executed, "nothing recomputes")
}
