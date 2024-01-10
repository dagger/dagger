package blob

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/containerd/containerd/labels"
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

// blob annotations are annotations to preserve
var blobAnnotations = map[string]struct{}{
	// uncompressed label is required by GetByBlob
	labels.LabelUncompressed: {},
}

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
		if _, ok := blobAnnotations[k]; ok {
			attrs[k] = v
		}
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
	opts := []cache.RefOption{
		// TODO: could also include description of original blob source by passing along more metadata
		cache.WithDescription(fmt.Sprintf("dagger blob source for %s", bs.id.Digest)),
	}
	ref, err := bs.cache.GetByBlob(ctx, bs.id.Descriptor, nil, opts...)
	var needsRemoteProviders cache.NeedsRemoteProviderError
	if errors.As(err, &needsRemoteProviders) {
		if optGetter := solver.CacheOptGetterOf(ctx); optGetter != nil {
			var keys []interface{}
			for _, dgst := range needsRemoteProviders {
				keys = append(keys, cache.DescHandlerKey(dgst))
			}
			descHandlers := cache.DescHandlers(make(map[digest.Digest]*cache.DescHandler))
			for k, v := range optGetter(true, keys...) {
				if key, ok := k.(cache.DescHandlerKey); ok {
					if handler, ok := v.(*cache.DescHandler); ok {
						descHandlers[digest.Digest(key)] = handler
					}
				}
			}
			opts = append(opts, descHandlers)
			ref, err = bs.cache.GetByBlob(ctx, bs.id.Descriptor, nil, opts...)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load ref for blob snapshot: %w", err)
	}
	return ref, nil
}
