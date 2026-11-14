package schema

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
)

// importLazyFixture is a lazy container Container.import makes of an image
// tarball in a file, on a scratch container, and the image's manifest digest.
// The image has only a config, with a platform unless fallback. With missing,
// the file holds no image, and the import fails.
func importLazyFixture(t *testing.T, missing, fallback bool) (context.Context, *dagql.Server, *dagql.Cache, *resolverOutputServer, dagql.ObjectResult[*core.Container], digest.Digest) {
	t.Helper()
	ctx, srv, cache, server := resolverOutputFixture(t)
	server.platform = core.Platform{OS: "linux", Architecture: "arm64"}
	packaged, err := local.NewStore(t.TempDir())
	require.NoError(t, err)
	server.content = packaged
	var tarball bytes.Buffer
	var manifest digest.Digest
	if !missing {
		config := ocispec.Image{Platform: ocispec.Platform{OS: "linux", Architecture: "x86_64"}, RootFS: ocispec.RootFS{Type: "layers"}, Config: ocispec.ImageConfig{Env: []string{"PREFIX=/go"}, WorkingDir: "/workspace", User: "123:456", Entrypoint: []string{"echo"}, Cmd: []string{"default"}}}
		if fallback {
			config.Platform = ocispec.Platform{}
		}
		configJSON, err := json.Marshal(config)
		require.NoError(t, err)
		cfg := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: digest.FromBytes(configJSON), Size: int64(len(configJSON))}
		manifestJSON, err := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: cfg, Layers: []ocispec.Descriptor{}})
		require.NoError(t, err)
		desc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(manifestJSON), Size: int64(len(manifestJSON))}
		indexJSON, err := json.Marshal(ocispec.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex, Manifests: []ocispec.Descriptor{desc}})
		require.NoError(t, err)
		tw := tar.NewWriter(&tarball)
		for _, f := range []struct {
			name string
			data []byte
		}{
			{ocispec.ImageLayoutFile, []byte(`{"imageLayoutVersion":"1.0.0"}`)},
			{"index.json", indexJSON},
			{"blobs/sha256/" + cfg.Digest.Encoded(), configJSON},
			{"blobs/sha256/" + desc.Digest.Encoded(), manifestJSON},
		} {
			require.NoError(t, tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.data)), Typeflag: tar.TypeReg}))
			_, err := tw.Write(f.data)
			require.NoError(t, err)
		}
		require.NoError(t, tw.Close())
		manifest = desc.Digest
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "image.tar"), tarball.Bytes(), 0o644))
	file := &core.File{File: new(core.LazyAccessor[string, *core.File]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.File])}
	file.File.SetValue("image.tar")
	file.Snapshot.SetValue(&resolverOutputRef{root: dir, id: "image-tarball"})
	source := persistableAttach(t, ctx, srv, cache, "imageTarball", file)
	scratch := persistableAttach(t, ctx, srv, cache, "container", core.NewContainer(server.platform))
	sourceID, err := source.ID()
	require.NoError(t, err)
	dagql.Fields[*core.Container]{dagql.NodeFunc("import", (&containerSchema{}).import_)}.Install(srv)
	var parent dagql.ObjectResult[*core.Container]
	require.NoError(t, srv.Select(ctx, scratch, &parent, dagql.Selector{Field: "import", Args: []dagql.NamedInput{{Name: "source", Value: dagql.NewID[*core.File](sourceID)}}}))
	require.False(t, parent.Self().Lazy.IsEvaluated())
	require.Empty(t, server.manager.outputs)
	return ctx, srv, cache, server, parent, manifest
}

// persistableAttach is resolverAttach for a result whose record is kept.
func persistableAttach[T dagql.Typed](t *testing.T, ctx context.Context, srv *dagql.Server, cache *dagql.Cache, field string, value T) dagql.ObjectResult[T] {
	t.Helper()
	call := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field, Type: dagql.NewResultCallType(value.Type())}
	result, err := cache.GetOrInitCall(ctx, "cleanup", srv, &dagql.CallRequest{ResultCall: call, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(value, srv, call)
	})
	require.NoError(t, err)
	return result.(dagql.ObjectResult[T])
}

