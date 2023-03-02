package secret

import (
	"context"
	"sync"

	"github.com/dagger/dagger/core"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session/secrets"
)

func NewStore() *Store {
	return &Store{
		nameToDigest:  map[string]string{},
		idToPlaintext: map[core.SecretID]string{},
	}
}

var _ secrets.SecretStore = &Store{}

type Store struct {
	gw bkgw.Client

	mu            sync.Mutex
	nameToDigest  map[string]string
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

	digest, err := id.Digest()
	if err != nil {
		// We shouldn't arrive here unless we messed up on our side.
		// We should stop here to analyze what's wrong.
		panic(err)
	}

	// add the digest to the map
	store.nameToDigest[name] = digest

	// add the plaintext to the map
	store.idToPlaintext[id] = plaintext

	return id, nil
}

// GetSecret returns the plaintext from the id.
func (store *Store) GetSecret(ctx context.Context, id string) ([]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	secretID := core.SecretID(id)
	// we check if it's the new SecretID format
	_, err := secretID.Digest()
	if err != nil {
		// if not, we use the legacy SecretID format
		return core.NewSecret(core.SecretID(id)).Plaintext(ctx, store.gw)
	}

	// if it is, we use it
	plaintext := []byte(store.idToPlaintext[secretID])

	return plaintext, nil
}
