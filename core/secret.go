package core

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"sync"

	"github.com/moby/buildkit/session/secrets"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/vektah/gqlparser/v2/ast"
)

// Secret is a content-addressed secret.
type Secret struct {
	Query *Query

	// Name specifies the name of the secret.
	Name string `json:"name,omitempty"`

	// Accessor specifies the accessor key for the secret.
	Accessor string `json:"accessor,omitempty"`
}

func GetLocalSecretAccessor(ctx context.Context, parent *Query, name string) (string, error) {
	m, err := parent.CurrentModule(ctx)
	if err != nil && !errors.Is(err, ErrNoCurrentModule) {
		return "", err
	}
	var d digest.Digest
	if m != nil {
		d = m.Source.ID().Digest()
	}
	return NewSecretAccessor(name, d.String()), nil
}

func NewSecretAccessor(name string, scope string) string {
	// Use an HMAC, which allows us to keep the scope secret
	// This also protects from length-extension attacks (where if we had
	// access to secret FOO in scope X, we could derive access to FOOBAR).
	h := hmac.New(digest.SHA256.Hash, []byte(scope))
	dt := h.Sum([]byte(name))
	return hex.EncodeToString(dt)
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
func (store *SecretStore) AddSecret(ctx context.Context, name string, plaintext []byte) error {
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
