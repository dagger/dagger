package secretprovider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/moby/buildkit/session/secrets"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type dataWithTTL struct {
	expiresAt time.Time
	data      map[string]any
}

type SecretResolver func(context.Context, string) ([]byte, error)

var resolvers = map[string]SecretResolver{
	"env":   envProvider,
	"file":  fileProvider,
	"cmd":   cmdProvider,
	"op":    opProvider,
	"vault": getVaultProvider(0), // no ttl for backward compatibility
}

func getSecretResolverWithTTL(scheme string, ttl time.Duration) (SecretResolver, error) {
	if scheme == "vault" && ttl > 0 {
		return getVaultProvider(ttl), nil
	}

	resolver, ok := resolvers[scheme]
	if !ok {
		return nil, fmt.Errorf("unsupported secret provider: %q", scheme)
	}

	return resolver, nil
}

func ResolverForID(id string) (SecretResolver, string, error) {
	parsed, err := url.Parse(id)
	if err != nil {
		return nil, "", fmt.Errorf("parse %q: malformed id. err: %w", id, err)
	}

	scheme := parsed.Scheme
	path := parsed.Path

	var ttl time.Duration
	ttlStr := strings.TrimSpace(parsed.Query().Get("ttl"))
	if ttlStr != "" {
		ttl, err = time.ParseDuration(ttlStr)
		if err != nil {
			return nil, "", fmt.Errorf("invalid ttl %q provided for secret %q. err: %w", ttlStr, id, err)
		}
	}

	resolver, err := getSecretResolverWithTTL(scheme, ttl)
	if err != nil {
		return nil, "", err
	}

	return resolver, path, nil
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
