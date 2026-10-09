package engineconn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCLISessionEnvironmentChild(t *testing.T) {
	if os.Getenv("ENGINECONN_ENV_CHILD") != "1" {
		return
	}
	values := map[string]*string{}
	for _, key := range []string{"REMOVE_ME", "KEEP_ME", "EXPLICIT", "_EXPERIMENTAL_DAGGER_RUNNER_HOST"} {
		if value, ok := os.LookupEnv(key); ok {
			values[key] = &value
		} else {
			values[key] = nil
		}
	}
	data, err := json.Marshal(values)
	if err != nil {
		os.Exit(1)
	}
	if report := os.Getenv("ENGINECONN_ENV_REPORT"); report != "" {
		if os.WriteFile(report, data, 0o600) != nil {
			os.Exit(1)
		}
		fmt.Println(`{"port":12345,"session_token":"test"}`)
		_, _ = io.Copy(io.Discard, os.Stdin)
	} else {
		fmt.Println(string(data))
	}
	os.Exit(0)
}

func TestCLISessionEnvRemovesInheritedVariables(t *testing.T) {
	t.Parallel()
	inherited := []string{"REMOVE_ME=secret", "KEEP_ME=kept", "EXPLICIT=inherited", "ENGINECONN_ENV_CHILD=1"}
	cfg := &Config{UnsetEnv: []string{"REMOVE_ME", "EXPLICIT"}, ExtraEnv: []string{"EXPLICIT=allowed"}, RunnerHost: "tcp://engine:1234"}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCLISessionEnvironmentChild$")
	cmd.Env = cliSessionEnv(inherited, cfg)
	out, err := cmd.Output()
	require.NoError(t, err)
	var values map[string]*string
	require.NoError(t, json.Unmarshal(out, &values))
	require.Nil(t, values["REMOVE_ME"], "removed is absent, not merely empty")
	require.Equal(t, "kept", *values["KEEP_ME"])
	require.Equal(t, "allowed", *values["EXPLICIT"], "explicit extra environment wins")
	require.Equal(t, cfg.RunnerHost, *values["_EXPERIMENTAL_DAGGER_RUNNER_HOST"])
	require.Equal(t, "REMOVE_ME=secret", inherited[0], "input slice remains unchanged")
}

func TestGetPreservesUnsetEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test CLI wrapper uses sh")
	}
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("Get intentionally prefers an existing session")
	}
	bin, err := os.Executable()
	require.NoError(t, err)
	wrapper := filepath.Join(t.TempDir(), "dagger")
	require.NoError(t, os.WriteFile(wrapper, []byte("#!/bin/sh\nexec \""+bin+"\" -test.run=^TestCLISessionEnvironmentChild$\n"), 0o700))
	t.Setenv("_EXPERIMENTAL_DAGGER_CLI_BIN", wrapper)
	t.Setenv("REMOVE_ME", "secret")
	t.Setenv("KEEP_ME", "kept")
	report := filepath.Join(t.TempDir(), "environment.json")
	conn, err := Get(t.Context(), &Config{UnsetEnv: []string{"REMOVE_ME"}, ExtraEnv: []string{"ENGINECONN_ENV_CHILD=1", "ENGINECONN_ENV_REPORT=" + report}})
	require.NoError(t, err)
	defer conn.Close()
	data, err := os.ReadFile(report)
	require.NoError(t, err)
	var values map[string]*string
	require.NoError(t, json.Unmarshal(data, &values))
	require.Nil(t, values["REMOVE_ME"], "Get must retain UnsetEnv when normalizing config")
	require.Equal(t, "kept", *values["KEEP_ME"])
	require.Equal(t, "secret", os.Getenv("REMOVE_ME"), "parent environment is untouched")
}

// TestCLISessionArgsIncludeWorkspace verifies that session provisioning forwards
// workspace selection as a dedicated CLI flag.
func TestCLISessionArgsIncludeWorkspace(t *testing.T) {
	t.Parallel()

	args := cliSessionArgs(&Config{
		Workspace: "github.com/acme/ws",
	})

	require.Contains(t, args, "--workspace")
	require.Contains(t, args, "github.com/acme/ws")
}

func TestCLISessionArgsIncludeLoadWorkspaceModules(t *testing.T) {
	t.Parallel()

	args := cliSessionArgs(&Config{
		LoadWorkspaceModules: true,
	})

	require.Contains(t, args, "--load-workspace-modules")
	require.NotContains(t, args, "--skip-workspace-modules")
}

// TestGetRejectsWorkspaceForExistingSession verifies that an existing session's
// workspace binding cannot be overridden by client config.
func TestGetRejectsWorkspaceForExistingSession(t *testing.T) {
	t.Setenv("DAGGER_SESSION_PORT", "1234")
	t.Setenv("DAGGER_SESSION_TOKEN", "secret")

	_, err := Get(context.Background(), &Config{
		Workspace: "github.com/acme/ws",
	})
	require.ErrorContains(t, err, "cannot configure workspace for existing session")
}

