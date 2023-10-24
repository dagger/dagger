package core

import (
	"context"
	"testing"

	"github.com/moby/buildkit/session/secrets"
	"github.com/stretchr/testify/require"
)

func TestSecretStore(t *testing.T) {
	store := NewSecretStore()
	store.AddSecret(context.Background(), "foo", []byte("bar"))
	result, err := store.GetSecret(context.Background(), "foo")
	require.NoError(t, err)
	require.Equal(t, []byte("bar"), result)
}

func TestSecretStoreNotFound(t *testing.T) {
	store := NewSecretStore()
	_, err := store.GetSecret(context.Background(), "foo")
	require.ErrorIs(t, err, secrets.ErrNotFound)
}
