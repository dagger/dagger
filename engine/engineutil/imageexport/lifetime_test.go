package imageexport

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff/apply"
	"github.com/containerd/containerd/v2/plugins/diff/walking"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestAssembledProviderLifetime(t *testing.T) {
	for _, rewrite := range []bool{false, true} {
		name := "original"
		if rewrite {
			name = "rewritten"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := testutil.NewStore(t)
			a, owner := store.Build(t, nil, "a.txt", "image bytes")
			writer, err := NewWriter(WriterOpt{Snapshotter: store.Snapshots, ContentStore: store.Content, LeaseManager: store.Leases,
				Applier: apply.NewFileSystemApplier(store.Content), Differ: walking.NewWalkingDiff(store.Content)})
			require.NoError(t, err)
			platform := ocispecs.Platform{OS: "linux", Architecture: "amd64"}
			epoch := time.Unix(1600000000, 0).UTC()
			image, err := writer.Assemble(ctx, &ExportRequest{Platforms: []PlatformExportInput{{Key: "linux/amd64", Platform: platform, Ref: a, Config: dockerspec.DockerOCIImage{Image: ocispecs.Image{Platform: platform}}}}},
				CommitOpts{OCITypes: true, RefCfg: config.RefConfig{Compression: compression.New(compression.Uncompressed)}, Epoch: &epoch, RewriteTimestamp: rewrite})
			require.NoError(t, err)
			defer image.Release(ctx)
			require.NoError(t, store.Manager.RemoveLease(ctx, owner))
			require.NoError(t, a.Release(ctx))
			store.GC(t)
			manifestBytes, err := content.ReadBlob(ctx, image.Provider, image.RootDesc)
			require.NoError(t, err)
			var manifest ocispecs.Manifest
			require.NoError(t, json.Unmarshal(manifestBytes, &manifest))
			require.Len(t, manifest.Layers, 1)
			configBytes, err := content.ReadBlob(ctx, image.Provider, manifest.Config)
			require.NoError(t, err)
			require.NotEmpty(t, configBytes)
			layerBytes, err := content.ReadBlob(ctx, image.Provider, manifest.Layers[0])
			require.NoError(t, err)
			require.NotEmpty(t, layerBytes)
			if rewrite {
				rows := store.Manager.PersistentMetadataRows()
				require.Len(t, rows.ImportedByBlob, 1)
				require.NotEqual(t, rows.ImportedByBlob[0].BlobDigest, manifest.Layers[0].Digest, "timestamp rewrite actually changed the returned provider blob")
			}
			require.NoError(t, image.Release(ctx))
			store.GC(t)
			owners, err := store.Leases.List(ctx)
			require.NoError(t, err)
			require.Empty(t, owners)
		})
	}
}
