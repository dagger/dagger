package buildkit

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/pkg/reference"
	"github.com/containerd/platforms"
	"github.com/dagger/dagger/engine/buildkit/exporter"
	imageexporter "github.com/dagger/dagger/engine/buildkit/exporter/containerimage"
	ociexporter "github.com/dagger/dagger/engine/buildkit/exporter/oci"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/client/llb/sourceresolver"
	"github.com/dagger/dagger/internal/buildkit/session"
	sessioncontent "github.com/dagger/dagger/internal/buildkit/session/content"
	containerdsnapshot "github.com/dagger/dagger/internal/buildkit/snapshot/containerd"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/util/imageutil"
	"github.com/dagger/dagger/internal/buildkit/util/iohelper"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	"github.com/dagger/dagger/internal/buildkit/util/resolver"
	"github.com/dagger/dagger/internal/buildkit/util/tracing"
	"github.com/hashicorp/go-multierror"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func (w *Worker) Close() error {
	var rerr error
	for _, provider := range w.NetworkProviders {
		if err := provider.Close(); err != nil {
			rerr = multierror.Append(rerr, err)
		}
	}
	return rerr
}

func (w *Worker) ContentStore() *containerdsnapshot.Store {
	return w.WorkerOpt.ContentStore
}

func (w *Worker) LeaseManager() *leaseutil.Manager {
	return w.WorkerOpt.LeaseManager
}

func (w *Worker) ID() string {
	return w.WorkerOpt.ID
}

func (w *Worker) Labels() map[string]string {
	return w.WorkerOpt.Labels
}

func (w *Worker) Platforms(_ bool) []ocispecs.Platform {
	return w.WorkerOpt.Platforms
}

func (w *Worker) BuildkitVersion() bkclient.BuildkitVersion {
	return w.WorkerOpt.BuildkitVersion
}

func (w *Worker) GCPolicy() []bkclient.PruneInfo {
	return w.WorkerOpt.GCPolicy
}

func (w *Worker) ResolveSourceMetadata(ctx context.Context, op *pb.SourceOp, opt sourceresolver.Opt, sm *session.Manager, g session.Group) (*sourceresolver.MetaResponse, error) {
	if opt.SourcePolicies != nil {
		return nil, errors.New("source policies can not be set for worker")
	}

	scheme, ref, ok := strings.Cut(op.Identifier, "://")
	if !ok {
		return nil, errors.Errorf("failed to parse source identifier %q", op.Identifier)
	}

	switch scheme {
	case "docker-image":
		if opt.ImageOpt == nil {
			opt.ImageOpt = &sourceresolver.ResolveImageOpt{}
		}
		dgst, config, err := w.resolveRegistryImageConfig(ctx, ref, opt, sm, g)
		if err != nil {
			return nil, err
		}
		return &sourceresolver.MetaResponse{
			Op: op,
			Image: &sourceresolver.ResolveImageResponse{
				Digest: dgst,
				Config: config,
			},
		}, nil
	case "oci-layout":
		opt.OCILayoutOpt = &sourceresolver.ResolveOCILayoutOpt{
			Store: sourceresolver.ResolveImageConfigOptStore{
				StoreID:   op.Attrs[pb.AttrOCILayoutStoreID],
				SessionID: op.Attrs[pb.AttrOCILayoutSessionID],
			},
		}
		dgst, config, err := w.resolveOCILayoutImageConfig(ctx, ref, opt, sm, g)
		if err != nil {
			return nil, err
		}
		return &sourceresolver.MetaResponse{
			Op: op,
			Image: &sourceresolver.ResolveImageResponse{
				Digest: dgst,
				Config: config,
			},
		}, nil
	default:
		return &sourceresolver.MetaResponse{
			Op: op,
		}, nil
	}
}

func (w *Worker) DiskUsage(ctx context.Context, opt bkclient.DiskUsageInfo) ([]*bkclient.UsageInfo, error) {
	// TODO: remove method after switch to dagql native pruning
	// return w.CacheMgr.DiskUsage(ctx, opt)
	return nil, nil
}

