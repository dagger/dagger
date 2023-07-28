package core

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestHTTP(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)
	defer c.Close()

	// do two in a row to ensure each gets downloaded correctly
	url := "https://raw.githubusercontent.com/dagger/dagger/main/TESTING.md"
	contents, err := c.HTTP(url).Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, contents, "tests")

	url = "https://raw.githubusercontent.com/dagger/dagger/main/README.md"
	contents, err = c.HTTP(url).Contents(ctx)
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

func TestHTTPServiceStableDigest(t *testing.T) {
	t.Parallel()

	hostname := func(ctx context.Context, c *dagger.Client) string {
		svc, url := httpService(ctx, t, c, "Hello, world!")

		hn, err := c.Container().
			From(alpineImage).
			WithMountedFile("/index.html", c.HTTP(url, dagger.HTTPOpts{
				ExperimentalServiceHost: svc,
			})).
			Hostname(ctx)
		require.NoError(t, err)
		return hn
	}

	c1, ctx1 := connect(t)
	defer c1.Close()

	c2, ctx2 := connect(t)
	defer c2.Close()

	require.Equal(t, hostname(ctx1, c1), hostname(ctx2, c2))
}
