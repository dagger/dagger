package schema

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/stretchr/testify/require"
)

var scratchTestFailure = errors.New("injected scratch acquisition failure")

type scratchObservedRef struct {
	bkcache.ImmutableRef
	releases atomic.Int64
	canceled atomic.Bool
}

func (r *scratchObservedRef) Release(ctx context.Context) error {
	r.releases.Add(1)
	r.canceled.Store(ctx.Err() != nil)
	return r.ImmutableRef.Release(ctx)
}

type scratchObservedManager struct {
	bkcache.SnapshotManager
	calls        atomic.Int64
	failScratch  atomic.Bool
	failCanceled atomic.Bool
	failPin      atomic.Bool
	failSync     atomic.Bool
	afterScratch func()
	mu           sync.Mutex
	refs         []*scratchObservedRef
}

func (m *scratchObservedManager) observe(ref bkcache.ImmutableRef, err error) (bkcache.ImmutableRef, error) {
	if err != nil {
		return nil, err
	}
	r := &scratchObservedRef{ImmutableRef: ref}
	m.mu.Lock()
	m.refs = append(m.refs, r)
	m.mu.Unlock()
	return r, nil
}

func (m *scratchObservedManager) Scratch(ctx context.Context) (bkcache.ImmutableRef, error) {
	m.calls.Add(1)
	if m.failCanceled.Swap(false) {
		return nil, context.Canceled
	}
	if m.failScratch.Swap(false) {
		return nil, scratchTestFailure
	}
	ref, err := m.observe(m.SnapshotManager.Scratch(ctx))
	if m.afterScratch != nil {
		m.afterScratch()
	}
	return ref, err
}

func (m *scratchObservedManager) GetBySnapshotID(ctx context.Context, id string, opts ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	return m.observe(m.SnapshotManager.GetBySnapshotID(ctx, id, opts...))
}

func (m *scratchObservedManager) PinSnapshot(ctx context.Context, id string) (bkcache.ImmutableRef, error) {
	if m.failPin.Swap(false) {
		return nil, scratchTestFailure
	}
	return m.observe(m.SnapshotManager.PinSnapshot(ctx, id))
}

func (m *scratchObservedManager) AttachLease(ctx context.Context, owner, id string) error {
	if err := m.SnapshotManager.AttachLease(ctx, owner, id); err != nil {
		return err
	}
	if m.failSync.Swap(false) {
		return scratchTestFailure
	}
	return nil
}

type scratchTestServer struct {
	*currentTypeDefsTestServer
	manager bkcache.SnapshotManager
}

func (s *scratchTestServer) SnapshotManager() bkcache.SnapshotManager { return s.manager }

func scratchTestCache(t *testing.T, store *testutil.Store, path, session string) (context.Context, *dagql.Cache, *dagql.Server) {
	t.Helper()
	server := &scratchTestServer{currentTypeDefsTestServer: &currentTypeDefsTestServer{platform: core.Platform{OS: "linux", Architecture: "arm64"}}, manager: store.Manager}
	query := core.NewRoot(server)
	ctx := core.ContextWithQuery(t.Context(), query)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: session, SessionID: session})
	cache, err := dagql.NewCache(ctx, path, store.Manager, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	ctx = dagql.ContextWithCache(ctx, cache)
	srv, err := dagql.NewServer(ctx, query)
	require.NoError(t, err)
	server.dag = srv
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.Directory]{}))
	// Use the actual resolver and actual scalar identity input.
	dagql.Fields[*core.Query]{dagql.NodeFunc("directory", (&directorySchema{}).directory).WithInput(engineDefaultPlatformInput)}.Install(srv)
	cache.EnableTransferFixtureParts()
	return ctx, cache, srv
}

func scratchSelect(t *testing.T, ctx context.Context, srv *dagql.Server) dagql.ObjectResult[*core.Directory] {
	t.Helper()
	var result dagql.ObjectResult[*core.Directory]
	require.NoError(t, srv.Select(ctx, srv.Root(), &result, dagql.Selector{Field: "directory"}))
	return result
}

