package core

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServices(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	www := c.Directory().WithNewFile("index.html", "Hello, world!")

	// NB: for now I'm just making this a Container, ignoring the Container vs
	// Service argument to stay focused
	srv := c.Container().
		From("python").
		WithMountedDirectory("/srv/www", www).
		WithWorkdir("/srv/www").
		WithExec([]string{"sh", "-exc", "env; hostname; python -m http.server"})
		// optional: not a fundamental requirement, but I suspect named ports will
		// be easier to work with + refactor across abstraction boundaries.
		// WithExposedPort("http", 8080)

	hostname, err := srv.Hostname(ctx)
	require.NoError(t, err)

	url, err := srv.Endpoint(ctx, 8000, "http")
	require.NoError(t, err)
	require.Equal(t, "http://"+hostname+":8000", url)

	srvCtx, cancelSrv := context.WithCancel(ctx)
	defer cancelSrv()

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		srv.ExitCode(srvCtx)
	}()

	client := c.Container().
		From("alpine").
		WithServiceDependency(srv).
		WithExec([]string{"apk", "add", "curl"}).
		WithExec([]string{"curl", "-s", url})

	out, err := client.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "Hello, world!", out)

	cancelSrv()
	wg.Wait()
}
