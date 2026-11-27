package core

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
	core "dagger.io/dagger/core"
)

type InterruptSuite struct{}

func TestInterrupt(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(InterruptSuite{})
}

// interruptProcessScript runs as a service and as an exec. It records in
// /marks/$NAME that it started, and whether it got SIGTERM, then runs
// $ON_TERM: "exit 0" to stop gracefully, or a heartbeat in /marks/$NAME.beat
// to keep running until it is killed.
const interruptProcessScript = `trap 'echo got-SIGTERM > /marks/$NAME; eval "$ON_TERM"' TERM
echo started > /marks/$NAME
(while true; do nc -l -p 8080 < /dev/null; done) &
while true; do sleep 1; done
`

const interruptKeepRunning = `i=0; while true; do i=$((i+1)); echo $i > /marks/$NAME.beat; sleep 0.5; done`

// interruptModule starts a service and runs an exec that never ends, both
// running interruptProcessScript with the given cache volume at /marks.
var interruptModule = fmt.Sprintf(`type Stopper {
  run(marks: CacheVolume!, onTerm: String!): Void @cache(policy: FunctionCachePolicy.NEVER) {
    let ctr = container.from(%q)
      .withMountedCache("/marks", marks)
      .withEnvVariable("ON_TERM", onTerm)
    ctr.withEnvVariable("NAME", "service")
      .withExposedPort(8080)
      .asService(args: ["sh", "-c", %q])
      .start
    ctr.withEnvVariable("NAME", "exec")
      .withEnvVariable("NONCE", UUID.v4)
      .withExec(["sh", "-c", %q])
      .sync
    null
  }
}
`, alpineImage, interruptProcessScript, interruptProcessScript)

// interruptDriverScript runs the CLI in the background, waits until the
// service and the exec started, then sends the CLI SIGINT $SIGINTS times,
// the second once both processes got SIGTERM. It prints the CLI's exit code,
// the processes' records, whether they still run after the CLI exited, and
// how long the CLI took to exit after the first SIGINT.
const interruptDriverScript = `set -u
dagger --progress=plain $CLI_FLAGS call stopper run --marks "$MARKS" --on-term "$ON_TERM" > /tmp/cli.log 2>&1 &
pid=$!
waitfor() {
  i=0
  until grep -qx "$2" "/marks/$1" 2>/dev/null; do
    i=$((i+1))
    if [ $i -gt 480 ]; then echo "timeout=waiting for $1 to be $2"; cat /tmp/cli.log; exit 0; fi
    sleep 0.5
  done
}
waitfor service started
waitfor exec started
start=$(date +%s)
kill -INT $pid
if [ "$SIGINTS" = 2 ]; then
  waitfor service got-SIGTERM
  waitfor exec got-SIGTERM
  kill -INT $pid
fi
wait $pid
code=$?
elapsed=$(( $(date +%s) - start ))
beats() { cat /marks/service.beat /marks/exec.beat 2>/dev/null | tr '\n' ' '; }
before=$(beats)
sleep 3
after=$(beats)
running=no
if [ "$before" != "$after" ]; then running=yes; fi
echo "exit=$code"
echo "service=$(cat /marks/service)"
echo "exec=$(cat /marks/exec)"
echo "running=$running"
echo "elapsed=$elapsed"
`

type interruptRun struct {
	exit    string
	service string
	exec    string
	running string
	elapsed int
}

