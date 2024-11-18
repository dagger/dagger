package buildkit

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/dagger/dagger/engine/sources/local"
	"github.com/moby/buildkit/cache/contenthash"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bksolverpb "github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/flightcontrol"
	bkworker "github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
)

// TODO: find best way to avoid global here
var checksumG flightcontrol.Group[digest.Digest]

// DefToBlob converts the given llb definition to a content addressed blob valid for the
// duration of the current session. It's useful for converting unstable sources like local
// dir imports into stable, content-defined sources.
// NOTE: it's currently assumed that the provided definition is a single layer. Definitions
// can be squashed into a single layer by copying from them to scratch.
// TODO: update above docs ^
// TODO: update above docs ^
// TODO: update above docs ^
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

	return checksumG.Do(ctx, ref.ID(), func(ctx context.Context) (digest.Digest, error) {
		if err := ref.Finalize(ctx); err != nil {
			return "", fmt.Errorf("failed to finalize ref: %w", err)
		}

		// TODO: weird to import local here, split out to separate shared pkg
		md := local.CacheRefMetadata{RefMetadata: ref}
		dgst, ok := md.GetContentHashKey()
		if ok {
			// TODO:
			// TODO:
			// TODO:
			bklog.G(ctx).Debugf("DEFTOBLOB REUSE, DGST: %s, REF: %s", dgst, ref.ID())
			return dgst, nil
		}

		// TODO: wrap with internal span so we can catch slowness
		dgst, err = contenthash.Checksum(ctx, ref, "/", contenthash.ChecksumOpts{}, nil)
		if err != nil {
			return "", fmt.Errorf("failed to checksum ref: %w", err)
		}
		if err := md.SetContentHashKey(dgst); err != nil {
			return "", fmt.Errorf("failed to set content hash key: %w", err)
		}

		// TODO:
		// TODO:
		// TODO:
		bklog.G(ctx).Debugf("DEFTOBLOB NEW, DGST: %s, REF: %s", dgst, ref.ID())

		return dgst, nil
	})
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
