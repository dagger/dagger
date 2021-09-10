package solver

import (
	"context"
	"strings"

	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/state"
)

type SecretsStore struct {
	Secrets session.Attachable
	store   *inputStore
}

func (s SecretsStore) GetSecret(ctx context.Context, id string) ([]byte, error) {
	return s.store.GetSecret(ctx, id)
}

func NewSecretsStoreProvider(st *state.State) SecretsStore {
	store := &inputStore{st}

	return SecretsStore{
		Secrets: secretsprovider.NewSecretProvider(store),
		store:   store,
	}
}

type inputStore struct {
	st *state.State
}

func (s *inputStore) GetSecret(ctx context.Context, id string) ([]byte, error) {
	lg := log.Ctx(ctx)

	const secretPrefix = "secret="

	if !strings.HasPrefix(id, secretPrefix) {
		return nil, secrets.ErrNotFound
	}

	id = strings.TrimPrefix(id, secretPrefix)

	id = strings.Split(id, ";hash=")[0]

	input, ok := s.st.Inputs[id]
	if !ok {
		return nil, secrets.ErrNotFound
	}
	if input.Secret == nil {
		return nil, secrets.ErrNotFound
	}

	lg.
		Debug().
		Str("id", id).
		Msg("injecting secret")

	return []byte(input.Secret.PlainText()), nil
}
