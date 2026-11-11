package core

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// partImportObservedManager counts image imports, the snapshot step of a
// builtin Container's whole operation, and the releases of the refs they
// return.
type partImportObservedManager struct {
	bkcache.SnapshotManager
	imports  atomic.Int64
	released atomic.Int64
}

func (m *partImportObservedManager) ImportImage(ctx context.Context, img *bkcache.ImportedImage, opts bkcache.ImportImageOpts) (bkcache.ImmutableRef, error) {
	m.imports.Add(1)
	ref, err := m.SnapshotManager.ImportImage(ctx, img, opts)
	if err != nil {
		return nil, err
	}
	return &partImportObservedRef{ImmutableRef: ref, manager: m}, nil
}

type partImportObservedRef struct {
	bkcache.ImmutableRef
	manager *partImportObservedManager
}

func (r *partImportObservedRef) Release(ctx context.Context) error {
	err := r.ImmutableRef.Release(ctx)
	if err == nil {
		r.manager.released.Add(1)
	}
	return err
}

func TestPartWholeLazyOperationMixedRestart(t *testing.T) {
	actx, _, a, asrv, aServer := executionFixture(t)
	bFixture := newExecutionFixture(t)
	bStore, bServer := bFixture.store, bFixture.server
	packaged, err := local.NewStore(t.TempDir())
	require.NoError(t, err)
	aServer.builtin, bServer.builtin = packaged, packaged
	configJSON := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]},"config":{"WorkingDir":"/whole"}}`)
	cfg := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: digest.FromBytes(configJSON), Size: int64(len(configJSON))}
	require.NoError(t, content.WriteBlob(actx, packaged, "config", bytes.NewReader(configJSON), cfg))
	raw, err := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: cfg, Layers: []ocispec.Descriptor{}})
	require.NoError(t, err)
	manifest := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(raw), Size: int64(len(raw))}
	require.NoError(t, content.WriteBlob(actx, packaged, "manifest", bytes.NewReader(raw), manifest))
	ctr, err := BuiltInContainer(actx, Platform{OS: "linux", Architecture: "amd64"}, manifest.Digest.String())
	require.NoError(t, err)
	original := attachTransferObject(t, actx, a, asrv, "operation-execution", "_builtinContainer", ctr)
	require.NoError(t, a.Evaluate(actx, original))
	dbPath := filepath.Join(t.TempDir(), "mixed.db")
	observed := &partImportObservedManager{}
	open := func(session string) (context.Context, *dagql.Cache, *dagql.Server) {
		ctx, cache, srv := transferCache(t, bStore, dbPath, session)
		query, err := CurrentQuery(ctx)
		require.NoError(t, err)
		observed.SnapshotManager = bStore.Manager
		bServer.cacheManager, bServer.store, bServer.srv = observed, bStore.Content, srv
		query.Server = bServer
		return ctx, cache, srv
	}
	bctx, b, bsrv := open("before")
	selection := dagql.ValueSelection{Roots: []dagql.AnyResult{original}, Outputs: []dagql.SelectedValueOutput{{Result: original, Address: dagql.PersistedPartAddress{Part: ContainerPartFS}}}}
	require.NoError(t, a.WithExportedValues(actx, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		require.Len(t, exported.Chains.Entries, 1)
		provider := &testutil.Provider{InfoReaderProvider: exported.Chains.Entries[0].Provider}
		b.SetPartContentSource(partTestContentSource{provider})
		// Exercise the valid mixed representation with execMeta still unknown.
		// The actual saved whole builtin resolves this snapshot part to absent;
		// the real exec test covers the non-absent metadata snapshot case.
		var payload persistedContainerPayload
		record := &exported.Bundle.Values[0].Record
		require.NoError(t, json.Unmarshal(record.Envelope.ObjectJSON, &payload))
		savedRecipe := bytes.Clone(payload.LazyJSON)
		require.NotEmpty(t, savedRecipe)
		payload.Parts[ContainerPartExecMeta] = persistedContainerPart{Kind: containerPartPending}
		record.Envelope.ObjectJSON, err = json.Marshal(payload)
		require.NoError(t, err)
		importedReply, err := b.MergeValues(bctx, dagql.CloudCacheID, exported.Bundle)
		imported := importedReply.Imported()
		require.NoError(t, err)
		loaded, err := b.LoadResultByResultID(bctx, "", bsrv, imported[0].ResultID)
		require.NoError(t, err)
		result := loaded.(dagql.ObjectResult[*Container])
		require.NoError(t, b.EvaluateParts(bctx, result, ContainerPartFS))
		view := result.Self().acquiredOutput.Load()
		require.Equal(t, containerPartPending, view.Payload.Parts[ContainerPartExecMeta].Kind)
		require.Len(t, view.Links, 1)
		fsID := view.Links[0].RefKey
		reads := provider.Reads.Load()
		require.Positive(t, reads)
		imports, released := observed.imports.Load(), observed.released.Load()
		require.NoError(t, b.ReleaseSession(bctx, "before"))
		require.NoError(t, b.Close(bctx))
		bStore.Reload(t)
		bctx, b, bsrv = open("after")
		require.Equal(t, dagql.CachePersistenceResetNone, b.PersistenceResetReason())
		b.SetPartContentSource(partTestContentSource{provider})
		loaded, err = b.LoadResultByResultID(bctx, "", bsrv, imported[0].ResultID)
		require.NoError(t, err)
		result = loaded.(dagql.ObjectResult[*Container])
		view = result.Self().acquiredOutput.Load()
		require.Nil(t, result.Self().Lazy)
		require.Equal(t, containerPartPending, view.Payload.Parts[ContainerPartExecMeta].Kind)
		require.Equal(t, fsID, view.Links[0].RefKey)
		require.Equal(t, reads, provider.Reads.Load(), "boot cannot read provider content")
		require.Equal(t, imports, observed.imports.Load(), "boot cannot run the operation")
		require.NoError(t, b.EvaluateParts(bctx, result, ContainerPartExecMeta))
		require.Equal(t, imports+1, observed.imports.Load(), "the whole operation runs once")
		require.Equal(t, released+1, observed.released.Load(), "the operation's private FS ref is released")
		view = result.Self().acquiredOutput.Load()
		require.Equal(t, containerPartAbsent, view.Payload.Parts[ContainerPartExecMeta].Kind)
		require.Len(t, view.Links, 1)
		require.Equal(t, fsID, view.Links[0].RefKey)
		require.JSONEq(t, string(savedRecipe), string(view.Payload.LazyJSON))
		require.Equal(t, reads, provider.Reads.Load(), "whole operation must preserve the already downloaded FS")
		return nil
	}))
}

// An evaluated whole operation, such as a builtin image, that came from
// another engine with no download address for any part is computed again by
// its operation, as one restored from local disk is, rather than searched
// for part by part.
func TestPartWholeOperationWithoutAddressesRunsItsOperation(t *testing.T) {
	actx, _, a, asrv, aServer := executionFixture(t)
	b := newExecutionFixture(t)
	packaged, err := local.NewStore(t.TempDir())
	require.NoError(t, err)
	aServer.builtin, b.server.builtin = packaged, packaged
	configJSON := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]},"config":{"WorkingDir":"/whole"}}`)
	cfg := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: digest.FromBytes(configJSON), Size: int64(len(configJSON))}
	require.NoError(t, content.WriteBlob(actx, packaged, "config", bytes.NewReader(configJSON), cfg))
	raw, err := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: cfg, Layers: []ocispec.Descriptor{}})
	require.NoError(t, err)
	manifest := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(raw), Size: int64(len(raw))}
	require.NoError(t, content.WriteBlob(actx, packaged, "manifest", bytes.NewReader(raw), manifest))
	ctr, err := BuiltInContainer(actx, Platform{OS: "linux", Architecture: "amd64"}, manifest.Digest.String())
	require.NoError(t, err)
	original := attachTransferObject(t, actx, a, asrv, "operation-execution", "_builtinContainer", ctr)
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
