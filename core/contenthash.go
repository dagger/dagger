package core

import (
	"context"
	"fmt"
	"strings"

	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkcontenthash "github.com/dagger/dagger/internal/buildkit/cache/contenthash"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	"resenje.org/singleflight"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/contenthash"
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

	return dirInst.WithContentDigest(dgst), nil
}

func GetContentHashFromDirectory(
	ctx context.Context,
	dirInst dagql.ObjectResult[*Directory],
) (digest.Digest, error) {
	if dirInst.Self() == nil {
		return "", fmt.Errorf("directory instance is nil")
	}

	snapshot, err := dirInst.Self().getSnapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get directory snapshot: %w", err)
	}
	if snapshot == nil {
		return "", fmt.Errorf("failed to get directory snapshot: nil")
	}

	dirPath := dirInst.Self().Dir
	if !strings.HasSuffix(dirPath, "/") {
		// omit directory name from the hash
		dirPath += "/"
	}
	dgst, err := getContentHashFromRef(ctx, snapshot, dirPath)
	if err != nil {
		return "", fmt.Errorf("failed to get content hash: %w", err)
	}

	return dgst, nil
}

func GetContentHashFromFile(
	ctx context.Context,
	fileInst dagql.ObjectResult[*File],
) (digest.Digest, error) {
	if fileInst.Self() == nil {
		return "", fmt.Errorf("file instance is nil")
	}

	snapshot, err := fileInst.Self().getSnapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get file snapshot: %w", err)
	}
	if snapshot == nil {
		return "", fmt.Errorf("failed to get file snapshot: nil")
	}

	dgst, err := getContentHashFromRef(ctx, snapshot, fileInst.Self().File)
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
	key := ref.ID() + "/" + strings.TrimPrefix(subdir, "/")
	dgst, _, err := checksumG.Do(ctx, key, func(ctx context.Context) (_ digest.Digest, rerr error) {
		if err := ref.Finalize(ctx); err != nil {
			return "", fmt.Errorf("failed to finalize ref: %w", err)
		}

		md := contenthash.CacheRefMetadata{RefMetadata: ref}

		if subdir == "/" {
			// content hashes for the root of dirs are saved in the metadata of the ref (both below
			// and in the local source implementation); check if we have it already
			dgst, ok := md.GetContentHashKey()
			if ok {
				bklog.G(ctx).Debugf("GetContentHashKey reusing ref %s with digest %s", ref.ID(), dgst)
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
		}, requiresBuildkitSessionGroup(ctx))
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

		bklog.G(ctx).Debugf("GetContentHashKey setting ref %s with digest %s", ref.ID(), dgst)

		return dgst, nil
	})

	return dgst, err
}
