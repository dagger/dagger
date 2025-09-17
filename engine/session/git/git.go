package git

import (
	context "context"
	"sync"

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

	// dagger/dagger#9323 renamed the GitCredential attachable to Git
	// it's easy to provide a fallback
	// TODO: simplify when we break client<->server compat
	serviceDesc := _Git_serviceDesc
	serviceDesc.ServiceName = "GitCredential"
	srv.RegisterService(&serviceDesc, &s)
}