func TestImportMetadataSelectors(t *testing.T) {
	for _, test := range []struct {
		name, path               string
		file, relative, fallback bool
	}{
		{name: "Directory", path: "$PREFIX/pkg"}, {name: "File", path: "$PREFIX/pkg/data", file: true},
		{name: "relative Directory", path: "pkg", relative: true}, {name: "relative File", path: "pkg/data", file: true, relative: true},
		{name: "platform fallback", path: "$PREFIX/pkg", fallback: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, srv, cache, server, parent, _ := importLazyFixture(t, false, test.fallback)
			store := testutil.NewStore(t)
			server.manager.SnapshotManager = store.Manager
			ref, _ := store.Build(t, nil, "go/pkg/data", "saved")
			server.manager.imageRef, _ = store.Build(t, ref, "workspace/pkg/data", "saved")

			s := &containerSchema{}
			dagql.Fields[*core.Container]{dagql.NodeFunc("directory", s.directory), dagql.NodeFunc("file", s.file), dagql.NodeFunc("rootfs", s.rootfs)}.Install(srv)
			ds := &directorySchema{}
			dagql.Fields[*core.Directory]{dagql.NodeFunc("directory", ds.subdirectory), dagql.NodeFunc("file", ds.file)}.Install(srv)
			wantPlatform := "linux/amd64"
			if test.fallback {
				wantPlatform = "linux/arm64"
			}
			wantPath := "/go/pkg"
			if test.relative {
				wantPath = "/workspace/pkg"
			}
			if test.file {
				wantPath += "/data"
			}
			selector := dagql.Selector{Field: "directory", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String(test.path)}, {Name: "expand", Value: dagql.Boolean(!test.relative)}}}
			if test.file {
				selector.Field = "file"
				var output dagql.ObjectResult[*core.File]
				require.NoError(t, srv.Select(ctx, parent, &output, selector))
				lazy := output.Self().Lazy.(*core.ContainerFileLazy)
				if !test.relative {
					require.Equal(t, wantPath, lazy.Path)
				}
				path, _ := output.Self().File.Peek()
				require.Equal(t, wantPath, path)
				require.Equal(t, wantPlatform, output.Self().Platform.Format())
			} else {
				var output dagql.ObjectResult[*core.Directory]
				require.NoError(t, srv.Select(ctx, parent, &output, selector))
				lazy := output.Self().Lazy.(*core.ContainerDirectoryLazy)
				if !test.relative {
					require.Equal(t, wantPath, lazy.Path)
				}
				path, _ := output.Self().Dir.Peek()
				require.Equal(t, wantPath, path)
				require.Equal(t, wantPlatform, output.Self().Platform.Format())
			}
			// Reading the selected bytes evaluates the output through a read-only
			// mount; native TestPipeline/Cold reads them. Here the imported parent
			// is evaluated directly.
			require.NoError(t, cache.Evaluate(ctx, parent))
			require.True(t, parent.Self().Lazy.IsEvaluated())
		})
	}
}

