package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
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
	"github.com/dagger/dagger/auth"
	bkauth "github.com/dagger/dagger/internal/buildkit/session/auth"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/internal/buildkit/util/contentutil"
	"github.com/dagger/dagger/internal/buildkit/util/flightcontrol"
	"github.com/dagger/dagger/internal/buildkit/util/imageutil"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	buildkitpush "github.com/dagger/dagger/internal/buildkit/util/push"
	"github.com/distribution/reference"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var ErrCredentialsNotFound = errors.New("registry credentials not found")

type Credentials struct {
	Username string
	Secret   string
}

type AuthSource interface {
	Credentials(ctx context.Context, host string) (Credentials, error)
}

type ResolveMode int

const (
	ResolveModeDefault ResolveMode = iota
	ResolveModeForcePull
)

type Opts struct {
	Hosts        docker.RegistryHosts
	Auth         AuthSource
	ContentStore content.Store
	LeaseManager leases.Manager
}

type ResolveImageConfigOpts struct {
	Platform    *ocispecs.Platform
	ResolveMode ResolveMode
}

type PullOpts struct {
	Platform    ocispecs.Platform
	ResolveMode ResolveMode
	LayerLimit  *int
}

type PushOpts struct {
	Insecure bool
	ByDigest bool
}

type PushedImage struct {
	RootDesc          ocispecs.Descriptor
	Provider          content.InfoReaderProvider
	SourceAnnotations map[digest.Digest]map[string]string
}

type PulledImage struct {
	Ref          string
	ManifestDesc ocispecs.Descriptor
	ConfigDesc   ocispecs.Descriptor
	Layers       []ocispecs.Descriptor
	Nonlayers    []ocispecs.Descriptor

	release func(context.Context) error
}

func (img *PulledImage) Release(ctx context.Context) error {
	if img == nil || img.release == nil {
		return nil
	}
	return img.release(ctx)
}

type Resolver struct {
	hosts        docker.RegistryHosts
	auth         AuthSource
	contentStore content.Store
	leaseManager leases.Manager

	resolveConfigG flightcontrol.Group[*resolveImageConfigResult]

	mu        sync.Mutex
	hostCache map[string][]docker.RegistryHost
	closers   []func() error
}

func New(opts Opts) *Resolver {
	return &Resolver{
		hosts:        opts.Hosts,
		auth:         opts.Auth,
		contentStore: opts.ContentStore,
		leaseManager: opts.LeaseManager,
		hostCache:    map[string][]docker.RegistryHost{},
	}
}

func (r *Resolver) Close() error {
	r.mu.Lock()
	closers := r.closers
	r.closers = nil
	r.hostCache = map[string][]docker.RegistryHost{}
	r.mu.Unlock()

	var rerr error
	for _, closeFn := range closers {
		if closeFn != nil {
			rerr = errors.Join(rerr, closeFn())
		}
	}
	return rerr
}

func (r *Resolver) ResolveImageConfig(
	ctx context.Context,
	ref string,
	opts ResolveImageConfigOpts,
) (string, digest.Digest, []byte, error) {
	key := ref
	if opts.Platform != nil {
		key += platforms.Format(platforms.Normalize(*opts.Platform))
	}
	key += fmt.Sprintf(":%d", opts.ResolveMode)

	resolved, err := r.resolveConfigG.Do(ctx, key, func(ctx context.Context) (*resolveImageConfigResult, error) {
		if opts.ResolveMode == ResolveModeDefault {
			if resolvedRef, dgst, configBytes, found, err := r.tryLocalCanonicalConfig(ctx, ref, opts); err != nil {
				return nil, err
			} else if found {
				return &resolveImageConfigResult{
					ref:    resolvedRef,
					digest: dgst,
					config: configBytes,
				}, nil
			}
		}

		resolvedRef, rootDesc, resolver, err := r.resolveRemoteRootDescriptor(ctx, ref)
		if err != nil {
			return nil, err
		}
		fetcher, err := resolver.Fetcher(ctx, resolvedRef)
		if err != nil {
			return nil, err
		}

		platformMatcher := platforms.Default()
		if opts.Platform != nil {
			platformMatcher = platforms.Only(platforms.Normalize(*opts.Platform))
		}
		provider := contentutil.FromFetcher(fetcher)
		configDesc, err := images.Config(ctx, provider, rootDesc, platformMatcher)
		if err != nil {
			return nil, err
		}
		configBytes, err := content.ReadBlob(ctx, provider, configDesc)
		if err != nil {
			return nil, err
		}

		return &resolveImageConfigResult{
			ref:    resolvedRef,
			digest: rootDesc.Digest,
			config: configBytes,
		}, nil
	})
	if err != nil {
		return "", "", nil, err
	}
	return resolved.ref, resolved.digest, resolved.config, nil
}

