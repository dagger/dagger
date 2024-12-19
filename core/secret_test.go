package core

import (
	"context"
	"testing"

	"github.com/moby/buildkit/session/secrets"
	"github.com/stretchr/testify/require"
)

func TestSecretStore(t *testing.T) {
	store := NewSecretStore(nil)
	require.NoError(t, store.AddSecret(&Secret{
		Query:    &Query{},
		IDDigest: "dgst",
	}, "foo", []byte("bar")))
	require.True(t, store.HasSecret("dgst"))
	name, ok := store.GetSecretName("dgst")
	require.True(t, ok)
	require.Equal(t, "foo", name)
	plaintext, err := store.GetSecretPlaintext(context.Background(), "dgst")
	require.NoError(t, err)
	require.Equal(t, []byte("bar"), plaintext)
}

func TestSecretStoreNotFound(t *testing.T) {
	store := NewSecretStore(nil)
	_, err := store.AsBuildkitSecretStore().GetSecret(context.Background(), "foo")
	require.ErrorIs(t, err, secrets.ErrNotFound)
}
