package blob

import (
	"context"
	"fmt"
	"strings"

	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/llbsolver/provenance"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/source"
	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/engine/contenthash"
)

const (
	BlobScheme = "blob"
)

type Opt struct {
	CacheAccessor cache.Accessor
}

type blobSource struct {
	cache cache.Accessor
}

type SourceIdentifier struct {
	Digest digest.Digest
}

func (SourceIdentifier) Scheme() string {
	return BlobScheme
}

func (id SourceIdentifier) Capture(*provenance.Capture, string) error {
	// TODO: should fill in someday once we support provenance
	return nil
}

func LLB(dgst digest.Digest) llb.State {
	attrs := map[string]string{}
	return llb.NewState(llb.NewSource(
		fmt.Sprintf("%s://%s", BlobScheme, dgst.String()),
		attrs,
		llb.Constraints{
			Metadata: pb.OpMetadata{
				Description: map[string]string{
					telemetry.UIPassthroughAttr: "true",
				},
			},
		},
	).Output())
}

func IdentifierFromPB(op *pb.SourceOp) (*SourceIdentifier, error) {
	scheme, ref, ok := strings.Cut(op.Identifier, "://")
	if !ok {
		return nil, fmt.Errorf("invalid blob source identifier %q", op.Identifier)
	}
	if scheme != BlobScheme {
		return nil, fmt.Errorf("invalid blob source identifier %q", op.Identifier)
	}
	bs := &blobSource{}
	return bs.identifier(ref)
}

func NewSource(opt Opt) (source.Source, error) {
	bs := &blobSource{
		cache: opt.CacheAccessor,
	}
	return bs, nil
}

func (bs *blobSource) Schemes() []string {
	return []string{BlobScheme}
}

func (bs *blobSource) Identifier(scheme, ref string, sourceAttrs map[string]string, p *pb.Platform) (source.Identifier, error) {
	return bs.identifier(ref)
}

func (bs *blobSource) identifier(ref string) (*SourceIdentifier, error) {
	dgst, err := digest.Parse(ref)
	if err != nil {
		return nil, fmt.Errorf("invalid blob digest %q: %w", ref, err)
	}
	return &SourceIdentifier{dgst}, nil
}

func (bs *blobSource) Resolve(ctx context.Context, id source.Identifier, sm *session.Manager, _ solver.Vertex) (source.SourceInstance, error) {
	blobIdentifier, ok := id.(*SourceIdentifier)
	if !ok {
		return nil, fmt.Errorf("invalid blob identifier %v", id)
	}
	return &blobSourceInstance{
		id:         blobIdentifier,
		sm:         sm,
		blobSource: bs,
	}, nil
}

type blobSourceInstance struct {
	id *SourceIdentifier
	sm *session.Manager
	*blobSource
}

func (bs *blobSourceInstance) CacheKey(context.Context, session.Group, int) (string, string, solver.CacheOpts, bool, error) {
	// HACK: ideally, we should return a "session:" prefix (turning into a "random:"
	// digest), which ensures that we don't upload the local directory into the cache.
	// However, this is currently unavoidable, since we already upload the local
	// directory because of the cached llb.Copy in client.LocalImport.
	//
	// Since we're already exporting the local directory anyways, we might as well
	// just cause it to upload here, which lets us match on the definition-based
	// fast cache for blob sources.
	// return "session:" + bs.id.Digest.String(), bs.id.Digest.String(), nil, true, nil

	// Definition-based fast cache does not currently match on "random:" digests
	// (because the exported cache loses these pieces). This requires an upstream
	// buildkit fix.
	return "dagger:" + bs.id.Digest.String(), bs.id.Digest.String(), nil, true, nil
}

func (bs *blobSourceInstance) Snapshot(ctx context.Context, _ session.Group) (cache.ImmutableRef, error) {
	mds, err := contenthash.SearchContentHash(ctx, bs.cache, bs.id.Digest)
	if err != nil {
		return nil, fmt.Errorf("searching for blob %s: %w", bs.id.Digest, err)
	}

	for _, md := range mds {
		ref, err := bs.cache.Get(ctx, md.ID(), nil)
		if err != nil {
			bklog.G(ctx).Errorf("failed to get cache ref %s: %v", md.ID(), err)
			continue
		}
		return ref, nil
	}

	return nil, fmt.Errorf("blob %s not found in cache", bs.id.Digest)
}
