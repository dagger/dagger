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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/network"
	"github.com/dagger/dagger/router"
	"github.com/google/uuid"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/vito/progrock"
	"golang.org/x/sys/unix"
)

const (
	metaMountPath = "/.dagger_meta_mount"
	stdinPath     = metaMountPath + "/stdin"
	exitCodePath  = metaMountPath + "/exitCode"
	runcPath      = "/usr/local/bin/runc"
	shimPath      = "/_shim"
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
		return 1
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "check":
		if err := check(args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		return 1
	}
}

func check(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: check port/tcp [port/udp ...]")
	}

	host, ports := args[0], args[1:]

	for _, port := range ports {
		port, network, ok := strings.Cut(port, "/")
		if !ok {
			network = "tcp"
		}

		pollAddr := net.JoinHostPort(host, port)

		fmt.Fprintln(os.Stderr, "polling for port", pollAddr)

		reached, err := pollForPort(network, pollAddr)
		if err != nil {
			return fmt.Errorf("poll %s: %w", pollAddr, err)
		}

		fmt.Fprintln(os.Stderr, "port is up at", reached)
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

func shim() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <path> [<args>]\n", os.Args[0])
		return 1
	}

	name := os.Args[1]
	args := []string{}
	if len(os.Args) > 2 {
		args = os.Args[2:]
	}

	cmd := exec.Command(name, args...)

	if stdinFile, err := os.Open(stdinPath); err == nil {
		defer stdinFile.Close()
		cmd.Stdin = stdinFile
	} else {
		cmd.Stdin = nil
	}

	stdoutRedirect, found := internalEnv("_DAGGER_REDIRECT_STDOUT")
	if found {
		stdoutPath = stdoutRedirect
	}

	stderrRedirect, found := internalEnv("_DAGGER_REDIRECT_STDERR")
	if found {
		stderrPath = stderrRedirect
	}

	if _, found := internalEnv(core.DebugFailedExecEnv); found {
		// if we are being requested to just obtain the output of a previously failed exec,
		// do that and exit
		stdoutFile, err := os.Open(stdoutPath)
		if err == nil {
			stdoutFileSize, err := stdoutFile.Seek(0, io.SeekEnd)
			if err != nil {
				panic(err)
			}
			seekFromEnd := stdoutFileSize
			if seekFromEnd > core.MaxExecErrorOutputBytes {
				// copy the last MaxExecErrorOutputBytes bytes only
				seekFromEnd = core.MaxExecErrorOutputBytes

				// prepend a notice when truncating
				_, err = io.WriteString(os.Stdout, fmt.Sprintf(
					core.TruncationMessage,
					stdoutFileSize-seekFromEnd,
				))
				if err != nil {
					panic(err)
				}
			}
			_, err = stdoutFile.Seek(-int64(seekFromEnd), io.SeekEnd)
			if err != nil {
				panic(err)
			}
			_, err = io.Copy(os.Stdout, stdoutFile)
			if err != nil {
				panic(err)
			}
		} else if !os.IsNotExist(err) {
			panic(err)
		}

		stderrFile, err := os.Open(stderrPath)
		if err == nil {
			stderrFileSize, err := stderrFile.Seek(0, io.SeekEnd)
			if err != nil {
				panic(err)
			}
			seekFromEnd := stderrFileSize
			if seekFromEnd > core.MaxExecErrorOutputBytes {
				// copy the last MaxExecErrorOutputBytes bytes only
				seekFromEnd = core.MaxExecErrorOutputBytes

				// prepend a notice when truncating
				_, err = io.WriteString(os.Stderr, fmt.Sprintf(
					core.TruncationMessage,
					stderrFileSize-seekFromEnd,
				))
				if err != nil {
					panic(err)
				}
			}
			_, err = stderrFile.Seek(-int64(seekFromEnd), io.SeekEnd)
			if err != nil {
				panic(err)
			}
			_, err = io.Copy(os.Stderr, stderrFile)
			if err != nil {
				panic(err)
			}
		} else if !os.IsNotExist(err) {
			panic(err)
		}

		code, err := os.ReadFile(exitCodePath)
		if os.IsNotExist(err) {
			return -1
		}
		if err != nil {
			panic(err)
		}
		exitCode, err := strconv.Atoi(string(code))
		if err != nil {
			panic(err)
		}
		return exitCode
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

	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		panic(err)
	}
	defer stderrFile.Close()

	outWriter := io.MultiWriter(stdoutFile, os.Stdout)
	errWriter := io.MultiWriter(stderrFile, os.Stderr)

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

	exitCode := 0
	if err := runWithNesting(ctx, cmd); err != nil {
		exitCode = 1
		if exiterr, ok := err.(*exec.ExitError); ok {
			exitCode = exiterr.ExitCode()
		} else {
			panic(err)
		}
	}

	if err := os.WriteFile(exitCodePath, []byte(fmt.Sprintf("%d", exitCode)), 0o600); err != nil {
		panic(err)
	}

	return exitCode
}

