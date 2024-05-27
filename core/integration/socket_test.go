package core

import (
	"context"
	_ "embed"
	"errors"
	"io"
	"net"
	"path/filepath"

	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

//go:embed testdata/socket-echo.go
var echoSocketSrc string

func (ContainerSuite) TestWithUnixSocket(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	tmp := t.TempDir()
	sock := filepath.Join(tmp, "test.sock")

	l, err := net.Listen("unix", sock)
	require.NoError(t, err)

	defer l.Close()

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Logf("accept: %s", err)
					panic(err)
				}
				return
			}

			n, err := io.Copy(c, c)
			if err != nil {
				t.Logf("hello: %s", err)
				panic(err)
			}

			t.Logf("copied %d bytes", n)

			err = c.Close()
			if err != nil {
				t.Logf("close: %s", err)
				panic(err)
			}
		}
	}()

	echo := c.Directory().WithNewFile("main.go", echoSocketSrc).File("main.go")

	ctr := c.Container().
		From(golangImage).
		WithMountedFile("/src/main.go", echo).
		WithUnixSocket("/tmp/test.sock", c.Host().UnixSocket(sock)).
		WithExec([]string{"go", "run", "/src/main.go", "/tmp/test.sock", "hello"})

	stdout, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello\n", stdout)

	t.Run("socket can be removed", func(ctx context.Context, t *testctx.T) {
		without := ctr.WithoutUnixSocket("/tmp/test.sock").
			WithExec([]string{"ls", "/tmp"})

		stdout, err = without.Stdout(ctx)
		require.NoError(t, err)
		require.Empty(t, stdout)
	})

	t.Run("replaces existing socket at same path", func(ctx context.Context, t *testctx.T) {
		repeated := ctr.WithUnixSocket("/tmp/test.sock", c.Host().UnixSocket(sock))

		stdout, err := repeated.Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello\n", stdout)

		without := repeated.WithoutUnixSocket("/tmp/test.sock").
			WithExec([]string{"ls", "/tmp"})

		stdout, err = without.Stdout(ctx)
		require.NoError(t, err)
		require.Empty(t, stdout)
	})
}

func (ContainerSuite) TestWithUnixSocketOwner(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	tmp := t.TempDir()
	sock := filepath.Join(tmp, "test.sock")

	l, err := net.Listen("unix", sock)
	require.NoError(t, err)

	defer l.Close()

	socket := c.Host().UnixSocket(sock)

	testOwnership(t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
		return ctr.WithUnixSocket(name, socket, dagger.ContainerWithUnixSocketOpts{
			Owner: owner,
		})
	})
}
