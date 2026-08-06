package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

type DetachableSessionSuite struct{}

const (
	detachedQueryAcknowledgedTestSocketEnv = "_DAGGER_TEST_DETACHED_QUERY_ACK_SOCKET"
	attachmentWaitMessage                  = "Waiting for the previous client connection to be declared unavailable..."
	commandStderrMaxLineBytes              = 4 << 20
	commandStderrDiagnosticBytes           = 8 << 20
)

func TestDetachableSession(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(DetachableSessionSuite{})
}

func TestWatchCommandStderrDrainsOversizedLine(t *testing.T) {
	const marker = "oversized-stderr-marker"
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf(
		"{ head -c %d /dev/zero | tr '\\000' x; printf '\\n%s\\n'; yes drained-after-marker | head -c %d; } >&2",
		bufio.MaxScanTokenSize+(32<<10), marker, commandStderrDiagnosticBytes+(1<<20),
	))
	matched, stderrDone := watchCommandStderr(t, cmd, marker)
	runErr := cmd.Run()
	output := <-stderrDone
	require.NoError(t, runErr, output)
	select {
	case <-matched:
	default:
		t.Fatal("oversized stderr marker was not observed")
	}
	require.Contains(t, output, "drained-after-marker")
	require.Contains(t, output, "stderr truncated to last")
	require.LessOrEqual(t, len(output), commandStderrDiagnosticBytes+128)
}

func (DetachableSessionSuite) TestRunningAndCompletedAttachDoNotReexecute(ctx context.Context, t *testctx.T) {
	modDir := writeDetachableSessionModule(t)
	_, err := hostDaggerExec(ctx, t, modDir, "-m", ".", "api", "functions")
	require.NoError(t, err, "warm detachable test module")

	t.Run("attach while running", func(ctx context.Context, t *testctx.T) {
		key := "detachable-running-" + identity.NewID()
		sessionID, queryID := startDetachedCall(ctx, t, modDir, "slow", "--key", key, "--delay", "4")
		requireValidDetachedIDs(t, sessionID, queryID)

		descriptor := inspectDetachedSession(ctx, t, modDir, sessionID)
		require.NotNil(t, descriptor.Query)
		require.Equal(t, queryID, descriptor.Query.ID)
		require.Equal(t, engine.SessionQueryStateRunning, descriptor.Query.Status)

		attachOutput, err := hostDaggerExec(ctx, t, modDir, "--debug", "--progress=plain", "sessions", "attach", sessionID)
		require.NoError(t, err, string(attachOutput))
		require.Contains(t, string(attachOutput), "detached-progress")
		require.Contains(t, string(attachOutput), "result-1")
		require.Equal(t, "1", callModuleState(ctx, t, modDir, "count", key))

		terminateDetachedSession(ctx, t, modDir, sessionID)
		requireSessionAbsent(ctx, t, modDir, sessionID)
	})

	t.Run("attach after completion", func(ctx context.Context, t *testctx.T) {
		key := "detachable-completed-" + identity.NewID()
		sessionID, queryID := startDetachedCall(ctx, t, modDir, "slow", "--key", key, "--delay", "1")
		requireValidDetachedIDs(t, sessionID, queryID)

		var completed engine.SessionDescriptor
		require.Eventually(t, func() bool {
			output, inspectErr := hostDaggerOutput(ctx, t, modDir, "sessions", "inspect", sessionID, "--json")
			if inspectErr != nil || json.Unmarshal(output, &completed) != nil || completed.Query == nil {
				return false
			}
			return completed.Query.Status == engine.SessionQueryStateSucceeded
		}, 30*time.Second, 100*time.Millisecond)

		attachOutput, err := hostDaggerOutput(ctx, t, modDir, "sessions", "attach", sessionID)
		require.NoError(t, err, string(attachOutput))
		require.Equal(t, "result-1", string(attachOutput))
		require.Equal(t, "1", callModuleState(ctx, t, modDir, "count", key))

		terminateDetachedSession(ctx, t, modDir, sessionID)
	})
}

