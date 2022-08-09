package secret

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/moby/buildkit/session/secrets"
)

func NewStore() *Store {
	return &Store{
		secrets: make(map[string][]byte),
	}
}

var _ secrets.SecretStore = &Store{}

type Store struct {
	secrets map[string][]byte
}

func (p *Store) AddSecret(ctx context.Context, value []byte) string {
	hash := sha256.Sum256([]byte(value))
	id := hex.EncodeToString(hash[:])
	p.secrets[id] = value
	return id
}

func (p *Store) GetSecret(ctx context.Context, id string) ([]byte, error) {
	if secret, ok := p.secrets[id]; ok {
		return secret, nil
	}
	return nil, secrets.ErrNotFound
}