func (r *Resolver) Pull(ctx context.Context, ref string, opts PullOpts) (_ *PulledImage, rerr error) {
	platformMatcher := platforms.Only(platforms.Normalize(opts.Platform))
	if opts.ResolveMode == ResolveModeDefault {
		if parsed, err := reference.ParseNormalizedNamed(ref); err == nil {
			if canonical, ok := parsed.(reference.Canonical); ok {
				rootDesc, found, err := r.localCanonicalRootDescriptor(ctx, canonical.Digest())
				if err != nil {
					return nil, err
				}
				if found {
					closure, found, release, err := r.tryLocalCanonicalClosure(ctx, canonical.String(), rootDesc, platformMatcher, opts.LayerLimit)
					if err != nil {
						return nil, err
					}
					if found {
						return &PulledImage{
							Ref:          canonical.String(),
							ManifestDesc: closure.ManifestDesc,
							ConfigDesc:   closure.ConfigDesc,
							Layers:       closure.Layers,
							Nonlayers:    closure.Nonlayers,
							release:      release,
						}, nil
					}
				}
			}
		}
	}

	resolvedRef, rootDesc, resolver, err := r.resolveRemoteRootDescriptor(ctx, ref)
	if err != nil {
		return nil, err
	}
	fetcher, err := resolver.Fetcher(ctx, resolvedRef)
	if err != nil {
		return nil, err
	}
	provider := contentutil.FromFetcher(fetcher)
	manifestDesc, manifest, err := resolveManifestDescriptor(ctx, provider, rootDesc, platformMatcher)
	if err != nil {
		return nil, err
	}

	leaseCtx, release, err := leaseutil.WithLease(ctx, r.leaseManager, leases.WithExpiration(5*time.Minute), leaseutil.MakeTemporary)
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil {
			release(context.WithoutCancel(leaseCtx))
		}
	}()

	metadata := map[digest.Digest]ocispecs.Descriptor{}
	recordNonLayers := images.HandlerFunc(func(ctx context.Context, desc ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
		if !images.IsLayerType(desc.MediaType) {
			metadata[desc.Digest] = desc
		}
		return nil, nil
	})
	dslHandler, err := docker.AppendDistributionSourceLabel(r.contentStore, resolvedRef)
	if err != nil {
		return nil, err
	}
	childrenHandler := images.ChildrenHandler(r.contentStore)
	handler := images.Handlers(
		recordNonLayers,
		remotes.FetchHandler(r.contentStore, fetcher),
		childrenHandler,
		dslHandler,
	)
	if err := images.Dispatch(leaseCtx, handler, nil, manifestDesc); err != nil {
		return nil, err
	}

	configDesc := manifest.Config
	diffIDs, err := images.RootFS(leaseCtx, r.contentStore, configDesc)
	if err != nil {
		return nil, err
	}
	if len(manifest.Layers) != len(diffIDs) {
		return nil, fmt.Errorf("mismatched image rootfs and manifest layers: %d diffIDs vs %d layers", len(diffIDs), len(manifest.Layers))
	}

	layers := make([]ocispecs.Descriptor, len(manifest.Layers))
	for i, layer := range manifest.Layers {
		layer = hydratePulledDescriptor(layer)
		if layer.Annotations == nil {
			layer.Annotations = map[string]string{}
		}
		layer.Annotations["containerd.io/uncompressed"] = diffIDs[i].String()
		layers[i] = layer
	}
	if opts.LayerLimit != nil {
		if *opts.LayerLimit > len(layers) {
			return nil, fmt.Errorf("layer limit %d exceeds image layer count %d", *opts.LayerLimit, len(layers))
		}
		layers = layers[:*opts.LayerLimit]
	}

	nonlayers := make([]ocispecs.Descriptor, 0, len(metadata))
	for _, desc := range metadata {
		if desc.Digest == manifestDesc.Digest || desc.Digest == configDesc.Digest || images.IsLayerType(desc.MediaType) {
			continue
		}
		nonlayers = append(nonlayers, hydratePulledDescriptor(desc))
	}
	slices.SortFunc(nonlayers, func(a, b ocispecs.Descriptor) int {
		return strings.Compare(a.Digest.String(), b.Digest.String())
	})

	return &PulledImage{
		Ref:          resolvedRef,
		ManifestDesc: manifestDesc,
		ConfigDesc:   hydratePulledDescriptor(configDesc),
		Layers:       layers,
		Nonlayers:    nonlayers,
		release:      release,
	}, nil
}