func setupBundle() int {
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
		return 1
	}

	var spec specs.Spec
	if err := json.Unmarshal(configBytes, &spec); err != nil {
		fmt.Printf("Error parsing config.json: %v\n", err)
		return 1
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

	for _, env := range spec.Process.Env {
		// We're running an internal shim command, i.e. a service health check
		if strings.HasPrefix(env, "_DAGGER_INTERNAL_COMMAND=") {
			isDaggerExec = true
			break
		}
		// We're running a service, which needs to report its IP to the health
		// checker as part of the IP exchange dance
		if strings.HasPrefix(env, "_DAGGER_CHECKER_ADDR=") {
			isDaggerExec = true
			break
		}
	}

	if isDaggerExec {
		// mount this executable into the container so it can be invoked as the shim
		selfPath, err := os.Executable()
		if err != nil {
			fmt.Printf("Error getting self path: %v\n", err)
			return 1
		}
		selfPath, err = filepath.EvalSymlinks(selfPath)
		if err != nil {
			fmt.Printf("Error getting self path: %v\n", err)
			return 1
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

	var hostsFilePath string
	var searchDomains []string
	for _, mnt := range spec.Mounts {
		switch mnt.Destination {
		case "/etc/hosts":
			hostsFilePath = mnt.Source
		case "/etc/resolv.conf":
			// collect search domains; we need them this early so that we can use
			// them for resolving service aliases

			var err error
			searchDomains, err = collectSearchDomains(mnt.Source)
			if err != nil {
				fmt.Fprintln(os.Stderr, "collect search domains:", err)
				return 1
			}

			// propagate search domains to the child
			spec.Process.Env = append(spec.Process.Env,
				"_EXPERIMENTAL_DAGGER_SEARCH_DOMAIN="+strings.Join(searchDomains, " "),
			)
		}
	}

	keepEnv := []string{}
	for _, env := range spec.Process.Env {
		switch {
		case strings.HasPrefix(env, "_DAGGER_ENABLE_NESTING="):
			// keep the env var; we use it at runtime
			keepEnv = append(keepEnv, env)

			// mount buildkit sock since it's nesting
			spec.Mounts = append(spec.Mounts, specs.Mount{
				Destination: "/.runner.sock",
				Type:        "bind",
				Options:     []string{"rbind"},
				Source:      "/run/buildkit/buildkitd.sock",
			})
		case strings.HasPrefix(env, aliasPrefix):
			// NB: don't keep this env var, it's only for the bundling step
			// keepEnv = append(keepEnv, env)

			if err := appendHostAlias(hostsFilePath, env, searchDomains); err != nil {
				fmt.Fprintln(os.Stderr, "host alias:", err)
				return 1
			}
		default:
			keepEnv = append(keepEnv, env)
		}
	}
	spec.Process.Env = keepEnv

	// write the updated config
	configBytes, err = json.Marshal(spec)
	if err != nil {
		fmt.Printf("Error marshaling config.json: %v\n", err)
		return 1
	}
	if err := os.WriteFile(configPath, configBytes, 0o600); err != nil {
		fmt.Printf("Error writing config.json: %v\n", err)
		return 1
	}

	// Run the actual runc binary as a child process with the (possibly updated) config
	// Run it in a separate goroutine locked to the OS thread to ensure that Pdeathsig
	// is never sent incorrectly: https://github.com/golang/go/issues/27505
	exitCodeCh := make(chan int)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(exitCodeCh)
		// #nosec G204
		cmd := exec.Command(runcPath, os.Args[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Pdeathsig: syscall.SIGKILL,
		}

		sigCh := make(chan os.Signal, 32)
		signal.Notify(sigCh)
		if err := cmd.Start(); err != nil {
			fmt.Printf("Error starting runc: %v", err)
			exitCodeCh <- 1
			return
		}
		go func() {
			for sig := range sigCh {
				cmd.Process.Signal(sig)
			}
		}()
		if err := cmd.Wait(); err != nil {
			if exiterr, ok := err.(*exec.ExitError); ok {
				if waitStatus, ok := exiterr.Sys().(syscall.WaitStatus); ok {
					exitcode := waitStatus.ExitStatus()
					if exitcode < 0 {
						exitcode = 255 // 255 is "unknown exit code"
					}
					exitCodeCh <- exitcode
					return
				}
			}
			fmt.Printf("Error waiting for runc: %v", err)
			exitCodeCh <- 1
			return
		}
	}()
	return <-exitCodeCh
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

// nolint: unparam
func execRunc() int {
	args := []string{runcPath}
	args = append(args, os.Args[1:]...)
	if err := unix.Exec(runcPath, args, os.Environ()); err != nil {
		fmt.Printf("Error execing runc: %v\n", err)
		return 1
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
		if err := cmd.Start(); err != nil {
			return err
		}

		// Wait for stdout and stderr copy goroutines to finish:
		pipeWg.Wait()

		if err := cmd.Wait(); err != nil {
			return err
		}
		return nil
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

	engineConf := engine.Config{
		SessionToken: sessionToken.String(),
		RunnerHost:   "unix:///.runner.sock",
	}

	if searchDomains, found := os.LookupEnv("_EXPERIMENTAL_DAGGER_SEARCH_DOMAIN"); found {
		// NB: don't use internalEnv since it unsets the env var. we keep it around
		// to propagate to the command, to support running 'dagger do' in dagger,
		// though this is primarily motivated by tests
		engineConf.ExtraSearchDomains = strings.Fields(searchDomains)
	}

	if _, err := os.Stat("/.progrock.sock"); err == nil {
		progW, err := progrock.DialRPC(ctx, "unix:///.progrock.sock")
		if err != nil {
			return fmt.Errorf("error connecting to progrock: %w", err)
		}

		engineConf.ProgrockWriter = progW
	}

	// pass dagger session along to command
	os.Setenv("DAGGER_SESSION_PORT", strconv.Itoa(l.Addr().(*net.TCPAddr).Port))
	os.Setenv("DAGGER_SESSION_TOKEN", sessionToken.String())

	var cmdErr error
	engineErr = engine.Start(ctx, engineConf, func(ctx context.Context, r *router.Router) error {
		go http.Serve(l, r) //nolint:gosec
		cmdErr = cmd.Start()
		if cmdErr != nil {
			return cmdErr
		}
		pipeWg.Wait()
		cmdErr = cmd.Wait()
		if cmdErr != nil {
			return cmdErr
		}
		return nil
	})
	if cmdErr != nil {
		// propagate inner error with higher priority
		return cmdErr
	}
	if engineErr != nil {
		return fmt.Errorf("engine: %w", engineErr)
	}
	return nil
}

func collectSearchDomains(resolv string) ([]string, error) {
	src, err := os.Open(resolv)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	srcScan := bufio.NewScanner(src)

	daggerDomains := []string{}
	for srcScan.Scan() {
		if !strings.HasPrefix(srcScan.Text(), "search") {
			continue
		}

		domains := strings.Fields(srcScan.Text())[1:]
		for _, domain := range domains {
			if strings.HasSuffix(domain, network.DomainSuffix) {
				daggerDomains = append(daggerDomains, domain)
			}
		}
	}

	return daggerDomains, nil
}
