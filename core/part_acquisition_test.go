package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

type partObservedManager struct {
	bkcache.SnapshotManager
	bodies             atomic.Int64
	failBody           atomic.Bool
	failOwner          atomic.Bool
	ownerAttempts      atomic.Int64
	pins               atomic.Int64
	accessorRefs       atomic.Int64
	failPinRelease     atomic.Bool
	pinReleaseAttempts atomic.Int64
}

func (m *partObservedManager) GetBySnapshotID(ctx context.Context, id string, opts ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	m.accessorRefs.Add(1)
	return m.SnapshotManager.GetBySnapshotID(ctx, id, opts...)
}

func (m *partObservedManager) New(ctx context.Context, parent bkcache.ImmutableRef, opts ...bkcache.RefOption) (bkcache.MutableRef, error) {
	m.bodies.Add(1)
	if m.failBody.Load() {
		return nil, partInjectedBodyFailure
	}
	return m.SnapshotManager.New(ctx, parent, opts...)
}

var partInjectedBodyFailure = errors.New("private operation failed")
var partInjectedProviderFailure = errors.New("supplied blob unavailable")

var partInjectedOwnerFailure = errors.New("injected owner acknowledgement failure")

func (m *partObservedManager) AttachLease(ctx context.Context, id, snapshot string) error {
	m.ownerAttempts.Add(1)
	if err := m.SnapshotManager.AttachLease(ctx, id, snapshot); err != nil {
		return err
	}
	if m.failOwner.Swap(false) {
		return partInjectedOwnerFailure
	}
	return nil
}

func (m *partObservedManager) PinSnapshot(ctx context.Context, id string) (bkcache.ImmutableRef, error) {
	m.pins.Add(1)
	ref, err := m.SnapshotManager.PinSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	return &partObservedPin{ImmutableRef: ref, manager: m}, nil
}

type partObservedPin struct {
	bkcache.ImmutableRef
	manager *partObservedManager
}

var partInjectedPinReleaseFailure = errors.New("injected pin release failure")

func (p *partObservedPin) Release(ctx context.Context) error {
	p.manager.pinReleaseAttempts.Add(1)
	if p.manager.failPinRelease.Swap(false) {
		return partInjectedPinReleaseFailure
	}
	return p.ImmutableRef.Release(ctx)
}

type partTestContentSource struct{ provider content.InfoReaderProvider }

