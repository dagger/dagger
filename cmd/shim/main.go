package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/uuid"
	"github.com/opencontainers/runtime-spec/specs-go"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/network"
	"github.com/dagger/dagger/telemetry"
)

const (
	metaMountPath = "/.dagger_meta_mount"
	stdinPath     = metaMountPath + "/stdin"
	exitCodePath  = metaMountPath + "/exitCode"
	runcPath      = "/usr/local/bin/runc"
	shimPath      = metaMountPath + "/shim"

	errorExitCode = 125
)

var (
	stdoutPath = metaMountPath + "/stdout"
	stderrPath = metaMountPath + "/stderr"
	pipeWg     sync.WaitGroup
)

/*
There are two "subcommands" of this binary:
 1. The setupBundle command, which is invoked by buildkitd as the oci executor. It updates the
    spec provided by buildkitd's executor to wrap the command in our shim (described below).
    It then exec's to runc which will do the actual container setup+execution.
 2. The shim, which is included in each Container.Exec and enables us to capture/redirect stdio,
    capture the exit code, etc.
*/
func main() {
	defer func() {
		if err := recover(); err != nil {
			os.Exit(errorExitCode)
		}
	}()

	if os.Args[0] == shimPath {
		if _, found := internalEnv("_DAGGER_INTERNAL_COMMAND"); found {
			os.Exit(internalCommand())
			return
		}

		// If we're being executed as `/_shim`, then we're inside the container and should shim
		// the user command.
		os.Exit(shim())
	} else {
		// Otherwise, we're being invoked directly by buildkitd and should setup the bundle.
		os.Exit(setupBundle())
	}
}

func internalCommand() int {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <command> [<args>]\n", os.Args[0])
		return errorExitCode
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "check":
		if err := check(args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return errorExitCode
		}
		return 0
	case "tunnel":
		if err := tunnel(args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		return errorExitCode
	}
}

func check(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: check <host> port/tcp [port/udp ...]")
	}

	host, ports := args[0], args[1:]

	for _, port := range ports {
		port, network, ok := strings.Cut(port, "/")
		if !ok {
			network = "tcp"
		}

		pollAddr := net.JoinHostPort(host, port)

		fmt.Println("polling for port", pollAddr)

		reached, err := pollForPort(network, pollAddr)
		if err != nil {
			return fmt.Errorf("poll %s: %w", pollAddr, err)
		}

		fmt.Println("port is up at", reached)
	}

	return nil
}

