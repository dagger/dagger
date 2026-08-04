package git

import (
	context "context"
	"errors"
	"fmt"
	"sync"

	"github.com/dagger/dagger/util/grpcutil"
	grpc "google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"
)

var gitMutex sync.Mutex

type GitAttachable struct {
	rootCtx context.Context

	UnimplementedGitServer
}

func NewGitAttachable(rootCtx context.Context) GitAttachable {
	return GitAttachable{
		rootCtx: rootCtx,
	}
}

func (s GitAttachable) Register(srv *grpc.Server) {
	RegisterGitServer(srv, &s)
}

type GitAttachableProxy struct {
	client GitClient
}

func NewGitAttachableProxy(client GitClient) GitAttachableProxy {
	return GitAttachableProxy{client: client}
}

func (p GitAttachableProxy) Register(server *grpc.Server) {
	RegisterGitServer(server, p)
}

func (p GitAttachableProxy) GetCredential(ctx context.Context, req *GitCredentialRequest) (*GitCredentialResponse, error) {
	return p.client.GetCredential(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p GitAttachableProxy) GetConfig(ctx context.Context, req *GitConfigRequest) (*GitConfigResponse, error) {
	return p.client.GetConfig(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p GitAttachableProxy) GetHead(ctx context.Context, req *GitHeadRequest) (*GitHeadResponse, error) {
	return p.client.GetHead(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p GitAttachableProxy) ApplyBundle(srv Git_ApplyBundleServer) error {
	ctx, cancel := context.WithCancelCause(srv.Context())
	defer cancel(errors.New("proxy stream closed"))

	clientStream, err := p.client.ApplyBundle(grpcutil.IncomingToOutgoingContext(ctx))
	if err != nil {
		return fmt.Errorf("create client stream: %w", err)
	}

	return grpcutil.ProxyStream[anypb.Any](ctx, clientStream, srv)
}

func (p GitAttachableProxy) CheckoutState(ctx context.Context, req *CheckoutStateRequest) (*CheckoutStateResponse, error) {
	return p.client.CheckoutState(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p GitAttachableProxy) PackCheckout(req *PackCheckoutRequest, srv Git_PackCheckoutServer) error {
	ctx, cancel := context.WithCancelCause(srv.Context())
	defer cancel(errors.New("proxy stream closed"))

	clientStream, err := p.client.PackCheckout(grpcutil.IncomingToOutgoingContext(ctx), req)
	if err != nil {
		return fmt.Errorf("create client stream: %w", err)
	}

	return grpcutil.ProxyStream[anypb.Any](ctx, clientStream, srv)
}
