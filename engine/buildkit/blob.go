package buildkit

import (
	"context"
	"fmt"
	"io/fs"

	bkcontenthash "github.com/moby/buildkit/cache/contenthash"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bksolverpb "github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	bkworker "github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
	"resenje.org/singleflight"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/engine/contenthash"
)

var checksumG singleflight.Group[string, digest.Digest]

// DefToBlob converts the given llb definition to a content addressed blob valid for the
// duration of the current session. It's useful for converting unstable sources like local
// dir imports into stable, content-defined sources.
func (c *Client) DefToBlob(
	ctx context.Context,
	pbDef *bksolverpb.Definition,
) (digest.Digest, error) {
	res, err := c.Solve(ctx, bkgw.SolveRequest{
		Definition: pbDef,
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
		return "", wrapError(ctx, err, c)
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
		dgst, ok := md.GetContentHashKey()
		if ok {
			bklog.G(ctx).Debugf("DefToBlob reusing ref %s with digest %s", ref.ID(), dgst)
			return dgst, nil
		}

		ctx, span := Tracer(ctx).Start(ctx,
			fmt.Sprintf("checksum def: %s", ref.ID()),
			telemetry.Internal(),
		)
		defer telemetry.End(span, func() error { return rerr })

		dgst, err = bkcontenthash.Checksum(ctx, ref, "/", bkcontenthash.ChecksumOpts{}, nil)
		if err != nil {
			return "", fmt.Errorf("failed to checksum ref: %w", err)
		}
		if err := md.SetContentHashKey(dgst); err != nil {
			return "", fmt.Errorf("failed to set content hash key: %w", err)
		}

		bklog.G(ctx).Debugf("DefToBlob setting ref %s with digest %s", ref.ID(), dgst)

		return dgst, nil
	})
	return dgst, err
}

func (c *Client) BytesToBlob(
	ctx context.Context,
	fileName string,
	perms fs.FileMode,
	bs []byte,
) (digest.Digest, error) {
	def, err := llb.Scratch().
		File(llb.Mkfile(fileName, perms, bs)).
		Marshal(ctx, WithTracePropagation(ctx), WithPassthrough())
	if err != nil {
		return "", fmt.Errorf("failed to create llb definition: %w", err)
	}
	return c.DefToBlob(ctx, def.ToPB())
}
