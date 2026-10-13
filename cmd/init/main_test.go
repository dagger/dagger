//go:build linux

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/engine/distconsts"
)

const (
	runAsInitEnv = "_DAGGER_INIT_TEST_RUN_AS_INIT"
	// testHelperEnv sets how this test binary behaves when /.init starts it as
	// the session helper: "hang" or "exit:<status>".
	testHelperEnv = "_DAGGER_INIT_TEST_SESSION_HELPER"
	// testHelperPidFileEnv names a file the session helper writes its pid to.
	testHelperPidFileEnv = "_DAGGER_INIT_TEST_SESSION_HELPER_PID_FILE"
	// testHelperPrintEnv is text the session helper writes to its stdout and
	// stderr when it starts.
	testHelperPrintEnv = "_DAGGER_INIT_TEST_SESSION_HELPER_PRINT"
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
		if pidFile := os.Getenv(testHelperPidFileEnv); pidFile != "" {
			os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600)
		}
		if text := os.Getenv(testHelperPrintEnv); text != "" {
			fmt.Fprint(os.Stdout, text)
			fmt.Fprint(os.Stderr, text)
		}
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

// Shell snippets for commands to synchronize with the session helper through
// its pid file, in testHelperPidFileEnv, each bounded to about 20 seconds.
const (
	// waitHelperStarted waits for the helper to write its pid file.
	waitHelperStarted = `i=0; while [ ! -s "$` + testHelperPidFileEnv + `" ] && [ $i -lt 2000 ]; do sleep 0.01; i=$((i+1)); done; `
	// waitHelperGone waits for the helper to have exited and been reaped,
	// which /.init reports on before handling anything else.
	waitHelperGone = waitHelperStarted + `p=$(cat "$` + testHelperPidFileEnv + `"); i=0; while kill -0 "$p" 2>/dev/null && [ $i -lt 2000 ]; do sleep 0.01; i=$((i+1)); done; `
)

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

	cmd := exec.Command(os.Args[0], "sh", "-c", waitHelperGone+"echo ran")
	cmd.Env = initEnv(
		"DAGGER_SESSION_TOKEN=token", "DAGGER_SESSION_PORT=1", testHelperEnv+"=exit:3",
		testHelperPidFileEnv+"="+filepath.Join(t.TempDir(), "helper.pid"),
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

// /.init stops and reaps the session helper before it exits, whether the
// command ran or failed to start, and keeps the command's exit status.
func TestInitStopsSessionHelper(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		status  int
		started bool // the helper certainly started before /.init exited
	}{
		{"command exits", []string{"sh", "-c", waitHelperStarted + "exit 5"}, 5, true},
		{"command fails to start", []string{"/does/not/exist"}, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "helper.pid")
			cmd := exec.Command(os.Args[0], tc.args...)
			cmd.Env = initEnv(
				"DAGGER_SESSION_TOKEN=token", "DAGGER_SESSION_PORT=1",
				testHelperEnv+"=hang", testHelperPidFileEnv+"="+pidFile,
			)
			err := cmd.Run()
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			require.Equal(t, tc.status, exitErr.ExitCode())

			if tc.started {
				_, err := os.Stat(pidFile)
				require.NoError(t, err, "the session helper didn't start")
			}
			// The helper's environment carries pidFile, which is unique to
			// this test.
			left := processesWithEnv(t, testHelperPidFileEnv+"="+pidFile)
			for _, pid := range left {
				unix.Kill(pid, unix.SIGKILL)
			}
			require.Empty(t, left, "the session helper outlived /.init")
		})
	}
}

// processesWithEnv returns the live processes whose environment has kv.
func processesWithEnv(t *testing.T, kv string) []int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	require.NoError(t, err)
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		env, err := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		if err != nil {
			continue
		}
		for _, v := range strings.Split(string(env), "\x00") {
			if v == kv {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids
}

// The session helper's output doesn't reach the command's stdout or stderr.
func TestInitKeepsSessionHelperOutputPrivate(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "helper.pid")
	cmd := exec.Command(os.Args[0], "sh", "-c", waitHelperStarted+"echo ran; echo oops >&2")
	cmd.Env = initEnv(
		"DAGGER_SESSION_TOKEN=token", "DAGGER_SESSION_PORT=1",
		testHelperEnv+"=hang", testHelperPidFileEnv+"="+pidFile, testHelperPrintEnv+"=helper says hi\n",
	)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	require.NoError(t, cmd.Run())
	_, err := os.Stat(pidFile)
	require.NoError(t, err, "the session helper didn't start")
	require.Equal(t, "ran\n", stdout.String())
	require.Equal(t, "oops\n", stderr.String())
}

// When the session helper exits while the command runs, /.init reports what
// it wrote along with its status.
func TestInitReportsSessionHelperOutput(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()

	cmd := exec.Command(os.Args[0], "sh", "-c", waitHelperGone+"echo ran")
	cmd.Env = initEnv(
		"DAGGER_SESSION_TOKEN=token", "DAGGER_SESSION_PORT=1", testHelperEnv+"=exit:2",
		testHelperPidFileEnv+"="+filepath.Join(t.TempDir(), "helper.pid"),
		testHelperPrintEnv+"=no session for you\n", distconsts.SessionHelperStatusFDEnv+"=3",
	)
	cmd.ExtraFiles = []*os.File{w}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	w.Close()
	require.NoError(t, err)
	require.Equal(t, "ran\n", string(out))
	require.Empty(t, stderr.String())

	report, err := bufio.NewReader(r).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "exited 2 "+strconv.Quote("no session for you\nno session for you\n")+"\n", report)
}
