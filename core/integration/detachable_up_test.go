package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/otel-go/oteltestctx"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type DetachableUpSuite struct{}

func TestDetachableUp(t *testing.T) {
	testctx.New(t, oteltestctx.WithTracing(oteltestctx.TraceConfig[*testing.T]{
		StartOptions: testutil.SpanOpts[*testing.T],
	})).RunTests(DetachableUpSuite{})
}

func (DetachableUpSuite) TestDetachExposeStopAndTerminate(ctx context.Context, t *testctx.T) {
	nativePort := reserveDetachableUpPort(t)
	fixedBackend := reserveDetachableUpPort(t)
	fixedFrontend := reserveDetachableUpPort(t)
	extraFrontend := reserveDetachableUpPort(t)
	workspace := writeDetachableUpModule(t, nativePort, fixedBackend, fixedFrontend)
	stateHome := t.TempDir()
	listOutput, err := detachableUpCLIOutput(ctx, t, workspace, stateHome, "up", "--list")
	require.NoError(t, err)
	require.Contains(t, string(listOutput), "hello-with-services:web")
	require.Contains(t, string(listOutput), "hello-with-services:infra:database")

	output, err := detachableUpCLIOutput(ctx, t, workspace, stateHome, "up", "--detach")
	require.NoError(t, err)
	matches := regexp.MustCompile(`(?m)^Detached session (sess_[a-z2-7]{26})$`).FindSubmatch(output)
	require.Len(t, matches, 2, string(output))
	sessionID := string(matches[1])
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
		defer cancel()
		_, _ = detachableUpCLIOutput(cleanupCtx, t, workspace, stateHome, "sessions", "terminate", sessionID)
	})
	require.Contains(t, string(output), fmt.Sprintf("tcp://localhost:%d", nativePort))
	require.Contains(t, string(output), fmt.Sprintf("tcp://localhost:%d", fixedFrontend))
	requireDetachableUpHTTP(t, nativePort, "native")
	requireDetachableUpHTTP(t, fixedFrontend, "fixed")

	descriptor := inspectDetachableUpSession(ctx, t, workspace, stateHome, sessionID)
	require.Equal(t, 2, countRetainedUpNames(descriptor))
	require.Equal(t, 2, countTunnelPorts(descriptor))
	require.NotNil(t, descriptor.Attachment)
	require.NotEmpty(t, descriptor.Attachment.Hostname)
	otherStateHome := t.TempDir()
	_, conflictErr := detachableUpCLIOutput(ctx, t, workspace, otherStateHome, "sessions", "expose", sessionID)
	require.ErrorContains(t, conflictErr, "ports are being served from")
	require.Equal(t, 1, detachableUpExitCode(t, conflictErr))

	recordPath := filepath.Join(stateHome, "dagger", "expose", sessionID+".pid")
	process, err := os.FindProcess(detachableUpExposePID(t, recordPath))
	require.NoError(t, err)
	require.NoError(t, process.Kill())
	require.Eventually(t, func() bool {
		return !detachableUpPortAccepts(nativePort) && !detachableUpPortAccepts(fixedFrontend)
	}, 15*time.Second, 100*time.Millisecond)
	require.Eventually(t, func() bool {
		descriptor := inspectDetachableUpSession(ctx, t, workspace, stateHome, sessionID)
		return countRetainedUpNames(descriptor) == 2 && countTunnelPorts(descriptor) == 0
	}, 20*time.Second, 200*time.Millisecond)

	exposeOutput, err := detachableUpCLIOutput(ctx, t, workspace, stateHome, "sessions", "expose", sessionID)
	require.NoError(t, err)
	require.Contains(t, string(exposeOutput), fmt.Sprintf("tcp://localhost:%d", nativePort))
	require.Contains(t, string(exposeOutput), fmt.Sprintf("tcp://localhost:%d", fixedFrontend))
	requireDetachableUpHTTP(t, nativePort, "native")
	requireDetachableUpHTTP(t, fixedFrontend, "fixed")

	extraSpec := fmt.Sprintf("hello-with-services:web=%d:%d", extraFrontend, fixedBackend)
	replaceOutput, err := detachableUpCLIOutput(
		ctx, t, workspace, stateHome,
		"sessions", "expose", sessionID, "--port", extraSpec, "--replace",
	)
	require.NoError(t, err)
	require.Contains(t, string(replaceOutput), fmt.Sprintf("tcp://localhost:%d", nativePort))
	require.Contains(t, string(replaceOutput), fmt.Sprintf("tcp://localhost:%d", fixedFrontend))
	require.Contains(t, string(replaceOutput), fmt.Sprintf("tcp://localhost:%d", extraFrontend))
	requireDetachableUpHTTP(t, nativePort, "native")
	requireDetachableUpHTTP(t, fixedFrontend, "fixed")
	requireDetachableUpHTTP(t, extraFrontend, "extra")
	require.Equal(t, 3, countTunnelPorts(inspectDetachableUpSession(ctx, t, workspace, stateHome, sessionID)))

	stopOutput, err := detachableUpCLIOutput(ctx, t, workspace, stateHome, "sessions", "expose", sessionID, "--stop")
	require.NoError(t, err)
	require.Contains(t, string(stopOutput), "Stopped local ports")
	require.Eventually(t, func() bool {
		descriptor := inspectDetachableUpSession(ctx, t, workspace, stateHome, sessionID)
		return countRetainedUpNames(descriptor) == 2 && countTunnelPorts(descriptor) == 0
	}, 20*time.Second, 200*time.Millisecond)

	attachOutput, err := detachableUpCLIOutput(ctx, t, workspace, stateHome, "sessions", "attach", sessionID)
	require.NoError(t, err)
	require.Contains(t, string(attachOutput), "SERVICE")
	require.Contains(t, string(attachOutput), "hello-with-services:web")
	require.Contains(t, string(attachOutput), "hello-with-services:infra:database")

	randomSpec := fmt.Sprintf("hello-with-services:web=:%d", nativePort)
	randomOutput, err := detachableUpCLIOutput(
		ctx, t, workspace, stateHome, "sessions", "expose", sessionID, "--port", randomSpec,
	)
	require.NoError(t, err)
	randomPort := detachableUpSummaryPort(t, randomOutput, "hello-with-services:web")
	require.Positive(t, randomPort)
	requireDetachableUpHTTP(t, randomPort, "native")
	requireDetachableUpHTTP(t, fixedFrontend, "fixed")

	randomProcess, err := os.FindProcess(detachableUpExposePID(t, recordPath))
	require.NoError(t, err)
	require.NoError(t, randomProcess.Kill())
	require.Eventually(t, func() bool {
		return !detachableUpPortAccepts(randomPort) && !detachableUpPortAccepts(fixedFrontend)
	}, 15*time.Second, 100*time.Millisecond)
	require.Eventually(t, func() bool {
		descriptor := inspectDetachableUpSession(ctx, t, workspace, stateHome, sessionID)
		return countRetainedUpNames(descriptor) == 2 && countTunnelPorts(descriptor) == 0
	}, 20*time.Second, 200*time.Millisecond)

	randomOutput, err = detachableUpCLIOutput(
		ctx, t, workspace, stateHome, "sessions", "expose", sessionID, "--port", randomSpec,
	)
	require.NoError(t, err)
	republishedRandomPort := detachableUpSummaryPort(t, randomOutput, "hello-with-services:web")
	require.Positive(t, republishedRandomPort)
	requireDetachableUpHTTP(t, republishedRandomPort, "native")

	terminateOutput, err := detachableUpCLIOutput(ctx, t, workspace, stateHome, "sessions", "terminate", sessionID)
	require.NoError(t, err)
	require.Contains(t, string(terminateOutput), "Terminated "+sessionID)
	require.Eventually(t, func() bool {
		_, err := os.Stat(recordPath)
		return os.IsNotExist(err)
	}, 5*time.Second, 100*time.Millisecond)
	secondTerminate, err := detachableUpCLIOutput(ctx, t, workspace, stateHome, "sessions", "terminate", sessionID)
	require.NoError(t, err)
	require.Contains(t, string(secondTerminate), "Terminated "+sessionID)
}

