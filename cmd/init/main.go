//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/engine/distconsts"
)

func main() {
	if os.Args[0] != "/.init" {
		return
	}
	if err := mainInit(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func mainInit() error {
	startedNS := monotonicNS()
	timing := engineFile(distconsts.InitTimingFDEnv, "init-timing")
	helperStatus := engineFile(distconsts.SessionHelperStatusFDEnv, "session-helper-status")

	sigCh := make(chan os.Signal, 16)
	// Handle every signal other than a few exceptions noted at the end.
	// Importantly, by handling all these signals, the child process will start with
	// the default signal disposition for them after the exec, which is what we want.
	// https://man7.org/linux/man-pages/man7/signal.7.html
	signal.Notify(sigCh,
		syscall.SIGABRT,
		syscall.SIGALRM,
		syscall.SIGBUS,
		syscall.SIGCHLD,
		syscall.SIGCLD,
		syscall.SIGCONT,
		syscall.SIGFPE,
		syscall.SIGHUP,
		syscall.SIGILL,
		syscall.SIGINT,
		syscall.SIGIO,
		syscall.SIGIOT,
		syscall.SIGPIPE,
		syscall.SIGPOLL,
		syscall.SIGPROF,
		syscall.SIGPWR,
		syscall.SIGQUIT,
		syscall.SIGSEGV,
		syscall.SIGSTKFLT,
		syscall.SIGSYS,
		syscall.SIGTERM,
		syscall.SIGTRAP,
		syscall.SIGTSTP,
		syscall.SIGTTIN,
		syscall.SIGTTOU,
		syscall.SIGUNUSED,
		syscall.SIGUSR1,
		syscall.SIGUSR2,
		syscall.SIGVTALRM,
		syscall.SIGWINCH,
		syscall.SIGXCPU,
		syscall.SIGXFSZ,
		// explicitly not caught
		// syscall.SIGKILL, // cannot be caught
		// syscall.SIGSTOP, // cannot be caught
		// syscall.SIGURG, // go runtime uses this internally
	)

	// try to detach from the terminal, if there is one
	// if detach successful, remember to ignore first SIGHUP/SIGCONT later (they might not be sent immediately and shouldn't be forwarded to the child)

	_, err := unix.IoctlGetTermios(0, unix.TCGETS)
	haveTTY := err == nil

	sid, err := unix.Getsid(0)
	if err != nil {
		return err
	}
	pid := unix.Getpid()
	weAreSessionLeader := sid == pid

	var ignoreFirstHUP bool
	var ignoreFirstCONT bool
	if haveTTY && weAreSessionLeader {
		ignoreFirstHUP = true
		ignoreFirstCONT = true
		_, err = unix.IoctlRetInt(0, unix.TIOCNOTTY)
		if err != nil {
			return err
		}
	}

	// The session helper starts alongside the command: the engine waits for
	// its attachables when the command first calls Dagger, and this process
	// reports on helperStatus if the helper fails.
	var helper *sessionHelper
	if _, ok := os.LookupEnv("DAGGER_SESSION_TOKEN"); ok {
		helper, err = startSessionSubprocess()
		if err != nil {
			if helperStatus != nil {
				fmt.Fprintf(helperStatus, "start-failed %s\n", err)
			}
			return err
		}
	}
	// The helper is only needed while the command runs, so every exit from
	// here on stops it first. Don't leave it to the kernel's teardown of the
	// container: once this process exits, the kernel refuses new threads in
	// the container, which a helper still starting up can crash on.

	child, err := startCommand(haveTTY)
	if err != nil {
		helper.stop()
		return err
	}
	spawnedNS := monotonicNS()

	// handle signals until our child exits
	for sig := range sigCh {
		sigNum := sig.(syscall.Signal)

		var goToBed bool
		switch sigNum {
		case syscall.SIGHUP:
			if ignoreFirstHUP {
				ignoreFirstHUP = false
				continue
			}
		case syscall.SIGCONT:
			if ignoreFirstCONT {
				ignoreFirstCONT = false
				continue
			}

		case syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU:
			sigNum = syscall.SIGSTOP
			goToBed = true

		case syscall.SIGCHLD:
			// reap what we have sown (aka various zombie children)
			for {
				var ws syscall.WaitStatus
				deadPid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
				if err != nil || deadPid == 0 {
					break
				}
				if helper != nil && deadPid == helper.pid {
					helper.reaped = true
					if helperStatus != nil {
						status := ws.ExitStatus()
						if status == -1 {
							status = 128 + int(ws.Signal())
						}
						if out := helper.outputAfterExit(); out != "" {
							fmt.Fprintf(helperStatus, "exited %d %s\n", status, strconv.Quote(out))
						} else {
							fmt.Fprintf(helperStatus, "exited %d\n", status)
						}
					}
				}
				if deadPid == child.Pid {
					exitedNS := monotonicNS()
					// our child died, so we should too
					exitStatus := ws.ExitStatus()
					if exitStatus == -1 {
						exitStatus = 128 + int(ws.Signal())
					}

					// send SIGTERM to anyone left
					unix.Kill(-child.Pid, syscall.SIGTERM)

					if timing != nil {
						fmt.Fprintf(timing, "%d %d %d\n", startedNS, spawnedNS, exitedNS)
					}

					// goodbye
					helper.stop()
					os.Exit(exitStatus)
				}
			}

			continue
		}

		// forward the signal to the child's process group
		unix.Kill(-child.Pid, sigNum) // ignore error, best effort

		if goToBed {
			unix.Kill(pid, syscall.SIGSTOP)
		}
	}

	helper.stop()
	return nil
}

// startCommand starts the command in os.Args[1:] in a new session, with
// this process's stdio.
func startCommand(haveTTY bool) (*os.Process, error) {
	// run the child in a new session
	sysProcAttr := syscall.SysProcAttr{
		Setsid: true,
	}
	if haveTTY {
		sysProcAttr.Setctty = true
		sysProcAttr.Ctty = 0
	}

	// start the child process
	fullPath := os.Args[1]
	if filepath.Base(fullPath) == fullPath {
		// search for the executable in $PATH
		var err error
		fullPath, err = exec.LookPath(fullPath)
		if errors.Is(err, exec.ErrDot) {
			// NOTE: backwards compat with dumb-init
			err = nil
		}
		if err != nil {
			return nil, err
		}
	}
	return os.StartProcess(fullPath, os.Args[1:], &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		Sys:   &sysProcAttr,
	})
}

// engineFile returns the fd the engine passed for reporting to it, named in
// envName (see distconsts.InitTimingFDEnv and
// distconsts.SessionHelperStatusFDEnv), or nil. It removes the variable so the
// command doesn't inherit it, and keeps the fd from leaking into the command
// too.
func engineFile(envName, name string) *os.File {
	v, ok := os.LookupEnv(envName)
	if !ok {
		return nil
	}
	os.Unsetenv(envName)
	fd, err := strconv.Atoi(v)
	if err != nil || fd < 3 {
		return nil
	}
	unix.CloseOnExec(fd)
	return os.NewFile(uintptr(fd), name)
}

func monotonicNS() int64 {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		return 0
	}
	return ts.Nano()
}

