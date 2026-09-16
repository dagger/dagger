package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/format"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// These resolvers allocate their outputs internally. An isolated Go build overlay
// injects the operational Lazy immediately before the real recording guards,
// without adding a production hook or replacing the resolver or cleanup bodies.
func testProducerRecordingRejections(t *testing.T) {
	const childEnv = "DAGGER_TEST_PRODUCER_RECORDING_FAULT"
	if os.Getenv(childEnv) != "1" {
		root, err := filepath.Abs(filepath.Join("..", ".."))
		require.NoError(t, err)
		dir := t.TempDir()
		replace := map[string]string{}
		for _, injection := range []struct{ file, anchor, body string }{
			{"completed_producer.go", "}](value T, producer Lazy[T]) error {", `
switch value := any(value).(type) {
case *Directory:
	value.Lazy = &DirectorySubdirectoryLazy{LazyState: NewLazyState()}
	ref, _ := value.Snapshot.Peek()
	ref.(interface{ ObserveRecordingFault(dagql.Typed) }).ObserveRecordingFault(value)
case *File:
	value.Lazy = &FileBlobLazy{LazyState: NewLazyState()}
	ref, _ := value.Snapshot.Peek()
	ref.(interface{ ObserveRecordingFault(dagql.Typed) }).ObserveRecordingFault(value)
}
`},
			{"completed_producer.go", "func RecordCompletedContainerMountProducer(value *Container, producer Lazy[*Container]) error {", `
{
value.Lazy = producer
mount := value.Mounts[len(value.Mounts)-1]
var ref bkcache.ImmutableRef
if mount.DirectorySource != nil { dir, _ := mount.DirectorySource.Peek(); ref, _ = dir.Snapshot.Peek() } else { file, _ := mount.FileSource.Peek(); ref, _ = file.Snapshot.Peek() }
ref.(interface{ ObserveRecordingFault(dagql.Typed) }).ObserveRecordingFault(value)
}
`},
			{"builtincontainer.go", "func recordCompletedBuiltinProducer(container *Container, producer *ContainerBuiltinLazy) error {", `
container.Lazy = producer
fs, _ := container.FS.Peek()
ref, _ := fs.Snapshot.Peek()
ref.(interface{ ObserveRecordingFault(dagql.Typed) }).ObserveRecordingFault(container)
`},
		} {
			source := filepath.Join(root, "core", injection.file)
			input := source
			if previous := replace[source]; previous != "" {
				input = previous
			}
			data, err := os.ReadFile(input)
			require.NoError(t, err)
			require.Equal(t, 1, strings.Count(string(data), injection.anchor))
			modified, err := format.Source([]byte(strings.Replace(string(data), injection.anchor, injection.anchor+injection.body, 1)))
			require.NoError(t, err)
			overlay := filepath.Join(dir, injection.file)
			require.NoError(t, os.WriteFile(overlay, modified, 0600))
			replace[source] = overlay
		}
		config, err := json.Marshal(struct{ Replace map[string]string }{replace})
		require.NoError(t, err)
		configPath := filepath.Join(dir, "overlay.json")
		require.NoError(t, os.WriteFile(configPath, config, 0600))
		cmd := exec.CommandContext(t.Context(), "go", "test", "-overlay", configPath, "./core/schema", "-run", "^TestProducerResolverCleanup/constructed_output_recording$", "-count=1", "-v")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), childEnv+"=1")
		output, err := cmd.CombinedOutput()
		t.Log(string(output))
		require.NoError(t, err)
		return
	}

	for _, site := range []string{"cleaned", "HTTP", "bundle", "schema File", "builtin Container", "mounted File", "mounted Directory"} {
		t.Run(site, func(t *testing.T) {
			ctx, srv, cache, server := resolverOutputFixture(t)
			releaseErr := errors.New("injected recording cleanup failure")
			server.manager.recordingReleaseError = releaseErr
			var invoke func(context.Context) error
			var borrowed *resolverOutputRef
			switch site {
			case "mounted File", "mounted Directory":
				borrowed = &resolverOutputRef{root: t.TempDir(), id: "mount-input"}
				server.manager.inputs = map[string]*resolverOutputRef{borrowed.id: borrowed}
				platform := core.Platform{OS: "linux", Architecture: "amd64"}
				dir := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory]), Platform: platform}
				dir.SetPath("/")
				dir.SetSnapshot(borrowed)
				ctr := core.NewContainer(platform)
				ctr.FS.SetValue(dir)
				old := new(core.LazyAccessor[*core.Directory, *core.Container])
				old.SetValue(dir)
				ctr.Mounts = core.ContainerMounts{{Target: "/target", DirectorySource: old}}
				parent := resolverAttach(t, ctx, srv, cache, "mountParent", ctr)
				if site == "mounted Directory" {
					source := resolverAttach(t, ctx, srv, cache, "mountSourceDirectory", dir)
					id, err := source.ID()
					require.NoError(t, err)
					invoke = installRecordingFaultCall(t, srv, func(ctx context.Context) (dagql.ObjectResult[*core.Container], error) {
						out, err := (&containerSchema{}).withMountedDirectory(ctx, parent, containerWithMountedDirectoryArgs{Path: "/target", Source: dagql.NewID[*core.Directory](id), ReadOnly: true})
						require.Nil(t, out)
						return dagql.ObjectResult[*core.Container]{}, err
					})
				} else {
					file := &core.File{File: new(core.LazyAccessor[string, *core.File]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.File]), Platform: platform}
					file.SetPath("/payload")
					file.SetSnapshot(borrowed)
					source := resolverAttach(t, ctx, srv, cache, "mountSourceFile", file)
					id, err := source.ID()
					require.NoError(t, err)
					invoke = installRecordingFaultCall(t, srv, func(ctx context.Context) (dagql.ObjectResult[*core.Container], error) {
						out, err := (&containerSchema{}).withMountedFile(ctx, parent, containerWithMountedFileArgs{Path: "/target", Source: dagql.NewID[*core.File](id)})
						require.Nil(t, out)
						return dagql.ObjectResult[*core.Container]{}, err
					})
				}
			case "cleaned", "bundle":
				root := t.TempDir()
				run := func(args ...string) {
					output, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
					require.NoError(t, err, string(output))
				}
				run("init", "-b", "main")
				require.NoError(t, os.WriteFile(filepath.Join(root, "data"), []byte("saved"), 0644))
				run("add", ".")
				run("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "saved")
				borrowed = &resolverOutputRef{root: root, id: "source"}
				dir := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
				dir.Dir.SetValue("/")
				dir.Snapshot.SetValue(borrowed)
				input := resolverAttach(t, ctx, srv, cache, "source", dir)
				repo := resolverAttach(t, ctx, srv, cache, "repository", &core.GitRepository{Backend: &core.LocalGitRepository{Directory: input}})
				if site == "cleaned" {
					invoke = installRecordingFaultCall(t, srv, func(ctx context.Context) (dagql.ObjectResult[*core.Directory], error) {
						return (&gitSchema{}).cleaned(ctx, repo, struct{}{})
					})
				} else {
					bundlePath := filepath.Join(t.TempDir(), "saved.bundle")
					run("bundle", "create", bundlePath, "main")
					file := &core.File{File: new(core.LazyAccessor[string, *core.File]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.File])}
					file.File.SetValue(filepath.Base(bundlePath))
					file.Snapshot.SetValue(&resolverOutputRef{root: filepath.Dir(bundlePath), id: "bundle-file"})
					fileRow := resolverAttach(t, ctx, srv, cache, "bundleFile", file)
					bundle, err := core.ParseGitBundle(ctx, fileRow)
					require.NoError(t, err)
					bundleRow := resolverAttach(t, ctx, srv, cache, "bundle", bundle)
					id, err := bundleRow.ID()
					require.NoError(t, err)
					invoke = installRecordingFaultCall(t, srv, func(ctx context.Context) (dagql.ObjectResult[*core.Directory], error) {
						return (&gitSchema{}).withBundleDirectory(ctx, repo, gitWithBundleArgs{Bundle: dagql.NewID[*core.GitBundle](id)})
					})
				}
			case "HTTP":
				origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("saved")) }))
				defer origin.Close()
				state := resolverAttach(t, ctx, srv, cache, "_httpState", &core.HTTPState{URL: origin.URL})
				invoke = installRecordingFaultCall(t, srv, func(ctx context.Context) (dagql.ObjectResult[*core.File], error) {
					return (&httpSchema{}).httpStateResolve(ctx, state, httpStateResolveArgs{Name: "data", Permissions: 0644})
				})
			case "schema File":
				borrowed = &resolverOutputRef{root: t.TempDir(), id: "scratch"}
				dagql.Fields[*core.Query]{dagql.Func("directory", func(context.Context, *core.Query, struct{}) (*core.Directory, error) {
					dir := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
					dir.Dir.SetValue("/")
					dir.Snapshot.SetValue(borrowed)
					return dir, nil
				})}.Install(srv)
				var input dagql.ObjectResult[*core.Directory]
				require.NoError(t, srv.Select(ctx, srv.Root(), &input, dagql.Selector{Field: "directory"}))
				invoke = installRecordingFaultCall(t, srv, func(ctx context.Context) (dagql.ObjectResult[*core.File], error) {
					return (&querySchema{}).schemaJSONFile(ctx, srv.Root().(dagql.ObjectResult[*core.Query]), schemaJSONArgs{})
				})
			case "builtin Container":
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
				invoke = installRecordingFaultCall(t, srv, func(ctx context.Context) (dagql.ObjectResult[*core.Container], error) {
					return (&hostSchema{}).builtinContainer(ctx, srv.Root().(dagql.ObjectResult[*core.Query]), builtinContainerArgs{Digest: desc.Digest.String()})
				})
			}
			materialized := func() []uint64 {
				var ids []uint64
				for _, row := range cache.DebugEGraphSnapshot().Results {
					if row.HasValue {
						ids = append(ids, row.SharedResultID)
					}
				}
				return ids
			}
			before := materialized()
			err := invoke(ctx)
			require.ErrorContains(t, err, "operation already recorded")
			require.ErrorIs(t, err, releaseErr)
			require.Equal(t, before, materialized(), "recording rejection published a value")
			injected := 0
			for _, ref := range server.manager.outputs {
				if ref.faultValue == nil {
					if strings.HasPrefix(site, "mounted ") {
						require.Equal(t, 1, ref.releases, "cloned parent ref was not released once")
					} else {
						require.Zero(t, ref.releases, "released retained HTTP state")
					}
					continue
				}
				injected++
				require.Equal(t, 1, ref.releases)
				switch value := ref.faultValue.(type) {
				case *core.Directory:
					require.Same(t, ref.faultLazy, value.Lazy)
				case *core.File:
					require.Same(t, ref.faultLazy, value.Lazy)
				case *core.Container:
					require.Same(t, ref.faultLazy, value.Lazy)
				}
			}
			require.Equal(t, 1, injected, "recording fault did not reach the eager output")
			if borrowed != nil {
				require.Zero(t, borrowed.releases)
			}
		})
	}
}

func (r *resolverOutputRef) ObserveRecordingFault(value dagql.Typed) {
	r.faultValue = value
	switch value := value.(type) {
	case *core.Directory:
		r.faultLazy = value.Lazy
	case *core.File:
		r.faultLazy = value.Lazy
	case *core.Container:
		r.faultLazy = value.Lazy
	}
}

func installRecordingFaultCall[T dagql.Typed](t *testing.T, srv *dagql.Server, invoke func(context.Context) (dagql.ObjectResult[T], error)) func(context.Context) error {
	t.Helper()
	dagql.Fields[*core.Query]{dagql.NodeFunc("recordingFault", func(ctx context.Context, _ dagql.ObjectResult[*core.Query], _ struct{}) (dagql.ObjectResult[T], error) {
		value, err := invoke(ctx)
		require.Equal(t, dagql.ObjectResult[T]{}, value, "recording rejection returned an output")
		return value, err
	})}.Install(srv)
	return func(ctx context.Context) error {
		var result dagql.ObjectResult[T]
		err := srv.Select(ctx, srv.Root(), &result, dagql.Selector{Field: "recordingFault"})
		require.Equal(t, dagql.ObjectResult[T]{}, result)
		return err
	}
}
