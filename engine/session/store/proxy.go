package store

import (
	"context"
	"errors"
	"fmt"

	contentapi "github.com/containerd/containerd/api/services/content/v1"
	imagesapi "github.com/containerd/containerd/api/services/images/v1"
	leasesapi "github.com/containerd/containerd/api/services/leases/v1"
	"github.com/dagger/dagger/util/grpcutil"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
)

// StoreProxy registers proxies for all store-related services so they can be
// forwarded over a session (used for scale-out).
type StoreProxy struct {
	content ContentProxy
	images  ImagesProxy
	leases  LeasesProxy
	basic   BasicStoreProxy
}

func NewStoreProxy(conn *grpc.ClientConn) StoreProxy {
	return StoreProxy{
		content: NewContentProxy(contentapi.NewContentClient(conn)),
		images:  NewImagesProxy(imagesapi.NewImagesClient(conn)),
		leases:  NewLeasesProxy(leasesapi.NewLeasesClient(conn)),
		basic:   NewBasicStoreProxy(NewBasicStoreClient(conn)),
	}
}

func (p StoreProxy) Register(server *grpc.Server) {
	p.content.Register(server)
	p.images.Register(server)
	p.leases.Register(server)
	p.basic.Register(server)
}

type ContentProxy struct {
	client contentapi.ContentClient
	contentapi.UnimplementedContentServer
}

func NewContentProxy(client contentapi.ContentClient) ContentProxy {
	return ContentProxy{client: client}
}

func (p ContentProxy) Register(server *grpc.Server) {
	contentapi.RegisterContentServer(server, p)
}

