package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/moby/locker"
	"github.com/stretchr/testify/require"
)

type transferGitServer struct {
	*cacheVolumeTestQueryServer
	locks *locker.Locker
	ns    *os.File
}

func (s *transferGitServer) Platform() Platform     { return Platform{OS: "linux", Architecture: "amd64"} }
func (s *transferGitServer) Locker() *locker.Locker { return s.locks }
func (s *transferGitServer) DNS() *oci.DNSConfig    { return &oci.DNSConfig{} }
func (s *transferGitServer) CleanMountNS() *os.File { return s.ns }

func TestValueTransferPartsGitTrees(t *testing.T) {
	for _, backend := range []string{"local", "remote"} {
		t.Run(backend, func(t *testing.T) {
			producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
			observed := &transferObservedSnapshots{SnapshotManager: producer.Manager}
			producer.Manager = observed
			ctx, cache, srv := transferCache(t, producer, filepath.Join(t.TempDir(), "a.db"), "a")
			ns, err := os.Open("/proc/self/ns/mnt")
			require.NoError(t, err)
			defer ns.Close()
			query, err := CurrentQuery(ctx)
			require.NoError(t, err)
			query.Server = &transferGitServer{cacheVolumeTestQueryServer: query.Server.(*cacheVolumeTestQueryServer), locks: locker.New(), ns: ns}
			source := t.TempDir()
			git := func(dir string, args ...string) string {
				t.Helper()
				cmd := exec.CommandContext(ctx, "git", args...)
				cmd.Dir = dir
				output, err := cmd.CombinedOutput()
				require.NoError(t, err, "%s", output)
				return strings.TrimSpace(string(output))
			}
			git(source, "init", "--initial-branch=main")
			require.NoError(t, os.WriteFile(filepath.Join(source, "tree.txt"), []byte("git selected bytes"), 0644))
			git(source, "add", "tree.txt")
			git(source, "-c", "user.name=transfer", "-c", "user.email=transfer@example.invalid", "commit", "-m", "fixture")
			ref := &gitutil.Ref{Name: "refs/heads/main", SHA: git(source, "rev-parse", "HEAD")}
			var selected *Directory
			if backend == "local" {
				mutable, err := producer.Manager.New(ctx, nil)
				require.NoError(t, err)
				require.NoError(t, MountRef(ctx, mutable, func(path string, _ *mount.Mount) error {
					git(path, "clone", "--no-hardlinks", source, ".")
					return nil
				}))
				snapshot, err := mutable.Commit(ctx)
				require.NoError(t, err)
				dir := &Directory{Platform: query.Platform(), Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory])}
				dir.SetPath("/")
				dir.SetSnapshot(snapshot)
				local := &LocalGitRepository{Directory: attachTransferObject(t, ctx, cache, srv, "a", "localGitSource", dir)}
				selected, err = (&LocalGitRef{Ref: ref, repo: local}).Tree(ctx, srv, true, 0, false, nil)
				require.NoError(t, err)
			} else {
				// The real remote backend fetches from a local file transport, keeping
				// this selected-byte test independent of network services.
				url := &gitutil.GitURL{Scheme: "file", Path: source}
				srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*RemoteGitMirror]{}))
				mirror := attachTransferObject(t, ctx, cache, srv, "a", "remoteGitMirror", NewRemoteGitMirror(url.Remote()))
				remote := &RemoteGitRepository{URL: url, Mirror: mirror}
				selected, err = (&RemoteGitRef{Ref: ref, repo: remote}).Tree(ctx, srv, true, 0, false, nil)
				require.NoError(t, err)
			}
			result := attachTransferObject(t, ctx, cache, srv, "a", "gitTree", selected)
			observed.opens = nil
			err = cache.WithExportedValues(ctx, dagql.ValueSelection{Roots: []dagql.AnyResult{result}, Outputs: []dagql.SelectedValueOutput{{Result: result, Address: dagql.PersistedPartAddress{Part: "snapshot"}}}}, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(ctx context.Context, values *dagql.ExportedValues) error {
				require.Len(t, values.Chains.Entries, 1)
				entry := values.Chains.Entries[0]
				copied, err := consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: entry.Layers, Provider: entry.Provider})
				require.NoError(t, err)
				defer copied.Release(context.WithoutCancel(ctx))
				require.NoError(t, MountRef(ctx, copied, func(root string, _ *mount.Mount) error {
					bytes, err := os.ReadFile(filepath.Join(root, "tree.txt"))
					require.NoError(t, err)
					require.Equal(t, "git selected bytes", string(bytes))
					_, err = os.Stat(filepath.Join(root, ".git"))
					require.True(t, os.IsNotExist(err), "discardGitDir survives transfer")
					return nil
				}, mountRefAsReadOnly))
				return nil
			})
			require.NoError(t, err)
			snapshot, ok := selected.Snapshot.Peek()
			require.True(t, ok)
			require.Equal(t, []string{snapshot.SnapshotID()}, observed.opens)
		})
	}
}

