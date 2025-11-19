package client

import (
	"context"

	"google.golang.org/grpc"

	"github.com/dagger/dagger/internal/buildkit/session/auth"
	"github.com/dagger/dagger/util/grpcutil"
)

type AuthProxy struct {
	client auth.AuthClient
}

func NewAuthProxy(client auth.AuthClient) AuthProxy {
	return AuthProxy{client: client}
}

func (p AuthProxy) Register(server *grpc.Server) {
	auth.RegisterAuthServer(server, p)
}

func (p AuthProxy) FetchToken(ctx context.Context, req *auth.FetchTokenRequest) (*auth.FetchTokenResponse, error) {
	return p.client.FetchToken(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p AuthProxy) Credentials(ctx context.Context, req *auth.CredentialsRequest) (*auth.CredentialsResponse, error) {
	return p.client.Credentials(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p AuthProxy) GetTokenAuthority(ctx context.Context, req *auth.GetTokenAuthorityRequest) (*auth.GetTokenAuthorityResponse, error) {
	return p.client.GetTokenAuthority(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p AuthProxy) VerifyTokenAuthority(ctx context.Context, req *auth.VerifyTokenAuthorityRequest) (*auth.VerifyTokenAuthorityResponse, error) {
	return p.client.VerifyTokenAuthority(grpcutil.IncomingToOutgoingContext(ctx), req)
}
