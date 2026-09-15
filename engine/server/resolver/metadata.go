package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	ctdreference "github.com/containerd/containerd/v2/pkg/reference"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/util/contentutil"
	"github.com/distribution/reference"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const imageMetadataLeaseTTL = 5 * time.Minute

type imageConfigMetadata struct {
	manifestDesc ocispecs.Descriptor
	manifest     ocispecs.Manifest
	configBytes  []byte
}

func imageConfigPlatformMatcher(platform *ocispecs.Platform) platforms.MatchComparer {
	if platform == nil {
		return platforms.Default()
	}
	return platforms.Only(platforms.Normalize(*platform))
}

func (r *Resolver) tryLocalCanonicalConfigMetadata(
	ctx context.Context,
	ref string,
	opts ResolveImageConfigOpts,
) (string, digest.Digest, []byte, bool, error) {
	parsed, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return "", "", nil, false, nil //nolint:nilerr // Invalid refs are not local-cache hits; the remote resolver reports the real error.
	}
	canonical, ok := parsed.(reference.Canonical)
	if !ok {
		return "", "", nil, false, nil
	}

	rootDesc, found, err := r.localCanonicalRootDescriptor(ctx, canonical.Digest())
	if err != nil || !found {
		return "", "", nil, found, err
	}

	manifestDesc, manifest, found, err := tryResolveLocalManifestDescriptor(ctx, r.contentStore, rootDesc, imageConfigPlatformMatcher(opts.Platform), false)
	if err != nil || !found {
		return "", "", nil, false, err
	}

	configBytes, err := content.ReadBlob(ctx, r.contentStore, manifest.Config)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return "", "", nil, false, nil
		}
		return "", "", nil, false, err
	}

	refspec, err := ctdreference.Parse(canonical.String())
	if err != nil {
		return "", "", nil, false, err
	}
	ok, err = r.localMetadataHasMatchingSource(ctx, refspec, rootDesc, manifestDesc, manifest.Config)
	if err != nil || !ok {
		return "", "", nil, false, err
	}

	return canonical.String(), rootDesc.Digest, configBytes, true, nil
}

func (r *Resolver) ensureImageConfigMetadata(
	ctx context.Context,
	resolvedRef string,
	rootDesc ocispecs.Descriptor,
	fetcher remotes.Fetcher,
	matcher platforms.MatchComparer,
	checkRootManifestPlatform bool,
) (_ *imageConfigMetadata, rerr error) {
	ctx = contentutil.RegisterContentPayloadTypes(ctx)
	leaseCtx, release, err := bkcache.WithLease(ctx, r.leaseManager, leases.WithExpiration(imageMetadataLeaseTTL), bkcache.MakeTemporary)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = release(context.WithoutCancel(leaseCtx))
		}
	}()

	sourceLabelHandler, err := docker.AppendDistributionSourceLabel(r.contentStore, resolvedRef)
	if err != nil {
		return nil, err
	}

	var metadataMu sync.Mutex
	var metadataDescs []ocispecs.Descriptor
	recordMetadata := images.HandlerFunc(func(ctx context.Context, desc ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
		if !images.IsLayerType(desc.MediaType) {
			metadataMu.Lock()
			metadataDescs = append(metadataDescs, desc)
			metadataMu.Unlock()
		}
		return nil, nil
	})

	fetchHandler := remotes.FetchHandler(r.contentStore, fetcher)
	fetchMetadata := images.HandlerFunc(func(ctx context.Context, desc ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
		if images.IsLayerType(desc.MediaType) {
			return nil, images.ErrSkipDesc
		}
		return fetchHandler(ctx, desc)
	})

	handler := images.Handlers(
		recordMetadata,
		fetchMetadata,
		sourceLabelHandler,
		imageConfigMetadataChildrenHandler(r.contentStore, matcher),
	)
	if err := images.Dispatch(leaseCtx, handler, nil, rootDesc); err != nil {
		return nil, err
	}
	if err := r.attachContentToLease(leaseCtx, metadataDescs); err != nil {
		return nil, err
	}

	manifestDesc, manifest, err := resolveManifestDescriptor(leaseCtx, r.contentStore, rootDesc, matcher, checkRootManifestPlatform)
	if err != nil {
		return nil, err
	}
	configBytes, err := content.ReadBlob(leaseCtx, r.contentStore, manifest.Config)
	if err != nil {
		return nil, err
	}

	success = true
	return &imageConfigMetadata{
		manifestDesc: manifestDesc,
		manifest:     manifest,
		configBytes:  configBytes,
	}, nil
}