func TestImportMetadataConsumersStopOnFailure(t *testing.T) {
	s := &containerSchema{}
	for _, test := range []struct {
		name string
		run  func(context.Context, dagql.ObjectResult[*core.Container]) error
	}{
		{name: "from", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.from(ctx, p, containerFromArgs{})
			return err
		}},
		{name: "withRootfs", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withRootfs(ctx, p, containerWithRootFSArgs{})
			return err
		}},
		{name: "rootfs", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.rootfs(ctx, p, struct{}{})
			return err
		}},
		{name: "build", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.build(ctx, p, containerBuildArgs{})
			return err
		}},
		{name: "withSymlink", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withSymlink(ctx, p, containerWithSymlinkArgs{})
			return err
		}},
		{name: "withWorkdir", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withWorkdir(ctx, p, containerWithWorkdirArgs{})
			return err
		}},
		{name: "withMountedDirectory", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withMountedDirectory(ctx, p, containerWithMountedDirectoryArgs{})
			return err
		}},
		{name: "withMountedFile", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withMountedFile(ctx, p, containerWithMountedFileArgs{})
			return err
		}},
		{name: "withMountedCache", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withMountedCache(ctx, p, containerWithMountedCacheArgs{})
			return err
		}},
		{name: "withMountedVolume", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withMountedVolume(ctx, p, containerWithMountedVolumeArgs{})
			return err
		}},
		{name: "withMountedTemp", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withMountedTemp(ctx, p, containerWithMountedTempArgs{})
			return err
		}},
		{name: "withoutMount", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withoutMount(ctx, p, containerWithoutMountArgs{})
			return err
		}},
		{name: "directory", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.directory(ctx, p, containerDirectoryArgs{})
			return err
		}},
		{name: "file", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.file(ctx, p, containerFileArgs{})
			return err
		}},
		{name: "withMountedSecret", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withMountedSecret(ctx, p, containerWithMountedSecretArgs{})
			return err
		}},
		{name: "withUnixSocket", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withUnixSocket(ctx, p, containerWithUnixSocketArgs{})
			return err
		}},
		{name: "withoutUnixSocket", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withoutUnixSocket(ctx, p, containerWithoutUnixSocketArgs{})
			return err
		}},
		{name: "withDirectory", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withDirectory(ctx, p, containerWithDirectoryArgs{})
			return err
		}},
		{name: "withFile", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withFile(ctx, p, containerWithFileArgs{})
			return err
		}},
		{name: "withFiles", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withFiles(ctx, p, containerWithFilesArgs{})
			return err
		}},
		{name: "withoutDirectory", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withoutDirectory(ctx, p, containerWithoutDirectoryArgs{})
			return err
		}},
		{name: "withoutFile", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withoutFile(ctx, p, containerWithoutFileArgs{})
			return err
		}},
		{name: "withoutFiles", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withoutFiles(ctx, p, containerWithoutFilesArgs{})
			return err
		}},
		{name: "withNewFile", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withNewFile(ctx, p, containerWithNewFileArgs{})
			return err
		}},
		{name: "withUser", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withUser(ctx, p, containerWithUserArgs{})
			return err
		}},
		{name: "withEnvVariable", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withEnvVariable(ctx, p, containerWithVariableArgs{})
			return err
		}},
		{name: "withAnnotation", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withAnnotation(ctx, p, containerWithAnnotationArgs{})
			return err
		}},
		{name: "withLabel", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withLabel(ctx, p, containerWithLabelArgs{})
			return err
		}},
		{name: "withExec", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.withExec(ctx, p, containerExecArgs{})
			return err
		}},
		{name: "user", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.user(ctx, p, struct{}{})
			return err
		}},
		{name: "platform", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.platform(ctx, p, struct{}{})
			return err
		}},
		{name: "healthcheck", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			_, err := s.healthcheck(ctx, p, struct{}{})
			return err
		}},
		{name: "cache dynamic input", run: func(ctx context.Context, p dagql.ObjectResult[*core.Container]) error {
			return s.withMountedCacheDynamicInputs(ctx, p, containerWithMountedCacheArgs{}, &dagql.CallRequest{})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, _, _, server, parent, _ := importLazyFixture(t, true, false)
			require.ErrorContains(t, test.run(ctx, parent), "image archive import")
			require.False(t, parent.Self().Lazy.IsEvaluated())
			require.Empty(t, server.manager.outputs)
		})
	}
}

