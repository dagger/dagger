package dagger

import (
	"context"
	"net/url"
	"strings"
	"sync"

	bkauth "github.com/moby/buildkit/session/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// registryAuthProvider is a buildkit provider for registry authentication
// Adapted from: https://github.com/moby/buildkit/blob/master/session/auth/authprovider/authprovider.go
type registryAuthProvider struct {
	credentials map[string]*bkauth.CredentialsResponse
	m           sync.RWMutex
}

func newRegistryAuthProvider() *registryAuthProvider {
	return &registryAuthProvider{
		credentials: map[string]*bkauth.CredentialsResponse{},
	}
}

func (a *registryAuthProvider) AddCredentials(target, username, secret string) {
	a.m.Lock()
	defer a.m.Unlock()

	a.credentials[target] = &bkauth.CredentialsResponse{
		Username: username,
		Secret:   secret,
	}
}

func (a *registryAuthProvider) Register(server *grpc.Server) {
	bkauth.RegisterAuthServer(server, a)
}

func (a *registryAuthProvider) Credentials(ctx context.Context, req *bkauth.CredentialsRequest) (*bkauth.CredentialsResponse, error) {
	reqURL, err := parseAuthHost(req.Host)
	if err != nil {
		return nil, err
	}

	a.m.RLock()
	defer a.m.RUnlock()

	for authHost, auth := range a.credentials {
		u, err := parseAuthHost(authHost)
		if err != nil {
			return nil, err
		}

		if u.Host == reqURL.Host {
			return auth, nil
		}
	}

	return &bkauth.CredentialsResponse{}, nil
}

func parseAuthHost(host string) (*url.URL, error) {
	if host == "registry-1.docker.io" {
		host = "https://index.docker.io/v1/"
	}

	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "https://" + host
	}
	return url.Parse(host)
}

func (a *registryAuthProvider) FetchToken(ctx context.Context, req *bkauth.FetchTokenRequest) (rr *bkauth.FetchTokenResponse, err error) {
	return nil, status.Errorf(codes.Unavailable, "client side tokens not implemented")
}

func (a *registryAuthProvider) GetTokenAuthority(ctx context.Context, req *bkauth.GetTokenAuthorityRequest) (*bkauth.GetTokenAuthorityResponse, error) {
	return nil, status.Errorf(codes.Unavailable, "client side tokens not implemented")
}

func (a *registryAuthProvider) VerifyTokenAuthority(ctx context.Context, req *bkauth.VerifyTokenAuthorityRequest) (*bkauth.VerifyTokenAuthorityResponse, error) {
	return nil, status.Errorf(codes.Unavailable, "client side tokens not implemented")
}
