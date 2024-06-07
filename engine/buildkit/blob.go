package buildkit

import (
	"context"
	"fmt"
	"io/fs"

	cacheconfig "github.com/moby/buildkit/cache/config"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bksolverpb "github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/compression"
	bkworker "github.com/moby/buildkit/worker"
	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/engine/sources/blob"
)

// DefToBlob converts the given llb definition to a content addressed blob valid for the
// duration of the current session. It's useful for converting unstable sources like local
// dir imports into stable, content-defined sources.
// NOTE: it's currently assumed that the provided definition is a single layer. Definitions
// can be squashed into a single layer by copying from them to scratch.
func (c *Client) DefToBlob(
	ctx context.Context,
	pbDef *bksolverpb.Definition,
	compressionType compression.Type,
) (_ *bksolverpb.Definition, desc specs.Descriptor, _ error) {
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

	blobDef, err := blob.LLB(desc).Marshal(ctx)
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
}

func (c *Client) BytesToBlob(
	ctx context.Context,
	fileName string,
	perms fs.FileMode,
	bs []byte,
	compressionType compression.Type,
) (_ *bksolverpb.Definition, desc specs.Descriptor, _ error) {
	def, err := llb.Scratch().
		File(llb.Mkfile(fileName, perms, bs)).
		Marshal(ctx)
	if err != nil {
		return nil, desc, fmt.Errorf("failed to create llb definition: %w", err)
	}
	return c.DefToBlob(ctx, def.ToPB(), compressionType)
}
