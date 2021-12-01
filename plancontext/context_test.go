package plancontext

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContext(t *testing.T) {
	ctx := New()

	secret := ctx.Secrets.New("test")
	get, err := ctx.Secrets.FromValue(secret.Value())
	require.NoError(t, err)
	require.Equal(t, "test", get.PlainText())
}