func TestImportLegacyServiceMetadata(t *testing.T) {
	for _, exec := range []bool{false, true} {
		t.Run(map[bool]string{false: "default command", true: "saved exec expansion"}[exec], func(t *testing.T) {
			ctx, srv, cache, _, parent, _ := importLazyFixture(t, false, false)
			srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.Service]{}))
			dagql.Fields[*core.Container]{dagql.NodeFunc("asService", (&serviceSchema{}).containerAsServiceLegacy)}.Install(srv)
			if exec {
				// Preserve a real withExec call frame without evaluating its body.
				dagql.Fields[*core.Container]{dagql.NodeFunc("withExec", (&containerSchema{}).withExec)}.Install(srv)
				parentID, err := cache.PersistedResultID(parent)
				require.NoError(t, err)
				frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "withExec", Type: dagql.NewResultCallType(parent.Type()), Receiver: &dagql.ResultCallRef{ResultID: parentID}, Args: []*dagql.ResultCallArg{
					{Name: "args", Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindList, ListItems: []*dagql.ResultCallLiteral{{Kind: dagql.ResultCallLiteralKindString, StringValue: "$PREFIX"}}}},
					{Name: "expand", Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindBool, BoolValue: true}},
				}}
				raw, err := cache.GetOrInitCall(ctx, "cleanup", srv, &dagql.CallRequest{ResultCall: frame}, func(context.Context) (dagql.AnyResult, error) {
					return dagql.NewObjectResultForCall(core.NewContainer(core.Platform{}), srv, frame)
				})
				require.NoError(t, err)
				parent = raw.(dagql.ObjectResult[*core.Container])
			}
			var svc dagql.ObjectResult[*core.Service]
			require.NoError(t, srv.Select(ctx, parent, &svc, dagql.Selector{Field: "asService"}))
			if exec {
				require.Equal(t, []string{"/go"}, svc.Self().Args)
			} else {
				require.Equal(t, []string{"echo", "default"}, svc.Self().Args)
			}
			require.True(t, svc.Self().Container.Self().Lazy.IsEvaluated())
		})
	}
}

func TestImportCacheOwnerDynamicInput(t *testing.T) {
	ctx, srv, cache, _, parent, _ := importLazyFixture(t, false, false)
	core.CacheSharingModes.Install(srv)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.CacheVolume]{}))
	// The real dynamic-input hook selects this frame; snapshot creation is not
	// part of resolving the inherited numeric owner under test.
	dagql.Fields[*core.Query]{dagql.NodeFunc("cacheVolume", func(ctx context.Context, _ dagql.ObjectResult[*core.Query], args cacheArgs) (dagql.Result[*core.CacheVolume], error) {
		return dagql.NewResultForCurrentCall(ctx, core.NewCache(args.Key, args.Namespace, dagql.Null[dagql.ObjectResult[*core.Directory]](), args.Sharing, args.Owner))
	})}.Install(srv)
	var volume dagql.Result[*core.CacheVolume]
	require.NoError(t, srv.Select(ctx, srv.Root(), &volume, dagql.Selector{Field: "cacheVolume", Args: []dagql.NamedInput{{Name: "key", Value: dagql.String("cache")}}}))
	id, err := volume.ID()
	require.NoError(t, err)
	args := containerWithMountedCacheArgs{Cache: dagql.NewID[*core.CacheVolume](id), InheritOwner: true}
	req := &dagql.CallRequest{ResultCall: &dagql.ResultCall{Args: []*dagql.ResultCallArg{{Name: "inheritOwner", Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindBool, BoolValue: true}}}}}
	require.NoError(t, (&containerSchema{}).withMountedCacheDynamicInputs(ctx, parent, args, req))
	input := req.Arg("cache")
	require.NotNil(t, input)
	resolved, err := cache.LoadResultByResultID(ctx, "cleanup", srv, input.Value.ResultRef.ResultID)
	require.NoError(t, err)
	require.Equal(t, "123:456", resolved.Unwrap().(*core.CacheVolume).Owner)
	require.True(t, parent.Self().Lazy.IsEvaluated())
}

