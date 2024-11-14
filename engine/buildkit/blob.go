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
	bkworker "github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
)

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

	if err := ref.Finalize(ctx); err != nil {
		return "", fmt.Errorf("failed to finalize ref: %w", err)
	}

	// TODO: weird to import local here, split out to separate shared pkg
	md := local.CacheRefMetadata{RefMetadata: ref}
	dgst, ok := md.GetContentHashKey()
	if ok {
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

	return dgst, nil

	/* TODO: this isn't needed anymore, right?
	// Force an unlazy of the ref in case it is a reference to remote cache
	err = ref.Extract(ctx, nil)
	if err != nil {
		return nil, desc, fmt.Errorf("failed to extract ref: %w", err)
	}
	*/

	/* TODO:
	if compressionType == nil {
		compressionType = compression.Zstd
	}

	res, err := c.Solve(ctx, bkgw.SolveRequest{
		Definition: pbDef,
		Evaluate:   true,
	})
	if err != nil {
		return nil, desc, err
	}
	resultProxy, err := res.SingleRef()
	if err != nil {
		return nil, desc, fmt.Errorf("failed to get single ref: %w", err)
	}
	cachedRes, err := resultProxy.Result(ctx)
	if err != nil {
		return nil, desc, wrapError(ctx, err, c)
	}
	workerRef, ok := cachedRes.Sys().(*bkworker.WorkerRef)
	if !ok {
		return nil, desc, fmt.Errorf("invalid ref: %T", cachedRes.Sys())
	}
	ref := workerRef.ImmutableRef

	// Force an unlazy of the copy in case it was lazy due to remote caching; we
	// need it to exist locally or else blob source won't work.
	// NOTE: in theory we could keep it lazy if we could get the descriptor handlers
	// for the remote over to the blob source code, but the plumbing to accomplish that
	// is tricky and ultimately only result in a marginal performance optimization.
	err = ref.Extract(ctx, nil)
	if err != nil {
		return nil, desc, fmt.Errorf("failed to extract ref: %w", err)
	}

	remotes, err := ref.GetRemotes(ctx, true, cacheconfig.RefConfig{
		Compression: compression.Config{Type: compressionType},
	}, false, nil)
	if err != nil {
		return nil, desc, fmt.Errorf("failed to get remotes: %w", err)
	}
	if len(remotes) != 1 {
		return nil, desc, fmt.Errorf("expected 1 remote, got %d", len(remotes))
	}
	remote := remotes[0]
	if len(remote.Descriptors) != 1 {
		return nil, desc, fmt.Errorf("expected 1 descriptor, got %d", len(remote.Descriptors))
	}

	desc = remote.Descriptors[0]

	blobDef, err := blob.LLB(desc).Marshal(ctx, WithTracePropagation(ctx))
	if err != nil {
		return nil, desc, fmt.Errorf("failed to marshal blob source: %w", err)
	}
	blobPB := blobDef.ToPB()

	// do a sync solve right now so we can release the cache ref for the first solve
	// without giving up the lease on the blob
	_, err = c.Solve(ctx, bkgw.SolveRequest{
		Definition: blobPB,
		Evaluate:   true,
	})
	if err != nil {
		return nil, desc, fmt.Errorf("failed to solve blobsource: %w", wrapError(ctx, err, c))
	}

	return blobPB, desc, nil
	*/
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
