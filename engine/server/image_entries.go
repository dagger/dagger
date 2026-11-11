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
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
)

// mergeImageEntries merges the entries the engine image ships, from dir: the
// bundle the image's build exported (schema.EngineImageBundleFile) and the
// blobs of its parts, which the cache reads from dir when a part is first
// used. An image without a bundle ships no entries. The start fails when the
// bundle doesn't merge whole, or a blob it names isn't in the image, so an
// engine that starts from the image has every entry before any request.
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
	for _, r := range reply.Roots {
		if r.Expired || !r.Retained {
			return fmt.Errorf("merge the engine image's entries: root %d did not merge (expired %t, retained %t)", r.Ordinal, r.Expired, r.Retained)
		}
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

// builtinSDKDigestEnvNames name the variables whose content digests name the
// built-in SDKs' files, directories the image's entries hold.
var builtinSDKDigestEnvNames = []string{
	distconsts.GoSDKDigestEnvName,
	distconsts.PythonSDKDigestEnvName,
	distconsts.TypescriptSDKDigestEnvName,
}

// checkImageDirectories fails unless the image's entries hold a directory
// with each of these content digests: the engine finds the built-in SDKs'
// files only there (Query.__bundledDirectory), so an image that names files
// it doesn't ship fails its start rather than its first module load. A prune
// never deletes them: it never deletes what the image ships. An empty digest
// names nothing.
func (srv *Server) checkImageDirectories(digests []string) error {
	for _, dgst := range digests {
		if dgst == "" {
			continue
		}
		if _, err := srv.engineCache.ImageEntryByContent(digest.Digest(dgst), "Directory"); err != nil {
			return fmt.Errorf("engine image entries: %w", err)
		}
	}
	return nil
}