func (w *Worker) Prune(ctx context.Context, ch chan bkclient.UsageInfo, opt ...bkclient.PruneInfo) error {
	// TODO: remove method after switch to dagql native pruning
	// return w.CacheMgr.Prune(ctx, ch, opt...)
	return nil
}

func (w *Worker) Exporter(name string, sm *session.Manager) (exporter.Exporter, error) {
	switch name {
	case bkclient.ExporterImage:
		return imageexporter.New(imageexporter.Opt{
			Images:         w.ImageStore,
			SessionManager: sm,
			ImageWriter:    w.imageWriter,
			RegistryHosts:  w.RegistryHosts,
			LeaseManager:   w.LeaseManager(),
		})
	case bkclient.ExporterOCI:
		return ociexporter.New(ociexporter.Opt{
			SessionManager: sm,
			ImageWriter:    w.imageWriter,
			Variant:        ociexporter.VariantOCI,
			LeaseManager:   w.LeaseManager(),
		})
	case bkclient.ExporterDocker:
		return ociexporter.New(ociexporter.Opt{
			SessionManager: sm,
			ImageWriter:    w.imageWriter,
			Variant:        ociexporter.VariantDocker,
			LeaseManager:   w.LeaseManager(),
		})
	case bkclient.ExporterLocal, bkclient.ExporterTar:
		return nil, errors.Errorf("exporter %q is not supported in engine buildkit worker", name)
	default:
		return nil, errors.Errorf("exporter %q could not be found", name)
	}
}

func (w *Worker) resolveRegistryImageConfig(ctx context.Context, ref string, opt sourceresolver.Opt, sm *session.Manager, g session.Group) (dgst digest.Digest, config []byte, retErr error) {
	span, ctx := tracing.StartSpan(ctx, "resolving "+ref)
	defer func() {
		tracing.FinishWithError(span, retErr)
	}()

	imageOpt := opt.ImageOpt
	if imageOpt == nil {
		return "", nil, errors.Errorf("missing imageopt for resolve")
	}

	resolveMode, err := resolver.ParseImageResolveMode(imageOpt.ResolveMode)
	if err != nil {
		return "", nil, err
	}

	key := ref
	if platform := opt.Platform; platform != nil {
		key += platforms.Format(*platform)
	}
	key += resolveMode.String()

	imageResolver := resolver.DefaultPool.GetResolver(w.RegistryHosts, ref, "pull", sm, g).WithImageStore(w.ImageStore, resolveMode)
	result, err := w.registryResolveImageConfigG.Do(ctx, key, func(ctx context.Context) (*resolveImageResult, error) {
		dgst, config, err := imageutil.Config(ctx, ref, imageResolver, w.ContentStore(), w.LeaseManager(), opt.Platform)
		if err != nil {
			return nil, err
		}
		return &resolveImageResult{digest: dgst, config: config}, nil
	})
	if err != nil {
		return "", nil, err
	}

	return result.digest, result.config, nil
}

func (w *Worker) resolveOCILayoutImageConfig(ctx context.Context, ref string, opt sourceresolver.Opt, sm *session.Manager, g session.Group) (dgst digest.Digest, config []byte, retErr error) {
	span, ctx := tracing.StartSpan(ctx, "resolving "+ref)
	defer func() {
		tracing.FinishWithError(span, retErr)
	}()

	ociLayoutOpt := opt.OCILayoutOpt
	if ociLayoutOpt == nil {
		return "", nil, errors.Errorf("missing ocilayoutopt for resolve")
	}

	resolveMode := resolver.ResolveModeForcePull

	key := ref
	if platform := opt.Platform; platform != nil {
		key += platforms.Format(*platform)
	}
	key += resolveMode.String()

	imageResolver := &ociLayoutResolver{
		store: ociLayoutOpt.Store,
		sm:    sm,
		g:     g,
	}
	result, err := w.ociLayoutResolveImageConfigG.Do(ctx, key, func(ctx context.Context) (*resolveImageResult, error) {
		dgst, config, err := imageutil.Config(ctx, ref, imageResolver, w.ContentStore(), w.LeaseManager(), opt.Platform)
		if err != nil {
			return nil, err
		}
		return &resolveImageResult{digest: dgst, config: config}, nil
	})
	if err != nil {
		return "", nil, err
	}

	return result.digest, result.config, nil
}