func pollForPort(network, addr string) (string, error) {
	retry := backoff.NewExponentialBackOff()
	retry.InitialInterval = 100 * time.Millisecond

	dialer := net.Dialer{
		Timeout: time.Second,
	}

	var reached string
	err := backoff.Retry(func() error {
		// NB(vito): it's a _little_ silly to dial a UDP network to see that it's
		// up, since it'll be a false positive even if they're not listening yet,
		// but it at least checks that we're able to resolve the container address.

		conn, err := dialer.Dial(network, addr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "port not ready: %s; elapsed: %s\n", err, retry.GetElapsedTime())
			return err
		}

		reached = conn.RemoteAddr().String()

		_ = conn.Close()

		return nil
	}, retry)
	if err != nil {
		return "", err
	}

	return reached, nil
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

	// Set up slog initially to log directly to stderr, in case something goes
	// wrong with the logging setup.
	slog.SetDefault(telemetry.PrettyLogger(os.Stderr, slog.LevelWarn))

	cleanup, err := proxyOtelToTCP()
	if err == nil {
		defer cleanup()
	} else {
		fmt.Fprintln(errWriter, "failed to set up opentelemetry proxy:", err)
	}

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

	logCtx := ctx
	if p, ok := os.LookupEnv("DAGGER_FUNCTION_TRACEPARENT"); ok {
		logCtx = propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": p})
	}

	ctx, stdoutOtel, stderrOtel := telemetry.WithStdioToOtel(logCtx, "dagger.io/shim")

	name := os.Args[1]
	args := []string{}
	if len(os.Args) > 2 {
		args = os.Args[2:]
	}

	cmd := exec.Command(name, args...)
	_, isTTY := internalEnv(core.ShimEnableTTYEnvVar)
	if isTTY {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		if stdinFile, err := os.Open(stdinPath); err == nil {
			defer stdinFile.Close()
			cmd.Stdin = stdinFile
		} else {
			cmd.Stdin = nil
		}

		stderrFile, err := os.OpenFile(stderrPath, os.O_WRONLY|os.O_APPEND, 0o666)
		if err != nil {
			panic(err)
		}
		defer stderrFile.Close()
		errWriter = io.MultiWriter(stderrFile, stderrOtel, os.Stderr)
		stderrRedirectPath, found := internalEnv("_DAGGER_REDIRECT_STDERR")
		if found {
			stderrRedirectFile, err := os.Create(stderrRedirectPath)
			if err != nil {
				panic(err)
			}
			defer stderrRedirectFile.Close()
			errWriter = io.MultiWriter(stderrFile, stderrRedirectFile, stderrOtel, os.Stderr)
		}

		var outWriter io.Writer = os.Stdout
		stdoutFile, err := os.OpenFile(stdoutPath, os.O_WRONLY|os.O_APPEND, 0o666)
		if err != nil {
			panic(err)
		}
		defer stdoutFile.Close()
		outWriter = io.MultiWriter(stdoutFile, stdoutOtel, os.Stdout)
		stdoutRedirectPath, found := internalEnv("_DAGGER_REDIRECT_STDOUT")
		if found {
			stdoutRedirectFile, err := os.Create(stdoutRedirectPath)
			if err != nil {
				panic(err)
			}
			defer stdoutRedirectFile.Close()
			outWriter = io.MultiWriter(stdoutFile, stdoutRedirectFile, stdoutOtel, os.Stdout)
		}

		var secretsToScrub core.SecretToScrubInfo
		secretsToScrubVar, found := internalEnv("_DAGGER_SCRUB_SECRETS")
		if found {
			err := json.Unmarshal([]byte(secretsToScrubVar), &secretsToScrub)
			if err != nil {
				panic(fmt.Errorf("cannot load secrets to scrub: %w", err))
			}
		}

		currentDirPath := "/"
		shimFS := os.DirFS(currentDirPath)

		// Direct slog to the new stderr. This is only for dev time debugging, and
		// runtime errors/warnings.
		slog.SetDefault(telemetry.PrettyLogger(errWriter, slog.LevelWarn))

		if len(secretsToScrub.Envs) == 0 && len(secretsToScrub.Files) == 0 {
			cmd.Stdout = outWriter
			cmd.Stderr = errWriter
		} else {
			// Get pipes for command's stdout and stderr and process output
			// through secret scrub reader in multiple goroutines:
			envToScrub := os.Environ()
			stdoutPipe, err := cmd.StdoutPipe()
			if err != nil {
				panic(err)
			}
			scrubOutReader, err := NewSecretScrubReader(stdoutPipe, currentDirPath, shimFS, envToScrub, secretsToScrub)
			if err != nil {
				panic(err)
			}
			pipeWg.Add(1)
			go func() {
				defer pipeWg.Done()
				io.Copy(outWriter, scrubOutReader)
			}()

			stderrPipe, err := cmd.StderrPipe()
			if err != nil {
				panic(err)
			}
			scrubErrReader, err := NewSecretScrubReader(stderrPipe, currentDirPath, shimFS, envToScrub, secretsToScrub)
			if err != nil {
				panic(err)
			}
			pipeWg.Add(1)
			go func() {
				defer pipeWg.Done()
				io.Copy(errWriter, scrubErrReader)
			}()
		}
	}

	exitCode := 0
	if err := runWithNesting(ctx, cmd); err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			exitCode = exiterr.ExitCode()
		} else {
			panic(err)
		}
	}

	if err := os.WriteFile(exitCodePath, []byte(fmt.Sprintf("%d", exitCode)), 0o666); err != nil {
		panic(err)
	}

	return exitCode
}

