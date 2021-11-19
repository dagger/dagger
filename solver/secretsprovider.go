package solver

import (
	"context"

	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/plancontext"
)

func NewSecretsStoreProvider(pctx *plancontext.Context) session.Attachable {
	return secretsprovider.NewSecretProvider(&inputStore{pctx})
}

type inputStore struct {
	pctx *plancontext.Context
}

func (s *inputStore) GetSecret(ctx context.Context, id string) ([]byte, error) {
	lg := log.Ctx(ctx)

	secret := s.pctx.Secrets.Get(plancontext.ContextKey(id))
	if secret == nil {
		return nil, secrets.ErrNotFound
	}

	lg.
		Debug().
		Str("id", id).
		Msg("injecting secret")

	return []byte(secret.PlainText), nil
}
