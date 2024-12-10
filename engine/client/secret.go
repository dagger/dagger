package client

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"

	"github.com/moby/buildkit/session/secrets"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type SecretResolver func(context.Context, *url.URL) ([]byte, error)

var resolvers = map[string]SecretResolver{
	"env": envSecretProvider,
	"op":  opSecretProvider,
}

type SecretProvider struct {
}

func (sp SecretProvider) Register(server *grpc.Server) {
	secrets.RegisterSecretsServer(server, sp)
}

func (sp SecretProvider) GetSecret(ctx context.Context, req *secrets.GetSecretRequest) (*secrets.GetSecretResponse, error) {
	u, err := url.Parse(req.ID)
	if err != nil {
		return nil, err
	}

	resolver, ok := resolvers[u.Scheme]
	if !ok {
		return nil, fmt.Errorf("unsupported secret scheme: %s", u.Scheme)
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

func envSecretProvider(_ context.Context, u *url.URL) ([]byte, error) {
	v, ok := os.LookupEnv(u.Host)
	if !ok {
		return nil, fmt.Errorf("env var %s not found", u.Host)
	}
	return []byte(v), nil
}

func opSecretProvider(ctx context.Context, u *url.URL) ([]byte, error) {
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