type transferObservedSnapshots struct {
	bkcache.SnapshotManager
	mu     sync.Mutex
	opens  []string
	failOn string
}

func (m *transferObservedSnapshots) GetBySnapshotID(ctx context.Context, id string, opts ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	m.mu.Lock()
	m.opens = append(m.opens, id)
	failed := m.failOn == id
	m.mu.Unlock()
	if failed {
		return nil, fmt.Errorf("injected snapshot open failure")
	}
	return m.SnapshotManager.GetBySnapshotID(ctx, id, opts...)
}

func TestValueTransferPartsSelectedChain(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	prefix, _ := producer.Build(t, nil, "private.txt", "whole parent bytes")
	tree, _ := producer.Build(t, prefix, "visible/value.txt", "selected bytes")
	observed := &transferObservedSnapshots{SnapshotManager: producer.Manager}
	producer.Manager = observed
	ctx, cache, srv := transferCache(t, producer, filepath.Join(t.TempDir(), "a.db"), "a")
	root := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Platform: Platform{OS: "linux", Architecture: "amd64"}}
	root.SetPath("/")
	root.SetSnapshot(tree)
	rootResult := attachTransferObject(t, ctx, cache, srv, "a", "hostCapture", root)
	view, err := root.Subdirectory(ctx, rootResult, "visible")
	require.NoError(t, err)
	viewResult := attachTransferObject(t, ctx, cache, srv, "a", "view", view)
	require.NoError(t, cache.Evaluate(ctx, viewResult))
	file, err := view.Subfile(ctx, viewResult, "value.txt")
	require.NoError(t, err)
	fileResult := attachTransferObject(t, ctx, cache, srv, "a", "nestedFile", file)
	require.NoError(t, cache.Evaluate(ctx, fileResult))
	observed.opens = nil
	var bundle dagql.ValueBundle
	var borrowed *dagql.SelectedChains
	err = cache.WithExportedValues(ctx, dagql.ValueSelection{Roots: []dagql.AnyResult{fileResult}, Outputs: []dagql.SelectedValueOutput{{Result: fileResult, Address: dagql.PersistedPartAddress{Part: "snapshot"}}}}, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(ctx context.Context, values *dagql.ExportedValues) error {
		bundle, borrowed = values.Bundle, values.Chains
		require.Len(t, values.Chains.Entries, 1)
		entry := values.Chains.Entries[0]
		opened, err := consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: entry.Layers, Provider: entry.Provider})
		require.NoError(t, err)
		defer opened.Release(context.WithoutCancel(ctx))
		testutil.CheckFile(t, opened, "private.txt", "whole parent bytes")
		testutil.CheckFile(t, opened, "visible/value.txt", "selected bytes")
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{tree.SnapshotID()}, observed.opens, "the view opens its exact parent snapshot once")
	require.NoError(t, borrowed.Release(ctx), "chain release is idempotent after the callback")
	require.Len(t, bundle.Outputs, 1)
	require.NotNil(t, bundle.Outputs[0].Owner)
	bctx, b, bsrv := transferCache(t, consumer, filepath.Join(t.TempDir(), "b.db"), "b")
	imported, err := b.ImportValues(bctx, bundle)
	require.NoError(t, err)
	loaded, err := b.LoadResultByResultID(bctx, "b", bsrv, imported[0].ResultID)
	require.NoError(t, err)
	pending := loaded.(dagql.ObjectResult[*File])
	require.Equal(t, "/visible/value.txt", mustTransferPath(t, bctx, pending))
	require.ErrorIs(t, b.Evaluate(bctx, pending), dagql.ErrUnavailablePart)
	require.NoError(t, b.WithExportedValues(bctx, dagql.ValueSelection{Roots: []dagql.AnyResult{pending}}, config.RefConfig{}, func(_ context.Context, forward *dagql.ExportedValues) error {
		require.Empty(t, forward.Chains.Entries)
		require.Empty(t, forward.Bundle.Outputs)
		require.Len(t, forward.Bundle.Values[len(forward.Bundle.Values)-1].Record.Envelope.PendingOffers, 1)
		return nil
	}))
}
func mustTransferPath(t *testing.T, ctx context.Context, file dagql.ObjectResult[*File]) string {
	t.Helper()
	path, err := file.Self().PathOrEval(ctx, file)
	require.NoError(t, err)
	return path
}

