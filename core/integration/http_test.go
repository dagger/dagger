package core

import (
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestHTTP(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)
	defer c.Close()

	url := "https://raw.githubusercontent.com/dagger/dagger/main/README.md"
	contents, err := c.HTTP(url).Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, contents, "Dagger")
}

func TestHTTPService(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)
	defer c.Close()

	svc, url := httpService(ctx, t, c, "Hello, world!")

	contents, err := c.HTTP(url, dagger.HTTPOpts{
		ExperimentalServiceHost: svc,
	}).Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, contents, "Hello, world!")
}