func imageConfigMetadataChildrenHandler(provider content.Provider, matcher platforms.MatchComparer) images.HandlerFunc {
	return func(ctx context.Context, desc ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
		switch {
		case images.IsIndexType(desc.MediaType):
			p, err := content.ReadBlob(ctx, provider, desc)
			if err != nil {
				return nil, err
			}
			var idx ocispecs.Index
			if err := json.Unmarshal(p, &idx); err != nil {
				return nil, err
			}
			candidates := matchingPlatformManifests(idx.Manifests, matcher)
			if len(candidates) == 0 {
				return nil, fmt.Errorf("no manifest matches requested platform for %s", desc.Digest)
			}
			return candidates[:1], nil

		case images.IsManifestType(desc.MediaType):
			p, err := content.ReadBlob(ctx, provider, desc)
			if err != nil {
				return nil, err
			}
			var manifest ocispecs.Manifest
			if err := json.Unmarshal(p, &manifest); err != nil {
				return nil, err
			}
			return []ocispecs.Descriptor{manifest.Config}, nil

		case images.IsLayerType(desc.MediaType):
			return nil, images.ErrSkipDesc

		default:
			return nil, nil
		}
	}
}

func matchingPlatformManifests(manifests []ocispecs.Descriptor, matcher platforms.MatchComparer) []ocispecs.Descriptor {
	candidates := make([]ocispecs.Descriptor, 0, len(manifests))
	for _, child := range manifests {
		if child.Platform == nil || matcher.Match(*child.Platform) {
			candidates = append(candidates, child)
		}
	}
	slices.SortStableFunc(candidates, func(a, b ocispecs.Descriptor) int {
		if a.Platform == nil {
			return 1
		}
		if b.Platform == nil {
			return -1
		}
		if matcher.Less(*a.Platform, *b.Platform) {
			return -1
		}
		if matcher.Less(*b.Platform, *a.Platform) {
			return 1
		}
		return 0
	})
	return candidates
}

func (r *Resolver) localMetadataHasMatchingSource(ctx context.Context, refspec ctdreference.Spec, descs ...ocispecs.Descriptor) (bool, error) {
	seen := map[digest.Digest]struct{}{}
	for _, desc := range descs {
		if desc.Digest == "" {
			continue
		}
		if _, ok := seen[desc.Digest]; ok {
			continue
		}
		seen[desc.Digest] = struct{}{}

		info, err := r.contentStore.Info(ctx, desc.Digest)
		if err != nil {
			if cerrdefs.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		ok, err := contentutil.HasSource(info, refspec)
		if err != nil || !ok {
			return ok, err
		}
	}
	return true, nil
}

func (r *Resolver) attachContentToLease(ctx context.Context, descs []ocispecs.Descriptor) error {
	leaseID, ok := leases.FromContext(ctx)
	if !ok {
		return errors.New("attach content to lease: missing lease")
	}
	seen := map[digest.Digest]struct{}{}
	for _, desc := range descs {
		if desc.Digest == "" {
			continue
		}
		if _, ok := seen[desc.Digest]; ok {
			continue
		}
		seen[desc.Digest] = struct{}{}
		if err := r.leaseManager.AddResource(ctx, leases.Lease{ID: leaseID}, leases.Resource{
			ID:   desc.Digest.String(),
			Type: "content",
		}); err != nil && !cerrdefs.IsAlreadyExists(err) {
			return err
		}
	}
	return nil
}
