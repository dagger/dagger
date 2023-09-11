package blob

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/llbsolver/provenance"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/source"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	BlobScheme = "blob"

	MediaTypeAttr = "daggerBlobSourceMediaType"
	SizeAttr      = "daggerBlobSourceSize"
)

type Opt struct {
	CacheAccessor cache.Accessor
}

type blobSource struct {
	cache cache.Accessor
}

type SourceIdentifier struct {
	ocispecs.Descriptor
}

func (SourceIdentifier) Scheme() string {
	return BlobScheme
}

func (id SourceIdentifier) Capture(*provenance.Capture, string) error {
	// TODO: safe to skip? Even if so, should fill in someday once we support provenance
	return nil
}

func LLB(desc ocispecs.Descriptor) llb.State {
	attrs := map[string]string{
		MediaTypeAttr: desc.MediaType,
		SizeAttr:      strconv.Itoa(int(desc.Size)),
	}
	for k, v := range desc.Annotations {
		attrs[k] = v
	}
	return llb.NewState(llb.NewSource(
		fmt.Sprintf("%s://%s", BlobScheme, desc.Digest.String()),
		attrs,
		llb.Constraints{},
	).Output())
}

func IdentifierFromPB(op *pb.SourceOp) (*SourceIdentifier, error) {
	scheme, ref, ok := strings.Cut(op.Identifier, "://")
	if !ok {
		return nil, fmt.Errorf("invalid blob source identifier %q", op.Identifier)
	}
	bs := &blobSource{}
	return bs.identifier(scheme, ref, op.GetAttrs(), nil)
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
	return bs.identifier(scheme, ref, sourceAttrs, p)
}

func (bs *blobSource) identifier(scheme, ref string, sourceAttrs map[string]string, _ *pb.Platform) (*SourceIdentifier, error) {
	desc := ocispecs.Descriptor{
		Digest:      digest.Digest(ref),
		Annotations: map[string]string{},
	}
	for k, v := range sourceAttrs {
		switch k {
		case MediaTypeAttr:
			desc.MediaType = v
		case SizeAttr:
			blobSize, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("invalid blob size %q: %w", v, err)
			}
			desc.Size = int64(blobSize)
		default:
			desc.Annotations[k] = v
		}
	}
	return &SourceIdentifier{desc}, nil
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
	return bs.id.Digest.String(), bs.id.Digest.String(), nil, true, nil
}

func (bs *blobSourceInstance) Snapshot(ctx context.Context, _ session.Group) (cache.ImmutableRef, error) {
	opts := []cache.RefOption{
		// TODO: could also include description of original blob source by passing along more metadata
		cache.WithDescription(fmt.Sprintf("dagger blob source for %s", bs.id.Digest)),
	}
	return bs.cache.GetByBlob(ctx, bs.id.Descriptor, nil, opts...)
}
