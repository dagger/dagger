package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

type resolverCleanupRef struct {
	bkcache.ImmutableRef
	releases   int
	releaseErr error
}

func (r *resolverCleanupRef) Release(ctx context.Context) error {
	r.releases++
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return r.releaseErr
}

type resolverCleanupTree struct {
	core.GitRefBackend
	output *core.Directory
}

func (r *resolverCleanupTree) Tree(context.Context, *dagql.Server, bool, int, bool, []core.GitRemote) (*core.Directory, error) {
	return r.output, nil
}

type resolverCleanupRepo struct {
	core.GitRepositoryBackend
	tree core.GitRefBackend
}

func (r *resolverCleanupRepo) Get(context.Context, *gitutil.Ref) (core.GitRefBackend, error) {
	return r.tree, nil
}

type resolverInputRef struct {
	bkcache.ImmutableRef
	root string
}

func (r *resolverInputRef) Mount(context.Context, bool) (bkcache.MountableRef, error) {
	return resolverInputMount(r.root), nil
}

type resolverInputMount string

func (m resolverInputMount) Mount() ([]mount.Mount, func() error, error) {
	return []mount.Mount{{Type: "bind", Source: string(m)}}, func() error { return nil }, nil
}
func TestProducerResolverCleanup(t *testing.T) {
	t.Run("constructed output recording", testProducerRecordingRejections)
	for _, kind := range []string{"ref wrapping", "ref digest", "commit wrapping", "ref recording", "commit recording"} {
		t.Run(kind, func(t *testing.T) {
			server := &currentTypeDefsTestServer{}
			query := core.NewRoot(server)
			ctx := core.ContextWithQuery(t.Context(), query)
			srv, err := dagql.NewServer(ctx, query)
			require.NoError(t, err)
			server.dag = srv
			srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.Directory]{}))
			srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.GitRepository]{}))
			srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.GitRef]{}))
			srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.GitCommit]{}))
			cleanupErr := errors.New("injected release failure")
			ref := &resolverCleanupRef{releaseErr: cleanupErr}
			dir := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
			dir.Dir.SetValue("/")
			dir.Snapshot.SetValue(ref)
			if strings.HasSuffix(kind, "recording") {
				dir.Lazy = &core.DirectorySubdirectoryLazy{LazyState: core.NewLazyState()}
			}
			tree := &resolverCleanupTree{output: dir}
			if strings.HasPrefix(kind, "commit") {
				cache, e := dagql.NewCache(ctx, "", nil, nil)
				require.NoError(t, e)
				t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
				ctx = dagql.ContextWithCache(ctx, cache)
				input := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
				input.Dir.SetValue("/")
				input.Snapshot.SetValue(&resolverInputRef{root: t.TempDir()})
				call := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "directory", Type: dagql.NewResultCallType(input.Type())}
				result, e := cache.GetOrInitCall(ctx, "cleanup", srv, &dagql.CallRequest{ResultCall: call}, func(context.Context) (dagql.AnyResult, error) { return dagql.NewObjectResultForCall(input, srv, call) })
				require.NoError(t, e)
				local := &core.LocalGitRepository{Directory: result.(dagql.ObjectResult[*core.Directory])}
				tree.GitRefBackend, e = local.Get(ctx, &gitutil.Ref{SHA: "saved"})
				require.NoError(t, e)
			}
			var backend core.GitRepositoryBackend = &resolverCleanupRepo{tree: tree}
			if kind == "ref digest" {
				backend = &core.RemoteGitRepository{}
			}
			repo := &core.GitRepository{Backend: backend}
			repoResult, err := dagql.NewObjectResultForCall(repo, srv, &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "git", Type: dagql.NewResultCallType(repo.Type())})
			require.NoError(t, err)
			if strings.HasPrefix(kind, "commit") {
				commit := &core.GitCommit{Repo: repoResult, Backend: tree, Ref: &gitutil.Ref{SHA: "saved"}}
				parent, e := dagql.NewObjectResultForCall(commit, srv, &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "commit", Type: dagql.NewResultCallType(commit.Type())})
				require.NoError(t, e)
				_, err = (&gitSchema{}).commitTree(ctx, parent, commitTreeArgs{})
			} else {
				gitRef := &core.GitRef{Repo: repoResult, Backend: tree, Ref: &gitutil.Ref{}}
				parent, e := dagql.NewObjectResultForCall(gitRef, srv, &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "ref", Type: dagql.NewResultCallType(gitRef.Type())})
				require.NoError(t, e)
				if kind == "ref digest" {
					ctx = dagql.ContextWithCall(ctx, &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "tree", Type: dagql.NewResultCallType(dir.Type())})
				}
				// Depth 1 keeps the call on the branch that builds the tree here:
				// main serves default-argument trees from its shared full checkout,
				// which records no producer.
				_, err = (&gitSchema{}).tree(ctx, parent, treeArgs{Depth: 1})
			}
			require.Error(t, err)
			if strings.HasSuffix(kind, "recording") {
				require.ErrorContains(t, err, "operation already recorded")
				require.NotNil(t, dir.Lazy)
			}
			require.ErrorIs(t, err, cleanupErr)
			require.Equal(t, 1, ref.releases)
		})
	}
}