func TestGetRejectsWorkspaceModuleLoadingForExistingSession(t *testing.T) {
	t.Setenv("DAGGER_SESSION_PORT", "1234")
	t.Setenv("DAGGER_SESSION_TOKEN", "secret")

	_, err := Get(context.Background(), &Config{
		LoadWorkspaceModules: true,
	})
	require.ErrorContains(t, err, "cannot configure workspace module loading for existing session")
}

// testSessionParams is a shell command that prints the connect params a test
// CLI session must emit before startCLISession returns.
const testSessionParams = `echo '{"port":12345,"session_token":"test"}'`

// startTestSession runs script as the dagger CLI of a new session.
func startTestSession(t *testing.T, script string, logOutput io.Writer) EngineConn {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test CLI uses sh")
	}
	bin := filepath.Join(t.TempDir(), "dagger")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o700))
	conn, err := startCLISession(t.Context(), bin, &Config{LogOutput: logOutput})
	require.NoError(t, err)
	return conn
}

func setSessionWaitDelay(t *testing.T, d time.Duration) {
	t.Helper()
	orig := sessionWaitDelay
	sessionWaitDelay = d
	t.Cleanup(func() { sessionWaitDelay = orig })
}

type slowWriter struct {
	w io.Writer
}

func (w slowWriter) Write(p []byte) (int, error) {
	time.Sleep(time.Millisecond)
	return w.w.Write(p)
}

// TestCLISessionCloseWritesStderrTail verifies that Close returns only after
// everything the session wrote to stderr reached the log output, even when the
// log output is slower than the session.
func TestCLISessionCloseWritesStderrTail(t *testing.T) {
	logs := &safeBuffer{}
	conn := startTestSession(t, testSessionParams+`
cat >/dev/null
i=0
while [ $i -lt 2000 ]; do
  echo "line $i" >&2
  i=$((i+1))
done
echo FINAL-ERROR >&2
`, slowWriter{logs})
	require.NoError(t, conn.Close())
	require.Contains(t, logs.String(), "line 1999\nFINAL-ERROR\n")
}

func TestCLISessionCloseNonzeroExit(t *testing.T) {
	logs := &safeBuffer{}
	conn := startTestSession(t, testSessionParams+`
cat >/dev/null
echo FINAL-ERROR >&2
exit 3
`, logs)
	require.ErrorContains(t, conn.Close(), "exit status 3")
	require.Contains(t, logs.String(), "FINAL-ERROR\n")
}

// TestCLISessionCloseKillsUnresponsiveSession verifies that Close kills a
// session that does not exit after its stdin is closed.
func TestCLISessionCloseKillsUnresponsiveSession(t *testing.T) {
	setSessionWaitDelay(t, time.Second)
	logs := &safeBuffer{}
	conn := startTestSession(t, `echo started >&2
`+testSessionParams+`
exec sleep 60
`, logs)
	start := time.Now()
	require.ErrorContains(t, conn.Close(), "signal: killed")
	require.Less(t, time.Since(start), 30*time.Second)
	require.Contains(t, logs.String(), "started\n")
}

// TestCLISessionCloseInheritedStderr verifies that Close does not wait for a
// leftover subprocess that inherited the session's stderr beyond WaitDelay.
func TestCLISessionCloseInheritedStderr(t *testing.T) {
	setSessionWaitDelay(t, 3*time.Second)
	pidFile := filepath.Join(t.TempDir(), "pid")
	logs := &safeBuffer{}
	conn := startTestSession(t, `sleep 60 &
echo $! > '`+pidFile+`'
`+testSessionParams+`
cat >/dev/null
echo FINAL-ERROR >&2
`, logs)
	t.Cleanup(func() {
		data, err := os.ReadFile(pidFile)
		require.NoError(t, err)
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		require.NoError(t, err)
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Kill()
		}
	})
	start := time.Now()
	require.NoError(t, conn.Close())
	require.Less(t, time.Since(start), 30*time.Second)
	require.Contains(t, logs.String(), "FINAL-ERROR\n")
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("log output broken")
}

// TestCLISessionCloseLogOutputError verifies that a failing log output neither
// blocks the session on a full stderr pipe nor goes unreported.
func TestCLISessionCloseLogOutputError(t *testing.T) {
	setSessionWaitDelay(t, 30*time.Second)
	conn := startTestSession(t, testSessionParams+`
cat >/dev/null
head -c 1048576 /dev/zero >&2
`, failingWriter{})
	start := time.Now()
	require.ErrorContains(t, conn.Close(), "write log output: log output broken")
	require.Less(t, time.Since(start), 20*time.Second)
}
