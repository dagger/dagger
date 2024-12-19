package client

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"

	"github.com/moby/buildkit/session/secrets"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type SecretResolver func(context.Context, *url.URL, SecretProvider) ([]byte, error)

var resolvers = map[string]SecretResolver{
	"env":  envSecretProvider,
	"file": fileSecretProvider,
	"op":   opSecretProvider,
}

func SecretResolverForID(id string) (SecretResolver, *url.URL, error) {
	u, err := url.Parse(id)
	if err != nil {
		return nil, nil, err
	}

	resolver, ok := resolvers[u.Scheme]
	if !ok {
		return nil, nil, fmt.Errorf("unsupported secret scheme: %s", u.Scheme)
	}
	return resolver, u, nil
}

type SecretProvider struct {
	LookupEnv func(string) (string, bool)
}

func (sp SecretProvider) Register(server *grpc.Server) {
	secrets.RegisterSecretsServer(server, sp)
}

func (sp SecretProvider) GetSecret(ctx context.Context, req *secrets.GetSecretRequest) (*secrets.GetSecretResponse, error) {
	if sp.LookupEnv == nil {
		sp.LookupEnv = os.LookupEnv
	}

	resolver, u, err := SecretResolverForID(req.ID)
	if err != nil {
		return nil, err
	}

	plaintext, err := resolver(ctx, u, sp)
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

func envSecretProvider(_ context.Context, u *url.URL, sp SecretProvider) ([]byte, error) {
	v, ok := sp.LookupEnv(u.Host)
	if !ok {
		return nil, fmt.Errorf("env var %s not found", u.Host)
	}
	return []byte(v), nil
}

func fileSecretProvider(_ context.Context, u *url.URL, _ SecretProvider) ([]byte, error) {
	return os.ReadFile(path.Join(u.Host, u.Path))
}

func opSecretProvider(ctx context.Context, u *url.URL, _ SecretProvider) ([]byte, error) {
	if _, err := exec.LookPath("op"); err != nil {
		return nil, fmt.Errorf("unable to lookup %s: op is not installed", u.String())
	}

	cmd := exec.CommandContext(
		ctx,
		"op",
		"read",
		"-n",
		u.String(),
	)
	cmd.Env = os.Environ()

	plaintext, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("unable to lookup %s: %w", u.String(), err)
	}

	return plaintext, nil
}