type localizedImageClosure struct {
	ManifestDesc ocispecs.Descriptor
	ConfigDesc   ocispecs.Descriptor
	Layers       []ocispecs.Descriptor
	Nonlayers    []ocispecs.Descriptor
}

func (r *Resolver) resolveRemoteRootDescriptor(ctx context.Context, ref string) (string, ocispecs.Descriptor, remotes.Resolver, error) {
	resolver := docker.NewResolver(docker.ResolverOptions{
		Hosts: r.registryHosts(),
	})
	resolvedRef, rootDesc, err := resolver.Resolve(ctx, ref)
	if err != nil {
		return "", ocispecs.Descriptor{}, nil, err
	}
	return resolvedRef, rootDesc, resolver, nil
}

func (r *Resolver) tryLocalCanonicalConfig(
	ctx context.Context,
	ref string,
	opts ResolveImageConfigOpts,
) (string, digest.Digest, []byte, bool, error) {
	parsed, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return "", "", nil, false, nil
	}
	canonical, ok := parsed.(reference.Canonical)
	if !ok {
		return "", "", nil, false, nil
	}
	rootDesc, found, err := r.localCanonicalRootDescriptor(ctx, canonical.Digest())
	if err != nil || !found {
		return "", "", nil, found, err
	}
	platformMatcher := platforms.Default()
	if opts.Platform != nil {
		platformMatcher = platforms.Only(platforms.Normalize(*opts.Platform))
	}
	closure, found, release, err := r.tryLocalCanonicalClosure(ctx, canonical.String(), rootDesc, platformMatcher, nil)
	if err != nil || !found {
		return "", "", nil, found, err
	}
	defer release(context.WithoutCancel(ctx))
	configBytes, err := content.ReadBlob(ctx, r.contentStore, closure.ConfigDesc)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return "", "", nil, false, nil
		}
		return "", "", nil, false, err
	}
	return canonical.String(), rootDesc.Digest, configBytes, true, nil
}