func setupBundle() (returnExitCode int) {
	var errWriter io.Writer = os.Stderr
	var stderrFile *os.File
	defer func() {
		if err := recover(); err != nil {
			fmt.Fprintf(errWriter, "shim error: %v\n%s", err, string(debug.Stack()))
			returnExitCode = errorExitCode + 1
		}
		if stderrFile != nil {
			stderrFile.Close()
		}
	}()

	// Figure out the path to the bundle dir, in which we can obtain the
	// oci runtime config.json
	var bundleDir string
	var isRun bool
	for i, arg := range os.Args {
		if arg == "--bundle" && i+1 < len(os.Args) {
			bundleDir = os.Args[i+1]
		}
		if arg == "run" {
			isRun = true
		}
	}
	if bundleDir == "" || !isRun {
		// this may be a different runc command, just passthrough
		return execRunc()
	}

	configPath := filepath.Join(bundleDir, "config.json")
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		panic(fmt.Errorf("error reading config.json: %w", err))
	}

	var spec specs.Spec
	if err := json.Unmarshal(configBytes, &spec); err != nil {
		panic(fmt.Errorf("error parsing config.json: %w", err))
	}

	// Check to see if this is a dagger exec, currently by using
	// the presence of the dagger meta mount. If it is, set up the
	// shim to be invoked as the init process. Otherwise, just
	// pass through as is
	var isDaggerExec bool
	for _, mnt := range spec.Mounts {
		if mnt.Destination == metaMountPath {
			isDaggerExec = true

			// setup the metaMount dir/files with the final container uid/gid owner so that
			// we guarantee the container init shim process can read/write them. This is needed
			// for the ca cert installer case where we may run some commands as root when the actual
			// user exec is non-root. Just setting 0666 perms isn't sufficient due to umask settings,
			// which we don't want to play with as it could subtly impact other things.
			if err := containerChownPath(mnt.Source, &spec); err != nil {
				panic(fmt.Errorf("error chowning meta mount path: %w", err))
			}
			for _, metaPath := range []string{
				stdinPath,
				stdoutPath,
				stderrPath,
				exitCodePath,
			} {
				path := filepath.Join(mnt.Source, strings.TrimPrefix(metaPath, metaMountPath))
				if err := containerTouchPath(path, 0o666, &spec); err != nil {
					panic(fmt.Errorf("error touching path %s: %w", path, err))
				}

				// for stderr specifically, also update errWriter to that file so that any
				// errors here actually show up in the final error message passed to clients
				if metaPath == stderrPath {
					var err error
					stderrFile, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o666)
					if err != nil {
						panic(err)
					}
					// don't both closing, will be closed when we exit and closing in defer here
					// break panic defer at beginning
					errWriter = io.MultiWriter(stderrFile, os.Stderr)
				}
			}

			break
		}
	}
	// We're running an internal shim command, i.e. a service health check
	for _, env := range spec.Process.Env {
		if strings.HasPrefix(env, "_DAGGER_INTERNAL_COMMAND=") {
			isDaggerExec = true
			break
		}
	}

	if isDaggerExec {
		// mount this executable into the container so it can be invoked as the shim
		selfPath, err := os.Executable()
		if err != nil {
			panic(fmt.Errorf("error getting self path: %w", err))
		}
		selfPath, err = filepath.EvalSymlinks(selfPath)
		if err != nil {
			panic(fmt.Errorf("error getting self path: %w", err))
		}
		spec.Mounts = append(spec.Mounts, specs.Mount{
			Destination: shimPath,
			Type:        "bind",
			Source:      selfPath,
			Options:     []string{"rbind", "ro"},
		})

		// update the args to specify the shim as the init process
		spec.Process.Args = append([]string{shimPath}, spec.Process.Args...)
	}

	var otelEndpoint string
	var otelProto string
	var serverID string
	var aliasEnvs []string
	keepEnv := []string{}
	for _, env := range spec.Process.Env {
		switch {
		case strings.HasPrefix(env, "_DAGGER_SERVER_ID="):
			keepEnv = append(keepEnv, env)
			serverID = strings.TrimPrefix(env, "_DAGGER_SERVER_ID=")
		case strings.HasPrefix(env, aliasPrefix):
			// NB: don't keep this env var, it's only for the bundling step
			// keepEnv = append(keepEnv, env)
			aliasEnvs = append(aliasEnvs, env)

			// filter out Buildkit's OTLP env vars, we have our own
		case strings.HasPrefix(env, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="):
			_, otelEndpoint, _ = strings.Cut(env, "=")

		case strings.HasPrefix(env, "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL="):
			_, otelProto, _ = strings.Cut(env, "=")

		default:
			keepEnv = append(keepEnv, env)
		}
	}
	spec.Process.Env = keepEnv

	var searchDomains []string
	if ns := serverID; ns != "" {
		searchDomains = append(searchDomains, network.ClientDomain(ns))
	}

	var hostsFilePath string
	for i, mnt := range spec.Mounts {
		switch mnt.Destination {
		case "/etc/hosts":
			hostsFilePath = mnt.Source
		case "/etc/resolv.conf":
			if len(searchDomains) == 0 {
				break
			}

			newResolvPath := filepath.Join(bundleDir, "resolv.conf")

			newResolv, err := os.Create(newResolvPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, "create resolv.conf:", err)
				return errorExitCode
			}

			if err := replaceSearch(newResolv, mnt.Source, searchDomains); err != nil {
				fmt.Fprintln(os.Stderr, "replace search:", err)
				return errorExitCode
			}

			if err := newResolv.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "close resolv.conf:", err)
				return errorExitCode
			}

			spec.Mounts[i].Source = newResolvPath
		}
	}
	for _, aliasEnv := range aliasEnvs {
		if err := appendHostAlias(hostsFilePath, aliasEnv, searchDomains); err != nil {
			fmt.Fprintln(os.Stderr, "host alias:", err)
			return errorExitCode + 3
		}
	}

	if otelEndpoint != "" {
		if strings.HasPrefix(otelEndpoint, "/") {
			// Buildkit currently sets this to /dev/otel-grpc.sock which is not a valid
			// endpoint URL despite being set in an OTEL_* env var.
			otelEndpoint = "unix://" + otelEndpoint
		}
		spec.Process.Env = append(spec.Process.Env,
			"OTEL_EXPORTER_OTLP_PROTOCOL="+otelProto,
			"OTEL_EXPORTER_OTLP_ENDPOINT="+otelEndpoint,
			// Re-set the otel env vars, but with a corrected otelEndpoint.
			"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL="+otelProto,
			"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="+otelEndpoint,
			// Dagger sets up a log exporter too. Explicitly set it so things can
			// detect support for it.
			"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL="+otelProto,
			"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT="+otelEndpoint,
			// Dagger doesn't set up metrics yet, but we should set this anyway,
			// since otherwise some tools default to localhost.
			"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL="+otelProto,
			"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT="+otelEndpoint,
		)
	}

	// write the updated config
	configBytes, err = json.Marshal(spec)
	if err != nil {
		panic(fmt.Errorf("error marshaling config.json: %w", err))
	}
	if err := os.WriteFile(configPath, configBytes, 0o600); err != nil {
		panic(fmt.Errorf("error writing config.json: %w", err))
	}

	// Run the actual runc binary as a child process with the (possibly updated) config.
	cmd := exec.Command(runcPath, os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}

	exitCode := 0
	if err := execProcess(cmd, false); err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			if waitStatus, ok := exiterr.Sys().(syscall.WaitStatus); ok {
				exitCode = waitStatus.ExitStatus()
				if exitCode < 0 {
					exitCode = errorExitCode
				}
			} else {
				exitCode = errorExitCode
			}
		} else {
			exitCode = errorExitCode
		}
	}
	return exitCode
}

