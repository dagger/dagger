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
	timing := timingFile()

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

	if _, ok := os.LookupEnv("DAGGER_SESSION_TOKEN"); ok {
		if err := startSessionSubprocess(); err != nil {
			return err
		}
	}

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
		fullPath, err = exec.LookPath(fullPath)
		if errors.Is(err, exec.ErrDot) {
			// NOTE: backwards compat with dumb-init
			err = nil
		}
		if err != nil {
			return err
		}
	}
	child, err := os.StartProcess(fullPath, os.Args[1:], &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		Sys:   &sysProcAttr,
	})
	if err != nil {
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

	return nil
}

// timingFile returns the fd the engine passed for reporting this process's
// timing when profiling (see distconsts.InitTimingFDEnv), or nil. It removes the
// variable so the command doesn't inherit it, and keeps the fd from leaking
// into the command too.
func timingFile() *os.File {
	v, ok := os.LookupEnv(distconsts.InitTimingFDEnv)
	if !ok {
		return nil
	}
	os.Unsetenv(distconsts.InitTimingFDEnv)
	fd, err := strconv.Atoi(v)
	if err != nil || fd < 3 {
		return nil
	}
	unix.CloseOnExec(fd)
	return os.NewFile(uintptr(fd), "init-timing")
}

func monotonicNS() int64 {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		return 0
	}
	return ts.Nano()
}

func startSessionSubprocess() error {
	// create a pipe to synchronize with the child process on when the session has started
	// when the child closes the write end of the pipe, we know it has started (or died, which
	// will result in errors for the nested exec process on any use of a session attachable)
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}

	// start the session subprocess
	cmd := exec.Command(distconsts.InitSessionContainerPath)

	// forwarding our stdio ensures that a panic in the child process won't get hidden and any other logging works too
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.ExtraFiles = []*os.File{w}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	err = cmd.Start()
	if errors.Is(err, fs.ErrNotExist) {
		// Not a nested client, though the command's env names a session: the
		// engine only mounts the helper for nested clients. Run the command
		// without session attachables, as when the helper fails at once.
		r.Close()
		w.Close()
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to start session subprocess: %w", err)
	}

	// wait for the session attachables to be ready (or the child to die)

	// need to close our dup of the write end of the pipe
	if err := w.Close(); err != nil {
		return fmt.Errorf("failed to close pipe: %w", err)
	}

	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		io.Copy(io.Discard, r)
	}()
	// something really really wrong would have to happen for this to block indefinitely, but be
	// cautious anyways w/ an overly generous timeout
	select {
	case <-doneCh:
		return nil
	case <-time.After(5 * time.Minute):
		return fmt.Errorf("timed out waiting for session subprocess to start")
	}
}