func (DetachableUpSuite) TestAttachedUpBehaviorUnchanged(ctx context.Context, t *testctx.T) {
	nativePort := reserveDetachableUpPort(t)
	fixedBackend := reserveDetachableUpPort(t)
	fixedFrontend := reserveDetachableUpPort(t)
	workspace := writeDetachableUpModule(t, nativePort, fixedBackend, fixedFrontend)
	stateHome := t.TempDir()

	commandCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := hostDaggerCommand(commandCtx, t, workspace, "up")
	setCommandEnv(cmd, "XDG_STATE_HOME", stateHome)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.MultiWriter(testutil.NewTWriter(t), &stderr)
	require.NoError(t, cmd.Start())
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	finished := false
	t.Cleanup(func() {
		if !finished {
			_ = cmd.Process.Kill()
			<-waitDone
		}
	})

	requireDetachableUpHTTP(t, nativePort, "native")
	requireDetachableUpHTTP(t, fixedFrontend, "fixed")
	require.NoError(t, cmd.Process.Signal(os.Interrupt))
	var waitErr error
	select {
	case waitErr = <-waitDone:
		finished = true
	case <-time.After(30 * time.Second):
		t.Fatalf("attached up did not exit after interrupt\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	require.NoError(t, waitErr, stderr.String()+stdout.String())
	require.Eventually(t, func() bool {
		return !detachableUpPortAccepts(nativePort) && !detachableUpPortAccepts(fixedFrontend)
	}, 15*time.Second, 100*time.Millisecond)
}

func (DetachableUpSuite) TestBackendExitRestartsThroughExpose(ctx context.Context, t *testctx.T) {
	restartPort := reserveDetachableUpPort(t)
	siblingPort := reserveDetachableUpPort(t)
	workspace := writeDetachableUpRestartModule(t, restartPort, siblingPort)
	stateHome := shortDetachableUpStateHome(t)

	output, err := detachableUpCLIOutput(ctx, t, workspace, stateHome, "up", "--detach")
	require.NoError(t, err)
	matches := regexp.MustCompile(`(?m)^Detached session (sess_[a-z2-7]{26})$`).FindSubmatch(output)
	require.Len(t, matches, 2, string(output))
	sessionID := string(matches[1])
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
		defer cancel()
		_, _ = detachableUpCLIOutput(cleanupCtx, t, workspace, stateHome, "sessions", "terminate", sessionID)
	})
	requireDetachableUpHTTP(t, restartPort, "restartable")
	requireDetachableUpHTTP(t, siblingPort, "sibling")

	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/quit", restartPort))
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusOK, response.StatusCode)

	require.Eventually(t, func() bool {
		return !detachableUpPortAccepts(restartPort) && !detachableUpPortAccepts(siblingPort)
	}, 20*time.Second, 100*time.Millisecond)
	recordPath := filepath.Join(stateHome, "dagger", "expose", sessionID+".pid")
	require.Eventually(t, func() bool {
		_, err := os.Stat(recordPath)
		return os.IsNotExist(err)
	}, 10*time.Second, 100*time.Millisecond)
	require.Eventually(t, func() bool {
		descriptor := inspectDetachableUpSession(ctx, t, workspace, stateHome, sessionID)
		if countTunnelPorts(descriptor) != 0 || countRetainedUpNames(descriptor) != 1 {
			return false
		}
		return retainedUpNames(descriptor)["restart-fixture:sibling"]
	}, 20*time.Second, 200*time.Millisecond)

	exposeOutput, err := detachableUpCLIOutput(ctx, t, workspace, stateHome, "sessions", "expose", sessionID)
	require.NoError(t, err)
	require.Contains(t, string(exposeOutput), fmt.Sprintf("tcp://localhost:%d", restartPort))
	require.Contains(t, string(exposeOutput), fmt.Sprintf("tcp://localhost:%d", siblingPort))
	requireDetachableUpHTTP(t, restartPort, "restartable")
	requireDetachableUpHTTP(t, siblingPort, "sibling")
	descriptor := inspectDetachableUpSession(ctx, t, workspace, stateHome, sessionID)
	require.Equal(t, 2, countRetainedUpNames(descriptor))
	require.Equal(t, 2, countTunnelPorts(descriptor))
}

