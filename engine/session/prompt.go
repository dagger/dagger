package session

import (
	context "context"
	"sync"
	"time"

	grpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

var promptMutex sync.Mutex

type PromptAttachable struct {
	rootCtx context.Context

	UnimplementedGitCredentialServer
}

func NewPromptAttachable(rootCtx context.Context) PromptAttachable {
	return PromptAttachable{
		rootCtx: rootCtx,
	}
}

func (p PromptAttachable) Register(srv *grpc.Server) {
	RegisterPromptServer(srv, p)
}

func (s PromptAttachable) Prompt(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	if req.Prompt == "" {
		return nil, status.Errorf(codes.InvalidArgument, "Invalid input: prompt is required")
	}

	// Ensure we're displaying 1 prompt at a time
	promptMutex.Lock()
	defer promptMutex.Unlock()

	// TODO: @vito: actually prompt the user via the frontend

	return &PromptResponse{
		Input: "no",
	}, nil
}
