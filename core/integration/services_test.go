package core

import (
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestServices(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	www := c.Directory().WithNewFile("index.html", "Hello, world!")

	srv := c.Container().
		From("python").
		WithMountedDirectory("/srv/www", www).
		WithWorkdir("/srv/www").
		WithExposedPort(8000).
		WithExec([]string{"python", "-m", "http.server"})

	hostname, err := srv.Hostname(ctx)
	require.NoError(t, err)

	url, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
		Scheme: "http",
	})
	require.NoError(t, err)
	require.Equal(t, "http://"+hostname+":8000", url)

	client := c.Container().
		From("alpine").
		WithServiceDependency(srv).
		WithExec([]string{"apk", "add", "curl"}).
		WithExec([]string{"curl", "-s", url})

	out, err := client.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "Hello, world!", out)
}

func TestServiceHostnamesAreStable(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	www := c.Directory().WithNewFile("index.html", "Hello, world!")

	srv := c.Container().
		From("python").
		WithMountedDirectory("/srv/www", www).
		WithWorkdir("/srv/www").
		WithExposedPort(8000).
		WithExec([]string{"echo", "hello"}).
		WithExec([]string{"echo", "hello"}).
		WithExec([]string{"echo", "hello"}).
		WithExec([]string{"python", "-m", "http.server"})

	hosts := map[string]int{}

	for i := 0; i < 100; i++ {
		hostname, err := srv.Hostname(ctx)
		require.NoError(t, err)
		hosts[hostname]++
	}

	require.Len(t, hosts, 1)
}
