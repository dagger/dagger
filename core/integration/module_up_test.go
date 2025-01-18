package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/creack/pty"
	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// Note: ensure each testcase use unique port, otherwise you might see flakes.
func (ModuleSuite) TestDaggerUp(ctx context.Context, t *testctx.T) {
	// set timeout to 3m for each test
	t = t.WithTimeout(3 * time.Minute)

	const defaultTrafficPortForContainerTests = "23100"
	const defaultTrafficPortForServiceTests = "23200"

	// need to give them different default ports to avoid port conflict
	// as tests run in parallel and try to claim same port otherwise
	modDirForAsContainerTests := daggerUpInitModFn(ctx, t, defaultTrafficPortForContainerTests)
	modDirForAsServiceTests := daggerUpInitModFn(ctx, t, defaultTrafficPortForServiceTests)

	testcases := []struct {
		name         string
		trafficPort  string
		daggerArgs   []string
		endpointFn   func(ctx context.Context, t *testctx.T, modDir string, daggerArgs []string, trafficPort string) string
		cachedModDir string
	}{
		{
			name:         "container native",
			endpointFn:   daggerUpAndGetEndpoint,
			trafficPort:  "23100",
			daggerArgs:   []string{"call", "ctr", "up"},
			cachedModDir: modDirForAsContainerTests,
		},
		{
			name:         "container random",
			endpointFn:   daggerUpAndGetEndpointFromLogs,
			trafficPort:  "",
			daggerArgs:   []string{"call", "ctr", "up", "--random"},
			cachedModDir: modDirForAsContainerTests,
		},
		{
			name:         "container port map",
			endpointFn:   daggerUpAndGetEndpoint,
			trafficPort:  "23101",
			daggerArgs:   []string{"call", "ctr", "up", "--ports", fmt.Sprintf("23101:%s", defaultTrafficPortForContainerTests)},
			cachedModDir: modDirForAsContainerTests,
		},
		{
			name:         "container port map with same front+back",
			endpointFn:   daggerUpAndGetEndpoint,
			trafficPort:  "23102",
			daggerArgs:   []string{"call", "--port", "23102", "ctr", "up", "--ports", "23102:23102"},
			cachedModDir: modDirForAsContainerTests,
		},
		{
			name:         "container port map with explicit args",
			endpointFn:   daggerUpAndGetEndpoint,
			trafficPort:  "23103",
			daggerArgs:   []string{"call", "ctr", "without-exposed-port", "--port", defaultTrafficPortForContainerTests, "with-exposed-port", "--port", "23103", "up", "--args", "python,-m,http.server,23103", "--ports", "23103:23103"},
			cachedModDir: modDirForAsContainerTests,
		},
		{
			name:         "service native",
			endpointFn:   daggerUpAndGetEndpoint,
			trafficPort:  "23200",
			daggerArgs:   []string{"call", "ctr", "as-service", "up"},
			cachedModDir: modDirForAsServiceTests,
		},
		{
			name:         "service random",
			endpointFn:   daggerUpAndGetEndpointFromLogs,
			trafficPort:  "",
			daggerArgs:   []string{"call", "ctr", "as-service", "up", "--random"},
			cachedModDir: modDirForAsServiceTests,
		},
		{
			name:         "service port map",
			endpointFn:   daggerUpAndGetEndpoint,
			trafficPort:  "23201",
			daggerArgs:   []string{"call", "ctr", "as-service", "up", "--ports", fmt.Sprintf("23201:%s", defaultTrafficPortForServiceTests)},
			cachedModDir: modDirForAsServiceTests,
		},
		{
			name:         "service port map with same front+back",
			endpointFn:   daggerUpAndGetEndpoint,
			trafficPort:  "23202",
			daggerArgs:   []string{"call", "--port", "23202", "ctr", "as-service", "up", "--ports", "23202:23202"},
			cachedModDir: modDirForAsServiceTests,
		},
	}

	t = t.WithTimeout(3 * time.Minute)

	for _, tc := range testcases {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			endpoint := tc.endpointFn(ctx, t, tc.cachedModDir, tc.daggerArgs, tc.trafficPort)

			for {
				select {
				case <-ctx.Done():
					require.FailNow(t, "timed out waiting for container to start")
				default:
				}

				resp, err := http.Get(endpoint)
				if err != nil {
					t.Logf("waiting for container to start: %s", err)
					time.Sleep(time.Second)
					continue
				}

				require.Equal(t, http.StatusOK, resp.StatusCode)

				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.Equal(t, "hey there", string(body))

				resp.Body.Close()

				break
			}
		})
	}
}

// Starts container and return endpoint with the expected tunnel port.
// In theory we can use daggerUpAndGetEndpointFromLogs to get port,
// but we want to test this with a pre-configured traffic port.
func daggerUpAndGetEndpoint(ctx context.Context, t *testctx.T, modDir string, daggerArgs []string, trafficPort string) string {
	cmd := hostDaggerCommand(ctx, t, modDir, daggerArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Start()
	require.NoError(t, err)

	return fmt.Sprintf("http://127.0.0.1:%s", trafficPort)
}

// Starts container and return the random port used for the tunnel
func daggerUpAndGetEndpointFromLogs(ctx context.Context, t *testctx.T, modDir string, daggerArgs []string, trafficPort string) string {
	console, err := newTUIConsole(t, time.Minute)
	require.NoError(t, err)

	tty := console.Tty()

	err = pty.Setsize(tty, &pty.Winsize{Rows: 10, Cols: 80})
	require.NoError(t, err)

	cmd := hostDaggerCommand(ctx, t, modDir, daggerArgs...)
	cmd.Env = append(cmd.Env, "NO_COLOR=true")
	cmd.Stdin = nil
	cmd.Stdout = tty
	cmd.Stderr = tty

	err = cmd.Start()
	require.NoError(t, err)

	_, matches, err := console.MatchLine(ctx, `tunnel started port=(\d+)`)
	require.NoError(t, err)

	port := matches[1]
	t.Logf("random port: %s", port)

	return fmt.Sprintf("http://127.0.0.1:%s", port)
}

// create a dagger module for DaggerUp test
func daggerUpInitModFn(ctx context.Context, t *testctx.T, defaultPort string) string {
	mainGoTmpl := `package main
	import (
		"strconv"
		"dagger/test/internal/dagger"
	)
	
	func New(
		// +optional
		// +default=%s
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
				WithDefaultArgs([]string{"python", "-m", "http.server", strconv.Itoa(port)}),
		}
	}
	
	type Test struct {
		Ctr *dagger.Container
	}
	`

	modDir := t.TempDir()
	err := os.WriteFile(filepath.Join(modDir, "main.go"), []byte(fmt.Sprintf(mainGoTmpl, defaultPort)), 0o644)
	require.NoError(t, err)

	_, err = hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
	require.NoError(t, err)

	// cache the module load itself so there's less to wait for below
	_, err = hostDaggerExec(ctx, t, modDir, "functions")
	require.NoError(t, err)

	return modDir
}
