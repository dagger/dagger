package git

import (
	context "context"
	"sync"

	"github.com/dagger/dagger/util/grpcutil"
	grpc "google.golang.org/grpc"
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