func scratchCount(t *testing.T, ctx context.Context, cache *dagql.Cache, session string, row uint64) int {
	t.Helper()
	report, err := cache.TransferFixtureSnapshot(ctx, session, nil)
	require.NoError(t, err)
	count := 0
	for _, event := range report.Parts {
		if event.ResultID != row {
			continue
		}
		require.NotEqual(t, "provider-read", event.Kind)
		require.NotEqual(t, "installed-chain", event.Kind)
		if event.Kind == "producer-enter" {
			require.Equal(t, "directory", event.Field)
			require.Equal(t, dagql.PersistedPartAddress{Part: "snapshot"}, event.Address)
			count++
		}
	}
	return count
}

func TestScratchDirectoryProducer(t *testing.T) {
	store := testutil.NewStore(t)
	observed := &scratchObservedManager{SnapshotManager: store.Manager}
	store.Manager = observed
	ctx, cache, srv := scratchTestCache(t, store, "", "eager")
	result := scratchSelect(t, ctx, srv)
	require.IsType(t, &core.DirectoryScratchLazy{}, result.Self().Lazy)
	require.False(t, result.Self().Lazy.IsEvaluated())
	require.Zero(t, observed.calls.Load())
	record, err := cache.CapturePersistedRecord(ctx, result)
	require.NoError(t, err)
	var payload struct {
		Form, LazyKind string
		LazyJSON       json.RawMessage
		Platform       core.Platform
	}
	require.NoError(t, json.Unmarshal(record.Envelope.ObjectJSON, &payload))
	require.Equal(t, "lazy", payload.Form)
	require.Equal(t, "scratch", payload.LazyKind)
	require.Equal(t, "{}", string(payload.LazyJSON))
	var withoutRecipe map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(record.Envelope.ObjectJSON, &withoutRecipe))
	delete(withoutRecipe, "lazyKind")
	delete(withoutRecipe, "lazyJSON")
	baseline, err := json.Marshal(withoutRecipe)
	require.NoError(t, err)
	t.Logf("scratch recipe-json-bytes=%d native-payload-added-bytes=%d construction-scratch-calls=%d", len(payload.LazyJSON), len(record.Envelope.ObjectJSON)-len(baseline), observed.calls.Load())
	require.Equal(t, "linux/arm64", payload.Platform.Format())
	require.Nil(t, record.Call.Receiver)
	require.Len(t, record.Call.ImplicitInputs, 1)
	require.Equal(t, "engineDefaultPlatform", record.Call.ImplicitInputs[0].Name)
	require.Equal(t, "linux/arm64", record.Call.ImplicitInputs[0].Value.StringValue)
	report, err := cache.TransferFixtureSnapshot(ctx, "eager", nil)
	require.NoError(t, err)
	for _, row := range report.Rows {
		if row.ResultID == record.ResultID {
			require.Empty(t, row.DependencyIDs)
		}
	}
	require.NoError(t, cache.Evaluate(ctx, result))
	require.Zero(t, scratchCount(t, ctx, cache, "eager", record.ResultID))
	require.EqualValues(t, 1, observed.calls.Load(), "capture/own completion opened scratch")
	require.ErrorContains(t, (&core.DirectoryScratchLazy{LazyState: core.NewLazyState()}).Evaluate(ctx, result.Self()), "already installed")
	require.EqualValues(t, 1, observed.calls.Load())

	// Two fresh recipe instances have independent latches/refs, while each
	// individual instance serializes its body under concurrent evaluation.
	for range 2 {
		decoded, err := (&core.Directory{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, 0, nil), json.RawMessage(`{"form":"lazy","platform":"linux/arm64","lazyKind":"scratch","lazyJSON":{}}`))
		require.NoError(t, err)
		private := decoded.(*core.Directory)
		lazy := private.Lazy.(*core.DirectoryScratchLazy)
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for range 8 {
			wg.Go(func() { errs <- lazy.Evaluate(ctx, private) })
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		require.Equal(t, payload.Platform, private.Platform)
		require.NoError(t, private.OnRelease(ctx))
	}
	require.EqualValues(t, 3, observed.calls.Load())
	for _, ref := range observed.refs[1:] {
		require.EqualValues(t, 1, ref.releases.Load())
	}

	// Cancellation after Scratch returned a ref must unwind inside the latch.
	canceled, cancel := context.WithCancel(ctx)
	observed.afterScratch = cancel
	private := &core.Directory{Platform: payload.Platform, Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
	err = (&core.DirectoryScratchLazy{LazyState: core.NewLazyState()}).Evaluate(canceled, private)
	require.ErrorIs(t, err, context.Canceled)
	_, set := private.Snapshot.Peek()
	require.False(t, set)
	last := observed.refs[len(observed.refs)-1]
	require.EqualValues(t, 1, last.releases.Load())
	require.False(t, last.canceled.Load())
}

func TestScratchDirectoryAcquisition(t *testing.T) {
	for _, mode := range []string{"cold", "canonical-only", "warm", "pending-restart", "manager-failure", "manager-canceled", "prepare-failure", "sync-retry"} {
		t.Run(mode, func(t *testing.T) {
			aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
			actx, a, asrv := scratchTestCache(t, aStore, "", "a")
			original := scratchSelect(t, actx, asrv)
			var bundle *dagql.ValueBundle
			require.NoError(t, a.WithExportedValues(actx, dagql.ValueSelection{Roots: []dagql.AnyResult{original}}, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
				require.Empty(t, exported.Chains.Entries)
				raw, err := json.Marshal(exported.Bundle)
				require.NoError(t, err)
				return json.Unmarshal(raw, &bundle)
			}))
			require.Len(t, bundle.Values, 1)
			require.Empty(t, bundle.Outputs)
			observed := &scratchObservedManager{SnapshotManager: bStore.Manager}
			bStore.Manager = observed
			path := filepath.Join(t.TempDir(), "b.db")
			ctx, b, srv := scratchTestCache(t, bStore, path, "b")
			var donor *scratchObservedRef
			if mode == "warm" {
				local := scratchSelect(t, ctx, srv)
				require.NoError(t, b.Evaluate(ctx, local))
				ref, _ := local.Self().Snapshot.Peek()
				donor = ref.(*scratchObservedRef)
			} else if mode == "canonical-only" {
				ref, err := observed.Scratch(ctx)
				require.NoError(t, err)
				require.NoError(t, ref.Release(ctx))
			}
			observed.calls.Store(0)
			// The imported row's arm64 platform must survive an amd64 B default.
			srv.Root().(dagql.ObjectResult[*core.Query]).Self().Server.(*scratchTestServer).platform = core.Platform{OS: "linux", Architecture: "amd64"}
			imported, err := b.ImportValues(ctx, *bundle)
			require.NoError(t, err)
			row := imported[0].ResultID
			reopen := func() {
				require.NoError(t, b.ReleaseSession(ctx, "b"))
				require.NoError(t, b.Close(ctx))
				bStore.Reload(t)
				observed = &scratchObservedManager{SnapshotManager: bStore.Manager}
				bStore.Manager = observed
				ctx, b, srv = scratchTestCache(t, bStore, path, "b")
				srv.Root().(dagql.ObjectResult[*core.Query]).Self().Server.(*scratchTestServer).platform = core.Platform{OS: "linux", Architecture: "amd64"}
				require.Equal(t, dagql.CachePersistenceResetNone, b.PersistenceResetReason())
				require.Zero(t, observed.calls.Load())
			}
			if mode == "pending-restart" {
				reopen()
			}
			loaded, err := b.LoadResultByResultID(ctx, "", srv, row)
			require.NoError(t, err)
			result := loaded.(dagql.ObjectResult[*core.Directory])
			_, err = b.AttachResult(ctx, "receiver", srv, result)
			require.NoError(t, err)
			if mode == "manager-failure" {
				observed.failScratch.Store(true)
			}
			if mode == "manager-canceled" {
				observed.failCanceled.Store(true)
			}
			if mode == "prepare-failure" {
				observed.failPin.Store(true)
			}
			if mode == "sync-retry" {
				observed.failSync.Store(true)
			}
			started := time.Now()
			if mode == "manager-failure" || mode == "manager-canceled" || mode == "prepare-failure" || mode == "sync-retry" {
				err := b.Evaluate(ctx, result)
				if mode == "manager-canceled" {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					require.ErrorIs(t, err, scratchTestFailure)
				}
				if mode == "sync-retry" {
					_, err := b.CapturePersistedRecord(ctx, result)
					require.ErrorIs(t, err, dagql.ErrPersistStateNotReady)
				} else {
					record, err := b.CapturePersistedRecord(ctx, result)
					require.NoError(t, err)
					require.Empty(t, record.SnapshotLinks, "failure before Commit installed a ref")
					for _, ref := range observed.refs {
						require.EqualValues(t, 1, ref.releases.Load())
					}
				}
			}
			var wg sync.WaitGroup
			errs := make(chan error, 12)
			for range 12 {
				wg.Go(func() { errs <- b.Evaluate(ctx, result) })
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
			want := 1
			if mode == "warm" {
				want = 0
			}
			if mode == "manager-failure" || mode == "manager-canceled" || mode == "prepare-failure" {
				want = 2
			}
			require.EqualValues(t, want, observed.calls.Load())
			require.Equal(t, want, scratchCount(t, ctx, b, "b", row))
			t.Logf("scratch mode=%s acquisition=%s scratch-calls=%d provider-reads=0", mode, time.Since(started), observed.calls.Load())
			entries, err := result.Self().Entries(ctx, result, "")
			require.NoError(t, err)
			require.Empty(t, entries)
			require.Equal(t, "linux/arm64", result.Self().Platform.Format())
			record, err := b.CapturePersistedRecord(ctx, result)
			require.NoError(t, err)
			require.Len(t, record.SnapshotLinks, 1)
			require.Contains(t, string(record.Envelope.ObjectJSON), `"lazyKind":"scratch"`)
			require.Contains(t, string(record.Envelope.ObjectJSON), `"lazyJSON":{}`)
			ref, set := result.Self().Snapshot.Peek()
			require.True(t, set)
			installed := ref.(*scratchObservedRef)
			require.Zero(t, installed.releases.Load())
			for _, temporary := range observed.refs {
				if temporary != installed && temporary != donor {
					require.EqualValues(t, 1, temporary.releases.Load(), "private output or temporary pin survived completed acquisition")
				}
			}
			// Drop A's rows and manager, independently of B's canonical lease.
			require.NoError(t, a.ReleaseSession(actx, "a"))
			_, err = a.Prune(actx, []dagql.CachePrunePolicy{{All: true}})
			require.NoError(t, err)
			require.NoError(t, a.CloseDiscardingPersistence())
			require.NoError(t, aStore.Manager.Close())
			if donor != nil {
				require.NotSame(t, donor, installed)
				require.NoError(t, b.ReleaseSession(ctx, "b"))
				_, err = b.Prune(ctx, []dagql.CachePrunePolicy{{All: true}})
				require.NoError(t, err)
				require.EqualValues(t, 1, donor.releases.Load())
				ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "receiver", SessionID: "receiver"})
			}
			entries, err = result.Self().Entries(ctx, result, "")
			require.NoError(t, err)
			require.Empty(t, entries)
			require.Zero(t, installed.releases.Load())
			if mode == "pending-restart" {
				require.NoError(t, b.ReleaseSession(ctx, "receiver"))
				reopen()
				loaded, err = b.LoadResultByResultID(ctx, "", srv, row)
				require.NoError(t, err)
				result = loaded.(dagql.ObjectResult[*core.Directory])
				require.NoError(t, b.Evaluate(ctx, result))
				require.Zero(t, observed.calls.Load())
				require.Zero(t, scratchCount(t, ctx, b, "b", row))
				restored, err := b.CapturePersistedRecord(ctx, result)
				require.NoError(t, err)
				require.JSONEq(t, string(record.Envelope.ObjectJSON), string(restored.Envelope.ObjectJSON))
				ref, set = result.Self().Snapshot.Peek()
				require.True(t, set)
				installed = ref.(*scratchObservedRef)
			}
			require.NoError(t, b.ReleaseSession(ctx, "b"))
			require.NoError(t, b.ReleaseSession(ctx, "receiver"))
			_, err = b.Prune(ctx, []dagql.CachePrunePolicy{{All: true}})
			require.NoError(t, err)
			require.EqualValues(t, 1, installed.releases.Load(), "final row release must release its own accessor")
		})
	}
}