const aliasPrefix = "_DAGGER_HOSTNAME_ALIAS_"

func appendHostAlias(hostsFilePath string, env string, searchDomains []string) error {
	alias, target, ok := strings.Cut(strings.TrimPrefix(env, aliasPrefix), "=")
	if !ok {
		return fmt.Errorf("malformed host alias: %s", env)
	}

	var ips []net.IP
	var errs error
	for _, domain := range append([]string{""}, searchDomains...) {
		qualified := target

		if domain != "" {
			qualified += "." + domain
		}

		var err error
		ips, err = net.LookupIP(qualified)
		if err == nil {
			errs = nil // ignore prior failures
			break
		}

		errs = errors.Join(errs, err)
	}
	if errs != nil {
		return errs
	}

	hostsFile, err := os.OpenFile(hostsFilePath, os.O_APPEND|os.O_WRONLY, 0o777)
	if err != nil {
		return err
	}

	for _, ip := range ips {
		if _, err := fmt.Fprintf(hostsFile, "\n%s\t%s\n", ip, alias); err != nil {
			return err
		}
	}

	return hostsFile.Close()
}

//nolint:unparam
func execRunc() int {
	args := []string{runcPath}
	args = append(args, os.Args[1:]...)
	if err := unix.Exec(runcPath, args, os.Environ()); err != nil {
		panic(fmt.Errorf("error execing runc: %w", err))
	}
	panic("congratulations: you've reached unreachable code, please report a bug!")
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
		if waitForStreams {
			pipeWg.Wait()
		}
		if err := cmd.Wait(); err != nil {
			errCh <- err
			return
		}
	}()
	return <-errCh
}

