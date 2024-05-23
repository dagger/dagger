package server

import (
	"context"
	"fmt"

	bksession "github.com/moby/buildkit/session"
	bkauth "github.com/moby/buildkit/session/auth"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type authProxy struct {
	c                *daggerClient
	bkSessionManager *bksession.Manager
}

func (p *authProxy) Register(srv *grpc.Server) {
	bkauth.RegisterAuthServer(srv, p)
}

// TODO: reduce boilerplate w/ generics?

func (p *authProxy) Credentials(ctx context.Context, req *bkauth.CredentialsRequest) (*bkauth.CredentialsResponse, error) {
	ctx = trace.ContextWithSpanContext(ctx, p.c.spanCtx) // ensure server's span context is propagated
	resp, err := p.c.daggerSession.authProvider.Credentials(ctx, req)
	if err == nil {
		return resp, nil
	}
	if status.Code(err) != codes.NotFound {
		return nil, err
	}
	caller, err := p.c.getMainClientCaller()
	if err != nil {
		return nil, fmt.Errorf("failed to get main client caller: %w", err)
	}

	resp, err = bkauth.NewAuthClient(caller.Conn()).Credentials(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get credentials: %w", err)
	}
	return resp, nil
}

func (p *authProxy) FetchToken(ctx context.Context, req *bkauth.FetchTokenRequest) (*bkauth.FetchTokenResponse, error) {
	ctx = trace.ContextWithSpanContext(ctx, p.c.spanCtx) // ensure server's span context is propagated
	resp, err := p.c.daggerSession.authProvider.FetchToken(ctx, req)
	if err == nil {
		return resp, nil
	}
	if status.Code(err) != codes.NotFound {
		return nil, err
	}
	caller, err := p.c.getMainClientCaller()
	if err != nil {
		return nil, fmt.Errorf("failed to get main client caller: %w", err)
	}
	resp, err = bkauth.NewAuthClient(caller.Conn()).FetchToken(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch token: %w", err)
	}
	return resp, nil
}

func (p *authProxy) GetTokenAuthority(ctx context.Context, req *bkauth.GetTokenAuthorityRequest) (*bkauth.GetTokenAuthorityResponse, error) {
	ctx = trace.ContextWithSpanContext(ctx, p.c.spanCtx) // ensure server's span context is propagated
	resp, err := p.c.daggerSession.authProvider.GetTokenAuthority(ctx, req)
	if err == nil {
		return resp, nil
	}
	if status.Code(err) != codes.NotFound {
		return nil, err
	}
	caller, err := p.c.getMainClientCaller()
	if err != nil {
		return nil, fmt.Errorf("failed to get main client caller: %w", err)
	}
	resp, err = bkauth.NewAuthClient(caller.Conn()).GetTokenAuthority(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get token authority: %w", err)
	}
	return resp, nil
}

func (p *authProxy) VerifyTokenAuthority(ctx context.Context, req *bkauth.VerifyTokenAuthorityRequest) (*bkauth.VerifyTokenAuthorityResponse, error) {
	ctx = trace.ContextWithSpanContext(ctx, p.c.spanCtx) // ensure server's span context is propagated
	resp, err := p.c.daggerSession.authProvider.VerifyTokenAuthority(ctx, req)
	if err == nil {
		return resp, nil
	}
	if status.Code(err) != codes.NotFound {
		return nil, err
	}
	caller, err := p.c.getMainClientCaller()
	if err != nil {
		return nil, fmt.Errorf("failed to get main client caller: %w", err)
	}
	resp, err = bkauth.NewAuthClient(caller.Conn()).VerifyTokenAuthority(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to verify token authority: %w", err)
	}
	return resp, nil
}