func TestScratchDirectoryNativeReopen(t *testing.T) {
	for _, evaluated := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "evaluated"}[evaluated], func(t *testing.T) {
			testScratchDirectoryNativeReopen(t, evaluated)
		})
	}
}

func testScratchDirectoryNativeReopen(t *testing.T, evaluated bool) {
	store := testutil.NewStore(t)
	observed := &scratchObservedManager{SnapshotManager: store.Manager}
	store.Manager = observed
	path := filepath.Join(t.TempDir(), "native.db")
	ctx, cache, srv := scratchTestCache(t, store, path, "native")
	result := scratchSelect(t, ctx, srv)
	if evaluated {
		require.NoError(t, cache.Evaluate(ctx, result))
	}
	require.EqualValues(t, map[bool]int{false: 0, true: 1}[evaluated], observed.calls.Load())
	record, err := cache.CapturePersistedRecord(ctx, result)
	require.NoError(t, err)
	// Keep the Directory as the dependency of a persisted root.
	// This changes neither its field options nor its recorded call.
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "keepScratch", Type: dagql.NewResultCallType(dagql.Int(1).Type()), Args: []*dagql.ResultCallArg{{Name: "directory", Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindResultRef, ResultRef: &dagql.ResultCallRef{ResultID: record.ResultID}}}}}
	_, err = cache.GetOrInitCall(ctx, "native", srv, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) { return dagql.NewResultForCall(dagql.Int(1), frame) })
	require.NoError(t, err)
	for attempt := range 2 {
		require.NoError(t, cache.ReleaseSession(ctx, "native"))
		require.NoError(t, cache.Close(ctx))
		store.Reload(t)
		observed = &scratchObservedManager{SnapshotManager: store.Manager}
		store.Manager = observed
		ctx, cache, srv = scratchTestCache(t, store, path, "native")
		require.Equal(t, dagql.CachePersistenceResetNone, cache.PersistenceResetReason())
		loaded, err := cache.LoadResultByResultID(ctx, "native", srv, record.ResultID)
		require.NoError(t, err)
		restored, err := cache.CapturePersistedRecord(ctx, loaded)
		require.NoError(t, err)
		require.JSONEq(t, string(record.Envelope.ObjectJSON), string(restored.Envelope.ObjectJSON))
		require.Empty(t, observed.refs, "capture opened a local ref")
		require.NoError(t, cache.Evaluate(ctx, loaded))
		if !evaluated && attempt == 0 {
			require.EqualValues(t, 1, observed.calls.Load())
		} else {
			require.Zero(t, observed.calls.Load())
		}
		record, err = cache.CapturePersistedRecord(ctx, loaded)
		require.NoError(t, err)
		require.Zero(t, scratchCount(t, ctx, cache, "native", record.ResultID))
	}
}