func (r *Resolver) tryLocalCanonicalClosure(
	ctx context.Context,
	resolvedRef string,
	rootDesc ocispecs.Descriptor,
	matcher platforms.MatchComparer,
	layerLimit *int,
) (_ *localizedImageClosure, _ bool, _ func(context.Context) error, rerr error) {
	rootDesc, found, err := r.localCanonicalRootDescriptor(ctx, rootDesc.Digest)
	if err != nil || !found {
		return nil, found, nil, err
	}

	manifestDesc, manifest, err := resolveManifestDescriptor(ctx, r.contentStore, rootDesc, matcher)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil, false, nil, nil
		}
		return nil, false, nil, err
	}

	configDesc := hydratePulledDescriptor(manifest.Config)
	if _, err := r.contentStore.Info(ctx, configDesc.Digest); err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil, false, nil, nil
		}
		return nil, false, nil, err
	}

	diffIDs, err := images.RootFS(ctx, r.contentStore, configDesc)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil, false, nil, nil
		}
		return nil, false, nil, err
	}
	if len(manifest.Layers) != len(diffIDs) {
		return nil, false, nil, fmt.Errorf("mismatched image rootfs and manifest layers: %d diffIDs vs %d layers", len(diffIDs), len(manifest.Layers))
	}

	layers := make([]ocispecs.Descriptor, len(manifest.Layers))
	for i, layer := range manifest.Layers {
		if _, err := r.contentStore.Info(ctx, layer.Digest); err != nil {
			if cerrdefs.IsNotFound(err) {
				return nil, false, nil, nil
			}
			return nil, false, nil, err
		}
		layer = hydratePulledDescriptor(layer)
		if layer.Annotations == nil {
			layer.Annotations = map[string]string{}
		}
		layer.Annotations["containerd.io/uncompressed"] = diffIDs[i].String()
		layers[i] = layer
	}
	if layerLimit != nil {
		if *layerLimit > len(layers) {
			return nil, false, nil, fmt.Errorf("layer limit %d exceeds image layer count %d", *layerLimit, len(layers))
		}
		layers = layers[:*layerLimit]
	}

	metadata := map[digest.Digest]ocispecs.Descriptor{}
	childrenHandler := images.ChildrenHandler(r.contentStore)
	recordNonLayers := images.HandlerFunc(func(ctx context.Context, desc ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
		if !images.IsLayerType(desc.MediaType) {
			metadata[desc.Digest] = desc
		}
		return childrenHandler(ctx, desc)
	})
	if err := images.Dispatch(ctx, recordNonLayers, nil, manifestDesc); err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil, false, nil, nil
		}
		return nil, false, nil, err
	}

	nonlayers := make([]ocispecs.Descriptor, 0, len(metadata))
	for _, desc := range metadata {
		if desc.Digest == manifestDesc.Digest || desc.Digest == configDesc.Digest || images.IsLayerType(desc.MediaType) {
			continue
		}
		nonlayers = append(nonlayers, hydratePulledDescriptor(desc))
	}
	slices.SortFunc(nonlayers, func(a, b ocispecs.Descriptor) int {
		return strings.Compare(a.Digest.String(), b.Digest.String())
	})

	closure := &localizedImageClosure{
		ManifestDesc: manifestDesc,
		ConfigDesc:   configDesc,
		Layers:       layers,
		Nonlayers:    nonlayers,
	}
	refspec, err := ctdreference.Parse(resolvedRef)
	if err != nil {
		return nil, false, nil, err
	}
	if ok, err := r.localClosureHasMatchingSource(ctx, refspec, closure); err != nil {
		return nil, false, nil, err
	} else if !ok {
		return nil, false, nil, nil
	}

	leaseCtx, release, err := leaseutil.WithLease(ctx, r.leaseManager, leases.WithExpiration(5*time.Minute), leaseutil.MakeTemporary)
	if err != nil {
		return nil, false, nil, err
	}
	defer func() {
		if rerr != nil {
			release(context.WithoutCancel(leaseCtx))
		}
	}()
	if err := r.attachLocalizedClosureToLease(leaseCtx, closure); err != nil {
		return nil, false, nil, err
	}

	return closure, true, release, nil
}

