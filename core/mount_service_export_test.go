package core

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A container that mounts a File or a Directory bound to a service exports,
// as do its descendant, whose mount part it delegates, the File or Directory
// read back from the mount, and a descendant of the container once built,
// which clones the mount: each names the source's service, which the exported
// record must have as a direct dependency. The export imports into another
// cache, which exports it again. Mounting an unbound source is the control.
// Each result is built as the schema's withMountedFile, withMountedDirectory,
// withWorkdir, file and directory build it.
func TestMountedServiceBoundSourceExports(t *testing.T) {
	for _, family := range []string{"File", "Directory"} {
		for _, bound := range []bool{true, false} {
			t.Run(family+map[bool]string{true: "/bound", false: "/unbound"}[bound], func(t *testing.T) {
				store := testutil.NewStore(t)
				ctx, cache, srv := transferCache(t, store, "", "a")
				srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Service]{}))
				bctx, b, bsrv := transferCache(t, testutil.NewStore(t), "", "b")
				bsrv.InstallObject(dagql.NewClass(bsrv, dagql.ClassOpts[*Service]{}))
				var services ServiceBindings
				if bound {
					service := attachTransferObject(t, ctx, cache, srv, "a", "boundService", &Service{CustomHostname: "fixture"})
					services = ServiceBindings{{Service: service, Hostname: "svc"}}
				}
				platform := Platform{OS: "linux", Architecture: "amd64"}
				ref, _ := store.Build(t, nil, "schema.json", "{}")
				parent := attachTransferObject(t, ctx, cache, srv, "a", "mountParent", NewContainer(platform))

				const target = "/schema.json"
				mounting := &Container{FS: new(LazyAccessor[*Directory, *Container]), MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container])}
				CopyContainerMetadata(mounting, parent.Self())
				if family == "File" {
					file := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Platform: platform, Services: services}
					file.SetPath(target)
					file.SetSnapshot(ref)
					source := attachTransferObject(t, ctx, cache, srv, "a", "boundFile", file)
					mounting.Lazy = &ContainerWithMountedFileLazy{LazyState: NewLazyState(), Parent: parent, Target: target, Source: source}
					mounting.Mounts = mounting.Mounts.With(ContainerMount{Target: target, FileSource: new(LazyAccessor[*File, *Container])})
				} else {
					dir := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Platform: platform, Services: services}
					dir.SetPath("/")
					dir.SetSnapshot(ref)
					source := attachTransferObject(t, ctx, cache, srv, "a", "boundDirectory", dir)
					mounting.Lazy = &ContainerWithMountedDirectoryLazy{LazyState: NewLazyState(), Parent: parent, Target: target, Source: source}
					mounting.Mounts = mounting.Mounts.With(ContainerMount{Target: target, DirectorySource: new(LazyAccessor[*Directory, *Container])})
				}
				mounted := attachDelegationChild(t, ctx, cache, srv, "a", "withMounted"+family, parent, mounting)

				workdir := &Container{FS: new(LazyAccessor[*Directory, *Container]), MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container])}
				CopyContainerMetadata(workdir, mounted.Self())
				workdir.Lazy = &ContainerWithWorkdirLazy{LazyState: NewLazyState(), Parent: mounted, Path: "/work"}
				descendant := attachDelegationChild(t, ctx, cache, srv, "a", "withWorkdir", mounted, workdir)

				var derived dagql.AnyResult
				if family == "File" {
					file := &File{Platform: mounted.Self().Platform, Services: slices.Clone(mounted.Self().Services), Lazy: &ContainerFileLazy{LazyState: NewLazyState(), Parent: mounted, Path: target},
						File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])}
					file.SetPath(target)
					derived = attachTransferObject(t, ctx, cache, srv, "a", "file", file)
				} else {
					dir := &Directory{Platform: mounted.Self().Platform, Services: slices.Clone(mounted.Self().Services), Lazy: &ContainerDirectoryLazy{LazyState: NewLazyState(), Parent: mounted, Path: target},
						Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory])}
					dir.SetPath(target)
					derived = attachTransferObject(t, ctx, cache, srv, "a", "directory", dir)
				}

				roundTrip := func(name string, result dagql.AnyResult) {
					t.Helper()
					refs := config.RefConfig{Compression: compression.New(compression.Uncompressed)}
					err := cache.WithExportedValues(ctx, dagql.ValueSelection{Roots: []dagql.AnyResult{result}}, refs, func(_ context.Context, exported *dagql.ExportedValues) error {
						reply, err := b.MergeValues(bctx, dagql.CloudCacheID, exported.Bundle)
						if err != nil {
							return fmt.Errorf("import: %w", err)
						}
						imported := reply.Imported()
						if len(imported) != 1 {
							return fmt.Errorf("import: %d roots", len(imported))
						}
						loaded, err := b.LoadResultByResultID(bctx, "b", bsrv, imported[0].ResultID)
						if err != nil {
							return fmt.Errorf("load the import: %w", err)
						}
						if err := b.WithExportedValues(bctx, dagql.ValueSelection{Roots: []dagql.AnyResult{loaded}}, refs, func(context.Context, *dagql.ExportedValues) error { return nil }); err != nil {
							return fmt.Errorf("export the import: %w", err)
						}
						return nil
					})
					assert.NoError(t, err, "round trip of the %s", name)
				}
				for _, c := range []struct {
					name     string
					result   dagql.AnyResult
					evaluate func() error
				}{
					{"mounting container", mounted, func() error { return cache.EvaluateParts(ctx, mounted, ContainerPartMount(target)) }},
					{"descendant container", descendant, func() error { return cache.EvaluateParts(ctx, descendant, ContainerPartMount(target)) }},
					{"mounted " + family, derived, func() error { return cache.Evaluate(ctx, derived) }},
				} {
					require.NoError(t, c.evaluate(), c.name)
					roundTrip(c.name, c.result)
				}

				// Over a built parent, withWorkdir clones the parent's parts
				// and keeps no operation, so the mount's value is set before
				// the descendant is published.
				require.NoError(t, cache.Evaluate(ctx, mounted))
				require.False(t, dagql.HasPendingLazyEvaluation(mounted))
				fs, err := CloneContainerDirectoryAccessor(ctx, mounted.Self().FS)
				require.NoError(t, err)
				mounts, err := CloneContainerMounts(ctx, mounted.Self().Mounts)
				require.NoError(t, err)
				meta, err := CloneContainerMetaSnapshot(ctx, mounted.Self().MetaSnapshot)
				require.NoError(t, err)
				built := &Container{FS: fs, MetaSnapshot: meta, Config: CloneContainerImageConfig(mounted.Self().Config), Mounts: mounts, Platform: platform, Services: slices.Clone(mounted.Self().Services)}
				built.Config.WorkingDir = "/work"
				roundTrip("descendant of the built container", attachDelegationChild(t, ctx, cache, srv, "a", "withWorkdirOverBuilt", mounted, built))
			})
		}
	}
}
