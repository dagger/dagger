package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/internal/buildkit/session/secrets"
	"github.com/stretchr/testify/require"
)

func TestSecretStoreNotFound(t *testing.T) {
	store := NewSecretStore(nil)
	_, err := store.AsBuildkitSecretStore().GetSecret(context.Background(), "foo")
	require.ErrorIs(t, err, secrets.ErrNotFound)
}
