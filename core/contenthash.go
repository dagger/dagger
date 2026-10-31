package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"
	bkcontenthash "github.com/dagger/dagger/engine/contenthash"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/dagger/dagger/util/layercopy"
	"github.com/opencontainers/go-digest"
	"resenje.org/singleflight"

	"github.com/dagger/dagger/dagql"
	telemetry "github.com/dagger/otel-go"
)

var checksumG singleflight.Group[string, digest.Digest]

func GetContentHashFromDirectory(
	ctx context.Context,
	snapshot bkcache.ImmutableRef,
	dirPath string,
) (digest.Digest, error) {
	if snapshot == nil {
		return "", fmt.Errorf("failed to get directory snapshot: nil")
	}

	if !strings.HasSuffix(dirPath, "/") {
		// omit directory name from the hash
		dirPath += "/"
	}
	dgst, err := getContentHashFromRef(ctx, snapshot, dirPath)
	if err != nil {
		return "", fmt.Errorf("failed to get content hash: %w", err)
	}
	dagql.TraceEGraphDebug(ctx, "directory_content_hash_from_ref", "phase", "runtime", "path", dirPath, "snapshot_ref_id", snapshot.SnapshotID(), "content_digest", dgst)

	return dgst, nil
}

// GetDirectoryRootHeaderHash returns the hash of the header of the directory
// at dirPath in snapshot: its mode, owner and extended attributes, as content
// hashes record them for every other entry. GetContentHashFromDirectory leaves
// the root out, but a copy of the directory's contents to another path gives
// the destination directory this header.
func GetDirectoryRootHeaderHash(
	ctx context.Context,
	snapshot bkcache.ImmutableRef,
	dirPath string,
) (digest.Digest, error) {
	if snapshot == nil {
		return "", fmt.Errorf("failed to get directory snapshot: nil")
	}
	var dgst digest.Digest
	err := MountRef(ctx, snapshot, func(root string, _ *mount.Mount) error {
		p, err := containerdfs.RootPath(root, dirPath)
		if err != nil {
			return err
		}
		fi, err := os.Stat(p)
		if err != nil {
			return err
		}
		h, err := bkcontenthash.NewFileHash(p, fi)
		if err != nil {
			return err
		}
		dgst = digest.NewDigest(digest.SHA256, h)
		return nil
	}, mountRefAsReadOnly)
	if err != nil {
		return "", fmt.Errorf("failed to hash directory header: %w", err)
	}
	return dgst, nil
}

func GetContentHashFromFile(
	ctx context.Context,
	snapshot bkcache.ImmutableRef,
	filePath string,
) (digest.Digest, error) {
	if snapshot == nil {
		return "", fmt.Errorf("failed to get file snapshot: nil")
	}
	dgst, err := getContentHashFromRef(ctx, snapshot, filePath)
	if err != nil {
		return "", fmt.Errorf("failed to get content hash: %w", err)
	}

	return dgst, nil
}

// GetContentHashFromCopySources returns the content hash of what an unfiltered
// Dockerfile COPY of srcPath out of srcDir in snapshot copies: each path that
// srcPath resolves to, resolving wildcards as the copy does, hashed whole
// together with that path. A path that doesn't exist is hashed as missing,
// for the copy to report when it runs.
func GetContentHashFromCopySources(
	ctx context.Context,
	snapshot bkcache.ImmutableRef,
	srcDir string,
	srcPath string,
) (digest.Digest, error) {
	if snapshot == nil {
		return "", fmt.Errorf("failed to get copy source snapshot: nil")
	}
	paths := []string{srcDir}
	missing := map[string]bool{}
	err := MountRef(ctx, snapshot, func(root string, _ *mount.Mount) error {
		if srcPath != "" {
			srcPathWithDir := srcPath
			if srcDir != "" && srcDir != "/" {
				srcPathWithDir = srcDir + "/" + srcPathWithDir
			}
			var err error
			paths, err = layercopy.ResolveWildcards(root, srcPathWithDir, true)
			if err != nil {
				return err
			}
		}
		for _, p := range paths {
			resolved, err := containerdfs.RootPath(root, p)
			if err != nil {
				return err
			}
			if _, err := os.Stat(resolved); errors.Is(err, os.ErrNotExist) {
				missing[p] = true
			}
		}
		return nil
	}, mountRefAsReadOnly)
	if err != nil {
		return "", fmt.Errorf("failed to resolve copy source paths: %w", err)
	}
	parts := make([]string, 0, 2*len(paths))
	for _, p := range paths {
		if missing[p] {
			parts = append(parts, p, "missing")
			continue
		}
		dgst, err := getContentHashFromRef(ctx, snapshot, path.Join("/", p))
		if err != nil {
			return "", fmt.Errorf("failed to get content hash of %q: %w", p, err)
		}
		parts = append(parts, p, string(dgst))
	}
	return hashutil.HashStrings(parts...), nil
}

func getContentHashFromRef(ctx context.Context, ref bkcache.ImmutableRef, subdir string) (digest.Digest, error) {
	if ref == nil {
		return "", fmt.Errorf("cannot get content hash from nil ref")
	}
	if subdir == "" {
		subdir = "/"
	}
	key := ref.SnapshotID() + "/" + strings.TrimPrefix(subdir, "/")
	dgst, _, err := checksumG.Do(ctx, key, func(ctx context.Context) (_ digest.Digest, rerr error) {
		mdRef, ok := any(ref).(bkcache.RefMetadata)
		if !ok {
			return "", fmt.Errorf("content hash metadata: unexpected ref type %T", ref)
		}
		md := bkcontenthash.CacheRefMetadata{RefMetadata: mdRef}

		if subdir == "/" {
			// content hashes for the root of dirs are saved in the metadata of the ref (both below
			// and in the local source implementation); check if we have it already
			dgst, ok := md.GetContentHashKey()
			if ok {
				bklog.G(ctx).Debugf("GetContentHashKey reusing snapshot %s with digest %s", ref.SnapshotID(), dgst)
				return dgst, nil
			}
		}

		ctx, span := Tracer(ctx).Start(ctx,
			fmt.Sprintf("checksum def: %s", key),
			telemetry.Internal(),
		)
		defer telemetry.EndWithCause(span, &rerr)

		dgst, err := bkcontenthash.Checksum(ctx, ref, subdir, bkcontenthash.ChecksumOpts{
			FollowLinks: true,
		})
		if err != nil {
			return "", fmt.Errorf("failed to checksum ref at subdir %s: %w", subdir, err)
		}

		if subdir == "/" {
			// Save the content hash in the metadata of the ref if it's for the root of the dir
			// We could probably save it for other subdirs with some shenanigans on the stored metadata,
			// but bkcontenthash.Checksum does caching of already calculated path hashes, so we can avoid
			// that for now without sacrificing much performance.
			if err := md.SetContentHashKey(dgst); err != nil {
				return "", fmt.Errorf("failed to set content hash key: %w", err)
			}
		}

		bklog.G(ctx).Debugf("GetContentHashKey setting snapshot %s with digest %s", ref.SnapshotID(), dgst)

		return dgst, nil
	})

	return dgst, err
}
