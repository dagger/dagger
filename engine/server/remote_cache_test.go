package server

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// renewalOnlyOffer is available only while a bridge is attached.
func renewalOnlyOffer() dagql.PersistedPartOffer {
	layer := snapshots.ExportLayer{Descriptor: ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayer, Digest: digest.FromString("blob"), Size: 4}}
	return dagql.PersistedPartOffer{Address: dagql.PersistedPartAddress{Part: "snapshot"}, Value: dagql.SnapshotValue{Kind: "directory"}, Chain: dagql.OfferedChain{Layers: []snapshots.ExportLayer{layer}, RenewalKey: "key"}}
}

// within bounds a wait so a failure cannot hang the package.
func within[T any](t *testing.T, ch <-chan T) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting")
	}
}

func boundedContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func bridgeAttached(cache *dagql.Cache) bool {
	return cache.PartContentSource().Available(renewalOnlyOffer(), time.Now())
}

func TestRemoteCacheIntegrationConfig(t *testing.T) {
	require.NoError(t, validateRemoteCacheIntegration(nil))
	require.ErrorContains(t, validateRemoteCacheIntegration(&RemoteCacheIntegrationConfig{}), "requires Run")
	_, err := NewServer(t.Context(), &NewServerOpts{RemoteCacheIntegration: &RemoteCacheIntegrationConfig{}})
	require.ErrorContains(t, err, "requires Run")

	cache := newGCTestCache(t)
	srv := &Server{engineCache: cache, shutdownCtx: t.Context()}
	require.NoError(t, srv.startRemoteCacheIntegration(nil))
	require.Nil(t, srv.remoteCacheAdapter)
	require.False(t, bridgeAttached(cache), "a nil config attaches no mailbox")
	require.NoError(t, srv.stopRemoteCacheIntegration(t.Context()))

	// An existing attachment is not a new one; Run starts only for created.
	bridge, created, err := cache.AttachRemoteCacheBridge()
	require.NoError(t, err)
	require.True(t, created)
	var runs atomic.Int32
	require.NoError(t, srv.startRemoteCacheIntegration(&RemoteCacheIntegrationConfig{Run: func(context.Context, *RemoteCacheAdapter) error {
		runs.Add(1)
		return nil
	}}))
	require.Nil(t, srv.remoteCacheAdapter)
	require.Zero(t, runs.Load())
	require.True(t, cache.DetachRemoteCacheBridge(bridge))
}

func TestRemoteCacheAdapterLifetime(t *testing.T) {
	t.Run("stop cancels a cooperative run", func(t *testing.T) {
		cache := newGCTestCache(t)
		ctx, shutdown := context.WithCancelCause(t.Context())
		defer shutdown(nil)
		srv := &Server{engineCache: cache, shutdownCtx: ctx}
		entered := make(chan struct{})
		var takeErr error
		require.NoError(t, srv.startRemoteCacheIntegration(&RemoteCacheIntegrationConfig{Run: func(ctx context.Context, adapter *RemoteCacheAdapter) error {
			close(entered)
			_, takeErr = adapter.TakeRenewalRequest(ctx)
			return takeErr
		}}))
		within(t, entered)
		adapter := srv.remoteCacheAdapter
		require.NotNil(t, adapter)
		require.True(t, bridgeAttached(cache))
		require.NoError(t, srv.stopRemoteCacheIntegration(boundedContext(t)))
		require.Error(t, takeErr)
		require.False(t, bridgeAttached(cache), "stop detaches the adapter's bridge")
		require.NoError(t, adapter.Stop(boundedContext(t)), "stop is idempotent")
		out, err := adapter.OfferParts(t.Context(), nil, []dagql.PersistedPartOffer{renewalOnlyOffer(), renewalOnlyOffer()})
		require.ErrorIs(t, err, ErrRemoteCacheAdapterClosed)
		require.Len(t, out, 2)
		for _, disposition := range out {
			require.Equal(t, dagql.OfferUnavailable, disposition.Outcome)
			require.ErrorIs(t, disposition.Err, ErrRemoteCacheAdapterClosed)
			require.Equal(t, dagql.PersistedPartAddress{Part: "snapshot"}, disposition.Address)
		}
		_, err = adapter.TakeRenewalRequest(t.Context())
		require.ErrorIs(t, err, ErrRemoteCacheAdapterClosed)
		require.Equal(t, dagql.RenewalReplyDiscarded, adapter.ReplyRenewal(dagql.RenewalReply{Unavailable: true}))
	})
	t.Run("shutdown context cancels run", func(t *testing.T) {
		cache := newGCTestCache(t)
		ctx, shutdown := context.WithCancelCause(t.Context())
		srv := &Server{engineCache: cache, shutdownCtx: ctx, shutdownCancel: shutdown}
		require.NoError(t, srv.startRemoteCacheIntegration(&RemoteCacheIntegrationConfig{Run: func(ctx context.Context, adapter *RemoteCacheAdapter) error {
			<-ctx.Done()
			return context.Cause(ctx)
		}}))
		srv.BeginGracefulStop()
		within(t, srv.remoteCacheAdapter.runDone)
		require.False(t, bridgeAttached(cache))
		require.NoError(t, srv.stopRemoteCacheIntegration(boundedContext(t)))
	})
	t.Run("run exit detaches without failing ordinary work", func(t *testing.T) {
		cache := newGCTestCache(t)
		srv := &Server{engineCache: cache, shutdownCtx: t.Context()}
		var runs atomic.Int32
		require.NoError(t, srv.startRemoteCacheIntegration(&RemoteCacheIntegrationConfig{Run: func(context.Context, *RemoteCacheAdapter) error {
			runs.Add(1)
			return errors.New("integration channel closed")
		}}))
		adapter := srv.remoteCacheAdapter
		within(t, adapter.runDone)
		require.EqualValues(t, 1, runs.Load(), "no replacement goroutine is started")
		require.False(t, bridgeAttached(cache))
		_, err := adapter.TakeRenewalRequest(t.Context())
		require.ErrorIs(t, err, ErrRemoteCacheAdapterClosed)
		addGCTestPersistable(t, cache, "ordinary", "afterIntegrationExit", dagql.NewInt(1))
		// A later explicit attachment is independent of the closed adapter.
		bridge, created, err := cache.AttachRemoteCacheBridge()
		require.NoError(t, err)
		require.True(t, created)
		require.Equal(t, dagql.RenewalReplyDiscarded, adapter.ReplyRenewal(dagql.RenewalReply{Unavailable: true}))
		require.True(t, cache.DetachRemoteCacheBridge(bridge))
		require.NoError(t, adapter.Stop(boundedContext(t)))
	})
	t.Run("noncooperative run is reported", func(t *testing.T) {
		cache := newGCTestCache(t)
		srv := &Server{engineCache: cache, shutdownCtx: t.Context()}
		release := make(chan struct{})
		var releaseOnce sync.Once
		defer releaseOnce.Do(func() { close(release) })
		require.NoError(t, srv.startRemoteCacheIntegration(&RemoteCacheIntegrationConfig{Run: func(context.Context, *RemoteCacheAdapter) error {
			<-release
			return nil
		}}))
		adapter := srv.remoteCacheAdapter
		expired, cancel := context.WithCancel(t.Context())
		cancel()
		err := adapter.Stop(expired)
		require.ErrorContains(t, err, "remote cache integration did not stop")
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, bridgeAttached(cache), "the bridge is detached even though Run is still running")
		releaseOnce.Do(func() { close(release) })
		within(t, adapter.runDone)
		require.Equal(t, err, adapter.Stop(boundedContext(t)), "the first stop result is kept")
	})
}
