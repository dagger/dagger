package core

import (
	"context"
	"testing"

	"github.com/moby/buildkit/session/secrets"
	"github.com/stretchr/testify/require"
)

func TestSecretStore(t *testing.T) {
	store := NewSecretStore()
	require.NoError(t, store.AddSecret(&Secret{
		Query:    &Query{},
		IDDigest: "dgst",
	}, "foo", []byte("bar")))
	require.True(t, store.HasSecret("dgst"))
	name, ok := store.GetSecretName("dgst")
	require.True(t, ok)
	require.Equal(t, "foo", name)
	plaintext, ok := store.GetSecretPlaintext("dgst")
	require.True(t, ok)
	require.Equal(t, []byte("bar"), plaintext)
}

func TestSecretStoreNotFound(t *testing.T) {
	store := NewSecretStore()
	_, err := store.AsBuildkitSecretStore().GetSecret(context.Background(), "foo")
	require.ErrorIs(t, err, secrets.ErrNotFound)
}
