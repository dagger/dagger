package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	"github.com/containerd/console"
	"github.com/google/uuid"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/runtime-spec/specs-go"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/network"
	"github.com/dagger/dagger/telemetry"
)

const (
	metaMountPath = "/.dagger_meta_mount"
	stdinPath     = metaMountPath + "/stdin"
	exitCodePath  = metaMountPath + "/exitCode"
	runcPath      = "/usr/local/bin/runc"
	shimPath      = "/_shim"

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
			fmt.Fprintf(os.Stderr, "panic: %v %s\n", err, string(debug.Stack()))
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <path> [<args>]\n", os.Args[0])
		return errorExitCode
	}

	// Set up slog initially to log directly to stderr, in case something goes
	// wrong with the logging setup.
	slog.SetDefault(telemetry.PrettyLogger(os.Stderr, slog.LevelWarn))

	cleanup, err := proxyOtelToTCP()
	if err == nil {
		defer cleanup()
	} else {
		fmt.Fprintln(os.Stderr, "failed to set up opentelemetry proxy:", err)
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

	ctx, stdoutOtel, stderrOtel := telemetry.WithStdioToOtel(ctx, "dagger.io/shim")

	name := os.Args[1]
	args := []string{}
	if len(os.Args) > 2 {
		args = os.Args[2:]
	}

	cmd := exec.Command(name, args...)
	_, isTTY := internalEnv(core.ShimEnableTTYEnvVar)
	if isTTY {
		// Re-enable onlcr now that we're in the container.
		if err := toggleONLCR(true); err != nil {
			panic(err)
		}
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

		stdoutFile, err := os.Create(stdoutPath)
		if err != nil {
			panic(err)
		}
		defer stdoutFile.Close()
		stdoutRedirect := io.Discard
		stdoutRedirectPath, found := internalEnv("_DAGGER_REDIRECT_STDOUT")
		if found {
			stdoutRedirectFile, err := os.Create(stdoutRedirectPath)
			if err != nil {
				panic(err)
			}
			defer stdoutRedirectFile.Close()
			stdoutRedirect = stdoutRedirectFile
		}

		stderrFile, err := os.Create(stderrPath)
		if err != nil {
			panic(err)
		}
		defer stderrFile.Close()
		stderrRedirect := io.Discard
		stderrRedirectPath, found := internalEnv("_DAGGER_REDIRECT_STDERR")
		if found {
			stderrRedirectFile, err := os.Create(stderrRedirectPath)
			if err != nil {
				panic(err)
			}
			defer stderrRedirectFile.Close()
			stderrRedirect = stderrRedirectFile
		}

		outWriter := io.MultiWriter(stdoutFile, stdoutRedirect, stdoutOtel, os.Stdout)
		errWriter := io.MultiWriter(stderrFile, stderrRedirect, stderrOtel, os.Stderr)

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
			exitCode = errorExitCode
			fmt.Fprintln(os.Stderr, err.Error())
		}
	}

	if err := os.WriteFile(exitCodePath, []byte(fmt.Sprintf("%d", exitCode)), 0o600); err != nil {
		panic(err)
	}

	return exitCode
}