func (p ContentProxy) Info(ctx context.Context, req *contentapi.InfoRequest) (*contentapi.InfoResponse, error) {
	return p.client.Info(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p ContentProxy) Update(ctx context.Context, req *contentapi.UpdateRequest) (*contentapi.UpdateResponse, error) {
	return p.client.Update(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p ContentProxy) List(req *contentapi.ListContentRequest, srv contentapi.Content_ListServer) error {
	ctx, cancel := context.WithCancelCause(srv.Context())
	defer cancel(errors.New("proxy stream closed"))

	clientStream, err := p.client.List(grpcutil.IncomingToOutgoingContext(ctx), req)
	if err != nil {
		return fmt.Errorf("create client stream: %w", err)
	}

	return grpcutil.ProxyStream[anypb.Any](ctx, clientStream, srv)
}

func (p ContentProxy) Delete(ctx context.Context, req *contentapi.DeleteContentRequest) (*emptypb.Empty, error) {
	return p.client.Delete(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p ContentProxy) Read(req *contentapi.ReadContentRequest, srv contentapi.Content_ReadServer) error {
	ctx, cancel := context.WithCancelCause(srv.Context())
	defer cancel(errors.New("proxy stream closed"))

	clientStream, err := p.client.Read(grpcutil.IncomingToOutgoingContext(ctx), req)
	if err != nil {
		return fmt.Errorf("create client stream: %w", err)
	}

	return grpcutil.ProxyStream[anypb.Any](ctx, clientStream, srv)
}

func (p ContentProxy) Status(ctx context.Context, req *contentapi.StatusRequest) (*contentapi.StatusResponse, error) {
	return p.client.Status(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p ContentProxy) ListStatuses(ctx context.Context, req *contentapi.ListStatusesRequest) (*contentapi.ListStatusesResponse, error) {
	return p.client.ListStatuses(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p ContentProxy) Write(srv contentapi.Content_WriteServer) error {
	ctx, cancel := context.WithCancelCause(srv.Context())
	defer cancel(errors.New("proxy stream closed"))

	clientStream, err := p.client.Write(grpcutil.IncomingToOutgoingContext(ctx))
	if err != nil {
		return fmt.Errorf("create client stream: %w", err)
	}

	return grpcutil.ProxyStream[anypb.Any](ctx, clientStream, srv)
}

func (p ContentProxy) Abort(ctx context.Context, req *contentapi.AbortRequest) (*emptypb.Empty, error) {
	return p.client.Abort(grpcutil.IncomingToOutgoingContext(ctx), req)
}

type ImagesProxy struct {
	client imagesapi.ImagesClient
	imagesapi.UnimplementedImagesServer
}

func NewImagesProxy(client imagesapi.ImagesClient) ImagesProxy {
	return ImagesProxy{client: client}
}

func (p ImagesProxy) Register(server *grpc.Server) {
	imagesapi.RegisterImagesServer(server, p)
}

func (p ImagesProxy) Get(ctx context.Context, req *imagesapi.GetImageRequest) (*imagesapi.GetImageResponse, error) {
	return p.client.Get(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p ImagesProxy) List(ctx context.Context, req *imagesapi.ListImagesRequest) (*imagesapi.ListImagesResponse, error) {
	return p.client.List(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p ImagesProxy) Create(ctx context.Context, req *imagesapi.CreateImageRequest) (*imagesapi.CreateImageResponse, error) {
	return p.client.Create(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p ImagesProxy) Update(ctx context.Context, req *imagesapi.UpdateImageRequest) (*imagesapi.UpdateImageResponse, error) {
	return p.client.Update(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p ImagesProxy) Delete(ctx context.Context, req *imagesapi.DeleteImageRequest) (*emptypb.Empty, error) {
	return p.client.Delete(grpcutil.IncomingToOutgoingContext(ctx), req)
}

type LeasesProxy struct {
	client leasesapi.LeasesClient
	leasesapi.UnimplementedLeasesServer
}

func NewLeasesProxy(client leasesapi.LeasesClient) LeasesProxy {
	return LeasesProxy{client: client}
}

func (p LeasesProxy) Register(server *grpc.Server) {
	leasesapi.RegisterLeasesServer(server, p)
}

func (p LeasesProxy) Create(ctx context.Context, req *leasesapi.CreateRequest) (*leasesapi.CreateResponse, error) {
	return p.client.Create(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p LeasesProxy) Delete(ctx context.Context, req *leasesapi.DeleteRequest) (*emptypb.Empty, error) {
	return p.client.Delete(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p LeasesProxy) List(ctx context.Context, req *leasesapi.ListRequest) (*leasesapi.ListResponse, error) {
	return p.client.List(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p LeasesProxy) AddResource(ctx context.Context, req *leasesapi.AddResourceRequest) (*emptypb.Empty, error) {
	return p.client.AddResource(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p LeasesProxy) DeleteResource(ctx context.Context, req *leasesapi.DeleteResourceRequest) (*emptypb.Empty, error) {
	return p.client.DeleteResource(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p LeasesProxy) ListResources(ctx context.Context, req *leasesapi.ListResourcesRequest) (*leasesapi.ListResourcesResponse, error) {
	return p.client.ListResources(grpcutil.IncomingToOutgoingContext(ctx), req)
}

type BasicStoreProxy struct {
	client BasicStoreClient
}

func NewBasicStoreProxy(client BasicStoreClient) BasicStoreProxy {
	return BasicStoreProxy{client: client}
}

func (p BasicStoreProxy) Register(server *grpc.Server) {
	RegisterBasicStoreServer(server, p)
}

func (p BasicStoreProxy) WriteTarball(srv BasicStore_WriteTarballServer) error {
	ctx, cancel := context.WithCancelCause(srv.Context())
	defer cancel(errors.New("proxy stream closed"))

	clientStream, err := p.client.WriteTarball(grpcutil.IncomingToOutgoingContext(ctx))
	if err != nil {
		return fmt.Errorf("create client stream: %w", err)
	}

	return grpcutil.ProxyStream[anypb.Any](ctx, clientStream, srv)
}

func (p BasicStoreProxy) ReadTarball(req *emptypb.Empty, srv BasicStore_ReadTarballServer) error {
	ctx, cancel := context.WithCancelCause(srv.Context())
	defer cancel(errors.New("proxy stream closed"))

	clientStream, err := p.client.ReadTarball(grpcutil.IncomingToOutgoingContext(ctx), req)
	if err != nil {
		return fmt.Errorf("create client stream: %w", err)
	}

	return grpcutil.ProxyStream[anypb.Any](ctx, clientStream, srv)
}