func TestValueTransferPartsContainerMount(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	sibling, _ := producer.Build(t, nil, "sibling.txt", "unselected bytes")
	selected, _ := producer.Build(t, nil, "selected.txt", "mount bytes")
	observed := &transferObservedSnapshots{SnapshotManager: producer.Manager}
	producer.Manager = observed
	ctx, cache, srv := transferCache(t, producer, filepath.Join(t.TempDir(), "a.db"), "a")
	platform := Platform{OS: "linux", Architecture: "amd64"}
	newDir := func(ref bkcache.ImmutableRef) *Directory {
		dir := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Platform: platform}
		dir.SetPath("/")
		dir.SetSnapshot(ref)
		return dir
	}
	ctr := NewContainer(platform)
	ctr.FS.setValue(newDir(sibling))
	mount := new(LazyAccessor[*Directory, *Container])
	mount.setValue(newDir(selected))
	ctr.Mounts = ContainerMounts{{Target: "/selected", DirectorySource: mount}}
	result := attachTransferObject(t, ctx, cache, srv, "a", "mountContainer", ctr)
	observed.opens = nil
	selection := dagql.ValueSelection{Roots: []dagql.AnyResult{result}, Outputs: []dagql.SelectedValueOutput{{Result: result, Address: dagql.PersistedPartAddress{Part: "mount:/selected"}}}}
	require.NoError(t, cache.WithExportedValues(ctx, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(ctx context.Context, values *dagql.ExportedValues) error {
		require.Len(t, values.Chains.Entries, 1)
		chain := values.Chains.Entries[0]
		ref, err := consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: chain.Layers, Provider: chain.Provider})
		require.NoError(t, err)
		defer ref.Release(context.WithoutCancel(ctx))
		testutil.CheckFile(t, ref, "selected.txt", "mount bytes")
		return nil
	}))
	require.Equal(t, []string{selected.SnapshotID()}, observed.opens, "unselected filesystem sibling must not open")
	observed.failOn = selected.SnapshotID()
	err := cache.WithExportedValues(ctx, selection, config.RefConfig{}, func(context.Context, *dagql.ExportedValues) error {
		t.Fatal("broken completed link delivered")
		return nil
	})
	require.ErrorContains(t, err, "injected snapshot open failure")
}
