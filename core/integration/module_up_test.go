package core

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/creack/pty"
	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestDaggerUp(ctx context.Context, t *testctx.T) {
	modDir := t.TempDir()
	err := os.WriteFile(filepath.Join(modDir, "main.go"), []byte(`package main
import (
	"strconv"
	"dagger/test/internal/dagger"
)

func New(
	// +optional
	// +default=23457
	port int,
) *Test {
	return &Test{
		Ctr: dag.Container().
			From("python").
			WithMountedDirectory(
				"/srv/www",
				dag.Directory().WithNewFile("index.html", "hey there"),
			).
			WithWorkdir("/srv/www").
			WithExposedPort(port).
			WithExec([]string{"python", "-m", "http.server", strconv.Itoa(port)}),
	}
}

type Test struct {
	Ctr *dagger.Container
}
`), 0o644)
	require.NoError(t, err)

	_, err = hostDaggerExec(ctx, t, modDir, "--debug", "init", "--source=.", "--name=test", "--sdk=go")
	require.NoError(t, err)

	// cache the module load itself so there's less to wait for below
	_, err = hostDaggerExec(ctx, t, modDir, "--debug", "functions")
	require.NoError(t, err)

	t = t.WithTimeout(3 * time.Minute)

	t.Run("native", func(ctx context.Context, t *testctx.T) {
		cmd := hostDaggerCommand(ctx, t, modDir, "call", "ctr", "as-service", "up")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		err := cmd.Start()
		require.NoError(t, err)
		cleanupExec(t, cmd)

		for {
			select {
			case <-ctx.Done():
				require.FailNow(t, "timed out waiting for container to start")
			default:
			}

			resp, err := http.Get("http://127.0.0.1:23457")
			if err != nil {
				t.Logf("waiting for container to start: %s", err)
				time.Sleep(time.Second)
				continue
			}
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, "hey there", string(body))
			break
		}
	})

	t.Run("random", func(ctx context.Context, t *testctx.T) {
		console, err := newTUIConsole(t, time.Minute)
		require.NoError(t, err)

		tty := console.Tty()

		err = pty.Setsize(tty, &pty.Winsize{Rows: 10, Cols: 80})
		require.NoError(t, err)

		cmd := hostDaggerCommand(ctx, t, modDir, "call", "ctr", "as-service", "up", "--random")
		cmd.Env = append(cmd.Env, "NO_COLOR=true")
		cmd.Stdin = nil
		cmd.Stdout = tty
		cmd.Stderr = tty

		err = cmd.Start()
		require.NoError(t, err)
		cleanupExec(t, cmd)

		_, matches, err := console.MatchLine(ctx, `tunnel started port=(\d+)`)
		require.NoError(t, err)

		port := matches[1]
		t.Logf("random port: %s", port)

		for {
			select {
			case <-ctx.Done():
				require.FailNow(t, "timed out waiting for container to start")
			default:
			}

			resp, err := http.Get("http://127.0.0.1:" + port)
			if err != nil {
				t.Logf("waiting for container to start: %s", err)
				time.Sleep(time.Second)
				continue
			}
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, "hey there", string(body))
			break
		}
	})

	t.Run("port map", func(ctx context.Context, t *testctx.T) {
		cmd := hostDaggerCommand(ctx, t, modDir, "call", "ctr", "as-service", "up", "--ports", "23458:23457")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		err := cmd.Start()
		require.NoError(t, err)
		cleanupExec(t, cmd)

		for {
			select {
			case <-ctx.Done():
				require.FailNow(t, "timed out waiting for container to start")
			default:
			}

			resp, err := http.Get("http://127.0.0.1:23458")
			if err != nil {
				t.Logf("waiting for container to start: %s", err)
				time.Sleep(time.Second)
				continue
			}
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, "hey there", string(body))
			break
		}
	})

	t.Run("port map with same front+back", func(ctx context.Context, t *testctx.T) {
		cmd := hostDaggerCommand(ctx, t, modDir, "call", "--port", "23459", "ctr", "as-service", "up", "--ports", "23459:23459")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		err := cmd.Start()
		require.NoError(t, err)
		cleanupExec(t, cmd)

		for {
			select {
			case <-ctx.Done():
				require.FailNow(t, "timed out waiting for container to start")
			default:
			}

			resp, err := http.Get("http://127.0.0.1:23459")
			if err != nil {
				t.Logf("waiting for container to start: %s", err)
				time.Sleep(time.Second)
				continue
			}
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, "hey there", string(body))
			break
		}
	})
}