func (s partTestContentSource) Provider(context.Context, dagql.PersistedPartOffer, *dagql.PartDemandState) content.InfoReaderProvider {
	return s.provider
}
func TestPartAcquisitionRootRoutes(t *testing.T) {
	for _, mode := range []string{"ready", "chain", "chain-sync-retry", "chain-sync-restart", "chain-pin-release-retry", "chain-fallback", "chain-fallback-operation-failure", "operation"} {
		t.Run(mode, func(t *testing.T) {
			aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
			actx, a, asrv := transferCache(t, aStore, filepath.Join(t.TempDir(), "a.db"), "a")
			observed := &partObservedManager{SnapshotManager: bStore.Manager}
			bStore.Manager = observed
			bPath := filepath.Join(t.TempDir(), "b.db")
			bctx, b, bsrv := transferCache(t, bStore, bPath, "b")
			platform := Platform{OS: "linux", Architecture: "amd64"}
			file := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Platform: platform, Lazy: &FileBlobLazy{LazyState: NewLazyState(), Filename: "produced.txt", Contents: []byte("operation bytes"), Permissions: 0644}}
			file.SetPath("/pending-preseed")
			original := attachTransferObject(t, actx, a, asrv, "a", "partFile", file)
			var selections []dagql.SelectedValueOutput
			if strings.HasPrefix(mode, "chain") {
				require.NoError(t, a.Evaluate(actx, original))
				selections = []dagql.SelectedValueOutput{{Result: original, Address: dagql.PersistedPartAddress{Part: "snapshot"}}}
			}
			if mode == "ready" {
				ref, _ := bStore.Build(t, nil, "donor/selected.txt", "ready bytes")
				donor := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Platform: platform}
				donor.SetPath("/donor/selected.txt")
				donor.SetSnapshot(ref)
				attachTransferObject(t, bctx, b, bsrv, "b", "partFile", donor)
			}
			observed.bodies.Store(0)
			require.NoError(t, a.WithExportedValues(actx, dagql.ValueSelection{Roots: []dagql.AnyResult{original}, Outputs: selections}, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(ctx context.Context, exported *dagql.ExportedValues) error {
				var provider *testutil.Provider
				if strings.HasPrefix(mode, "chain") {
					require.Len(t, exported.Chains.Entries, 1)
					provider = &testutil.Provider{InfoReaderProvider: exported.Chains.Entries[0].Provider}
					if strings.HasPrefix(mode, "chain-fallback") {
						provider.BeforeRead = func(context.Context, ocispec.Descriptor) error { return partInjectedProviderFailure }
					}
					b.SetPartContentSource(partTestContentSource{provider})
				}
				imported, err := b.ImportValues(bctx, exported.Bundle)
				require.NoError(t, err)
				loaded, err := b.LoadResultByResultID(bctx, "", bsrv, imported[0].ResultID)
				require.NoError(t, err)
				result := loaded.(dagql.ObjectResult[*File])
				oldPath, oldSnapshot := result.Self().File, result.Self().Snapshot
				observed.ownerAttempts.Store(0)
				observed.pins.Store(0)
				started := time.Now()
				if mode == "chain-fallback-operation-failure" {
					observed.failBody.Store(true)
					err := b.Evaluate(bctx, result)
					require.ErrorIs(t, err, partInjectedProviderFailure)
					require.ErrorIs(t, err, partInjectedBodyFailure)
					var contentErr *bkcache.ChainContentError
					require.ErrorAs(t, err, &contentErr)
					require.EqualValues(t, 1, observed.bodies.Load())
					require.EqualValues(t, 1, provider.Reads.Load())
					return nil
				}
				if mode == "chain-pin-release-retry" {
					observed.failPinRelease.Store(true)
					require.ErrorIs(t, b.Evaluate(bctx, result), partInjectedPinReleaseFailure)
					reads, syncs := provider.Reads.Load(), observed.ownerAttempts.Load()
					require.Positive(t, reads)
					require.EqualValues(t, 1, observed.pinReleaseAttempts.Load())
					require.NoError(t, b.Evaluate(bctx, result))
					require.Equal(t, reads, provider.Reads.Load(), "retained pin retry cannot redownload")
					require.Equal(t, syncs, observed.ownerAttempts.Load(), "successful lease sync cannot repeat")
					require.EqualValues(t, 2, observed.pinReleaseAttempts.Load())
					require.EqualValues(t, 1, observed.pins.Load())
				} else if strings.HasPrefix(mode, "chain-sync-") {
					observed.failOwner.Store(true)
					require.ErrorIs(t, b.Evaluate(bctx, result), partInjectedOwnerFailure)
					_, err := b.CapturePersistedRecord(bctx, result)
					require.ErrorIs(t, err, dagql.ErrPersistStateNotReady)
					reads := provider.Reads.Load()
					require.Positive(t, reads)
					if mode == "chain-sync-restart" {
						require.NoError(t, b.ReleaseSession(bctx, "b"))
						require.NoError(t, b.Close(bctx))
						bStore.Reload(t)
						observed = &partObservedManager{SnapshotManager: bStore.Manager}
						bStore.Manager = observed
						bctx, b, bsrv = transferCache(t, bStore, bPath, "restart")
						require.Equal(t, dagql.CachePersistenceResetNone, b.PersistenceResetReason())
						b.SetPartContentSource(partTestContentSource{provider})
						loaded, err = b.LoadResultByResultID(bctx, "", bsrv, imported[0].ResultID)
						require.NoError(t, err)
						result = loaded.(dagql.ObjectResult[*File])
						oldPath, oldSnapshot = result.Self().File, result.Self().Snapshot
					}
					require.NoError(t, b.Evaluate(bctx, result))
					require.Equal(t, reads, provider.Reads.Load(), "bookkeeping retry must not repeat acquisition")
				} else {
					require.NoError(t, b.Evaluate(bctx, result))
				}
				t.Logf("route=%s latency=%s pins=%d owner-sync-attempts=%d private-bodies=%d", mode, time.Since(started), observed.pins.Load(), observed.ownerAttempts.Load(), observed.bodies.Load())
				require.Same(t, oldPath, result.Self().File)
				require.Same(t, oldSnapshot, result.Self().Snapshot)
				got, err := result.Self().Contents(bctx, result, nil, nil)
				require.NoError(t, err)
				if mode == "ready" {
					require.Equal(t, "ready bytes", string(got))
					require.Equal(t, "/donor/selected.txt", mustTransferPath(t, bctx, result))
				} else {
					require.Equal(t, "operation bytes", string(got))
					require.Equal(t, "/produced.txt", mustTransferPath(t, bctx, result))
				}
				if mode == "operation" || mode == "chain-fallback" {
					require.EqualValues(t, 1, observed.bodies.Load())
				} else {
					require.Zero(t, observed.bodies.Load())
				}
				if provider != nil {
					require.Greater(t, provider.Reads.Load(), int64(0))
				}
				record, err := b.CapturePersistedRecord(bctx, result)
				require.NoError(t, err)
				require.Len(t, record.SnapshotLinks, 1)
				require.Empty(t, record.Envelope.PendingOffers)
				return nil
			}))
		})
	}
}

