//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine"
)

const runAsInitEnv = "_DAGGER_INIT_TEST_RUN_AS_INIT"

// TestMain runs the test binary as /.init when a test starts it with
// runAsInitEnv set; its remaining arguments are the command.
func TestMain(m *testing.M) {
	if os.Getenv(runAsInitEnv) != "" {
		os.Unsetenv(runAsInitEnv)
		os.Args = append([]string{"/.init"}, os.Args[1:]...)
		if err := mainInit(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}

// initEnv is the environment for running the test binary as /.init: the
// test's own, without a Dagger session (which would make /.init start its
// session subprocess, i.e. this test binary again), plus env.
func initEnv(env ...string) []string {
	var out []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "DAGGER_SESSION_") {
			out = append(out, kv)
		}
	}
	return append(append(out, runAsInitEnv+"=1"), env...)
}

func TestInitReportsTiming(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()

	cmd := exec.Command(os.Args[0], "sh", "-c", `sleep 0.2; env; test -e /proc/$$/fd/3 && echo fd3-leaked; exit 3`)
	cmd.Env = initEnv(engine.InitTimingFDEnv + "=3")
	cmd.ExtraFiles = []*os.File{w}
	before := monotonicNS()
	out, err := cmd.Output()
	after := monotonicNS()
	w.Close()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, 3, exitErr.ExitCode(), "the command's exit status passes through")
	require.NotContains(t, string(out), engine.InitTimingFDEnv, "the command doesn't inherit the variable")
	require.NotContains(t, string(out), "fd3-leaked", "the command doesn't inherit the fd")

	var started, spawned, exited int64
	_, err = fmt.Fscanf(r, "%d %d %d\n", &started, &spawned, &exited)
	require.NoError(t, err)
	require.Less(t, before, started)
	require.LessOrEqual(t, started, spawned)
	require.GreaterOrEqual(t, exited-spawned, int64(200*time.Millisecond), "the command ran between spawn and exit")
	require.Less(t, exited, after)
}

func TestInitWithoutTiming(t *testing.T) {
	cmd := exec.Command(os.Args[0], "sh", "-c", `env`)
	// An fd below 3 is never a timing pipe.
	cmd.Env = initEnv(engine.InitTimingFDEnv + "=1")
	out, err := cmd.Output()
	require.NoError(t, err)
	require.NotContains(t, string(out), engine.InitTimingFDEnv)
	require.NotRegexp(t, `^\d+ \d+ \d+\n`, string(out), "nothing is written to stdout")
}
