package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
)

// mergeImageEntries merges the entries the engine image ships, from dir, so
// that the first calls after the start find them. The entries only spare the
// engine work it can do itself, so the start never depends on them: whatever
// keeps them from merging is logged, and the engine starts without them, or
// with the part that merged. The built-in SDKs' files are entries too, the
// directories with the content digests sdkDigests names, and no module of
// those SDKs loads without them: when they aren't there, the cache records
// why, so that a load of one of those SDKs says so.
func (srv *Server) mergeImageEntries(ctx context.Context, dir string, sdkDigests []digest.Digest) {
	err := srv.mergeImageBundle(ctx, dir)
	if err != nil {
		slog.Warn("starting without some or all of the engine image's entries", "error", err)
	}
	missing := srv.missingImageDirectories(sdkDigests)
	if len(missing) == 0 {
		return
	}
	cause := fmt.Sprintf("the engine image's entries at %s hold no such directory", dir)
	if err != nil {
		cause = err.Error()
	}
	srv.engineCache.SetImageEntriesCause(cause)
	slog.Warn("starting without some of the built-in SDKs' files", "missing", missing, "cause", cause)
}

// mergeImageBundle merges the entries the engine image ships, from dir: the
// bundle the image's build exported (schema.EngineImageBundleFile) and the
// blobs of its parts, which the cache reads from dir when a part is first
// used. An image without a bundle ships no entries. The merge drops the
// expiries the cache that built the image gave its entries, so none is left
// out however old the image. It merges nothing when the bundle can't be
// read or a blob it names isn't in the image, so no entry reads a part the
// image doesn't hold, and a merge that fails before its commit changes
// nothing. It returns what kept the bundle, or part of it, from merging.
func (srv *Server) mergeImageBundle(ctx context.Context, dir string) error {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("engine image entries: %w", err)
	}
	defer root.Close()
	raw, err := root.ReadFile(schema.EngineImageBundleFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("engine image entries: %w", err)
	}
	start := time.Now()
	var bundle dagql.ValueBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return fmt.Errorf("engine image entries: %w", err)
	}
	blobs, size, err := checkImageBlobs(root, bundle)
	if err != nil {
		return fmt.Errorf("engine image entries: %w", err)
	}
	if err := srv.engineCache.SetImageContent(dir); err != nil {
		return err
	}
	reply, err := srv.engineCache.MergeValues(ctx, dagql.ImageCacheID, bundle)
	if err != nil {
		return fmt.Errorf("merge the engine image's entries (committed %t): %w", reply.Committed, err)
	}
	if len(reply.Stale) > 0 {
		return fmt.Errorf("merge the engine image's entries: %d held rows are stale, so none merged", len(reply.Stale))
	}
	unretained := 0
	for _, r := range reply.Roots {
		if !r.Retained {
			unretained++
		}
	}
	if unretained > 0 {
		return fmt.Errorf("merge the engine image's entries: %d of %d roots merged without the image's retention", unretained, len(reply.Roots))
	}
	slog.Info("merged the engine image's entries",
		"roots", len(reply.Roots), "values", len(reply.Values), "blobs", blobs, "blobBytes", size,
		"duration", time.Since(start))
	return nil
}

// checkImageBlobs checks that the image holds every blob the bundle's offers
// name, at its offered size, and returns how many there are and their total
// size.
func checkImageBlobs(root *os.Root, bundle dagql.ValueBundle) (int, int64, error) {
	sizes := map[digest.Digest]int64{}
	for _, listed := range bundle.Layers {
		sizes[listed.Layer.Descriptor.Digest] = listed.Layer.Descriptor.Size
	}
	for _, output := range bundle.Outputs {
		if output.Chain == nil {
			continue
		}
		for _, layer := range output.Chain.Layers {
			sizes[layer.Descriptor.Digest] = layer.Descriptor.Size
		}
	}
	var total int64
	for d, size := range sizes {
		if err := d.Validate(); err != nil {
			return 0, 0, err
		}
		info, err := root.Stat(dagql.ImageBlobPath(d))
		if err != nil {
			return 0, 0, fmt.Errorf("blob %s: %w", d, err)
		}
		if info.Size() != size {
			return 0, 0, fmt.Errorf("blob %s: the image holds %d bytes, the bundle names %d", d, info.Size(), size)
		}
		total += size
	}
	return len(sizes), total, nil
}

// builtinSDKDigestEnvNames name the variables whose content digests name the
// built-in SDKs' files, directories the image's entries hold.
var builtinSDKDigestEnvNames = []string{
	distconsts.GoSDKDigestEnvName,
	distconsts.PythonSDKDigestEnvName,
	distconsts.TypescriptSDKDigestEnvName,
}

// missingImageDirectories returns the content digests of dirs with no
// directory among the image's entries in the cache. The engine finds the
// built-in SDKs' files only there (Query.__bundledDirectory). A prune never
// deletes them: it never deletes what the image ships. An empty digest names
// nothing.
func (srv *Server) missingImageDirectories(dirs []digest.Digest) []digest.Digest {
	var missing []digest.Digest
	for _, dgst := range dirs {
		if dgst == "" {
			continue
		}
		if _, err := srv.engineCache.ImageEntryByContent(dgst, (&core.Directory{}).Type().Name()); err != nil {
			missing = append(missing, dgst)
		}
	}
	return missing
}