func TestMountConstructorsOwnNoRefs(t *testing.T) {
	for _, file := range []bool{false, true} {
		for _, parentFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("file=%t/parentFirst=%t", file, parentFirst), func(t *testing.T) {
				ctx, srv, cache, server := resolverOutputFixture(t)
				manager := server.manager
				manager.inputs = map[string]*resolverOutputRef{}
				ref := func(id string) *resolverOutputRef {
					r := &resolverOutputRef{id: id, root: t.TempDir()}
					manager.inputs[id] = r
					return r
				}
				directory := func(id string) *core.Directory {
					d := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
					d.SetPath("/")
					d.SetSnapshot(ref(id))
					return d
				}
				p := core.NewContainer(core.Platform{OS: "linux", Architecture: "amd64"})
				p.Config.WorkingDir = "/work"
				p.FS.SetValue(directory("fs"))
				p.MetaSnapshot.SetValue(ref("meta"))
				for _, target := range []string{"/work/replaced", "/kept"} {
					a := new(core.LazyAccessor[*core.Directory, *core.Container])
					a.SetValue(directory(target))
					p.Mounts = p.Mounts.With(core.ContainerMount{Target: target, DirectorySource: a})
				}
				parent := resolverAttach(t, ctx, srv, cache, "parent", p)
				dir := resolverAttach(t, ctx, srv, cache, "source", directory("source"))
				f := &core.File{File: new(core.LazyAccessor[string, *core.File]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.File])}
				f.SetPath("/file")
				f.SetSnapshot(ref("source-file"))
				fileSource := resolverAttach(t, ctx, srv, cache, "fileSource", f)
				dirID, err := dir.ID()
				require.NoError(t, err)
				fileID, err := fileSource.ID()
				require.NoError(t, err)
				construct := func(conflict bool) (*core.Container, error) {
					owner := ""
					if conflict {
						owner = "123"
					}
					if file {
						return (&containerSchema{}).withMountedFile(ctx, parent, containerWithMountedFileArgs{Path: "replaced", Source: dagql.NewID[*core.File](fileID), Owner: owner, InheritOwner: conflict})
					}
					return (&containerSchema{}).withMountedDirectory(ctx, parent, containerWithMountedDirectoryArgs{Path: "replaced", Source: dagql.NewID[*core.Directory](dirID), Owner: owner, InheritOwner: conflict})
				}
				_, err = construct(true)
				require.ErrorContains(t, err, "cannot set both owner and inheritOwner")
				require.Empty(t, manager.outputs, "failure after metadata copy must own no refs")
				child, err := construct(false)
				require.NoError(t, err)
				require.Empty(t, manager.outputs, "construction must not clone parent refs")
				require.NotSame(t, p.FS, child.FS)
				require.NotSame(t, p.MetaSnapshot, child.MetaSnapshot)
				_, set := child.FS.Peek()
				require.False(t, set)
				_, set = child.MetaSnapshot.Peek()
				require.False(t, set)
				for _, mount := range child.Mounts {
					if mount.DirectorySource != nil {
						_, set := mount.DirectorySource.Peek()
						require.False(t, set)
					}
					if mount.FileSource != nil {
						_, set := mount.FileSource.Peek()
						require.False(t, set)
					}
				}
				op := child.Lazy.(core.LazyContainerParts)
				require.NoError(t, op.EvaluateContainerGroup(ctx, child, core.ContainerLazyGroupMetadata))
				require.Empty(t, manager.outputs, "metadata must not clone parent refs")
				require.NoError(t, child.Evaluate(ctx))
				require.Len(t, manager.outputs, 4, "FS, execMeta, kept mount and new mount each need one owned ref")
				for _, output := range manager.outputs {
					require.Zero(t, output.releases)
					require.NotEqual(t, "/work/replaced", output.id, "shadowed ref must never be cloned")
				}
				releaseParent := func() {
					require.NoError(t, cache.ReleaseSession(ctx, "cleanup"))
					require.NoError(t, cache.WaitSessionRelease(ctx, "cleanup"))
					_, err := cache.Prune(ctx, []dagql.CachePrunePolicy{{All: true}})
					require.NoError(t, err)
				}
				if parentFirst {
					releaseParent()
					for _, output := range manager.outputs {
						require.Zero(t, output.releases)
					}
					require.NoError(t, child.OnRelease(ctx))
				} else {
					require.NoError(t, child.OnRelease(ctx))
					for _, input := range manager.inputs {
						require.Zero(t, input.releases)
					}
					releaseParent()
				}
				for _, output := range manager.outputs {
					require.Equal(t, 1, output.releases)
				}
				for _, input := range manager.inputs {
					require.Equal(t, 1, input.releases)
				}
			})
		}
	}
}

