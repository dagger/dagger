package secret

import (
	"context"
	"errors"
	"sync"

	"github.com/dagger/dagger/core"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session/secrets"
)

// ErrNotFound indicates a secret can not be found.
var ErrNotFound = errors.New("secret not found")

func NewStore() *Store {
	return &Store{
		idToPlaintext: map[core.SecretID]string{},
	}
}

var _ secrets.SecretStore = &Store{}

type Store struct {
	gw bkgw.Client

	mu            sync.Mutex
	idToPlaintext map[core.SecretID]string
}

func (store *Store) SetGateway(gw bkgw.Client) {
	store.gw = gw
}

// AddSecret adds the secret identified by user defined name with its plaintext
// value to the secret store.
func (store *Store) AddSecret(_ context.Context, name, plaintext string) (core.SecretID, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	id, err := core.NewSecretID(name, plaintext)
	if err != nil {
		return id, err
	}

	// add the plaintext to the map
	store.idToPlaintext[id] = plaintext

	return id, nil
}

// GetSecret returns the plaintext from the id.
func (store *Store) GetSecret(ctx context.Context, id string) ([]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	secretID := core.SecretID(id)

	// we check if it's the old SecretID format
	isOldSecretIDFormat, err := secretID.IsOldFormat()
	if err != nil {
		return nil, err
	}

	if isOldSecretIDFormat {
		// we use the legacy SecretID format
		return core.NewSecret(core.SecretID(id)).Plaintext(ctx, store.gw)
	}

	// we use the new SecretID format
	plaintext, ok := store.idToPlaintext[secretID]
	if !ok {
		return nil, ErrNotFound
	}

	return []byte(plaintext), nil
}
