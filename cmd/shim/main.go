package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

const (
	metaMountPath = "/.dagger_meta_mount"
	stdinPath     = metaMountPath + "/stdin"
	exitCodePath  = metaMountPath + "/exitCode"
	runcPath      = "/usr/bin/buildkit-runc"
	shimPath      = "/_shim"
)

var (
	stdoutPath = metaMountPath + "/stdout"
	stderrPath = metaMountPath + "/stderr"
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
		// If we're being executed as `/_shim`, then we're inside the container and should shim
		// the user command.
		os.Exit(shim())
	} else {
		// Otherwise, we're being invoked directly by buildkitd and should setup the bundle.
		os.Exit(setupBundle())
	}
}

func shim() int {
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
	cmd.Env = os.Environ()

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

	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		panic(err)
	}
	defer stdoutFile.Close()
	cmd.Stdout = io.MultiWriter(stdoutFile, os.Stdout)

	stderrRedirect, found := internalEnv("_DAGGER_REDIRECT_STDERR")
	if found {
		stderrPath = stderrRedirect
	}

	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		panic(err)
	}
	defer stderrFile.Close()
	cmd.Stderr = io.MultiWriter(stderrFile, os.Stderr)

	exitCode := 0
	if err := cmd.Run(); err != nil {
		exitCode = 1
		if exiterr, ok := err.(*exec.ExitError); ok {
			exitCode = exiterr.ExitCode()
		}
	}

	if err := os.WriteFile(exitCodePath, []byte(fmt.Sprintf("%d", exitCode)), 0600); err != nil {
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

		spec, err = toggleNesting(spec)
		if err != nil {
			fmt.Printf("Error toggling nesting: %v\n", err)
			return 1
		}

		// update the args to specify the shim as the init process
		spec.Process.Args = append([]string{shimPath}, spec.Process.Args...)

		// write the updated config
		configBytes, err = json.Marshal(spec)
		if err != nil {
			fmt.Printf("Error marshaling config.json: %v\n", err)
			return 1
		}
		if err := os.WriteFile(configPath, configBytes, 0600); err != nil {
			fmt.Printf("Error writing config.json: %v\n", err)
			return 1
		}
	}

	// Run the actual runc binary as a child process with the (possibly updated) config
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
		return 1
	}
	go func() {
		for sig := range sigCh {
			cmd.Process.Signal(sig)
		}
	}()
	if err := cmd.Wait(); err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			if exitcode, ok := exiterr.Sys().(syscall.WaitStatus); ok {
				return exitcode.ExitStatus()
			}
		}
		fmt.Printf("Error waiting for runc: %v", err)
		return 1
	}
	return 0
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

func toggleNesting(spec specs.Spec) (specs.Spec, error) {
	// Setup engine session and runner socket mounts if requested based on env vars
	var enableNesting bool
	var daggerHost *url.URL
	var daggerRunnerHost *url.URL
	var err error
	for i, env := range spec.Process.Env {
		switch {
		case strings.HasPrefix(env, "_DAGGER_ENABLE_NESTING="):
			enableNesting = true
			// hide it from the container
			spec.Process.Env = append(spec.Process.Env[:i], spec.Process.Env[i+1:]...)
		case strings.HasPrefix(env, "DAGGER_HOST="):
			daggerHost, err = url.Parse(strings.TrimPrefix(env, "DAGGER_HOST="))
			if err != nil {
				return specs.Spec{}, fmt.Errorf("error parsing DAGGER_HOST: %w", err)
			}
		case strings.HasPrefix(env, "DAGGER_RUNNER_HOST="):
			daggerRunnerHost, err = url.Parse(strings.TrimPrefix(env, "DAGGER_RUNNER_HOST="))
			if err != nil {
				return specs.Spec{}, fmt.Errorf("error parsing DAGGER_RUNNER_HOST: %w", err)
			}
		}
	}
	if enableNesting {
		if daggerHost != nil && daggerHost.Scheme == "bin" {
			engineBinDest := daggerHost.Host + daggerHost.Path
			engineBinSource := "/usr/bin/dagger-engine-session-linux-" + runtime.GOARCH
			spec.Mounts = append(spec.Mounts, specs.Mount{
				Destination: engineBinDest,
				Type:        "bind",
				Source:      engineBinSource,
				Options:     []string{"rbind", "ro"},
			})
		}
		if daggerRunnerHost != nil && daggerRunnerHost.Scheme == "unix" {
			runnerSocketDest := daggerRunnerHost.Host + daggerRunnerHost.Path
			runnerSocketSource := "/run/buildkit/buildkitd.sock"
			spec.Mounts = append(spec.Mounts, specs.Mount{
				Destination: runnerSocketDest,
				Type:        "bind",
				Source:      runnerSocketSource,
				Options:     []string{"rbind", "ro"},
			})
		}
	}
	return spec, nil
}