func (DetachableSessionSuite) TestHostCallbackRemainsBoundToCreator(ctx context.Context, t *testctx.T) {
	modDir := writeDetachableSessionModule(t)
	socketPath := filepath.Join(modDir, "source.sock")
	closeCreatorSocket := serveDetachableSocket(t, socketPath, "creator-bytes")
	_, err := hostDaggerExec(ctx, t, modDir, "-m", ".", "api", "functions")
	require.NoError(t, err, "warm detachable test module")

	sessionID, queryID := startDetachedCall(
		ctx, t, modDir, "delayed-socket-read", "--socket", socketPath, "--delay", "5",
	)
	requireValidDetachedIDs(t, sessionID, queryID)
	closeCreatorSocket()
	closeObserverSocket := serveDetachableSocket(t, socketPath, "observer-bytes")
	defer closeObserverSocket()
	// The observer runs with a different live source at the same host path. P0
	// must not let it satisfy the creator-bound Socket source callback.

	attachOutput, attachErr := hostDaggerExec(ctx, t, modDir, "--debug", "--progress=plain", "sessions", "attach", sessionID)
	require.Error(t, attachErr, string(attachOutput))
	require.Contains(t, string(attachOutput), "creator client unavailable")
	require.NotContains(t, string(attachOutput), "observer-bytes")

	descriptor := inspectDetachedSession(ctx, t, modDir, sessionID)
	require.NotNil(t, descriptor.Query)
	require.Equal(t, engine.SessionQueryStateFailed, descriptor.Query.Status)
	requireSessionPresent(ctx, t, modDir, sessionID)
	terminateDetachedSession(ctx, t, modDir, sessionID)
}

func (DetachableSessionSuite) TestCreatorTransportLossAcrossStorageBoundary(ctx context.Context, t *testctx.T) {
	modDir := writeDetachableSessionModule(t)

	t.Run("session published before storage", func(ctx context.Context, t *testctx.T) {
		key := "detachable-pre-storage-kill-" + identity.NewID()
		clientID := "detachable-pre-storage-client-" + identity.NewID()
		cmd := hostDaggerCommand(ctx, t, modDir, "--debug", "--progress=plain", "-m", ".", "call", "--detach", "slow", "--key", key, "--delay", "4")
		setCommandEnv(cmd, "DAGGER_SESSION_CLIENT_ID", clientID)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		require.NoError(t, cmd.Start())

		observed := waitForAttachedSessionByClientID(ctx, t, modDir, clientID)
		require.NoError(t, cmd.Process.Kill(), "creator exited before the publication-side transport cut")
		require.Error(t, cmd.Wait(), stderr.String()+stdout.String())

		descriptor := inspectDetachedSession(ctx, t, modDir, observed.ID)
		if descriptor.Query == nil {
			// The transport loss won before the storage barrier. Creation alone
			// gives no execution guarantee, but leaves a manageable empty session.
			terminateDetachedSession(ctx, t, modDir, observed.ID)
			return
		}

		// Submission won the race. Once a query record exists, it must obey the
		// same post-202 guarantee as the explicit acknowledgment case below.
		attachOutput, err := hostDaggerExec(ctx, t, modDir, "--debug", "--progress=plain", "sessions", "attach", observed.ID)
		require.NoError(t, err, string(attachOutput))
		require.Contains(t, string(attachOutput), "result-1")
		require.Equal(t, "1", callModuleState(ctx, t, modDir, "count", key))
		terminateDetachedSession(ctx, t, modDir, observed.ID)
	})

	t.Run("acknowledged storage survives abrupt loss", func(ctx context.Context, t *testctx.T) {
		_, err := hostDaggerExec(ctx, t, modDir, "-m", ".", "api", "functions")
		require.NoError(t, err, "warm detachable test module")
		key := "detachable-post-storage-kill-" + identity.NewID()
		barrierPath := filepath.Join(os.TempDir(), "dagger-detach-ack-"+identity.NewID()+".sock")
		barrier, err := net.Listen("unix", barrierPath)
		require.NoError(t, err)
		defer func() {
			require.NoError(t, barrier.Close())
			if err := os.Remove(barrierPath); err != nil {
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		}()

		cmd := hostDaggerCommand(ctx, t, modDir, "--debug", "--progress=plain", "-m", ".", "call", "--detach", "slow", "--key", key, "--delay", "4")
		cmd.Env = append(cmd.Env, detachedQueryAcknowledgedTestSocketEnv+"="+barrierPath)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		require.NoError(t, cmd.Start())

		unixBarrier := barrier.(*net.UnixListener)
		require.NoError(t, unixBarrier.SetDeadline(time.Now().Add(30*time.Second)))
		barrierConn, err := unixBarrier.Accept()
		require.NoError(t, err, "accept detached acknowledgment barrier")
		defer barrierConn.Close()
		require.NoError(t, barrierConn.SetReadDeadline(time.Now().Add(30*time.Second)))
		acknowledgment, err := bufio.NewReader(barrierConn).ReadString('\n')
		require.NoError(t, err, "read detached acknowledgment barrier")
		parts := strings.Fields(acknowledgment)
		require.Len(t, parts, 2, "detached acknowledgment test barrier: %q", acknowledgment)
		sessionID, queryID := parts[0], parts[1]
		requireValidDetachedIDs(t, sessionID, queryID)

		descriptor := inspectDetachedSession(ctx, t, modDir, sessionID)
		require.NotNil(t, descriptor.Query)
		require.Equal(t, queryID, descriptor.Query.ID)

		// The creator remains live at the acknowledged barrier. A real observer
		// must wait once, preserve the live attachment, and fail at its bounded
		// deadline rather than stealing it.
		attachStarted := time.Now()
		blockedAttachOutput, blockedAttachErr := hostDaggerExec(ctx, t, modDir, "--debug", "--progress=plain", "sessions", "attach", sessionID)
		require.Error(t, blockedAttachErr, string(blockedAttachOutput))
		require.GreaterOrEqual(t, time.Since(attachStarted), 29*time.Second)
		require.Equal(t, 1, strings.Count(string(blockedAttachOutput), attachmentWaitMessage), string(blockedAttachOutput))
		require.NotNil(t, inspectDetachedSession(ctx, t, modDir, sessionID).Attachment)

		require.NoError(t, cmd.Process.Kill())
		require.Error(t, cmd.Wait(), stderr.String()+stdout.String())

		attachOutput, attachErr := hostDaggerExec(ctx, t, modDir, "--debug", "--progress=plain", "sessions", "attach", sessionID)
		require.NoError(t, attachErr, string(attachOutput))
		require.Contains(t, string(attachOutput), "detached-progress")
		require.Contains(t, string(attachOutput), "result-1")
		require.Equal(t, "1", callModuleState(ctx, t, modDir, "count", key))
		terminateDetachedSession(ctx, t, modDir, sessionID)
	})
}

func serveDetachableSocket(t testing.TB, path, response string) func() {
	t.Helper()
	listener, err := net.Listen("unix", path)
	require.NoError(t, err)
	var wg sync.WaitGroup
	var once sync.Once
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_, _ = conn.Write([]byte(response))
			_ = conn.Close()
		}
	}()
	closeSocket := func() {
		once.Do(func() {
			_ = listener.Close()
			wg.Wait()
		})
	}
	t.Cleanup(closeSocket)
	return closeSocket
}