// The manager exposes real temporary files and counts only output ownership.
// Faults happen after the eager body has produced its immutable output.
type resolverOutputRef struct {
	bkcache.ImmutableRef
	root, id           string
	releases           int
	faultValue         dagql.Typed
	faultLazy          any
	faultReleaseError  error
	releaseAlwaysFails bool
}

func (r *resolverOutputRef) Mount(context.Context, bool) (bkcache.MountableRef, error) {
	return resolverInputMount(r.root), nil
}
func (r *resolverOutputRef) ID() string         { return r.id }
func (r *resolverOutputRef) SnapshotID() string { return r.id }
func (r *resolverOutputRef) Release(ctx context.Context) error {
	r.releases++
	if r.faultValue != nil || r.releaseAlwaysFails {
		return errors.Join(ctx.Err(), r.faultReleaseError)
	}
	return ctx.Err()
}
func (r *resolverOutputRef) Size(context.Context) (int64, error) { return 0, nil }

type resolverMutableRef struct {
	bkcache.MutableRef
	output *resolverOutputRef
}

func (r *resolverMutableRef) Mount(context.Context, bool) (bkcache.MountableRef, error) {
	return resolverInputMount(r.output.root), nil
}
func (r *resolverMutableRef) Commit(context.Context) (bkcache.ImmutableRef, error) {
	return r.output, nil
}
func (r *resolverMutableRef) Release(ctx context.Context) error { return ctx.Err() }

type resolverOutputManager struct {
	bkcache.SnapshotManager
	t                     *testing.T
	outputs               []*resolverOutputRef
	leaseFault            error
	recordingReleaseError error
	inputs                map[string]*resolverOutputRef
}

func (m *resolverOutputManager) Scratch(context.Context) (bkcache.ImmutableRef, error) {
	ref := &resolverOutputRef{root: m.t.TempDir(), id: "scratch", faultReleaseError: m.recordingReleaseError, releaseAlwaysFails: m.recordingReleaseError != nil}
	m.outputs = append(m.outputs, ref)
	return ref, nil
}

func (m *resolverOutputManager) GetBySnapshotID(_ context.Context, id string, _ ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	input := m.inputs[id]
	if input == nil {
		return nil, fmt.Errorf("missing test snapshot %s", id)
	}
	ref := &resolverOutputRef{root: input.root, id: id, faultReleaseError: m.recordingReleaseError}
	m.outputs = append(m.outputs, ref)
	return ref, nil
}

func (m *resolverOutputManager) AttachLease(context.Context, string, string) error {
	return m.leaseFault
}
func (m *resolverOutputManager) RemoveLease(context.Context, string) error { return nil }
func (m *resolverOutputManager) DeleteStaleDaggerOwnerLeases(context.Context, map[string]struct{}) error {
	return nil
}
func (m *resolverOutputManager) New(ctx context.Context, parent bkcache.ImmutableRef, _ ...bkcache.RefOption) (bkcache.MutableRef, error) {
	root := m.t.TempDir()
	if parent != nil {
		src := parent.(*resolverOutputRef).root
		require.NoError(m.t, filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, e := filepath.Rel(src, path)
			if e != nil {
				return e
			}
			dst := filepath.Join(root, rel)
			if info.IsDir() {
				return os.MkdirAll(dst, info.Mode())
			}
			data, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			return os.WriteFile(dst, data, info.Mode())
		}))
	}
	ref := &resolverOutputRef{root: root, id: fmt.Sprintf("output-%d", len(m.outputs)), faultReleaseError: m.recordingReleaseError}
	m.outputs = append(m.outputs, ref)
	return &resolverMutableRef{output: ref}, nil
}
func (m *resolverOutputManager) ImportImage(context.Context, *bkcache.ImportedImage, bkcache.ImportImageOpts) (bkcache.ImmutableRef, error) {
	ref := &resolverOutputRef{root: m.t.TempDir(), id: "builtin", faultReleaseError: m.recordingReleaseError}
	m.outputs = append(m.outputs, ref)
	return ref, nil
}