func (DetachableUpSuite) TestCreatorKillAfterAcknowledgement(ctx context.Context, t *testctx.T) {
	port := reserveDetachableUpPort(t)
	workspace := writeDetachableUpDelayedModule(t, port, 6*time.Second)
	stateHome := shortDetachableUpStateHome(t)
	before := detachableUpSessionIDs(ctx, t, workspace, stateHome)

	commandCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := hostDaggerCommand(commandCtx, t, workspace, "up", "--detach")
	setCommandEnv(cmd, "XDG_STATE_HOME", stateHome)
	var creatorStdout, creatorStderr bytes.Buffer
	cmd.Stdout = &creatorStdout
	cmd.Stderr = io.MultiWriter(testutil.NewTWriter(t), &creatorStderr)
	require.NoError(t, cmd.Start())
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	finished := false
	t.Cleanup(func() {
		if !finished {
			_ = cmd.Process.Kill()
			<-waitDone
		}
	})

	var sessionID string
	require.Eventually(t, func() bool {
		for id := range detachableUpSessionIDs(ctx, t, workspace, stateHome) {
			if _, existed := before[id]; existed {
				continue
			}
			descriptor := inspectDetachableUpSession(ctx, t, workspace, stateHome, id)
			if descriptor.Query != nil && descriptor.Query.Status == engine.SessionQueryStateRunning {
				sessionID = id
				return true
			}
		}
		return false
	}, 45*time.Second, 100*time.Millisecond, "detachable up did not reach the accepted running query state")
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
		defer cancel()
		_, _ = detachableUpCLIOutput(cleanupCtx, t, workspace, stateHome, "sessions", "terminate", sessionID)
	})
	require.NoError(t, cmd.Process.Kill())
	require.Error(t, <-waitDone)
	finished = true

	exposeCtx, exposeCancel := context.WithTimeout(ctx, 90*time.Second)
	defer exposeCancel()
	exposeCmd := hostDaggerCommand(exposeCtx, t, workspace, "sessions", "expose", sessionID)
	setCommandEnv(exposeCmd, "XDG_STATE_HOME", stateHome)
	var exposeStdout, exposeStderr bytes.Buffer
	exposeCmd.Stdout = &exposeStdout
	exposeCmd.Stderr = io.MultiWriter(testutil.NewTWriter(t), &exposeStderr)
	require.NoError(t, exposeCmd.Run(), exposeStderr.String()+exposeStdout.String())
	require.Contains(t, exposeStderr.String(), "Services are still starting...")
	require.Contains(t, exposeStdout.String(), fmt.Sprintf("tcp://localhost:%d", port))
	requireDetachableUpHTTP(t, port, "delayed")
	descriptor := inspectDetachableUpSession(ctx, t, workspace, stateHome, sessionID)
	require.NotNil(t, descriptor.Query)
	require.Equal(t, engine.SessionQueryStateSucceeded, descriptor.Query.Status)
	require.Equal(t, 1, countRetainedUpNames(descriptor))
	require.Equal(t, 1, countTunnelPorts(descriptor))
}