func (DetachableSessionSuite) TestTerminateAndInterruptLifecycle(ctx context.Context, t *testctx.T) {
	modDir := writeDetachableSessionModule(t)
	_, err := hostDaggerExec(ctx, t, modDir, "-m", ".", "api", "functions")
	require.NoError(t, err, "warm detachable test module")

	terminateKey := "detachable-terminate-" + identity.NewID()
	terminatedSession, _ := startDetachedCall(ctx, t, modDir, "slow", "--key", terminateKey, "--delay", "30")
	descriptor := inspectDetachedSession(ctx, t, modDir, terminatedSession)
	require.NotNil(t, descriptor.Query)
	require.Equal(t, engine.SessionQueryStateRunning, descriptor.Query.Status)
	terminateDetachedSession(ctx, t, modDir, terminatedSession)
	requireSessionAbsent(ctx, t, modDir, terminatedSession)

	interruptKey := "detachable-interrupt-" + identity.NewID()
	interruptedSession, _ := startDetachedCall(ctx, t, modDir, "slow", "--key", interruptKey, "--delay", "30")
	attachCmd := hostDaggerCommand(ctx, t, modDir, "--debug", "--progress=plain", "sessions", "attach", interruptedSession)
	progress, stderrDone := watchCommandStderr(t, attachCmd, "detached-progress")
	var stdout bytes.Buffer
	attachCmd.Stdout = &stdout
	if err := attachCmd.Start(); err != nil {
		t.Fatalf("start attach: %v\nstdout:\n%s", err, stdout.String())
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- attachCmd.Wait() }()
	var waitErr error
	var stderrOutput string
	reaped := false
	finishWait := func(err error) {
		waitErr = err
		stderrOutput = <-stderrDone
		reaped = true
	}
	reap := func() {
		if reaped {
			return
		}
		_ = attachCmd.Process.Kill()
		finishWait(<-waitDone)
	}
	t.Cleanup(reap)
	failWithOutput := func(message string) {
		reap()
		t.Fatalf("%s\nwait error: %v\nstdout:\n%s\nstderr:\n%s", message, waitErr, stdout.String(), stderrOutput)
	}

	select {
	case <-progress:
	case err := <-waitDone:
		finishWait(err)
		failWithOutput("attach exited before replaying progress")
	case <-time.After(30 * time.Second):
		failWithOutput("attach did not replay progress before timeout")
	}
	if err := attachCmd.Process.Signal(os.Interrupt); err != nil {
		failWithOutput(fmt.Sprintf("interrupt attach: %v", err))
	}
	select {
	case err := <-waitDone:
		finishWait(err)
	case <-time.After(30 * time.Second):
		failWithOutput("attach did not exit after interrupt")
	}
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) {
		failWithOutput("attach did not return an exit error after interrupt")
	}
	if exitErr.ExitCode() != 130 {
		failWithOutput(fmt.Sprintf("attach exit code = %d, want 130", exitErr.ExitCode()))
	}
	requireSessionPresent(ctx, t, modDir, interruptedSession)
	terminateDetachedSession(ctx, t, modDir, interruptedSession)
}

