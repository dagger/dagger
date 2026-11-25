package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/coreos/go-systemd/v22/activation"
	sddaemon "github.com/coreos/go-systemd/v22/daemon"
	"github.com/dagger/dagger/engine/server"
	"github.com/dagger/dagger/engine/slog"
	"github.com/gofrs/flock"
	"github.com/moby/sys/reexec"
	"golang.org/x/sys/unix"
)

const (
	engineChildCommand  = "dagger-engine-supervised"
	engineCacheResetEnv = "_DAGGER_ENGINE_CACHE_RESET"
	engineStatusFDEnv   = "_DAGGER_ENGINE_STATUS_FD"
	enginePanicTailSize = 256 * 1024
)

type engineStatus struct {
	Root   string `json:"root,omitempty"`
	Notify string `json:"notify,omitempty"`
}

// Only the supervised engine writes to this pipe. Keep it out of descendants.
var engineStatusPipe *os.File

func main() {
	// Dispatch after all package init functions, including engine identity and
	// Unix setup. A supervised child runs the normal engine entrypoint here.
	if reexec.Init() {
		return
	}
	runEngine()
}

func superviseEngineCommand() error {
	listeners := activation.Files(false)
	signals := make(chan os.Signal, 16)
	signal.Notify(signals, unix.SIGINT, unix.SIGTERM, unix.SIGQUIT, unix.SIGHUP, unix.SIGUSR1, unix.SIGUSR2)
	code, err := superviseEngine(func() *exec.Cmd {
		cmd := exec.Command(reexec.Self(), os.Args[1:]...)
		cmd.Args[0] = engineChildCommand
		cmd.ExtraFiles = listeners
		// Networking setup bind-mounts resolv.conf. Discard those mounts when
		// a child dies so retrying does not read and overwrite the same file.
		cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWNS}
		cmd.Env = append(os.Environ(), fmt.Sprintf("LISTEN_FDS=%d", len(listeners)))
		return cmd
	}, signals, os.Stdout, os.Stderr)
	signal.Stop(signals)
	for _, listener := range listeners {
		listener.Close()
	}
	if err != nil {
		return err
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

func runSupervisedEngine() {
	fd, err := strconv.Atoi(os.Getenv(engineStatusFDEnv))
	if err != nil || fd < 3 {
		fmt.Fprintln(os.Stderr, "dagger-engine: missing supervisor status pipe")
		os.Exit(1)
	}
	// Prevent mounts in this child from propagating back to its supervisor.
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		fmt.Fprintf(os.Stderr, "dagger-engine: make engine mounts private: %v\n", err)
		os.Exit(1)
	}
	engineStatusPipe = os.NewFile(uintptr(fd), "engine-status")
	for inheritedFD := 3; inheritedFD <= fd; inheritedFD++ {
		unix.CloseOnExec(inheritedFD)
	}
	// The supervisor validated and forwarded any socket-activation FDs.
	if os.Getenv("LISTEN_FDS") != "" {
		if err := os.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid())); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	defer engineStatusPipe.Close()
	runEngine()
}

func reportEngineStatus(status engineStatus) error {
	if engineStatusPipe == nil {
		// Also support callers of serveAPI outside the supervised entrypoint.
		notifyEngineStatus(status.Notify)
		return nil
	}
	return json.NewEncoder(engineStatusPipe).Encode(status)
}

func notifyEngineStatus(status string) {
	if status != "" && os.Getenv("NOTIFY_SOCKET") != "" {
		notified, err := sddaemon.SdNotify(false, status)
		slog.Debug("engine status notification", "status", status, "notified", notified, "error", err)
	}
}

// superviseEngine retries one startup failure whose panic stack identifies
// bbolt corruption. Running engines and unrelated failures keep their status.
func superviseEngine(newChild func() *exec.Cmd, signals <-chan os.Signal, stdout, stderr io.Writer) (int, error) {
	for attempt := 0; attempt < 2; attempt++ {
		cmd := newChild()
		if cmd.Env == nil {
			cmd.Env = os.Environ()
		}
		// Always include the panicking goroutine, and use exit 2 instead of a
		// core dump even if GOTRACEBACK=none/crash was configured externally.
		cmd.Env = append(cmd.Env, "GOTRACEBACK=single", fmt.Sprintf("%s=%d", engineCacheResetEnv, attempt))
		result, err := waitEngine(cmd, signals, stdout, stderr)
		if err != nil {
			return 1, err
		}
		if attempt != 0 || result.code != 2 || result.interrupted || result.ready || result.root == "" || !isBoltCorruptionPanic(result.panicLog) {
			return result.code, nil
		}
		// A stop request may have arrived alongside the crash. Do not reset
		// cache state or start another engine while shutting down.
		select {
		case <-signals:
			return result.code, nil
		default:
		}
		fmt.Fprintln(stderr, "dagger-engine: bbolt corruption during startup; resetting local cache and retrying once")
		if err := resetEngineCache(result.root); err != nil {
			return 1, err
		}
	}
	panic("unreachable")
}

