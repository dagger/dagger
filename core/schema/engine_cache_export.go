package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"strings"

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

type cacheExportArgs struct {
	Results           dagql.ArrayInput[dagql.AnyID]
	ForcedCompression dagql.Optional[core.ImageLayerCompression]
}

// cacheExport exports the cache entries of results as a bundle directory
// (dagql.Cache.ExportBundle): each result with what it was built from, as
// records, and the result's own data, whatever its type, or, for a result
// with no data of its own, the data of the results its value holds.
func (s *engineSchema) cacheExport(ctx context.Context, _ dagql.ObjectResult[*core.EngineCache], args cacheExportArgs) (inst dagql.ObjectResult[*core.Directory], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return inst, err
	}
	results := make([]dagql.AnyResult, 0, len(args.Results))
	for _, arg := range args.Results {
		id, err := arg.ID()
		if err != nil {
			return inst, err
		}
		res, err := srv.Load(ctx, id)
		if err != nil {
			return inst, err
		}
		results = append(results, res)
	}
	workers := runtime.NumCPU()
	cfg := config.RefConfig{
		Compression: compression.New(compression.Zstd),
		Builds:      config.LayerBuilds{Workers: workers, Gate: semaphore.NewWeighted(int64(workers))},
	}
	if args.ForcedCompression.Valid {
		ctype, err := compression.Parse(strings.ToLower(string(args.ForcedCompression.Value)))
		if err != nil {
			return inst, err
		}
		cfg.Compression = compression.New(ctype).SetForce(true)
	}
	ref, err := query.SnapshotManager().New(ctx, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("cache export"))
	if err != nil {
		return inst, err
	}
	err = core.MountRef(ctx, ref, func(root string, _ *mount.Mount) error {
		dir, err := os.OpenRoot(root)
		if err != nil {
			return err
		}
		defer dir.Close()
		return cache.ExportBundle(ctx, srv, results, cfg, bundleDir{root: dir})
	})
	if err != nil {
		// Commit consumes the ref; an export that failed never commits it.
		return inst, errors.Join(err, ref.Release(context.WithoutCancel(ctx)))
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

// bundleDir writes a bundle directory.
type bundleDir struct{ root *os.Root }

func (d bundleDir) WriteBundle(_ context.Context, bundle dagql.ValueBundle) error {
	raw, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	return d.root.WriteFile(dagql.BundleFile, raw, 0o644)
}

func (d bundleDir) WriteBlob(ctx context.Context, dgst digest.Digest, r content.ReaderAt) error {
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
