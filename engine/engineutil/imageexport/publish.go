package imageexport

import (
	"context"
	"errors"
	"fmt"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	cerrdefs "github.com/containerd/errdefs"
	cache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/util/contentutil"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

type PushOpts struct {
	PushByDigest bool
	Insecure     bool
}

type RegistryPusher interface {
	PushImage(
		ctx context.Context,
		img *ExportedImage,
		ref string,
		opts PushOpts,
	) error
}

type Deps struct {
	Images       images.Store
	ContentStore content.Store
	LeaseManager leases.Manager
	Writer       *Writer
	Pusher       RegistryPusher
}

type ExportOpts struct {
	Names          []string
	NameCanonical  bool
	DanglingPrefix string

	Push         bool
	PushByDigest bool
	Store        bool
	Insecure     bool

	Commit CommitOpts
}

type ExportResponse struct {
	RootDesc   ocispecs.Descriptor
	Platforms  []ExportedPlatform
	ImageNames []string
}

func Export(
	ctx context.Context,
	deps Deps,
	req *ExportRequest,
	opts ExportOpts,
) (*ExportResponse, error) {
	img, err := deps.Writer.Assemble(ctx, req, opts.Commit)
	if err != nil {
		return nil, err
	}

	names := append([]string(nil), opts.Names...)
	if len(names) == 0 && opts.DanglingPrefix != "" {
		names = append(names, opts.DanglingPrefix+"@"+img.RootDesc.Digest.String())
	}

	appliedNames := make([]string, 0, len(names))
	for _, name := range names {
		if opts.Store {
			if deps.Images == nil || deps.ContentStore == nil || deps.LeaseManager == nil {
				return nil, errors.New("image export store dependencies are not initialized")
			}
			if err := exportToImageStore(ctx, deps, name, img, opts.NameCanonical); err != nil {
				return nil, err
			}
		}
		if opts.Push {
			if deps.Pusher == nil {
				return nil, errors.New("image export registry pusher is not initialized")
			}
			if err := deps.Pusher.PushImage(ctx, img, name, PushOpts{
				PushByDigest: opts.PushByDigest,
				Insecure:     opts.Insecure,
			}); err != nil {
				return nil, err
			}
		}

		appliedNames = append(appliedNames, name)
		if opts.NameCanonical {
			appliedNames = append(appliedNames, name+"@"+img.RootDesc.Digest.String())
		}
	}

	return &ExportResponse{
		RootDesc:   img.RootDesc,
		Platforms:  img.Platforms,
		ImageNames: appliedNames,
	}, nil
}

func exportToImageStore(
	ctx context.Context,
	deps Deps,
	imageName string,
	img *ExportedImage,
	nameCanonical bool,
) error {
	leaseCtx, done, err := cache.WithLease(ctx, deps.LeaseManager, cache.MakeTemporary)
	if err != nil {
		return err
	}
	defer done(context.WithoutCancel(leaseCtx))

	if err := contentutil.CopyChain(leaseCtx, deps.ContentStore, img.Provider, img.RootDesc); err != nil {
		return err
	}

	handler := images.ChildrenHandler(deps.ContentStore)
	handler = images.SetChildrenMappedLabels(deps.ContentStore, handler, images.ChildGCLabels)
	if err := images.WalkNotEmpty(leaseCtx, handler, img.RootDesc); err != nil {
		return err
	}

	names := []string{imageName}
	if nameCanonical {
		names = append(names, imageName+"@"+img.RootDesc.Digest.String())
	}
	for _, name := range names {
		record := images.Image{
			Name:   name,
			Target: img.RootDesc,
		}
		if _, err := deps.Images.Update(leaseCtx, record); err != nil {
			if !errors.Is(err, cerrdefs.ErrNotFound) {
				return fmt.Errorf("update image record %q: %w", name, err)
			}
			if _, err := deps.Images.Create(leaseCtx, record); err != nil {
				return fmt.Errorf("create image record %q: %w", name, err)
			}
		}
	}
	return nil
}
