package core

// Workspace alignment: aligned; this suite owns the workspace-era `dagger listen` command surface.
// Scope: Host-side `dagger listen` session serving, including base API access without a module and one explicit-workspace module smoke test.
// Intent: Keep `dagger listen` behavior separate from CWD module nomination and entrypoint arbitration, which belong in module_loading_test.go.

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type ListenSuite struct{}

func TestListen(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ListenSuite{})
}

func (ListenSuite) TestDaggerListen(ctx context.Context, t *testctx.T) {
	t.Run("serves explicit workspace entrypoint", func(ctx context.Context, t *testctx.T) {
		workspaceDir := t.TempDir()
		initHostDangBlueprint(ctx, t, workspaceDir, "greeter", `
type Greeter {
  pub hello: String! {
    "hello from workspace"
  }
}
`)

		addr, token := startListenSession(ctx, t, workspaceDir)

		out, err := callViaListenSession(ctx, t, t.TempDir(), addr, token, "hello")
		require.NoError(t, err)
		require.Equal(t, "hello from workspace", strings.TrimSpace(string(out)))
	})

	t.Run("disable host read write serves base api without workspace", func(ctx context.Context, t *testctx.T) {
		addr, token := startListenSession(ctx, t, t.TempDir(), "--disable-host-read-write")

		out, err := queryViaListenSession(ctx, t, t.TempDir(), addr, token, fmt.Sprintf(`query{container{from(address:"%s"){file(path:"/etc/alpine-release"){contents}}}}`, alpineImage))
		require.NoError(t, err)
		require.Contains(t, string(out), distconsts.AlpineVersion)
	})

	t.Run("disable host read write still serves base api from workspace root", func(ctx context.Context, t *testctx.T) {
		workspaceDir := t.TempDir()
		initHostDangBlueprint(ctx, t, workspaceDir, "greeter", `
type Greeter {
  pub hello: String! {
    "hello from workspace"
  }
}
`)

		addr, token := startListenSession(ctx, t, workspaceDir, "--disable-host-read-write")

		out, err := queryViaListenSession(ctx, t, t.TempDir(), addr, token, fmt.Sprintf(`query{container{from(address:"%s"){file(path:"/etc/alpine-release"){contents}}}}`, alpineImage))
		require.NoError(t, err)
		require.Contains(t, string(out), distconsts.AlpineVersion)
	})
}

func startListenSession(ctx context.Context, t *testctx.T, workdir string, extraArgs ...string) (string, string) {
	t.Helper()

	addr := reserveListenAddr(t)
	token := "listen-test-token"

	args := append([]string{"listen"}, extraArgs...)
	args = append(args, "--listen", addr)

	listenCmd := hostDaggerCommandRaw(ctx, t, workdir, args...)
	listenCmd.Env = append(listenCmd.Env, "DAGGER_SESSION_TOKEN="+token)
	listenCmd.Stdout = testutil.NewTWriter(t)
	listenCmd.Stderr = testutil.NewTWriter(t)
	require.NoError(t, listenCmd.Start())

	waitForListenSession(t, addr)
	return addr, token
}

func waitForListenSession(t *testctx.T, addr string) {
	t.Helper()

	err := backoff.Retry(func() error {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return err
		}
		return conn.Close()
	}, backoff.NewExponentialBackOff(
		backoff.WithMaxElapsedTime(time.Minute),
	))
	require.NoError(t, err)
}

func reserveListenAddr(t *testctx.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()

	return l.Addr().String()
}

func queryViaListenSession(ctx context.Context, t *testctx.T, workdir, addr, token, query string) ([]byte, error) {
	t.Helper()

	cmd := hostDaggerCommandRaw(ctx, t, workdir, "query")
	cmd.Stdin = strings.NewReader(query)
	cmd.Stderr = testutil.NewTWriter(t)
	cmd.Env = append(cmd.Env, listenSessionEnv(t, addr, token)...)
	return cmd.Output()
}

func callViaListenSession(ctx context.Context, t *testctx.T, workdir, addr, token string, args ...string) ([]byte, error) {
	t.Helper()

	cmd := hostDaggerCommandRaw(ctx, t, workdir, append([]string{"call"}, args...)...)
	cmd.Stderr = testutil.NewTWriter(t)
	cmd.Env = append(cmd.Env, listenSessionEnv(t, addr, token)...)
	return cmd.Output()
}

func listenSessionEnv(t *testctx.T, addr, token string) []string {
	t.Helper()

	_, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)

	return []string{
		"DAGGER_SESSION_PORT=" + port,
		"DAGGER_SESSION_TOKEN=" + token,
	}
}