type partSelectingContentSource map[string]content.InfoReaderProvider

func (s partSelectingContentSource) Provider(_ context.Context, offer dagql.PersistedPartOffer, _ *dagql.PartDemandState) content.InfoReaderProvider {
	return s[string(offer.Address.Part)]
}
func partTestDirectory(ref bkcache.ImmutableRef, path string) *Directory {
	dir := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Platform: Platform{OS: "linux", Architecture: "amd64"}}
	dir.SetPath(path)
	dir.SetSnapshot(ref)
	return dir
}
func TestPartContainerMixedRestart(t *testing.T) {
	aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
	actx, a, asrv := transferCache(t, aStore, filepath.Join(t.TempDir(), "a.db"), "a")
	path := filepath.Join(t.TempDir(), "b.db")
	bctx, b, bsrv := transferCache(t, bStore, path, "b")
	fs, _ := aStore.Build(t, nil, "data", "filesystem bytes")
	meta, _ := aStore.Build(t, nil, "exitCode", "0")
	ctr := NewContainer(Platform{OS: "linux", Architecture: "amd64"})
	ctr.FS.setValue(partTestDirectory(fs, "/"))
	ctr.MetaSnapshot.setValue(meta)
	original := attachTransferObject(t, actx, a, asrv, "a", "mixedContainer", ctr)
	selection := dagql.ValueSelection{Roots: []dagql.AnyResult{original}, Outputs: []dagql.SelectedValueOutput{{Result: original, Address: dagql.PersistedPartAddress{Part: "fs"}}, {Result: original, Address: dagql.PersistedPartAddress{Part: "execMeta"}}}}
	require.NoError(t, a.WithExportedValues(actx, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		providers := partSelectingContentSource{}
		counts := map[string]*testutil.Provider{}
		for _, entry := range exported.Chains.Entries {
			p := &testutil.Provider{InfoReaderProvider: entry.Provider}
			counts[string(entry.Address.Part)] = p
			providers[string(entry.Address.Part)] = p
		}
		b.SetPartContentSource(providers)
		imported, err := b.ImportValues(bctx, exported.Bundle)
		require.NoError(t, err)
		loaded, err := b.LoadResultByResultID(bctx, "", bsrv, imported[0].ResultID)
		require.NoError(t, err)
		result := loaded.(dagql.ObjectResult[*Container])
		require.NoError(t, b.EvaluateParts(bctx, result, ContainerPartMetadata))
		require.Zero(t, counts["fs"].Reads.Load())
		require.Zero(t, counts["execMeta"].Reads.Load())
		oldFS := result.Self().FS
		require.NoError(t, b.EvaluateParts(bctx, result, ContainerPartFS))
		require.Same(t, oldFS, result.Self().FS)
		require.Zero(t, counts["execMeta"].Reads.Load())
		first := result.Self().acquiredOutput.Load()
		require.Equal(t, containerPartDirectory, first.Payload.Parts[ContainerPartFS].Kind)
		require.Equal(t, containerPartPending, first.Payload.Parts[ContainerPartExecMeta].Kind)
		require.Len(t, first.Links, 1)
		fsID := first.Links[0].RefKey
		record, err := b.CapturePersistedRecord(bctx, result)
		require.NoError(t, err)
		require.Len(t, record.Envelope.PendingOffers, 1)
		require.NoError(t, b.ReleaseSession(bctx, "b"))
		require.NoError(t, b.Close(bctx))
		bStore.Reload(t)
		bctx, b, bsrv = transferCache(t, bStore, path, "restart")
		require.Equal(t, dagql.CachePersistenceResetNone, b.PersistenceResetReason())
		b.SetPartContentSource(providers)
		loaded, err = b.LoadResultByResultID(bctx, "restart", bsrv, imported[0].ResultID)
		require.NoError(t, err)
		result = loaded.(dagql.ObjectResult[*Container])
		before := counts["fs"].Reads.Load()
		require.NoError(t, b.EvaluateParts(bctx, result, ContainerPartFS))
		require.Equal(t, before, counts["fs"].Reads.Load())
		require.NoError(t, b.EvaluateParts(bctx, result, ContainerPartExecMeta))
		view := result.Self().acquiredOutput.Load()
		require.Len(t, view.Links, 2)
		for _, link := range view.Links {
			if link.Role == "fs" {
				require.Equal(t, fsID, link.RefKey)
			}
		}
		dir, _ := result.Self().FS.Peek()
		snapshot, _ := dir.Snapshot.Peek()
		testutil.CheckFile(t, snapshot, "data", "filesystem bytes")
		record, err = b.CapturePersistedRecord(bctx, result)
		require.NoError(t, err)
		require.Empty(t, record.Envelope.PendingOffers)
		require.NoError(t, b.WithExportedValues(bctx, dagql.ValueSelection{Roots: []dagql.AnyResult{result}}, config.RefConfig{}, func(_ context.Context, forward *dagql.ExportedValues) error {
			require.Empty(t, forward.Bundle.Values[0].Record.Envelope.PendingOffers)
			return nil
		}))
		return nil
	}))
}
func TestPartMountReceiverRoles(t *testing.T) {
	aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
	actx, a, asrv := transferCache(t, aStore, "", "a")
	bctx, b, bsrv := transferCache(t, bStore, "", "b")
	makeCtr := func(store *testutil.Store, targets []string) *Container {
		ctr := NewContainer(Platform{OS: "linux", Architecture: "amd64"})
		for _, target := range targets {
			ref, _ := store.Build(t, nil, "payload", target)
			accessor := new(LazyAccessor[*Directory, *Container])
			accessor.setValue(partTestDirectory(ref, "/"))
			ctr.Mounts = append(ctr.Mounts, ContainerMount{Target: target, DirectorySource: accessor})
		}
		return ctr
	}
	original := attachTransferObject(t, actx, a, asrv, "a", "mounts", makeCtr(aStore, []string{"/a", "/b"}))
	attachTransferObject(t, bctx, b, bsrv, "b", "mounts", makeCtr(bStore, []string{"/b", "/a"}))
	require.NoError(t, a.WithExportedValues(actx, dagql.ValueSelection{Roots: []dagql.AnyResult{original}}, config.RefConfig{}, func(_ context.Context, exported *dagql.ExportedValues) error {
		imported, err := b.ImportValues(bctx, exported.Bundle)
		require.NoError(t, err)
		loaded, err := b.LoadResultByResultID(bctx, "", bsrv, imported[0].ResultID)
		require.NoError(t, err)
		result := loaded.(dagql.ObjectResult[*Container])
		require.NoError(t, b.EvaluateParts(bctx, result, ContainerPartMount("/a")))
		view := result.Self().acquiredOutput.Load()
		require.Len(t, view.Links, 1)
		require.Equal(t, "mount_dir:0", view.Links[0].Role)
		require.Equal(t, "/a", view.Payload.Metadata.Value.Mounts[0].Target)
		dir, _ := result.Self().Mounts[0].DirectorySource.Peek()
		ref, _ := dir.Snapshot.Peek()
		testutil.CheckFile(t, ref, "payload", "/a")
		return nil
	}))
}

