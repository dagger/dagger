package core

// These tests cover `dagger listen`, which serves a Dagger session over a local
// TCP listener. They verify authenticated requests, base API access without a
// module, and a workspace-backed module smoke test.
//
// See also:
// - module_loading_test.go: current-directory module nomination and mains.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
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
	t.Run("serves explicit workspace main", func(ctx context.Context, t *testctx.T) {
		workspaceDir := t.TempDir()
		initGitRepo(ctx, t, workspaceDir)
		copyTestdataFixture(ctx, t, workspaceDir, "workspaces", "listen-greeter")

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
		initGitRepo(ctx, t, workspaceDir)
		copyTestdataFixture(ctx, t, workspaceDir, "workspaces", "listen-greeter")

		addr, token := startListenSession(ctx, t, workspaceDir, "--disable-host-read-write")

		out, err := queryViaListenSession(ctx, t, t.TempDir(), addr, token, fmt.Sprintf(`query{container{from(address:"%s"){file(path:"/etc/alpine-release"){contents}}}}`, alpineImage))
		require.NoError(t, err)
		require.Contains(t, string(out), distconsts.AlpineVersion)
	})
}

func startListenSession(ctx context.Context, t *testctx.T, workdir string, extraArgs ...string) (string, string) {
	t.Helper()

	addr := reserveListenAddr(t)
	token := "listen-" + rand.Text()

	args := append([]string{"listen"}, extraArgs...)
	args = append(args, "--listen", addr)

	listenCmd := hostDaggerCommandRaw(ctx, t, workdir, args...)
	listenCmd.Env = append(listenCmd.Env, "DAGGER_SESSION_TOKEN="+token)
	listenCmd.Stdout = testutil.NewTWriter(t)
	listenCmd.Stderr = testutil.NewTWriter(t)
	require.NoError(t, listenCmd.Start())

	waitForListenSession(ctx, t, addr, token)
	return addr, token
}

// waitForListenSession waits until the session answers an authenticated
// query with the expected result. A bare TCP connect isn't enough: the address
// was only reserved and released, so another test's listener, or a loopback
// self-connect, can answer on it before the CLI binds it.
func waitForListenSession(ctx context.Context, t *testctx.T, addr, token string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	client := &http.Client{
		Transport: &http.Transport{DialContext: dialListenSession},
		Timeout:   5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer client.CloseIdleConnections()

	var lastErr error
	err := backoff.Retry(func() error {
		lastErr = probeListenSession(ctx, client, addr, token)
		return lastErr
	}, backoff.WithContext(backoff.NewExponentialBackOff(), ctx))
	require.NoError(t, err, "dagger listen at %s never answered; last probe: %v", addr, lastErr)
}

func probeListenSession(ctx context.Context, client *http.Client, addr, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/query", strings.NewReader(`{"query":"{__typename}"}`))
	if err != nil {
		return backoff.Permanent(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(token, "")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s", resp.Status)
	}
	var result struct {
		Data struct {
			Typename string `json:"__typename"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if result.Data.Typename != "Query" || len(result.Errors) > 0 {
		return fmt.Errorf("unexpected response: __typename %q, %d errors", result.Data.Typename, len(result.Errors))
	}
	return nil
}

// dialListenSession dials the session, refusing a loopback self-connect: with
// nothing bound to the port, the kernel can pick it as the dial's own source
// port and connect the socket to itself.
func dialListenSession(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	if conn.LocalAddr().String() == conn.RemoteAddr().String() {
		conn.Close()
		return nil, fmt.Errorf("self-connect to %s", address)
	}
	return conn, nil
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
