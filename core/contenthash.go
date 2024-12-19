package core

import (
	"context"
	"fmt"

	bkcontenthash "github.com/moby/buildkit/cache/contenthash"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	bkworker "github.com/moby/buildkit/worker"
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
	dirInst dagql.Instance[*Directory],
) (retInst dagql.Instance[*Directory], err error) {
	if dirInst.Self == nil {
		return retInst, fmt.Errorf("directory instance is nil")
	}

	st, err := dirInst.Self.State()
	if err != nil {
		return retInst, fmt.Errorf("failed to get state: %w", err)
	}
	def, err := st.Marshal(ctx, llb.Platform(dirInst.Self.Platform.Spec()))
	if err != nil {
		return retInst, fmt.Errorf("failed to marshal state: %w", err)
	}
	dgst, err := GetContentHashFromDef(ctx, bk, def.ToPB(), dirInst.Self.Dir)
	if err != nil {
		return retInst, fmt.Errorf("failed to get content hash: %w", err)
	}

	return dirInst.WithMetadata(dgst, true), nil
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

	dgst, _, err := checksumG.Do(ctx, ref.ID(), func(ctx context.Context) (_ digest.Digest, rerr error) {
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
			fmt.Sprintf("checksum def: %s", ref.ID()),
			telemetry.Internal(),
		)
		defer telemetry.End(span, func() error { return rerr })

		dgst, err := bkcontenthash.Checksum(ctx, ref, "/", bkcontenthash.ChecksumOpts{}, nil)
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
