package core

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// An evaluated whole operation, such as an image import, that came from
// another engine with no download address for any part is computed again by
// its operation, as one restored from local disk is, rather than searched
// for part by part.
func TestPartWholeOperationWithoutAddressesRunsItsOperation(t *testing.T) {
	actx, _, a, asrv, _ := executionFixture(t)
	b := newExecutionFixture(t)
	original, _ := importTestContainer(t, actx, a, asrv, "/whole")
	require.NoError(t, a.Evaluate(actx, original))
	var logs partSourceLogs
	bctx := logs.into(b.ctx)
	require.NoError(t, a.WithExportedValues(actx, dagql.ValueSelection{Roots: []dagql.AnyResult{original}}, config.RefConfig{}, func(_ context.Context, exported *dagql.ExportedValues) error {
		reply, err := b.cache.MergeValues(bctx, dagql.CloudCacheID, exported.Bundle)
		require.NoError(t, err)
		loaded, err := b.cache.LoadResultByResultID(bctx, "", b.srv, reply.Imported()[0].ResultID)
		require.NoError(t, err)
		result := loaded.(dagql.ObjectResult[*Container])
		require.NoError(t, b.cache.Evaluate(bctx, result))
		require.Equal(t, "/whole", result.Self().Config.WorkingDir)
		require.False(t, dagql.HasPendingLazyComputation(result), "the container is computed")
		require.Empty(t, logs.computed(t), "no part is searched for and then computed for want of a source")
		return nil
	}))
}

func TestPartPendingImageMetadataStaysSelective(t *testing.T) {
	aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
	actx, a, asrv := transferCache(t, aStore, "", "a")
	observed := &transferObservedSnapshots{SnapshotManager: bStore.Manager}
	bStore.Manager = observed
	bctx, b, bsrv := transferCache(t, bStore, "", "b")
	platform := Platform{OS: "linux", Architecture: "amd64"}
	parent := attachTransferObject(t, actx, a, asrv, "a", "parent", NewContainer(platform))
	child := NewContainer(platform)
	child.Config.WorkingDir = "/pending-image-config"
	child.Lazy = &ContainerFromImageRefLazy{LazyState: NewLazyState(), Parent: parent, CanonicalRef: "example.invalid/image@sha256:" + strings.Repeat("a", 64), Config: child.Config, Platform: platform}
	original := attachTransferObject(t, actx, a, asrv, "a", "from", child)
	require.NoError(t, a.WithExportedValues(actx, dagql.ValueSelection{Roots: []dagql.AnyResult{original}}, config.RefConfig{}, func(_ context.Context, exported *dagql.ExportedValues) error {
		importedReply, err := b.MergeValues(bctx, dagql.CloudCacheID, exported.Bundle)
		imported := importedReply.Imported()
		require.NoError(t, err)
		loaded, err := b.LoadResultByResultID(bctx, "", bsrv, imported[0].ResultID)
		require.NoError(t, err)
		result := loaded.(dagql.ObjectResult[*Container])
		ctr := result.Self()
		require.False(t, ctr.containerPartComputed(bctx, ctr.lazyOpForRouting(), ContainerPartMetadata))
		require.NoError(t, b.EvaluateParts(bctx, result, ContainerPartMetadata))
		require.True(t, ctr.containerPartComputed(bctx, ctr.lazyOpForRouting(), ContainerPartMetadata))
		require.Equal(t, "/pending-image-config", ctr.Config.WorkingDir)
		require.False(t, ctr.containerPartComputed(bctx, ctr.lazyOpForRouting(), ContainerPartFS), "the image's filesystem stays pending")
		require.Empty(t, observed.opens)
		return nil
	}))
}

func TestPartNativePendingContainerRequiresRecipe(t *testing.T) {
	fixture := newExecutionFixture(t)
	ctx, srv := fixture.ctx, fixture.srv
	for _, metadataKnown := range []bool{false, true} {
		payload := persistedContainerPayload{Metadata: persistedContainerMetadata{Consumed: metadataKnown, Value: persistedContainerMetadataValue{Platform: Platform{OS: "linux", Architecture: "amd64"}}}}
		if metadataKnown {
			payload.Parts = map[dagql.PartKey]persistedContainerPart{ContainerPartFS: {Kind: containerPartPending}, ContainerPartExecMeta: {Kind: containerPartAbsent}}
		}
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		_, err = (*Container)(nil).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, 1, nil).WithSnapshotRoles([]dagql.PersistedSnapshotRefLink{}), raw)
		require.ErrorContains(t, err, "no recipe", "ordinary local pending Container cannot borrow the raw adapter exception")
	}
}

// importTestContainer is a lazy container an import makes of an image with
// only a config, whose working directory is workdir: from an OCI layout
// tarball in a blob file, on a scratch container. Nothing it reads is outside
// the cache, so another cache that merges its records can run it. It returns
// the result and its operation.
func importTestContainer(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, workdir string) (dagql.ObjectResult[*Container], *ContainerImportLazy) {
	t.Helper()
	platform := Platform{OS: "linux", Architecture: "amd64"}
	configJSON := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]},"config":{"WorkingDir":"` + workdir + `"}}`)
	cfg := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: digest.FromBytes(configJSON), Size: int64(len(configJSON))}
	manifestJSON, err := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: cfg, Layers: []ocispec.Descriptor{}})
	require.NoError(t, err)
	manifest := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(manifestJSON), Size: int64(len(manifestJSON)), Platform: &ocispec.Platform{OS: "linux", Architecture: "amd64"}}
	indexJSON, err := json.Marshal(ocispec.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex, Manifests: []ocispec.Descriptor{manifest}})
	require.NoError(t, err)
	var tarball bytes.Buffer
	tw := tar.NewWriter(&tarball)
	for _, f := range []struct {
		name string
		data []byte
	}{
		{ocispec.ImageLayoutFile, []byte(`{"imageLayoutVersion":"1.0.0"}`)},
		{"index.json", indexJSON},
		{"blobs/sha256/" + cfg.Digest.Encoded(), configJSON},
		{"blobs/sha256/" + manifest.Digest.Encoded(), manifestJSON},
	} {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.data)), Typeflag: tar.TypeReg}))
		_, err := tw.Write(f.data)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	sourceFile := &File{
		Platform: platform,
		Lazy:     &FileBlobLazy{LazyState: NewLazyState(), Filename: "image.tar", Contents: tarball.Bytes(), Permissions: 0o644},
		File:     new(LazyAccessor[string, *File]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]),
	}
	sourceFile.SetPath("/image.tar")
	source := attachTransferObject(t, ctx, cache, srv, "operation-execution", "blob", sourceFile)
	parent := attachTransferObject(t, ctx, cache, srv, "operation-execution", "container", NewContainer(platform))
	op := &ContainerImportLazy{LazyState: NewLazyState(), Parent: parent, Source: source}
	ctr := NewContainer(platform)
	ctr.Lazy = op
	return attachTransferObject(t, ctx, cache, srv, "operation-execution", "import", ctr), op
}
