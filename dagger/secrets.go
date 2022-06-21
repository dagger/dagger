package dagger

import (
	"context"

	"github.com/moby/buildkit/session/secrets"
)

func newSecretProvider() *secretProvider {
	return &secretProvider{secrets: make(map[string][]byte)}
}

type secretProvider struct {
	secrets map[string][]byte
}

func (p *secretProvider) GetSecret(ctx context.Context, id string) ([]byte, error) {
	if secret, ok := p.secrets[id]; ok {
		return secret, nil
	}
	return nil, secrets.ErrNotFound
}
