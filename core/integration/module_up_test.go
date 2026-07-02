package core

// These tests cover `dagger call ... up` and `dagger shell ... up` for module
// functions that return containers or services. They verify endpoint discovery,
// port mapping, and serving module results for local development.
//
// See also:
// - module_terminal_test.go: terminal attachment during module calls.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// Note: ensure each testcase use unique port, otherwise you might see flakes.
func (ModuleSuite) TestDaggerUp(ctx context.Context, t *testctx.T) {
	// these tests are slow if you're running locally, skip if -short is specified
	if testing.Short() {
		t.SkipNow()
	}
	// set timeout to 3m for each test
	t = t.Using(testctx.WithTimeout[*testing.T](3 * time.Minute))

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
			daggerArgs:   []string{"-m", ".", "call", "ctr", "up"},
			cachedModDir: modDirForAsContainerTests,
		},
		{
			name:         "container random",
			endpointFn:   daggerUpAndGetEndpointFromLogs,
			trafficPort:  "",
			daggerArgs:   []string{"-m", ".", "call", "ctr", "up", "--random"},
			cachedModDir: modDirForAsContainerTests,
		},
		{
			name:         "container port map",
			endpointFn:   daggerUpAndGetEndpoint,
			trafficPort:  "23101",
			daggerArgs:   []string{"-m", ".", "call", "ctr", "up", "--ports", fmt.Sprintf("23101:%s", defaultTrafficPortForContainerTests)},
			cachedModDir: modDirForAsContainerTests,
		},
		{
			name:         "container port map with same front+back",
			endpointFn:   daggerUpAndGetEndpoint,
			trafficPort:  "23102",
			daggerArgs:   []string{"-m", ".", "call", "--port", "23102", "ctr", "up", "--ports", "23102:23102"},
			cachedModDir: modDirForAsContainerTests,
		},
		{
			name:         "container port map with explicit args",
			endpointFn:   daggerUpAndGetEndpoint,
			trafficPort:  "23103",
			daggerArgs:   []string{"-m", ".", "call", "ctr", "without-exposed-port", "--port", defaultTrafficPortForContainerTests, "with-exposed-port", "--port", "23103", "up", "--args", "python,-m,http.server,23103", "--ports", "23103:23103"},
			cachedModDir: modDirForAsContainerTests,
		},
		{
			name:         "shell container port map",
			endpointFn:   daggerUpAndGetEndpoint,
			trafficPort:  "23104",
			daggerArgs:   []string{"-m", ".", "shell", "-c", fmt.Sprintf("ctr | up --ports 23104:%s", defaultTrafficPortForContainerTests)},
			cachedModDir: modDirForAsContainerTests,
		},
		{
			name:         "service native",
			endpointFn:   daggerUpAndGetEndpoint,
			trafficPort:  "23200",
			daggerArgs:   []string{"-m", ".", "call", "ctr", "as-service", "up"},
			cachedModDir: modDirForAsServiceTests,
		},
		{
			name:         "service random",
			endpointFn:   daggerUpAndGetEndpointFromLogs,
			trafficPort:  "",
			daggerArgs:   []string{"-m", ".", "call", "ctr", "as-service", "up", "--random"},
			cachedModDir: modDirForAsServiceTests,
		},
		{
			name:         "service port map",
			endpointFn:   daggerUpAndGetEndpoint,
			trafficPort:  "23201",
			daggerArgs:   []string{"-m", ".", "call", "ctr", "as-service", "up", "--ports", fmt.Sprintf("23201:%s", defaultTrafficPortForServiceTests)},
			cachedModDir: modDirForAsServiceTests,
		},
		{
			name:         "service port map with same front+back",
			endpointFn:   daggerUpAndGetEndpoint,
			trafficPort:  "23202",
			daggerArgs:   []string{"-m", ".", "call", "--port", "23202", "ctr", "as-service", "up", "--ports", "23202:23202"},
			cachedModDir: modDirForAsServiceTests,
		},
	}

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
	cmd := hostDaggerCommandRaw(ctx, t, modDir, daggerArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Start()
	require.NoError(t, err)

	return fmt.Sprintf("http://127.0.0.1:%s", trafficPort)
}

var tunnelPortRegex = regexp.MustCompile(`tunnel started port=(\d+)`)

// Starts container and return the random port used for the tunnel
func daggerUpAndGetEndpointFromLogs(ctx context.Context, t *testctx.T, modDir string, daggerArgs []string, trafficPort string) string {
	var logs safeBuffer
	cmd := hostDaggerCommandRaw(ctx, t, modDir, daggerArgs...)
	cmd.Env = append(cmd.Env, "NO_COLOR=true", "DAGGER_PROGRESS=logs")
	cmd.Stdin = nil
	cmd.Stdout = &logs
	cmd.Stderr = &logs

	err := cmd.Start()
	require.NoError(t, err)

	var port string
	require.Eventually(t, func() bool {
		matches := tunnelPortRegex.FindStringSubmatch(logs.String())
		if len(matches) == 0 {
			return false
		}
		port = matches[1]
		t.Logf("random port: %s", port)
		return true
	}, time.Minute, time.Second)

	return fmt.Sprintf("http://127.0.0.1:%s", port)
}

// create a dagger module for DaggerUp test
func daggerUpInitModFn(ctx context.Context, t *testctx.T, defaultPort string) string {
	modDir := t.TempDir()
	copyTestdataFixture(ctx, t, modDir, "modules", "go", "module-up-"+defaultPort)

	// cache the module load itself so there's less to wait for below
	_, err := hostDaggerExecRaw(ctx, t, modDir, "-m", ".", "api", "functions")
	require.NoError(t, err)

	return modDir
}
