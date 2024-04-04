package auth

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	bkauth "github.com/moby/buildkit/session/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TODO: this could be consolidated to buildkit pkg now, should only be needed there

const defaultDockerDomain = "docker.io"

// RegistryAuthProvider is a custom auth provider for image's registry
// authentication from dynamic user provided secrets.
// Adapted from: https://github.com/dagger/dagger/blob/v0.2.36/solver/registryauth.go
// and merge with Buildkit DockerAuthProvider from
// https://github.com/moby/buildkit/blob/master/session/auth/authprovider/authprovider.go#L42
//
// RegistryAuthProvider implements session.Attachable to be used by Buildkit as
// credential provider.
// It also implements auth.AuthServer to merge dockerAuthProvider capabilities
// with in memory storage.
type RegistryAuthProvider struct {
	// Memory map credential storage.
	credentials map[string]*bkauth.CredentialsResponse

	// Mutex to handle concurrency.
	m sync.RWMutex

	bkauth.UnimplementedAuthServer
}

// NewRegistryAuthProvider initializes a new store.
func NewRegistryAuthProvider() *RegistryAuthProvider {
	return &RegistryAuthProvider{credentials: map[string]*bkauth.CredentialsResponse{}}
}

// AddCredential inserts a new credential for the corresponding address.
// Returns an error if the address does not match the standard registry
// address: {registry_domain}.{extension}.
func (r *RegistryAuthProvider) AddCredential(address, username, secret string) error {
	address, err := parseAuthAddress(address)
	if err != nil {
		return err
	}

	r.m.Lock()
	defer r.m.Unlock()

	r.credentials[address] = &bkauth.CredentialsResponse{
		Username: username,
		Secret:   secret,
	}

	return nil
}

// parseAuthAddress sanitizes the given address to retrieves its host.
// Given address may have http prefix, tag, hash or anything, those
// will be ignored.
//
// Based on splitReposSearchTerm from https://github.com/moby/moby/tree/master/registry
func parseAuthAddress(address string) (string, error) {
	// Remove any URL format.
	address = strings.TrimPrefix(address, "http://")
	address = strings.TrimPrefix(address, "https://")
	address = strings.TrimSuffix(address, "/")

	// Remove Hash value or anything after a "@".
	// registry@sha256:<hash> 					-> registry.
	// registry:5000/owner/image@sha256:<hash>	-> registry:5000/owner/image.
	address = strings.SplitN(address, "@", 2)[0]

	// Remove tag and anything after ":" but keep
	// the port of the address.
	// localhost:5000/image:1.0	-> localhost:5000/image
	// registry.com:5000:1.4 	-> registry.com:5000
	if strings.Count(address, ":") > 1 {
		address = address[:strings.LastIndex(address, ":")]
	}

	// Recheck for tag or port if there's still ":" and
	// remove everything after if it's a tag.
	// registry.com:5000	-> registry.com:5000
	// registry/foo:14 	 	-> registry/foo:14
	if strings.Count(address, ":") > 0 {
		tmp := address[strings.LastIndex(address, ":"):]

		// If it's a tag, it may contains a "."
		if strings.Count(tmp, ".") > 0 {
			address = address[:strings.LastIndex(address, ":")]
		}
	}

	addressPart := strings.SplitN(address, "/", 2)
	domain := addressPart[0]

	portStr := domain[strings.LastIndex(domain, ":")+1:]
	// Parse port to check if it's a valid port.
	port, _ := strconv.Atoi(portStr)

	switch {
	// Local registry
	// E.g., localhost:5000
	case strings.Contains(domain, "localhost"):
		return domain, nil
	case len(address) >= 1 && strings.Contains(domain, ":") && port > 0:
		return domain, nil
	// If the address is only an image name without any "."
	// E.g., ubuntu, alpine, redis
	case len(address) == 1 && !strings.Contains(domain, "."):
		return defaultDockerDomain, nil
	// If the address contains an image without "." nor ":"
	// E.g., bitnami/redis
	case !strings.Contains(address, ".") && !strings.Contains(address, ":"):
		return defaultDockerDomain, nil
	// If the address is docker hub related.
	// E.g., registry-1.docker.io, index.docker.io
	case strings.Contains(address, "docker.io"):
		return defaultDockerDomain, nil
	// Private registry or other well formatted address.
	case strings.Contains(domain, "."):
		return domain, nil
	default:
		return "", fmt.Errorf("failed parsing [%s], expected address format: [%s]", domain, "registrydomain.extension")
	}
}

// RemoveCredential suppress the credential bind to the given address.
// Nothing happens if the address has no credential.
func (r *RegistryAuthProvider) RemoveCredential(address string) error {
	address, err := parseAuthAddress(address)
	if err != nil {
		return err
	}

	r.m.Lock()
	defer r.m.Unlock()

	delete(r.credentials, address)
	return nil
}

func (r *RegistryAuthProvider) Register(server *grpc.Server) {
	bkauth.RegisterAuthServer(server, r)
}

func (r *RegistryAuthProvider) credential(domain string) *bkauth.CredentialsResponse {
	// Update default DNS of Docker Hub registry to short name.
	if domain == "registry-1.docker.io" || domain == "index.docker.io" {
		domain = defaultDockerDomain
	}

	r.m.Lock()
	defer r.m.Unlock()

	for authAddress, credential := range r.credentials {
		if authAddress == domain {
			return credential
		}
	}

	return nil
}

// Credentials retrieves credentials of the requested address.
// It searches in the memory map for the standardize address.
//
// If the address isn't registered in the memory map, it will search
// on DockerAuthProvider.
func (r *RegistryAuthProvider) Credentials(ctx context.Context, req *bkauth.CredentialsRequest) (*bkauth.CredentialsResponse, error) {
	memoryCredential := r.credential(req.GetHost())
	if memoryCredential != nil {
		return memoryCredential, nil
	}
	return nil, status.Errorf(codes.NotFound, "no credential found for %s", req.GetHost())
}
