package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/telemetry"
)

const errorExitCode = 125

func main() {
	defer func() {
		if err := recover(); err != nil {
			os.Exit(errorExitCode)
		}
	}()

	os.Exit(shim())
}

func shim() (returnExitCode int) {
	var errWriter io.Writer = os.Stderr
	defer func() {
		if err := recover(); err != nil {
			fmt.Fprintf(errWriter, "shim error: %v\n%s", err, string(debug.Stack()))
			returnExitCode = errorExitCode + 2
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if len(os.Args) < 2 {
		panic(fmt.Errorf("usage: %s <path> [<args>]", os.Args[0]))
	}

	name := os.Args[1]
	args := []string{}
	if len(os.Args) > 2 {
		args = os.Args[2:]
	}

	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	exitCode := 0
	if err := runWithNesting(ctx, cmd); err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			exitCode = exiterr.ExitCode()
		} else {
			panic(err)
		}
	}

	return exitCode
}

func internalEnv(name string) (string, bool) {
	val, found := os.LookupEnv(name)
	if !found {
		return "", false
	}

	os.Unsetenv(name)

	return val, true
}

func runWithNesting(ctx context.Context, cmd *exec.Cmd) error {
	// remove this from being set in user process no matter what
	serverID, _ := internalEnv("_DAGGER_SERVER_ID")

	clientID, ok := internalEnv("_DAGGER_NESTED_CLIENT_ID")
	if !ok {
		// no nesting; run as normal
		return execProcess(cmd, true)
	}

	// setup a session and associated env vars for the container

	traceCfg := telemetry.Config{
		Detect: false, // false, since we want "live" exporting
		Resource: resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("dagger-shim"),
			semconv.ServiceVersionKey.String(engine.Version),
		),
	}
	if exp, ok := telemetry.ConfiguredSpanExporter(ctx); ok {
		traceCfg.LiveTraceExporters = append(traceCfg.LiveTraceExporters, exp)
	}
	if exp, ok := telemetry.ConfiguredLogExporter(ctx); ok {
		traceCfg.LiveLogExporters = append(traceCfg.LiveLogExporters, exp)
	}

	ctx = telemetry.Init(ctx, traceCfg)
	defer telemetry.Close()

	if serverID == "" {
		return fmt.Errorf("missing server ID")
	}

	sessionToken, engineErr := uuid.NewRandom()
	if engineErr != nil {
		return fmt.Errorf("error generating session token: %w", engineErr)
	}

	l, engineErr := net.Listen("tcp", "127.0.0.1:0")
	if engineErr != nil {
		return fmt.Errorf("error listening on session socket: %w", engineErr)
	}
	sessionPort := l.Addr().(*net.TCPAddr).Port

	clientParams := client.Params{
		ID:          clientID,
		ServerID:    serverID,
		SecretToken: sessionToken.String(),
		RunnerHost:  "unix:///.runner.sock",
	}

	sess, ctx, err := client.Connect(ctx, clientParams)
	if err != nil {
		return fmt.Errorf("error connecting to engine: %w", err)
	}
	defer sess.Close()

	_ = ctx // avoid ineffasign lint

	srv := &http.Server{ //nolint:gosec
		Handler:     sess,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	go srv.Serve(l)

	// pass dagger session along to any SDKs that run in the container
	os.Setenv("DAGGER_SESSION_PORT", strconv.Itoa(sessionPort))
	os.Setenv("DAGGER_SESSION_TOKEN", sessionToken.String())
	return execProcess(cmd, true)
}

// execProcess runs the command as a child process.
//
// It forwards all signals from the parent process into the child (e.g. so that
// the child can receive SIGTERM, etc). Additionally, it spawns a separate
// goroutine locked to the OS thread to ensure that Pdeathsig is never sent
// incorrectly: https://github.com/golang/go/issues/27505
func execProcess(cmd *exec.Cmd, waitForStreams bool) error {
	errCh := make(chan error)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(errCh)

		sigCh := make(chan os.Signal, 32)
		signal.Notify(sigCh)
		if err := cmd.Start(); err != nil {
			errCh <- err
			return
		}
		go func() {
			for sig := range sigCh {
				cmd.Process.Signal(sig)
			}
		}()
		if err := cmd.Wait(); err != nil {
			errCh <- err
			return
		}
	}()
	return <-errCh
}
