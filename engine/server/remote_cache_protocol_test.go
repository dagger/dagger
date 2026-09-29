package server

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/remotecache"
	"github.com/dagger/dagger/engine/remotecache/protocol"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
)

func newTestRemoteCacheAdapter(t *testing.T, cache *dagql.Cache) *RemoteCacheAdapter {
	t.Helper()
	bridge, _, err := cache.AttachRemoteCacheBridge()
	require.NoError(t, err)
	adapter := newRemoteCacheAdapter(cache, bridge)
	t.Cleanup(func() { adapter.close(errors.New("test done")) })
	return adapter
}

// A bundle captured from one engine cache merges into another through the
// adapter, which answers with the entry each record landed on and the roots
// it retains.
func TestRemoteCacheAdapterMergesABundle(t *testing.T) {
	source := newGCTestCache(t)
	ctx, res := addGCTestPersistableResult(t, source, "source", "merged", dagql.NewInt(7))
	var bundle dagql.ValueBundle
	require.NoError(t, source.WithExportedValues(ctx, dagql.ValueSelection{Roots: []dagql.AnyResult{res}}, config.RefConfig{}, func(_ context.Context, exp *dagql.ExportedValues) error {
		bundle = exp.Bundle
		return nil
	}))
	require.Len(t, bundle.Values, 1)

	target := newGCTestCache(t)
	adapter := newTestRemoteCacheAdapter(t, target)
	merged, err := adapter.Merge(t.Context(), protocol.Merge{Bundle: bundle})
	require.NoError(t, err)
	require.Equal(t, target.Identity().Generation, merged.Generation)
	require.NotZero(t, merged.EngineTime)
	require.Len(t, merged.Values, 1)
	value := merged.Values[0]
	require.Equal(t, bundle.Values[0].Ordinal, value.Ordinal)
	require.NotZero(t, value.Number)
	require.NotNil(t, value.Deps)
	require.NotNil(t, value.Complete)
	require.NotNil(t, value.Offered)
	require.Equal(t, []protocol.RetainedRoot{{Ordinal: bundle.Roots[0].Ordinal, ExpiresAtUnix: bundle.Roots[0].ExpiresAtUnix}}, merged.Retained)
	require.Empty(t, merged.Skipped)

	// The identity the integration says in hello is the cache's.
	cacheID, generation := adapter.CacheIdentity()
	require.Equal(t, target.Identity().ID, cacheID)
	require.Equal(t, target.Identity().Generation, generation)
}

// A merge that committed and then failed to release something is still
// answered with what it committed: the cache changed, and the service must
// learn how. The injected result is the one MergeValues returns when a
// release after its commit fails.
func TestRemoteCacheAdapterAnswersACommittedMergeDespiteAReleaseError(t *testing.T) {
	adapter := newTestRemoteCacheAdapter(t, newGCTestCache(t))
	adapter.mergeValues = func(context.Context, dagql.CacheID, dagql.ValueBundle) (dagql.MergeReply, error) {
		return dagql.MergeReply{
			Committed:          true,
			Generation:         4,
			EngineTimeUnixNano: 42,
			Values:             []dagql.MergedValue{{Ordinal: 1, Number: 40, Replacements: 1, ExpiresAtUnix: 99, Deps: []uint64{3}}},
			Roots: []dagql.MergedRoot{
				{Ordinal: 1, Number: 40, Retained: true, RetentionExpiresAtUnix: 99},
				{Ordinal: 2, Expired: true},
			},
		}, errors.New("release failed")
	}
	merged, err := adapter.Merge(t.Context(), protocol.Merge{})
	require.NoError(t, err, "a committed merge is answered merged")
	require.Equal(t, protocol.Merged{
		Generation: 4,
		EngineTime: 42,
		Values:     []protocol.MergedValue{{Ordinal: 1, Number: 40, Replacements: 1, ExpiresAtUnix: 99, Deps: []uint64{3}, Complete: []dagql.PersistedPartAddress{}, Offered: []dagql.PersistedPartAddress{}}},
		Retained:   []protocol.RetainedRoot{{Ordinal: 1, ExpiresAtUnix: 99}},
		Skipped:    []protocol.SkippedValue{{Ordinal: 2, Reason: protocol.SkipExpired}},
	}, merged)
}

// A merge that changed nothing is answered with its error.
func TestRemoteCacheAdapterMergeRefused(t *testing.T) {
	adapter := newTestRemoteCacheAdapter(t, newGCTestCache(t))
	adapter.mergeValues = func(context.Context, dagql.CacheID, dagql.ValueBundle) (dagql.MergeReply, error) {
		return dagql.MergeReply{}, errors.New("the bundle would close a cycle")
	}
	_, err := adapter.Merge(t.Context(), protocol.Merge{})
	require.ErrorContains(t, err, "cycle")
}

