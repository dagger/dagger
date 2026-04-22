package containersource

import (
	"context"
	"strconv"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/containerd/containerd/v2/pkg/reference"
	"github.com/containerd/platforms"
	"github.com/dagger/dagger/internal/buildkit/cache"
	"github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/client/llb/sourceresolver"
	"github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/snapshot"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/source"
	srctypes "github.com/dagger/dagger/internal/buildkit/source/types"
	"github.com/dagger/dagger/internal/buildkit/util/imageutil"
	"github.com/dagger/dagger/internal/buildkit/util/pull"
	"github.com/dagger/dagger/internal/buildkit/util/resolver"
	"github.com/dagger/dagger/internal/buildkit/util/tracing"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type ResolverType int

const (
	ResolverTypeRegistry ResolverType = iota
	ResolverTypeOCILayout
)

type SourceOpt struct {
	Snapshotter   snapshot.Snapshotter
	ContentStore  content.Store
	CacheAccessor cache.Accessor
	ImageStore    images.Store // optional
	RegistryHosts docker.RegistryHosts
	ResolverType
	LeaseManager leases.Manager
}

type Source struct {
	SourceOpt
}

func NewSource(opt SourceOpt) (*Source, error) {
	is := &Source{
		SourceOpt: opt,
	}

	return is, nil
}

func (is *Source) Schemes() []string {
	if is.ResolverType == ResolverTypeOCILayout {
		return []string{srctypes.OCIScheme}
	}
	return []string{srctypes.DockerImageScheme}
}

func (is *Source) Identifier(ref string, attrs map[string]string, platform *pb.Platform) (source.Identifier, error) {
	if is.ResolverType == ResolverTypeOCILayout {
		return is.ociIdentifier(ref, attrs, platform)
	}

	return is.registryIdentifier(ref, attrs, platform)
}

// SourceInstance represents a cacheable vertex created by a Source.
type SourceInstance interface {
	// Snapshot creates a cache ref for the instance. May return a nil ref if source points to empty content, e.g. image without any layers.
	Snapshot(ctx context.Context, g session.Group) (cache.ImmutableRef, error)
}

func (is *Source) Resolve(ctx context.Context, id source.Identifier, sm *session.Manager) (SourceInstance, error) {
	var (
		p          *puller
		platform   = platforms.DefaultSpec()
		pullerUtil *pull.Puller
		mode       resolver.ResolveMode
		recordType client.UsageRecordType
		ref        reference.Spec
		store      sourceresolver.ResolveImageConfigOptStore
	)
	switch is.ResolverType {
	case ResolverTypeRegistry:
		imageIdentifier, ok := id.(*ImageIdentifier)
		if !ok {
			return nil, errors.Errorf("invalid image identifier %v", id)
		}

		if imageIdentifier.Platform != nil {
			platform = *imageIdentifier.Platform
		}
		mode = imageIdentifier.ResolveMode
		recordType = imageIdentifier.RecordType
		ref = imageIdentifier.Reference
	case ResolverTypeOCILayout:
		ociIdentifier, ok := id.(*OCIIdentifier)
		if !ok {
			return nil, errors.Errorf("invalid OCI layout identifier %v", id)
		}

		if ociIdentifier.Platform != nil {
			platform = *ociIdentifier.Platform
		}
		mode = resolver.ResolveModeForcePull // with OCI layout, we always just "pull"
		store = sourceresolver.ResolveImageConfigOptStore{
			SessionID: ociIdentifier.SessionID,
			StoreID:   ociIdentifier.StoreID,
		}
		ref = ociIdentifier.Reference
	default:
		return nil, errors.Errorf("unknown resolver type: %v", is.ResolverType)
	}
	pullerUtil = &pull.Puller{
		ContentStore: is.ContentStore,
		Platform:     platform,
		Src:          ref,
	}
	p = &puller{
		CacheAccessor:  is.CacheAccessor,
		LeaseManager:   is.LeaseManager,
		Puller:         pullerUtil,
		RegistryHosts:  is.RegistryHosts,
		ResolverType:   is.ResolverType,
		ImageStore:     is.ImageStore,
		Mode:           mode,
		RecordType:     recordType,
		Ref:            ref.String(),
		SessionManager: sm,
		store:          store,
	}
	return p, nil
}

func (is *Source) ResolveImageConfig(ctx context.Context, ref string, opt sourceresolver.Opt, sm *session.Manager, g session.Group) (digest digest.Digest, config []byte, retErr error) {
	span, ctx := tracing.StartSpan(ctx, "resolving "+ref)
	defer func() {
		tracing.FinishWithError(span, retErr)
	}()

	var (
		rslvr remotes.Resolver
		err   error
	)

	switch is.ResolverType {
	case ResolverTypeRegistry:
		iopt := opt.ImageOpt
		if iopt == nil {
			return "", nil, errors.Errorf("missing imageopt for resolve")
		}
		rm, err := resolver.ParseImageResolveMode(iopt.ResolveMode)
		if err != nil {
			return "", nil, err
		}
		rslvr = resolver.DefaultPool.GetResolver(is.RegistryHosts, ref, "pull", sm, g).WithImageStore(is.ImageStore, rm)
	case ResolverTypeOCILayout:
		iopt := opt.OCILayoutOpt
		if iopt == nil {
			return "", nil, errors.Errorf("missing ocilayoutopt for resolve")
		}
		rslvr = getOCILayoutResolver(iopt.Store, sm, g)
	}
	dgst, dt, err := imageutil.Config(ctx, ref, rslvr, is.ContentStore, is.LeaseManager, opt.Platform)
	if err != nil {
		return "", nil, err
	}
	return dgst, dt, nil
}

func (is *Source) registryIdentifier(ref string, attrs map[string]string, platform *pb.Platform) (source.Identifier, error) {
	id, err := NewImageIdentifier(ref)
	if err != nil {
		return nil, err
	}

	if platform != nil {
		id.Platform = &ocispecs.Platform{
			OS:           platform.OS,
			Architecture: platform.Architecture,
			Variant:      platform.Variant,
			OSVersion:    platform.OSVersion,
		}
		if platform.OSFeatures != nil {
			id.Platform.OSFeatures = append([]string{}, platform.OSFeatures...)
		}
	}

	for k, v := range attrs {
		switch k {
		case pb.AttrImageResolveMode:
			rm, err := resolver.ParseImageResolveMode(v)
			if err != nil {
				return nil, err
			}
			id.ResolveMode = rm
		case pb.AttrImageRecordType:
			rt, err := parseImageRecordType(v)
			if err != nil {
				return nil, err
			}
			id.RecordType = rt
		case pb.AttrImageLayerLimit:
			l, err := strconv.Atoi(v)
			if err != nil {
				return nil, errors.Wrapf(err, "invalid layer limit %s", v)
			}
			if l <= 0 {
				return nil, errors.Errorf("invalid layer limit %s", v)
			}
		}
	}

	return id, nil
}

func (is *Source) ociIdentifier(ref string, attrs map[string]string, platform *pb.Platform) (source.Identifier, error) {
	id, err := NewOCIIdentifier(ref)
	if err != nil {
		return nil, err
	}

	if platform != nil {
		id.Platform = &ocispecs.Platform{
			OS:           platform.OS,
			Architecture: platform.Architecture,
			Variant:      platform.Variant,
			OSVersion:    platform.OSVersion,
		}
		if platform.OSFeatures != nil {
			id.Platform.OSFeatures = append([]string{}, platform.OSFeatures...)
		}
	}

	for k, v := range attrs {
		switch k {
		case pb.AttrOCILayoutSessionID:
			id.SessionID = v
		case pb.AttrOCILayoutStoreID:
			id.StoreID = v
		case pb.AttrOCILayoutLayerLimit:
			l, err := strconv.Atoi(v)
			if err != nil {
				return nil, errors.Wrapf(err, "invalid layer limit %s", v)
			}
			if l <= 0 {
				return nil, errors.Errorf("invalid layer limit %s", v)
			}
		}
	}

	return id, nil
}

func parseImageRecordType(v string) (client.UsageRecordType, error) {
	switch client.UsageRecordType(v) {
	case "", client.UsageRecordTypeRegular:
		return client.UsageRecordTypeRegular, nil
	case client.UsageRecordTypeInternal:
		return client.UsageRecordTypeInternal, nil
	case client.UsageRecordTypeFrontend:
		return client.UsageRecordTypeFrontend, nil
	default:
		return "", errors.Errorf("invalid record type %s", v)
	}
}
