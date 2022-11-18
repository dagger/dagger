package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

const (
	metaMountPath = "/.dagger_meta_mount"
	runcPath      = "/usr/bin/buildkit-runc"
)

/*
This binary implements our shim, which enables each Container.Exec to
capture/redirect stdio, capture the exit code, provide a dagger session,
etc.

It's implemented as a wrapper around runc and is invoked by buildkitd
as the oci worker. Modeling the shim as an oci runtime allows us to
customize containers with dagger specific logic+configuration without
having to fork buildkitd or override custom executors in its codebase.
*/
func main() {
	os.Exit(run())
}

func run() int {
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

	// Check to see if this is a dagger exec by seeing if there is
	// a dagger meta mount, which is where stdio and exit code files
	// are written. This mount is part of buildkit's cache, so anything
	// written to it will be cached alongside the actual container
	// rootfs+mounts.
	var metaMount *specs.Mount
	for i, mnt := range spec.Mounts {
		mnt := mnt
		if mnt.Destination == metaMountPath {
			if mnt.Type != "bind" {
				// NOTE: we could handle others but this is simpler for now and
				// it should currently always be a bind mount.
				fmt.Printf("Error: dagger meta mount must be a bind mount")
				return 1
			}
			// we found it, remove it from the actual container which
			// doesn't need it mounted
			metaMount = &mnt
			spec.Mounts = append(spec.Mounts[:i], spec.Mounts[i+1:]...)
			break
		}
	}

	var stdin io.Reader = os.Stdin
	var stdout io.Writer = os.Stdout
	var stderr io.Writer = os.Stderr
	if metaMount != nil {
		metaPath := metaMount.Source

		// If there's some stdin to provide, connect the file to the stdin of the process
		stdinPath := filepath.Join(metaPath, "stdin")
		if stdinFile, err := os.Open(stdinPath); err == nil {
			defer stdinFile.Close()
			stdin = stdinFile
		}

		// By default, stdout goes to both our output (and thus buildkit progress logs)
		// and also to the default stdout file in the meta mount. But if the user setup
		// a redirection, write to that instead of the meta mount.
		//
		// One slight complication: the redirect path is relative to the container's
		// root and may be in a sub-mount, which doesn't exist yet. We could solve this
		// with oci hooks but that is pretty complicated. Instead, we use the lazyOpenWriter
		// implementation, which delays opening the path for writing until data is actually
		// received (at which time the mount has been made).
		redirectStdoutPath, newEnv, found := internalEnv("_DAGGER_REDIRECT_STDOUT", spec.Process.Env)
		if found {
			spec.Process.Env = newEnv
			stdoutPath := filepath.Join(bundleDir, "rootfs", redirectStdoutPath)
			stdout = io.MultiWriter(&lazyOpenWriter{path: stdoutPath}, os.Stdout)
		} else {
			stdoutPath := filepath.Join(metaPath, "stdout")
			f, err := os.Create(stdoutPath)
			if err != nil {
				fmt.Printf("Error creating stdout file: %v\n", err)
				return 1
			}
			defer f.Close()
			stdout = io.MultiWriter(f, os.Stdout)
		}

		// Do the exact same thing as stdout for stderr
		redirectStderrPath, newEnv, found := internalEnv("_DAGGER_REDIRECT_STDERR", spec.Process.Env)
		if found {
			spec.Process.Env = newEnv
			stderrPath := filepath.Join(bundleDir, "rootfs", redirectStderrPath)
			stderr = io.MultiWriter(&lazyOpenWriter{path: stderrPath}, os.Stderr)
		} else {
			stderrPath := filepath.Join(metaPath, "stderr")
			f, err := os.Create(stderrPath)
			if err != nil {
				fmt.Printf("Error creating stderr file: %v\n", err)
				return 1
			}
			defer f.Close()
			stderr = io.MultiWriter(f, os.Stderr)
		}

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
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}

	// Forward any signals we receive to our runc child
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

	// capture the exit code so we can exit with it too
	exitCode := 0
	if err := cmd.Wait(); err != nil {
		exiterr, ok := err.(*exec.ExitError)
		if !ok {
			fmt.Printf("Error waiting for runc: %v", err)
			return 1
		}
		waitStatus, ok := exiterr.Sys().(syscall.WaitStatus)
		if !ok {
			fmt.Printf("Error getting exit status from runc: %v", err)
			return 1
		}
		exitCode = waitStatus.ExitStatus()
	}

	// if we are running a dagger exec, also write the exit code to
	// the meta mount
	if metaMount != nil {
		exitCodePath := filepath.Join(metaMount.Source, "exitCode")
		if err := os.WriteFile(exitCodePath, []byte(fmt.Sprintf("%d", exitCode)), 0600); err != nil {
			fmt.Printf("Error writing exit code: %v", err)
			return 1
		}
	}

	return exitCode
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

func internalEnv(name string, env []string) (string, []string, bool) {
	for i, e := range env {
		if strings.HasPrefix(e, name+"=") {
			return e[len(name)+1:], append(env[:i], env[i+1:]...), true
		}
	}
	return "", env, false
}

// lazyOpenWriter is an io.Writer that delays opening the file at the given path
// until data is actually received, after which it just writes to that file.
type lazyOpenWriter struct {
	path     string
	openOnce sync.Once
	file     *os.File
}

func (w *lazyOpenWriter) Write(p []byte) (n int, err error) {
	w.openOnce.Do(func() {
		w.file, err = os.Create(w.path)
	})
	if err != nil {
		return 0, err
	}
	return w.file.Write(p)
}