func (r *Resolver) localClosureHasMatchingSource(ctx context.Context, refspec ctdreference.Spec, closure *localizedImageClosure) (bool, error) {
	descs := make([]ocispecs.Descriptor, 0, 1+len(closure.Layers)+len(closure.Nonlayers))
	descs = append(descs, closure.ConfigDesc)
	descs = append(descs, closure.Layers...)
	descs = append(descs, closure.Nonlayers...)
	for _, desc := range descs {
		info, err := r.contentStore.Info(ctx, desc.Digest)
		if err != nil {
			if cerrdefs.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		ok, err := contentutil.HasSource(info, refspec)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (r *Resolver) attachLocalizedClosureToLease(ctx context.Context, closure *localizedImageClosure) error {
	leaseID, ok := leases.FromContext(ctx)
	if !ok {
		return errors.New("attach localized closure: missing lease")
	}
	seen := map[digest.Digest]struct{}{}
	descs := make([]ocispecs.Descriptor, 0, 2+len(closure.Layers)+len(closure.Nonlayers))
	descs = append(descs, closure.ManifestDesc, closure.ConfigDesc)
	descs = append(descs, closure.Layers...)
	descs = append(descs, closure.Nonlayers...)
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

func (r *Resolver) localCanonicalRootDescriptor(ctx context.Context, dgst digest.Digest) (ocispecs.Descriptor, bool, error) {
	if dgst == "" {
		return ocispecs.Descriptor{}, false, nil
	}
	ra, err := r.contentStore.ReaderAt(ctx, ocispecs.Descriptor{Digest: dgst})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return ocispecs.Descriptor{}, false, nil
		}
		return ocispecs.Descriptor{}, false, err
	}
	defer ra.Close()

	mediaType, err := imageutil.DetectManifestMediaType(ra)
	if err != nil {
		return ocispecs.Descriptor{}, false, err
	}
	return ocispecs.Descriptor{
		Digest:    dgst,
		Size:      ra.Size(),
		MediaType: mediaType,
	}, true, nil
}

func (r *Resolver) PushImage(ctx context.Context, img *PushedImage, ref string, opts PushOpts) error {
	if img == nil {
		return errors.New("pushed image is nil")
	}

	ctx = contentutil.RegisterContentPayloadTypes(ctx)
	rootDesc := img.RootDesc
	parsedRef, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return err
	}
	if opts.ByDigest {
		ref = parsedRef.Name()
	} else {
		refWithDigest, err := reference.WithDigest(reference.TagNameOnly(parsedRef), rootDesc.Digest)
		if err != nil {
			return err
		}
		ref = refWithDigest.String()
	}

	resolver := docker.NewResolver(docker.ResolverOptions{
		Hosts: r.pushRegistryHosts(opts.Insecure),
	})
	pusher, err := buildkitpush.Pusher(ctx, resolver, ref)
	if err != nil {
		return err
	}

	pushHandler := buildkitpush.Pusher
	_ = pushHandler

	pushUpdateSourceHandler, err := updateDistributionSourceHandler(r.contentStore, images.HandlerFunc(func(ctx context.Context, desc ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
		_, err := limitedPushHandler(pusher, img.Provider, ref)(ctx, desc)
		return nil, err
	}), ref)
	if err != nil {
		return err
	}

	handlers := []images.Handler{
		images.HandlerFunc(annotateDistributionSourceHandler(r.contentStore, img.SourceAnnotations, imageChildrenHandler(img.Provider))),
		images.HandlerFunc(func(ctx context.Context, desc ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
			switch desc.MediaType {
			case images.MediaTypeDockerSchema2Manifest, ocispecs.MediaTypeImageManifest,
				images.MediaTypeDockerSchema2ManifestList, ocispecs.MediaTypeImageIndex:
				return nil, images.ErrStopHandler
			default:
				return nil, nil
			}
		}),
		dedupeHandler(pushUpdateSourceHandler),
	}

	ra, err := img.Provider.ReaderAt(ctx, rootDesc)
	if err != nil {
		return err
	}
	mediaType, err := imageutil.DetectManifestMediaType(ra)
	if err != nil {
		return err
	}
	rootDesc.MediaType = mediaType

	if err := images.Dispatch(ctx, skipNonDistributableBlobs(images.Handlers(handlers...)), nil, rootDesc); err != nil {
		return err
	}

	manifestStack, err := collectManifestStack(ctx, img.Provider, rootDesc)
	if err != nil {
		return err
	}
	pushLeaf := limitedPushHandler(pusher, img.Provider, ref)
	for i := len(manifestStack) - 1; i >= 0; i-- {
		if _, err := pushLeaf(ctx, manifestStack[i]); err != nil {
			return err
		}
	}
	return nil
}

type resolveImageConfigResult struct {
	ref    string
	digest digest.Digest
	config []byte
}

type sessionAuthSource struct {
	authProvider      *auth.RegistryAuthProvider
	getMainClientConn func(context.Context) (*grpc.ClientConn, error)
}

func NewSessionAuthSource(
	authProvider *auth.RegistryAuthProvider,
	getMainClientConn func(context.Context) (*grpc.ClientConn, error),
) AuthSource {
	return &sessionAuthSource{
		authProvider:      authProvider,
		getMainClientConn: getMainClientConn,
	}
}

func (s *sessionAuthSource) Credentials(ctx context.Context, host string) (Credentials, error) {
	if s == nil || s.authProvider == nil {
		return Credentials{}, ErrCredentialsNotFound
	}

	resp, err := s.authProvider.Credentials(ctx, &bkauth.CredentialsRequest{Host: host})
	switch {
	case err == nil:
		return Credentials{
			Username: resp.Username,
			Secret:   resp.Secret,
		}, nil
	case status.Code(err) != codes.NotFound:
		return Credentials{}, err
	}

	if s.getMainClientConn == nil {
		return Credentials{}, ErrCredentialsNotFound
	}
	conn, err := s.getMainClientConn(ctx)
	if err != nil {
		return Credentials{}, fmt.Errorf("get main client conn: %w", err)
	}
	resp, err = bkauth.NewAuthClient(conn).Credentials(ctx, &bkauth.CredentialsRequest{Host: host})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return Credentials{}, ErrCredentialsNotFound
		}
		return Credentials{}, fmt.Errorf("get credentials: %w", err)
	}
	return Credentials{
		Username: resp.Username,
		Secret:   resp.Secret,
	}, nil
}

