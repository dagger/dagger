package core

import (
	"context"
	"sync"

	"github.com/dagger/dagger/core/idproto"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/moby/buildkit/session/secrets"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

// Secret is a content-addressed secret.
type Secret struct {
	ID *idproto.ID `json:"id"`

	// Name specifies the arbitrary name/id of the secret.
	Name string `json:"name,omitempty"`
}

func NewDynamicSecret(name string) *Secret {
	return &Secret{
		Name: name,
	}
}

func (secret *Secret) Clone() *Secret {
	cp := *secret
	return &cp
}

func (secret *Secret) Digest() (digest.Digest, error) {
	return secret.ID.Digest()
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
func (store *SecretStore) AddSecret(_ context.Context, name string, plaintext []byte) (SecretID, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	secret := NewDynamicSecret(name)

	// add the plaintext to the map
	store.secrets[secret.Name] = plaintext

	return NewCanonicalSecret(secret.Name), nil
}

// NewCanonicalSecret returns a canonical SecretID for the given name.
func NewCanonicalSecret(name string) SecretID {
	var id SecretID = resourceid.New[Secret]("Secret")
	id.Append("secret", idproto.Arg("name", name))
	return id
}

// GetSecret returns the plaintext secret value.
//
// Its argument may either be the user defined name originally specified within
// a SecretID, or a full SecretID value.
//
// A user defined name will be received when secrets are used in a Dockerfile
// build.
//
// In all other cases, a SecretID is expected.
func (store *SecretStore) GetSecret(ctx context.Context, name string) ([]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	plaintext, ok := store.secrets[name]
	if !ok {
		return nil, errors.Wrapf(secrets.ErrNotFound, "secret %s", name)
	}

	return plaintext, nil
}