type resolveImageResult struct {
	digest digest.Digest
	config []byte
}

const maxReadSize = 4 * 1024 * 1024

type ociLayoutResolver struct {
	remotes.Resolver
	store sourceresolver.ResolveImageConfigOptStore
	sm    *session.Manager
	g     session.Group
}

func (r *ociLayoutResolver) Fetcher(context.Context, string) (remotes.Fetcher, error) {
	return r, nil
}

func (r *ociLayoutResolver) Fetch(ctx context.Context, desc ocispecs.Descriptor) (io.ReadCloser, error) {
	var rc io.ReadCloser
	err := r.withCaller(ctx, func(ctx context.Context, caller session.Caller) error {
		store := sessioncontent.NewCallerStore(caller, "oci:"+r.store.StoreID)
		readerAt, err := store.ReaderAt(ctx, desc)
		if err != nil {
			return err
		}
		rc = iohelper.ReadCloser(readerAt)
		return nil
	})
	return rc, err
}

func (r *ociLayoutResolver) Resolve(ctx context.Context, refString string) (string, ocispecs.Descriptor, error) {
	ref, err := reference.Parse(refString)
	if err != nil {
		return "", ocispecs.Descriptor{}, errors.Wrapf(err, "invalid reference %q", refString)
	}

	dgst := ref.Digest()
	if dgst == "" {
		return "", ocispecs.Descriptor{}, errors.Errorf("reference %q must have digest", refString)
	}

	info, err := r.info(ctx, ref)
	if err != nil {
		return "", ocispecs.Descriptor{}, errors.Wrap(err, "unable to get info about digest")
	}

	desc := ocispecs.Descriptor{
		Digest: info.Digest,
		Size:   info.Size,
	}
	rc, err := r.Fetch(ctx, desc)
	if err != nil {
		return "", ocispecs.Descriptor{}, errors.Wrap(err, "unable to get root manifest")
	}
	b, err := io.ReadAll(io.LimitReader(rc, maxReadSize))
	if err != nil {
		return "", ocispecs.Descriptor{}, errors.Wrap(err, "unable to read root manifest")
	}
	mediaType, err := imageutil.DetectManifestBlobMediaType(b)
	if err != nil {
		return "", ocispecs.Descriptor{}, errors.Wrapf(err, "reference %q contains neither an index nor a manifest", refString)
	}
	desc.MediaType = mediaType
	return refString, desc, nil
}

func (r *ociLayoutResolver) info(ctx context.Context, ref reference.Spec) (content.Info, error) {
	var info *content.Info
	err := r.withCaller(ctx, func(ctx context.Context, caller session.Caller) error {
		store := sessioncontent.NewCallerStore(caller, "oci:"+r.store.StoreID)

		dgst := ref.Digest()
		if dgst == "" {
			return errors.Errorf("reference %q does not contain a digest", ref.String())
		}

		in, err := store.Info(ctx, dgst)
		info = &in
		return err
	})
	if err != nil {
		return content.Info{}, err
	}
	if info == nil {
		return content.Info{}, errors.Errorf("reference %q did not match any content", ref.String())
	}
	return *info, nil
}

func (r *ociLayoutResolver) withCaller(ctx context.Context, f func(context.Context, session.Caller) error) error {
	if r.store.SessionID != "" {
		timeoutCtx, cancel := context.WithCancelCause(ctx)
		timeoutCtx, _ = context.WithTimeoutCause(timeoutCtx, 5*time.Second, errors.WithStack(context.DeadlineExceeded))
		defer cancel(errors.WithStack(context.Canceled))

		caller, err := r.sm.Get(timeoutCtx, r.store.SessionID, false)
		if err != nil {
			return err
		}
		return f(ctx, caller)
	}

	return r.sm.Any(ctx, r.g, func(ctx context.Context, _ string, caller session.Caller) error {
		return f(ctx, caller)
	})
}