func (DetachableUpSuite) TestSetSecretBackendRestarts(ctx context.Context, t *testctx.T) {
	protectedPort := reserveDetachableUpPort(t)
	siblingPort := reserveDetachableUpPort(t)
	workspace := writeDetachableUpSecretModule(t, protectedPort, siblingPort, false)
	stateHome := shortDetachableUpStateHome(t)

	output, err := detachableUpCLIOutput(ctx, t, workspace, stateHome, "up", "--detach")
	require.NoError(t, err)
	sessionID := detachableUpSessionID(t, output)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
		defer cancel()
		_, _ = detachableUpCLIOutput(cleanupCtx, t, workspace, stateHome, "sessions", "terminate", sessionID)
	})
	requireDetachableUpHTTP(t, protectedPort, "set-secret")
	requireDetachableUpHTTP(t, siblingPort, "sibling")
	quitDetachableUpBackend(t, protectedPort)
	require.Eventually(t, func() bool {
		return !detachableUpPortAccepts(protectedPort) && !detachableUpPortAccepts(siblingPort)
	}, 20*time.Second, 100*time.Millisecond)

	exposeOutput, err := detachableUpCLIOutput(ctx, t, workspace, stateHome, "sessions", "expose", sessionID)
	require.NoError(t, err)
	require.Contains(t, string(exposeOutput), fmt.Sprintf("tcp://localhost:%d", protectedPort))
	require.Contains(t, string(exposeOutput), fmt.Sprintf("tcp://localhost:%d", siblingPort))
	requireDetachableUpHTTP(t, protectedPort, "set-secret")
	requireDetachableUpHTTP(t, siblingPort, "sibling")
}

func (DetachableUpSuite) TestURISecretBackendRestartFailsFast(ctx context.Context, t *testctx.T) {
	protectedPort := reserveDetachableUpPort(t)
	siblingPort := reserveDetachableUpPort(t)
	workspace := writeDetachableUpSecretModule(t, protectedPort, siblingPort, true)
	stateHome := shortDetachableUpStateHome(t)

	output, err := detachableUpCLIOutputWithEnv(
		ctx, t, workspace, stateHome,
		map[string]string{"DETACHABLE_UP_URI_SECRET": "uri-secret"},
		"up", "--detach",
	)
	require.NoError(t, err)
	sessionID := detachableUpSessionID(t, output)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
		defer cancel()
		_, _ = detachableUpCLIOutput(cleanupCtx, t, workspace, stateHome, "sessions", "terminate", sessionID)
	})
	requireDetachableUpHTTP(t, protectedPort, "uri-secret")
	requireDetachableUpHTTP(t, siblingPort, "sibling")
	quitDetachableUpBackend(t, protectedPort)
	require.Eventually(t, func() bool {
		return !detachableUpPortAccepts(protectedPort) && !detachableUpPortAccepts(siblingPort)
	}, 20*time.Second, 100*time.Millisecond)

	_, exposeErr := detachableUpCLIOutput(ctx, t, workspace, stateHome, "sessions", "expose", sessionID)
	require.ErrorContains(t, exposeErr, "no available client binding")
	require.ErrorContains(t, exposeErr, "no active session attachables")
	require.Equal(t, 1, detachableUpExitCode(t, exposeErr))
	descriptor := inspectDetachableUpSession(ctx, t, workspace, stateHome, sessionID)
	require.Equal(t, 0, countTunnelPorts(descriptor))
	require.Equal(t, map[string]bool{"secret-fixture:sibling": true}, retainedUpNames(descriptor))
}

func (DetachableUpSuite) TestHostSocketDependencyBreaksAfterCreatorExit(ctx context.Context, t *testctx.T) {
	protectedPort := reserveDetachableUpPort(t)
	siblingPort := reserveDetachableUpPort(t)
	socketDir, err := os.MkdirTemp("", "du-socket-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(socketDir)) })
	socketPath := filepath.Join(socketDir, "source.sock")
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })
	listenerCtx, stopListener := context.WithCancel(context.Background())
	t.Cleanup(stopListener)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("creator-socket"))
			_ = conn.Close()
			select {
			case <-listenerCtx.Done():
				return
			default:
			}
		}
	}()

	workspace := writeDetachableUpSocketModule(t, protectedPort, siblingPort, socketPath)
	stateHome := shortDetachableUpStateHome(t)
	output, err := detachableUpCLIOutput(ctx, t, workspace, stateHome, "up", "--detach")
	require.NoError(t, err)
	sessionID := detachableUpSessionID(t, output)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
		defer cancel()
		_, _ = detachableUpCLIOutput(cleanupCtx, t, workspace, stateHome, "sessions", "terminate", sessionID)
	})
	requireDetachableUpHTTP(t, protectedPort, "socket-backed")
	requireDetachableUpHTTP(t, siblingPort, "sibling")

	require.Eventually(t, func() bool {
		return !detachableUpPortAccepts(protectedPort) && !detachableUpPortAccepts(siblingPort)
	}, 25*time.Second, 100*time.Millisecond)
	require.Eventually(t, func() bool {
		descriptor := inspectDetachableUpSession(ctx, t, workspace, stateHome, sessionID)
		return countTunnelPorts(descriptor) == 0 &&
			len(retainedUpNames(descriptor)) == 1 &&
			retainedUpNames(descriptor)["socket-fixture:sibling"]
	}, 20*time.Second, 200*time.Millisecond)
}

func (DetachableUpSuite) TestNonContainerServiceFailureParity(ctx context.Context, t *testctx.T) {
	workspace := writeDetachableUpNonContainerModule(t, reserveDetachableUpPort(t))
	stateHome := shortDetachableUpStateHome(t)
	before := detachableUpSessionIDs(ctx, t, workspace, stateHome)

	_, detachedErr := detachableUpCLIOutput(ctx, t, workspace, stateHome, "up", "--detach")
	require.ErrorContains(t, detachedErr, "tunneling to non-Container services is not supported")
	require.Equal(t, 1, detachableUpExitCode(t, detachedErr))
	require.Equal(t, before, detachableUpSessionIDs(ctx, t, workspace, stateHome))

	_, attachedErr := detachableUpCLIOutput(ctx, t, workspace, stateHome, "up")
	require.ErrorContains(t, attachedErr, "tunneling to non-Container services is not supported")
	require.Equal(t, 1, detachableUpExitCode(t, attachedErr))
}

