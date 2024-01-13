package core

import (
	"context"
	"sync"

	"github.com/moby/buildkit/session/secrets"
	"github.com/pkg/errors"
	"github.com/vektah/gqlparser/v2/ast"
)

// Secret is a content-addressed secret.
type Secret struct {
	Query *Query
	// Name specifies the name of the secret.
	Name string `json:"name,omitempty"`
}

func (*Secret) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Secret",
		NonNull:   true,
	}
}

func (*Secret) TypeDescription() string {
	return "A reference to a secret value, which can be handled more safely than the value itself."
}

func (secret *Secret) Clone() *Secret {
	cp := *secret
	return &cp
}

func (secret *Secret) Plaintext(ctx context.Context) ([]byte, error) {
	return secret.Query.Secrets.GetSecret(ctx, secret.Name)
}

func NewSecretStore() *SecretStore {
	return &SecretStore{
		secrets: map[string][]byte{},
	}
}

var _ secrets.SecretStore = &SecretStore{}

type SecretStore struct {
	mu      sync.Mutex
	secrets map[string][]byte
}

// AddSecret adds the secret identified by user defined name with its plaintext
// value to the secret store.
func (store *SecretStore) AddSecret(_ context.Context, name string, plaintext []byte) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.secrets[name] = plaintext
	return nil
}

// GetSecret returns the plaintext secret value for a user defined secret name.
func (store *SecretStore) GetSecret(ctx context.Context, name string) ([]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	plaintext, ok := store.secrets[name]
	if !ok {
		return nil, errors.Wrapf(secrets.ErrNotFound, "secret %s", name)
	}
	return plaintext, nil
}