func TestPartPrivateWholeBuiltin(t *testing.T) {
	ctx, _, cache, srv, server := executionFixture(t)
	packaged, err := local.NewStore(t.TempDir())
	require.NoError(t, err)
	server.builtin = packaged
	configJSON := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]},"config":{"WorkingDir":"/private"}}`)
	cfg := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: digest.FromBytes(configJSON), Size: int64(len(configJSON))}
	require.NoError(t, content.WriteBlob(ctx, packaged, "config", bytes.NewReader(configJSON), cfg))
	raw, err := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: cfg, Layers: []ocispec.Descriptor{}})
	require.NoError(t, err)
	manifest := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(raw), Size: int64(len(raw))}
	require.NoError(t, content.WriteBlob(ctx, packaged, "manifest", bytes.NewReader(raw), manifest))
	platform := Platform{OS: "linux", Architecture: "amd64"}
	ctr := NewContainer(platform)
	originalLazy := &ContainerBuiltinLazy{LazyState: NewLazyState(), Platform: platform, ManifestDigest: manifest.Digest}
	ctr.Lazy = originalLazy
	original := attachTransferObject(t, ctx, cache, srv, "operation-execution", "_builtinContainer", ctr)
	require.NoError(t, cache.WithExportedValues(ctx, dagql.ValueSelection{Roots: []dagql.AnyResult{original}}, config.RefConfig{}, func(_ context.Context, exported *dagql.ExportedValues) error {
		imported, err := cache.ImportValues(ctx, exported.Bundle)
		require.NoError(t, err)
		loaded, err := cache.LoadResultByResultID(ctx, "", srv, imported[0].ResultID)
		require.NoError(t, err)
		result := loaded.(dagql.ObjectResult[*Container])
		require.NoError(t, cache.EvaluateParts(ctx, result, ContainerPartFS))
		require.Equal(t, "/private", result.Self().Config.WorkingDir)
		view := result.Self().acquiredOutput.Load()
		require.NotNil(t, view)
		require.True(t, view.Payload.Metadata.Consumed)
		require.Equal(t, containerPartDirectory, view.Payload.Parts[ContainerPartFS].Kind)
		require.NotEmpty(t, view.Payload.LazyJSON)
		require.False(t, originalLazy.lazyInitComplete.Load())
		require.Same(t, originalLazy, ctr.Lazy)
		require.NoError(t, cache.Evaluate(ctx, result))
		return nil
	}))
}

func (partTestContentSource) Available(dagql.PersistedPartOffer, int64) bool { return true }

func (partSelectingContentSource) Available(dagql.PersistedPartOffer, int64) bool { return true }

type partFixtureTestRef struct {
	bkcache.ImmutableRef
	id       string
	releases int
}

func (r *partFixtureTestRef) SnapshotID() string            { return r.id }
func (r *partFixtureTestRef) Release(context.Context) error { r.releases++; return nil }

func TestPartFixtureReleaseObserverPrivateFS(t *testing.T) {
	ordinary := NewContainer(Platform{OS: "linux", Architecture: "amd64"})
	ordinaryRef := &partFixtureTestRef{id: "ordinary-fs"}
	ordinary.FS.setValue(partTestDirectory(ordinaryRef, "/"))
	private := NewContainer(ordinary.Platform)
	fs, meta, mount := &partFixtureTestRef{id: "private-fs"}, &partFixtureTestRef{id: "private-meta"}, &partFixtureTestRef{id: "private-mount"}
	private.FS.setValue(partTestDirectory(fs, "/"))
	private.MetaSnapshot.setValue(meta)
	mountDir := new(LazyAccessor[*Directory, *Container])
	mountDir.setValue(partTestDirectory(mount, "/"))
	private.Mounts = []ContainerMount{{Target: "/mount", DirectorySource: mountDir}}
	observations := 0
	observePrivateContainerFSRelease(private, func(id string, err error) {
		require.NoError(t, err)
		require.Equal(t, "private-fs", id)
		require.Equal(t, 1, fs.releases, "observer runs after the underlying release")
		observations++
	})
	dir, _ := private.FS.Peek()
	wrapped, _ := dir.Snapshot.Peek()
	require.IsType(t, &partFixtureReleaseRef{}, wrapped)
	originalDir, _ := ordinary.FS.Peek()
	originalRef, _ := originalDir.Snapshot.Peek()
	require.Same(t, ordinaryRef, originalRef)
	gotMeta, _ := private.MetaSnapshot.Peek()
	require.Same(t, meta, gotMeta)
	gotMount, _ := mountDir.Peek()
	gotMountRef, _ := gotMount.Snapshot.Peek()
	require.Same(t, mount, gotMountRef)
	require.NoError(t, wrapped.Release(t.Context()))
	require.Equal(t, 1, observations)
	require.Zero(t, ordinaryRef.releases)
	require.Zero(t, meta.releases)
	require.Zero(t, mount.releases)
}
