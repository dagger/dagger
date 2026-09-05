package engineutil

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/diff/apply"
	"github.com/containerd/containerd/v2/plugins/diff/walking"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestPreparedImageCacheOwnsProvider(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewStore(t)
	a, owner := store.Build(t, nil, "a.txt", "prepared bytes")
	client, err := NewClient(ctx, &Opts{Snapshotter: store.Snapshots, ContentStore: store.Content, LeaseManager: store.Leases,
		Applier: apply.NewFileSystemApplier(store.Content), Differ: walking.NewWalkingDiff(store.Content)})
	require.NoError(t, err)
	cache, err := dagql.NewCache(ctx, "", store.Manager, nil)
	require.NoError(t, err)
	defer cache.CloseDiscardingPersistence()
	cached, err := cache.GetOrInitArbitrary(ctx, "image-session", "prepared-image", func(ctx context.Context) (any, error) {
		return client.PrepareContainerImage(ctx, map[string]ContainerExport{"linux/amd64": {Ref: a}}, true, "uncompressed")
	})
	require.NoError(t, err)
	prepared := cached.Value().(*PreparedContainerImage)
	require.NoError(t, store.Manager.RemoveLease(ctx, owner))
	require.NoError(t, a.Release(ctx))
	store.GC(t)
	var manifestData bytes.Buffer
	require.NoError(t, prepared.Manifest().WriteTo(ctx, &manifestData))
	var manifest ocispecs.Manifest
	require.NoError(t, json.Unmarshal(manifestData.Bytes(), &manifest))
	require.Len(t, manifest.Layers, 1)
	blob, err := prepared.Blob(manifest.Layers[0].Digest.String())
	require.NoError(t, err)
	var layer bytes.Buffer
	require.NoError(t, blob.WriteTo(ctx, &layer))
	require.NotZero(t, layer.Len())
	require.NoError(t, cache.ReleaseSession(ctx, "image-session"))
	require.Eventually(t, func() bool {
		owners, err := store.Leases.List(ctx)
		return err == nil && len(owners) == 0
	}, 5*time.Second, 10*time.Millisecond, "ordinary arbitrary-cache release must release the image and copied chains")
	store.GC(t)
}

func TestPreparedImageCanceledLateCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := testutil.NewStore(t)
	a, owner := store.Build(t, nil, "a.txt", "abandoned prepared bytes")
	client, err := NewClient(ctx, &Opts{Snapshotter: store.Snapshots, ContentStore: store.Content, LeaseManager: store.Leases,
		Applier: apply.NewFileSystemApplier(store.Content), Differ: walking.NewWalkingDiff(store.Content)})
	require.NoError(t, err)
	cache, err := dagql.NewCache(ctx, "", store.Manager, nil)
	require.NoError(t, err)
	defer cache.CloseDiscardingPersistence()
	ready := make(chan *PreparedContainerImage, 1)
	allowReturn := make(chan struct{})
	finish := sync.OnceFunc(func() { close(allowReturn) })
	defer finish()
	caller := make(chan error, 1)
	go func() {
		_, err := cache.GetOrInitArbitrary(ctx, "canceled-image", "prepared-image", func(ctx context.Context) (any, error) {
			prepared, err := client.PrepareContainerImage(ctx, map[string]ContainerExport{"linux/amd64": {Ref: a}}, true, "uncompressed")
			if err != nil {
				return nil, err
			}
			ready <- prepared
			<-allowReturn
			return prepared, nil
		})
		caller <- err
	}()
	prepared := <-ready
	require.NoError(t, store.Manager.RemoveLease(context.Background(), owner))
	require.NoError(t, a.Release(context.Background()))
	store.GC(t)
	var manifestData bytes.Buffer
	require.NoError(t, prepared.Manifest().WriteTo(context.Background(), &manifestData))
	var manifest ocispecs.Manifest
	require.NoError(t, json.Unmarshal(manifestData.Bytes(), &manifest))
	blob, err := prepared.Blob(manifest.Layers[0].Digest.String())
	require.NoError(t, err)
	var layer bytes.Buffer
	require.NoError(t, blob.WriteTo(context.Background(), &layer))
	require.NotZero(t, layer.Len())
	cancel()
	require.ErrorIs(t, <-caller, context.Canceled)
	finish()
	require.NoError(t, cache.Close(context.Background()))
	owners, err := store.Leases.List(context.Background())
	require.NoError(t, err)
	require.Empty(t, owners)
	store.GC(t)
}