type resolverOutputServer struct {
	*currentTypeDefsTestServer
	manager *resolverOutputManager
	content content.Store
}

func (s *resolverOutputServer) DNS() *oci.DNSConfig                      { return &oci.DNSConfig{} }
func (s *resolverOutputServer) SnapshotManager() bkcache.SnapshotManager { return s.manager }
func (s *resolverOutputServer) OCIStore() content.Store                  { return s.content }
func (s *resolverOutputServer) BuiltinOCIStore() content.Store           { return s.content }
func resolverOutputFixture(t *testing.T) (context.Context, *dagql.Server, *dagql.Cache, *resolverOutputServer) {
	t.Helper()
	server := &resolverOutputServer{currentTypeDefsTestServer: &currentTypeDefsTestServer{}, manager: &resolverOutputManager{t: t}}
	query := core.NewRoot(server)
	ctx := core.ContextWithQuery(t.Context(), query)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "cleanup", SessionID: "cleanup"})
	cache, err := dagql.NewCache(ctx, "", server.manager, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	srv, err := dagql.NewServer(ctx, query)
	require.NoError(t, err)
	server.dag = srv
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.Directory]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.File]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.Container]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.HTTPState]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.GitRepository]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.GitBundle]{}))
	return ctx, srv, cache, server
}
func resolverAttach[T dagql.Typed](t *testing.T, ctx context.Context, srv *dagql.Server, cache *dagql.Cache, field string, value T) dagql.ObjectResult[T] {
	t.Helper()
	call := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field, Type: dagql.NewResultCallType(value.Type())}
	result, err := cache.GetOrInitCall(ctx, "cleanup", srv, &dagql.CallRequest{ResultCall: call}, func(context.Context) (dagql.AnyResult, error) { return dagql.NewObjectResultForCall(value, srv, call) })
	require.NoError(t, err)
	return result.(dagql.ObjectResult[T])
}
func TestProducerResolverOutputCleanup(t *testing.T) { testProducerResolverOutputs(t, false) }
func TestProducerResolverCapture(t *testing.T) {
	testProducerResolverOutputs(t, true)
	for _, kind := range []string{"gitCleaned", "gitTree", "gitCommitTree"} {
		t.Run(kind, func(t *testing.T) {
			ctx, srv, cache, server := resolverOutputFixture(t)
			srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.GitRef]{}))
			srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.GitCommit]{}))
			root := t.TempDir()
			run := func(args ...string) string {
				out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
				require.NoError(t, err, string(out))
				return strings.TrimSpace(string(out))
			}
			run("init", "-b", "main")
			require.NoError(t, os.WriteFile(filepath.Join(root, "data"), []byte("saved"), 0644))
			run("add", ".")
			run("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "saved")
			sha := run("rev-parse", "HEAD")
			dir := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
			dir.Dir.SetValue("/")
			dir.Snapshot.SetValue(&resolverOutputRef{root: root, id: "source"})
			input := resolverAttach(t, ctx, srv, cache, "source", dir)
			repo := &core.GitRepository{Backend: &core.LocalGitRepository{Directory: input}}
			parent := resolverAttach(t, ctx, srv, cache, "repository", repo)
			var result dagql.ObjectResult[*core.Directory]
			var err error
			if kind == "gitCleaned" {
				ctx = producerResolverCall(ctx, "__cleaned", dir)
				dagql.Fields[*core.GitRepository]{dagql.NodeFunc("__cleaned", (&gitSchema{}).cleaned)}.Install(srv)
				err = srv.Select(ctx, parent, &result, dagql.Selector{Field: "__cleaned"})
			} else {
				ref := &gitutil.Ref{Name: "refs/heads/main", SHA: sha}
				backend, e := repo.Backend.Get(ctx, ref)
				require.NoError(t, e)
				ctx = producerResolverCall(ctx, "tree", dir)
				if kind == "gitTree" {
					input := resolverAttach(t, ctx, srv, cache, "ref", &core.GitRef{Repo: parent, Ref: ref, Backend: backend})
					// Depth 1: see TestProducerResolverCleanup.
					result, err = (&gitSchema{}).tree(ctx, input, treeArgs{Depth: 1})
				} else {
					input := resolverAttach(t, ctx, srv, cache, "commit", &core.GitCommit{Repo: parent, Ref: ref, Backend: backend})
					result, err = (&gitSchema{}).commitTree(ctx, input, commitTreeArgs{})
				}
			}
			require.NoError(t, err)
			assertResolverProducer(t, ctx, cache, result.Self(), kind)
			require.Len(t, server.manager.outputs, 1)
			if kind == "gitCleaned" {
				require.Zero(t, server.manager.outputs[0].releases)
			} else {
				require.NoError(t, result.Self().OnRelease(ctx))
				require.Equal(t, 1, server.manager.outputs[0].releases)
			}
		})
	}
}
func testProducerResolverOutputs(t *testing.T, recorded bool) {
	t.Run("scratch wrapping", func(t *testing.T) {
		ctx, srv, cache, server := resolverOutputFixture(t)
		server.platform = core.Platform{OS: "linux", Architecture: "amd64"}
		if recorded {
			ctx = producerResolverCall(ctx, "directory", &core.Directory{})
		} else {
			server.manager.recordingReleaseError = errors.New("scratch cleanup failure")
		}
		result, err := (&directorySchema{}).directory(ctx, srv.Root().(dagql.ObjectResult[*core.Query]), struct{}{})
		require.Len(t, server.manager.outputs, 1)
		if recorded {
			require.NoError(t, err)
			assertResolverProducer(t, ctx, cache, result.Self(), "scratch")
			require.NoError(t, result.Self().OnRelease(ctx))
		} else {
			require.ErrorContains(t, err, "call is nil")
			require.ErrorIs(t, err, server.manager.recordingReleaseError)
			require.Equal(t, dagql.ObjectResult[*core.Directory]{}, result)
		}
		require.Equal(t, 1, server.manager.outputs[0].releases)
	})
	t.Run("http state sync", func(t *testing.T) {
		ctx, srv, cache, server := resolverOutputFixture(t)
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "body") }))
		defer origin.Close()
		state := &core.HTTPState{URL: origin.URL}
		parent := resolverAttach(t, ctx, srv, cache, "_httpState", state)
		if recorded {
			ctx = producerResolverCall(ctx, "_resolve", &core.File{})
		} else {
			server.manager.leaseFault = errors.New("injected state owner failure")
		}
		var err error
		result, callErr := (&httpSchema{}).httpStateResolve(ctx, parent, httpStateResolveArgs{Name: "data", Permissions: 0644})
		err = callErr
		if recorded {
			require.NoError(t, err)
			assertResolverProducer(t, ctx, cache, result.Self(), "httpResolve")
			require.NoError(t, result.Self().OnRelease(ctx))
			return
		}
		require.ErrorContains(t, err, "sync http state snapshot owner leases")
		require.Len(t, server.manager.outputs, 2)
		require.Zero(t, server.manager.outputs[0].releases)
		require.Equal(t, 1, server.manager.outputs[1].releases)
		server.manager.leaseFault = nil
	})
	t.Run("schema wrapping", func(t *testing.T) {
		ctx, srv, cache, server := resolverOutputFixture(t)
		dagql.Fields[*core.Query]{dagql.Func("directory", func(context.Context, *core.Query, struct{}) (*core.Directory, error) {
			dir := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
			dir.Dir.SetValue("/")
			dir.Snapshot.SetValue(&resolverOutputRef{root: t.TempDir(), id: "scratch"})
			return dir, nil
		})}.Install(srv)
		if recorded {
			ctx = producerResolverCall(ctx, "__schemaJSONFile", &core.File{})
		}
		result, err := (&querySchema{}).schemaJSONFile(ctx, srv.Root().(dagql.ObjectResult[*core.Query]), schemaJSONArgs{})
		if recorded {
			require.NoError(t, err)
			assertResolverProducer(t, ctx, cache, result.Self(), "file.blob")
			require.NoError(t, result.Self().OnRelease(ctx))
			return
		}
		require.ErrorContains(t, err, "call is nil")
		require.Len(t, server.manager.outputs, 1)
		require.Equal(t, 1, server.manager.outputs[0].releases)
	})
	t.Run("builtin wrapping", func(t *testing.T) {
		ctx, srv, cache, server := resolverOutputFixture(t)
		store, err := local.NewStore(t.TempDir())
		require.NoError(t, err)
		server.content = store
		config := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}`)
		configDesc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: digest.FromBytes(config), Size: int64(len(config))}
		require.NoError(t, content.WriteBlob(ctx, store, "config", bytes.NewReader(config), configDesc))
		manifest, err := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: configDesc, Layers: []ocispec.Descriptor{}})
		require.NoError(t, err)
		desc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(manifest), Size: int64(len(manifest))}
		require.NoError(t, content.WriteBlob(ctx, store, "manifest", bytes.NewReader(manifest), desc))
		if recorded {
			ctx = producerResolverCall(ctx, "_builtinContainer", &core.Container{})
		}
		result, callErr := (&hostSchema{}).builtinContainer(ctx, srv.Root().(dagql.ObjectResult[*core.Query]), builtinContainerArgs{Digest: desc.Digest.String()})
		err = callErr
		if recorded {
			require.NoError(t, err)
			assertResolverProducer(t, ctx, cache, result.Self(), "")
			require.NoError(t, result.Self().OnRelease(ctx))
			return
		}
		require.ErrorContains(t, err, "call is nil")
		require.Len(t, server.manager.outputs, 1)
		require.Equal(t, 1, server.manager.outputs[0].releases)
	})
	t.Run("bundle wrapping", func(t *testing.T) {
		ctx, srv, cache, server := resolverOutputFixture(t)
		root := t.TempDir()
		for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "initial"}, {"bundle", "create", filepath.Join(t.TempDir(), "repo.bundle"), "--all"}} {
			cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
			if args[0] == "bundle" {
				file := &core.File{File: new(core.LazyAccessor[string, *core.File]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.File])}
				file.File.SetValue(filepath.Base(args[2]))
				file.Snapshot.SetValue(&resolverOutputRef{root: filepath.Dir(args[2]), id: "bundle-file"})
				fileRes := resolverAttach(t, ctx, srv, cache, "bundleFile", file)
				bundle, err := core.ParseGitBundle(ctx, fileRes)
				require.NoError(t, err)
				bundleRes := resolverAttach(t, ctx, srv, cache, "bundle", bundle)
				dir := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
				dir.Dir.SetValue("/")
				dir.Snapshot.SetValue(&resolverOutputRef{root: root, id: "repository"})
				dirRes := resolverAttach(t, ctx, srv, cache, "repository", dir)
				repo := &core.GitRepository{Backend: &core.LocalGitRepository{Directory: dirRes}}
				repoRes := resolverAttach(t, ctx, srv, cache, "git", repo)
				bundleID, err := bundleRes.ID()
				require.NoError(t, err)
				if recorded {
					ctx = producerResolverCall(ctx, "__withBundleDirectory", &core.Directory{})
				}
				result, callErr := (&gitSchema{}).withBundleDirectory(ctx, repoRes, gitWithBundleArgs{Bundle: dagql.NewID[*core.GitBundle](bundleID)})
				err = callErr
				if recorded {
					require.NoError(t, err)
					assertResolverProducer(t, ctx, cache, result.Self(), "gitBundleImport")
					require.NoError(t, result.Self().OnRelease(ctx))
					return
				}
				require.ErrorContains(t, err, "call is nil")
				require.Len(t, server.manager.outputs, 1)
				require.Equal(t, 1, server.manager.outputs[0].releases)
			}
		}
	})
}

func producerResolverCall(ctx context.Context, field string, value dagql.Typed) context.Context {
	return dagql.ContextWithCall(ctx, &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field, Type: dagql.NewResultCallType(value.Type())})
}
func assertResolverProducer(t *testing.T, ctx context.Context, cache *dagql.Cache, value dagql.PersistedObject, kind string) {
	t.Helper()
	encoded, err := value.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(cache, 0, dagql.CurrentCall(ctx)))
	require.NoError(t, err)
	var payload struct {
		LazyKind string          `json:"lazyKind"`
		LazyJSON json.RawMessage `json:"lazyJSON"`
	}
	require.NoError(t, json.Unmarshal(encoded.JSON, &payload))
	require.Equal(t, kind, payload.LazyKind)
	require.NotEmpty(t, payload.LazyJSON)
}