func TestMountConstructionLeavesParentBytesPending(t *testing.T) {
	ctx, srv, cache, server := resolverOutputFixture(t)
	p := core.NewContainer(core.Platform{OS: "linux", Architecture: "amd64"})
	p.Config.WorkingDir = "/pending"
	p.Lazy = &containerImagePartsConcurrencyTestOp{LazyState: core.NewLazyState(), fsBodyHook: func() { t.Fatal("construction demanded parent bytes") }}
	parent := resolverAttach(t, ctx, srv, cache, "pendingParent", p)
	d := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory]), Lazy: &core.DirectoryScratchLazy{LazyState: core.NewLazyState()}}
	d.SetPath("/")
	dir := resolverAttach(t, ctx, srv, cache, "pendingSource", d)
	f := &core.File{File: new(core.LazyAccessor[string, *core.File]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.File]), Lazy: &core.FileBlobLazy{LazyState: core.NewLazyState(), Filename: "file", Contents: []byte("bytes")}}
	file := resolverAttach(t, ctx, srv, cache, "pendingFile", f)
	dirID, err := dir.ID()
	require.NoError(t, err)
	fileID, err := file.ID()
	require.NoError(t, err)
	dirMount, err := (&containerSchema{}).withMountedDirectory(ctx, parent, containerWithMountedDirectoryArgs{Path: "target", Source: dagql.NewID[*core.Directory](dirID)})
	require.NoError(t, err)
	fileMount, err := (&containerSchema{}).withMountedFile(ctx, parent, containerWithMountedFileArgs{Path: "target", Source: dagql.NewID[*core.File](fileID)})
	require.NoError(t, err)
	require.Equal(t, "/pending/target", dirMount.Lazy.(*core.ContainerWithMountedDirectoryLazy).Target)
	require.Equal(t, "/pending/target", fileMount.Lazy.(*core.ContainerWithMountedFileLazy).Target)
	require.True(t, p.HasPendingLazyComputation())
	require.True(t, d.HasPendingLazyComputation())
	require.True(t, f.HasPendingLazyComputation())
	require.Empty(t, server.manager.outputs)
}

// A container an image fills isn't the scratch container From may give a
// content digest: From keeps its environment (PREFIX here), so sharing a
// scratch parent's From would serve results without it. An import isn't
// scratch before its image fills it either.
func TestImageFilledContainerIsNoScratchForFrom(t *testing.T) {
	ctx, _, cache, server, parent, manifest := importLazyFixture(t, false, false)
	require.False(t, parent.Self().CanUseFromContentDigest(), "unevaluated import")
	store := testutil.NewStore(t)
	server.manager.SnapshotManager = store.Manager
	server.manager.imageRef, _ = store.Build(t, nil, "go/pkg/data", "saved")
	require.NoError(t, cache.Evaluate(ctx, parent))
	require.False(t, parent.Self().CanUseFromContentDigest(), "evaluated import")

	info, err := server.content.Info(ctx, manifest)
	require.NoError(t, err)
	ctr := core.NewContainer(server.platform)
	require.True(t, ctr.CanUseFromContentDigest())
	ctr, err = ctr.FromOCIStore(ctx, ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: manifest, Size: info.Size}, "")
	require.NoError(t, err)
	require.Contains(t, ctr.Config.Env, "PREFIX=/go")
	require.False(t, ctr.CanUseFromContentDigest(), "container filled from an OCI store")
}