// adapterTestServer gives the core query the one server facility a file
// blob's body uses: the snapshot manager.
type adapterTestServer struct {
	coreServer
	manager bkcache.SnapshotManager
}

// coreServer names the embedded interface apart from its Server method.
type coreServer = core.Server

func (s adapterTestServer) SnapshotManager() bkcache.SnapshotManager { return s.manager }

// pausingManager holds the next body that creates a snapshot, once armed,
// until released.
type pausingManager struct {
	bkcache.SnapshotManager
	armed       atomic.Bool
	entered     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

// unpause lets a held body continue. It may be called more than once.
func (m *pausingManager) unpause() {
	m.releaseOnce.Do(func() { close(m.release) })
}

func (m *pausingManager) New(ctx context.Context, parent bkcache.ImmutableRef, opts ...bkcache.RefOption) (bkcache.MutableRef, error) {
	if m.armed.CompareAndSwap(true, false) {
		close(m.entered)
		select {
		case <-m.release:
		case <-ctx.Done():
		}
	}
	return m.SnapshotManager.New(ctx, parent, opts...)
}

// adapterTestEngine is an engine cache on a real snapshot store, holding
// core files, with its remote cache adapter.
type adapterTestEngine struct {
	ctx     context.Context
	cache   *dagql.Cache
	srv     *dagql.Server
	adapter *RemoteCacheAdapter
	bodies  *pausingManager
}

func newAdapterTestEngine(t *testing.T) *adapterTestEngine {
	t.Helper()
	store := testutil.NewStore(t)
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{ClientID: "adapter", SessionID: "adapter"})
	cache, err := dagql.NewCache(ctx, "", store.Manager, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	bodies := &pausingManager{SnapshotManager: store.Manager, entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(bodies.unpause)
	query := &core.Query{Server: adapterTestServer{manager: bodies}}
	ctx = core.ContextWithQuery(dagql.ContextWithCache(ctx, cache), query)
	srv, err := dagql.NewServer(ctx, query)
	require.NoError(t, err)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.File]{}))
	return &adapterTestEngine{ctx: ctx, cache: cache, srv: srv, adapter: newTestRemoteCacheAdapter(t, cache), bodies: bodies}
}

var adapterTestPlatform = core.Platform{OS: "linux", Architecture: "amd64"}

// file publishes a pending file whose body writes name's bytes.
func (e *adapterTestEngine) file(t *testing.T, name string) dagql.ObjectResult[*core.File] {
	t.Helper()
	res, _ := e.publishFile(t, name, nil)
	return res
}

// publishFile publishes a pending file whose body writes name's bytes, as a
// field of receiver when one is given. The decision it returns reads the
// file's state in the cache.
func (e *adapterTestEngine) publishFile(t *testing.T, name string, receiver dagql.AnyResult) (dagql.ObjectResult[*core.File], *dagql.CacheDecision) {
	t.Helper()
	file := &core.File{File: new(core.LazyAccessor[string, *core.File]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.File]), Platform: adapterTestPlatform,
		Lazy: &core.FileBlobLazy{LazyState: core.NewLazyState(), Filename: name + ".txt", Contents: []byte(name + " bytes"), Permissions: 0o644}}
	file.SetPath("/pending")
	call := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: name, Type: dagql.NewResultCallType(file.Type())}
	if receiver != nil {
		call.Receiver = &dagql.ResultCallRef{ResultID: entryNumber(t, receiver)}
	}
	decision := dagql.NewCacheDecision()
	res, err := e.cache.GetOrInitCall(e.ctx, "adapter", e.srv, &dagql.CallRequest{ResultCall: call, IsPersistable: true, CacheEvidence: decision}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(file, e.srv, call)
	})
	require.NoError(t, err)
	return res.(dagql.ObjectResult[*core.File]), decision
}

// busyFile publishes a file whose body is running, held until the test ends.
func (e *adapterTestEngine) busyFile(t *testing.T, name string) dagql.ObjectResult[*core.File] {
	t.Helper()
	busy := e.file(t, name)
	e.bodies.armed.Store(true)
	evaluated := make(chan error, 1)
	go func() { evaluated <- e.cache.Evaluate(e.ctx, busy) }()
	within(t, e.bodies.entered)
	// The body runs in the cache's own task, so it outlives its caller once
	// the test's context ends. Let it finish, and wait for it, before the
	// cache and the snapshot store close.
	t.Cleanup(func() {
		e.bodies.unpause()
		within(t, evaluated)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(e.ctx), 10*time.Second)
		defer cancel()
		require.NoError(t, e.cache.Evaluate(ctx, busy))
	})
	return busy
}