func setupBundle() int {
	// If we're running with a TTY, disable onlcr; it gets re-enabled on the
	// container side.
	if err := toggleONLCR(false); err != nil {
		panic(err)
	}

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
		fmt.Printf("Error reading config.json: %v\n", err)
		return errorExitCode
	}

	var spec specs.Spec
	if err := json.Unmarshal(configBytes, &spec); err != nil {
		fmt.Printf("Error parsing config.json: %v\n", err)
		return errorExitCode
	}

	// Check to see if this is a dagger exec, currently by using
	// the presence of the dagger meta mount. If it is, set up the
	// shim to be invoked as the init process. Otherwise, just
	// pass through as is
	var isDaggerExec bool
	for _, mnt := range spec.Mounts {
		if mnt.Destination == metaMountPath {
			isDaggerExec = true
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
			fmt.Printf("Error getting self path: %v\n", err)
			return errorExitCode
		}
		selfPath, err = filepath.EvalSymlinks(selfPath)
		if err != nil {
			fmt.Printf("Error getting self path: %v\n", err)
			return errorExitCode
		}
		spec.Mounts = append(spec.Mounts, specs.Mount{
			Destination: shimPath,
			Type:        "bind",
			Source:      selfPath,
			Options:     []string{"rbind", "ro"},
		})

		spec.Hooks = &specs.Hooks{}
		if gpuSupportEnabled := os.Getenv("_EXPERIMENTAL_DAGGER_GPU_SUPPORT"); gpuSupportEnabled != "" {
			spec.Hooks.Prestart = []specs.Hook{
				{
					Args: []string{
						"nvidia-container-runtime-hook",
						"prestart",
					},
					Path: "/usr/bin/nvidia-container-runtime-hook",
				},
			}
		}

		// update the args to specify the shim as the init process
		spec.Process.Args = append([]string{shimPath}, spec.Process.Args...)
	}

	execMetadata := new(buildkit.ContainerExecUncachedMetadata)
	for i, env := range spec.Process.Env {
		found, err := execMetadata.FromEnv(env)
		if err != nil {
			fmt.Printf("Error parsing env: %v\n", err)
			return errorExitCode
		}
		if found {
			// remove the ftp_proxy env var from being set in the container
			spec.Process.Env = append(spec.Process.Env[:i], spec.Process.Env[i+1:]...)
			break
		}
	}

	var searchDomains []string
	for _, parentClientID := range execMetadata.ParentClientIDs {
		searchDomains = append(searchDomains, network.ClientDomain(parentClientID))
	}
	if len(searchDomains) > 0 {
		spec.Process.Env = append(spec.Process.Env, "_DAGGER_PARENT_CLIENT_IDS="+strings.Join(execMetadata.ParentClientIDs, " "))
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
				panic(err)
			}

			if err := replaceSearch(newResolv, mnt.Source, searchDomains); err != nil {
				panic(err)
			}

			if err := newResolv.Close(); err != nil {
				panic(err)
			}

			spec.Mounts[i].Source = newResolvPath
		}
	}

	var gpuParams string
	var otelEndpoint string
	var otelProto string
	keepEnv := []string{}
	for _, env := range spec.Process.Env {
		switch {
		case strings.HasPrefix(env, "_DAGGER_ENABLE_NESTING="):
			// keep the env var; we use it at runtime
			keepEnv = append(keepEnv, env)

			// provide the server id to connect back to
			if execMetadata.ServerID == "" {
				fmt.Fprintln(os.Stderr, "missing server id")
				return errorExitCode
			}
			keepEnv = append(keepEnv, "_DAGGER_SERVER_ID="+execMetadata.ServerID)

			// mount buildkit sock since it's nesting
			spec.Mounts = append(spec.Mounts, specs.Mount{
				Destination: "/.runner.sock",
				Type:        "bind",
				Options:     []string{"rbind"},
				Source:      "/run/buildkit/buildkitd.sock",
			})
		case strings.HasPrefix(env, "_DAGGER_SERVER_ID="):
		case strings.HasPrefix(env, aliasPrefix):
			// NB: don't keep this env var, it's only for the bundling step
			// keepEnv = append(keepEnv, env)

			if err := appendHostAlias(hostsFilePath, env, searchDomains); err != nil {
				fmt.Fprintln(os.Stderr, "host alias:", err)
				return errorExitCode
			}
		case strings.HasPrefix(env, "_EXPERIMENTAL_DAGGER_GPU_PARAMS="):
			_, gpuParams, _ = strings.Cut(env, "=")

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

	if otelEndpoint != "" {
		if strings.HasPrefix(otelEndpoint, "/") {
			// Buildkit currently sets this to /dev/otel-grpc.sock which is not a valid
			// endpoint URL despite being set in an OTEL_* env var.
			otelEndpoint = "unix://" + otelEndpoint
		}
		spec.Process.Env = append(spec.Process.Env,
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

	if gpuParams != "" {
		spec.Process.Env = append(spec.Process.Env, fmt.Sprintf("NVIDIA_VISIBLE_DEVICES=%s", gpuParams))
	}

	// write the updated config
	configBytes, err = json.Marshal(spec)
	if err != nil {
		fmt.Printf("Error marshaling config.json: %v\n", err)
		return errorExitCode
	}
	if err := os.WriteFile(configPath, configBytes, 0o600); err != nil {
		fmt.Printf("Error writing config.json: %v\n", err)
		return errorExitCode
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
		fmt.Printf("Error execing runc: %v\n", err)
		return errorExitCode
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
	if _, found := internalEnv("_DAGGER_ENABLE_NESTING"); !found {
		// no nesting; run as normal
		return execProcess(cmd, true)
	}

	// setup a session and associated env vars for the container

	sessionToken, engineErr := uuid.NewRandom()
	if engineErr != nil {
		return fmt.Errorf("error generating session token: %w", engineErr)
	}

	l, engineErr := net.Listen("tcp", "127.0.0.1:0")
	if engineErr != nil {
		return fmt.Errorf("error listening on session socket: %w", engineErr)
	}
	sessionPort := l.Addr().(*net.TCPAddr).Port

	parentClientIDsVal, _ := internalEnv("_DAGGER_PARENT_CLIENT_IDS")

	clientParams := client.Params{
		SecretToken:     sessionToken.String(),
		RunnerHost:      "unix:///.runner.sock",
		ParentClientIDs: strings.Fields(parentClientIDsVal),
	}

	if _, ok := internalEnv("_DAGGER_ENABLE_NESTING_IN_SAME_SESSION"); ok {
		serverID, ok := internalEnv("_DAGGER_SERVER_ID")
		if !ok {
			return fmt.Errorf("missing _DAGGER_SERVER_ID")
		}
		clientParams.ServerID = serverID
	}

	moduleCallerDigest, ok := internalEnv("_DAGGER_MODULE_CALLER_DIGEST")
	if ok {
		clientParams.ModuleCallerDigest = digest.Digest(moduleCallerDigest)
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

// toggleONLCR implements a tricky dance whereby we disable onlcr on the host
// side when running a TTY and then re-enable it on the container side. This
// way simple CLIs like bash will start with a sane terminal (onlcr) and TUIs
// like vim are still able to turn it off.
//
// Disabling ONLCR and calling it a day is not really an option. It has to be
// on by default. But when it's on by default on the host side, the container
// side is unable to change it. But if it's only on in the container side, it
// can be disabled properly.
//
// For clarity: the creator of the PTY we're dealing with is Buildkit itself,
// in executor/runcexecutor/executor_linux.go. It would not necessarily make
// sense to upstream this fix because you really need the other half
// (re-enabling it in the container) for the change to be useful. Thankfully
// we're able to access the container's PTY from both the host side and the
// container side because the shim first runs as the OCI runtime and then
// re-execs itself as the container.
func toggleONLCR(enable bool) error {
	fd := os.Stdin.Fd()
	if !term.IsTerminal(int(fd)) {
		return nil
	}
	if enable {
		return console.SetONLCR(fd)
	} else {
		return console.ClearONLCR(fd)
	}
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
