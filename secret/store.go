package secret

import (
	"context"

	"github.com/dagger/dagger/core"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session/secrets"
)

func NewStore() *Store {
	return &Store{}
}

var _ secrets.SecretStore = &Store{}

type Store struct {
	gw bkgw.Client
}

func (store *Store) SetGateway(gw bkgw.Client) {
	store.gw = gw
}

func (store *Store) GetSecret(ctx context.Context, id string) ([]byte, error) {
	return core.NewSecret(core.SecretID(id)).Plaintext(ctx, store.gw)
}