// sessionHelperPath is where the engine mounts the session helper; tests
// point it at a stand-in.
var sessionHelperPath = distconsts.InitSessionContainerPath

// sessionHelper is the session subprocess /.init started.
type sessionHelper struct {
	pid    int
	reaped bool

	// output is the start of what the helper wrote to its stdout and stderr,
	// which go to a pipe drained into it rather than to the command's. It's
	// only reported if the helper exits while the command runs.
	output  headBuffer
	drained chan struct{}
}

// outputAfterExit returns the helper's output once it has exited, waiting
// briefly for the pipe to drain.
func (h *sessionHelper) outputAfterExit() string {
	select {
	case <-h.drained:
	case <-time.After(time.Second):
	}
	return h.output.String()
}

// headBuffer keeps the first headBufferSize bytes written to it and drops
// the rest.
type headBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const headBufferSize = 4096

func (b *headBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n := headBufferSize - len(b.buf); n > 0 {
		b.buf = append(b.buf, p[:min(n, len(p))]...)
	}
	return len(p), nil
}

func (b *headBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// stop kills the helper and reaps it, unless it has already exited. It's
// safe to call on a nil helper and more than once.
func (h *sessionHelper) stop() {
	if h == nil || h.reaped {
		return
	}
	unix.Kill(h.pid, unix.SIGKILL)
	var ws unix.WaitStatus
	for {
		if _, err := unix.Wait4(h.pid, &ws, 0, nil); err != unix.EINTR {
			break
		}
	}
	h.reaped = true
}

// startSessionSubprocess starts the session helper, which connects this
// container's session attachables to the engine, or returns nil if the exec
// has no helper.
func startSessionSubprocess() (*sessionHelper, error) {
	cmd := exec.Command(sessionHelperPath)

	// The helper's output isn't the command's: keep it out of the exec's
	// stdout and stderr, and report it to the engine if the helper exits
	// while the command runs.
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to start session subprocess: %w", err)
	}
	cmd.Stdout = w
	cmd.Stderr = w

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	err = cmd.Start()
	w.Close()
	if err != nil {
		r.Close()
	}
	if errors.Is(err, fs.ErrNotExist) {
		// Not a nested client, though the command's env names a session: the
		// engine only mounts the helper for nested clients. Run the command
		// without session attachables, as when the helper fails at once.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to start session subprocess: %w", err)
	}
	h := &sessionHelper{pid: cmd.Process.Pid, drained: make(chan struct{})}
	go func() {
		defer close(h.drained)
		io.Copy(&h.output, r)
		r.Close()
	}()
	return h, nil
}
