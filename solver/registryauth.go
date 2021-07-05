package solver

import (
	"context"
	"strings"
	"sync"

	"github.com/docker/distribution/reference"
	bkauth "github.com/moby/buildkit/session/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RegistryAuthProvider is a buildkit provider for registry authentication
// Adapted from: https://github.com/moby/buildkit/blob/master/session/auth/authprovider/authprovider.go
type RegistryAuthProvider struct {
	credentials map[string]*bkauth.CredentialsResponse
	m           sync.RWMutex
}

func NewRegistryAuthProvider() *RegistryAuthProvider {
	return &RegistryAuthProvider{
		credentials: map[string]*bkauth.CredentialsResponse{},
	}
}

func (a *RegistryAuthProvider) AddCredentials(target, username, secret string) {
	a.m.Lock()
	defer a.m.Unlock()

	a.credentials[target] = &bkauth.CredentialsResponse{
		Username: username,
		Secret:   secret,
	}
}

func (a *RegistryAuthProvider) Register(server *grpc.Server) {
	bkauth.RegisterAuthServer(server, a)
}

func (a *RegistryAuthProvider) Credentials(ctx context.Context, req *bkauth.CredentialsRequest) (*bkauth.CredentialsResponse, error) {
	host := req.Host
	if host == "registry-1.docker.io" {
		host = "docker.io"
	}

	a.m.RLock()
	defer a.m.RUnlock()

	for authHost, auth := range a.credentials {
		u, err := parseAuthHost(authHost)
		if err != nil {
			return nil, err
		}

		if u == host {
			return auth, nil
		}
	}

	return &bkauth.CredentialsResponse{}, nil
}

func parseAuthHost(host string) (string, error) {
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "https://")

	ref, err := reference.ParseNormalizedNamed(host)

	if err != nil {
		return "", err
	}
	return reference.Domain(ref), nil
}

func (a *RegistryAuthProvider) FetchToken(ctx context.Context, req *bkauth.FetchTokenRequest) (rr *bkauth.FetchTokenResponse, err error) {
	return nil, status.Errorf(codes.Unavailable, "client side tokens not implemented")
}

func (a *RegistryAuthProvider) GetTokenAuthority(ctx context.Context, req *bkauth.GetTokenAuthorityRequest) (*bkauth.GetTokenAuthorityResponse, error) {
	return nil, status.Errorf(codes.Unavailable, "client side tokens not implemented")
}

func (a *RegistryAuthProvider) VerifyTokenAuthority(ctx context.Context, req *bkauth.VerifyTokenAuthorityRequest) (*bkauth.VerifyTokenAuthorityResponse, error) {
	return nil, status.Errorf(codes.Unavailable, "client side tokens not implemented")
}
