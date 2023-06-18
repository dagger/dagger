package secret

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/dagger/dagger/core"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session/secrets"
)

// ErrNotFound indicates a secret can not be found.
var ErrNotFound = errors.New("secret not found")

func NewStore() *Store {
	return &Store{
		secrets: map[string]string{},
	}
}

var _ secrets.SecretStore = &Store{}

type Store struct {
	gw bkgw.Client

	mu      sync.Mutex
	secrets map[string]string
}

func (store *Store) SetGateway(gw bkgw.Client) {
	store.gw = gw
}

// AddSecret adds the secret identified by user defined name with its plaintext
// value to the secret store.
func (store *Store) AddSecret(_ context.Context, name, plaintext string) (core.SecretID, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	secret := core.NewDynamicSecret(name)

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
func (store *Store) GetSecret(ctx context.Context, idOrName string) ([]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	if strings.HasPrefix(idOrName, core.ServicesSecretPrefix) {
		hosts := strings.Split(strings.TrimPrefix(idOrName, core.ServicesSecretPrefix), ",")

		pairs := make([]string, len(hosts))
		for _, host := range hosts {
			svc, found := core.AllServices.Service(host)
			if !found {
				return nil, fmt.Errorf("could not find IP for host %q", host)
			}

			pairs = append(pairs, fmt.Sprintf("%s:%s", host, svc.IP))
		}

		return []byte(strings.Join(pairs, ";")), nil
	}

	var name string
	if secret, err := core.SecretID(idOrName).ToSecret(); err == nil {
		if secret.IsOldFormat() {
			// use the legacy SecretID format
			return secret.LegacyPlaintext(ctx, store.gw)
		}

		name = secret.Name
	} else {
		name = idOrName
	}

	plaintext, ok := store.secrets[name]
	if !ok {
		return nil, ErrNotFound
	}

	return []byte(plaintext), nil
}
