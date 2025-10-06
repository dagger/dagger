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
}