func (DetachableUpSuite) TestBindConflictRollsBack(ctx context.Context, t *testctx.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })
	nativePort := listener.Addr().(*net.TCPAddr).Port
	workspace := writeDetachableUpModule(
		t, nativePort, reserveDetachableUpPort(t), reserveDetachableUpPort(t),
	)
	stateHome := t.TempDir()
	before := detachableUpSessionIDs(ctx, t, workspace, stateHome)

	_, runErr := detachableUpCLIOutput(ctx, t, workspace, stateHome, "up", "--detach")
	require.ErrorContains(t, runErr, "address already in use")
	require.Equal(t, 1, detachableUpExitCode(t, runErr))
	require.Equal(t, before, detachableUpSessionIDs(ctx, t, workspace, stateHome))
	records, err := filepath.Glob(filepath.Join(stateHome, "dagger", "expose", "*.pid"))
	require.NoError(t, err)
	require.Empty(t, records)
}

func (DetachableUpSuite) TestInterruptAfterAcknowledgementRollsBack(ctx context.Context, t *testctx.T) {
	workspace := writeDetachableUpModuleWithNginxPath(
		t,
		reserveDetachableUpPort(t),
		reserveDetachableUpPort(t),
		reserveDetachableUpPort(t),
		"/etc/nginx/http.d/default.conf",
	)
	stateHome := t.TempDir()
	before := detachableUpSessionIDs(ctx, t, workspace, stateHome)
	commandCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := hostDaggerCommand(commandCtx, t, workspace, "up", "--detach")
	setCommandEnv(cmd, "XDG_STATE_HOME", stateHome)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.MultiWriter(testutil.NewTWriter(t), &stderr)
	require.NoError(t, cmd.Start())
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	finished := false
	t.Cleanup(func() {
		if !finished {
			_ = cmd.Process.Kill()
			<-waitDone
		}
	})

	var interruptedSession string
	require.Eventually(t, func() bool {
		for id := range detachableUpSessionIDs(ctx, t, workspace, stateHome) {
			if _, existed := before[id]; !existed {
				interruptedSession = id
				return true
			}
		}
		return false
	}, 45*time.Second, 250*time.Millisecond, "detachable up was not acknowledged")
	require.NoError(t, cmd.Process.Signal(os.Interrupt))
	var waitErr error
	select {
	case waitErr = <-waitDone:
		finished = true
	case <-time.After(30 * time.Second):
		t.Fatalf("detached up did not roll back after interrupt\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	require.Equal(t, 130, detachableUpExitCode(t, waitErr), stderr.String()+stdout.String())
	require.Eventually(t, func() bool {
		_, exists := detachableUpSessionIDs(ctx, t, workspace, stateHome)[interruptedSession]
		return !exists
	}, 20*time.Second, 250*time.Millisecond)
}

func reserveDetachableUpPort(t testing.TB) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	return port
}

func shortDetachableUpStateHome(t testing.TB) string {
	t.Helper()
	stateHome, err := os.MkdirTemp("", "du-state-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(stateHome)) })
	return stateHome
}

func detachableUpSessionID(t testing.TB, output []byte) string {
	t.Helper()
	matches := regexp.MustCompile(`(?m)^Detached session (sess_[a-z2-7]{26})$`).FindSubmatch(output)
	require.Len(t, matches, 2, string(output))
	return string(matches[1])
}

func quitDetachableUpBackend(t testing.TB, port int) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/quit", port))
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusOK, response.StatusCode)
}

func writeDetachableUpModule(t testing.TB, nativePort, fixedBackend, fixedFrontend int) string {
	return writeDetachableUpModuleWithNginxPath(
		t, nativePort, fixedBackend, fixedFrontend, "/etc/nginx/conf.d/default.conf",
	)
}

func writeDetachableUpModuleWithNginxPath(
	t testing.TB,
	nativePort int,
	fixedBackend int,
	fixedFrontend int,
	nginxPath string,
) string {
	t.Helper()
	workspace := t.TempDir()
	gitInit := exec.Command("git", "init", workspace)
	gitOutput, err := gitInit.CombinedOutput()
	require.NoError(t, err, string(gitOutput))
	moduleDir := filepath.Join(workspace, "module")
	require.NoError(t, os.MkdirAll(moduleDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "dagger.toml"), []byte(fmt.Sprintf(`
[modules.hello-with-services]
source = "./module"
up.skip = ["redis"]

[ports.%d]
backendService = "hello-with-services:infra:database"
backendPort = %d
`, fixedFrontend, fixedBackend)), 0o644))
	knownModule := testDataPath(t, "services", "hello-with-services")
	for _, name := range []string{"dagger.json", "go.mod", "go.sum"} {
		contents, err := os.ReadFile(filepath.Join(knownModule, name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(moduleDir, name), contents, 0o644))
	}
	source := fmt.Sprintf(`// fixture %s
package main

import "dagger/hello-with-services/internal/dagger"

type HelloWithServices struct{}

// +up
func (m *HelloWithServices) Web() *dagger.Service {
	return dag.Container().
		From("nginx:alpine").
		WithNewFile(%q, %q + %q).
		WithExposedPort(%d).
		AsService()
}

type Infra struct{}

func (m *HelloWithServices) Infra() *Infra { return &Infra{} }

// +up
func (i *Infra) Database() *dagger.Service {
	return dag.Container().
		From("nginx:alpine").
		WithNewFile(%q, %q).
		WithExposedPort(%d).
		AsService()
}
`, identity.NewID(), nginxPath, nginxConfig(nativePort, "native"), nginxConfig(fixedBackend, "extra"), nativePort,
		nginxPath, nginxConfig(fixedBackend, "fixed"), fixedBackend)
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "main.go"), []byte(source), 0o644))
	return workspace
}

