package containersource

import (
	"context"
	"encoding/json"
	"maps"
	"runtime"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/containerd/containerd/v2/core/snapshots"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/internal/buildkit/cache"
	"github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/client/llb/sourceresolver"
	"github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/solver/errdefs"
	"github.com/dagger/dagger/internal/buildkit/util/estargz"
	"github.com/dagger/dagger/internal/buildkit/util/imageutil"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	"github.com/dagger/dagger/internal/buildkit/util/pull"
	"github.com/dagger/dagger/internal/buildkit/util/resolver"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

type puller struct {
	CacheAccessor  cache.Accessor
	LeaseManager   leases.Manager
	RegistryHosts  docker.RegistryHosts
	ImageStore     images.Store
	Mode           resolver.ResolveMode
	RecordType     client.UsageRecordType
	Ref            string
	SessionManager *session.Manager
	ResolverType
	store sourceresolver.ResolveImageConfigOptStore

	cacheKeyErr      error
	cacheKeyDone     bool
	releaseTmpLeases func(context.Context) error
	descHandlers     cache.DescHandlers
	manifest         *pull.PulledManifests
	manifestKey      string
	configKey        string
	*pull.Puller
}

func mainManifestKey(desc ocispecs.Descriptor, platform ocispecs.Platform) (digest.Digest, error) {
	dt, err := json.Marshal(struct {
		Digest     digest.Digest
		OS         string
		Arch       string
		Variant    string   `json:",omitempty"`
		OSVersion  string   `json:",omitempty"`
		OSFeatures []string `json:",omitempty"`
		Limit      *int     `json:",omitempty"`
	}{
		Digest:     desc.Digest,
		OS:         platform.OS,
		Arch:       platform.Architecture,
		Variant:    platform.Variant,
		OSVersion:  platform.OSVersion,
		OSFeatures: platform.OSFeatures,
	})
	if err != nil {
		return "", err
	}
	return digest.FromBytes(dt), nil
}

//nolint:gocyclo
func (p *puller) Snapshot(ctx context.Context, g session.Group) (ir cache.ImmutableRef, err error) {
	var getResolver pull.SessionResolver
	switch p.ResolverType {
	case ResolverTypeRegistry:
		resolver := resolver.DefaultPool.GetResolver(p.RegistryHosts, p.Ref, "pull", p.SessionManager, g).WithImageStore(p.ImageStore, p.Mode)
		p.Puller.Resolver = resolver
		getResolver = func(g session.Group) remotes.Resolver { return resolver.WithSession(g) }
	case ResolverTypeOCILayout:
		resolver := getOCILayoutResolver(p.store, p.SessionManager, g)
		p.Puller.Resolver = resolver
		// OCILayout has no need for session
		getResolver = func(g session.Group) remotes.Resolver { return resolver }
	default:
	}

	if p.cacheKeyErr != nil || p.cacheKeyDone {
		return nil, p.cacheKeyErr
	}
	defer func() {
		if !errdefs.IsCanceled(ctx, err) {
			p.cacheKeyErr = err
		}
	}()
	ctx, done, err := leaseutil.WithLease(ctx, p.LeaseManager, leases.WithExpiration(5*time.Minute), leaseutil.MakeTemporary)
	if err != nil {
		return nil, err
	}
	p.releaseTmpLeases = done
	defer imageutil.AddLease(done)

	p.manifest, err = p.PullManifests(ctx, getResolver)
	if err != nil {
		return nil, err
	}

	if len(p.manifest.Descriptors) > 0 {
		p.descHandlers = cache.DescHandlers(make(map[digest.Digest]*cache.DescHandler))
		for i, desc := range p.manifest.Descriptors {
			labels := snapshots.FilterInheritedLabels(desc.Annotations)
			if labels == nil {
				labels = make(map[string]string)
			}
			maps.Copy(labels, estargz.SnapshotLabels(p.manifest.Ref, p.manifest.Descriptors, i))

			p.descHandlers[desc.Digest] = &cache.DescHandler{
				Provider:       p.manifest.Provider,
				SnapshotLabels: labels,
				Annotations:    desc.Annotations,
				Ref:            p.manifest.Ref,
			}
		}
	}

	desc := p.manifest.MainManifestDesc
	k, err := mainManifestKey(desc, p.Platform)
	if err != nil {
		return nil, err
	}
	p.manifestKey = k.String()

	dt, err := content.ReadBlob(ctx, p.ContentStore, p.manifest.ConfigDesc)
	if err != nil {
		return nil, err
	}
	ck := cacheKeyFromConfig(dt)
	p.configKey = ck.String()
	p.cacheKeyDone = true

	if len(p.manifest.Descriptors) == 0 {
		return nil, nil
	}
	defer func() {
		if p.releaseTmpLeases != nil {
			p.releaseTmpLeases(context.WithoutCancel(ctx))
		}
	}()

	var current cache.ImmutableRef
	defer func() {
		if err != nil && current != nil {
			current.Release(context.WithoutCancel(ctx))
		}
	}()

	var parent cache.ImmutableRef
	setWindowsLayerType := p.Platform.OS == "windows" && runtime.GOOS != "windows"
	for _, layerDesc := range p.manifest.Descriptors {
		parent = current
		current, err = p.CacheAccessor.GetByBlob(ctx, layerDesc, parent,
			p.descHandlers, cache.WithImageRef(p.manifest.Ref))
		if parent != nil {
			parent.Release(context.WithoutCancel(ctx))
		}
		if err != nil {
			return nil, err
		}
		if setWindowsLayerType {
			if err := current.SetLayerType("windows"); err != nil {
				return nil, err
			}
		}
	}

	for _, desc := range p.manifest.Nonlayers {
		if _, err := p.ContentStore.Info(ctx, desc.Digest); cerrdefs.IsNotFound(err) {
			// manifest or config must have gotten gc'd after CacheKey, re-pull them
			ctx, done, err := leaseutil.WithLease(ctx, p.LeaseManager, leaseutil.MakeTemporary)
			if err != nil {
				return nil, err
			}
			defer done(context.WithoutCancel(ctx))

			if _, err := p.PullManifests(ctx, getResolver); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}

		if err := p.LeaseManager.AddResource(ctx, leases.Lease{ID: current.ID()}, leases.Resource{
			ID:   desc.Digest.String(),
			Type: "content",
		}); err != nil {
			return nil, err
		}
	}

	if p.RecordType != "" && current.GetRecordType() == "" {
		if err := current.SetRecordType(p.RecordType); err != nil {
			return nil, err
		}
	}

	return current, nil
}

// cacheKeyFromConfig returns a stable digest from image config. If image config
// is a known oci image we will use chainID of layers.
func cacheKeyFromConfig(dt []byte) digest.Digest {
	var img ocispecs.Image
	err := json.Unmarshal(dt, &img)
	if err != nil {
		return digest.FromBytes(dt) // digest of config
	}
	if img.RootFS.Type != "layers" || len(img.RootFS.DiffIDs) == 0 {
		return ""
	}

	return identity.ChainID(img.RootFS.DiffIDs)
}