func (DetachableSessionSuite) TestOrdinaryDisconnectStillCancelsSlowCall(ctx context.Context, t *testctx.T) {
	modDir := writeDetachableSessionModule(t)
	_, err := hostDaggerExec(ctx, t, modDir, "-m", ".", "api", "functions")
	require.NoError(t, err, "warm detachable test module")

	key := "ordinary-disconnect-" + identity.NewID()
	const delay = 10
	cmd := hostDaggerCommand(ctx, t, modDir, "-m", ".", "call", "slow", "--key", key, "--delay", fmt.Sprint(delay))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	require.Eventually(t, func() bool {
		output, countErr := hostDaggerOutput(ctx, t, modDir, "-m", ".", "call", "count", "--key", key, "--nonce", identity.NewID())
		return countErr == nil && strings.TrimSpace(string(output)) == "1"
	}, 30*time.Second, 250*time.Millisecond, "ordinary call did not begin before disconnect")
	require.NoError(t, cmd.Process.Kill(), "kill ordinary call after runtime start")
	require.Error(t, cmd.Wait(), stderr.String()+stdout.String())

	// The increment happens before the sleep and is allowed to survive
	// cancellation. A call that keeps running after disconnect would write the
	// completion marker at the end of that sleep.
	select {
	case <-time.After((delay + 2) * time.Second):
	case <-ctx.Done():
		t.Fatalf("waiting past ordinary call completion window: %v", context.Cause(ctx))
	}
	require.Equal(t, "0", callModuleState(ctx, t, modDir, "completed", key),
		"ordinary call completed successfully after its client disconnected")
}

type DetachableSessionPreAcknowledgmentSuite struct{}

func TestDetachableSessionPreAcknowledgment(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(DetachableSessionPreAcknowledgmentSuite{})
}

func (DetachableSessionPreAcknowledgmentSuite) TestFailureTerminatesSession(ctx context.Context, t *testctx.T) {
	modDir := writeDetachableSessionModule(t)
	_, err := hostDaggerExec(ctx, t, modDir, "-m", ".", "api", "functions")
	require.NoError(t, err, "warm detachable test module")

	clientID := "detachable-preack-client-" + identity.NewID()
	cmd := hostDaggerCommand(ctx, t, modDir, "-m", ".", "call", "--detach", "containers")
	setCommandEnv(cmd, "DAGGER_SESSION_CLIENT_ID", clientID)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	created := waitForAttachedSessionByClientID(ctx, t, modDir, clientID)

	callErr := cmd.Wait()
	diagnostics := stderr.String() + stdout.String()
	require.Error(t, callErr, diagnostics)
	require.Contains(t, diagnostics, "object-list results")
	requireSessionAbsent(ctx, t, modDir, created.ID)
}

type DetachableSessionRestartSuite struct{}

func TestDetachableSessionRestart(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(DetachableSessionRestartSuite{})
}

