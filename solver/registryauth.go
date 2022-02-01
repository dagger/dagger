package solver

import (
	"context"
	"fmt"
	"strings"
	"sync"

	bkauth "github.com/moby/buildkit/session/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const defaultDockerDomain = "docker.io"

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
		host = defaultDockerDomain
	}

	a.m.RLock()
	defer a.m.RUnlock()

	for authHost, auth := range a.credentials {
		u, err := ParseAuthHost(authHost)
		if err != nil {
			return nil, err
		}
		if u == host {
			return auth, nil
		}
	}

	return &bkauth.CredentialsResponse{}, nil
}

// Parsing function based on splitReposSearchTerm
// "github.com/docker/docker/registry"
func ParseAuthHost(host string) (string, error) {
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimSuffix(host, "/")

	// Remove everything after @
	nameParts := strings.SplitN(host, "@", 2)
	host = nameParts[0]

	// if ":" > 1, trim after last ":" found
	if strings.Count(host, ":") > 1 {
		host = host[:strings.LastIndex(host, ":")]
	}

	// if ":" > 0, trim after last ":" found if it contains "."
	// ex: samalba/hipache:1.15, registry.com:5000:1.0
	if strings.Count(host, ":") > 0 {
		tmpStr := host[strings.LastIndex(host, ":"):]
		if strings.Count(tmpStr, ".") > 0 {
			host = host[:strings.LastIndex(host, ":")]
		}
	}

	nameParts = strings.SplitN(host, "/", 2)
	var domain string
	switch {
	// Localhost registry parsing
	case strings.Contains(nameParts[0], "localhost"):
		domain = nameParts[0]
	// If the split returned an array of len 1 that doesn't contain any .
	// ex: ubuntu
	case len(nameParts) == 1 && !strings.Contains(nameParts[0], "."):
		domain = defaultDockerDomain
	// if the split does not contain "." nor ":", but contains images
	// ex: samalba/hipache, samalba/hipache:1.15, samalba/hipache@sha:...
	case !strings.Contains(nameParts[0], ".") && !strings.Contains(nameParts[0], ":"):
		domain = defaultDockerDomain
	case nameParts[0] == "registry-1.docker.io":
		domain = defaultDockerDomain
	case nameParts[0] == "index.docker.io":
		domain = defaultDockerDomain
	// Private remaining registry parsing
	case strings.Contains(nameParts[0], "."):
		domain = nameParts[0]
	// Fail by default
	default:
		return "", fmt.Errorf("failed parsing [%s] expected host format: [%s]", nameParts[0], "registrydomain.extension")
	}
	return domain, nil
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
