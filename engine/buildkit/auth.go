package buildkit

import (
	"context"

	bkauth "github.com/moby/buildkit/session/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type authProxy struct {
	c *Client
}

func (p *authProxy) Register(srv *grpc.Server) {
	bkauth.RegisterAuthServer(srv, p)
}

// TODO: reduce boilerplate w/ generics?

func (p *authProxy) Credentials(ctx context.Context, req *bkauth.CredentialsRequest) (*bkauth.CredentialsResponse, error) {
	resp, err := p.c.AuthProvider.Credentials(ctx, req)
	if err == nil {
		return resp, nil
	}
	if status.Code(err) != codes.NotFound {
		return nil, err
	}
	if p.c.MainClientCaller == nil {
		return nil, status.Error(codes.NotFound, "no auth provider")
	}
	return bkauth.NewAuthClient(p.c.MainClientCaller.Conn()).Credentials(ctx, req)
}

func (p *authProxy) FetchToken(ctx context.Context, req *bkauth.FetchTokenRequest) (*bkauth.FetchTokenResponse, error) {
	resp, err := p.c.AuthProvider.FetchToken(ctx, req)
	if err == nil {
		return resp, nil
	}
	if status.Code(err) != codes.NotFound {
		return nil, err
	}
	if p.c.MainClientCaller == nil {
		return nil, status.Error(codes.NotFound, "no auth provider")
	}
	return bkauth.NewAuthClient(p.c.MainClientCaller.Conn()).FetchToken(ctx, req)
}

func (p *authProxy) GetTokenAuthority(ctx context.Context, req *bkauth.GetTokenAuthorityRequest) (*bkauth.GetTokenAuthorityResponse, error) {
	resp, err := p.c.AuthProvider.GetTokenAuthority(ctx, req)
	if err == nil {
		return resp, nil
	}
	if status.Code(err) != codes.NotFound {
		return nil, err
	}
	if p.c.MainClientCaller == nil {
		return nil, status.Error(codes.NotFound, "no auth provider")
	}
	return bkauth.NewAuthClient(p.c.MainClientCaller.Conn()).GetTokenAuthority(ctx, req)
}

func (p *authProxy) VerifyTokenAuthority(ctx context.Context, req *bkauth.VerifyTokenAuthorityRequest) (*bkauth.VerifyTokenAuthorityResponse, error) {
	resp, err := p.c.AuthProvider.VerifyTokenAuthority(ctx, req)
	if err == nil {
		return resp, nil
	}
	if status.Code(err) != codes.NotFound {
		return nil, err
	}
	if p.c.MainClientCaller == nil {
		return nil, status.Error(codes.NotFound, "no auth provider")
	}
	return bkauth.NewAuthClient(p.c.MainClientCaller.Conn()).VerifyTokenAuthority(ctx, req)
}