func (r *Resolver) registryHosts() docker.RegistryHosts {
	return func(domain string) ([]docker.RegistryHost, error) {
		r.mu.Lock()
		cached, ok := r.hostCache[domain]
		r.mu.Unlock()
		if ok {
			return cloneRegistryHosts(cached), nil
		}

		var hosts []docker.RegistryHost
		var err error
		if r.hosts != nil {
			hosts, err = r.hosts(domain)
			if err != nil {
				return nil, err
			}
		}
		if len(hosts) == 0 {
			hosts, err = docker.ConfigureDefaultRegistries(docker.WithPlainHTTP(docker.MatchLocalhost))(domain)
			if err != nil {
				return nil, err
			}
		}

		sessionHosts := make([]docker.RegistryHost, len(hosts))
		closeFns := make([]func() error, 0, len(hosts))
		for i, host := range hosts {
			host := host
			if host.Header != nil {
				host.Header = host.Header.Clone()
			}
			if host.Client == nil {
				host.Client = http.DefaultClient
			}
			host.Authorizer = docker.NewDockerAuthorizer(
				docker.WithAuthClient(host.Client),
				docker.WithAuthHeader(host.Header),
				docker.WithAuthCreds(func(host string) (string, string, error) {
					username, secret, err := r.lookupCredentials(host)
					return username, secret, err
				}),
			)
			sessionHosts[i] = host
			if host.Client != nil {
				closeFns = append(closeFns, func() error {
					host.Client.CloseIdleConnections()
					return nil
				})
			}
		}

		r.mu.Lock()
		r.hostCache[domain] = cloneRegistryHosts(sessionHosts)
		r.closers = append(r.closers, closeFns...)
		r.mu.Unlock()

		return cloneRegistryHosts(sessionHosts), nil
	}
}

func (r *Resolver) lookupCredentials(host string) (string, string, error) {
	if r.auth == nil {
		return "", "", nil
	}
	creds, err := r.auth.Credentials(context.Background(), host)
	if err != nil {
		if errors.Is(err, ErrCredentialsNotFound) {
			return "", "", nil
		}
		return "", "", err
	}
	return creds.Username, creds.Secret, nil
}

func cloneRegistryHosts(src []docker.RegistryHost) []docker.RegistryHost {
	if len(src) == 0 {
		return nil
	}
	dst := make([]docker.RegistryHost, len(src))
	for i, host := range src {
		dst[i] = host
		if host.Header != nil {
			dst[i].Header = host.Header.Clone()
		}
	}
	return dst
}

