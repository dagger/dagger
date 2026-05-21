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

type SecretResolver interface {
	Resolve(context.Context, string) ([]byte, error)
	ResolveMany(context.Context, []string) (map[string][]byte, error)
}

type SecretResolverFunc func(context.Context, string) ([]byte, error)

func (f SecretResolverFunc) Resolve(ctx context.Context, id string) ([]byte, error) {
	return f(ctx, id)
}

func (f SecretResolverFunc) ResolveMany(ctx context.Context, ids []string) (map[string][]byte, error) {
	values := make(map[string][]byte, len(ids))
	for _, id := range ids {
		value, err := f(ctx, id)
		if err != nil {
			return nil, err
		}
		values[id] = value
	}
	return values, nil
}

var resolvers = map[string]SecretResolver{
	"env":       SecretResolverFunc(envProvider),
	"file":      SecretResolverFunc(fileProvider),
	"cmd":       SecretResolverFunc(cmdProvider),
	"op":        opResolver{},
	"vault":     SecretResolverFunc(vaultProvider),
	"libsecret": SecretResolverFunc(libsecretProvider),
	"gcp":       SecretResolverFunc(gcpProvider),
	"aws+sm":    SecretResolverFunc(awsSecretManagerProvider),
	"aws+ps":    SecretResolverFunc(awsParameterStoreProvider),
}

func Schemes() []string {
	return slices.Collect(maps.Keys(resolvers))
}

func ResolverForID(id string) (SecretResolver, string, error) {
	scheme, pathWithQuery, err := SchemeForID(id)
	if err != nil {
		return nil, "", err
	}

	resolver, ok := resolvers[scheme]
	if !ok {
		return nil, "", fmt.Errorf("unsupported secret provider: %q", scheme)
	}
	return resolver, pathWithQuery, nil
}

type SecretProvider struct{}

func NewSecretProvider() SecretProvider {
	return SecretProvider{}
}

func (sp SecretProvider) Register(server *grpc.Server) {
	secrets.RegisterSecretsServer(server, sp)
}

func (sp SecretProvider) GetSecret(ctx context.Context, req *secrets.GetSecretRequest) (*secrets.GetSecretResponse, error) {
	resp, err := sp.GetSecrets(ctx, &secrets.GetSecretsRequest{
		IDs:         []string{req.ID},
		Annotations: req.Annotations,
	})
	if err != nil {
		return nil, err
	}
	secret, ok := resp.Secrets[req.ID]
	if !ok {
		return nil, status.Error(codes.NotFound, secrets.ErrNotFound.Error())
	}
	return secret, nil
}

func (sp SecretProvider) GetSecrets(ctx context.Context, req *secrets.GetSecretsRequest) (*secrets.GetSecretsResponse, error) {
	pathsByID := make(map[string]string, len(req.IDs))
	idsByScheme := make(map[string][]string)
	for _, id := range req.IDs {
		scheme, u, err := SchemeForID(id)
		if err != nil {
			return nil, err
		}
		idsByScheme[scheme] = append(idsByScheme[scheme], id)
		pathsByID[id] = u
	}

	resp := &secrets.GetSecretsResponse{
		Secrets: make(map[string]*secrets.GetSecretResponse, len(req.IDs)),
	}
	for scheme, ids := range idsByScheme {
		resolver, ok := resolvers[scheme]
		if !ok {
			return nil, fmt.Errorf("unsupported secret provider: %q", scheme)
		}

		paths := make([]string, 0, len(ids))
		for _, id := range ids {
			paths = append(paths, pathsByID[id])
		}
		values, err := resolver.ResolveMany(ctx, paths)
		if err != nil {
			if errors.Is(err, secrets.ErrNotFound) {
				return nil, status.Error(codes.NotFound, err.Error())
			}
			return nil, err
		}
		for _, id := range ids {
			value, ok := values[pathsByID[id]]
			if !ok {
				return nil, fmt.Errorf("secret %q: missing response", id)
			}
			resp.Secrets[id] = &secrets.GetSecretResponse{Data: value}
		}
	}
	return resp, nil
}

func SchemeForID(id string) (string, string, error) {
	scheme, pathWithQuery, ok := strings.Cut(id, "://")
	if !ok {
		return "", "", fmt.Errorf("parse %q: malformed id", id)
	}
	return scheme, pathWithQuery, nil
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

func (sp SecretProviderProxy) GetSecrets(ctx context.Context, req *secrets.GetSecretsRequest) (*secrets.GetSecretsResponse, error) {
	return sp.client.GetSecrets(grpcutil.IncomingToOutgoingContext(ctx), req)
}