func writeDetachableUpRestartModule(t testing.TB, restartPort, siblingPort int) string {
	t.Helper()
	workspace := t.TempDir()
	gitInit := exec.Command("git", "init", workspace)
	gitOutput, err := gitInit.CombinedOutput()
	require.NoError(t, err, string(gitOutput))
	moduleDir := filepath.Join(workspace, "module")
	require.NoError(t, os.MkdirAll(moduleDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "dagger.toml"), []byte(`
[modules.restart-fixture]
source = "./module"
`), 0o644))
	knownModule := testDataPath(t, "services", "hello-with-services")
	for _, name := range []string{"dagger.json", "go.mod", "go.sum"} {
		contents, err := os.ReadFile(filepath.Join(knownModule, name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(moduleDir, name), contents, 0o644))
	}
	source := fmt.Sprintf(`// fixture %s
package main

import "dagger/hello-with-services/internal/dagger"

type HelloWithServices struct{}

// +up
func (m *HelloWithServices) Restartable() *dagger.Service {
	return dag.Container().
		From("python:alpine").
		WithNewFile("/server.py", %q).
		WithExposedPort(%d).
		AsService(dagger.ContainerAsServiceOpts{Args: []string{"python", "/server.py"}})
}

// +up
func (m *HelloWithServices) Sibling() *dagger.Service {
	return dag.Container().
		From("nginx:alpine").
		WithNewFile("/etc/nginx/conf.d/default.conf", %q).
		WithExposedPort(%d).
		AsService()
}
`, identity.NewID(), restartableHTTPServer(restartPort), restartPort,
		nginxConfig(siblingPort, "sibling"), siblingPort)
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "main.go"), []byte(source), 0o644))
	return workspace
}

func writeDetachableUpDelayedModule(t testing.TB, port int, delay time.Duration) string {
	t.Helper()
	workspace := t.TempDir()
	gitInit := exec.Command("git", "init", workspace)
	gitOutput, err := gitInit.CombinedOutput()
	require.NoError(t, err, string(gitOutput))
	moduleDir := filepath.Join(workspace, "module")
	require.NoError(t, os.MkdirAll(moduleDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "dagger.toml"), []byte(`
[modules.delayed-fixture]
source = "./module"
`), 0o644))
	knownModule := testDataPath(t, "services", "hello-with-services")
	for _, name := range []string{"dagger.json", "go.mod", "go.sum"} {
		contents, err := os.ReadFile(filepath.Join(knownModule, name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(moduleDir, name), contents, 0o644))
	}
	server := fmt.Sprintf(`import http.server
import time

time.sleep(%f)

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"delayed")

    def log_message(self, format, *args):
        pass

http.server.HTTPServer(("0.0.0.0", %d), Handler).serve_forever()
`, delay.Seconds(), port)
	source := fmt.Sprintf(`// fixture %s
package main

import "dagger/hello-with-services/internal/dagger"

type HelloWithServices struct{}

// +up
func (m *HelloWithServices) Delayed() *dagger.Service {
	return dag.Container().
		From("python:alpine").
		WithNewFile("/server.py", %q).
		WithExposedPort(%d).
		AsService(dagger.ContainerAsServiceOpts{Args: []string{"python", "/server.py"}})
}
`, identity.NewID(), server, port)
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "main.go"), []byte(source), 0o644))
	return workspace
}

func writeDetachableUpSecretModule(t testing.TB, protectedPort, siblingPort int, useURI bool) string {
	t.Helper()
	workspace := t.TempDir()
	gitInit := exec.Command("git", "init", workspace)
	gitOutput, err := gitInit.CombinedOutput()
	require.NoError(t, err, string(gitOutput))
	moduleDir := filepath.Join(workspace, "module")
	require.NoError(t, os.MkdirAll(moduleDir, 0o755))
	workspaceConfig := `
[modules.secret-fixture]
source = "./module"
`
	if useURI {
		workspaceConfig += `
[modules.secret-fixture.settings]
secret = "env://DETACHABLE_UP_URI_SECRET"
`
	}
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "dagger.toml"), []byte(workspaceConfig), 0o644))
	knownModule := testDataPath(t, "services", "hello-with-services")
	for _, name := range []string{"dagger.json", "go.mod", "go.sum"} {
		contents, err := os.ReadFile(filepath.Join(knownModule, name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(moduleDir, name), contents, 0o644))
	}
	secretExpression := `dag.SetSecret("detachable-up-set-secret", "set-secret")`
	rootDeclaration := `type HelloWithServices struct{}`
	if useURI {
		secretExpression = `m.Secret`
		rootDeclaration = `type HelloWithServices struct {
	Secret *dagger.Secret
}

func New(secret *dagger.Secret) *HelloWithServices {
	return &HelloWithServices{Secret: secret}
}`
	}
	source := fmt.Sprintf(`// fixture %s
package main

import "dagger/hello-with-services/internal/dagger"

%s

// +up
func (m *HelloWithServices) Protected() *dagger.Service {
	secret := %s
	return dag.Container().
		From("python:alpine").
		WithNewFile("/server.py", %q).
		WithSecretVariable("BODY", secret).
		WithExposedPort(%d).
		AsService(dagger.ContainerAsServiceOpts{Args: []string{"python", "/server.py"}})
}

// +up
func (m *HelloWithServices) Sibling() *dagger.Service {
	return dag.Container().
		From("nginx:alpine").
		WithNewFile("/etc/nginx/conf.d/default.conf", %q).
		WithExposedPort(%d).
		AsService()
}
`, identity.NewID(), rootDeclaration, secretExpression, secretBackedHTTPServer(protectedPort), protectedPort,
		nginxConfig(siblingPort, "sibling"), siblingPort)
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "main.go"), []byte(source), 0o644))
	return workspace
}