func resolveManifestDescriptor(
	ctx context.Context,
	provider content.Provider,
	desc ocispecs.Descriptor,
	matcher platforms.MatchComparer,
) (ocispecs.Descriptor, ocispecs.Manifest, error) {
	switch {
	case images.IsManifestType(desc.MediaType):
		p, err := content.ReadBlob(ctx, provider, desc)
		if err != nil {
			return ocispecs.Descriptor{}, ocispecs.Manifest{}, err
		}
		var manifest ocispecs.Manifest
		if err := json.Unmarshal(p, &manifest); err != nil {
			return ocispecs.Descriptor{}, ocispecs.Manifest{}, err
		}
		if desc.Platform != nil && !matcher.Match(*desc.Platform) {
			return ocispecs.Descriptor{}, ocispecs.Manifest{}, fmt.Errorf("manifest %s does not match requested platform", desc.Digest)
		}
		if desc.Platform == nil {
			imagePlatform, err := images.ConfigPlatform(ctx, provider, manifest.Config)
			if err != nil {
				return ocispecs.Descriptor{}, ocispecs.Manifest{}, err
			}
			if !matcher.Match(imagePlatform) {
				return ocispecs.Descriptor{}, ocispecs.Manifest{}, fmt.Errorf("manifest %s does not match requested platform", desc.Digest)
			}
		}
		return desc, manifest, nil

	case images.IsIndexType(desc.MediaType):
		p, err := content.ReadBlob(ctx, provider, desc)
		if err != nil {
			return ocispecs.Descriptor{}, ocispecs.Manifest{}, err
		}
		var idx ocispecs.Index
		if err := json.Unmarshal(p, &idx); err != nil {
			return ocispecs.Descriptor{}, ocispecs.Manifest{}, err
		}
		candidates := make([]ocispecs.Descriptor, 0, len(idx.Manifests))
		for _, child := range idx.Manifests {
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
		if len(candidates) == 0 {
			return ocispecs.Descriptor{}, ocispecs.Manifest{}, fmt.Errorf("no manifest matches requested platform for %s", desc.Digest)
		}
		for _, candidate := range candidates {
			manifestDesc, manifest, err := resolveManifestDescriptor(ctx, provider, candidate, matcher)
			if err == nil {
				return manifestDesc, manifest, nil
			}
		}
		return ocispecs.Descriptor{}, ocispecs.Manifest{}, fmt.Errorf("no manifest matches requested platform for %s", desc.Digest)

	default:
		return ocispecs.Descriptor{}, ocispecs.Manifest{}, fmt.Errorf("unsupported descriptor media type %s", desc.MediaType)
	}
}

func hydratePulledDescriptor(desc ocispecs.Descriptor) ocispecs.Descriptor {
	if desc.Annotations != nil {
		desc.Annotations = maps.Clone(desc.Annotations)
	}
	if desc.URLs != nil {
		desc.URLs = slices.Clone(desc.URLs)
	}
	return desc
}

func (r *Resolver) pushRegistryHosts(insecure bool) docker.RegistryHosts {
	base := r.registryHosts()
	if !insecure {
		return base
	}
	return func(domain string) ([]docker.RegistryHost, error) {
		hosts, err := base(domain)
		if err != nil {
			return nil, err
		}
		for i := range hosts {
			hosts[i].Scheme = "http"
			if hosts[i].Client != nil {
				hosts[i].Client = &http.Client{
					Transport:     hosts[i].Client.Transport,
					CheckRedirect: hosts[i].Client.CheckRedirect,
					Jar:           hosts[i].Client.Jar,
					Timeout:       hosts[i].Client.Timeout,
				}
			}
		}
		return hosts, nil
	}
}

func collectManifestStack(ctx context.Context, provider content.Provider, rootDesc ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
	var stack []ocispecs.Descriptor
	var walk func(ocispecs.Descriptor) error
	walk = func(desc ocispecs.Descriptor) error {
		switch desc.MediaType {
		case images.MediaTypeDockerSchema2Manifest, ocispecs.MediaTypeImageManifest:
			stack = append(stack, desc)
			payload, err := content.ReadBlob(ctx, provider, desc)
			if err != nil {
				return err
			}
			var manifest ocispecs.Manifest
			if err := json.Unmarshal(payload, &manifest); err != nil {
				return err
			}
			return nil
		case images.MediaTypeDockerSchema2ManifestList, ocispecs.MediaTypeImageIndex:
			stack = append(stack, desc)
			payload, err := content.ReadBlob(ctx, provider, desc)
			if err != nil {
				return err
			}
			var index ocispecs.Index
			if err := json.Unmarshal(payload, &index); err != nil {
				return err
			}
			for _, child := range index.Manifests {
				if err := walk(child); err != nil {
					return err
				}
			}
			return nil
		default:
			return nil
		}
	}
	if err := walk(rootDesc); err != nil {
		return nil, err
	}
	return stack, nil
}

func limitedPushHandler(pusher remotes.Pusher, provider content.Provider, ref string) images.HandlerFunc {
	return func(ctx context.Context, desc ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
		cw, err := pusher.Push(ctx, desc)
		if err != nil {
			if errors.Is(err, cerrdefs.ErrAlreadyExists) {
				return nil, nil
			}
			return nil, err
		}
		defer cw.Close()
		ra, err := provider.ReaderAt(ctx, desc)
		if err != nil {
			return nil, err
		}
		defer ra.Close()
		if err := content.Copy(ctx, cw, content.NewReader(ra), desc.Size, desc.Digest); err != nil {
			if errors.Is(err, cerrdefs.ErrAlreadyExists) {
				return nil, nil
			}
			return nil, err
		}
		return nil, nil
	}
}

func skipNonDistributableBlobs(f images.HandlerFunc) images.HandlerFunc {
	return func(ctx context.Context, desc ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
		if images.IsNonDistributable(desc.MediaType) {
			bklog.G(ctx).WithField("digest", desc.Digest).WithField("mediatype", desc.MediaType).Debug("skipping non-distributable blob")
			return nil, images.ErrSkipDesc
		}
		return f(ctx, desc)
	}
}

func annotateDistributionSourceHandler(manager content.Manager, annotations map[digest.Digest]map[string]string, f images.HandlerFunc) images.HandlerFunc {
	return func(ctx context.Context, desc ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
		children, err := f(ctx, desc)
		if err != nil {
			return nil, err
		}

		switch desc.MediaType {
		case images.MediaTypeDockerSchema2Manifest, ocispecs.MediaTypeImageManifest,
			images.MediaTypeDockerSchema2ManifestList, ocispecs.MediaTypeImageIndex:
		default:
			return children, nil
		}

		for i := range children {
			child := children[i]
			for key, value := range annotations[child.Digest] {
				if !strings.HasPrefix(key, "containerd.io/distribution.source.") {
					continue
				}
				if child.Annotations == nil {
					child.Annotations = map[string]string{}
				}
				child.Annotations[key] = value
			}

			info, err := manager.Info(ctx, child.Digest)
			if errors.Is(err, cerrdefs.ErrNotFound) {
				children[i] = child
				continue
			}
			if err != nil {
				return nil, err
			}
			for key, value := range info.Labels {
				if !strings.HasPrefix(key, "containerd.io/distribution.source.") {
					continue
				}
				if child.Annotations == nil {
					child.Annotations = map[string]string{}
				}
				child.Annotations[key] = value
			}
			children[i] = child
		}
		return children, nil
	}
}

func imageChildrenHandler(provider content.Provider) images.HandlerFunc {
	return func(ctx context.Context, desc ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
		switch desc.MediaType {
		case images.MediaTypeDockerSchema2Manifest, ocispecs.MediaTypeImageManifest:
			payload, err := content.ReadBlob(ctx, provider, desc)
			if err != nil {
				return nil, err
			}
			var manifest ocispecs.Manifest
			if err := json.Unmarshal(payload, &manifest); err != nil {
				return nil, err
			}
			children := []ocispecs.Descriptor{manifest.Config}
			children = append(children, manifest.Layers...)
			return children, nil
		case images.MediaTypeDockerSchema2ManifestList, ocispecs.MediaTypeImageIndex:
			payload, err := content.ReadBlob(ctx, provider, desc)
			if err != nil {
				return nil, err
			}
			var index ocispecs.Index
			if err := json.Unmarshal(payload, &index); err != nil {
				return nil, err
			}
			return slices.Clone(index.Manifests), nil
		default:
			return nil, nil
		}
	}
}

func updateDistributionSourceHandler(manager content.Manager, pushF images.HandlerFunc, ref string) (images.HandlerFunc, error) {
	updateF, err := docker.AppendDistributionSourceLabel(manager, ref)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, desc ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
		children, err := pushF(ctx, desc)
		if err != nil {
			return nil, err
		}
		switch desc.MediaType {
		case images.MediaTypeDockerSchema2Layer, images.MediaTypeDockerSchema2LayerGzip,
			ocispecs.MediaTypeImageLayer, ocispecs.MediaTypeImageLayerGzip:
			if _, err := updateF(ctx, desc); err != nil {
				bklog.G(ctx).Warnf("failed to update distribution source for layer %v: %v", desc.Digest, err)
			}
		}
		return children, nil
	}, nil
}

func dedupeHandler(h images.HandlerFunc) images.HandlerFunc {
	var g flightcontrol.Group[[]ocispecs.Descriptor]
	res := map[digest.Digest][]ocispecs.Descriptor{}
	var mu sync.Mutex

	return func(ctx context.Context, desc ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
		return g.Do(ctx, desc.Digest.String(), func(ctx context.Context) ([]ocispecs.Descriptor, error) {
			mu.Lock()
			if children, ok := res[desc.Digest]; ok {
				mu.Unlock()
				return children, nil
			}
			mu.Unlock()

			children, err := h(ctx, desc)
			if err != nil {
				return nil, err
			}
			mu.Lock()
			res[desc.Digest] = children
			mu.Unlock()
			return children, nil
		})
	}
}