func (DetachableSessionRestartSuite) TestEngineRestartDropsSessionsAndStaleResults(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modDir := writeDetachableSessionModule(t)
	stateKey := "detachable-restart-" + identity.NewID()

	engineA := startDetachableDevEngine(ctx, t, c, stateKey, false)
	t.Cleanup(func() { stopDetachableDevEngine(ctx, t, engineA, true) })
	clientA := detachableDevEngineClient(ctx, t, c, engineA, modDir)
	_, err := nestedDaggerOutput(ctx, t, clientA, "-m", ".", "api", "functions")
	require.NoError(t, err, "warm module on first engine")
	sessionID, queryID := startNestedDetachedCall(ctx, t, clientA, "slow", "--key", "restart-"+identity.NewID(), "--delay", "30")
	requireValidDetachedIDs(t, sessionID, queryID)

	stopDetachableDevEngine(ctx, t, engineA, false)
	engineA = nil

	_, err = c.Container().From(alpineImage).
		WithMountedCache("/state", c.CacheVolume(stateKey)).
		WithExec([]string{"sh", "-ec", "mkdir -p /state/worker/session-results/stale-process/stale-session; printf stale >/state/worker/session-results/stale-process/stale-session/stale-marker"}).
		Sync(ctx)
	require.NoError(t, err)

	engineB := startDetachableDevEngine(ctx, t, c, stateKey, false)
	t.Cleanup(func() { stopDetachableDevEngine(ctx, t, engineB, true) })
	clientB := detachableDevEngineClient(ctx, t, c, engineB, modDir)
	listOutput, err := nestedDaggerOutput(ctx, t, clientB, "sessions", "list", "--json")
	require.NoError(t, err, string(listOutput))
	var sessions []engine.SessionDescriptor
	require.NoError(t, json.Unmarshal(listOutput, &sessions), string(listOutput))
	for _, descriptor := range sessions {
		require.NotEqual(t, sessionID, descriptor.ID, "old detachable session survived engine restart")
	}

	stopDetachableDevEngine(ctx, t, engineB, false)
	engineB = nil
	staleFiles, err := c.Container().From(alpineImage).
		WithMountedCache("/state", c.CacheVolume(stateKey)).
		WithExec([]string{"sh", "-ec", "find /state/worker/session-results -name stale-marker -print"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Empty(t, strings.TrimSpace(staleFiles), "startup retained a stale detachable result marker")
}

type DetachableSessionCacheSuite struct{}

func TestDetachableSessionCache(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(DetachableSessionCacheSuite{})
}

func (DetachableSessionCacheSuite) TestDagQLCacheRetentionMeasurement(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modDir := writeDetachableSessionModule(t)
	stateKey := "detachable-cache-measurement-" + identity.NewID()
	devEngine := startDetachableDevEngine(ctx, t, c, stateKey, true)
	t.Cleanup(func() { stopDetachableDevEngine(ctx, t, devEngine, true) })
	client := detachableDevEngineClient(ctx, t, c, devEngine, modDir)
	_, err := nestedDaggerOutput(ctx, t, client, "-m", ".", "api", "functions")
	require.NoError(t, err, "warm module before cache baseline")

	baseline := measureDetachableDagQLCache(ctx, t, c, devEngine)
	const sessionCount = 3
	sessionIDs := make([]string, 0, sessionCount)
	for i := 0; i < sessionCount; i++ {
		sessionID, queryID := startNestedDetachedCall(
			ctx, t, client, "slow", "--key", fmt.Sprintf("cache-measurement-%d-%s", i, identity.NewID()), "--delay", "0",
		)
		requireValidDetachedIDs(t, sessionID, queryID)
		sessionIDs = append(sessionIDs, sessionID)
		require.Eventually(t, func() bool {
			descriptor, inspectErr := inspectNestedDetachedSession(ctx, t, client, sessionID)
			return inspectErr == nil && descriptor.Query != nil && descriptor.Query.Status == engine.SessionQueryStateSucceeded
		}, 30*time.Second, 100*time.Millisecond)
	}
	completed := measureDetachableDagQLCache(ctx, t, c, devEngine)
	for _, sessionID := range sessionIDs {
		output, terminateErr := nestedDaggerOutput(ctx, t, client, "sessions", "terminate", sessionID)
		require.NoError(t, terminateErr, string(output))
	}
	terminated := measureDetachableDagQLCache(ctx, t, c, devEngine)
	t.Logf("detachable DagQL cache retention: baseline=%+v completed_%d=%+v terminated=%+v", baseline, sessionCount, completed, terminated)
	require.GreaterOrEqual(t, completed.Results, baseline.Results)
	require.GreaterOrEqual(t, completed.Bytes, baseline.Bytes)
	for _, sessionID := range sessionIDs {
		output, inspectErr := nestedDaggerOutput(ctx, t, client, "sessions", "inspect", sessionID, "--json")
		require.Error(t, inspectErr, string(output))
	}
}

type detachableCacheMeasurement struct {
	Bytes          int
	Results        int
	SessionResults int
	Terms          int
	ResultTerms    int
	EqClasses      int
}

func measureDetachableDagQLCache(ctx context.Context, t testing.TB, c *dagger.Client, devEngine *dagger.Service) detachableCacheMeasurement {
	t.Helper()
	raw, err := c.Container().From(alpineImage).
		WithServiceBinding("engine-debug", devEngine).
		WithEnvVariable("DETACHABLE_CACHE_SNAPSHOT", identity.NewID()).
		WithExec([]string{"wget", "-qO-", "http://engine-debug:6060/debug/dagql/cache"}).
		Stdout(ctx)
	require.NoError(t, err)
	var snapshot struct {
		Results        []json.RawMessage `json:"results"`
		SessionResults []json.RawMessage `json:"session_results"`
		Terms          []json.RawMessage `json:"terms"`
		ResultTerms    []json.RawMessage `json:"result_terms"`
		EqClasses      []json.RawMessage `json:"eq_classes"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &snapshot))
	return detachableCacheMeasurement{
		Bytes: len(raw), Results: len(snapshot.Results), SessionResults: len(snapshot.SessionResults),
		Terms: len(snapshot.Terms), ResultTerms: len(snapshot.ResultTerms), EqClasses: len(snapshot.EqClasses),
	}
}

func startDetachableDevEngine(ctx context.Context, t *testctx.T, c *dagger.Client, stateKey string, debug bool) *dagger.Service {
	t.Helper()
	var opts []func(*dagger.Container) *dagger.Container
	if debug {
		opts = append(opts,
			engineWithBkConfig(ctx, t, func(_ context.Context, _ *testctx.T, cfg bkconfig.Config) bkconfig.Config {
				cfg.GRPC.DebugAddress = "0.0.0.0:6060"
				return cfg
			}),
			func(ctr *dagger.Container) *dagger.Container {
				return ctr.WithExposedPort(6060, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp})
			},
		)
	}
	service := devEngineContainerAsService(devEngineContainerWithStateKey(c, stateKey, opts...))
	running, err := service.Start(ctx)
	require.NoError(t, err)
	return running
}

func stopDetachableDevEngine(ctx context.Context, t testing.TB, service *dagger.Service, kill bool) {
	t.Helper()
	if service == nil {
		return
	}
	_, err := service.Stop(ctx, dagger.ServiceStopOpts{Kill: kill})
	if err != nil && !strings.Contains(err.Error(), "not found") {
		require.NoError(t, err)
	}
}

func detachableDevEngineClient(ctx context.Context, t *testctx.T, c *dagger.Client, devEngine *dagger.Service, modDir string) *dagger.Container {
	t.Helper()
	return engineClientContainer(ctx, t, c, devEngine).
		WithDirectory("/work", c.Host().Directory(modDir)).
		WithWorkdir("/work")
}

func nestedDaggerOutput(ctx context.Context, t testing.TB, client *dagger.Container, args ...string) ([]byte, error) {
	t.Helper()
	execArgs := append([]string{"/bin/dagger"}, args...)
	output, err := client.
		WithEnvVariable("DETACHABLE_CLI_RUN", identity.NewID()).
		WithExec(execArgs).
		Stdout(ctx)
	return []byte(output), err
}

func startNestedDetachedCall(ctx context.Context, t testing.TB, client *dagger.Container, args ...string) (string, string) {
	t.Helper()
	command := []string{"-m", ".", "call", "--detach"}
	command = append(command, args...)
	output, err := nestedDaggerOutput(ctx, t, client, command...)
	require.NoError(t, err, string(output))
	matches := detachedIDsPattern.FindSubmatch(output)
	require.Len(t, matches, 3, "detached acknowledgment output: %q", string(output))
	return string(matches[1]), string(matches[2])
}

func inspectNestedDetachedSession(ctx context.Context, t testing.TB, client *dagger.Container, sessionID string) (engine.SessionDescriptor, error) {
	t.Helper()
	output, err := nestedDaggerOutput(ctx, t, client, "sessions", "inspect", sessionID, "--json")
	if err != nil {
		return engine.SessionDescriptor{}, err
	}
	var descriptor engine.SessionDescriptor
	if err := json.Unmarshal(output, &descriptor); err != nil {
		return engine.SessionDescriptor{}, err
	}
	return descriptor, nil
}

var detachedIDsPattern = regexp.MustCompile(`(?m)^Session:\s+(sess_[a-z2-7]{26})\nQuery:\s+(qry_[a-z2-7]{26})$`)

func startDetachedCall(ctx context.Context, t testing.TB, modDir string, args ...string) (string, string) {
	t.Helper()
	command := []string{"-m", ".", "call", "--detach"}
	command = append(command, args...)
	output, err := hostDaggerOutput(ctx, t, modDir, command...)
	require.NoError(t, err, string(output))
	matches := detachedIDsPattern.FindSubmatch(output)
	require.Len(t, matches, 3, "detached acknowledgment output: %q", string(output))
	return string(matches[1]), string(matches[2])
}

func inspectDetachedSession(ctx context.Context, t testing.TB, modDir, sessionID string) engine.SessionDescriptor {
	t.Helper()
	output, err := hostDaggerOutput(ctx, t, modDir, "sessions", "inspect", sessionID, "--json")
	require.NoError(t, err, string(output))
	var descriptor engine.SessionDescriptor
	require.NoError(t, json.Unmarshal(output, &descriptor), string(output))
	return descriptor
}

func terminateDetachedSession(ctx context.Context, t testing.TB, modDir, sessionID string) {
	t.Helper()
	output, err := hostDaggerOutput(ctx, t, modDir, "sessions", "terminate", sessionID)
	require.NoError(t, err, string(output))
	require.Equal(t, "Terminated "+sessionID+"\n", string(output))
}

func requireSessionAbsent(ctx context.Context, t testing.TB, modDir, sessionID string) {
	t.Helper()
	output, err := hostDaggerOutput(ctx, t, modDir, "sessions", "list", "--json")
	require.NoError(t, err, string(output))
	var sessions []engine.SessionDescriptor
	require.NoError(t, json.Unmarshal(output, &sessions), string(output))
	for _, descriptor := range sessions {
		require.NotEqual(t, sessionID, descriptor.ID)
	}
}

func requireSessionPresent(ctx context.Context, t testing.TB, modDir, sessionID string) {
	t.Helper()
	_, present := listedSessionIDs(ctx, t, modDir)[sessionID]
	require.True(t, present, "session %s is not listed", sessionID)
}

func listedSessionIDs(ctx context.Context, t testing.TB, modDir string) map[string]struct{} {
	t.Helper()
	sessions := listDetachedSessions(ctx, t, modDir)
	ids := make(map[string]struct{}, len(sessions))
	for _, descriptor := range sessions {
		ids[descriptor.ID] = struct{}{}
	}
	return ids
}

func listDetachedSessions(ctx context.Context, t testing.TB, modDir string) []engine.SessionDescriptor {
	t.Helper()
	output, err := hostDaggerOutput(ctx, t, modDir, "sessions", "list", "--json")
	require.NoError(t, err, string(output))
	var sessions []engine.SessionDescriptor
	require.NoError(t, json.Unmarshal(output, &sessions), string(output))
	return sessions
}

func waitForAttachedSessionByClientID(ctx context.Context, t *testctx.T, modDir, clientID string) engine.SessionDescriptor {
	t.Helper()
	var observed engine.SessionDescriptor
	require.Eventually(t, func() bool {
		for _, descriptor := range listDetachedSessions(ctx, t, modDir) {
			if descriptor.Attachment != nil && descriptor.Attachment.ClientID == clientID {
				observed = descriptor
				return true
			}
		}
		return false
	}, 30*time.Second, 100*time.Millisecond, "session for client %s was never published", clientID)
	return observed
}

func setCommandEnv(cmd *exec.Cmd, key, value string) {
	prefix := key + "="
	for i, entry := range cmd.Env {
		if strings.HasPrefix(entry, prefix) {
			cmd.Env[i] = prefix + value
			return
		}
	}
	cmd.Env = append(cmd.Env, prefix+value)
}

func watchCommandStderr(t testing.TB, cmd *exec.Cmd, match string) (<-chan struct{}, <-chan string) {
	t.Helper()
	stderr, err := cmd.StderrPipe()
	require.NoError(t, err)
	matched := make(chan struct{}, 1)
	done := make(chan string, 1)
	go func() {
		var output boundedStderrOutput
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 64<<10), commandStderrMaxLineBytes)
		for scanner.Scan() {
			line := scanner.Text()
			output.WriteString(line)
			output.WriteString("\n")
			if strings.Contains(line, match) {
				select {
				case matched <- struct{}{}:
				default:
				}
			}
		}
		if scanErr := scanner.Err(); scanErr != nil && !errors.Is(scanErr, os.ErrClosed) {
			output.WriteString("[stderr scan error: " + scanErr.Error() + "]\n")
		}
		if _, drainErr := io.Copy(io.Discard, stderr); drainErr != nil && !errors.Is(drainErr, os.ErrClosed) {
			output.WriteString("[stderr drain error: " + drainErr.Error() + "]\n")
		}
		done <- output.String()
	}()
	return matched, done
}

type boundedStderrOutput struct {
	data    []byte
	start   int
	written int64
}

func (output *boundedStderrOutput) WriteString(value string) {
	output.written += int64(len(value))
	if len(value) >= commandStderrDiagnosticBytes {
		if cap(output.data) < commandStderrDiagnosticBytes {
			output.data = make([]byte, commandStderrDiagnosticBytes)
		} else {
			output.data = output.data[:commandStderrDiagnosticBytes]
		}
		copy(output.data, value[len(value)-commandStderrDiagnosticBytes:])
		output.start = 0
		return
	}
	if available := commandStderrDiagnosticBytes - len(output.data); available > 0 {
		if len(value) <= available {
			output.data = append(output.data, value...)
			return
		}
		output.data = append(output.data, value[:available]...)
		value = value[available:]
		output.start = 0
	}
	first := min(len(value), commandStderrDiagnosticBytes-output.start)
	copy(output.data[output.start:], value[:first])
	copy(output.data, value[first:])
	output.start = (output.start + len(value)) % commandStderrDiagnosticBytes
}

func (output *boundedStderrOutput) String() string {
	if output.written <= commandStderrDiagnosticBytes {
		return string(output.data)
	}
	ordered := make([]byte, 0, len(output.data))
	ordered = append(ordered, output.data[output.start:]...)
	ordered = append(ordered, output.data[:output.start]...)
	return fmt.Sprintf("[stderr truncated to last %d bytes]\n%s", commandStderrDiagnosticBytes, ordered)
}

func requireValidDetachedIDs(t testing.TB, sessionID, queryID string) {
	t.Helper()
	require.NoError(t, engine.ValidateSessionID(sessionID))
	require.NoError(t, engine.ValidateQueryID(queryID))
}

func callModuleScalar(ctx context.Context, t testing.TB, modDir string, args ...string) string {
	t.Helper()
	command := []string{"-m", ".", "call"}
	command = append(command, args...)
	output, err := hostDaggerOutput(ctx, t, modDir, command...)
	require.NoError(t, err, string(output))
	return strings.TrimSpace(string(output))
}

func callModuleState(ctx context.Context, t testing.TB, modDir, function, key string) string {
	t.Helper()
	return callModuleScalar(ctx, t, modDir, function, "--key", key, "--nonce", identity.NewID())
}

func writeDetachableSessionModule(t testing.TB) string {
	t.Helper()
	modDir := t.TempDir()
	config := `{"name":"detachable","engineVersion":"latest","sdk":{"source":"go"},"source":"."}`
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "dagger.json"), []byte(config), 0o644))
	source := fmt.Sprintf(`// fixture %s
package main

import (
	"context"
	"fmt"
	"time"

	"dagger/detachable/internal/dagger"
)

type Detachable struct{}

func (m *Detachable) Slow(ctx context.Context, key string, delay int) (string, error) {
	script := fmt.Sprintf("set -eu; n=$(cat /state/count 2>/dev/null || echo 0); n=$((n+1)); echo $n >/state/count; echo detached-progress >&2; sleep %%d; echo $n >/state/completed; printf result-$n", delay)
	return dag.Container().
		From(%q).
		WithMountedCache("/state", dag.CacheVolume(key)).
		WithExec([]string{"sh", "-c", script}).
		Stdout(ctx)
}

func (m *Detachable) Count(ctx context.Context, key, nonce string) (string, error) {
	return dag.Container().
		From(%q).
		WithMountedCache("/state", dag.CacheVolume(key)).
		WithEnvVariable("STATE_NONCE", nonce).
		WithExec([]string{"sh", "-c", "cat /state/count 2>/dev/null || echo 0"}).
		Stdout(ctx)
}

func (m *Detachable) Completed(ctx context.Context, key, nonce string) (string, error) {
	return dag.Container().
		From(%q).
		WithMountedCache("/state", dag.CacheVolume(key)).
		WithEnvVariable("STATE_NONCE", nonce).
		WithExec([]string{"sh", "-c", "cat /state/completed 2>/dev/null || echo 0"}).
		Stdout(ctx)
}

func (m *Detachable) DelayedSocketRead(ctx context.Context, socket *dagger.Socket, delay int) (string, error) {
	time.Sleep(time.Duration(delay) * time.Second)
	out, err := dag.Container().
		From(%q).
		WithExec([]string{"apk", "add", "netcat-openbsd"}).
		WithUnixSocket("/source.sock", socket).
		WithExec([]string{"nc", "-w", "5", "-U", "/source.sock"}).
		Stdout(ctx)
	if err != nil {
		return "", err
	}
	if out == "" {
		return "", fmt.Errorf("creator client unavailable: socket returned no bytes")
	}
	if out != "creator-bytes" {
		return "", fmt.Errorf("unexpected creator socket bytes: %%q", out)
	}
	return out, nil
}

func (m *Detachable) Containers() []*dagger.Container {
	return []*dagger.Container{dag.Container().From(%q)}
}
`, identity.NewID(), alpineImage, alpineImage, alpineImage, alpineImage, alpineImage)
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "main.go"), []byte(source), 0o644))
	return modDir
}
