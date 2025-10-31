package store

import (
	context "context"
	"errors"
	io "io"

	contentapi "github.com/containerd/containerd/api/services/content/v1"
	imagesapi "github.com/containerd/containerd/api/services/images/v1"
	leasesapi "github.com/containerd/containerd/api/services/leases/v1"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/pkg/epoch"
	"github.com/containerd/containerd/v2/plugins/services/content/contentserver"
	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/internal/buildkit/session"
	grpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/containerd/errdefs/pkg/errgrpc"
)

func NewImageLoaderAttachable(loader *imageload.Loader) (session.Attachable, error) {
	if loader == nil {
		return nil, errors.New("cannot attach nil loader")
	}

	switch {
	case loader.ContentStore != nil && loader.ImagesStore != nil:
		content := contentserver.New(loader.ContentStore)
		images := &imagesServer{store: loader.ImagesStore}
		leases := &leasesServer{leases: loader.LeaseManager}

		a := storeLoaderAttachable{
			content: content,
			images:  images,
			leases:  leases,
			cleanup: loader.Close,
		}
		return &a, nil
	case loader.TarballWriter != nil || loader.TarballReader != nil:
		return basicStoreAttachable{
			write: loader.TarballWriter,
			read:  loader.TarballReader,
		}, nil
	default:
		return nil, errors.New("invalid loader")
	}
}

type storeLoaderAttachable struct {
	content contentapi.ContentServer
	images  imagesapi.ImagesServer
	leases  leasesapi.LeasesServer
	cleanup func() error
}

func (attachable storeLoaderAttachable) Register(srv *grpc.Server) {
	contentapi.RegisterContentServer(srv, attachable.content)
	imagesapi.RegisterImagesServer(srv, attachable.images)
	leasesapi.RegisterLeasesServer(srv, attachable.leases)
}

var _ io.Closer = storeLoaderAttachable{}

func (attachable storeLoaderAttachable) Close() error {
	if attachable.cleanup == nil {
		return nil
	}
	return attachable.cleanup()
}

// imagesServer is provided, since no upstream impl exists
type imagesServer struct {
	store images.Store

	imagesapi.UnimplementedImagesServer
}

var _ imagesapi.ImagesServer = &imagesServer{}

func (l *imagesServer) Get(ctx context.Context, req *imagesapi.GetImageRequest) (*imagesapi.GetImageResponse, error) {
	image, err := l.store.Get(ctx, req.Name)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}

	imagepb := imageToProto(&image)
	return &imagesapi.GetImageResponse{
		Image: imagepb,
	}, nil
}

func (l *imagesServer) List(ctx context.Context, req *imagesapi.ListImagesRequest) (*imagesapi.ListImagesResponse, error) {
	images, err := l.store.List(ctx, req.Filters...)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}

	return &imagesapi.ListImagesResponse{
		Images: imagesToProto(images),
	}, nil
}

func (l *imagesServer) Create(ctx context.Context, req *imagesapi.CreateImageRequest) (*imagesapi.CreateImageResponse, error) {
	if req.Image.Name == "" {
		return nil, status.Errorf(codes.InvalidArgument, "Image.Name required")
	}

	if req.SourceDateEpoch != nil {
		tm := req.SourceDateEpoch.AsTime()
		ctx = epoch.WithSourceDateEpoch(ctx, &tm)
	}

	image := imageFromProto(req.Image)
	created, err := l.store.Create(ctx, image)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}

	return &imagesapi.CreateImageResponse{
		Image: imageToProto(&created),
	}, nil
}

func (l *imagesServer) Update(ctx context.Context, req *imagesapi.UpdateImageRequest) (*imagesapi.UpdateImageResponse, error) {
	if req.Image.Name == "" {
		return nil, status.Errorf(codes.InvalidArgument, "Image.Name required")
	}

	var fieldpaths []string
	if req.UpdateMask != nil && len(req.UpdateMask.Paths) > 0 {
		fieldpaths = append(fieldpaths, req.UpdateMask.Paths...)
	}

	if req.SourceDateEpoch != nil {
		tm := req.SourceDateEpoch.AsTime()
		ctx = epoch.WithSourceDateEpoch(ctx, &tm)
	}

	image := imageFromProto(req.Image)
	updated, err := l.store.Update(ctx, image, fieldpaths...)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}

	return &imagesapi.UpdateImageResponse{
		Image: imageToProto(&updated),
	}, nil
}

func (l *imagesServer) Delete(ctx context.Context, req *imagesapi.DeleteImageRequest) (*emptypb.Empty, error) {
	if err := l.store.Delete(ctx, req.Name); err != nil {
		return nil, errgrpc.ToGRPC(err)
	}

	return &emptypb.Empty{}, nil
}

type leasesServer struct {
	leases leases.Manager

	leasesapi.UnimplementedLeasesServer
}

var _ leasesapi.LeasesServer = &leasesServer{}

func (s *leasesServer) Create(ctx context.Context, req *leasesapi.CreateRequest) (*leasesapi.CreateResponse, error) {
	var opts []leases.Opt
	opts = append(opts, leases.WithID(req.ID))
	opts = append(opts, leases.WithLabels(req.Labels))

	lease, err := s.leases.Create(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return &leasesapi.CreateResponse{
		Lease: leaseToProto(lease),
	}, nil
}

func (s *leasesServer) Delete(ctx context.Context, req *leasesapi.DeleteRequest) (*emptypb.Empty, error) {
	var opts []leases.DeleteOpt
	if req.Sync {
		opts = append(opts, leases.SynchronousDelete)
	}

	lease := leases.Lease{ID: req.ID}
	err := s.leases.Delete(ctx, lease, opts...)
	if err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}

func (s *leasesServer) List(ctx context.Context, req *leasesapi.ListRequest) (*leasesapi.ListResponse, error) {
	result, err := s.leases.List(ctx, req.Filters...)
	if err != nil {
		return nil, err
	}

	protoLeases := make([]*leasesapi.Lease, len(result))
	for i, lease := range result {
		protoLeases[i] = leaseToProto(lease)
	}

	return &leasesapi.ListResponse{
		Leases: protoLeases,
	}, nil
}

func (s *leasesServer) AddResource(ctx context.Context, req *leasesapi.AddResourceRequest) (*emptypb.Empty, error) {
	lease := leases.Lease{ID: req.ID}
	resource := protoToResource(req.Resource)

	err := s.leases.AddResource(ctx, lease, resource)
	if err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}

func (s *leasesServer) DeleteResource(ctx context.Context, req *leasesapi.DeleteResourceRequest) (*emptypb.Empty, error) {
	lease := leases.Lease{ID: req.ID}
	resource := protoToResource(req.Resource)

	err := s.leases.DeleteResource(ctx, lease, resource)
	if err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}

func (s *leasesServer) ListResources(ctx context.Context, req *leasesapi.ListResourcesRequest) (*leasesapi.ListResourcesResponse, error) {
	lease := leases.Lease{ID: req.ID}
	resources, err := s.leases.ListResources(ctx, lease)
	if err != nil {
		return nil, err
	}

	protoResources := make([]*leasesapi.Resource, len(resources))
	for i, resource := range resources {
		protoResources[i] = resourceToProto(resource)
	}

	return &leasesapi.ListResourcesResponse{
		Resources: protoResources,
	}, nil
}
