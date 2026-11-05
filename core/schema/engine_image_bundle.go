package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"runtime"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/opencontainers/go-digest"
	"golang.org/x/sync/semaphore"
)

// EngineImageBundleFile is the bundle's file in an engine image's directory of
// entries, beside its blobs (dagql.ImageBlobPath).
const EngineImageBundleFile = "bundle.json"

type engineImageBundleArgs struct {
	Results dagql.ArrayInput[dagql.AnyID]
}

// engineImageBundle exports results for an engine image to ship, as a
// directory holding the bundle and its blobs (dagql.ExportImageBundle): a
// directory's or file's snapshot, a container's image config and filesystem,
// and a Git module source's checkout, the tree it loads from as the module
// loader selects it, which no client can select itself. The engine image's
// build calls it on an engine of the same build, with the results the image
// pre-warms; nothing in an engine's runtime does.
func (s *hostSchema) engineImageBundle(ctx context.Context, parent dagql.ObjectResult[*core.Query], args engineImageBundleArgs) (inst dagql.ObjectResult[*core.Directory], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return inst, err
	}
	var outputs []dagql.SelectedValueOutput
	var results []dagql.AnyResult
	var containers []dagql.ObjectResult[*core.Container]
	add := func(res dagql.AnyResult, parts ...dagql.PartKey) {
		for _, part := range parts {
			outputs = append(outputs, dagql.SelectedValueOutput{Result: res, Address: dagql.PersistedPartAddress{Part: part}})
		}
		results = append(results, res)
	}
	for _, arg := range args.Results {
		id, err := arg.ID()
		if err != nil {
			return inst, err
		}
		res, err := srv.Load(ctx, id)
		if err != nil {
			return inst, err
		}
		switch res := res.(type) {
		case dagql.ObjectResult[*core.Directory], dagql.ObjectResult[*core.File]:
			add(res, "snapshot")
		case dagql.ObjectResult[*core.Container]:
			// A container loads from both its parts: the image config and the
			// filesystem, and an exec's container from its metadata too (below).
			add(res, core.ContainerPartMetadata, core.ContainerPartFS)
			containers = append(containers, res)
		case dagql.ObjectResult[*core.ModuleSource]:
			src := res.Self()
			if src.Kind != core.ModuleSourceKindGit || src.Git == nil || src.Git.UnfilteredContextDir.Self() == nil {
				return inst, fmt.Errorf("engine image bundle: module source %q is not a Git module source", src.AsString())
			}
			add(src.Git.UnfilteredContextDir, "snapshot")
		default:
			return inst, fmt.Errorf("engine image bundle: a %s is not a directory, file, container or Git module source", res.ObjectType().TypeName())
		}
	}
	if err := cache.Evaluate(ctx, results...); err != nil {
		return inst, fmt.Errorf("engine image bundle: evaluate the results: %w", err)
	}
	// An exec's container holds the exec's metadata, which evaluating the
	// container evaluates too: without it, a hit on the container runs the
	// exec again.
	for _, ctr := range containers {
		if meta := ctr.Self().MetaSnapshot; meta != nil {
			if snapshot, ok := meta.Peek(); ok && snapshot != nil {
				outputs = append(outputs, dagql.SelectedValueOutput{Result: ctr, Address: dagql.PersistedPartAddress{Part: core.ContainerPartExecMeta}})
			}
		}
	}
	query := parent.Self()
	ref, err := query.SnapshotManager().New(ctx, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("engine image bundle"))
	if err != nil {
		return inst, err
	}
	workers := runtime.NumCPU()
	cfg := config.RefConfig{
		Compression: compression.New(compression.Zstd),
		Builds:      config.LayerBuilds{Workers: workers, Gate: semaphore.NewWeighted(int64(workers))},
	}
	err = core.MountRef(ctx, ref, func(root string, _ *mount.Mount) error {
		dir, err := os.OpenRoot(root)
		if err != nil {
			return err
		}
		defer dir.Close()
		return cache.ExportImageBundle(ctx, outputs, cfg, imageBundleDir{root: dir})
	})
	if err != nil {
		return inst, err
	}
	snap, err := ref.Commit(ctx)
	if err != nil {
		return inst, err
	}
	out := &core.Directory{
		Platform: query.Platform(),
		Dir:      new(core.LazyAccessor[string, *core.Directory]),
		Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory]),
	}
	out.SetPath("/")
	out.SetSnapshot(snap)
	return dagql.NewObjectResultForCurrentCall(ctx, srv, out)
}

// imageBundleDir writes an engine image's bundle into a directory.
type imageBundleDir struct{ root *os.Root }

func (d imageBundleDir) WriteBundle(_ context.Context, bundle dagql.ValueBundle) error {
	raw, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	return d.root.WriteFile(EngineImageBundleFile, raw, 0o644)
}

func (d imageBundleDir) WriteBlob(ctx context.Context, dgst digest.Digest, r content.ReaderAt) error {
	name := dagql.ImageBlobPath(dgst)
	if err := d.root.MkdirAll(path.Dir(name), 0o755); err != nil {
		return err
	}
	f, err := d.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	verifier := dgst.Verifier()
	_, err = io.Copy(io.MultiWriter(f, verifier), io.NewSectionReader(r, 0, r.Size()))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if !verifier.Verified() {
		return fmt.Errorf("blob %s: content does not match its digest", dgst)
	}
	return ctx.Err()
}