// runInterrupted runs the stopper module with a fresh CLI session, interrupts
// it, and reports what its service and exec saw.
func runInterrupted(ctx context.Context, t *testctx.T, c *dagger.Client, cliFlags, onTerm string, sigints int, userConfig string) interruptRun {
	t.Helper()
	marks := "interrupt-marks-" + rand.Text()
	ctr := workspaceBase(t, c).
		WithNewFile("dagger.toml", "[modules.stopper]\nsource = \".dagger/modules/stopper\"\n").
		WithNewFile(".dagger/modules/stopper/dagger-module.toml", "name = \"stopper\"\nengineVersion = \"latest\"\n\n[runtime]\nsource = \"dang\"\n").
		WithNewFile(".dagger/modules/stopper/main.dang", interruptModule).
		WithMountedCache("/marks", core.NewQuery(c).CacheVolume(marks)).
		WithEnvVariable("MARKS", marks).
		WithEnvVariable("CLI_FLAGS", cliFlags).
		WithEnvVariable("ON_TERM", onTerm).
		WithEnvVariable("SIGINTS", fmt.Sprint(sigints)).
		WithEnvVariable("XDG_STATE_HOME", "/tmp").
		WithMountedTemp("/tmp").
		With(nonNestedDevEngine(c))
	if userConfig != "" {
		ctr = ctr.
			WithNewFile("/config/dagger/config.toml", userConfig).
			WithEnvVariable("DAGGER_CONFIG", "/config/dagger/config.toml")
	}
	out, err := ctr.
		WithExec([]string{"sh", "-c", interruptDriverScript}, core.ContainerWithExecOpts{
			DisableDaggerInDagger: true,
		}).
		Stdout(ctx)
	require.NoError(t, err)

	var run interruptRun
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		key, value, _ := strings.Cut(line, "=")
		switch key {
		case "timeout":
			t.Fatalf("%s:\n%s", value, out)
		case "exit":
			run.exit = value
		case "service":
			run.service = value
		case "exec":
			run.exec = value
		case "running":
			run.running = value
		case "elapsed":
			fmt.Sscan(value, &run.elapsed)
		}
	}
	require.NotEmpty(t, run.exit, out)
	return run
}

// The first interrupt gives the run's service and its cancelled exec
// SIGTERM, and waits for them to stop.
func (InterruptSuite) TestFirstInterruptStopsGracefully(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	run := runInterrupted(ctx, t, c, "--stop-timeout=60s", "exit 0", 1, "")
	require.NotEqual(t, "0", run.exit, "an interrupted run fails")
	require.Equal(t, "got-SIGTERM", run.service)
	require.Equal(t, "got-SIGTERM", run.exec)
	require.Equal(t, "no", run.running)
	require.Less(t, run.elapsed, 60)
}

// A zero stop timeout kills the run's service and exec at once.
func (InterruptSuite) TestZeroStopTimeoutKills(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	run := runInterrupted(ctx, t, c, "--stop-timeout=0s", "exit 0", 1, "")
	require.NotEqual(t, "0", run.exit, "an interrupted run fails")
	require.Equal(t, "started", run.service)
	require.Equal(t, "started", run.exec)
	require.Equal(t, "no", run.running)
}

// The stopTimeout user setting applies when --stop-timeout is not set, and
// the flag wins over it.
func (InterruptSuite) TestStopTimeoutUserConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("setting applies", func(ctx context.Context, t *testctx.T) {
		run := runInterrupted(ctx, t, c, "", "exit 0", 1, "stopTimeout = \"0s\"\n")
		require.Equal(t, "started", run.service)
		require.Equal(t, "started", run.exec)
	})
	t.Run("flag wins", func(ctx context.Context, t *testctx.T) {
		run := runInterrupted(ctx, t, c, "--stop-timeout=60s", "exit 0", 1, "stopTimeout = \"0s\"\n")
		require.Equal(t, "got-SIGTERM", run.service)
		require.Equal(t, "got-SIGTERM", run.exec)
	})
}

// Processes that outlive the stop timeout after SIGTERM are killed.
func (InterruptSuite) TestStopTimeoutKillsTheRest(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	run := runInterrupted(ctx, t, c, "--stop-timeout=5s", interruptKeepRunning, 1, "")
	require.NotEqual(t, "0", run.exit, "an interrupted run fails")
	require.Equal(t, "got-SIGTERM", run.service)
	require.Equal(t, "got-SIGTERM", run.exec)
	require.Equal(t, "no", run.running)
}

// The second interrupt kills what is still stopping, without waiting for the
// stop timeout.
func (InterruptSuite) TestSecondInterruptForces(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	run := runInterrupted(ctx, t, c, "--stop-timeout=10m", interruptKeepRunning, 2, "")
	require.Equal(t, "got-SIGTERM", run.service)
	require.Equal(t, "got-SIGTERM", run.exec)
	require.Equal(t, "no", run.running)
	require.Less(t, run.elapsed, 120)
}
