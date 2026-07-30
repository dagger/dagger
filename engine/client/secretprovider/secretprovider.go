package secretprovider

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dagger/dagger/internal/buildkit/session/secrets"
	"github.com/dagger/dagger/util/grpcutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type SecretResolver func(context.Context, string) ([]byte, error)

var resolvers = map[string]SecretResolver{
	"env":       envProvider,
	"file":      fileProvider,
	"cmd":       cmdProvider,
	"op":        opProvider,
	"vault":     vaultProvider,
	"libsecret": libsecretProvider,
	"gcp":       gcpProvider,
	"aws+sm":    awsSecretManagerProvider,
	"aws+ps":    awsParameterStoreProvider,
}

func Schemes() []string {
	return slices.Collect(maps.Keys(resolvers))
}

func ResolverForID(id string) (SecretResolver, string, error) {
	scheme, pathWithQuery, ok := strings.Cut(id, "://")
	if !ok {
		return nil, "", fmt.Errorf("parse %q: malformed id", id)
	}

	resolver, ok := resolvers[scheme]
	if !ok {
		return nil, "", fmt.Errorf("unsupported secret provider: %q", scheme)
	}
	return resolver, pathWithQuery, nil
}

type SecretProvider struct {
	// workspaceRoot is the directory containing this client's dagger.json,
	// if any. Resolved once at construction time, since it doesn't change
	// over the lifetime of a client. See findWorkspaceRoot for why this is
	// determined independently rather than reusing the engine's own
	// workspace resolution.
	workspaceRoot string
}

func NewSecretProvider() SecretProvider {
	return SecretProvider{workspaceRoot: findWorkspaceRoot()}
}

func (sp SecretProvider) Register(server *grpc.Server) {
	secrets.RegisterSecretsServer(server, sp)
}

func (sp SecretProvider) GetSecret(ctx context.Context, req *secrets.GetSecretRequest) (*secrets.GetSecretResponse, error) {
	resolver, u, err := ResolverForID(req.ID)
	if err != nil {
		return nil, err
	}

	ctx = withWorkspaceRoot(ctx, sp.workspaceRoot)
	plaintext, err := resolver(ctx, u)
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, err
	}

	return &secrets.GetSecretResponse{
		Data: plaintext,
	}, nil
}

type SecretProviderProxy struct {
	client secrets.SecretsClient
}

func NewSecretProviderProxy(client secrets.SecretsClient) SecretProviderProxy {
	return SecretProviderProxy{
		client: client,
	}
}

func (sp SecretProviderProxy) Register(server *grpc.Server) {
	secrets.RegisterSecretsServer(server, sp)
}

func (sp SecretProviderProxy) GetSecret(ctx context.Context, req *secrets.GetSecretRequest) (*secrets.GetSecretResponse, error) {
	return sp.client.GetSecret(grpcutil.IncomingToOutgoingContext(ctx), req)
}
