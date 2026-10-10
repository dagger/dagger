//go:build linux

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/distconsts"
)

const (
	runAsInitEnv = "_DAGGER_INIT_TEST_RUN_AS_INIT"
	// testHelperEnv sets how this test binary behaves when /.init starts it as
	// the session helper: "hang" or "exit:<status>".
	testHelperEnv = "_DAGGER_INIT_TEST_SESSION_HELPER"
)

// TestMain runs the test binary as /.init when a test starts it with
// runAsInitEnv set; its remaining arguments are the command. With
// testHelperEnv set, that /.init starts this binary again as its session
// helper, which acts as testHelperEnv says; without it, there is no helper.
func TestMain(m *testing.M) {
	if os.Getenv(runAsInitEnv) != "" {
		os.Unsetenv(runAsInitEnv)
		if _, ok := os.LookupEnv(testHelperEnv); ok {
			sessionHelperPath = os.Args[0]
		}
		os.Args = append([]string{"/.init"}, os.Args[1:]...)
		if err := mainInit(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if helper, ok := os.LookupEnv(testHelperEnv); ok {
		if status, ok := strings.CutPrefix(helper, "exit:"); ok {
			code, _ := strconv.Atoi(status)
			os.Exit(code)
		}
		// Never ready: keep running, without holding the test's pipes open.
		os.Stdout.Close()
		os.Stderr.Close()
		time.Sleep(time.Minute)
		os.Exit(0)
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
	cmd.Env = initEnv(distconsts.InitTimingFDEnv + "=3")
	cmd.ExtraFiles = []*os.File{w}
	before := monotonicNS()
	out, err := cmd.Output()
	after := monotonicNS()
	w.Close()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, 3, exitErr.ExitCode(), "the command's exit status passes through")
	require.NotContains(t, string(out), distconsts.InitTimingFDEnv, "the command doesn't inherit the variable")
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
	cmd.Env = initEnv(distconsts.InitTimingFDEnv + "=1")
	out, err := cmd.Output()
	require.NoError(t, err)
	require.NotContains(t, string(out), distconsts.InitTimingFDEnv)
	require.NotRegexp(t, `^\d+ \d+ \d+\n`, string(out), "nothing is written to stdout")
}

// /.init runs before every exec's command, so all its dependencies'
// initialization is paid per exec: keep it to the standard library and
// x/sys. The session attachables live in cmd/init-session.
func TestInitImportsStayMinimal(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").Output()
	require.NoError(t, err)
	for _, pkg := range strings.Fields(string(out)) {
		switch {
		case pkg == "github.com/dagger/dagger/cmd/init",
			pkg == "github.com/dagger/dagger/engine/distconsts",
			strings.HasPrefix(pkg, "golang.org/x/sys/"):
		default:
			t.Errorf("/.init depends on %s", pkg)
		}
	}
}

func TestInitWithoutSessionHelper(t *testing.T) {
	// The command's env names a session, but the engine mounted no session
	// helper (it only does for nested clients): the command still runs.
	cmd := exec.Command(os.Args[0], "sh", "-c", `echo ran; exit 4`)
	cmd.Env = initEnv("DAGGER_SESSION_TOKEN=token", "DAGGER_SESSION_PORT=1")
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, 4, exitErr.ExitCode())
	require.Equal(t, "ran\n", string(out))
}

// /.init starts the session helper alongside the command: the command runs
// even though the helper never gets ready.
func TestInitDoesNotWaitForSessionHelper(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "sh", "-c", "echo ran")
	cmd.Env = initEnv("DAGGER_SESSION_TOKEN=token", "DAGGER_SESSION_PORT=1", testHelperEnv+"=hang")
	start := time.Now()
	out, err := cmd.Output()
	require.NoError(t, err)
	require.Equal(t, "ran\n", string(out))
	require.Less(t, time.Since(start), 10*time.Second, "the command waited for the session helper")
}

// When the session helper exits, /.init reports its status to the engine,
// and the command keeps running.
func TestInitReportsSessionHelperExit(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()

	cmd := exec.Command(os.Args[0], "sh", "-c", "sleep 1; echo ran")
	cmd.Env = initEnv(
		"DAGGER_SESSION_TOKEN=token", "DAGGER_SESSION_PORT=1", testHelperEnv+"=exit:3",
		distconsts.SessionHelperStatusFDEnv+"=3",
	)
	cmd.ExtraFiles = []*os.File{w}
	out, err := cmd.Output()
	w.Close()
	require.NoError(t, err)
	require.Equal(t, "ran\n", string(out))

	report, err := bufio.NewReader(r).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "exited 3\n", report)
}