func writeDetachableUpSocketModule(t testing.TB, protectedPort, siblingPort int, socketPath string) string {
	t.Helper()
	workspace := t.TempDir()
	gitInit := exec.Command("git", "init", workspace)
	gitOutput, err := gitInit.CombinedOutput()
	require.NoError(t, err, string(gitOutput))
	moduleDir := filepath.Join(workspace, "module")
	require.NoError(t, os.MkdirAll(moduleDir, 0o755))
	workspaceConfig := fmt.Sprintf(`
[modules.socket-fixture]
source = "./module"

[modules.socket-fixture.settings]
socket = "unix://%s"
`, socketPath)
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "dagger.toml"), []byte(workspaceConfig), 0o644))
	knownModule := testDataPath(t, "services", "hello-with-services")
	for _, name := range []string{"dagger.json", "go.mod", "go.sum"} {
		contents, err := os.ReadFile(filepath.Join(knownModule, name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(moduleDir, name), contents, 0o644))
	}
	source := fmt.Sprintf(`// fixture %s
package main

import "dagger/hello-with-services/internal/dagger"

type HelloWithServices struct {
	Socket *dagger.Socket
}

func New(socket *dagger.Socket) *HelloWithServices {
	return &HelloWithServices{Socket: socket}
}

// +up
func (m *HelloWithServices) Protected() *dagger.Service {
	return dag.Container().
		From("python:alpine").
		WithNewFile("/server.py", %q).
		WithUnixSocket("/source.sock", m.Socket).
		WithExposedPort(%d).
		AsService(dagger.ContainerAsServiceOpts{Args: []string{"python", "/server.py"}})
}

// +up
func (m *HelloWithServices) Sibling() *dagger.Service {
	return dag.Container().
		From("nginx:alpine").
		WithNewFile("/etc/nginx/conf.d/default.conf", %q).
		WithExposedPort(%d).
		AsService()
}
`, identity.NewID(), socketBackedHTTPServer(protectedPort), protectedPort,
		nginxConfig(siblingPort, "sibling"), siblingPort)
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "main.go"), []byte(source), 0o644))
	return workspace
}

func writeDetachableUpNonContainerModule(t testing.TB, port int) string {
	t.Helper()
	workspace := t.TempDir()
	gitInit := exec.Command("git", "init", workspace)
	gitOutput, err := gitInit.CombinedOutput()
	require.NoError(t, err, string(gitOutput))
	moduleDir := filepath.Join(workspace, "module")
	require.NoError(t, os.MkdirAll(moduleDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "dagger.toml"), []byte(`
[modules.non-container-fixture]
source = "./module"
`), 0o644))
	knownModule := testDataPath(t, "services", "hello-with-services")
	for _, name := range []string{"dagger.json", "go.mod", "go.sum"} {
		contents, err := os.ReadFile(filepath.Join(knownModule, name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(moduleDir, name), contents, 0o644))
	}
	source := fmt.Sprintf(`// fixture %s
package main

import "dagger/hello-with-services/internal/dagger"

type HelloWithServices struct{}

// +up
func (m *HelloWithServices) Relay() *dagger.Service {
	return dag.Address(%q).Service()
}
`, identity.NewID(), fmt.Sprintf("tcp://localhost:%d", port))
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "main.go"), []byte(source), 0o644))
	return workspace
}

func socketBackedHTTPServer(port int) string {
	return fmt.Sprintf(`import http.server
import os
import socket
import threading
import time

def probe_later():
    time.sleep(12)
    try:
        source = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        source.settimeout(3)
        source.connect("/source.sock")
        if source.recv(64) != b"creator-socket":
            os._exit(2)
        source.close()
    except Exception:
        os._exit(3)

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"socket-backed")

    def log_message(self, format, *args):
        pass

threading.Thread(target=probe_later, daemon=True).start()
http.server.HTTPServer(("0.0.0.0", %d), Handler).serve_forever()
`, port)
}

func secretBackedHTTPServer(port int) string {
	return fmt.Sprintf(`import http.server
import os
import threading
import time

body = os.environ["BODY"].encode()

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        if self.path == "/quit":
            self.wfile.write(b"bye")
            threading.Thread(target=self.exit_soon, daemon=True).start()
        else:
            self.wfile.write(body)

    def exit_soon(self):
        time.sleep(0.2)
        os._exit(0)

    def log_message(self, format, *args):
        pass

http.server.HTTPServer(("0.0.0.0", %d), Handler).serve_forever()
`, port)
}