func replaceSearch(dst io.Writer, resolv string, searchDomains []string) error {
	src, err := os.Open(resolv)
	if err != nil {
		return nil
	}
	defer src.Close()

	srcScan := bufio.NewScanner(src)

	var replaced bool
	for srcScan.Scan() {
		if !strings.HasPrefix(srcScan.Text(), "search") {
			fmt.Fprintln(dst, srcScan.Text())
			continue
		}

		oldDomains := strings.Fields(srcScan.Text())[1:]

		newDomains := append([]string{}, searchDomains...)
		newDomains = append(newDomains, oldDomains...)
		fmt.Fprintln(dst, "search", strings.Join(newDomains, " "))
		replaced = true
	}

	if !replaced {
		fmt.Fprintln(dst, "search", strings.Join(searchDomains, " "))
	}

	return nil
}

// Some OpenTelemetry clients don't support unix:// endpoints, so we proxy them
// through a TCP endpoint instead.
func proxyOtelToTCP() (cleanup func(), rerr error) {
	endpoints := map[string][]string{}
	for _, env := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
	} {
		if val := os.Getenv(env); val != "" {
			slog.Debug("found otel endpoint", "env", env, "endpoint", val)
			endpoints[val] = append(endpoints[val], env)
		}
	}
	closers := []func() error{}
	cleanup = func() {
		for _, closer := range closers {
			closer()
		}
	}
	defer func() {
		if rerr != nil {
			cleanup()
		}
	}()
	for endpoint, envs := range endpoints {
		if !strings.HasPrefix(endpoint, "unix://") {
			// We only need to fix up unix:// endpoints.
			continue
		}

		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return func() {}, fmt.Errorf("listen: %w", err)
		}
		closers = append(closers, l.Close)

		slog.Debug("listening for otel proxy", "endpoint", endpoint, "proxy", l.Addr().String())
		go proxyOtelSocket(l, endpoint)

		for _, env := range envs {
			slog.Debug("proxying otel endpoint", "env", env, "endpoint", endpoint)
			os.Setenv(env, "http://"+l.Addr().String())
		}
	}
	return cleanup, nil
}

func proxyOtelSocket(l net.Listener, endpoint string) {
	sockPath := strings.TrimPrefix(endpoint, "unix://")
	for {
		conn, err := l.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				slog.Error("failed to accept connection", "error", err)
			}
			return
		}

		slog.Debug("accepting otel connection", "endpoint", endpoint)

		go func() {
			defer conn.Close()

			remote, err := net.Dial("unix", sockPath)
			if err != nil {
				slog.Error("failed to dial socket", "error", err)
				return
			}
			defer remote.Close()

			go io.Copy(remote, conn)
			io.Copy(conn, remote)
		}()
	}
}

func containerTouchPath(path string, perms os.FileMode, spec *specs.Spec) error {
	// mknod w/ S_IFREG is equivalent to "touch"
	if err := unix.Mknod(path, uint32(perms)|unix.S_IFREG, 0); err != nil && !errors.Is(err, unix.EEXIST) {
		return err
	}
	return containerChownPath(path, spec)
}

func containerChownPath(path string, spec *specs.Spec) error {
	return unix.Chown(path, int(spec.Process.User.UID), int(spec.Process.User.GID))
}