func entryNumber(t *testing.T, res dagql.AnyResult) uint64 {
	t.Helper()
	number, ok := dagql.CacheResultNumber(res)
	require.True(t, ok)
	return number
}

var snapshotPart = dagql.PersistedPartAddress{Part: "snapshot"}

// goneNumber is a number the cache never registered.
const goneNumber = 1 << 40

// bundleRoots are the sender numbers of a bundle's roots.
func bundleRoots(bundle *dagql.ValueBundle) []uint64 {
	var roots []uint64
	for _, root := range bundle.Roots {
		for _, value := range bundle.Values {
			if value.Ordinal == root.Ordinal {
				roots = append(roots, value.SenderNumber)
			}
		}
	}
	return roots
}

// An export holds the numbers it is asked for, skips the gone ones, and hands
// consume the bundle with the selected output's chain open: every layer
// reads back as the blob it names.
func TestRemoteCacheAdapterExport(t *testing.T) {
	e := newAdapterTestEngine(t)
	ready := e.file(t, "ready")
	require.NoError(t, e.cache.Evaluate(e.ctx, ready))
	req := protocol.Export{
		Roots:   []uint64{entryNumber(t, ready), goneNumber},
		Outputs: []protocol.ExportOutput{{Number: entryNumber(t, ready), Address: snapshotPart}, {Number: goneNumber, Address: snapshotPart}},
	}
	calls := 0
	err := e.adapter.Export(e.ctx, req, func(ctx context.Context, exp remotecache.Export) error {
		calls++
		require.Equal(t, []protocol.SkippedRoot{{Number: goneNumber, Reason: protocol.SkipGone}}, exp.Skipped)
		require.NotNil(t, exp.Bundle)
		require.Equal(t, []uint64{entryNumber(t, ready)}, bundleRoots(exp.Bundle))
		require.Len(t, exp.Bundle.Outputs, 1)
		chain := exp.Bundle.Outputs[0].Chain
		require.NotNil(t, chain)
		require.NotEmpty(t, chain.Layers)
		for _, layer := range chain.Layers {
			body, size, err := exp.Blobs.ReadBlob(ctx, layer.Descriptor.Digest)
			require.NoError(t, err)
			data, err := io.ReadAll(body)
			require.NoError(t, err)
			require.NoError(t, body.Close())
			require.Equal(t, layer.Descriptor.Size, size)
			require.Equal(t, layer.Descriptor.Digest, digest.FromBytes(data))
		}
		_, _, err := exp.Blobs.ReadBlob(ctx, digest.FromString("another blob"))
		require.ErrorContains(t, err, "not in the export's chains")
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

// A root whose body is running fails the capture. Each root is then probed
// alone: the busy one is skipped as busy, and the others go in one bundle
// with only the outputs their closures hold.
func TestRemoteCacheAdapterExportSkipsBusyRoots(t *testing.T) {
	e := newAdapterTestEngine(t)
	ready := e.file(t, "ready")
	require.NoError(t, e.cache.Evaluate(e.ctx, ready))
	busy := e.busyFile(t, "busy")
	req := protocol.Export{
		Roots:   []uint64{entryNumber(t, busy), entryNumber(t, ready)},
		Outputs: []protocol.ExportOutput{{Number: entryNumber(t, busy), Address: snapshotPart}, {Number: entryNumber(t, ready), Address: snapshotPart}},
	}
	calls := 0
	err := e.adapter.Export(e.ctx, req, func(_ context.Context, exp remotecache.Export) error {
		calls++
		require.Equal(t, []protocol.SkippedRoot{{Number: entryNumber(t, busy), Reason: protocol.SkipBusy}}, exp.Skipped)
		require.NotNil(t, exp.Bundle)
		require.Equal(t, []uint64{entryNumber(t, ready)}, bundleRoots(exp.Bundle))
		require.Len(t, exp.Bundle.Outputs, 1, "the busy root's output lies outside the closure, and is left out")
		require.Equal(t, exp.Bundle.Roots[0].Ordinal, exp.Bundle.Outputs[0].Ordinal)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

// With no root left, consume is called once, with the skipped roots and no
// bundle.
func TestRemoteCacheAdapterExportWithNoSurvivingRoot(t *testing.T) {
	e := newAdapterTestEngine(t)
	busy := e.busyFile(t, "busy")
	calls := 0
	err := e.adapter.Export(e.ctx, protocol.Export{Roots: []uint64{entryNumber(t, busy), goneNumber}}, func(_ context.Context, exp remotecache.Export) error {
		calls++
		require.Nil(t, exp.Bundle)
		require.Equal(t, []protocol.SkippedRoot{{Number: goneNumber, Reason: protocol.SkipGone}, {Number: entryNumber(t, busy), Reason: protocol.SkipBusy}}, exp.Skipped)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

// Offers go to entries by number: a pending part accepts its offer, a
// complete one is already complete, and a missing number is gone. An item
// carries its entry's replacement count and dependencies.
func TestRemoteCacheAdapterOfferParts(t *testing.T) {
	e := newAdapterTestEngine(t)
	complete := e.file(t, "complete")
	require.NoError(t, e.cache.Evaluate(e.ctx, complete))
	pending, decision := e.publishFile(t, "pending", complete)
	state, ok := decision.ResultState(e.ctx, pending)
	require.True(t, ok)
	require.Equal(t, []uint64{entryNumber(t, complete)}, state.Deps, "the pending file depends on its receiver")
	other := testutil.NewStore(t)
	ref, _ := other.Build(t, nil, "offered.txt", "offered bytes")
	chain, err := ref.ExportChain(t.Context(), config.RefConfig{Compression: compression.New(compression.Uncompressed)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, chain.Release(context.Background())) })
	spec := adapterTestPlatform.Spec()
	offer := protocol.CloudPart{
		Offer:       dagql.PersistedPartOffer{Address: snapshotPart, Value: dagql.SnapshotValue{Kind: "file", Path: "/offered.txt", Platform: &spec}, Chain: dagql.OfferedChain{Layers: chain.Layers, RenewalKey: "7/snapshot"}},
		CloudNumber: 7,
	}
	offered, err := e.adapter.OfferParts(e.ctx, protocol.Offer{Items: []protocol.OfferItem{
		{Number: entryNumber(t, pending), Offers: []protocol.CloudPart{offer}},
		{Number: entryNumber(t, complete), Offers: []protocol.CloudPart{offer}},
		{Number: goneNumber, Offers: []protocol.CloudPart{offer}},
	}})
	require.NoError(t, err)
	require.Equal(t, protocol.Offered{Items: []protocol.OfferedItem{
		{Number: entryNumber(t, pending), Replacements: state.Replacements, Deps: state.Deps, Parts: []protocol.OfferedPart{{Address: snapshotPart, Outcome: protocol.OfferAccepted}}},
		{Number: entryNumber(t, complete), Parts: []protocol.OfferedPart{{Address: snapshotPart, Outcome: protocol.OfferAlreadyComplete}}},
		{Number: goneNumber, Gone: true},
	}}, offered)
}

// attachBlockedInt is an Int whose dependency attachment waits for release,
// as a publication does while it attaches a value's dependencies.
type attachBlockedInt struct {
	dagql.Int
	attaching chan struct{}
	release   chan struct{}
}

func (v attachBlockedInt) AttachDependencyResults(context.Context, dagql.AnyResult, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	close(v.attaching)
	<-v.release
	return nil, nil
}

// A merge waiting for a target's attachment to settle returns when its
// context is cancelled, having changed nothing, so the integration's Run can
// return promptly mid-merge.
func TestRemoteCacheAdapterMergeReturnsWhenCancelled(t *testing.T) {
	source := newGCTestCache(t)
	ctx, res := addGCTestPersistableResult(t, source, "source", "merged", dagql.NewInt(7))
	var bundle dagql.ValueBundle
	require.NoError(t, source.WithExportedValues(ctx, dagql.ValueSelection{Roots: []dagql.AnyResult{res}}, config.RefConfig{}, func(_ context.Context, exp *dagql.ExportedValues) error {
		bundle = exp.Bundle
		return nil
	}))

	target := newGCTestCache(t)
	adapter := newTestRemoteCacheAdapter(t, target)
	value := attachBlockedInt{Int: dagql.NewInt(8), attaching: make(chan struct{}), release: make(chan struct{})}
	published := make(chan error, 1)
	go func() {
		ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{ClientID: "target", SessionID: "target"})
		frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Type: dagql.NewResultCallType(value.Type()), Field: "merged"}
		_, err := target.GetOrInitCall(ctx, "target", gcTestTypeResolver{}, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
			return dagql.NewResultForCall(value, frame)
		})
		published <- err
	}()
	within(t, value.attaching)
	defer func() {
		close(value.release)
		within(t, published)
	}()

	mergeCtx, cancel := context.WithCancel(t.Context())
	merged := make(chan error, 1)
	go func() {
		_, err := adapter.Merge(mergeCtx, protocol.Merge{Bundle: bundle})
		merged <- err
	}()
	select {
	case err := <-merged:
		t.Fatalf("the merge did not wait for the attachment: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-merged:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("the merge did not return when cancelled")
	}
}
