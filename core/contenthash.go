package core

import (
	"context"
	"fmt"
	"strings"

	bkcontenthash "github.com/dagger/dagger/internal/buildkit/cache/contenthash"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	bkgw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	bkworker "github.com/dagger/dagger/internal/buildkit/worker"
	"github.com/opencontainers/go-digest"
	"resenje.org/singleflight"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/contenthash"
)

var checksumG singleflight.Group[string, digest.Digest]

// MakeDirectoryContentHashed returns an updated instance of the given Directory that
// has it's dagql ID digest set to a content hash of the directory. This allows all
// directory instances with the same content to be deduplicated in dagql's cache.
func MakeDirectoryContentHashed(
	ctx context.Context,
	bk *buildkit.Client,
	dirInst dagql.ObjectResult[*Directory],
) (retInst dagql.ObjectResult[*Directory], err error) {
	dgst, err := GetContentHashFromDirectory(ctx, bk, dirInst)
	if err != nil {
		return retInst, err
	}

	return dirInst.WithObjectDigest(dgst), nil
}

func GetContentHashFromDirectory(
	ctx context.Context,
	bk *buildkit.Client,
	dirInst dagql.ObjectResult[*Directory],
) (digest.Digest, error) {
	if dirInst.Self() == nil {
		return "", fmt.Errorf("directory instance is nil")
	}

	st, err := dirInst.Self().State()
	if err != nil {
		return "", fmt.Errorf("failed to get state: %w", err)
	}
	def, err := st.Marshal(ctx, llb.Platform(dirInst.Self().Platform.Spec()))
	if err != nil {
		return "", fmt.Errorf("failed to marshal state: %w", err)
	}
	dirPath := dirInst.Self().Dir
	if !strings.HasSuffix(dirPath, "/") {
		// omit directory name from the hash
		dirPath += "/"
	}
	dgst, err := GetContentHashFromDef(ctx, bk, def.ToPB(), dirPath)
	if err != nil {
		return "", fmt.Errorf("failed to get content hash: %w", err)
	}

	return dgst, nil
}

func GetContentHashFromFile(
	ctx context.Context,
	bk *buildkit.Client,
	fileInst dagql.ObjectResult[*File],
) (digest.Digest, error) {
	if fileInst.Self() == nil {
		return "", fmt.Errorf("file instance is nil")
	}

	st, err := fileInst.Self().State()
	if err != nil {
		return "", fmt.Errorf("failed to get state: %w", err)
	}
	def, err := st.Marshal(ctx, llb.Platform(fileInst.Self().Platform.Spec()))
	if err != nil {
		return "", fmt.Errorf("failed to marshal state: %w", err)
	}
	dgst, err := GetContentHashFromDef(ctx, bk, def.ToPB(), fileInst.Self().File)
	if err != nil {
		return "", fmt.Errorf("failed to get content hash: %w", err)
	}

	return dgst, nil
}

func GetContentHashFromDef(
	ctx context.Context,
	bk *buildkit.Client,
	def *pb.Definition,
	subdir string,
) (digest.Digest, error) {
	if subdir == "" {
		subdir = "/"
	}

	res, err := bk.Solve(ctx, bkgw.SolveRequest{
		Definition: def,
		Evaluate:   true,
	})
	if err != nil {
		return "", err
	}
	resultProxy, err := res.SingleRef()
	if err != nil {
		return "", fmt.Errorf("failed to get single ref: %w", err)
	}
	cachedRes, err := resultProxy.Result(ctx)
	if err != nil {
		return "", buildkit.WrapError(ctx, err, bk)
	}
	workerRef, ok := cachedRes.Sys().(*bkworker.WorkerRef)
	if !ok {
		return "", fmt.Errorf("invalid ref: %T", cachedRes.Sys())
	}
	ref := workerRef.ImmutableRef

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
		}, nil)
		if err != nil {
			return "", fmt.Errorf("failed to checksum ref: %w", err)
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
