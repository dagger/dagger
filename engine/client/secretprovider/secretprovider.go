package secretprovider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/moby/buildkit/session/secrets"
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
}

func NewSecretProvider() SecretProvider {
	return SecretProvider{}
}

func (sp SecretProvider) Register(server *grpc.Server) {
	secrets.RegisterSecretsServer(server, sp)
}

func (sp SecretProvider) GetSecret(ctx context.Context, req *secrets.GetSecretRequest) (*secrets.GetSecretResponse, error) {
	resolver, u, err := ResolverForID(req.ID)
	if err != nil {
		return nil, err
	}

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