func restartableHTTPServer(port int) string {
	return fmt.Sprintf(`import http.server
import os
import threading
import time

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        if self.path == "/quit":
            self.wfile.write(b"bye")
            threading.Thread(target=self.exit_soon, daemon=True).start()
        else:
            self.wfile.write(b"restartable")

    def exit_soon(self):
        time.sleep(0.2)
        os._exit(0)

    def log_message(self, format, *args):
        pass

http.server.HTTPServer(("0.0.0.0", %d), Handler).serve_forever()
`, port)
}

func nginxConfig(port int, body string) string {
	return fmt.Sprintf("server { listen %d; location / { return 200 '%s'; } }", port, body)
}

func detachableUpCLIOutput(
	ctx context.Context,
	t testing.TB,
	workdir string,
	stateHome string,
	args ...string,
) ([]byte, error) {
	return detachableUpCLIOutputWithEnv(ctx, t, workdir, stateHome, nil, args...)
}

func detachableUpCLIOutputWithEnv(
	ctx context.Context,
	t testing.TB,
	workdir string,
	stateHome string,
	environment map[string]string,
	args ...string,
) ([]byte, error) {
	t.Helper()
	commandCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := hostDaggerCommand(commandCtx, t, workdir, args...)
	setCommandEnv(cmd, "XDG_STATE_HOME", stateHome)
	for name, value := range environment {
		setCommandEnv(cmd, name, value)
	}
	var stderr bytes.Buffer
	cmd.Stderr = io.MultiWriter(testutil.NewTWriter(t), &stderr)
	output, err := cmd.Output()
	if err != nil {
		logs, _ := filepath.Glob(filepath.Join(stateHome, "dagger", "expose", "*.log"))
		var portServerLogs bytes.Buffer
		for _, logPath := range logs {
			contents, readErr := os.ReadFile(logPath)
			if readErr == nil {
				fmt.Fprintf(&portServerLogs, "\n%s:\n%s", logPath, contents)
			}
		}
		return output, fmt.Errorf(
			"stdout: %s\nstderr: %s\nport server logs:%s: %w",
			output, stderr.String(), portServerLogs.String(), err,
		)
	}
	return output, nil
}

func detachableUpSessionIDs(
	ctx context.Context,
	t testing.TB,
	workdir string,
	stateHome string,
) map[string]struct{} {
	t.Helper()
	output, err := detachableUpCLIOutput(ctx, t, workdir, stateHome, "sessions", "list", "--json")
	require.NoError(t, err)
	var descriptors []engine.SessionDescriptor
	require.NoError(t, json.Unmarshal(output, &descriptors), string(output))
	ids := make(map[string]struct{}, len(descriptors))
	for _, descriptor := range descriptors {
		ids[descriptor.ID] = struct{}{}
	}
	return ids
}

func detachableUpExposePID(t testing.TB, recordPath string) int {
	t.Helper()
	recordBytes, err := os.ReadFile(recordPath)
	require.NoError(t, err)
	var record struct {
		PID   int    `json:"pid"`
		State string `json:"state"`
	}
	require.NoError(t, json.Unmarshal(recordBytes, &record))
	require.Equal(t, "ready", record.State)
	require.Positive(t, record.PID)
	return record.PID
}

func detachableUpSummaryPort(t testing.TB, output []byte, service string) int {
	t.Helper()
	pattern := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(service) + `  [a-z]+://localhost:([0-9]+)$`)
	match := pattern.FindSubmatch(output)
	require.Len(t, match, 2, string(output))
	var port int
	_, err := fmt.Sscanf(string(match[1]), "%d", &port)
	require.NoError(t, err)
	return port
}

func detachableUpExitCode(t testing.TB, err error) int {
	t.Helper()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr.ExitCode()
}

func inspectDetachableUpSession(
	ctx context.Context,
	t testing.TB,
	workdir string,
	stateHome string,
	sessionID string,
) engine.SessionDescriptor {
	t.Helper()
	output, err := detachableUpCLIOutput(ctx, t, workdir, stateHome, "sessions", "inspect", sessionID, "--json")
	require.NoError(t, err)
	var descriptor engine.SessionDescriptor
	require.NoError(t, json.Unmarshal(output, &descriptor), string(output))
	return descriptor
}

func countRetainedUpNames(descriptor engine.SessionDescriptor) int {
	return len(retainedUpNames(descriptor))
}

func retainedUpNames(descriptor engine.SessionDescriptor) map[string]bool {
	names := map[string]struct{}{}
	for _, service := range descriptor.Services {
		if service.Retained {
			for _, name := range service.Names {
				names[name] = struct{}{}
			}
		}
	}
	result := make(map[string]bool, len(names))
	for name := range names {
		result[name] = true
	}
	return result
}

func countTunnelPorts(descriptor engine.SessionDescriptor) int {
	count := 0
	for _, service := range descriptor.Services {
		if service.TunnelUpstream != nil {
			count += len(service.Ports)
		}
	}
	return count
}

func requireDetachableUpHTTP(t testing.TB, port int, expected string) {
	t.Helper()
	require.Eventually(t, func() bool {
		client := &http.Client{Timeout: time.Second}
		response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d", port))
		if err != nil {
			return false
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		return err == nil && string(body) == expected
	}, 30*time.Second, 100*time.Millisecond)
}

func detachableUpPortAccepts(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
