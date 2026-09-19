package core

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
)

// pausedBodyManager counts native body entries, recognized by their mutable
// ref, and can hold the first entry until released.
type pausedBodyManager struct {
	bkcache.SnapshotManager
	bodies  atomic.Int64
	pause   bool
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (m *pausedBodyManager) New(ctx context.Context, parent bkcache.ImmutableRef, opts ...bkcache.RefOption) (bkcache.MutableRef, error) {
	m.bodies.Add(1)
	if m.pause {
		m.once.Do(func() {
			close(m.entered)
			select {
			case <-m.release:
			case <-ctx.Done():
			}
		})
	}
	return m.SnapshotManager.New(ctx, parent, opts...)
}

func waitWithin[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting")
		panic("unreachable")
	}
}

// TestOfferPartsNativeAdmission uses a real pending native File. An offer
// accepted before its body starts is installed by the demand without entering
// the body; an offer arriving after the body starts is refused and the body
// finishes.
func TestOfferPartsNativeAdmission(t *testing.T) {
	platform := Platform{OS: "linux", Architecture: "amd64"}
	setup := func(t *testing.T, pause bool) (context.Context, *dagql.Cache, dagql.ObjectResult[*File], *pausedBodyManager, func(), dagql.PersistedPartOffer, *testutil.Provider) {
		t.Helper()
		aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
		ref, _ := aStore.Build(t, nil, "offered.txt", "offered bytes")
		chain, err := ref.ExportChain(t.Context(), config.RefConfig{Compression: compression.New(compression.Uncompressed)})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, chain.Release(context.Background())) })
		manager := &pausedBodyManager{SnapshotManager: bStore.Manager, pause: pause, entered: make(chan struct{}), release: make(chan struct{})}
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(manager.release) }) }
		t.Cleanup(release)
		bStore.Manager = manager
		ctx, cache, srv := transferCache(t, bStore, filepath.Join(t.TempDir(), "b.db"), "b")
		file := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Platform: platform, Lazy: &FileBlobLazy{LazyState: NewLazyState(), Filename: "produced.txt", Contents: []byte("operation bytes"), Permissions: 0644}}
		file.SetPath("/pending-preseed")
		result := attachTransferObject(t, ctx, cache, srv, "b", "partFile", file)
		provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
		// This case is about admission; bytes come through the test override.
		cache.SetPartContentSource(partTestContentSource{provider})
		spec := platform.Spec()
		offer := dagql.PersistedPartOffer{Address: dagql.PersistedPartAddress{Part: "snapshot"}, Value: dagql.SnapshotValue{Kind: "file", Path: "/offered.txt", Platform: &spec}, Chain: dagql.OfferedChain{Layers: chain.Layers}}
		return ctx, cache, result, manager, release, offer, provider
	}
	t.Run("accepted before start", func(t *testing.T) {
		ctx, cache, result, manager, _, offer, provider := setup(t, false)
		out, err := cache.OfferParts(ctx, result, []dagql.PersistedPartOffer{offer})
		require.NoError(t, err)
		require.Equal(t, dagql.OfferAccepted, out[0].Outcome)
		require.Zero(t, manager.bodies.Load(), "acceptance enters no body")
		require.Zero(t, provider.Reads.Load(), "acceptance reads no content")
		require.NoError(t, cache.Evaluate(ctx, result))
		require.Zero(t, manager.bodies.Load(), "the demand installed the offer instead of entering the body")
		require.EqualValues(t, 1, provider.Reads.Load())
		contents := demandedFileContents(t, ctx, result)
		require.Equal(t, "offered bytes", string(contents))
		require.Equal(t, "/offered.txt", mustTransferPath(t, ctx, result))
		record, err := cache.CapturePersistedRecord(ctx, result)
		require.NoError(t, err)
		require.Empty(t, record.Envelope.PendingOffers, "settlement retired the installed offer")
		require.Zero(t, manager.bodies.Load())
	})
	t.Run("refused after start", func(t *testing.T) {
		ctx, cache, result, manager, release, offer, provider := setup(t, true)
		evaluated := make(chan error, 1)
		go func() { evaluated <- cache.Evaluate(ctx, result) }()
		waitWithin(t, manager.entered)
		out, err := cache.OfferParts(ctx, result, []dagql.PersistedPartOffer{offer})
		require.NoError(t, err)
		require.Equal(t, dagql.OfferExecutionStarted, out[0].Outcome)
		release()
		require.NoError(t, waitWithin(t, evaluated))
		require.EqualValues(t, 1, manager.bodies.Load())
		require.Zero(t, provider.Reads.Load())
		contents := demandedFileContents(t, ctx, result)
		require.Equal(t, "operation bytes", string(contents))
		record, err := cache.CapturePersistedRecord(ctx, result)
		require.NoError(t, err)
		require.Empty(t, record.Envelope.PendingOffers, "a refused offer never attaches")
	})
}
