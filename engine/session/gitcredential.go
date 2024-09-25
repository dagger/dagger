package session

import (
	context "context"

	grpc "google.golang.org/grpc"
)

type GitCredentialAttachable struct {
	rootCtx context.Context

	UnimplementedGitCredentialServer
}

func NewGitCredentialAttachable(rootCtx context.Context) GitCredentialAttachable {
	return GitCredentialAttachable{
		rootCtx: rootCtx,
	}
}

func (s GitCredentialAttachable) Register(srv *grpc.Server) {
	RegisterGitCredentialServer(srv, s)
}

func (s GitCredentialAttachable) GetCredential(ctx context.Context, req *GitCredentialRequest) (*GitCredentialResponse, error) {
	// Implement the logic to get the credential
	// This is just a placeholder implementation
	return &GitCredentialResponse{
		Result: &GitCredentialResponse_Credential{
			Credential: &CredentialInfo{
				Protocol: req.Protocol,
				Host:     req.Host,
				// Fill in username and password as needed
			},
		},
	}, nil
}