type engineExit struct {
	code        int
	root        string
	ready       bool
	interrupted bool
	panicLog    []byte
}

func waitEngine(cmd *exec.Cmd, signals <-chan os.Signal, stdout, stderr io.Writer) (engineExit, error) {
	readStatus, writeStatus, err := os.Pipe()
	if err != nil {
		return engineExit{}, err
	}
	defer readStatus.Close()
	defer writeStatus.Close()
	statusFD := 3 + len(cmd.ExtraFiles)
	cmd.ExtraFiles = append(cmd.ExtraFiles, writeStatus)
	cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%d", engineStatusFDEnv, statusFD))
	cmd.Stdin, cmd.Stdout = os.Stdin, stdout
	var tail enginePanicTail
	cmd.Stderr = io.MultiWriter(stderr, &tail)
	cmd.WaitDelay = 5 * time.Second
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL

	// Pdeathsig is tied to the OS thread that starts the process. Keep that
	// thread alive until the engine has exited.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := cmd.Start(); err != nil {
		return engineExit{}, fmt.Errorf("start engine: %w", err)
	}
	writeStatus.Close()
	defer unix.Kill(-cmd.Process.Pid, unix.SIGKILL) // clean up remaining processes in the child's group

	events := make(chan engineStatus)
	go func() {
		defer close(events)
		decoder := json.NewDecoder(readStatus)
		for {
			var status engineStatus
			if err := decoder.Decode(&status); err != nil {
				return
			}
			events <- status
		}
	}()
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	var result engineExit
	applyStatus := func(status engineStatus) {
		if status.Root != "" {
			result.root = status.Root
		}
		if status.Notify == sddaemon.SdNotifyReady {
			result.ready = true
		}
		notifyEngineStatus(status.Notify)
	}
	for {
		select {
		case status, ok := <-events:
			if ok {
				applyStatus(status)
			} else {
				events = nil
			}
		case sig := <-signals:
			result.interrupted = true
			if err := cmd.Process.Signal(sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
				fmt.Fprintf(stderr, "dagger-engine: forward signal: %v\n", err)
			}
		case err := <-wait:
			// Drain status before deciding whether startup completed. The
			// child's death closes the pipe; it is not inherited by execs.
			if events != nil {
				for status := range events {
					applyStatus(status)
				}
			}
			result.panicLog = tail.Bytes()
			if err == nil {
				return result, nil
			}
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				return result, fmt.Errorf("wait for engine: %w", err)
			}
			result.code = exitErr.ExitCode()
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				result.code = 128 + int(status.Signal())
			}
			return result, nil
		}
	}
}

func resetEngineCache(root string) error {
	lock := flock.New(filepath.Join(root, "dagger-engine.lock"))
	defer lock.Close()
	locked, err := lock.TryLock()
	if err != nil {
		return fmt.Errorf("lock engine state for reset: %w", err)
	}
	if !locked {
		return errors.New("cannot reset local cache: engine state is locked")
	}
	return server.RemoveLocalCacheState(root)
}

// Match the runtime's panic header and the panicking goroutine's frames, not
// quoted logs or bbolt frames elsewhere in an all-goroutine dump.
func isBoltCorruptionPanic(log []byte) bool {
	pending, running := false, false
	for line := range strings.SplitSeq(string(log), "\n") {
		if strings.HasPrefix(line, "panic: ") {
			pending = strings.HasPrefix(line, "panic: assertion failed:") || strings.HasPrefix(line, "panic: freepages:")
			running = false
		} else if strings.HasPrefix(line, "goroutine ") {
			running = pending && strings.Contains(line, "[running]:")
			pending = false
		} else if running && (strings.HasPrefix(line, "go.etcd.io/bbolt.") || strings.HasPrefix(line, "go.etcd.io/bbolt/")) {
			return true
		}
	}
	return false
}

// Keep only a bounded tail without shifting or allocating on each log write.
type enginePanicTail struct {
	data   [enginePanicTailSize]byte
	offset int
	size   int
}

func (tail *enginePanicTail) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > len(tail.data) {
		p = p[len(p)-len(tail.data):]
	}
	copied := copy(tail.data[tail.offset:], p)
	copy(tail.data[:], p[copied:])
	tail.offset = (tail.offset + len(p)) % len(tail.data)
	tail.size = min(len(tail.data), tail.size+len(p))
	return n, nil
}

func (tail *enginePanicTail) Bytes() []byte {
	out := make([]byte, tail.size)
	start := (tail.offset - tail.size + len(tail.data)) % len(tail.data)
	n := copy(out, tail.data[start:])
	copy(out[n:], tail.data[:tail.offset])
	return out
}
