package core

import (
	"context"
	"errors"
	"sync"

	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/moby/buildkit/session/secrets"
	"github.com/opencontainers/go-digest"
)

// Secret is a content-addressed secret.
type Secret struct {
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

func (secret *Secret) ID() (SecretID, error) {
	return resourceid.Encode(secret)
}

func (secret *Secret) Digest() (digest.Digest, error) {
	return stableDigest(secret)
}

// ErrNotFound indicates a secret can not be found.
var ErrNotFound = errors.New("secret not found")

func NewSecretStore() *SecretStore {
	return &SecretStore{
		secrets: map[string][]byte{},
	}
}

var _ secrets.SecretStore = &SecretStore{}

type SecretStore struct {
	mu      sync.Mutex
	secrets map[string][]byte
	bk      *buildkit.Client
}

func (store *SecretStore) SetBuildkitClient(bk *buildkit.Client) {
	store.bk = bk
}

// AddSecret adds the secret identified by user defined name with its plaintext
// value to the secret store.
func (store *SecretStore) AddSecret(_ context.Context, name string, plaintext []byte) (SecretID, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	secret := NewDynamicSecret(name)

	// add the plaintext to the map
	store.secrets[secret.Name] = plaintext

	return secret.ID()
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
func (store *SecretStore) GetSecret(ctx context.Context, idOrName string) ([]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	var name string
	if secret, err := SecretID(idOrName).Decode(); err == nil {
		name = secret.Name
	} else {
		name = idOrName
	}

	plaintext, ok := store.secrets[name]
	if !ok {
		return nil, ErrNotFound
	}

	return plaintext, nil
}
