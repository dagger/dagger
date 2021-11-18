package plancontext

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContext(t *testing.T) {
	ctx := New()

	id := ctx.Secrets.Register(&Secret{
		PlainText: "test",
	})
	secret := ctx.Secrets.Get(id)
	require.NotNil(t, secret)
	require.Equal(t, "test", secret.PlainText)
}
