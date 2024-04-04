package core

import (
	"context"
	"testing"

	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

func TestHTTP(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	// do two in a row to ensure each gets downloaded correctly
	url := "https://raw.githubusercontent.com/dagger/dagger/main/CONTRIBUTING.md"
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

	svc, url := httpService(ctx, t, c, "Hello, world!")

	contents, err := c.HTTP(url, dagger.HTTPOpts{
		ExperimentalServiceHost: svc,
	}).Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, contents, "Hello, world!")
}

func TestHTTPServiceStableDigest(t *testing.T) {
	t.Parallel()

	content := identity.NewID()
	hostname := func(ctx context.Context, c *dagger.Client) string {
		svc, url := httpService(ctx, t, c, content)

		hn, err := c.Container().
			From(alpineImage).
			WithMountedFile("/index.html", c.HTTP(url, dagger.HTTPOpts{
				ExperimentalServiceHost: svc,
			})).
			AsService().
			Hostname(ctx)
		require.NoError(t, err)
		return hn
	}

	c1, ctx1 := connect(t)
	c2, ctx2 := connect(t)
	require.Equal(t, hostname(ctx1, c1), hostname(ctx2, c2))
}
