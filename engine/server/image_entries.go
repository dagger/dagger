package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
)

// mergeImageEntries merges the entries the engine image ships, from dir: the
// bundle the image's build exported (schema.EngineImageBundleFile) and the
// blobs of its parts, which the cache reads from dir when a part is first
// used. An image without a bundle ships no entries. An entry keeps the expiry
// its cache gave it when the image was built, such as the lifetime of the
// module function that returned it, and the image outlives that: a root that
// has expired since is left out, with whatever only it needed, and the start
// goes on. The start fails when the rest of the bundle doesn't merge whole, or
// a blob it names isn't in the image, so an engine that starts from the image
// has every unexpired entry before any request.
func (srv *Server) mergeImageEntries(ctx context.Context, dir string) error {
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
		return fmt.Errorf("merge the engine image's entries: %w", err)
	}
	if len(reply.Stale) > 0 {
		return fmt.Errorf("merge the engine image's entries: %d held rows are stale", len(reply.Stale))
	}
	expired := 0
	for _, r := range reply.Roots {
		if r.Expired {
			expired++
			continue
		}
		if !r.Retained {
			return fmt.Errorf("merge the engine image's entries: root %d did not merge (retained false)", r.Ordinal)
		}
	}
	slog.Info("merged the engine image's entries",
		"roots", len(reply.Roots), "expiredRoots", expired, "values", len(reply.Values), "blobs", blobs, "blobBytes", size,
		"duration", time.Since(start))
	return nil
}

// checkImageBlobs checks that the image holds every blob the bundle's offers
// name, at its offered size, and returns how many there are and their total
// size.
func checkImageBlobs(root *os.Root, bundle dagql.ValueBundle) (int, int64, error) {
	sizes := map[digest.Digest]int64{}
	for _, layer := range bundle.Layers {
		sizes[layer.Descriptor.Digest] = layer.Descriptor.Size
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
