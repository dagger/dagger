package core

import (
	"context"
	"fmt"
	"strings"

	bkcontenthash "github.com/dagger/dagger/engine/contenthash"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	"resenje.org/singleflight"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/contenthash"
	telemetry "github.com/dagger/otel-go"
)

var checksumG singleflight.Group[string, digest.Digest]

// MakeDirectoryContentHashed returns an updated instance of the given Directory that
// has it's dagql ID digest set to a content hash of the directory. This allows all
// directory instances with the same content to be deduplicated in dagql's cache.
func MakeDirectoryContentHashed(
	ctx context.Context,
	dirInst dagql.ObjectResult[*Directory],
) (retInst dagql.ObjectResult[*Directory], err error) {
	dgst, err := GetContentHashFromDirectory(ctx, dirInst)
	if err != nil {
		return retInst, err
	}
	dirPath := ""
	if dirInst.Self() != nil {
		dirPath, _ = dirInst.Self().Dir.Peek()
	}
	dagql.TraceEGraphDebug(ctx, "directory_content_hash_computed", "phase", "runtime", "path", dirPath, "content_digest", dgst)

	if _, err := dirInst.ID(); err == nil {
		frame, frameErr := dirInst.ResultCall()
		oldContentDigest := ""
		if frameErr == nil && frame != nil {
			oldContentDigest = frame.ContentDigest().String()
		}
		dagql.TraceEGraphDebug(ctx, "directory_content_hash_teach_attempt", "phase", "runtime", "path", dirPath, "old_content_digest", oldContentDigest, "new_content_digest", dgst)
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return retInst, err
		}
		if err := cache.TeachContentDigest(ctx, dirInst, dgst); err != nil {
			return retInst, fmt.Errorf("teach directory content digest: %w", err)
		}
		dagql.TraceEGraphDebug(ctx, "directory_content_hash_taught", "phase", "runtime", "path", dirPath, "content_digest", dgst)
		return dirInst, nil
	}

	dagql.TraceEGraphDebug(ctx, "directory_content_hash_detached_result", "phase", "runtime", "path", dirPath, "content_digest", dgst)
	return dirInst.WithContentDigest(ctx, dgst)
}

func GetContentHashFromDirectory(
	ctx context.Context,
	dirInst dagql.ObjectResult[*Directory],
) (digest.Digest, error) {
	if dirInst.Self() == nil {
		return "", fmt.Errorf("directory instance is nil")
	}
	if _, err := dirInst.ID(); err == nil {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return "", err
		}
		if err := cache.Evaluate(ctx, dirInst); err != nil {
			return "", err
		}
	}

	snapshot, err := dirInst.Self().Snapshot.GetOrEval(ctx, dirInst.Result)
	if err != nil {
		return "", fmt.Errorf("failed to get directory snapshot: %w", err)
	}
	if snapshot == nil {
		return "", fmt.Errorf("failed to get directory snapshot: nil")
	}

	dirPath, err := dirInst.Self().Dir.GetOrEval(ctx, dirInst.Result)
	if err != nil {
		return "", fmt.Errorf("failed to get directory path: %w", err)
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

func GetContentHashFromFile(
	ctx context.Context,
	fileInst dagql.ObjectResult[*File],
) (digest.Digest, error) {
	if fileInst.Self() == nil {
		return "", fmt.Errorf("file instance is nil")
	}
	if _, err := fileInst.ID(); err == nil {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return "", err
		}
		if err := cache.Evaluate(ctx, fileInst); err != nil {
			return "", err
		}
	}

	snapshot, err := fileInst.Self().Snapshot.GetOrEval(ctx, fileInst.Result)
	if err != nil {
		return "", fmt.Errorf("failed to get file snapshot: %w", err)
	}
	if snapshot == nil {
		return "", fmt.Errorf("failed to get file snapshot: nil")
	}

	filePath, err := fileInst.Self().File.GetOrEval(ctx, fileInst.Result)
	if err != nil {
		return "", fmt.Errorf("failed to get file path: %w", err)
	}
	dgst, err := getContentHashFromRef(ctx, snapshot, filePath)
	if err != nil {
		return "", fmt.Errorf("failed to get content hash: %w", err)
	}

	return dgst, nil
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
		md := contenthash.CacheRefMetadata{RefMetadata: mdRef}

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
