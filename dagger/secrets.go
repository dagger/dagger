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

// TODO: this only works from the client right now, should just be in the API and thus usable anywhere
func AddSecret(ctx *Context, id, val string) {
	// TODO: synchronization
	ctx.secretProvider.secrets[id] = []byte(val)
}
